package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event represents a file system event
type Event struct {
	Path      string
	Operation Operation
}

// Operation represents the type of file system operation
type Operation int

const (
	Create Operation = iota
	Write
	Remove
	Rename
	Chmod
)

func (op Operation) String() string {
	switch op {
	case Create:
		return "CREATE"
	case Write:
		return "WRITE"
	case Remove:
		return "REMOVE"
	case Rename:
		return "RENAME"
	case Chmod:
		return "CHMOD"
	default:
		return "UNKNOWN"
	}
}

// Watcher monitors file system changes in the vault
type Watcher struct {
	vaultPath  string
	interval   time.Duration
	events     chan Event
	errors     chan error
	done       chan struct{}
	fsWatcher  *fsnotify.Watcher
	usePolling bool
}

// New creates a new file watcher
func New(vaultPath string, interval time.Duration) (*Watcher, error) {
	w := &Watcher{
		vaultPath: vaultPath,
		interval:  interval,
		events:    make(chan Event, 100),
		errors:    make(chan error, 10),
		done:      make(chan struct{}),
	}

	// Try to use fsnotify first
	if err := w.initFsnotify(); err != nil {
		slog.Warn("Failed to initialize fsnotify, falling back to polling",
			"error", err,
			"interval", interval)
		w.usePolling = true
	}

	return w, nil
}

// Start begins monitoring the vault for changes
func (w *Watcher) Start(ctx context.Context) error {
	if w.usePolling {
		return w.startPolling(ctx)
	}
	return w.startFsnotify(ctx)
}

// Events returns the channel for file system events
func (w *Watcher) Events() <-chan Event {
	return w.events
}

// Errors returns the channel for watcher errors
func (w *Watcher) Errors() <-chan error {
	return w.errors
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.done)
	if w.fsWatcher != nil {
		w.fsWatcher.Close()
	}
}

// initFsnotify initializes fsnotify-based watching
func (w *Watcher) initFsnotify() error {
	var err error
	w.fsWatcher, err = fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating fsnotify watcher: %w", err)
	}

	// Add vault directory recursively
	err = filepath.Walk(w.vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and .git
		if info.IsDir() {
			name := filepath.Base(path)
			if name[0] == '.' && name != "." {
				return filepath.SkipDir
			}
		}

		if info.IsDir() {
			if err := w.fsWatcher.Add(path); err != nil {
				slog.Warn("Failed to watch directory", "path", path, "error", err)
			}
		}

		return nil
	})

	if err != nil {
		w.fsWatcher.Close()
		return fmt.Errorf("adding paths to fsnotify: %w", err)
	}

	return nil
}

// startFsnotify runs the fsnotify event loop
func (w *Watcher) startFsnotify(ctx context.Context) error {
	slog.Info("Starting fsnotify file watcher", "vault", w.vaultPath)

	go func() {
		defer close(w.events)
		defer close(w.errors)

		for {
			select {
			case <-ctx.Done():
				return
			case <-w.done:
				return
			case event, ok := <-w.fsWatcher.Events:
				if !ok {
					return
				}
				w.handleFsnotifyEvent(event)
			case err, ok := <-w.fsWatcher.Errors:
				if !ok {
					return
				}
				select {
				case w.errors <- err:
				case <-ctx.Done():
					return
				case <-w.done:
					return
				}
			}
		}
	}()

	return nil
}

// handleFsnotifyEvent converts fsnotify events to our Event type
func (w *Watcher) handleFsnotifyEvent(event fsnotify.Event) {
	// Filter out events we don't care about
	if !w.shouldProcessPath(event.Name) {
		return
	}

	var op Operation
	switch {
	case event.Op&fsnotify.Create == fsnotify.Create:
		op = Create
		// If a new directory was created, watch it
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			if err := w.fsWatcher.Add(event.Name); err != nil {
				slog.Warn("Failed to watch new directory", "path", event.Name, "error", err)
			}
		}
	case event.Op&fsnotify.Write == fsnotify.Write:
		op = Write
	case event.Op&fsnotify.Remove == fsnotify.Remove:
		op = Remove
	case event.Op&fsnotify.Rename == fsnotify.Rename:
		op = Rename
	case event.Op&fsnotify.Chmod == fsnotify.Chmod:
		op = Chmod
	default:
		return // Unknown operation
	}

	select {
	case w.events <- Event{Path: event.Name, Operation: op}:
	case <-w.done:
	}
}

// startPolling runs the polling-based watcher
func (w *Watcher) startPolling(ctx context.Context) error {
	slog.Info("Starting polling file watcher",
		"vault", w.vaultPath,
		"interval", w.interval)

	// TODO: Implement polling-based file watching
	// For now, just log that we would be polling
	go func() {
		defer close(w.events)
		defer close(w.errors)

		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-w.done:
				return
			case <-ticker.C:
				// TODO: Scan vault directory and detect changes
				// This would involve comparing file modification times
				// and checksums against the last known state
				slog.Debug("Polling vault for changes")
			}
		}
	}()

	return nil
}

// shouldProcessPath determines if we should process events for this path
func (w *Watcher) shouldProcessPath(path string) bool {
	name := filepath.Base(path)

	// Skip hidden files and directories (except for our lock file)
	if name[0] == '.' && name != ".obsidian-hugo-sync.lock" {
		return false
	}

	// Only process markdown files and our lock file
	ext := filepath.Ext(path)
	return ext == ".md" || name == ".obsidian-hugo-sync.lock"
} 
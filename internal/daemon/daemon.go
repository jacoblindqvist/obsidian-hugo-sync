package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"obsidian-hugo-sync/internal/config"
	"obsidian-hugo-sync/internal/git"
	"obsidian-hugo-sync/internal/hugo"
	"obsidian-hugo-sync/internal/images"
	"obsidian-hugo-sync/internal/state"
	"obsidian-hugo-sync/internal/vault"
	"obsidian-hugo-sync/internal/watcher"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Daemon orchestrates the sync process between Obsidian vault and Hugo repository
type Daemon struct {
	config       *config.Config
	stateManager *state.Manager
	gitRepo      *git.Repository
	hugoGen      *hugo.Generator
	imageManager *images.Manager
	watcher      *watcher.Watcher
	
	// Internal state
	isRunning bool
	lastSync  time.Time
}

// New creates a new daemon instance
func New(cfg *config.Config) (*Daemon, error) {
	// Initialize state manager
	stateManager, err := state.NewManager(cfg.CacheDir, cfg.Vault)
	if err != nil {
		return nil, fmt.Errorf("creating state manager: %w", err)
	}

	// Initialize Git repository
	gitRepo, err := git.NewRepository(cfg.Repo, cfg.Branch, cfg.GitToken, cfg.DryRun)
	if err != nil {
		return nil, fmt.Errorf("initializing git repository: %w", err)
	}

	// Initialize Hugo generator
	hugoGen := hugo.NewGenerator(cfg.ContentDir, cfg.LinkFormat, cfg.UnpublishedLink)

	// Initialize image manager
	imageManager := images.NewManager(cfg.Vault, cfg.Repo, cfg.ContentDir, cfg.DryRun)

	// Initialize file watcher
	fileWatcher, err := watcher.New(cfg.Vault, cfg.Interval)
	if err != nil {
		return nil, fmt.Errorf("creating file watcher: %w", err)
	}

	return &Daemon{
		config:       cfg,
		stateManager: stateManager,
		gitRepo:      gitRepo,
		hugoGen:      hugoGen,
		imageManager: imageManager,
		watcher:      fileWatcher,
	}, nil
}

// Start begins the daemon operation
func (d *Daemon) Start(ctx context.Context) error {
	d.isRunning = true
	defer func() { d.isRunning = false }()

	slog.Info("Starting daemon", "vault", d.config.Vault, "repo", d.config.Repo)

	// Ensure Git branch exists
	if err := d.gitRepo.EnsureBranch(); err != nil {
		return fmt.Errorf("ensuring git branch: %w", err)
	}

	// Perform initial full sync
	if err := d.performFullSync(); err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	// Start file watcher
	if err := d.watcher.Start(ctx); err != nil {
		return fmt.Errorf("starting file watcher: %w", err)
	}

	// Main event loop
	return d.eventLoop(ctx)
}

// eventLoop handles file system events and periodic syncs
func (d *Daemon) eventLoop(ctx context.Context) error {
	// Periodic sync timer
	syncTicker := time.NewTicker(d.config.Interval)
	defer syncTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Daemon stopping")
			d.watcher.Stop()
			return nil

		case event := <-d.watcher.Events():
			if err := d.handleFileEvent(event); err != nil {
				slog.Error("Error handling file event", "event", event, "error", err)
			}

		case err := <-d.watcher.Errors():
			slog.Error("File watcher error", "error", err)

		case <-syncTicker.C:
			if err := d.performIncrementalSync(); err != nil {
				slog.Error("Incremental sync failed", "error", err)
			}
		}
	}
}

// handleFileEvent processes individual file system events
func (d *Daemon) handleFileEvent(event watcher.Event) error {
	slog.Debug("Processing file event", "path", event.Path, "operation", event.Operation)

	// Only process markdown files
	if filepath.Ext(event.Path) != ".md" {
		return nil
	}

	switch event.Operation {
	case watcher.Create, watcher.Write:
		_, err := d.processNote(event.Path)
		return err
	case watcher.Remove:
		return d.handleNoteRemoval(event.Path)
	case watcher.Rename:
		// Handle as removal of old path and creation of new path
		// The watcher should generate separate events for the new location
		return d.handleNoteRemoval(event.Path)
	}

	return nil
}

// performFullSync scans the entire vault and syncs all changes
func (d *Daemon) performFullSync() error {
	slog.Info("Performing full vault sync")
	startTime := time.Now()

	// Scan vault for all notes
	notePaths, err := vault.ScanVault(d.config.Vault)
	if err != nil {
		return fmt.Errorf("scanning vault: %w", err)
	}

	slog.Info("Found notes in vault", "count", len(notePaths))

	// Process each note
	var processed, published, errors int
	publishedNotes := make(map[string]*vault.Note)

	for _, notePath := range notePaths {
		note, err := d.processNote(notePath)
		if err != nil {
			slog.Error("Error processing note", "path", notePath, "error", err)
			errors++
			continue
		}

		processed++
		if note != nil && note.Published {
			publishedNotes[note.UID] = note
			published++
		}
	}

	// Update Hugo generator's slug map
	d.hugoGen.UpdateSlugMap(publishedNotes)

	// Process all published notes again for wikilink conversion
	if err := d.regeneratePublishedContent(publishedNotes); err != nil {
		return fmt.Errorf("regenerating published content: %w", err)
	}

	// Clean up unused images
	if err := d.cleanupImages(); err != nil {
		slog.Error("Error cleaning up images", "error", err)
	}

	// Commit and push changes
	if err := d.commitAndPush(); err != nil {
		return fmt.Errorf("committing changes: %w", err)
	}

	// Save state
	if err := d.stateManager.Save(); err != nil {
		slog.Error("Error saving state", "error", err)
	}

	d.lastSync = time.Now()
	duration := time.Since(startTime)

	slog.Info("Full sync completed",
		"duration", duration,
		"processed", processed,
		"published", published,
		"errors", errors)

	return nil
}

// performIncrementalSync checks for changes and syncs only modified files
func (d *Daemon) performIncrementalSync() error {
	slog.Debug("Performing incremental sync")

	// Check for any uncommitted changes in Git
	hasChanges, err := d.gitRepo.HasChanges()
	if err != nil {
		return fmt.Errorf("checking git changes: %w", err)
	}

	if hasChanges {
		if err := d.commitAndPush(); err != nil {
			return fmt.Errorf("committing pending changes: %w", err)
		}
	}

	return nil
}

// processNote parses and processes a single note
func (d *Daemon) processNote(notePath string) (*vault.Note, error) {
	note, err := vault.ParseNote(notePath)
	if err != nil {
		return nil, fmt.Errorf("parsing note: %w", err)
	}

	// Ensure note has UID
	uidChanged := note.EnsureUID()

	// Calculate content hash
	contentHash := state.CalculateContentHash(note.Raw)

	// Check if sync is needed
	if !d.stateManager.NeedsSync(note.UID, notePath, note.ModTime, contentHash) && !uidChanged {
		return note, nil // No changes
	}

	// Update front-matter if needed
	var frontMatterChanged bool
	if d.config.AutoWeight {
		// Calculate weight (simplified for now)
		weight := d.calculateNoteWeight(notePath)
		if note.EnsureWeight(weight, d.config.AutoWeight) {
			frontMatterChanged = true
		}
	}

	// Write back to vault if front-matter changed
	if uidChanged || frontMatterChanged {
		if err := d.writeNoteToVault(note); err != nil {
			slog.Error("Error updating note front-matter", "path", notePath, "error", err)
		}
	}

	// Process based on publish status
	if note.Published {
		if err := d.publishNote(note); err != nil {
			return nil, fmt.Errorf("publishing note: %w", err)
		}
	} else {
		if err := d.unpublishNote(note); err != nil {
			return nil, fmt.Errorf("unpublishing note: %w", err)
		}
	}

	// Update state
	d.stateManager.SetNote(note.UID, &state.Note{
		SourcePath:   notePath,
		HugoPath:     d.calculateHugoPath(note),
		LastModified: note.ModTime,
		LastSync:     time.Now(),
		Published:    note.Published,
		ContentHash:  contentHash,
	})

	return note, nil
}

// publishNote converts and writes a note to the Hugo repository
func (d *Daemon) publishNote(note *vault.Note) error {
	// Calculate weight
	weight := d.calculateNoteWeight(note.Path)
	
	// Generate Hugo content
	hugoContent, err := d.hugoGen.GenerateContent(note, weight)
	if err != nil {
		return fmt.Errorf("generating hugo content: %w", err)
	}

	// Write to Hugo repository
	if err := d.gitRepo.WriteFile(hugoContent.Path, hugoContent.Serialize()); err != nil {
		return fmt.Errorf("writing hugo file: %w", err)
	}

	// Process images
	if err := d.processNoteImages(note); err != nil {
		slog.Error("Error processing images", "note", note.Path, "error", err)
	}

	// Ensure section _index.md exists
	if err := d.ensureSectionIndex(hugoContent.Path); err != nil {
		slog.Error("Error ensuring section index", "path", hugoContent.Path, "error", err)
	}

	slog.Info("Published note", "note", note.Title, "path", hugoContent.Path)
	return nil
}

// unpublishNote removes a note from the Hugo repository
func (d *Daemon) unpublishNote(note *vault.Note) error {
	hugoPath := d.calculateHugoPath(note)
	
	// Remove from Hugo repository
	if err := d.gitRepo.DeleteFile(hugoPath); err != nil {
		return fmt.Errorf("deleting hugo file: %w", err)
	}

	// Remove image references
	d.removeNoteImageReferences(note)

	slog.Info("Unpublished note", "note", note.Title, "path", hugoPath)
	return nil
}

// handleNoteRemoval handles when a note is deleted from the vault
func (d *Daemon) handleNoteRemoval(notePath string) error {
	// Find note in state by path
	for uid, stateNote := range d.stateManager.GetAllNotes() {
		if stateNote.SourcePath == notePath {
			// Remove from Hugo if it was published
			if stateNote.Published {
				if err := d.gitRepo.DeleteFile(stateNote.HugoPath); err != nil {
					slog.Error("Error removing deleted note from Hugo", "path", stateNote.HugoPath, "error", err)
				}
			}
			
			// Remove from state
			d.stateManager.DeleteNote(uid)
			slog.Info("Removed deleted note", "path", notePath)
			break
		}
	}
	
	return nil
}

// Helper methods

func (d *Daemon) calculateHugoPath(note *vault.Note) string {
	// This is simplified - should use the Hugo generator's path calculation
	return filepath.Join(d.config.ContentDir, strings.TrimSuffix(filepath.Base(note.Path), ".md")+".md")
}

func (d *Daemon) calculateNoteWeight(notePath string) int {
	// Simplified weight calculation
	relPath, _ := filepath.Rel(d.config.Vault, notePath)
	depth := strings.Count(relPath, string(filepath.Separator))
	return 100 + (depth * 10)
}

func (d *Daemon) writeNoteToVault(note *vault.Note) error {
	content, err := note.SerializeContent()
	if err != nil {
		return err
	}
	
	if d.config.DryRun {
		slog.Info("DRY RUN: Would update note front-matter", "path", note.Path)
		return nil
	}
	
	return d.gitRepo.WriteFile(note.Path, string(content))
}

func (d *Daemon) processNoteImages(note *vault.Note) error {
	imageRefs := note.ExtractImageReferences()
	
	for _, imgRef := range imageRefs {
		if _, err := d.imageManager.CopyImage(imgRef.Path, note.UID); err != nil {
			slog.Error("Error copying image", "image", imgRef.Path, "error", err)
			continue
		}
		
		// Track image reference
		d.stateManager.AddImageReference(imgRef.Path, note.UID)
	}
	
	return nil
}

func (d *Daemon) removeNoteImageReferences(note *vault.Note) {
	imageRefs := note.ExtractImageReferences()
	
	for _, imgRef := range imageRefs {
		d.stateManager.RemoveImageReference(imgRef.Path, note.UID)
	}
}

func (d *Daemon) ensureSectionIndex(notePath string) error {
	dir := filepath.Dir(notePath)
	if dir == d.config.ContentDir {
		return nil // Root level, no index needed
	}
	
	indexPath := filepath.Join(dir, "_index.md")
	
	// Check if index already exists
	status, err := d.gitRepo.GetStatus()
	if err != nil {
		return err
	}
	
	// If index doesn't exist in git, create it
	found := false
	for path := range status {
		if path == indexPath {
			found = true
			break
		}
	}
	
	if !found {
		weight := hugo.CalculateFolderWeight(dir)
		indexContent := d.hugoGen.GenerateIndexFile(dir, weight)
		
		if err := d.gitRepo.WriteFile(indexContent.Path, indexContent.Serialize()); err != nil {
			return fmt.Errorf("creating section index: %w", err)
		}
	}
	
	return nil
}

func (d *Daemon) regeneratePublishedContent(publishedNotes map[string]*vault.Note) error {
	// Sort notes for consistent processing order
	var notes []*vault.Note
	for _, note := range publishedNotes {
		notes = append(notes, note)
	}
	
	sort.Slice(notes, func(i, j int) bool {
		return notes[i].Path < notes[j].Path
	})
	
	// Regenerate content with updated wikilinks
	for _, note := range notes {
		weight := d.calculateNoteWeight(note.Path)
		hugoContent, err := d.hugoGen.GenerateContent(note, weight)
		if err != nil {
			return fmt.Errorf("regenerating content for %s: %w", note.Path, err)
		}
		
		if err := d.gitRepo.WriteFile(hugoContent.Path, hugoContent.Serialize()); err != nil {
			return fmt.Errorf("writing regenerated content: %w", err)
		}
	}
	
	return nil
}

func (d *Daemon) cleanupImages() error {
	allImages := d.stateManager.GetAllImages()
	return d.imageManager.CleanupUnusedImages(allImages)
}

func (d *Daemon) commitAndPush() error {
	// Create commit message
	status, err := d.gitRepo.GetStatus()
	if err != nil {
		return err
	}
	
	if len(status) == 0 {
		return nil // No changes
	}
	
	var added, modified, deleted int
	for _, statusCode := range status {
		switch statusCode {
		case 65: // Added
			added++
		case 77: // Modified  
			modified++
		case 68: // Deleted
			deleted++
		}
	}
	
	message := git.CreateCommitMessage(added, modified, deleted)
	
	if err := d.gitRepo.CommitChanges(message); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	
	if err := d.gitRepo.Push(); err != nil {
		return fmt.Errorf("pushing: %w", err)
	}
	
	return nil
} 
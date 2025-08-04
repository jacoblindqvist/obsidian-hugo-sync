package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"obsidian-hugo-sync/internal/config"
	"obsidian-hugo-sync/internal/daemon"
	"obsidian-hugo-sync/internal/logging"
	"obsidian-hugo-sync/internal/process"
	"os"
	"os/signal"
	"syscall"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	var (
		vault           = flag.String("vault", "", "Path to Obsidian vault (required)")
		repo            = flag.String("repo", "", "Path to Hugo site directory (required)")
		contentDir      = flag.String("content-dir", "content/docs", "Target directory for Hugo content (e.g., 'content', 'content/docs', 'content/blog')")
		autoWeight      = flag.Bool("auto-weight", true, "Auto-assign weights to notes and folders")
		linkFormat      = flag.String("link-format", "relref", "Link format: 'relref' or 'md'")
		unpublishedLink = flag.String("unpublished-link", "text", "How to handle unpublished links: 'text' or 'hash'")
		interval        = flag.String("interval", "30s", "Scan interval when fsnotify is unavailable")
		logLevel        = flag.String("log-level", "info", "Log level: debug, info, warn, error")
		dryRun          = flag.Bool("dry-run", false, "Preview changes without writing files")
		configFile      = flag.String("config", "", "Path to configuration file")
		showVersion     = flag.Bool("version", false, "Show version information")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Obsidian → Hugo Sync Daemon\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *showVersion {
		fmt.Printf("obsidian-hugo-sync %s (commit %s)\n", version, commit)
		os.Exit(0)
	}

	// Initialize logging first
	logger := logging.NewLogger(*logLevel)
	slog.SetDefault(logger)

	// Load and validate configuration
	cfg, err := config.Load(&config.Options{
		Vault:           *vault,
		Repo:            *repo,
		ContentDir:      *contentDir,
		AutoWeight:      *autoWeight,
		LinkFormat:      *linkFormat,
		UnpublishedLink: *unpublishedLink,
		Interval:        *interval,
		LogLevel:        *logLevel,
		DryRun:          *dryRun,
		ConfigFile:      *configFile,
	})
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	slog.Info("Starting Obsidian → Hugo Sync Daemon",
		"version", version,
		"vault", cfg.Vault,
		"hugo_dir", cfg.Repo,
		"dry_run", cfg.DryRun,
	)

	// Check for existing process and create lock file
	lockFile, err := process.AcquireLock(cfg.Vault)
	if err != nil {
		slog.Error("Failed to acquire process lock", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := process.ReleaseLock(lockFile); err != nil {
			slog.Error("Failed to release process lock", "error", err)
		}
	}()

	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Info("Received shutdown signal", "signal", sig)
		cancel()
	}()

	// Initialize and start the daemon
	daemon, err := daemon.New(cfg)
	if err != nil {
		slog.Error("Failed to create daemon", "error", err)
		os.Exit(1)
	}

	slog.Info("Daemon initialization complete")
	
	// Start the daemon
	if err := daemon.Start(ctx); err != nil {
		slog.Error("Daemon failed", "error", err)
		os.Exit(1)
	}
	
	slog.Info("Shutting down gracefully")
} 
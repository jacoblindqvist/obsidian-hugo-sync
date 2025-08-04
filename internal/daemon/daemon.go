package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"obsidian-hugo-sync/internal/config"
	"obsidian-hugo-sync/internal/hugo"
	"obsidian-hugo-sync/internal/images"
	"obsidian-hugo-sync/internal/state"
	"obsidian-hugo-sync/internal/vault"
	"obsidian-hugo-sync/internal/watcher"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Daemon orchestrates the sync process between Obsidian vault and Hugo repository
type Daemon struct {
	config       *config.Config
	stateManager *state.Manager
	hugoGen      *hugo.Generator
	imageManager *images.Manager
	watcher      *watcher.Watcher
	
	// Internal state
	isRunning       bool
	lastSync        time.Time
	needsLinkUpdate bool
}

// New creates a new daemon instance
func New(cfg *config.Config) (*Daemon, error) {
	// Initialize state manager
	stateManager, err := state.NewManager(cfg.CacheDir, cfg.Vault)
	if err != nil {
		return nil, fmt.Errorf("creating state manager: %w", err)
	}

	// No Git repository needed - just copy files to Hugo directory

	// Initialize Hugo generator
	hugoGen := hugo.NewGenerator(cfg.Vault, cfg.ContentDir, cfg.LinkFormat, cfg.UnpublishedLink)

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
		hugoGen:      hugoGen,
		imageManager: imageManager,
		watcher:      fileWatcher,
	}, nil
}

// Start begins the daemon operation
func (d *Daemon) Start(ctx context.Context) error {
	d.isRunning = true
	defer func() { d.isRunning = false }()

	slog.Info("Starting daemon", "vault", d.config.Vault, "hugo_dir", d.config.Repo)

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
		// Add small delay to let Obsidian finish writing
		time.Sleep(100 * time.Millisecond)
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

	// Repair missing _index.md files for existing content (fixes older versions)
	if err := d.repairMissingSectionIndexes(); err != nil {
		slog.Error("Error repairing section indexes", "error", err)
	}

	// Repair orphaned Hugo files and broken links from previous buggy versions
	// Pass the fresh publishedNotes to ensure we have current publish status
	if err := d.repairOrphanedHugoFiles(publishedNotes); err != nil {
		slog.Error("Error repairing orphaned Hugo files", "error", err)
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

	// Check if we need to regenerate content due to link updates (file renames)
	if d.needsLinkUpdate {
		slog.Info("Regenerating all published content due to file renames")
		
		// Get all published notes
		publishedNotes := make(map[string]*vault.Note)
		for uid, stateNote := range d.stateManager.GetAllNotes() {
			if stateNote.Published {
				note, err := vault.ParseNote(stateNote.SourcePath)
				if err != nil {
					slog.Error("Error parsing note for link update", "path", stateNote.SourcePath, "error", err)
					continue
				}
				publishedNotes[uid] = note
			}
		}
		
		// Update slug map and regenerate all published content
		d.hugoGen.UpdateSlugMap(publishedNotes)
		if err := d.regeneratePublishedContent(publishedNotes); err != nil {
			slog.Error("Error regenerating published content", "error", err)
		} else {
			slog.Info("Successfully updated all links after file renames")
		}
		
		d.needsLinkUpdate = false
	}

	// Save state
	if err := d.stateManager.Save(); err != nil {
		slog.Error("Error saving state", "error", err)
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

	// Check if this is a file rename (path changed but UID exists)
	oldNote := d.stateManager.GetNote(note.UID)
	isRenamed := oldNote != nil && oldNote.SourcePath != notePath

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

	// Handle file rename cleanup
	if isRenamed && oldNote.Published {
		// Remove old Hugo file
		oldHugoPath := oldNote.HugoPath
		oldFullPath := filepath.Join(d.config.Repo, oldHugoPath)
		
		if _, err := os.Stat(oldFullPath); err == nil {
			if d.config.DryRun {
				slog.Info("DRY RUN: Would delete old Hugo file after rename", "old_path", oldHugoPath, "new_path", d.calculateHugoPath(note))
			} else {
				if err := os.Remove(oldFullPath); err != nil {
					slog.Error("Error removing old Hugo file after rename", "path", oldHugoPath, "error", err)
				} else {
					slog.Info("Removed old Hugo file after rename", "old_path", oldHugoPath, "new_path", d.calculateHugoPath(note))
					d.removeEmptyDirs(filepath.Dir(oldFullPath))
				}
			}
		}
		
		// Schedule regeneration of all published content to update links
		if note.Published {
			slog.Info("File renamed, will regenerate all published content to update links", "old_path", oldNote.SourcePath, "new_path", notePath)
			// Mark that we need to regenerate all content during next incremental sync
			d.needsLinkUpdate = true
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

	// Write to Hugo directory
	fullPath := filepath.Join(d.config.Repo, hugoContent.Path)
	if d.config.DryRun {
		slog.Info("DRY RUN: Would write Hugo file", "path", hugoContent.Path)
	} else {
		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
		if err := os.WriteFile(fullPath, []byte(hugoContent.Serialize()), 0644); err != nil {
			return fmt.Errorf("writing hugo file: %w", err)
		}
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
	fullPath := filepath.Join(d.config.Repo, hugoPath)
	
	// Remove from Hugo directory (only if it exists)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		// File doesn't exist, nothing to do
		slog.Debug("Hugo file doesn't exist, skipping deletion", "path", hugoPath)
	} else if d.config.DryRun {
		slog.Info("DRY RUN: Would delete Hugo file", "path", hugoPath)
	} else {
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("deleting hugo file: %w", err)
		}
		// Remove empty directories
		d.removeEmptyDirs(filepath.Dir(fullPath))
		slog.Info("Deleted Hugo file", "path", hugoPath)
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
				fullPath := filepath.Join(d.config.Repo, stateNote.HugoPath)
				if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
					slog.Error("Error removing deleted note from Hugo", "path", stateNote.HugoPath, "error", err)
				} else {
					d.removeEmptyDirs(filepath.Dir(fullPath))
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
	
	// Write directly to vault file system, NOT to git repo
	return os.WriteFile(note.Path, content, 0644)
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
	
	// Create index files for all directories in the path (excluding content root)
	return d.ensureAllSectionIndexes(dir)
}

// ensureAllSectionIndexes recursively creates _index.md files for all directories in a path
func (d *Daemon) ensureAllSectionIndexes(dir string) error {
	// Stop at content directory root
	if dir == d.config.ContentDir || dir == "." || dir == "/" {
		return nil
	}
	
	// Recursively ensure parent directories have indexes first
	parentDir := filepath.Dir(dir)
	if err := d.ensureAllSectionIndexes(parentDir); err != nil {
		return err
	}
	
	// Create index for current directory
	indexPath := filepath.Join(dir, "_index.md")
	fullIndexPath := filepath.Join(d.config.Repo, indexPath)
	
	// Check if index already exists
	if _, err := os.Stat(fullIndexPath); os.IsNotExist(err) {
		// Create index file
		weight := hugo.CalculateFolderWeight(dir)
		indexContent := d.hugoGen.GenerateIndexFile(dir, weight)
		
		if d.config.DryRun {
			slog.Info("DRY RUN: Would create section index", "path", indexContent.Path)
		} else {
			// Ensure directory exists
			if err := os.MkdirAll(filepath.Dir(fullIndexPath), 0755); err != nil {
				return fmt.Errorf("creating index directory: %w", err)
			}
			if err := os.WriteFile(fullIndexPath, []byte(indexContent.Serialize()), 0644); err != nil {
				return fmt.Errorf("creating section index: %w", err)
			}
			slog.Info("Created section index", "path", indexPath)
		}
	}
	
	return nil
}

// repairMissingSectionIndexes scans Hugo content and creates missing _index.md files
// This fixes installations that were created with older versions of the daemon
func (d *Daemon) repairMissingSectionIndexes() error {
	contentPath := filepath.Join(d.config.Repo, d.config.ContentDir)
	
	// Find all directories that contain .md files (but not _index.md files themselves)
	contentDirs := make(map[string]bool)
	
	err := filepath.Walk(contentPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		// Skip if this is the content root
		if path == contentPath {
			return nil
		}
		
		// If this is a .md file (but not _index.md), mark its directory as needing an index
		if !info.IsDir() && strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, "_index.md") {
			dir := filepath.Dir(path)
			contentDirs[dir] = true
		}
		
		return nil
	})
	
	if err != nil {
		return fmt.Errorf("scanning content directory: %w", err)
	}
	
	// Create missing _index.md files for all content directories
	created := 0
	for dir := range contentDirs {
		// Get relative path from repo root for ensureAllSectionIndexes
		relDir, err := filepath.Rel(d.config.Repo, dir)
		if err != nil {
			slog.Error("Error calculating relative path", "dir", dir, "error", err)
			continue
		}
		
		// Ensure all parent directories have indexes
		if err := d.ensureAllSectionIndexes(relDir); err != nil {
			slog.Error("Error creating section indexes", "dir", relDir, "error", err)
			continue
		}
		created++
	}
	
	if created > 0 {
		slog.Info("Repaired missing section indexes", "directories", len(contentDirs), "created", created)
	}
	
	return nil
}

// repairOrphanedHugoFiles scans Hugo content and removes orphaned files from previous buggy versions
// This fixes files left behind from renames, duplicates, and broken links
func (d *Daemon) repairOrphanedHugoFiles(publishedNotes map[string]*vault.Note) error {
	contentPath := filepath.Join(d.config.Repo, d.config.ContentDir)
	
	// Use fresh publishedNotes data (just parsed from vault) instead of potentially stale state
	// Also get all notes from state for notes that exist but aren't published
	allStateNotes := d.stateManager.GetAllNotes()
	
	// Build maps for current published notes (from fresh vault scan)
	currentlyPublished := make(map[string]string) // uid -> hugo_path
	for uid, note := range publishedNotes {
		hugoPath := d.calculateHugoPath(note)
		currentlyPublished[uid] = hugoPath
	}
	
	// Scan all Hugo files and check for orphans/duplicates
	orphanedFiles := make([]string, 0)
	duplicateFiles := make(map[string][]string) // uid -> []file_paths
	
	err := filepath.Walk(contentPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		// Skip directories and _index.md files
		if info.IsDir() || strings.HasSuffix(path, "_index.md") {
			return nil
		}
		
		// Only process .md files
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		
		// Extract noteUid from file front-matter
		uid, err := d.extractNoteUidFromHugoFile(path)
		if err != nil {
			slog.Error("Error reading Hugo file for repair", "path", path, "error", err)
			return nil // Continue scanning
		}
		
		if uid == "" {
			// File has no noteUid, might be manually created - leave it alone
			return nil
		}
		
		// Get relative path from repo root
		relPath, err := filepath.Rel(d.config.Repo, path)
		if err != nil {
			slog.Error("Error calculating relative path", "path", path, "error", err)
			return nil
		}
		
		// Check if this UID corresponds to a currently published note (from fresh vault scan)
		expectedHugoPath, isCurrentlyPublished := currentlyPublished[uid]
		
		if !isCurrentlyPublished {
			// Check if note exists in vault at all (might be unpublished)
			if _, existsInVault := allStateNotes[uid]; !existsInVault {
				// Truly orphaned file - note completely deleted from vault
				orphanedFiles = append(orphanedFiles, relPath)
			} else {
				// Note exists in vault but is unpublished - should remove Hugo file
				orphanedFiles = append(orphanedFiles, relPath)
			}
		} else {
			// Note is currently published - check if Hugo file is in correct location
			if expectedHugoPath != relPath {
				// Duplicate/wrong location - track for cleanup
				if duplicateFiles[uid] == nil {
					duplicateFiles[uid] = make([]string, 0)
				}
				duplicateFiles[uid] = append(duplicateFiles[uid], relPath)
			}
		}
		
		return nil
	})
	
	if err != nil {
		return fmt.Errorf("scanning Hugo content for repair: %w", err)
	}
	
	// Remove orphaned files
	removed := 0
	for _, orphanPath := range orphanedFiles {
		fullPath := filepath.Join(d.config.Repo, orphanPath)
		
		if d.config.DryRun {
			slog.Info("DRY RUN: Would remove orphaned Hugo file", "path", orphanPath)
		} else {
			if err := os.Remove(fullPath); err != nil {
				slog.Error("Error removing orphaned Hugo file", "path", orphanPath, "error", err)
			} else {
				slog.Info("Removed orphaned Hugo file", "path", orphanPath)
				d.removeEmptyDirs(filepath.Dir(fullPath))
				removed++
			}
		}
	}
	
	// Handle duplicates - keep the expected path, remove others
	for uid, paths := range duplicateFiles {
		expectedPath := currentlyPublished[uid]
		for _, wrongPath := range paths {
			if wrongPath != expectedPath {
				fullPath := filepath.Join(d.config.Repo, wrongPath)
				
				if d.config.DryRun {
					slog.Info("DRY RUN: Would remove duplicate Hugo file", "path", wrongPath, "correct_path", expectedPath, "uid", uid)
				} else {
					if err := os.Remove(fullPath); err != nil {
						slog.Error("Error removing duplicate Hugo file", "path", wrongPath, "error", err)
					} else {
						slog.Info("Removed duplicate Hugo file", "path", wrongPath, "correct_path", expectedPath, "uid", uid)
						d.removeEmptyDirs(filepath.Dir(fullPath))
						removed++
					}
				}
			}
		}
	}
	
	if removed > 0 || len(orphanedFiles) > 0 || len(duplicateFiles) > 0 {
		slog.Info("Repaired Hugo content", 
			"orphaned_files", len(orphanedFiles), 
			"duplicate_groups", len(duplicateFiles), 
			"files_removed", removed)
		
		// Force regeneration of all content to fix any remaining broken links
		d.needsLinkUpdate = true
	}
	
	return nil
}

// extractNoteUidFromHugoFile reads a Hugo file and extracts the noteUid from front-matter
func (d *Daemon) extractNoteUidFromHugoFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	
	content := string(data)
	
	// Check for front-matter
	if !strings.HasPrefix(content, "---\n") {
		return "", nil // No front-matter
	}
	
	// Find end of front-matter
	lines := strings.Split(content, "\n")
	endIndex := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIndex = i
			break
		}
	}
	
	if endIndex <= 0 {
		return "", nil // Malformed front-matter
	}
	
	// Parse front-matter to extract noteUid
	frontMatterContent := strings.Join(lines[1:endIndex], "\n")
	
	// Simple regex to extract noteUid (more robust than full YAML parsing)
	noteUidRegex := regexp.MustCompile(`(?m)^noteUid:\s*(.+)$`)
	matches := noteUidRegex.FindStringSubmatch(frontMatterContent)
	
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), nil
	}
	
	return "", nil
}

// removeEmptyDirs recursively removes empty directories
func (d *Daemon) removeEmptyDirs(dir string) {
	// Don't remove the Hugo repository root or content directory
	hugoContentDir := filepath.Join(d.config.Repo, d.config.ContentDir)
	if dir == d.config.Repo || dir == hugoContentDir {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return // Directory not empty or error reading
	}

	// Directory is empty, remove it
	if err := os.Remove(dir); err == nil {
		// Recursively check parent directory
		parent := filepath.Dir(dir)
		if parent != dir { // Avoid infinite loop
			d.removeEmptyDirs(parent)
		}
	}
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
		
		fullPath := filepath.Join(d.config.Repo, hugoContent.Path)
		if d.config.DryRun {
			slog.Info("DRY RUN: Would regenerate Hugo file", "path", hugoContent.Path)
		} else {
			// Ensure directory exists
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				return fmt.Errorf("creating directory for regenerated content: %w", err)
			}
			if err := os.WriteFile(fullPath, []byte(hugoContent.Serialize()), 0644); err != nil {
				return fmt.Errorf("writing regenerated content: %w", err)
			}
		}
	}
	
	return nil
}

func (d *Daemon) cleanupImages() error {
	allImages := d.stateManager.GetAllImages()
	return d.imageManager.CleanupUnusedImages(allImages)
}

// No longer needed - user handles Git operations manually 
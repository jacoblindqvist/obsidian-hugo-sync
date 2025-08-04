package images

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manager handles image copying and cleanup
type Manager struct {
	vaultPath   string
	hugoPath    string
	contentDir  string
	dryRun      bool
	gracePeriod time.Duration
}

// NewManager creates a new image manager
func NewManager(vaultPath, hugoPath, contentDir string, dryRun bool) *Manager {
	return &Manager{
		vaultPath:   vaultPath,
		hugoPath:    hugoPath,
		contentDir:  contentDir,
		dryRun:      dryRun,
		gracePeriod: 24 * time.Hour, // 24h grace period before cleanup
	}
}

// ImageInfo represents information about an image
type ImageInfo struct {
	VaultPath string    // Original path in vault
	HugoPath  string    // Target path in Hugo repo
	Size      int64     // File size in bytes
	ModTime   time.Time // Last modification time
}

// CopyImage copies an image from vault to Hugo repository
func (m *Manager) CopyImage(vaultImagePath, noteUID string) (*ImageInfo, error) {
	// Validate image format
	if !m.isSupportedFormat(vaultImagePath) {
		return nil, fmt.Errorf("unsupported image format: %s", filepath.Ext(vaultImagePath))
	}

	// Calculate Hugo path for the image
	hugoImagePath := m.calculateHugoImagePath(vaultImagePath)

	if m.dryRun {
		slog.Info("DRY RUN: Would copy image",
			"from", vaultImagePath,
			"to", hugoImagePath,
			"note", noteUID)
		
		// Return mock info for dry run
		return &ImageInfo{
			VaultPath: vaultImagePath,
			HugoPath:  hugoImagePath,
			Size:      0,
			ModTime:   time.Now(),
		}, nil
	}

	// Check if source exists
	srcPath := vaultImagePath
	if !filepath.IsAbs(srcPath) {
		srcPath = filepath.Join(m.vaultPath, vaultImagePath)
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("source image not found: %w", err)
	}

	// Calculate full destination path
	dstPath := filepath.Join(m.hugoPath, hugoImagePath)

	// Check if destination already exists and is up to date
	if dstInfo, err := os.Stat(dstPath); err == nil {
		if dstInfo.ModTime().Equal(srcInfo.ModTime()) && dstInfo.Size() == srcInfo.Size() {
			slog.Debug("Image already up to date", "path", hugoImagePath)
			return &ImageInfo{
				VaultPath: vaultImagePath,
				HugoPath:  hugoImagePath,
				Size:      srcInfo.Size(),
				ModTime:   srcInfo.ModTime(),
			}, nil
		}
	}

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return nil, fmt.Errorf("creating destination directory: %w", err)
	}

	// Copy the file
	if err := m.copyFile(srcPath, dstPath); err != nil {
		return nil, fmt.Errorf("copying image: %w", err)
	}

	// Preserve modification time
	if err := os.Chtimes(dstPath, srcInfo.ModTime(), srcInfo.ModTime()); err != nil {
		slog.Warn("Failed to preserve image modification time", "path", dstPath, "error", err)
	}

	slog.Info("Copied image", 
		"from", vaultImagePath,
		"to", hugoImagePath,
		"size", srcInfo.Size())

	return &ImageInfo{
		VaultPath: vaultImagePath,
		HugoPath:  hugoImagePath,
		Size:      srcInfo.Size(),
		ModTime:   srcInfo.ModTime(),
	}, nil
}

// CleanupUnusedImages removes images that are no longer referenced
func (m *Manager) CleanupUnusedImages(referencedImages map[string][]string) error {
	// Find all images in the Hugo repository
	var existingImages []string
	
	contentPath := filepath.Join(m.hugoPath, m.contentDir)
	err := filepath.Walk(contentPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && m.isSupportedFormat(path) {
			// Convert to relative path from Hugo repo root
			relPath, err := filepath.Rel(m.hugoPath, path)
			if err != nil {
				return err
			}
			existingImages = append(existingImages, relPath)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("scanning existing images: %w", err)
	}

	// Check each existing image for references
	var deletedCount int
	for _, imagePath := range existingImages {
		if refs := referencedImages[imagePath]; len(refs) == 0 {
			// No references found, check if grace period has passed
			fullPath := filepath.Join(m.hugoPath, imagePath)
			info, err := os.Stat(fullPath)
			if err != nil {
				continue // File might have been deleted already
			}

			// Check if file is old enough to delete (grace period)
			if time.Since(info.ModTime()) > m.gracePeriod {
				if err := m.deleteImage(imagePath); err != nil {
					slog.Warn("Failed to delete unused image", "path", imagePath, "error", err)
				} else {
					deletedCount++
				}
			} else {
				slog.Debug("Image in grace period, keeping", 
					"path", imagePath,
					"remaining", m.gracePeriod-time.Since(info.ModTime()))
			}
		}
	}

	if deletedCount > 0 {
		slog.Info("Cleaned up unused images", "count", deletedCount)
	}

	return nil
}

// deleteImage removes an image file and cleans up empty directories
func (m *Manager) deleteImage(imagePath string) error {
	fullPath := filepath.Join(m.hugoPath, imagePath)

	if m.dryRun {
		slog.Info("DRY RUN: Would delete image", "path", imagePath)
		return nil
	}

	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting image file: %w", err)
	}

	// Clean up empty directories
	dir := filepath.Dir(fullPath)
	m.removeEmptyDirs(dir)

	slog.Info("Deleted unused image", "path", imagePath)
	return nil
}

// calculateHugoImagePath converts a vault image path to Hugo path
func (m *Manager) calculateHugoImagePath(vaultImagePath string) string {
	// Remove vault root prefix if present
	relPath := vaultImagePath
	if filepath.IsAbs(vaultImagePath) {
		if rel, err := filepath.Rel(m.vaultPath, vaultImagePath); err == nil {
			relPath = rel
		}
	}

	// Build Hugo path
	return filepath.Join(m.contentDir, relPath)
}

// isSupportedFormat checks if the file extension is a supported image format
func (m *Manager) isSupportedFormat(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	supportedFormats := []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp"}
	
	for _, format := range supportedFormats {
		if ext == format {
			return true
		}
	}
	return false
}

// copyFile copies a file from src to dst
func (m *Manager) copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source file: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("creating destination file: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copying file content: %w", err)
	}

	return dstFile.Sync()
}

// removeEmptyDirs recursively removes empty directories
func (m *Manager) removeEmptyDirs(dir string) {
	// Don't remove the Hugo repository root or content directory
	hugoContentDir := filepath.Join(m.hugoPath, m.contentDir)
	if dir == m.hugoPath || dir == hugoContentDir {
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
			m.removeEmptyDirs(parent)
		}
	}
}

// GetImageStats returns statistics about images in the Hugo repository
func (m *Manager) GetImageStats() (*ImageStats, error) {
	stats := &ImageStats{
		Formats: make(map[string]int),
	}

	contentPath := filepath.Join(m.hugoPath, m.contentDir)
	err := filepath.Walk(contentPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && m.isSupportedFormat(path) {
			stats.TotalCount++
			stats.TotalSize += info.Size()

			ext := strings.ToLower(filepath.Ext(path))
			stats.Formats[ext]++

			if info.Size() > stats.LargestSize {
				stats.LargestSize = info.Size()
				stats.LargestPath = path
			}
		}

		return nil
	})

	return stats, err
}

// ImageStats contains statistics about images
type ImageStats struct {
	TotalCount  int            // Total number of images
	TotalSize   int64          // Total size in bytes
	Formats     map[string]int // Count by format
	LargestSize int64          // Size of largest image
	LargestPath string         // Path to largest image
} 
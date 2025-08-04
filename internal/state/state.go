package state

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	stateVersion = "1.0"
	stateFileName = "state.json"
)

// State represents the daemon's persistent state
type State struct {
	Version   string             `json:"version"`
	VaultHash string             `json:"vault_hash"`
	Notes     map[string]*Note   `json:"notes"`
	Images    map[string][]string `json:"images"` // image_path -> []note_uid
}

// Note represents the cached state of a note
type Note struct {
	SourcePath   string    `json:"source_path"`
	HugoPath     string    `json:"hugo_path"`
	LastModified time.Time `json:"last_modified"`
	LastSync     time.Time `json:"last_sync"`
	Published    bool      `json:"published"`
	ContentHash  string    `json:"content_hash"`
}

// Manager handles state persistence and change detection
type Manager struct {
	statePath string
	state     *State
}

// NewManager creates a new state manager
func NewManager(cacheDir, vaultPath string) (*Manager, error) {
	statePath := filepath.Join(cacheDir, stateFileName)
	
	// Calculate vault hash for validation
	vaultAbs, err := filepath.Abs(vaultPath)
	if err != nil {
		return nil, fmt.Errorf("getting absolute vault path: %w", err)
	}
	vaultHash := hashString(vaultAbs)

	manager := &Manager{
		statePath: statePath,
		state: &State{
			Version:   stateVersion,
			VaultHash: vaultHash,
			Notes:     make(map[string]*Note),
			Images:    make(map[string][]string),
		},
	}

	// Load existing state if available
	if err := manager.load(); err != nil {
		// If loading fails, we'll start with a fresh state
		// Log the error but don't fail initialization
		fmt.Printf("Warning: Could not load existing state: %v\n", err)
	}

	return manager, nil
}

// GetNote returns the cached state for a note by UID
func (m *Manager) GetNote(uid string) *Note {
	return m.state.Notes[uid]
}

// SetNote updates the cached state for a note
func (m *Manager) SetNote(uid string, note *Note) {
	if m.state.Notes == nil {
		m.state.Notes = make(map[string]*Note)
	}
	m.state.Notes[uid] = note
}

// DeleteNote removes a note from the cached state
func (m *Manager) DeleteNote(uid string) {
	delete(m.state.Notes, uid)
}

// GetAllNotes returns all cached notes
func (m *Manager) GetAllNotes() map[string]*Note {
	return m.state.Notes
}

// AddImageReference adds a note UID to an image's reference list
func (m *Manager) AddImageReference(imagePath, noteUID string) {
	if m.state.Images == nil {
		m.state.Images = make(map[string][]string)
	}
	
	refs := m.state.Images[imagePath]
	
	// Check if reference already exists
	for _, ref := range refs {
		if ref == noteUID {
			return // Already exists
		}
	}
	
	m.state.Images[imagePath] = append(refs, noteUID)
}

// RemoveImageReference removes a note UID from an image's reference list
func (m *Manager) RemoveImageReference(imagePath, noteUID string) {
	refs := m.state.Images[imagePath]
	for i, ref := range refs {
		if ref == noteUID {
			// Remove this reference
			m.state.Images[imagePath] = append(refs[:i], refs[i+1:]...)
			break
		}
	}
	
	// If no more references, remove the image entry
	if len(m.state.Images[imagePath]) == 0 {
		delete(m.state.Images, imagePath)
	}
}

// GetImageReferences returns all note UIDs referencing an image
func (m *Manager) GetImageReferences(imagePath string) []string {
	return m.state.Images[imagePath]
}

// GetAllImages returns all tracked images and their references
func (m *Manager) GetAllImages() map[string][]string {
	return m.state.Images
}

// NeedsSync determines if a note needs to be synced based on file modification time and content hash
func (m *Manager) NeedsSync(uid, filePath string, modTime time.Time, contentHash string) bool {
	note := m.GetNote(uid)
	if note == nil {
		return true // New note
	}

	// Check if file path changed (note was moved/renamed)
	if note.SourcePath != filePath {
		return true
	}

	// Check if content changed
	if note.ContentHash != contentHash {
		return true
	}

	// Check if file was modified after last sync
	if modTime.After(note.LastSync) {
		return true
	}

	return false
}

// Save persists the current state to disk
func (m *Manager) Save() error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	// Write to temporary file first for atomic operation
	tempPath := m.statePath + ".tmp"
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, m.statePath); err != nil {
		os.Remove(tempPath) // Clean up on error
		return fmt.Errorf("renaming state file: %w", err)
	}

	return nil
}

// load reads state from disk
func (m *Manager) load() error {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No existing state, start fresh
		}
		return fmt.Errorf("reading state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("unmarshaling state: %w", err)
	}

	// Validate state version
	if state.Version != stateVersion {
		return fmt.Errorf("state version mismatch: got %s, expected %s", state.Version, stateVersion)
	}

	// Validate vault hash
	if state.VaultHash != m.state.VaultHash {
		return fmt.Errorf("vault hash mismatch: state is for a different vault")
	}

	// Initialize maps if nil
	if state.Notes == nil {
		state.Notes = make(map[string]*Note)
	}
	if state.Images == nil {
		state.Images = make(map[string][]string)
	}

	m.state = &state
	return nil
}

// Reset clears all cached state (useful for full rescan)
func (m *Manager) Reset() {
	m.state.Notes = make(map[string]*Note)
	m.state.Images = make(map[string][]string)
}

// CalculateContentHash computes SHA256 hash of file content
func CalculateContentHash(content []byte) string {
	hash := sha256.Sum256(content)
	return fmt.Sprintf("sha256-%x", hash)
}

// hashString creates a simple hash of a string
func hashString(s string) string {
	hash := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", hash[:8]) // Use first 8 bytes for directory naming
} 
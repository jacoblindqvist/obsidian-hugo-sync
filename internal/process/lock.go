package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const lockFileName = ".obsidian-hugo-sync.lock"

// LockFile represents an acquired process lock
type LockFile struct {
	path string
	file *os.File
}

// AcquireLock creates a PID lock file for the given vault path
// Returns an error if another instance is already running
func AcquireLock(vaultPath string) (*LockFile, error) {
	lockPath := filepath.Join(vaultPath, lockFileName)

	// Check if lock file exists
	if _, err := os.Stat(lockPath); err == nil {
		// Lock file exists, check if process is still running
		if isProcessRunning(lockPath) {
			return nil, fmt.Errorf("another obsidian-hugo-sync instance is already running for vault %s", vaultPath)
		}

		// Stale lock file, remove it
		if err := os.Remove(lockPath); err != nil {
			return nil, fmt.Errorf("removing stale lock file: %w", err)
		}
	}

	// Create new lock file
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return nil, fmt.Errorf("creating lock file: %w", err)
	}

	// Write current PID to the file
	pid := os.Getpid()
	if _, err := file.WriteString(fmt.Sprintf("%d\n", pid)); err != nil {
		file.Close()
		os.Remove(lockPath)
		return nil, fmt.Errorf("writing PID to lock file: %w", err)
	}

	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(lockPath)
		return nil, fmt.Errorf("syncing lock file: %w", err)
	}

	return &LockFile{
		path: lockPath,
		file: file,
	}, nil
}

// ReleaseLock removes the PID lock file
func ReleaseLock(lock *LockFile) error {
	if lock == nil {
		return nil
	}

	if lock.file != nil {
		lock.file.Close()
	}

	if err := os.Remove(lock.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing lock file: %w", err)
	}

	return nil
}

// isProcessRunning checks if the process with PID in the lock file is still running
func isProcessRunning(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return false
	}

	// Check if process exists by sending signal 0
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix systems, we can check if the process is still running
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		// Process doesn't exist or we don't have permission to signal it
		return false
	}

	return true
}

// GetLockPath returns the lock file path for a given vault
func GetLockPath(vaultPath string) string {
	return filepath.Join(vaultPath, lockFileName)
} 
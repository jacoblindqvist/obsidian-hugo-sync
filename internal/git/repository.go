package git

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// Repository wraps git operations for the Hugo repository
type Repository struct {
	repo       *git.Repository
	repoPath   string
	branch     string
	auth       transport.AuthMethod
	dryRun     bool
}

// NewRepository creates a new Git repository wrapper
func NewRepository(repoPath, branch string, authToken string, dryRun bool) (*Repository, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("opening git repository: %w", err)
	}

	r := &Repository{
		repo:     repo,
		repoPath: repoPath,
		branch:   branch,
		dryRun:   dryRun,
	}

	// Set up authentication
	if err := r.setupAuth(authToken); err != nil {
		return nil, fmt.Errorf("setting up authentication: %w", err)
	}

	return r, nil
}

// setupAuth configures Git authentication
func (r *Repository) setupAuth(token string) error {
	// Try token-based auth first
	if token != "" {
		r.auth = &http.BasicAuth{
			Username: "token",
			Password: token,
		}
		return nil
	}

	// Try SSH key authentication
	sshPath := filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519")
	if _, err := os.Stat(sshPath); err == nil {
		sshAuth, err := ssh.NewPublicKeysFromFile("git", sshPath, "")
		if err == nil {
			r.auth = sshAuth
			return nil
		}
	}

	// Fall back to id_rsa
	sshPath = filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
	if _, err := os.Stat(sshPath); err == nil {
		sshAuth, err := ssh.NewPublicKeysFromFile("git", sshPath, "")
		if err == nil {
			r.auth = sshAuth
			return nil
		}
	}

	// No authentication configured - may work with system git config
	slog.Warn("No Git authentication configured, relying on system configuration")
	return nil
}

// EnsureBranch ensures the sync branch exists and switches to it
func (r *Repository) EnsureBranch() error {
	if r.dryRun {
		slog.Info("DRY RUN: Would ensure branch exists", "branch", r.branch)
		return nil
	}

	worktree, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	// Check if branch already exists
	branchRef := plumbing.NewBranchReferenceName(r.branch)
	_, err = r.repo.Reference(branchRef, true)
	
	if err != nil {
		// Branch doesn't exist, create it
		slog.Info("Creating new branch", "branch", r.branch)
		
		// Get current HEAD commit
		head, err := r.repo.Head()
		if err != nil {
			return fmt.Errorf("getting HEAD: %w", err)
		}

		// Create new branch
		ref := plumbing.NewHashReference(branchRef, head.Hash())
		if err := r.repo.Storer.SetReference(ref); err != nil {
			return fmt.Errorf("creating branch reference: %w", err)
		}
	}

	// Switch to the branch
	if err := worktree.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
	}); err != nil {
		return fmt.Errorf("checking out branch: %w", err)
	}

	slog.Info("Switched to branch", "branch", r.branch)
	return nil
}

// WriteFile writes content to a file in the repository
func (r *Repository) WriteFile(relativePath, content string) error {
	fullPath := filepath.Join(r.repoPath, relativePath)
	
	if r.dryRun {
		slog.Info("DRY RUN: Would write file", "path", relativePath, "size", len(content))
		return nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}

// DeleteFile removes a file from the repository
func (r *Repository) DeleteFile(relativePath string) error {
	fullPath := filepath.Join(r.repoPath, relativePath)
	
	if r.dryRun {
		slog.Info("DRY RUN: Would delete file", "path", relativePath)
		return nil
	}

	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting file: %w", err)
	}

	// Remove empty directories
	dir := filepath.Dir(fullPath)
	r.removeEmptyDirs(dir)

	return nil
}

// removeEmptyDirs recursively removes empty directories
func (r *Repository) removeEmptyDirs(dir string) {
	// Don't remove the repository root or content directory
	repoContentDir := filepath.Join(r.repoPath, "content")
	if dir == r.repoPath || dir == repoContentDir {
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
			r.removeEmptyDirs(parent)
		}
	}
}

// CommitChanges commits all changes in the repository
func (r *Repository) CommitChanges(message string) error {
	if r.dryRun {
		slog.Info("DRY RUN: Would commit changes", "message", message)
		return r.showDiff()
	}

	worktree, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	// Add all changes
	if err := worktree.AddGlob("."); err != nil {
		return fmt.Errorf("adding changes: %w", err)
	}

	// Check if there are any changes to commit
	status, err := worktree.Status()
	if err != nil {
		return fmt.Errorf("getting status: %w", err)
	}

	if status.IsClean() {
		slog.Info("No changes to commit")
		return nil
	}

	// Commit changes
	commit, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "obsidian-hugo-sync",
			Email: "obsidian-hugo-sync@automated",
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("committing changes: %w", err)
	}

	slog.Info("Committed changes", "commit", commit.String(), "message", message)
	return nil
}

// Push pushes the current branch to origin
func (r *Repository) Push() error {
	if r.dryRun {
		slog.Info("DRY RUN: Would push to origin", "branch", r.branch)
		return nil
	}

	// Push with retries
	var lastErr error
	retries := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	
	for attempt := 0; attempt <= len(retries); attempt++ {
		if attempt > 0 {
			slog.Warn("Retrying push", "attempt", attempt, "delay", retries[attempt-1])
			time.Sleep(retries[attempt-1])
		}

		err := r.repo.Push(&git.PushOptions{
			RemoteName: "origin",
			RefSpecs: []config.RefSpec{
				config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", r.branch, r.branch)),
			},
			Auth: r.auth,
		})

		if err == nil {
			slog.Info("Successfully pushed to origin", "branch", r.branch)
			return nil
		}

		lastErr = err
		
		// Don't retry certain errors
		if err == git.NoErrAlreadyUpToDate {
			slog.Info("Repository already up to date")
			return nil
		}
	}

	return fmt.Errorf("failed to push after retries: %w", lastErr)
}

// showDiff shows what changes would be made (for dry-run mode)
func (r *Repository) showDiff() error {
	worktree, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return fmt.Errorf("getting status: %w", err)
	}

	if status.IsClean() {
		slog.Info("No changes to show")
		return nil
	}

	slog.Info("Changes that would be committed:")
	for file, status := range status {
		var action string
		switch status.Staging {
		case git.Added:
			action = "added"
		case git.Modified:
			action = "modified"
		case git.Deleted:
			action = "deleted"
		default:
			if status.Worktree == git.Modified {
				action = "modified"
			} else if status.Worktree == git.Deleted {
				action = "deleted"
			} else {
				action = "added"
			}
		}
		slog.Info("", "action", action, "file", file)
	}

	return nil
}

// GetStatus returns the current repository status
func (r *Repository) GetStatus() (map[string]git.StatusCode, error) {
	worktree, err := r.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("getting worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return nil, fmt.Errorf("getting status: %w", err)
	}

	result := make(map[string]git.StatusCode)
	for file, fileStatus := range status {
		result[file] = fileStatus.Staging
		if fileStatus.Staging == git.Untracked {
			result[file] = fileStatus.Worktree
		}
	}

	return result, nil
}

// HasChanges returns true if there are uncommitted changes
func (r *Repository) HasChanges() (bool, error) {
	status, err := r.GetStatus()
	if err != nil {
		return false, err
	}
	return len(status) > 0, nil
}

// CreateCommitMessage creates a descriptive commit message based on changes
func CreateCommitMessage(added, modified, deleted int) string {
	var parts []string
	
	if added > 0 {
		parts = append(parts, fmt.Sprintf("added %d", added))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("updated %d", modified))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("deleted %d", deleted))
	}
	
	if len(parts) == 0 {
		return "sync: no changes"
	}
	
	message := "sync: " + strings.Join(parts, ", ")
	if len(parts) == 1 {
		if added > 0 || modified > 0 || deleted > 0 {
			message += " notes"
		}
	} else {
		message += " notes"
	}
	
	return message
} 
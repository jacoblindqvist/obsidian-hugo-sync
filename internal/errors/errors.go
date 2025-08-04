package errors

import (
	"fmt"
	"log/slog"
	"time"
)

// ErrorType represents different categories of errors
type ErrorType int

const (
	ErrorTypeConfig ErrorType = iota
	ErrorTypeVault
	ErrorTypeGit
	ErrorTypeHugo
	ErrorTypeImage
	ErrorTypeState
	ErrorTypeProcess
	ErrorTypeNetwork
	ErrorTypeFileSystem
	ErrorTypeUnknown
)

func (et ErrorType) String() string {
	switch et {
	case ErrorTypeConfig:
		return "Configuration"
	case ErrorTypeVault:
		return "Vault"
	case ErrorTypeGit:
		return "Git"
	case ErrorTypeHugo:
		return "Hugo"
	case ErrorTypeImage:
		return "Image"
	case ErrorTypeState:
		return "State"
	case ErrorTypeProcess:
		return "Process"
	case ErrorTypeNetwork:
		return "Network"
	case ErrorTypeFileSystem:
		return "FileSystem"
	default:
		return "Unknown"
	}
}

// DaemonError represents an error with context and recovery information
type DaemonError struct {
	Type        ErrorType
	Operation   string
	Err         error
	Recoverable bool
	UserMessage string
	Suggestions []string
	Context     map[string]interface{}
}

func (de *DaemonError) Error() string {
	return fmt.Sprintf("[%s] %s: %v", de.Type, de.Operation, de.Err)
}

func (de *DaemonError) Unwrap() error {
	return de.Err
}

// New creates a new DaemonError
func New(errType ErrorType, operation string, err error) *DaemonError {
	de := &DaemonError{
		Type:      errType,
		Operation: operation,
		Err:       err,
		Context:   make(map[string]interface{}),
	}
	
	de.setDefaults()
	return de
}

// setDefaults sets default values based on error type
func (de *DaemonError) setDefaults() {
	switch de.Type {
	case ErrorTypeConfig:
		de.Recoverable = false
		de.UserMessage = "Configuration error"
		de.Suggestions = []string{
			"Check your configuration file and command-line arguments",
			"Verify that all required paths exist and are accessible",
			"Run with --help to see available options",
		}
	case ErrorTypeVault:
		de.Recoverable = true
		de.UserMessage = "Vault processing error"
		de.Suggestions = []string{
			"Check that the vault path is correct and accessible",
			"Verify that markdown files have valid YAML front-matter",
			"Ensure the vault is not locked by another application",
		}
	case ErrorTypeGit:
		de.Recoverable = true
		de.UserMessage = "Git operation failed"
		de.Suggestions = []string{
			"Check your Git authentication credentials",
			"Verify repository permissions and network connectivity",
			"Test with: ssh -T git@github.com (for SSH) or check token validity",
		}
	case ErrorTypeHugo:
		de.Recoverable = true
		de.UserMessage = "Hugo content generation error"
		de.Suggestions = []string{
			"Check that the Hugo repository structure is valid",
			"Verify content directory path in configuration",
			"Ensure Hugo site is properly initialized",
		}
	case ErrorTypeImage:
		de.Recoverable = true
		de.UserMessage = "Image processing error"
		de.Suggestions = []string{
			"Check that image files exist and are accessible",
			"Verify image formats are supported (.png, .jpg, .gif, .svg, .webp)",
			"Ensure sufficient disk space for image copies",
		}
	case ErrorTypeState:
		de.Recoverable = true
		de.UserMessage = "State management error"
		de.Suggestions = []string{
			"State cache may be corrupted - it will be rebuilt automatically",
			"Check disk space and permissions in cache directory",
			"Consider running with --dry-run to test without state changes",
		}
	case ErrorTypeProcess:
		de.Recoverable = false
		de.UserMessage = "Process management error"
		de.Suggestions = []string{
			"Another instance may be running - check for lock files",
			"Verify that the daemon has proper permissions",
			"Try stopping any existing instances before restarting",
		}
	case ErrorTypeNetwork:
		de.Recoverable = true
		de.UserMessage = "Network error"
		de.Suggestions = []string{
			"Check internet connectivity",
			"Verify Git remote URL and credentials",
			"Consider running during off-peak hours for better connectivity",
		}
	case ErrorTypeFileSystem:
		de.Recoverable = true
		de.UserMessage = "File system error"
		de.Suggestions = []string{
			"Check file and directory permissions",
			"Verify sufficient disk space",
			"Ensure paths are not too long for the file system",
		}
	default:
		de.Recoverable = true
		de.UserMessage = "An unexpected error occurred"
		de.Suggestions = []string{
			"Check the logs for more details",
			"Try running with increased log level (--log-level debug)",
			"Consider filing an issue if the problem persists",
		}
	}
}

// WithContext adds context information to the error
func (de *DaemonError) WithContext(key string, value interface{}) *DaemonError {
	de.Context[key] = value
	return de
}

// WithUserMessage overrides the default user message
func (de *DaemonError) WithUserMessage(message string) *DaemonError {
	de.UserMessage = message
	return de
}

// WithSuggestions overrides the default suggestions
func (de *DaemonError) WithSuggestions(suggestions ...string) *DaemonError {
	de.Suggestions = suggestions
	return de
}

// SetRecoverable sets whether the error is recoverable
func (de *DaemonError) SetRecoverable(recoverable bool) *DaemonError {
	de.Recoverable = recoverable
	return de
}

// LogError logs the error with appropriate level and context
func (de *DaemonError) LogError() {
	logAttrs := []interface{}{
		"type", de.Type.String(),
		"operation", de.Operation,
		"recoverable", de.Recoverable,
		"error", de.Err,
	}
	
	// Add context to log
	for k, v := range de.Context {
		logAttrs = append(logAttrs, k, v)
	}
	
	if de.Recoverable {
		slog.Warn("Recoverable error occurred", logAttrs...)
	} else {
		slog.Error("Fatal error occurred", logAttrs...)
	}
}

// PrintUserError prints a user-friendly error message
func (de *DaemonError) PrintUserError() {
	fmt.Printf("\n❌ %s\n", de.UserMessage)
	fmt.Printf("Operation: %s\n", de.Operation)
	
	if de.Err != nil {
		fmt.Printf("Details: %v\n", de.Err)
	}
	
	if len(de.Suggestions) > 0 {
		fmt.Println("\n💡 Suggestions:")
		for i, suggestion := range de.Suggestions {
			fmt.Printf("  %d. %s\n", i+1, suggestion)
		}
	}
	
	if len(de.Context) > 0 {
		fmt.Println("\n🔍 Additional Information:")
		for k, v := range de.Context {
			fmt.Printf("  %s: %v\n", k, v)
		}
	}
	
	fmt.Println()
}

// RetryConfig defines retry behavior for recoverable errors
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Backoff     float64
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   time.Second,
		MaxDelay:    30 * time.Second,
		Backoff:     2.0,
	}
}

// Retry executes a function with exponential backoff on recoverable errors
func Retry(config *RetryConfig, operation string, fn func() error) error {
	var lastErr error
	
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		
		lastErr = err
		
		// Check if error is recoverable
		if daemonErr, ok := err.(*DaemonError); ok && !daemonErr.Recoverable {
			return err // Don't retry non-recoverable errors
		}
		
		if attempt < config.MaxAttempts {
			delay := time.Duration(float64(config.BaseDelay) * float64(attempt-1) * config.Backoff)
			if delay > config.MaxDelay {
				delay = config.MaxDelay
			}
			
			slog.Warn("Operation failed, retrying",
				"operation", operation,
				"attempt", attempt,
				"max_attempts", config.MaxAttempts,
				"delay", delay,
				"error", err)
			
			time.Sleep(delay)
		}
	}
	
	return fmt.Errorf("operation failed after %d attempts: %w", config.MaxAttempts, lastErr)
}

// WrapError wraps a standard error as a DaemonError
func WrapError(errType ErrorType, operation string, err error) *DaemonError {
	if err == nil {
		return nil
	}
	
	// If it's already a DaemonError, return it
	if daemonErr, ok := err.(*DaemonError); ok {
		return daemonErr
	}
	
	return New(errType, operation, err)
}

// IsRecoverable checks if an error is recoverable
func IsRecoverable(err error) bool {
	if daemonErr, ok := err.(*DaemonError); ok {
		return daemonErr.Recoverable
	}
	return true // Assume unknown errors are recoverable
}

// GetErrorType returns the error type if it's a DaemonError
func GetErrorType(err error) ErrorType {
	if daemonErr, ok := err.(*DaemonError); ok {
		return daemonErr.Type
	}
	return ErrorTypeUnknown
} 
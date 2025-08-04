package logging

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger creates a new structured logger with the specified level
func NewLogger(level string) *slog.Logger {
	logLevel := parseLogLevel(level)
	
	// Create a text handler for human-readable output
	opts := &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize timestamp format
			if a.Key == slog.TimeKey {
				return slog.String("time", a.Value.Time().Format("2006-01-02 15:04:05"))
			}
			return a
		},
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}

// parseLogLevel converts string log level to slog.Level
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithContext creates a logger with additional context fields
func WithContext(logger *slog.Logger, attrs ...slog.Attr) *slog.Logger {
	return logger.With(attrsToArgs(attrs)...)
}

// attrsToArgs converts slog.Attr slice to interface{} slice for With()
func attrsToArgs(attrs []slog.Attr) []interface{} {
	args := make([]interface{}, 0, len(attrs)*2)
	for _, attr := range attrs {
		args = append(args, attr.Key, attr.Value)
	}
	return args
} 
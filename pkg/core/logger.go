package core

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// LogLevel represents the severity level of a log message
type LogLevel int

const (
	// LevelDebug is for detailed debugging information
	LevelDebug LogLevel = iota
	// LevelInfo is for general informational messages
	LevelInfo
	// LevelWarn is for warning messages
	LevelWarn
	// LevelError is for error messages
	LevelError
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger is the interface for logging operations
type Logger interface {
	// Debug logs a debug message
	Debug(msg string, keyvals ...any)
	// Info logs an informational message
	Info(msg string, keyvals ...any)
	// Warn logs a warning message
	Warn(msg string, keyvals ...any)
	// Error logs an error message
	Error(msg string, keyvals ...any)
	// With returns a new logger with additional key-value pairs
	With(keyvals ...any) Logger
}

// defaultLogger is a simple thread-safe logger implementation
type defaultLogger struct {
	mu       sync.Mutex
	writer   io.Writer
	minLevel LogLevel
	prefix   string
	keyvals  []any
}

// NewLogger creates a new logger that writes to the given writer
func NewLogger(writer io.Writer, minLevel LogLevel) Logger {
	return &defaultLogger{
		writer:   writer,
		minLevel: minLevel,
	}
}

// NewStdLogger creates a new logger that writes to stdout
func NewStdLogger(minLevel LogLevel) Logger {
	return NewLogger(os.Stdout, minLevel)
}

// Debug logs a debug message
func (l *defaultLogger) Debug(msg string, keyvals ...any) {
	l.log(LevelDebug, msg, keyvals...)
}

// Info logs an informational message
func (l *defaultLogger) Info(msg string, keyvals ...any) {
	l.log(LevelInfo, msg, keyvals...)
}

// Warn logs a warning message
func (l *defaultLogger) Warn(msg string, keyvals ...any) {
	l.log(LevelWarn, msg, keyvals...)
}

// Error logs an error message
func (l *defaultLogger) Error(msg string, keyvals ...any) {
	l.log(LevelError, msg, keyvals...)
}

// With returns a new logger with additional key-value pairs
func (l *defaultLogger) With(keyvals ...any) Logger {
	newKeyvals := make([]any, 0, len(l.keyvals)+len(keyvals))
	newKeyvals = append(newKeyvals, l.keyvals...)
	newKeyvals = append(newKeyvals, keyvals...)
	return &defaultLogger{
		writer:   l.writer,
		minLevel: l.minLevel,
		prefix:   l.prefix,
		keyvals:  newKeyvals,
	}
}

// log formats and writes a log message
func (l *defaultLogger) log(level LogLevel, msg string, keyvals ...any) {
	if level < l.minLevel {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(l.writer, "%s [%s] %s", timestamp, level, l.prefix)

	// Add base keyvals
	if len(l.keyvals) > 0 {
		for i := 0; i < len(l.keyvals); i += 2 {
			if i+1 < len(l.keyvals) {
				fmt.Fprintf(l.writer, " %v=%v", l.keyvals[i], l.keyvals[i+1])
			}
		}
	}

	// Add message-specific keyvals
	for i := 0; i < len(keyvals); i += 2 {
		if i+1 < len(keyvals) {
			fmt.Fprintf(l.writer, " %v=%v", keyvals[i], keyvals[i+1])
		}
	}

	fmt.Fprintf(l.writer, ": %s\n", msg)
}

// nopLogger is a no-op logger that discards all log messages
type nopLogger struct{}

// Debug is a no-op
func (nopLogger) Debug(msg string, keyvals ...any) {}

// Info is a no-op
func (nopLogger) Info(msg string, keyvals ...any) {}

// Warn is a no-op
func (nopLogger) Warn(msg string, keyvals ...any) {}

// Error is a no-op
func (nopLogger) Error(msg string, keyvals ...any) {}

// With returns a new nopLogger
func (n nopLogger) With(keyvals ...any) Logger {
	return n
}

// NopLogger returns a logger that discards all messages
func NopLogger() Logger {
	return nopLogger{}
}

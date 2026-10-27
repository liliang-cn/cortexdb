package observability

import (
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
)

// Structured logs.
//
// The server's lines used to be log.Printf text, with the caller's own
// values (a tool name, a key id) written into them. A value holding a space
// or a newline then wrote fields, or whole lines, of its own. slog quotes
// any value that holds a space, '=', a quote or a control character, so a
// line has exactly the fields the code wrote; JSON does the same by
// construction.
//
//	CORTEXDB_LOG_FORMAT  text (logfmt, the default) or json
//	CORTEXDB_LOG_LEVEL   debug, info (the default), warn or error

// NewLogger returns a logger writing format ("text" or "json") at level.
func NewLogger(w io.Writer, format string, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(strings.TrimSpace(format), "json") {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// ParseLogLevel reads a level name; anything unknown is info.
func ParseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// SetupLogsFromEnv makes a logger from CORTEXDB_LOG_FORMAT and
// CORTEXDB_LOG_LEVEL on stderr and installs it as the default, which also
// routes the standard log package through it: a log.Printf anywhere in the
// process becomes a structured record at info level.
func SetupLogsFromEnv() *slog.Logger {
	logger := NewLogger(os.Stderr, os.Getenv("CORTEXDB_LOG_FORMAT"), ParseLogLevel(os.Getenv("CORTEXDB_LOG_LEVEL")))
	slog.SetDefault(logger)
	log.SetFlags(0)
	return logger
}

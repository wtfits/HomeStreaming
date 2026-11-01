// Package logging configures slog: human-readable text on stderr and JSON in a
// rotating file under <data>/logs.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Setup installs the default logger and returns a closer for the log file.
func Setup(dataDir, level string, toFile bool, stderr io.Writer) (*slog.Logger, func()) {
	lvl := ParseLevel(level)
	if stderr == nil {
		stderr = os.Stderr
	}
	handlers := []slog.Handler{slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: lvl})}
	closer := func() {}
	if toFile && dataDir != "" {
		lj := &lumberjack.Logger{
			Filename:   filepath.Join(dataDir, "logs", "homestreaming.log"),
			MaxSize:    20, // MB
			MaxBackups: 5,
		}
		handlers = append(handlers, slog.NewJSONHandler(lj, &slog.HandlerOptions{Level: lvl}))
		closer = func() { _ = lj.Close() }
	}
	logger := slog.New(fanout(handlers))
	slog.SetDefault(logger)
	return logger, closer
}

// ParseLevel maps a level name to slog.Level (default info).
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

var tokenRe = regexp.MustCompile(`(?i)(token=)[^&\s]+`)

// RedactURL hides query tokens in a URL or path for logging.
func RedactURL(s string) string { return tokenRe.ReplaceAllString(s, "${1}REDACTED") }

type multiHandler []slog.Handler

func fanout(hs []slog.Handler) slog.Handler {
	if len(hs) == 1 {
		return hs[0]
	}
	return multiHandler(hs)
}

func (m multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m {
		if h.Enabled(ctx, r.Level) {
			_ = h.Handle(ctx, r.Clone())
		}
	}
	return nil
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	out := make(multiHandler, len(m))
	for i, h := range m {
		out[i] = h.WithGroup(name)
	}
	return out
}

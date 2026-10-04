//go:build !dev

package ethol

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

const isDevBuild = false

// DefaultLogLevel returns the log level from the LOG_LEVEL env var.
// Accepted values: debug, info, warn, error. Defaults to info.
func DefaultLogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
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

func devLog(_ string, _ ...any) {}

func devLogHTTP(_, _ string, _ int, _ time.Duration, _ error) {}

func devLogTelegramCommand(_ int64, _, _ string, _ time.Duration, _ int, _ error) {}

//go:build !dev

package ethol

import (
	"log/slog"
	"time"
)

const isDevBuild = false

// DefaultLogLevel returns slog.LevelDebug if verbose is true, otherwise slog.LevelInfo.
func DefaultLogLevel(verbose bool) slog.Level {
	if verbose {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func devLog(_ string, _ ...any) {}

func devLogHTTP(_, _ string, _ int, _ time.Duration, _ error) {}

func devLogTelegramCommand(_ int64, _, _ string, _ time.Duration, _ int, _ error) {}

package ethol

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestDevLogFunctions(t *testing.T) {
	devLog("test message", "key", "val")
	devLogHTTP("GET", "https://ethol.pens.ac.id/api/test", 200, 10*time.Millisecond, nil)
	devLogHTTP("POST", "https://ethol.pens.ac.id/api/test", 500, 20*time.Millisecond, errors.New("network error"))
	devLogTelegramCommand(12345, "/status", "/status", 5*time.Millisecond, 50, nil)
	devLogTelegramCommand(12345, "/error", "/error", 5*time.Millisecond, 0, errors.New("send failed"))
}

func TestDevBuildFlag(t *testing.T) {
	t.Logf("isDevBuild: %v", isDevBuild)
	if isDevBuild {
		// Default (no env) should be debug in dev builds.
		t.Setenv("LOG_LEVEL", "")
		if lvl := DefaultLogLevel(); lvl != slog.LevelDebug {
			t.Fatalf("expected LevelDebug by default in dev build, got %v", lvl)
		}
		for _, tc := range []struct {
			env  string
			want slog.Level
		}{
			{"info", slog.LevelInfo},
			{"INFO", slog.LevelInfo},
			{"warn", slog.LevelWarn},
			{"warning", slog.LevelWarn},
			{"error", slog.LevelError},
			{"debug", slog.LevelDebug},
			{"bogus", slog.LevelDebug},
		} {
			t.Setenv("LOG_LEVEL", tc.env)
			if got := DefaultLogLevel(); got != tc.want {
				t.Errorf("dev build LOG_LEVEL=%q: got %v, want %v", tc.env, got, tc.want)
			}
		}
	} else {
		// Default (no env) should be info in release builds.
		t.Setenv("LOG_LEVEL", "")
		if lvl := DefaultLogLevel(); lvl != slog.LevelInfo {
			t.Fatalf("expected LevelInfo by default in release build, got %v", lvl)
		}
		for _, tc := range []struct {
			env  string
			want slog.Level
		}{
			{"debug", slog.LevelDebug},
			{"DEBUG", slog.LevelDebug},
			{"info", slog.LevelInfo},
			{"warn", slog.LevelWarn},
			{"warning", slog.LevelWarn},
			{"error", slog.LevelError},
			{"bogus", slog.LevelInfo},
		} {
			t.Setenv("LOG_LEVEL", tc.env)
			if got := DefaultLogLevel(); got != tc.want {
				t.Errorf("release build LOG_LEVEL=%q: got %v, want %v", tc.env, got, tc.want)
			}
		}
	}
}

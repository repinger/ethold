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
		if lvl := DefaultLogLevel(false); lvl != slog.LevelDebug {
			t.Fatalf("expected LevelDebug in dev build, got %v", lvl)
		}
	} else {
		if lvl := DefaultLogLevel(false); lvl != slog.LevelInfo {
			t.Fatalf("expected LevelInfo in release build, got %v", lvl)
		}
		if lvl := DefaultLogLevel(true); lvl != slog.LevelDebug {
			t.Fatalf("expected LevelDebug when verbose, got %v", lvl)
		}
	}
}

//go:build dev

package ethol

import (
	"log/slog"
	"time"
)

const isDevBuild = true

// DefaultLogLevel returns slog.LevelDebug in dev builds.
func DefaultLogLevel(_ bool) slog.Level {
	return slog.LevelDebug
}

func devLog(msg string, args ...any) {
	//nolint:sloglint // dynamic message forwarding in dev logger
	slog.Debug(msg, args...)
}

func devLogHTTP(method, rawURL string, status int, dur time.Duration, err error) {
	if err != nil {
		slog.Debug("dev: http request error", "method", method, "url", rawURL, "duration", dur, "error", err)
		return
	}
	slog.Debug("dev: http request", "method", method, "url", rawURL, "status", status, "duration", dur)
}

func devLogTelegramCommand(chatID int64, cmd, rawText string, dur time.Duration, replyLen int, err error) {
	if err != nil {
		slog.Debug("dev: telegram command failed", "chat_id", chatID, "cmd", cmd, "raw", rawText, "duration", dur, "error", err)
		return
	}
	slog.Debug("dev: telegram command handled", "chat_id", chatID, "cmd", cmd, "raw", rawText, "duration", dur, "reply_len", replyLen)
}

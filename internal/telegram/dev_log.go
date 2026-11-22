//go:build dev

package telegram

import (
	"log/slog"
	"time"
)

func devLog(msg string, args ...any) {
	//nolint:sloglint // dynamic message forwarding in dev logger
	slog.Debug(msg, args...)
}

func devLogTelegramCommand(chatID int64, cmd, rawText string, dur time.Duration, replyLen int, err error) {
	if err != nil {
		slog.Debug("dev: telegram command failed", "chat_id", chatID, "cmd", cmd, "raw", rawText, "duration", dur, "error", err)
		return
	}
	slog.Debug("dev: telegram command handled", "chat_id", chatID, "cmd", cmd, "raw", rawText, "duration", dur, "reply_len", replyLen)
}

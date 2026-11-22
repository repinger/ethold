//go:build dev

package daemon

import "log/slog"

func devLog(msg string, args ...any) {
	//nolint:sloglint // dynamic message forwarding in dev logger
	slog.Debug(msg, args...)
}

//go:build !dev

package telegram

import "time"

func devLog(_ string, _ ...any) {}

func devLogTelegramCommand(_ int64, _, _ string, _ time.Duration, _ int, _ error) {}

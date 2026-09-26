package ethol

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Username                string
	Password                string
	TelegramToken           string
	TelegramChatID          string
	TelegramCommandThreadID int64
	TelegramNotifThreadID   int64
	AutoPresence            bool
	StateRetentionDays      int
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func parseEnv(data []byte) map[string]string {
	env := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		env[key] = val
	}
	return env
}

func resolveValue(override, key string, fileEnv map[string]string) string {
	if override != "" {
		return override
	}
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fileEnv[key]
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

func resolveInt(override int, key string, fileEnv map[string]string, defaultVal int) int {
	if override != 0 {
		return override
	}
	val := resolveValue("", key, fileEnv)
	if val == "" {
		return defaultVal
	}
	d, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || d < 0 {
		return defaultVal
	}
	return d
}

func resolveInt64(override int64, key string, fileEnv map[string]string) int64 {
	if override != 0 {
		return override
	}
	if val := os.Getenv(key); val != "" {
		return parseInt64(val)
	}
	return parseInt64(fileEnv[key])
}

func LoadConfig(path string, overrides ...Config) (*Config, error) {
	env := make(map[string]string)
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		if err == nil {
			env = parseEnv(data)
		}
	}

	var override Config
	if len(overrides) > 0 {
		override = overrides[0]
	}

	cfg := &Config{
		Username:                resolveValue(override.Username, "ETHOL_EMAIL", env),
		Password:                resolveValue(override.Password, "ETHOL_PASSWORD", env),
		TelegramToken:           resolveValue(override.TelegramToken, "TELEGRAM_TOKEN", env),
		TelegramChatID:          resolveValue(override.TelegramChatID, "TELEGRAM_CHAT_ID", env),
		TelegramCommandThreadID: resolveInt64(override.TelegramCommandThreadID, "TELEGRAM_COMMAND_THREAD_ID", env),
		TelegramNotifThreadID:   resolveInt64(override.TelegramNotifThreadID, "TELEGRAM_NOTIF_THREAD_ID", env),
		AutoPresence:            override.AutoPresence || parseBool(resolveValue("", "ETHOL_AUTO_PRESENCE", env)),
		StateRetentionDays:      resolveInt(override.StateRetentionDays, "ETHOL_STATE_RETENTION_DAYS", env, DefaultRetentionDays),
	}

	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("ETHOL_EMAIL and ETHOL_PASSWORD are required in config")
	}

	return cfg, nil
}

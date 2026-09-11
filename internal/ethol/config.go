package ethol

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Username       string
	Password       string
	TelegramToken  string
	TelegramChatID string
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
		Username:       resolveValue(override.Username, "ETHOL_USERNAME", env),
		Password:       resolveValue(override.Password, "ETHOL_PASSWORD", env),
		TelegramToken:  resolveValue(override.TelegramToken, "TELEGRAM_TOKEN", env),
		TelegramChatID: resolveValue(override.TelegramChatID, "TELEGRAM_CHAT_ID", env),
	}

	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("ETHOL_USERNAME and ETHOL_PASSWORD are required in config")
	}

	return cfg, nil
}

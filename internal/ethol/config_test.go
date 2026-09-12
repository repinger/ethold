package ethol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvConfig(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	content := `
# Comment line
ETHOL_USERNAME=user@student.pens.ac.id
ETHOL_PASSWORD="secret_password"
TELEGRAM_TOKEN='123456:ABC-DEF'
export TELEGRAM_CHAT_ID=987654321
`
	if err := os.WriteFile(envPath, []byte(content), 0600); err != nil {
		t.Fatalf("write temp env: %v", err)
	}

	cfg, err := LoadConfig(envPath)
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}

	if cfg.Username != "user@student.pens.ac.id" {
		t.Errorf("expected username 'user@student.pens.ac.id', got '%s'", cfg.Username)
	}
	if cfg.Password != "secret_password" {
		t.Errorf("expected password 'secret_password', got '%s'", cfg.Password)
	}
	if cfg.TelegramToken != "123456:ABC-DEF" {
		t.Errorf("expected telegram_token '123456:ABC-DEF', got '%s'", cfg.TelegramToken)
	}
	if cfg.TelegramChatID != "987654321" {
		t.Errorf("expected telegram_chat_id '987654321', got '%s'", cfg.TelegramChatID)
	}
}

func TestLoadEnvConfigValidation(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	if err := os.WriteFile(envPath, []byte("TELEGRAM_TOKEN=123\n"), 0600); err != nil {
		t.Fatalf("write temp env: %v", err)
	}

	_, err := LoadConfig(envPath)
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("ETHOL_USERNAME", "env_user@student.pens.ac.id")
	t.Setenv("ETHOL_PASSWORD", "env_secret")
	t.Setenv("TELEGRAM_TOKEN", "env_token")
	t.Setenv("TELEGRAM_CHAT_ID", "112233")

	// Missing .env file should not fail if env vars provide required credentials
	cfg, err := LoadConfig("nonexistent.env")
	if err != nil {
		t.Fatalf("unexpected error when loading from env: %v", err)
	}

	if cfg.Username != "env_user@student.pens.ac.id" {
		t.Errorf("expected username 'env_user@student.pens.ac.id', got '%s'", cfg.Username)
	}
	if cfg.Password != "env_secret" {
		t.Errorf("expected password 'env_secret', got '%s'", cfg.Password)
	}
	if cfg.TelegramToken != "env_token" {
		t.Errorf("expected token 'env_token', got '%s'", cfg.TelegramToken)
	}
	if cfg.TelegramChatID != "112233" {
		t.Errorf("expected chat id '112233', got '%s'", cfg.TelegramChatID)
	}
}

func TestLoadConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := `
ETHOL_USERNAME=file_user
ETHOL_PASSWORD=file_pass
TELEGRAM_TOKEN=file_token
TELEGRAM_CHAT_ID=file_chat
`
	if err := os.WriteFile(envPath, []byte(content), 0600); err != nil {
		t.Fatalf("write temp env: %v", err)
	}

	// Env overrides file
	t.Setenv("ETHOL_PASSWORD", "env_pass")
	t.Setenv("TELEGRAM_TOKEN", "env_token")

	// Flag override overrides env and file
	flagOverride := Config{
		Username: "flag_user",
	}

	cfg, err := LoadConfig(envPath, flagOverride)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Username != "flag_user" {
		t.Errorf("expected username 'flag_user' (flag override), got '%s'", cfg.Username)
	}
	if cfg.Password != "env_pass" {
		t.Errorf("expected password 'env_pass' (env override), got '%s'", cfg.Password)
	}
	if cfg.TelegramToken != "env_token" {
		t.Errorf("expected token 'env_token' (env override), got '%s'", cfg.TelegramToken)
	}
	if cfg.TelegramChatID != "file_chat" {
		t.Errorf("expected chat id 'file_chat' (file fallback), got '%s'", cfg.TelegramChatID)
	}
}

func TestLoadConfigAutoPresence(t *testing.T) {
	cases := []struct {
		envVal   string
		expected bool
	}{
		{"", false},
		{"false", false},
		{"0", false},
		{"no", false},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"YES", true},
	}

	for _, tc := range cases {
		t.Run(tc.envVal, func(t *testing.T) {
			t.Setenv("ETHOL_USERNAME", "dummy_user")
			t.Setenv("ETHOL_PASSWORD", "dummy_pass")
			t.Setenv("ETHOL_AUTO_PRESENCE", tc.envVal)

			cfg, err := LoadConfig("")
			if err != nil {
				t.Fatalf("LoadConfig unexpected error: %v", err)
			}
			if cfg.AutoPresence != tc.expected {
				t.Errorf("ETHOL_AUTO_PRESENCE=%q: expected %v, got %v", tc.envVal, tc.expected, cfg.AutoPresence)
			}
		})
	}
}

func BenchmarkParseEnv(b *testing.B) {
	// ponytail: static 15-line env fixture, add file loader benchmark when env size grows
	raw := []byte(`# Configuration for ethold
ETHOL_USERNAME=3120600001
ETHOL_PASSWORD="secret_password_here"
ETHOL_BASE_URL='https://ethol.pens.ac.id'
ETHOL_CAS_URL=https://cas.pens.ac.id/cas

# Notification
ETHOL_TELEGRAM_BOT_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11
ETHOL_TELEGRAM_CHAT_ID="999888777"

# Intervals
ETHOL_POLL_INTERVAL=60s
ETHOL_AUTO_PRESENCE=true
export ETHOL_DEBUG=1
`)
	b.ResetTimer()
	for b.Loop() {
		_ = parseEnv(raw)
	}
}

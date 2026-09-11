package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethol-autopresence/internal/ethol"
)

func main() {
	configPath := flag.String("config", ".env", "Path to .env config file")
	statePath := flag.String("state", "attended_keys.json", "Path to state file")
	once := flag.Bool("once", false, "Run single scan pass and exit")
	concurrency := flag.Int("concurrency", 4, "Concurrent workers for course checking")
	verbose := flag.Bool("verbose", false, "Enable debug logging")
	username := flag.String("username", "", "ETHOL CAS username or NRP")
	password := flag.String("password", "", "ETHOL CAS password")
	telegramToken := flag.String("telegram-token", "", "Telegram bot token")
	telegramChatID := flag.String("telegram-chat-id", "", "Telegram chat ID")
	flag.Parse()

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	cfg, err := ethol.LoadConfig(*configPath, ethol.Config{
		Username:       *username,
		Password:       *password,
		TelegramToken:  *telegramToken,
		TelegramChatID: *telegramChatID,
	})
	if err != nil {
		slog.Error("Failed to load configuration", "path", *configPath, "error", err)
		os.Exit(1)
	}

	client, err := ethol.NewHTTPClient()
	if err != nil {
		slog.Error("Failed to create HTTP client", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	baseURL := "https://ethol.pens.ac.id"
	auth := ethol.NewAuthManager(client, baseURL, cfg.Username, cfg.Password)
	courses := ethol.NewCourseManager(client, baseURL, 10*time.Minute)
	academic := ethol.NewAcademicManager(client, baseURL, 10*time.Minute)
	notifier := ethol.NewTelegramNotifier(client, "", cfg.TelegramToken, cfg.TelegramChatID)

	var (
		state    *ethol.StateManager
		presence *ethol.PresenceEngine
	)
	if cfg.AutoPresence {
		state, err = ethol.NewStateManager(*statePath)
		if err != nil {
			slog.Error("Failed to initialize state manager", "path", *statePath, "error", err)
			os.Exit(1)
		}
		presence = ethol.NewPresenceEngine(client, baseURL)
	} else {
		slog.Info("Auto-presence disabled, running in academic-only mode")
	}

	// Initial authentication test
	if _, err := auth.Login(ctx); err != nil {
		slog.Error("Initial CAS SSO login failed", "error", err)
		os.Exit(1)
	}

	scanner := ethol.NewScanner(auth, courses, presence, academic, state, notifier, *concurrency)

	if cfg.TelegramToken != "" && cfg.TelegramChatID != "" {
		go notifier.StartCommandPoller(ctx, scanner.HandleTelegramCommand)
		go academic.StartNotificationPoller(ctx, 30*time.Second, auth.EnsureSession, func(ket string) {
			if cfg.AutoPresence {
				_ = notifier.SendMessage(ctx, fmt.Sprintf("🔔 <b>NOTIFIKASI ETHOL:</b>\n%s\n\n<i>Memicu auto-presensi seketika...</i>", html.EscapeString(ket)))
				_, _ = scanner.ScanOnce(ctx)
			} else {
				_ = notifier.SendMessage(ctx, fmt.Sprintf("🔔 <b>NOTIFIKASI ETHOL:</b>\n%s", html.EscapeString(ket)))
			}
		}, func(ket string) {
			_ = notifier.SendMessage(ctx, fmt.Sprintf("📝 <b>NOTIFIKASI TUGAS BARU:</b>\n%s", html.EscapeString(ket)))
		})
	}

	if *once {
		if !cfg.AutoPresence {
			slog.Info("Auto-presence is disabled, nothing to scan")
			return
		}
		slog.Info("Running in single-pass scan mode")
		attended, err := scanner.ScanOnce(ctx)
		if err != nil {
			slog.Error("Scan failed", "error", err)
			os.Exit(1)
		}
		slog.Info("Scan completed", "attended", attended)
		return
	}

	if !cfg.AutoPresence {
		slog.Info("Academic daemon running (auto-presence disabled)")
		<-ctx.Done()
		slog.Info("Daemon stopped")
		return
	}

	slog.Info("Starting auto-presence daemon", "concurrency", *concurrency)
	if err := scanner.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Daemon stopped with error: %v\n", err)
		os.Exit(1)
	}
}

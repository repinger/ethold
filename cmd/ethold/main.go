package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/repinger/ethold/internal/ethol"
)

var version = ""

func formatVersion(tag string, isExact bool, shortSHA string) string {
	if isExact && tag != "" {
		return tag
	}
	if tag != "" {
		if shortSHA != "" {
			return tag + "-dev-" + shortSHA
		}
		return tag + "-dev"
	}
	if shortSHA != "" {
		return "dev-" + shortSHA
	}
	return "dev"
}

func detectGitInfo() (tag string, isExact bool, shortSHA string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(ctx, "git", "describe", "--tags", "--exact-match").Output(); err == nil {
		t := strings.TrimSpace(string(out))
		if t != "" {
			return t, true, ""
		}
	}
	if out, err := exec.CommandContext(ctx, "git", "describe", "--tags", "--abbrev=0").Output(); err == nil {
		tag = strings.TrimSpace(string(out))
	}
	if out, err := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		shortSHA = strings.TrimSpace(string(out))
	}
	return tag, false, shortSHA
}

func detectBuildInfo() (tag string, shortSHA string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" && !strings.Contains(info.Main.Version, "-") {
		tag = info.Main.Version
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			shortSHA = s.Value[:7]
			break
		}
	}
	return tag, shortSHA
}

func getVersion() string {
	if version != "" {
		return version
	}
	tag, isExact, sha := detectGitInfo()
	if tag != "" || sha != "" {
		return formatVersion(tag, isExact, sha)
	}
	if t, sha := detectBuildInfo(); t != "" || sha != "" {
		return formatVersion(t, t != "", sha)
	}
	return "dev"
}

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", ".env", "Path to .env config file")
	statePath := flag.String("state", "attended_keys.json", "Path to state file")
	once := flag.Bool("once", false, "Run single scan pass and exit")
	concurrency := flag.Int("concurrency", 4, "Concurrent workers for course checking")
	verbose := flag.Bool("verbose", false, "Enable debug logging")
	showVersion := flag.Bool("version", false, "Print program version and exit")
	username := flag.String("username", "", "ETHOL CAS username or NRP")
	password := flag.String("password", "", "ETHOL CAS password")
	telegramToken := flag.String("telegram-token", "", "Telegram bot token")
	telegramChatID := flag.String("telegram-chat-id", "", "Telegram chat ID")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Version %s\n", getVersion())
		return nil
	}

	logLevel := ethol.DefaultLogLevel(*verbose)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))
	slog.Info("Starting ethold", "version", getVersion())

	cfg, err := ethol.LoadConfig(*configPath, ethol.Config{
		Username:       *username,
		Password:       *password,
		TelegramToken:  *telegramToken,
		TelegramChatID: *telegramChatID,
	})
	if err != nil {
		slog.Error("Failed to load configuration", "path", *configPath, "error", err)
		return err
	}

	client, err := ethol.NewHTTPClient()
	if err != nil {
		slog.Error("Failed to create HTTP client", "error", err)
		return err
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
			return err
		}
		presence = ethol.NewPresenceEngine(client, baseURL)
	} else {
		slog.Info("Auto-presence disabled, running in academic-only mode")
	}

	// Initial authentication test
	if _, err := auth.Login(ctx); err != nil {
		slog.Error("Initial CAS SSO login failed", "error", err)
		return err
	}

	scanner := ethol.NewScanner(auth, courses, presence, academic, state, notifier, *concurrency)

	if cfg.TelegramToken != "" && cfg.TelegramChatID != "" {
		go notifier.StartCommandPoller(ctx, scanner.HandleTelegramCommand)
		go academic.StartNotificationPoller(ctx, 30*time.Second, auth.EnsureSession, func(ket string) {
			if cfg.AutoPresence {
				_ = notifier.SendMessage(ctx, fmt.Sprintf("🔔 <b>NOTIFIKASI ETHOL:</b>\n%s\n\n<i>Memicu auto-presensi seketika...</i>", html.EscapeString(ket)))
				go func() {
					_, _, _ = scanner.TryScanOnce(ctx)
				}()
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
			return nil
		}
		slog.Info("Running in single-pass scan mode")
		attended, err := scanner.ScanOnce(ctx)
		if err != nil {
			slog.Error("Scan failed", "error", err)
			return err
		}
		slog.Info("Scan completed", "attended", attended)
		return nil
	}

	if !cfg.AutoPresence {
		slog.Info("Academic daemon running (auto-presence disabled)")
		<-ctx.Done()
		slog.Info("Daemon stopped")
		return nil
	}

	slog.Info("Starting auto-presence daemon", "concurrency", *concurrency)
	if err := scanner.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Daemon stopped with error: %v\n", err)
		return err
	}
	return nil
}

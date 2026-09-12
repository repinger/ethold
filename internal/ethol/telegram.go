package ethol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type chatRateLimiter struct {
	mu           sync.Mutex
	tokens       float64
	maxTokens    float64
	refillRate   float64
	lastRefill   time.Time
	lastWarnTime time.Time
}

func newChatRateLimiter(burst float64, refillPerSec float64) *chatRateLimiter {
	return &chatRateLimiter{
		tokens:     burst,
		maxTokens:  burst,
		refillRate: refillPerSec,
		lastRefill: time.Now(),
	}
}

func (rl *chatRateLimiter) Allow(now time.Time, warnCooldown time.Duration) (allowed bool, warnAllowed bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.lastRefill = now
	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}

	if rl.tokens >= 1.0 {
		rl.tokens -= 1.0
		return true, false
	}

	if rl.lastWarnTime.IsZero() || now.Sub(rl.lastWarnTime) >= warnCooldown {
		rl.lastWarnTime = now
		return false, true
	}
	return false, false
}

type TelegramNotifier struct {
	client        *http.Client
	pollClient    *http.Client
	baseURL       string
	token         string
	chatID        string
	chatIDInt     int64
	rateLimiter   *chatRateLimiter
	unauthMu      sync.Mutex
	lastUnauthLog time.Time
}

func NewTelegramNotifier(client *http.Client, baseURL, token, chatID string) *TelegramNotifier {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	pollClient := &http.Client{
		Timeout: 45 * time.Second,
	}
	if client != nil {
		pollClient.Transport = client.Transport
	}
	cid, _ := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64)
	return &TelegramNotifier{
		client:      client,
		pollClient:  pollClient,
		baseURL:     strings.TrimRight(baseURL, "/"),
		token:       token,
		chatID:      chatID,
		chatIDInt:   cid,
		rateLimiter: newChatRateLimiter(3, 1.0),
	}
}

type tgSendMessagePayload struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

const maxTelegramMessageLen = 4000

func (tn *TelegramNotifier) sanitizeError(err error) error {
	if err == nil || tn.token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), tn.token, "[REDACTED]"))
}

// ponytail: splits plain lines by newline boundary. upgrade path: html entity aware tree splitting if rich markup spans lines.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	lines := strings.Split(text, "\n")
	var chunks []string
	var current strings.Builder

	for _, line := range lines {
		needed := len(line)
		if current.Len() > 0 {
			needed++ // for '\n'
		}

		if current.Len() > 0 && current.Len()+needed > maxLen {
			chunks = append(chunks, current.String())
			current.Reset()
		}

		for len(line) > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			chunks = append(chunks, line[:maxLen])
			line = line[maxLen:]
		}

		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func (tn *TelegramNotifier) SendMessage(ctx context.Context, text string) error {
	if tn.token == "" || tn.chatID == "" {
		slog.Debug("Telegram notification skipped: token or chat_id empty")
		return nil
	}

	chunks := splitMessage(text, maxTelegramMessageLen)
	for i, chunk := range chunks {
		if i > 0 {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := tn.sendSingleMessage(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

type tgErrorParameters struct {
	RetryAfter int `json:"retry_after"`
}

type tgErrorBody struct {
	Ok          bool              `json:"ok"`
	Description string            `json:"description"`
	Parameters  tgErrorParameters `json:"parameters"`
}

func extractRetryAfter(body []byte) int {
	var eb tgErrorBody
	if err := json.Unmarshal(body, &eb); err == nil && eb.Parameters.RetryAfter > 0 {
		return eb.Parameters.RetryAfter
	}
	return 0
}

func (tn *TelegramNotifier) sendSingleMessage(ctx context.Context, text string) error {
	payload := tgSendMessagePayload{
		ChatID:    tn.chatID,
		Text:      text,
		ParseMode: "HTML",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", tn.baseURL, tn.token)

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			return tn.sanitizeError(fmt.Errorf("create telegram req: %w", err))
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := tn.client.Do(req)
		if err != nil {
			return tn.sanitizeError(fmt.Errorf("send telegram request: %w", err))
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return nil
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			waitSec := extractRetryAfter(respBody)
			if waitSec <= 0 || waitSec > 5 {
				waitSec = 1
			}
			timer := time.NewTimer(time.Duration(waitSec) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				continue
			}
		}

		errText := strings.TrimSpace(string(respBody))
		if errText != "" {
			return tn.sanitizeError(fmt.Errorf("telegram api error: HTTP %d: %s", resp.StatusCode, errText))
		}
		return fmt.Errorf("telegram api error: HTTP %d", resp.StatusCode)
	}

	return fmt.Errorf("telegram api error: rate limit exceeded")
}

func (tn *TelegramNotifier) NotifyPresenceSuccess(ctx context.Context, mkName, dosen, key, respMsg string) error {
	waktuStr := NowWIB().Format("02-01-2006 15:04:05 WIB")
	msg := fmt.Sprintf(
		"🎉 <b>PRESENSI BERHASIL DICATAT!</b>\n\n"+
			"📚 <b>Mata Kuliah:</b> %s\n"+
			"👨‍🏫 <b>Dosen:</b> %s\n"+
			"🔑 <b>Key:</b> <code>%s</code>\n"+
			"🕒 <b>Waktu:</b> %s\n"+
			"💬 <b>Respon:</b> %s",
		html.EscapeString(mkName),
		html.EscapeString(dosen),
		html.EscapeString(key),
		waktuStr,
		html.EscapeString(respMsg),
	)

	// Fire with a 10s context timeout
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := tn.SendMessage(c, msg); err != nil {
		slog.Error("Failed to send Telegram notification", "error", err)
		return err
	}
	return nil
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgMessage struct {
	MessageID int64  `json:"message_id"`
	Chat      tgChat `json:"chat"`
	Text      string `json:"text"`
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

type tgUpdatesResponse struct {
	Ok     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

func parseCommand(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	cmd := fields[0]
	if idx := strings.Index(cmd, "@"); idx != -1 {
		cmd = cmd[:idx]
	}
	return cmd
}

func (tn *TelegramNotifier) PollOnce(ctx context.Context, offset int64, handler func(ctx context.Context, cmd string) string) (int64, error) {
	if tn.token == "" || tn.chatID == "" {
		return offset, nil
	}

	endpoint := fmt.Sprintf("%s/bot%s/getUpdates?offset=%d&timeout=20", tn.baseURL, tn.token, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return offset, tn.sanitizeError(fmt.Errorf("create getUpdates req: %w", err))
	}

	resp, err := tn.pollClient.Do(req)
	if err != nil {
		return offset, tn.sanitizeError(fmt.Errorf("getUpdates request: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		errText := strings.TrimSpace(string(respBody))
		if errText != "" {
			return offset, tn.sanitizeError(fmt.Errorf("getUpdates http %d: %s", resp.StatusCode, errText))
		}
		return offset, fmt.Errorf("getUpdates http %d", resp.StatusCode)
	}

	var updatesResp tgUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&updatesResp); err != nil {
		return offset, tn.sanitizeError(fmt.Errorf("decode getUpdates: %w", err))
	}

	nextOffset := offset
	for _, u := range updatesResp.Result {
		if u.UpdateID >= nextOffset {
			nextOffset = u.UpdateID + 1
		}
		if u.Message == nil {
			continue
		}
		if (tn.chatIDInt != 0 && u.Message.Chat.ID != tn.chatIDInt) || (tn.chatIDInt == 0 && strconv.FormatInt(u.Message.Chat.ID, 10) != tn.chatID) {
			now := time.Now()
			tn.unauthMu.Lock()
			shouldLog := tn.lastUnauthLog.IsZero() || now.Sub(tn.lastUnauthLog) >= 5*time.Second
			if shouldLog {
				tn.lastUnauthLog = now
			}
			tn.unauthMu.Unlock()
			if shouldLog {
				slog.Warn("Ignoring Telegram command from unauthorized chat", "chat_id", u.Message.Chat.ID)
			}
			continue
		}
		cmd := parseCommand(u.Message.Text)
		if cmd == "" {
			continue
		}
		if tn.rateLimiter != nil {
			allowed, warnAllowed := tn.rateLimiter.Allow(time.Now(), 5*time.Second)
			if !allowed {
				slog.Warn("Telegram command rate limited", "cmd", cmd)
				if warnAllowed {
					_ = tn.SendMessage(ctx, "⏳ <b>Terlalu banyak perintah.</b> Harap tunggu beberapa detik.")
				}
				continue
			}
		}
		cmdCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		reply := handler(cmdCtx, cmd)
		cancel()
		if reply != "" {
			if err := tn.SendMessage(ctx, reply); err != nil {
				slog.Error("Failed to reply to Telegram command", "cmd", cmd, "error", err)
			}
		}
	}

	return nextOffset, nil
}

func (tn *TelegramNotifier) StartCommandPoller(ctx context.Context, handler func(ctx context.Context, cmd string) string) {
	if tn.token == "" || tn.chatID == "" {
		return
	}
	slog.Info("Starting Telegram command poller")
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		nextOffset, err := tn.PollOnce(ctx, offset, handler)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("Telegram poller error", "error", err)
			timer := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		offset = nextOffset
	}
}

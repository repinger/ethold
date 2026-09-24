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
	client          *http.Client
	pollClient      *http.Client
	baseURL         string
	token           string
	chatID          string
	chatIDInt       int64
	commandThreadID int64
	notifThreadID   int64
	rateLimiter     *chatRateLimiter
	unauthMu        sync.Mutex
	lastUnauthLog   time.Time
	activeMu        sync.Mutex
	activeMsgID     int64
	extraMsgIDs     []int64
	deleteWg        sync.WaitGroup
	markupMu        sync.RWMutex
	replyMarkup     any
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

func (tn *TelegramNotifier) SetThreadIDs(commandThreadID, notifThreadID int64) {
	tn.commandThreadID = commandThreadID
	tn.notifThreadID = notifThreadID
}

type InlineButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

type tgInlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

type tgSendMessagePayload struct {
	ChatID          string `json:"chat_id"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
	Text            string `json:"text"`
	ParseMode       string `json:"parse_mode"`
	ReplyMarkup     any    `json:"reply_markup,omitempty"`
}

func (tn *TelegramNotifier) SetInlineKeyboard(buttons [][]InlineButton) {
	tn.markupMu.Lock()
	defer tn.markupMu.Unlock()
	if len(buttons) == 0 {
		tn.replyMarkup = nil
		return
	}
	tn.replyMarkup = &tgInlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}
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

	chunks := make([]string, 0, len(text)/maxLen+1)
	var current strings.Builder
	current.Grow(maxLen)

	rem := text
	for len(rem) > 0 {
		var line string
		if idx := strings.IndexByte(rem, '\n'); idx >= 0 {
			line = rem[:idx]
			rem = rem[idx+1:]
		} else {
			line = rem
			rem = ""
		}

		needed := len(line)
		if current.Len() > 0 {
			needed++ // for '\n'
		}

		if current.Len() > 0 && current.Len()+needed > maxLen {
			chunks = append(chunks, current.String())
			current.Reset()
			current.Grow(maxLen)
		}

		for len(line) > maxLen {
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
				current.Grow(maxLen)
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
	_, err := tn.SendMessageIDs(ctx, text)
	return err
}

func (tn *TelegramNotifier) SendMessageToThread(ctx context.Context, text string, threadID int64) error {
	_, err := tn.SendMessageIDsToThread(ctx, text, threadID)
	return err
}

func (tn *TelegramNotifier) SendMessageIDs(ctx context.Context, text string) ([]int64, error) {
	return tn.SendMessageIDsWithMarkupToThread(ctx, text, nil, tn.notifThreadID)
}

func (tn *TelegramNotifier) SendMessageIDsToThread(ctx context.Context, text string, threadID int64) ([]int64, error) {
	return tn.SendMessageIDsWithMarkupToThread(ctx, text, nil, threadID)
}

func (tn *TelegramNotifier) SendMessageIDsWithMarkupToThread(ctx context.Context, text string, markup any, threadID int64) ([]int64, error) {
	if tn.token == "" || tn.chatID == "" {
		slog.Debug("Telegram notification skipped: token or chat_id empty")
		return nil, nil
	}

	chunks := splitMessage(text, maxTelegramMessageLen)
	var msgIDs []int64
	for i, chunk := range chunks {
		if i > 0 {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return msgIDs, ctx.Err()
			case <-timer.C:
			}
		}
		var msgMarkup any
		if i == len(chunks)-1 {
			msgMarkup = markup
		}
		msgID, err := tn.sendSingleMessage(ctx, chunk, msgMarkup, threadID)
		if err != nil {
			return msgIDs, err
		}
		if msgID > 0 {
			msgIDs = append(msgIDs, msgID)
		}
	}
	return msgIDs, nil
}

type tgErrorParameters struct {
	RetryAfter int `json:"retry_after"`
}

type tgErrorBody struct {
	Ok          bool              `json:"ok"`
	Description string            `json:"description"`
	Parameters  tgErrorParameters `json:"parameters"`
}

type tgSendResponse struct {
	Ok     bool `json:"ok"`
	Result *struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

func extractRetryAfter(body []byte) int {
	var eb tgErrorBody
	if err := json.Unmarshal(body, &eb); err == nil && eb.Parameters.RetryAfter > 0 {
		return eb.Parameters.RetryAfter
	}
	return 0
}

func (tn *TelegramNotifier) sendSingleMessage(ctx context.Context, text string, markup any, threadID int64) (int64, error) {
	payload := tgSendMessagePayload{
		ChatID:          tn.chatID,
		MessageThreadID: threadID,
		Text:            text,
		ParseMode:       "HTML",
		ReplyMarkup:     markup,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal telegram payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", tn.baseURL, tn.token)

	client := tn.client
	if client == nil {
		client = http.DefaultClient
	}

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			return 0, tn.sanitizeError(fmt.Errorf("create telegram req: %w", err))
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return 0, tn.sanitizeError(fmt.Errorf("send telegram request: %w", err))
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var sr tgSendResponse
			if err := json.Unmarshal(respBody, &sr); err != nil {
				return 0, tn.sanitizeError(fmt.Errorf("decode sendMessage response: %w", err))
			}
			if sr.Result != nil {
				return sr.Result.MessageID, nil
			}
			return 0, nil
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
				return 0, ctx.Err()
			case <-timer.C:
				continue
			}
		}

		errText := strings.TrimSpace(string(respBody))
		if errText != "" {
			return 0, tn.sanitizeError(fmt.Errorf("telegram api error: HTTP %d: %s", resp.StatusCode, errText))
		}
		return 0, fmt.Errorf("telegram api error: HTTP %d", resp.StatusCode)
	}

	return 0, fmt.Errorf("telegram api error: rate limit exceeded")
}

type tgEditMessagePayload struct {
	ChatID      string `json:"chat_id"`
	MessageID   int64  `json:"message_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

func (tn *TelegramNotifier) EditMessageText(ctx context.Context, messageID int64, text string, markup any) error {
	if tn.token == "" || tn.chatID == "" || messageID <= 0 {
		return nil
	}

	payload := tgEditMessagePayload{
		ChatID:      tn.chatID,
		MessageID:   messageID,
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: markup,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal editMessageText payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/editMessageText", tn.baseURL, tn.token)

	client := tn.client
	if client == nil {
		client = http.DefaultClient
	}

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			return tn.sanitizeError(fmt.Errorf("create editMessageText req: %w", err))
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return tn.sanitizeError(fmt.Errorf("send editMessageText request: %w", err))
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
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
		if strings.Contains(errText, "message is not modified") {
			return nil
		}

		if errText != "" {
			return tn.sanitizeError(fmt.Errorf("telegram editMessageText api error: HTTP %d: %s", resp.StatusCode, errText))
		}
		return fmt.Errorf("telegram editMessageText api error: HTTP %d", resp.StatusCode)
	}

	return fmt.Errorf("telegram editMessageText api error: rate limit exceeded")
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

func (tn *TelegramNotifier) NotifyServerError(ctx context.Context, err error) error {
	waktuStr := NowWIB().Format("02-01-2006 15:04:05 WIB")
	msg := fmt.Sprintf(
		"⚠️ <b>GANGGUAN SERVER ETHOL</b>\n\n"+
			"🕒 <b>Waktu:</b> %s\n"+
			"❌ <b>Kendala:</b> %s\n\n"+
			"<i>Bot menunda pengecekan dan akan mencoba kembali secara otomatis.</i>",
		waktuStr,
		html.EscapeString(err.Error()),
	)

	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if sendErr := tn.SendMessage(c, msg); sendErr != nil {
		slog.Error("Failed to send Telegram server error notification", "error", sendErr)
		return sendErr
	}
	return nil
}

func (tn *TelegramNotifier) NotifyServerRecovery(ctx context.Context) error {
	waktuStr := NowWIB().Format("02-01-2006 15:04:05 WIB")
	msg := fmt.Sprintf(
		"✅ <b>LAYANAN ETHOL PULIH</b>\n\n"+
			"🕒 <b>Waktu:</b> %s\n"+
			"ℹ️ Layanan ETHOL dan CAS SSO dapat diakses kembali secara normal.",
		waktuStr,
	)

	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if sendErr := tn.SendMessage(c, msg); sendErr != nil {
		slog.Error("Failed to send Telegram recovery notification", "error", sendErr)
		return sendErr
	}
	return nil
}

func (tn *TelegramNotifier) NotifyAuthFailure(ctx context.Context, err error) error {
	waktuStr := NowWIB().Format("02-01-2006 15:04:05 WIB")
	msg := fmt.Sprintf(
		"🚨 <b>GAGAL AUTENTIKASI</b>\n\n"+
			"🕒 <b>Waktu:</b> %s\n"+
			"❌ <b>Kendala:</b> %s\n\n"+
			"<i>Sesi kedaluwarsa atau kredensial tidak valid. Silakan periksa kredensial di .env atau lakukan /relogin.</i>",
		waktuStr,
		html.EscapeString(err.Error()),
	)

	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if sendErr := tn.SendMessage(c, msg); sendErr != nil {
		slog.Error("Failed to send Telegram auth failure notification", "error", sendErr)
		return sendErr
	}
	return nil
}

type StartupInfo struct {
	Version      string
	User         *UserInfo
	AutoPresence bool
	WorkerCount  int
	TotalRecords int
	TodayRecords int
	ScanInterval time.Duration
	InScanWindow bool
}

func (tn *TelegramNotifier) NotifyStartup(ctx context.Context, info StartupInfo) error {
	waktuStr := NowWIB().Format("02-01-2006 15:04:05 WIB")

	userStr := "Tidak diketahui"
	if info.User != nil {
		userStr = fmt.Sprintf("%s (%s)", html.EscapeString(info.User.Nama), html.EscapeString(info.User.NipNrp))
	}

	verStr := info.Version
	if verStr == "" {
		verStr = "dev"
	}

	var sb strings.Builder
	sb.WriteString("🚀 <b>ETHOLD BOT AKTIF</b>\n\n")
	sb.WriteString(fmt.Sprintf("👤 <b>Pengguna:</b> %s\n", userStr))
	sb.WriteString(fmt.Sprintf("🏷️ <b>Versi:</b> <code>%s</code>\n", html.EscapeString(verStr)))
	sb.WriteString(fmt.Sprintf("🕒 <b>Waktu Mulai:</b> %s\n", waktuStr))

	setupTag := "[Direct Chat]"
	if tn.commandThreadID != 0 || tn.notifThreadID != 0 {
		setupTag = "[Forum Topics]"
	}

	if info.AutoPresence {
		sb.WriteString(fmt.Sprintf("⚙️ <b>Mode:</b> Auto-Presence (%d workers) %s\n", info.WorkerCount, setupTag))
		if info.ScanInterval > 0 {
			scanMode := "Background Sweep"
			if info.InScanWindow {
				scanMode = "Active Session"
			}
			sb.WriteString(fmt.Sprintf("⚡ <b>Interval Scan:</b> %v (%s)\n", info.ScanInterval, scanMode))
		}
		sb.WriteString(fmt.Sprintf("💾 <b>Presensi Tersimpan:</b> %d entri (%d hari ini)\n", info.TotalRecords, info.TodayRecords))
	} else {
		sb.WriteString(fmt.Sprintf("⚙️ <b>Mode:</b> Akademik Saja (Presensi Manual) %s\n", setupTag))
	}

	sb.WriteString("\n<i>Bot siap menerima perintah dan memantau aktivitas ETHOL.</i>")

	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if sendErr := tn.SendMessage(c, sb.String()); sendErr != nil {
		slog.Error("Failed to send Telegram startup notification", "error", sendErr)
		return sendErr
	}
	return nil
}

type tgDeleteMessagesPayload struct {
	ChatID     string  `json:"chat_id"`
	MessageIDs []int64 `json:"message_ids"`
}

func (tn *TelegramNotifier) DeleteMessages(ctx context.Context, messageIDs []int64) error {
	if tn.token == "" || tn.chatID == "" || len(messageIDs) == 0 {
		return nil
	}

	client := tn.client
	if client == nil {
		client = http.DefaultClient
	}

	endpoint := fmt.Sprintf("%s/bot%s/deleteMessages", tn.baseURL, tn.token)

	for len(messageIDs) > 0 {
		chunkSize := len(messageIDs)
		if chunkSize > 100 {
			chunkSize = 100
		}
		chunk := messageIDs[:chunkSize]
		messageIDs = messageIDs[chunkSize:]

		payload := tgDeleteMessagesPayload{
			ChatID:     tn.chatID,
			MessageIDs: chunk,
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal deleteMessages payload: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			return tn.sanitizeError(fmt.Errorf("create deleteMessages req: %w", err))
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return tn.sanitizeError(fmt.Errorf("send deleteMessages request: %w", err))
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errText := strings.TrimSpace(string(respBody))
			if errText != "" {
				return tn.sanitizeError(fmt.Errorf("telegram deleteMessages api error: HTTP %d: %s", resp.StatusCode, errText))
			}
			return fmt.Errorf("telegram deleteMessages api error: HTTP %d", resp.StatusCode)
		}
	}
	return nil
}

type tgSendChatActionPayload struct {
	ChatID          string `json:"chat_id"`
	Action          string `json:"action"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
}

func (tn *TelegramNotifier) SendChatAction(ctx context.Context, action string, threadID int64) error {
	if tn.token == "" || tn.chatID == "" {
		return nil
	}
	if action == "" {
		action = "typing"
	}

	payload := tgSendChatActionPayload{
		ChatID:          tn.chatID,
		Action:          action,
		MessageThreadID: threadID,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal sendChatAction payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendChatAction", tn.baseURL, tn.token)

	client := tn.client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return tn.sanitizeError(fmt.Errorf("create sendChatAction req: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return tn.sanitizeError(fmt.Errorf("send sendChatAction request: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		errText := strings.TrimSpace(string(respBody))
		if errText != "" {
			return tn.sanitizeError(fmt.Errorf("telegram sendChatAction api error: HTTP %d: %s", resp.StatusCode, errText))
		}
		return fmt.Errorf("telegram sendChatAction api error: HTTP %d", resp.StatusCode)
	}
	return nil
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgUser struct {
	ID int64 `json:"id"`
}

type tgMessage struct {
	MessageID       int64  `json:"message_id"`
	Chat            tgChat `json:"chat"`
	Text            string `json:"text"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message,omitempty"`
	Data    string     `json:"data"`
}

type tgUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *tgMessage       `json:"message,omitempty"`
	CallbackQuery *tgCallbackQuery `json:"callback_query,omitempty"`
}

type tgUpdatesResponse struct {
	Ok     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

var commandTextAliases = map[string]string{
	// Keyboard buttons & friendly text
	"📅 jadwal":         "/jadwal",
	"jadwal":           "/jadwal",
	"schedule":         "/jadwal",
	"📝 tugas":          "/tugas",
	"tugas":            "/tugas",
	"tasks":            "/tugas",
	"task":             "/tugas",
	"👥 presensi kelas": "/presensi_kelas",
	"presensi kelas":   "/presensi_kelas",
	"presensi_kelas":   "/presensi_kelas",
	"roster":           "/presensi_kelas",
	"📊 rekap":          "/rekap",
	"rekap":            "/rekap",
	"rekapitulasi":     "/rekap",
	"⚡ scan presensi":  "/check",
	"scan presensi":    "/check",
	"scan":             "/check",
	"check":            "/check",
	"📌 hari ini":       "/today",
	"hari ini":         "/today",
	"today":            "/today",
	"📚 materi":         "/materi",
	"materi":           "/materi",
	"materials":        "/materi",
	"📢 pengumuman":     "/pengumuman",
	"pengumuman":       "/pengumuman",
	"announcements":    "/pengumuman",
	"announcement":     "/pengumuman",
	"ℹ️ status":        "/status",
	"status":           "/status",
	"❓ bantuan":        "/help",
	"bantuan":          "/help",
	"help":             "/help",
	"start":            "/start",
	// Secondary commands
	"🎓 ujian":       "/ujian",
	"ujian":         "/ujian",
	"exams":         "/ujian",
	"exam":          "/ujian",
	"📖 mata kuliah": "/courses",
	"mata kuliah":   "/courses",
	"matkul":        "/courses",
	"courses":       "/courses",
	"👤 profil":      "/whoami",
	"profil":        "/whoami",
	"profile":       "/whoami",
	"whoami":        "/whoami",
	"🔄 relogin":     "/relogin",
	"relogin":       "/relogin",
	"⏸️ pause":      "/pause",
	"pause":         "/pause",
	"jeda":          "/pause",
	"▶️ resume":     "/resume",
	"resume":        "/resume",
	"lanjut":        "/resume",
	"🛠️ debug":      "/debug",
	"debug":         "/debug",
	"🏓 ping":        "/ping",
	"ping":          "/ping",
}

func parseCommand(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "/") {
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
	if cmd, ok := commandTextAliases[strings.ToLower(text)]; ok {
		return cmd
	}
	return ""
}

type tgAnswerCallbackPayload struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
}

func (tn *TelegramNotifier) answerCallbackQuery(ctx context.Context, queryID, text string) error {
	if tn.token == "" || queryID == "" {
		return nil
	}
	endpoint := fmt.Sprintf("%s/bot%s/answerCallbackQuery", tn.baseURL, tn.token)
	payload := tgAnswerCallbackPayload{
		CallbackQueryID: queryID,
		Text:            text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return tn.sanitizeError(err)
	}
	req.Header.Set("Content-Type", "application/json")
	hc := tn.client
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return tn.sanitizeError(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
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

		var (
			rawChatID   int64
			rawThreadID int64
			rawText     string
			userMsgID   int64
			callbackID  string
		)

		if u.Message != nil {
			rawChatID = u.Message.Chat.ID
			rawThreadID = u.Message.MessageThreadID
			rawText = u.Message.Text
			userMsgID = u.Message.MessageID
		} else if u.CallbackQuery != nil {
			if u.CallbackQuery.Message != nil {
				rawChatID = u.CallbackQuery.Message.Chat.ID
				rawThreadID = u.CallbackQuery.Message.MessageThreadID
			} else {
				rawChatID = u.CallbackQuery.From.ID
			}
			rawText = u.CallbackQuery.Data
			callbackID = u.CallbackQuery.ID
		} else {
			continue
		}

		if (tn.chatIDInt != 0 && rawChatID != tn.chatIDInt) || (tn.chatIDInt == 0 && strconv.FormatInt(rawChatID, 10) != tn.chatID) {
			now := time.Now()
			tn.unauthMu.Lock()
			shouldLog := tn.lastUnauthLog.IsZero() || now.Sub(tn.lastUnauthLog) >= 5*time.Second
			if shouldLog {
				tn.lastUnauthLog = now
			}
			tn.unauthMu.Unlock()
			if shouldLog {
				slog.Warn("Ignoring Telegram command from unauthorized chat", "chat_id", rawChatID)
			}
			devLog("Telegram message from unauthorized chat", "chat_id", rawChatID, "text", rawText)
			continue
		}

		cmd := parseCommand(rawText)
		if cmd == "" {
			if callbackID != "" {
				ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_ = tn.answerCallbackQuery(ackCtx, callbackID, "")
				ackCancel()
			}
			continue
		}

		if tn.notifThreadID != 0 && rawThreadID == tn.notifThreadID {
			slog.Debug("Ignoring Telegram command in notification topic", "cmd", cmd, "thread_id", rawThreadID)
			devLog("Telegram command in notification topic", "cmd", cmd, "thread_id", rawThreadID)
			if callbackID != "" {
				ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_ = tn.answerCallbackQuery(ackCtx, callbackID, "⚠️ Perintah tidak dapat digunakan di topik notifikasi.")
				ackCancel()
			}
			continue
		}

		if tn.commandThreadID != 0 && rawThreadID != tn.commandThreadID {
			slog.Debug("Ignoring Telegram command outside command topic", "cmd", cmd, "thread_id", rawThreadID, "expected", tn.commandThreadID)
			devLog("Telegram command outside command topic", "cmd", cmd, "thread_id", rawThreadID, "expected", tn.commandThreadID)
			if callbackID != "" {
				ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_ = tn.answerCallbackQuery(ackCtx, callbackID, "⚠️ Perintah hanya dapat digunakan di topik perintah.")
				ackCancel()
			}
			continue
		}

		targetThreadID := rawThreadID
		if targetThreadID == 0 && tn.commandThreadID != 0 {
			targetThreadID = tn.commandThreadID
		}

		if tn.rateLimiter != nil {
			allowed, warnAllowed := tn.rateLimiter.Allow(time.Now(), 5*time.Second)
			if !allowed {
				slog.Warn("Telegram command rate limited", "cmd", cmd)
				devLog("Telegram command rate limited", "chat_id", rawChatID, "cmd", cmd)
				if callbackID != "" {
					ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					if warnAllowed {
						_ = tn.answerCallbackQuery(ackCtx, callbackID, "⏳ Terlalu banyak perintah. Harap tunggu.")
					} else {
						_ = tn.answerCallbackQuery(ackCtx, callbackID, "")
					}
					ackCancel()
				} else if warnAllowed {
					_ = tn.SendMessageToThread(ctx, "⏳ <b>Terlalu banyak perintah.</b> Harap tunggu beberapa detik.", targetThreadID)
				}
				if userMsgID > 0 {
					tn.deleteWg.Add(1)
					go func(mid int64) {
						defer tn.deleteWg.Done()
						delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
						defer cancel()
						_ = tn.DeleteMessages(delCtx, []int64{mid})
					}(userMsgID)
				}
				continue
			}
		}

		if callbackID != "" {
			ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = tn.answerCallbackQuery(ackCtx, callbackID, "")
			ackCancel()
		}

		if userMsgID > 0 {
			tn.deleteWg.Add(1)
			go func(mid int64) {
				defer tn.deleteWg.Done()
				delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				if err := tn.DeleteMessages(delCtx, []int64{mid}); err != nil {
					slog.Warn("Failed to delete Telegram user command message", "msg_id", mid, "error", err)
					devLog("Failed to delete Telegram user command message", "error", err)
				}
			}(userMsgID)
		}

		tn.deleteWg.Add(1)
		go func(threadID int64) {
			defer tn.deleteWg.Done()
			actionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = tn.SendChatAction(actionCtx, "typing", threadID)
		}(targetThreadID)

		cmdStart := time.Now()
		cmdCtx, cancel := context.WithTimeout(ctx, 45*time.Second)

		reply := handler(cmdCtx, cmd)
		cancel()
		var sendErr error
		if reply != "" {
			tn.markupMu.RLock()
			markup := tn.replyMarkup
			tn.markupMu.RUnlock()

			var toDelete []int64

			targetEditID := int64(0)
			if u.CallbackQuery != nil && u.CallbackQuery.Message != nil {
				targetEditID = u.CallbackQuery.Message.MessageID
			} else {
				tn.activeMu.Lock()
				targetEditID = tn.activeMsgID
				tn.activeMu.Unlock()
			}

			tn.activeMu.Lock()
			if targetEditID > 0 {
				toDelete = append(toDelete, tn.extraMsgIDs...)
				tn.extraMsgIDs = nil
				if tn.activeMsgID > 0 && tn.activeMsgID != targetEditID {
					toDelete = append(toDelete, tn.activeMsgID)
				}
			} else {
				if tn.activeMsgID > 0 {
					toDelete = append(toDelete, tn.activeMsgID)
					tn.activeMsgID = 0
				}
				toDelete = append(toDelete, tn.extraMsgIDs...)
				tn.extraMsgIDs = nil
			}
			tn.activeMu.Unlock()

			chunks := splitMessage(reply, maxTelegramMessageLen)
			edited := false

			if targetEditID > 0 && len(chunks) > 0 {
				editErr := tn.EditMessageText(ctx, targetEditID, chunks[0], markup)
				if editErr == nil {
					edited = true
					tn.activeMu.Lock()
					tn.activeMsgID = targetEditID
					tn.activeMu.Unlock()

					if len(chunks) > 1 {
						for _, chunk := range chunks[1:] {
							mid, err := tn.sendSingleMessage(ctx, chunk, nil, targetThreadID)
							if err != nil {
								sendErr = err
								break
							}
							if mid > 0 {
								tn.activeMu.Lock()
								tn.extraMsgIDs = append(tn.extraMsgIDs, mid)
								tn.activeMu.Unlock()
							}
						}
					}
				} else {
					slog.Warn("Failed to edit Telegram message, falling back to send", "msg_id", targetEditID, "error", editErr)
					toDelete = append(toDelete, targetEditID)
				}
			}

			if !edited {
				var newIDs []int64
				newIDs, sendErr = tn.SendMessageIDsWithMarkupToThread(ctx, reply, markup, targetThreadID)
				if sendErr != nil {
					slog.Error("Failed to reply to Telegram command", "cmd", cmd, "error", sendErr)
				} else if len(newIDs) > 0 {
					tn.activeMu.Lock()
					tn.activeMsgID = newIDs[0]
					if len(newIDs) > 1 {
						tn.extraMsgIDs = append(tn.extraMsgIDs, newIDs[1:]...)
					}
					tn.activeMu.Unlock()
				}
			}

			if len(toDelete) > 0 {
				tn.deleteWg.Add(1)
				go func(ids []int64) {
					defer tn.deleteWg.Done()
					delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					defer cancel()
					if err := tn.DeleteMessages(delCtx, ids); err != nil {
						slog.Warn("Failed to delete previous Telegram messages", "count", len(ids), "error", err)
						devLog("Failed to delete previous Telegram messages", "error", err)
					}
				}(toDelete)
			}
		}

		devLogTelegramCommand(rawChatID, cmd, rawText, time.Since(cmdStart), len(reply), sendErr)
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

package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTelegramNotifier_PollOnce(t *testing.T) {
	var offsetRequested atomic.Int64
	var sentMessages []string
	var sentMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot12345/getUpdates":
			offsetStr := r.URL.Query().Get("offset")
			var off int64
			fmt.Sscanf(offsetStr, "%d", &off)
			offsetRequested.Store(off)

			resp := tgUpdatesResponse{
				Ok: true,
				Result: []tgUpdate{
					{
						UpdateID: 100,
						Message: &tgMessage{
							MessageID: 1,
							Chat:      tgChat{ID: 999888},
							Text:      "/ping",
						},
					},
					{
						UpdateID: 101,
						Message: &tgMessage{
							MessageID: 2,
							Chat:      tgChat{ID: 111222}, // unauthorized chat ID
							Text:      "/ping",
						},
					},
					{
						UpdateID: 102,
						Message: &tgMessage{
							MessageID: 3,
							Chat:      tgChat{ID: 999888},
							Text:      "/status@TestBot", // with bot suffix
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)

		case "/bot12345/sendMessage":
			var payload tgSendMessagePayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sentMu.Lock()
			sentMessages = append(sentMessages, payload.Text)
			sentMu.Unlock()
			w.Write([]byte(`{"ok":true}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "12345", "999888")
	ctx := context.Background()

	var handledCmds []string
	handler := func(ctx context.Context, cmd string) string {
		handledCmds = append(handledCmds, cmd)
		if cmd == "/ping" {
			return "pong"
		}
		if cmd == "/status" {
			return "all good"
		}
		return "unknown"
	}

	nextOffset, err := tn.PollOnce(ctx, 50, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}

	if nextOffset != 103 {
		t.Errorf("expected nextOffset 103, got %d", nextOffset)
	}

	if offsetRequested.Load() != 50 {
		t.Errorf("expected offset 50 sent to server, got %d", offsetRequested.Load())
	}

	// Should only handle 2 authorized commands (/ping and /status), ignoring chat 111222
	if len(handledCmds) != 2 {
		t.Fatalf("expected 2 handled commands, got %d: %v", len(handledCmds), handledCmds)
	}
	if handledCmds[0] != "/ping" || handledCmds[1] != "/status" {
		t.Errorf("unexpected handled commands: %v", handledCmds)
	}

	sentMu.Lock()
	defer sentMu.Unlock()
	if len(sentMessages) != 2 {
		t.Fatalf("expected 2 sent messages, got %d: %v", len(sentMessages), sentMessages)
	}
	if sentMessages[0] != "pong" || sentMessages[1] != "all good" {
		t.Errorf("unexpected replies: %v", sentMessages)
	}
}

func TestTelegramNotifier_ParseCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/status", "/status"},
		{"/status@EtholBot", "/status"},
		{"/check now please", "/check"},
		{"/ping", "/ping"},
		// Interactive emoji labels
		{"📅 Jadwal", "/jadwal"},
		{"📝 Tugas", "/tugas"},
		{"👥 Presensi Kelas", "/presensi_kelas"},
		{"📊 Rekap", "/rekap"},
		{"⚡ Scan Presensi", "/check"},
		{"📌 Hari Ini", "/today"},
		{"📚 Materi", "/materi"},
		{"📢 Pengumuman", "/pengumuman"},
		{"ℹ️ Status", "/status"},
		{"❓ Bantuan", "/help"},
		{"🎓 Ujian", "/ujian"},
		{"📖 Mata Kuliah", "/courses"},
		{"👤 Profil", "/whoami"},
		{"🔄 Relogin", "/relogin"},
		{"⏸️ Pause", "/pause"},
		{"▶️ Resume", "/resume"},
		{"🛠️ Debug", "/debug"},
		{"🏓 Ping", "/ping"},
		// Plain text aliases
		{"jadwal", "/jadwal"},
		{"JADWAL", "/jadwal"},
		{"schedule", "/jadwal"},
		{"tugas", "/tugas"},
		{"ujian", "/ujian"},
		{"matkul", "/courses"},
		{"courses", "/courses"},
		{"profil", "/whoami"},
		{"whoami", "/whoami"},
		{"relogin", "/relogin"},
		{"pause", "/pause"},
		{"jeda", "/pause"},
		{"resume", "/resume"},
		{"lanjut", "/resume"},
		{"debug", "/debug"},
		{"ping", "/ping"},
		{"bantuan", "/help"},
		{"help", "/help"},
		{"start", "/start"},
		// Negative / unknown
		{"hello", ""},
		{"", ""},
		{"   ", ""},
		{"unknown text message", ""},
	}

	for _, tt := range tests {
		got := parseCommand(tt.input)
		if got != tt.expected {
			t.Errorf("parseCommand(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func BenchmarkParseCommand(b *testing.B) {
	inputs := []string{
		"/jadwal",
		"/jadwal@EtholBot",
		"📅 Jadwal",
		"jadwal",
		"⚡ Scan Presensi",
		"unknown command text",
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			_ = parseCommand(input)
		}
	}
}

func BenchmarkChatRateLimiter_Allow(b *testing.B) {
	rl := newChatRateLimiter(1000, 1000.0)
	now := time.Now()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rl.Allow(now, 5*time.Second)
	}
}

func TestTelegramNotifier_SendMessage_SplitLongMessage(t *testing.T) {
	var receivedRequests []string
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botsecrettoken123/sendMessage" {
			http.NotFound(w, r)
			return
		}
		var payload tgSendMessagePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(payload.Text) > 4000 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message is too long"}`))
			return
		}
		mu.Lock()
		receivedRequests = append(receivedRequests, payload.Text)
		mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "secrettoken123", "999888")

	// Generate a message of ~9000 characters with 100 lines
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&sb, "%d. Line with some content describing student attendance roster %d\n", i, i)
	}
	longMsg := sb.String()
	if len(longMsg) < 6000 {
		t.Fatalf("expected longMsg > 6000 chars, got %d", len(longMsg))
	}

	err = tn.SendMessage(context.Background(), longMsg)
	if err != nil {
		t.Fatalf("expected SendMessage to succeed with splitting, got error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(receivedRequests) < 2 {
		t.Fatalf("expected message to be split into at least 2 requests, got %d", len(receivedRequests))
	}
	for i, chunk := range receivedRequests {
		if len(chunk) > 4000 {
			t.Errorf("chunk %d exceeded 4000 chars: %d", i, len(chunk))
		}
	}
}

func TestTelegramNotifier_SendMessage_ErrorBodyIncluded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message is too long"}`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "mytoken", "999888")
	err = tn.SendMessage(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Bad Request: message is too long") {
		t.Errorf("expected error to include Telegram error description, got: %v", err)
	}
}

func TestTelegramNotifier_SanitizeError(t *testing.T) {
	token := "my-super-secret-token"
	tn := NewTelegramNotifier(nil, "", token, "123")

	rawErr := fmt.Errorf("request to https://api.telegram.org/bot%s/getUpdates failed", token)
	sanitized := tn.sanitizeError(rawErr)
	if strings.Contains(sanitized.Error(), token) {
		t.Errorf("token leaked in sanitized error: %v", sanitized)
	}
	if !strings.Contains(sanitized.Error(), "[REDACTED]") {
		t.Errorf("expected [REDACTED] in sanitized error: %v", sanitized)
	}
}

func TestTelegramNotifier_RateLimiter_SpamSuppression(t *testing.T) {
	var sentMessages []string
	var sentMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			var updates []tgUpdate
			for i := 1; i <= 10; i++ {
				updates = append(updates, tgUpdate{
					UpdateID: int64(100 + i),
					Message: &tgMessage{
						MessageID: int64(i),
						Chat:      tgChat{ID: 555},
						Text:      "/status",
					},
				})
			}
			json.NewEncoder(w).Encode(tgUpdatesResponse{Ok: true, Result: updates})
		case "/bot123/sendMessage":
			var p tgSendMessagePayload
			_ = json.NewDecoder(r.Body).Decode(&p)
			sentMu.Lock()
			sentMessages = append(sentMessages, p.Text)
			sentMu.Unlock()
			w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "555")
	var handledCount int
	handler := func(ctx context.Context, cmd string) string {
		handledCount++
		return "status-ok"
	}

	nextOffset, err := tn.PollOnce(context.Background(), 1, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}
	if nextOffset != 111 {
		t.Errorf("expected nextOffset 111, got %d", nextOffset)
	}

	// Burst is 3, so only 3 commands should be executed
	if handledCount != 3 {
		t.Errorf("expected 3 handled commands, got %d", handledCount)
	}

	sentMu.Lock()
	defer sentMu.Unlock()
	// 3 replies + 1 rate-limit warning (subsequent rate-limited commands suppressed)
	if len(sentMessages) != 4 {
		t.Fatalf("expected 4 sent messages (3 replies + 1 warning), got %d: %v", len(sentMessages), sentMessages)
	}
	if !strings.Contains(sentMessages[3], "Terlalu banyak perintah") {
		t.Errorf("expected warning message at index 3, got %q", sentMessages[3])
	}
}

func TestTelegramNotifier_RateLimiter_SlowHandlerNoLeak(t *testing.T) {
	var sentMessages []string
	var sentMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			var updates []tgUpdate
			for i := 1; i <= 6; i++ {
				updates = append(updates, tgUpdate{
					UpdateID: int64(100 + i),
					Message: &tgMessage{
						MessageID: int64(i),
						Chat:      tgChat{ID: 555},
						Text:      "/jadwal",
					},
				})
			}
			json.NewEncoder(w).Encode(tgUpdatesResponse{Ok: true, Result: updates})
		case "/bot123/sendMessage":
			var p tgSendMessagePayload
			_ = json.NewDecoder(r.Body).Decode(&p)
			sentMu.Lock()
			sentMessages = append(sentMessages, p.Text)
			sentMu.Unlock()
			w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "555")
	// High refill rate so any elapsed execution time would have refilled tokens under the buggy logic
	tn.rateLimiter = newChatRateLimiter(3, 1000.0)

	var handledCount int
	handler := func(ctx context.Context, cmd string) string {
		handledCount++
		time.Sleep(15 * time.Millisecond) // Simulated slow command processing
		return "jadwal-ok"
	}

	nextOffset, err := tn.PollOnce(context.Background(), 1, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}
	if nextOffset != 107 {
		t.Errorf("expected nextOffset 107, got %d", nextOffset)
	}

	// Burst is 3; slow execution must not refill tokens during the batch
	if handledCount != 3 {
		t.Errorf("expected exactly 3 handled commands, got %d", handledCount)
	}

	sentMu.Lock()
	defer sentMu.Unlock()
	// 3 replies + 1 warning message
	if len(sentMessages) != 4 {
		t.Fatalf("expected 4 sent messages (3 replies + 1 warning), got %d: %v", len(sentMessages), sentMessages)
	}
	if !strings.Contains(sentMessages[3], "Terlalu banyak perintah") {
		t.Errorf("expected warning message at index 3, got %q", sentMessages[3])
	}
}

func TestTelegramNotifier_RateLimiter_AcrossPollsNoBusyRefill(t *testing.T) {
	var pollCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			p := pollCount.Add(1)
			var updates []tgUpdate
			if p == 1 {
				// First poll returns 2 updates
				for i := 1; i <= 2; i++ {
					updates = append(updates, tgUpdate{
						UpdateID: int64(100 + i),
						Message: &tgMessage{
							MessageID: int64(i),
							Chat:      tgChat{ID: 555},
							Text:      "/tugas",
						},
					})
				}
			} else if p == 2 {
				// Second poll returns 2 updates immediately after first finishes
				for i := 3; i <= 4; i++ {
					updates = append(updates, tgUpdate{
						UpdateID: int64(100 + i),
						Message: &tgMessage{
							MessageID: int64(i),
							Chat:      tgChat{ID: 555},
							Text:      "/tugas",
						},
					})
				}
			}
			json.NewEncoder(w).Encode(tgUpdatesResponse{Ok: true, Result: updates})
		case "/bot123/sendMessage":
			w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "555")
	// High refill rate (1000/s). If processing time counted as idle time, poll 2 would get full tokens.
	tn.rateLimiter = newChatRateLimiter(3, 1000.0)

	var handledCount int
	handler := func(ctx context.Context, cmd string) string {
		handledCount++
		time.Sleep(15 * time.Millisecond) // simulate work
		return "tugas-ok"
	}

	// Poll 1: 2 commands executed
	offset, err := tn.PollOnce(context.Background(), 1, handler)
	if err != nil {
		t.Fatalf("PollOnce 1 failed: %v", err)
	}
	if handledCount != 2 {
		t.Fatalf("expected 2 handled in poll 1, got %d", handledCount)
	}

	// Poll 2 called immediately: only 1 more token left from burst 3
	_, err = tn.PollOnce(context.Background(), offset, handler)
	if err != nil {
		t.Fatalf("PollOnce 2 failed: %v", err)
	}

	// Total handled should be 3 (burst 3), 4th command was rate-limited
	if handledCount != 3 {
		t.Errorf("expected exactly 3 total handled commands across polls, got %d", handledCount)
	}
}

func TestTelegramNotifier_SendMessage_Retry429(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)
		if att == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "token", "chat1")
	err = tn.SendMessage(context.Background(), "test retry")
	if err != nil {
		t.Fatalf("expected SendMessage to succeed after retry, got %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestTelegramNotifier_CommandTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			json.NewEncoder(w).Encode(tgUpdatesResponse{
				Ok: true,
				Result: []tgUpdate{
					{
						UpdateID: 200,
						Message: &tgMessage{
							MessageID: 1,
							Chat:      tgChat{ID: 777},
							Text:      "/slow",
						},
					},
				},
			})
		case "/bot123/sendMessage":
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "777")
	var hasDeadline bool
	handler := func(ctx context.Context, cmd string) string {
		_, hasDeadline = ctx.Deadline()
		return "done"
	}

	_, err = tn.PollOnce(context.Background(), 1, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}
	if !hasDeadline {
		t.Error("expected command context to have a deadline/timeout set")
	}
}

func TestTelegramNotifier_NotifyServerError(t *testing.T) {
	var sentPayload tgSendMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bot123/sendMessage" {
			_ = json.NewDecoder(r.Body).Decode(&sentPayload)
			w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "777")
	err = tn.NotifyServerError(context.Background(), fmt.Errorf("502 Bad Gateway"))
	if err != nil {
		t.Fatalf("NotifyServerError failed: %v", err)
	}

	if !strings.Contains(sentPayload.Text, "GANGGUAN SERVER ETHOL") {
		t.Errorf("expected server error title, got: %s", sentPayload.Text)
	}
	if !strings.Contains(sentPayload.Text, "502 Bad Gateway") {
		t.Errorf("expected error message detail, got: %s", sentPayload.Text)
	}
}

func TestTelegramNotifier_NotifyServerRecovery(t *testing.T) {
	var sentPayload tgSendMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bot123/sendMessage" {
			_ = json.NewDecoder(r.Body).Decode(&sentPayload)
			w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "777")
	err = tn.NotifyServerRecovery(context.Background())
	if err != nil {
		t.Fatalf("NotifyServerRecovery failed: %v", err)
	}

	if !strings.Contains(sentPayload.Text, "LAYANAN ETHOL PULIH") {
		t.Errorf("expected server recovery title, got: %s", sentPayload.Text)
	}
}

func TestTelegramNotifier_NotifyAuthFailure(t *testing.T) {
	var sentPayload tgSendMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bot123/sendMessage" {
			_ = json.NewDecoder(r.Body).Decode(&sentPayload)
			w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "777")
	err = tn.NotifyAuthFailure(context.Background(), fmt.Errorf("401 Unauthorized"))
	if err != nil {
		t.Fatalf("NotifyAuthFailure failed: %v", err)
	}

	if !strings.Contains(sentPayload.Text, "GAGAL AUTENTIKASI") {
		t.Errorf("expected auth failure title, got: %s", sentPayload.Text)
	}
	if !strings.Contains(sentPayload.Text, "401 Unauthorized") {
		t.Errorf("expected error message detail, got: %s", sentPayload.Text)
	}
}

func TestTelegramNotifier_SendMessageIDs(t *testing.T) {
	var currentMsgID int64 = 100
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bot123/sendMessage" {
			currentMsgID++
			resp := map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": currentMsgID,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "777")
	ids, err := tn.SendMessageIDs(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("SendMessageIDs failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != 101 {
		t.Fatalf("expected message ID [101], got %v", ids)
	}
}

func TestTelegramNotifier_SendMessageIDs_LargeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bot123/sendMessage" {
			// Simulate a realistic large response (>4096 bytes) for a message with entities
			padding := strings.Repeat("A", 6000)
			resp := map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": int64(888),
					"text":       padding,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "777")
	ids, err := tn.SendMessageIDs(context.Background(), "roster text")
	if err != nil {
		t.Fatalf("SendMessageIDs failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != 888 {
		t.Fatalf("expected message ID [888], got %v", ids)
	}
}

func TestTelegramNotifier_DeleteMessages(t *testing.T) {
	var deletedBatches [][]int64
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bot123/deleteMessages" {
			var payload tgDeleteMessagesPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if payload.ChatID != "777" {
				http.Error(w, "bad chat_id", http.StatusBadRequest)
				return
			}
			mu.Lock()
			deletedBatches = append(deletedBatches, payload.MessageIDs)
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "777")

	// 1. Test empty IDs no-op
	if err := tn.DeleteMessages(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error on nil IDs: %v", err)
	}

	// 2. Test batch of 150 IDs (should chunk into 100 + 50)
	var testIDs []int64
	for i := int64(1); i <= 150; i++ {
		testIDs = append(testIDs, i)
	}
	if err := tn.DeleteMessages(context.Background(), testIDs); err != nil {
		t.Fatalf("DeleteMessages failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(deletedBatches) != 2 {
		t.Fatalf("expected 2 delete batches, got %d", len(deletedBatches))
	}
	if len(deletedBatches[0]) != 100 {
		t.Errorf("expected 100 IDs in first batch, got %d", len(deletedBatches[0]))
	}
	if len(deletedBatches[1]) != 50 {
		t.Errorf("expected 50 IDs in second batch, got %d", len(deletedBatches[1]))
	}
}

func TestTelegramNotifier_BatchCleanupCycle(t *testing.T) {
	var mu sync.Mutex
	var deletedBatches [][]int64
	var nextBotMsgID int64 = 500

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			// Not used by direct PollOnce test below, handled per sub-call
			w.Write([]byte(`{"ok":true,"result":[]}`))

		case "/bot123/sendMessage":
			mu.Lock()
			nextBotMsgID++
			botID := nextBotMsgID
			mu.Unlock()
			resp := map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": botID,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)

		case "/bot123/deleteMessages":
			var payload tgDeleteMessagesPayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			deletedBatches = append(deletedBatches, payload.MessageIDs)
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":true}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "999")
	// Disable rate limiter for testing to send sequential commands quickly
	tn.rateLimiter = nil

	handler := func(ctx context.Context, cmd string) string {
		return "reply to " + cmd
	}

	ctx := context.Background()

	// Helper to simulate polling a single command update
	pollCommand := func(updateID, userMsgID int64, cmdText string) {
		// Prepare single-message update response directly via mocked poll
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/bot123/getUpdates":
				resp := tgUpdatesResponse{
					Ok: true,
					Result: []tgUpdate{
						{
							UpdateID: updateID,
							Message: &tgMessage{
								MessageID: userMsgID,
								Chat:      tgChat{ID: 999},
								Text:      cmdText,
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)

			case "/bot123/sendMessage":
				mu.Lock()
				nextBotMsgID++
				botID := nextBotMsgID
				mu.Unlock()
				resp := map[string]any{
					"ok": true,
					"result": map[string]any{
						"message_id": botID,
					},
				}
				_ = json.NewEncoder(w).Encode(resp)

			case "/bot123/deleteMessages":
				var payload tgDeleteMessagesPayload
				_ = json.NewDecoder(r.Body).Decode(&payload)
				mu.Lock()
				deletedBatches = append(deletedBatches, payload.MessageIDs)
				mu.Unlock()
				w.Write([]byte(`{"ok":true,"result":true}`))

			default:
				http.NotFound(w, r)
			}
		})

		_, err := tn.PollOnce(ctx, updateID, handler)
		if err != nil {
			t.Fatalf("PollOnce failed: %v", err)
		}
		tn.WaitPendingDeletions()
	}

	// Command 1 (User msg 10 -> Bot reply 501)
	pollCommand(1, 10, "/status")
	mu.Lock()
	if len(deletedBatches) != 0 {
		t.Fatalf("expected 0 delete calls after command 1, got %d", len(deletedBatches))
	}
	mu.Unlock()

	// Command 2 (User msg 20 -> Bot reply 502)
	pollCommand(2, 20, "/ping")
	mu.Lock()
	if len(deletedBatches) != 0 {
		t.Fatalf("expected 0 delete calls after command 2, got %d", len(deletedBatches))
	}
	mu.Unlock()

	// Command 3 (User msg 30 -> Bot reply 503)
	pollCommand(3, 30, "/help")
	mu.Lock()
	if len(deletedBatches) != 0 {
		t.Fatalf("expected 0 delete calls after command 3, got %d", len(deletedBatches))
	}
	mu.Unlock()

	// Command 4 (User msg 40 -> Bot reply 504)
	// Must trigger deletion of commands 1-3: user IDs [10, 20, 30] and bot IDs [501, 502, 503]
	pollCommand(4, 40, "/whoami")
	mu.Lock()
	if len(deletedBatches) != 1 {
		t.Fatalf("expected 1 delete call after command 4, got %d", len(deletedBatches))
	}
	expectedDeleted := []int64{10, 501, 20, 502, 30, 503}
	if len(deletedBatches[0]) != len(expectedDeleted) {
		t.Fatalf("expected %d deleted IDs, got %v", len(expectedDeleted), deletedBatches[0])
	}
	for i, id := range expectedDeleted {
		if deletedBatches[0][i] != id {
			t.Errorf("deleted batch index %d: expected %d, got %d", i, id, deletedBatches[0][i])
		}
	}
	mu.Unlock()

	// Command 5 (User msg 50 -> Bot reply 505) - part of second batch
	pollCommand(5, 50, "/jadwal")
	mu.Lock()
	if len(deletedBatches) != 1 {
		t.Fatalf("expected still 1 delete call after command 5, got %d", len(deletedBatches))
	}
	mu.Unlock()

	// Command 6 (User msg 60 -> Bot reply 606)
	pollCommand(6, 60, "/courses")
	mu.Lock()
	if len(deletedBatches) != 1 {
		t.Fatalf("expected still 1 delete call after command 6, got %d", len(deletedBatches))
	}
	mu.Unlock()

	// Command 7 (User msg 70 -> Bot reply 507)
	// Must trigger deletion of commands 4-6: user IDs [40, 50, 60] and bot IDs [504, 505, 506]
	pollCommand(7, 70, "/tugas")
	mu.Lock()
	if len(deletedBatches) != 2 {
		t.Fatalf("expected 2 delete calls after command 7, got %d", len(deletedBatches))
	}
	expectedBatch2 := []int64{40, 504, 50, 505, 60, 506}
	if len(deletedBatches[1]) != len(expectedBatch2) {
		t.Fatalf("expected %d deleted IDs in batch 2, got %v", len(expectedBatch2), deletedBatches[1])
	}
	for i, id := range expectedBatch2 {
		if deletedBatches[1][i] != id {
			t.Errorf("deleted batch 2 index %d: expected %d, got %d", i, id, deletedBatches[1][i])
		}
	}
	mu.Unlock()
}

func TestTelegramNotifier_BatchCleanup_OutputBeforeDeletion(t *testing.T) {
	var mu sync.Mutex
	var order []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			w.Write([]byte(`{"ok":true,"result":[]}`))
		case "/bot123/sendMessage":
			mu.Lock()
			order = append(order, "send")
			mu.Unlock()
			resp := map[string]any{
				"ok":     true,
				"result": map[string]any{"message_id": 999},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/bot123/deleteMessages":
			mu.Lock()
			order = append(order, "delete")
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "999")
	tn.rateLimiter = nil

	handler := func(ctx context.Context, cmd string) string {
		time.Sleep(20 * time.Millisecond)
		return "reply: " + cmd
	}

	ctx := context.Background()
	pollCommand := func(updateID, userMsgID int64) {
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/bot123/getUpdates":
				resp := tgUpdatesResponse{
					Ok: true,
					Result: []tgUpdate{
						{
							UpdateID: updateID,
							Message: &tgMessage{
								MessageID: userMsgID,
								Chat:      tgChat{ID: 999},
								Text:      "/test",
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
			case "/bot123/sendMessage":
				mu.Lock()
				order = append(order, "send")
				mu.Unlock()
				resp := map[string]any{
					"ok":     true,
					"result": map[string]any{"message_id": 999},
				}
				_ = json.NewEncoder(w).Encode(resp)
			case "/bot123/deleteMessages":
				mu.Lock()
				order = append(order, "delete")
				mu.Unlock()
				w.Write([]byte(`{"ok":true,"result":true}`))
			default:
				http.NotFound(w, r)
			}
		})

		_, err := tn.PollOnce(ctx, updateID, handler)
		if err != nil {
			t.Fatalf("PollOnce failed: %v", err)
		}
		tn.WaitPendingDeletions()
	}

	// Commands 1-3
	pollCommand(1, 10)
	pollCommand(2, 20)
	pollCommand(3, 30)

	mu.Lock()
	order = nil // reset order before 4th command
	mu.Unlock()

	// Command 4: should send command output BEFORE deleting previous messages
	pollCommand(4, 40)

	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 {
		t.Fatalf("expected at least [send, delete], got %v", order)
	}
	if order[0] != "send" || order[1] != "delete" {
		t.Fatalf("expected output first ('send' before 'delete'), got order: %v", order)
	}
}

func TestTelegramNotifier_PersistentKeyboard(t *testing.T) {
	var (
		mu          sync.Mutex
		lastPayload map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			resp := tgUpdatesResponse{
				Ok: true,
				Result: []tgUpdate{
					{
						UpdateID: 1,
						Message: &tgMessage{
							MessageID: 10,
							Chat:      tgChat{ID: 999},
							Text:      "/help",
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/bot123/sendMessage":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			lastPayload = payload
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":{"message_id":100}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "999")
	buttons := [][]string{
		{"/jadwal", "/tugas"},
		{"/status", "/help"},
	}
	tn.SetReplyKeyboard(buttons)

	// 1. Background SendMessage should NOT attach reply markup
	if err := tn.SendMessage(context.Background(), "background notification"); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
	mu.Lock()
	if _, ok := lastPayload["reply_markup"]; ok {
		t.Fatalf("expected SendMessage not to include reply_markup, got %v", lastPayload["reply_markup"])
	}
	lastPayload = nil
	mu.Unlock()

	// 2. PollOnce interactive reply should attach persistent reply markup
	handler := func(ctx context.Context, cmd string) string {
		return "help menu"
	}
	_, err = tn.PollOnce(context.Background(), 0, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}

	mu.Lock()
	markup, ok := lastPayload["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("expected reply_markup in PollOnce payload, got %v", lastPayload)
	}
	if resize, _ := markup["resize_keyboard"].(bool); !resize {
		t.Errorf("expected resize_keyboard: true, got %v", markup["resize_keyboard"])
	}
	if persist, _ := markup["is_persistent"].(bool); !persist {
		t.Errorf("expected is_persistent: true, got %v", markup["is_persistent"])
	}
	keyboardRows, ok := markup["keyboard"].([]any)
	if !ok || len(keyboardRows) != 2 {
		t.Fatalf("expected 2 keyboard rows, got %v", markup["keyboard"])
	}
	row0 := keyboardRows[0].([]any)
	btn0 := row0[0].(map[string]any)["text"]
	btn1 := row0[1].(map[string]any)["text"]
	if btn0 != "/jadwal" || btn1 != "/tugas" {
		t.Errorf("unexpected buttons in row 0: %v, %v", btn0, btn1)
	}
	lastPayload = nil
	mu.Unlock()

	// 3. Clear keyboard and verify reply markup is omitted
	tn.SetReplyKeyboard(nil)
	_, err = tn.PollOnce(context.Background(), 0, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}

	mu.Lock()
	if _, ok := lastPayload["reply_markup"]; ok {
		t.Errorf("expected reply_markup to be omitted after SetReplyKeyboard(nil), got %v", lastPayload["reply_markup"])
	}
	mu.Unlock()
}

func TestTelegramNotifier_PollOnce_TextAlias(t *testing.T) {
	var (
		mu          sync.Mutex
		lastHandled string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			resp := tgUpdatesResponse{
				Ok: true,
				Result: []tgUpdate{
					{
						UpdateID: 1,
						Message: &tgMessage{
							MessageID: 10,
							Chat:      tgChat{ID: 999},
							Text:      "📅 Jadwal",
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/bot123/sendMessage":
			w.Write([]byte(`{"ok":true,"result":{"message_id":101}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "999")
	handler := func(ctx context.Context, cmd string) string {
		mu.Lock()
		lastHandled = cmd
		mu.Unlock()
		return "schedule response"
	}

	_, err = tn.PollOnce(context.Background(), 0, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}

	mu.Lock()
	handled := lastHandled
	mu.Unlock()

	if handled != "/jadwal" {
		t.Errorf("expected handler to receive /jadwal for '📅 Jadwal', got %q", handled)
	}
}


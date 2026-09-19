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

func waitPendingDeletions(tn *TelegramNotifier) {
	tn.deleteWg.Wait()
}

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

func TestTelegramNotifier_SingleMessageReplacementCycle(t *testing.T) {
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
		waitPendingDeletions(tn)
	}

	// Command 1 (User msg 10 -> Bot reply 501, deletes user msg 10)
	pollCommand(1, 10, "/status")
	mu.Lock()
	if len(deletedBatches) != 1 {
		t.Fatalf("expected 1 delete batch after command 1, got %d", len(deletedBatches))
	}
	if len(deletedBatches[0]) != 1 || deletedBatches[0][0] != 10 {
		t.Fatalf("expected user msg 10 deleted, got %v", deletedBatches[0])
	}
	mu.Unlock()

	// Command 2 (User msg 20 -> Bot reply 502, deletes user msg 20 and bot msg 501)
	pollCommand(2, 20, "/ping")
	mu.Lock()
	if len(deletedBatches) != 2 {
		t.Fatalf("expected 2 delete batches after command 2, got %d", len(deletedBatches))
	}
	batch2 := deletedBatches[1]
	if len(batch2) != 2 || batch2[0] != 20 || batch2[1] != 501 {
		t.Fatalf("expected [20, 501] deleted, got %v", batch2)
	}
	mu.Unlock()

	// Command 3 (User msg 30 -> Bot reply 503, deletes user msg 30 and bot msg 502)
	pollCommand(3, 30, "/help")
	mu.Lock()
	if len(deletedBatches) != 3 {
		t.Fatalf("expected 3 delete batches after command 3, got %d", len(deletedBatches))
	}
	batch3 := deletedBatches[2]
	if len(batch3) != 2 || batch3[0] != 30 || batch3[1] != 502 {
		t.Fatalf("expected [30, 502] deleted, got %v", batch3)
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
		waitPendingDeletions(tn)
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

func TestTelegramNotifier_InlineKeyboard(t *testing.T) {
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
	buttons := [][]InlineButton{
		{{Text: "📅 Jadwal", Data: "/jadwal"}, {Text: "📝 Tugas", Data: "/tugas"}},
		{{Text: "ℹ️ Status", Data: "/status"}, {Text: "❓ Bantuan", Data: "/help"}},
	}
	tn.SetInlineKeyboard(buttons)

	// 1. Interactive reply in PollOnce should include inline_keyboard markup
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
	inlineRows, ok := markup["inline_keyboard"].([]any)
	if !ok || len(inlineRows) != 2 {
		t.Fatalf("expected 2 inline keyboard rows, got %v", markup["inline_keyboard"])
	}
	row0 := inlineRows[0].([]any)
	btn0 := row0[0].(map[string]any)
	btn1 := row0[1].(map[string]any)
	if btn0["text"] != "📅 Jadwal" || btn0["callback_data"] != "/jadwal" {
		t.Errorf("unexpected button 0 in row 0: %v", btn0)
	}
	if btn1["text"] != "📝 Tugas" || btn1["callback_data"] != "/tugas" {
		t.Errorf("unexpected button 1 in row 0: %v", btn1)
	}
	lastPayload = nil
	mu.Unlock()

	// 2. Clear inline keyboard and verify reply markup is omitted
	tn.SetInlineKeyboard(nil)
	_, err = tn.PollOnce(context.Background(), 0, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}

	mu.Lock()
	if _, ok := lastPayload["reply_markup"]; ok {
		t.Errorf("expected reply_markup to be omitted after SetInlineKeyboard(nil), got %v", lastPayload["reply_markup"])
	}
	mu.Unlock()
}

func TestTelegramNotifier_PollOnce_CallbackQuery(t *testing.T) {
	var (
		mu              sync.Mutex
		answeredQueryID string
		lastHandledCmd  string
		lastEditPayload map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			resp := tgUpdatesResponse{
				Ok: true,
				Result: []tgUpdate{
					{
						UpdateID: 10,
						CallbackQuery: &tgCallbackQuery{
							ID:   "cb_query_999",
							From: tgUser{ID: 999},
							Message: &tgMessage{
								MessageID: 50,
								Chat:      tgChat{ID: 999},
								Text:      "previous bot menu",
							},
							Data: "/jadwal",
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/bot123/answerCallbackQuery":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			answeredQueryID, _ = payload["callback_query_id"].(string)
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":true}`))
		case "/bot123/editMessageText":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			lastEditPayload = payload
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":{"message_id":50}}`))
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
		lastHandledCmd = cmd
		mu.Unlock()
		return "jadwal response"
	}

	nextOffset, err := tn.PollOnce(context.Background(), 0, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}
	if nextOffset != 11 {
		t.Errorf("expected nextOffset to be 11, got %d", nextOffset)
	}

	mu.Lock()
	defer mu.Unlock()

	if answeredQueryID != "cb_query_999" {
		t.Errorf("expected answerCallbackQuery with cb_query_999, got %q", answeredQueryID)
	}
	if lastHandledCmd != "/jadwal" {
		t.Errorf("expected handled cmd /jadwal, got %q", lastHandledCmd)
	}
	if lastEditPayload == nil || lastEditPayload["text"] != "jadwal response" {
		t.Errorf("expected edit message 'jadwal response', got %v", lastEditPayload)
	}
	if mid, ok := lastEditPayload["message_id"].(float64); !ok || int64(mid) != 50 {
		t.Errorf("expected edit message_id 50, got %v", lastEditPayload["message_id"])
	}
}

func TestTelegramNotifier_PollOnce_CallbackQuery_Unauthorized(t *testing.T) {
	var (
		mu              sync.Mutex
		answeredQueryID string
		handlerCalled   bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot123/getUpdates":
			resp := tgUpdatesResponse{
				Ok: true,
				Result: []tgUpdate{
					{
						UpdateID: 15,
						CallbackQuery: &tgCallbackQuery{
							ID:   "cb_unauth",
							From: tgUser{ID: 777}, // unauthorized chat/user
							Message: &tgMessage{
								MessageID: 50,
								Chat:      tgChat{ID: 777},
								Text:      "previous bot menu",
							},
							Data: "/jadwal",
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/bot123/answerCallbackQuery":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			answeredQueryID, _ = payload["callback_query_id"].(string)
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
	handler := func(ctx context.Context, cmd string) string {
		mu.Lock()
		handlerCalled = true
		mu.Unlock()
		return "should not be called"
	}

	_, err = tn.PollOnce(context.Background(), 0, handler)
	if err != nil {
		t.Fatalf("PollOnce failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if handlerCalled {
		t.Errorf("expected handler not to be called for unauthorized callback query")
	}
	if answeredQueryID != "" {
		t.Errorf("expected unauthorized callback query not to be answered, got %q", answeredQueryID)
	}
}

func TestTelegramNotifier_EditMessageText(t *testing.T) {
	var (
		mu          sync.Mutex
		lastPayload tgEditMessagePayload
		attempts    int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123/editMessageText" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		attempts++
		curAttempt := attempts
		mu.Unlock()

		if curAttempt == 1 {
			// First attempt returns 429
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`))
			return
		}

		var payload tgEditMessagePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		lastPayload = payload
		mu.Unlock()
		w.Write([]byte(`{"ok":true,"result":{"message_id":100}}`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "999")
	markup := &tgInlineKeyboardMarkup{
		InlineKeyboard: [][]InlineButton{
			{{Text: "btn1", Data: "/btn1"}},
		},
	}

	err = tn.EditMessageText(context.Background(), 100, "updated text", markup)
	if err != nil {
		t.Fatalf("EditMessageText failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Errorf("expected 2 attempts due to 429 retry, got %d", attempts)
	}
	if lastPayload.MessageID != 100 {
		t.Errorf("expected MessageID 100, got %d", lastPayload.MessageID)
	}
	if lastPayload.Text != "updated text" {
		t.Errorf("expected Text 'updated text', got %q", lastPayload.Text)
	}
	if lastPayload.ReplyMarkup == nil {
		t.Errorf("expected ReplyMarkup to be present")
	}
}

func TestTelegramNotifier_EditMessageText_MessageNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123/editMessageText" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message"}`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "123", "999")
	err = tn.EditMessageText(context.Background(), 100, "identical text", nil)
	if err != nil {
		t.Fatalf("expected message is not modified to return nil, got %v", err)
	}
}

func BenchmarkTelegram_SplitMessage(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("Line ")
		sb.WriteString(fmt.Sprintf("%d: This is a sample Telegram message line with some formatting details\n", i))
	}
	text := sb.String()

	b.ResetTimer()
	for b.Loop() {
		_ = splitMessage(text, maxTelegramMessageLen)
	}
}

func BenchmarkTelegram_SendMessageIDs(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":{"message_id":123}}`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		b.Fatal(err)
	}
	tn := NewTelegramNotifier(client, server.URL, "token", "123")
	ctx := context.Background()
	msg := "Hello Telegram Notification Bench"

	b.ResetTimer()
	for b.Loop() {
		_, _ = tn.SendMessageIDs(ctx, msg)
	}
}

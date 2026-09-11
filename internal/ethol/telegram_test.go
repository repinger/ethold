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
		{"hello", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := parseCommand(tt.input)
		if got != tt.expected {
			t.Errorf("parseCommand(%q) = %q, expected %q", tt.input, got, tt.expected)
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

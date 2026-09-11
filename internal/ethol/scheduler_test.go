package ethol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSchedulerModes(t *testing.T) {
	cases := []struct {
		hour     int
		minute   int
		expected string
		interval time.Duration
	}{
		{2, 0, ModeNightRest, 300 * time.Second},
		{3, 59, ModeNightRest, 300 * time.Second},
		{4, 0, ModeDawn, 180 * time.Second},
		{6, 29, ModeDawn, 180 * time.Second},
		{6, 30, ModeNormal, 60 * time.Second},
		{12, 0, ModeNormal, 60 * time.Second},
		{21, 29, ModeNormal, 60 * time.Second},
		{21, 30, ModeNightRest, 300 * time.Second},
		{23, 0, ModeNightRest, 300 * time.Second},
	}

	for _, tc := range cases {
		fakeTime := time.Date(2024, 1, 15, tc.hour, tc.minute, 0, 0, WIBLocation)
		mode := GetScheduleMode(fakeTime)
		if mode.Name != tc.expected {
			t.Errorf("at %02d:%02d expected mode %s, got %s", tc.hour, tc.minute, tc.expected, mode.Name)
		}
		if mode.Interval != tc.interval {
			t.Errorf("at %02d:%02d expected interval %v, got %v", tc.hour, tc.minute, tc.interval, mode.Interval)
		}
	}
}

func TestTelegramNotifier(t *testing.T) {
	var sentPayload tgSendMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot12345/sendMessage" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&sentPayload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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

	tn := NewTelegramNotifier(client, server.URL, "12345", "999888")
	ctx := context.Background()

	err = tn.NotifyPresenceSuccess(ctx, "Matematika Diskrit", "Dr. Budi", "key-999", "Presensi berhasil")
	if err != nil {
		t.Fatalf("notify presence success err: %v", err)
	}

	if sentPayload.ChatID != "999888" {
		t.Errorf("expected chat id 999888, got %s", sentPayload.ChatID)
	}
	if sentPayload.ParseMode != "HTML" {
		t.Errorf("expected HTML parse mode, got %s", sentPayload.ParseMode)
	}
}

func TestCalculateJitter(t *testing.T) {
	// 1. Non-positive base / fraction
	if got := calculateJitter(0, 0.2); got != 0 {
		t.Errorf("expected 0 for base 0, got %v", got)
	}
	if got := calculateJitter(-10*time.Second, 0.2); got != -10*time.Second {
		t.Errorf("expected -10s for negative base, got %v", got)
	}
	if got := calculateJitter(10*time.Second, 0); got != 10*time.Second {
		t.Errorf("expected 10s for fraction 0, got %v", got)
	}
	if got := calculateJitter(10*time.Second, -0.1); got != 10*time.Second {
		t.Errorf("expected 10s for negative fraction, got %v", got)
	}

	// 2. Bound checks
	base := 100 * time.Second
	fraction := 0.10 // ±10s -> [90s, 110s]
	distinct := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		val := calculateJitter(base, fraction)
		if val < 90*time.Second || val > 110*time.Second {
			t.Fatalf("jitter %v out of bounds [90s, 110s]", val)
		}
		distinct[val] = true
	}
	if len(distinct) <= 1 {
		t.Errorf("expected jitter variance, got %d distinct value", len(distinct))
	}
}


package ethol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcademicManager_NotificationPolling(t *testing.T) {
	var (
		presenceTriggered bool
		taskTriggered     bool
		markedReadID      int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			w.Write([]byte(`{"jumlah":2}`))
		case "/api/notifikasi/mahasiswa":
			w.Write([]byte(`[
				{
					"idNotifikasi": 801,
					"kodeNotifikasi": "PRESENSI-KULIAH",
					"keterangan": "Sesi presensi Struktur Data telah dibuka",
					"status": "1"
				},
				{
					"idNotifikasi": 802,
					"kodeNotifikasi": "TUGAS-BARU",
					"keterangan": "Tugas baru Algoritma telah dirilis",
					"status": "1"
				}
			]`))
		case "/api/notifikasi/mahasiswa-baca-notif":
			var body map[string]int
			json.NewDecoder(r.Body).Decode(&body)
			markedReadID = body["idNotifikasi"]
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()

	err = am.PollNotifications(ctx, func(ket string) {
		presenceTriggered = true
	}, func(ket string) {
		taskTriggered = true
	})

	if err != nil {
		t.Fatalf("PollNotifications error: %v", err)
	}
	if !presenceTriggered {
		t.Errorf("expected presence notification callback to trigger")
	}
	if !taskTriggered {
		t.Errorf("expected task notification callback to trigger")
	}
	if markedReadID == 0 {
		t.Errorf("expected notifications to be marked as read")
	}

	// Verify deduplication: a second poll should not trigger callbacks
	presenceTriggered = false
	taskTriggered = false
	err = am.PollNotifications(ctx, func(ket string) {
		presenceTriggered = true
	}, func(ket string) {
		taskTriggered = true
	})
	if err != nil {
		t.Fatalf("PollNotifications second run error: %v", err)
	}
	if presenceTriggered || taskTriggered {
		t.Errorf("expected deduplication to prevent re-triggering callbacks")
	}

	// Verify StartNotificationPoller stops cleanly on context cancellation
	pollCtx, cancel := context.WithCancel(context.Background())
	pollerDone := make(chan struct{})
	go func() {
		am.StartNotificationPoller(pollCtx, 20*time.Millisecond, nil, nil, nil)
		close(pollerDone)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-pollerDone:
	case <-time.After(1 * time.Second):
		t.Errorf("expected StartNotificationPoller to stop on context cancel")
	}

	// Verify zero unread notifications return nil without fetching
	zeroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			w.Write([]byte(`{"jumlah":0}`))
		default:
			t.Errorf("unexpected call to %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer zeroServer.Close()

	zeroAM := NewAcademicManager(client, zeroServer.URL, 5*time.Minute)
	if err := zeroAM.PollNotifications(ctx, nil, nil); err != nil {
		t.Fatalf("expected nil error on zero unread, got %v", err)
	}

	// Verify 401 Unauthorized returns ErrUnauthorized
	unauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer unauthServer.Close()

	unauthAM := NewAcademicManager(client, unauthServer.URL, 5*time.Minute)
	if err := unauthAM.PollNotifications(ctx, nil, nil); err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestAcademicManager_NotificationPolling_StringID(t *testing.T) {
	var presenceTriggered bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			w.Write([]byte(`{"jumlah":1}`))
		case "/api/notifikasi/mahasiswa":
			w.Write([]byte(`[
				{
					"idNotifikasi": "801",
					"kodeNotifikasi": "PRESENSI-KULIAH",
					"keterangan": "Sesi presensi Struktur Data telah dibuka",
					"status": "1"
				}
			]`))
		case "/api/notifikasi/mahasiswa-baca-notif":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	err = am.PollNotifications(context.Background(), func(ket string) {
		presenceTriggered = true
	}, nil)

	if err != nil {
		t.Fatalf("PollNotifications error with string ID: %v", err)
	}
	if !presenceTriggered {
		t.Errorf("expected presence notification callback to trigger")
	}
}

func TestAcademicManager_PollNotifications_UnhandledCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			w.Write([]byte(`{"jumlah":1}`))
		case "/api/notifikasi/mahasiswa":
			w.Write([]byte(`[
				{
					"idNotifikasi": 999,
					"kodeNotifikasi": "SISTEM-INFO",
					"keterangan": "Pengumuman kampus",
					"status": "1"
				}
			]`))
		case "/api/notifikasi/mahasiswa-baca-notif":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	err = am.PollNotifications(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("PollNotifications unhandled code error: %v", err)
	}
}

func TestAcademicManager_StartNotificationPoller_RetryAuth(t *testing.T) {
	var (
		pollAttempts   int32
		ensureAuthRuns int32
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			attempt := atomic.AddInt32(&pollAttempts, 1)
			if attempt == 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"jumlah":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	pollerCtx, pollerCancel := context.WithCancel(context.Background())
	defer pollerCancel()

	ensureAuthCalled := make(chan struct{}, 1)
	go am.StartNotificationPoller(pollerCtx, 50*time.Millisecond, func(ctx context.Context) error {
		atomic.AddInt32(&ensureAuthRuns, 1)
		select {
		case ensureAuthCalled <- struct{}{}:
		default:
		}
		return nil
	}, nil, nil)

	select {
	case <-ensureAuthCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected ensureAuth to be called on poller ErrUnauthorized")
	}
	pollerCancel()

	if atomic.LoadInt32(&ensureAuthRuns) < 1 {
		t.Errorf("expected ensureAuth to run at least once")
	}
}

func TestAcademicManager_BoundedNotificationMemory(t *testing.T) {
	var currentBatch int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			w.Write([]byte(`{"jumlah": 100}`))
		case "/api/notifikasi/mahasiswa":
			batch := atomic.AddInt32(&currentBatch, 1)
			var items []map[string]any
			start := int((batch - 1) * 100)
			for i := 0; i < 100; i++ {
				items = append(items, map[string]any{
					"idNotifikasi":   start + i + 1,
					"kodeNotifikasi": "SISTEM",
					"keterangan":     "Notice",
				})
			}
			json.NewEncoder(w).Encode(items)
		case "/api/notifikasi/mahasiswa-baca-notif":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()

	// Poll 12 batches = 1200 notifications
	for i := 0; i < 12; i++ {
		if err := am.PollNotifications(ctx, nil, nil); err != nil {
			t.Fatalf("poll notifications batch %d: %v", i, err)
		}
	}

	am.mu.RLock()
	count := len(am.processedNotifIDs)
	qLen := len(am.processedNotifQueue)
	am.mu.RUnlock()

	if count > 1000 {
		t.Errorf("expected processedNotifIDs to be bounded <= 1000, got %d", count)
	}
	if qLen > 1000 {
		t.Errorf("expected processedNotifQueue to be bounded <= 1000, got %d", qLen)
	}
	if count != qLen {
		t.Errorf("expected processedNotifIDs count (%d) to match queue length (%d)", count, qLen)
	}
}

func TestCalculatePollerInterval(t *testing.T) {
	base := 30 * time.Second
	minExpected := 24 * time.Second
	maxExpected := 36 * time.Second

	distinct := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		got := calculatePollerInterval(base)
		if got < minExpected || got > maxExpected {
			t.Fatalf("interval %v outside [%v, %v]", got, minExpected, maxExpected)
		}
		distinct[got] = true
	}
	if len(distinct) <= 1 {
		t.Errorf("expected distinct jittered intervals, got %d", len(distinct))
	}
	if got := calculatePollerInterval(0); got != 0 {
		t.Errorf("expected 0 for 0 base, got %v", got)
	}
}

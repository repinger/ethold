package ethol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcademicManager_NotificationPolling(t *testing.T) {
	var (
		presenceTriggered bool
		taskTriggered     bool
		markedReadID      string
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
			var body map[string]string
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
	}, nil)

	if err != nil {
		t.Fatalf("PollNotifications error: %v", err)
	}
	if !presenceTriggered {
		t.Errorf("expected presence notification callback to trigger")
	}
	if !taskTriggered {
		t.Errorf("expected task notification callback to trigger")
	}
	if markedReadID == "" {
		t.Errorf("expected notifications to be marked as read")
	}

	// Verify deduplication: a second poll should not trigger callbacks
	presenceTriggered = false
	taskTriggered = false
	err = am.PollNotifications(ctx, func(ket string) {
		presenceTriggered = true
	}, func(ket string) {
		taskTriggered = true
	}, nil)
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
		am.StartNotificationPoller(pollCtx, 20*time.Millisecond, nil, nil, nil, nil)
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
	if err := zeroAM.PollNotifications(ctx, nil, nil, nil); err != nil {
		t.Fatalf("expected nil error on zero unread, got %v", err)
	}

	// Verify 401 Unauthorized returns ErrUnauthorized
	unauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer unauthServer.Close()

	unauthAM := NewAcademicManager(client, unauthServer.URL, 5*time.Minute)
	if err := unauthAM.PollNotifications(ctx, nil, nil, nil); !errors.Is(err, ErrUnauthorized) {
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
	}, nil, nil)

	if err != nil {
		t.Fatalf("PollNotifications error with string ID: %v", err)
	}
	if !presenceTriggered {
		t.Errorf("expected presence notification callback to trigger")
	}
}

func TestAcademicManager_NotificationPolling_UUIDAndStatusFilter(t *testing.T) {
	var (
		presenceTriggered bool
		taskTriggered     bool
		markedReadIDs     []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			w.Write([]byte(`{"jumlah":1}`))
		case "/api/notifikasi/mahasiswa":
			w.Write([]byte(`[
				{
					"idNotifikasi": "376cfdf5-59d9-4f4b-8fdb-169593e5e769-23573",
					"kodeNotifikasi": "PRESENSI-KULIAH",
					"keterangan": "Dosen telah membuka presensi",
					"status": "1"
				},
				{
					"idNotifikasi": "old-uuid-99999",
					"kodeNotifikasi": "TUGAS-BARU",
					"keterangan": "Tugas lama",
					"status": "2"
				}
			]`))
		case "/api/notifikasi/mahasiswa-baca-notif":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			markedReadIDs = append(markedReadIDs, body["idNotifikasi"])
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
	}, func(ket string) {
		taskTriggered = true
	}, nil)

	if err != nil {
		t.Fatalf("PollNotifications error: %v", err)
	}
	if !presenceTriggered {
		t.Errorf("expected presence callback for unread notification (status 1)")
	}
	if taskTriggered {
		t.Errorf("expected task callback NOT to trigger for read notification (status 2)")
	}
	if len(markedReadIDs) != 1 || markedReadIDs[0] != "376cfdf5-59d9-4f4b-8fdb-169593e5e769-23573" {
		t.Errorf("expected UUID marked as read, got %v", markedReadIDs)
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
	var (
		otherTriggered bool
		receivedKode   string
		receivedKet    string
	)
	err = am.PollNotifications(context.Background(), nil, nil, func(kode, ket string) {
		otherTriggered = true
		receivedKode = kode
		receivedKet = ket
	})
	if err != nil {
		t.Fatalf("PollNotifications unhandled code error: %v", err)
	}
	if !otherTriggered {
		t.Errorf("expected other notification callback to trigger")
	}
	if receivedKode != "SISTEM-INFO" || receivedKet != "Pengumuman kampus" {
		t.Errorf("unexpected callback args: kode=%q ket=%q", receivedKode, receivedKet)
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
	}, nil, nil, nil)

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
		if err := am.PollNotifications(ctx, nil, nil, nil); err != nil {
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

func TestAcademicManager_InitNotificationBaseline(t *testing.T) {
	var readMarked []string
	var serverItems []map[string]any
	var mu sync.Mutex

	serverItems = []map[string]any{
		{
			"idNotifikasi":   "baseline-1",
			"kodeNotifikasi": "PRESENSI-KULIAH",
			"keterangan":     "Baseline presensi 1",
			"status":         "1",
		},
		{
			"idNotifikasi":   "baseline-2",
			"kodeNotifikasi": "TUGAS-KULIAH",
			"keterangan":     "Baseline tugas 2",
			"status":         "1",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			_ = json.NewEncoder(w).Encode(map[string]int{"jumlah": len(serverItems)})
		case "/api/notifikasi/mahasiswa":
			_ = json.NewEncoder(w).Encode(serverItems)
		case "/api/notifikasi/mahasiswa-baca-notif":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			readMarked = append(readMarked, body["idNotifikasi"])
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

	// 1. Establish baseline
	if err := am.InitNotificationBaseline(ctx); err != nil {
		t.Fatalf("InitNotificationBaseline failed: %v", err)
	}

	mu.Lock()
	if len(readMarked) != 0 {
		t.Errorf("expected no items marked read during baseline, got %v", readMarked)
	}
	mu.Unlock()

	// 2. Poll immediately: baseline items should not trigger callbacks
	var triggered []string
	err = am.PollNotifications(ctx, func(ket string) {
		triggered = append(triggered, ket)
	}, func(ket string) {
		triggered = append(triggered, ket)
	}, nil)
	if err != nil {
		t.Fatalf("PollNotifications failed: %v", err)
	}
	if len(triggered) != 0 {
		t.Errorf("expected 0 callbacks for baseline items, got %v", triggered)
	}

	// 3. Introduce a new notification
	mu.Lock()
	serverItems = append(serverItems, map[string]any{
		"idNotifikasi":   "new-item-3",
		"kodeNotifikasi": "PRESENSI-KULIAH",
		"keterangan":     "New presensi 3",
		"status":         "1",
	})
	mu.Unlock()

	err = am.PollNotifications(ctx, func(ket string) {
		triggered = append(triggered, ket)
	}, nil, nil)
	if err != nil {
		t.Fatalf("PollNotifications failed on new item: %v", err)
	}
	if len(triggered) != 1 || triggered[0] != "New presensi 3" {
		t.Errorf("expected only new notification to trigger, got %v", triggered)
	}

	mu.Lock()
	if len(readMarked) != 1 || readMarked[0] != "new-item-3" {
		t.Errorf("expected new notification marked read, got %v", readMarked)
	}
	mu.Unlock()
}

func TestAcademicManager_StartNotificationPoller_Baseline(t *testing.T) {
	var mu sync.Mutex
	serverItems := []map[string]any{
		{
			"idNotifikasi":   "old-item-1",
			"kodeNotifikasi": "PRESENSI-KULIAH",
			"keterangan":     "Old presensi 1",
			"status":         "1",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case "/api/notifikasi/mahasiswa-belum-baca":
			_ = json.NewEncoder(w).Encode(map[string]int{"jumlah": len(serverItems)})
		case "/api/notifikasi/mahasiswa":
			_ = json.NewEncoder(w).Encode(serverItems)
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
	pollerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan string, 10)
	go am.StartNotificationPoller(pollerCtx, 20*time.Millisecond, nil, func(ket string) {
		received <- ket
	}, nil, nil)

	// Baseline cycle executes; old item should NOT be sent to channel
	select {
	case ket := <-received:
		t.Fatalf("unexpected notification from baseline item: %s", ket)
	case <-time.After(60 * time.Millisecond):
	}

	// Now add a new item
	mu.Lock()
	serverItems = append(serverItems, map[string]any{
		"idNotifikasi":   "post-startup-2",
		"kodeNotifikasi": "PRESENSI-KULIAH",
		"keterangan":     "Post startup 2",
		"status":         "1",
	})
	mu.Unlock()

	// New item should arrive
	select {
	case ket := <-received:
		if ket != "Post startup 2" {
			t.Errorf("expected 'Post startup 2', got %q", ket)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for post-startup notification")
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

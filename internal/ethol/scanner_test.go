package ethol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScannerScanOnce(t *testing.T) {
	var (
		presenceSubmitted int32
		tgSent            int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)

		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `
					<form id="fm1" action="/cas/login" method="post">
						<input type="text" name="username" />
						<input type="password" name="password" />
					</form>
				`)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)

		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))

		case "/api/auth/refresh":
			w.WriteHeader(http.StatusOK)

		case "/api/auth/config":
			json.NewEncoder(w).Encode(map[string]any{"tahun_aktif": 2024, "semester_aktif": 1})

		case "/api/kuliah":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"nomor":       501,
					"jenisSchema": 0,
					"dosen":       "Dr. Tech",
					"matakuliah":  map[string]any{"nama": "Algoritma & Pemrograman"},
				},
			})

		case "/api/presensi/aktif-kuliah":
			json.NewEncoder(w).Encode([]map[string]any{{"key": "test-key-501"}})

		case "/api/presensi/mahasiswa":
			atomic.AddInt32(&presenceSubmitted, 1)
			json.NewEncoder(w).Encode(map[string]any{
				"sukses": true,
				"pesan":  "Presensi berhasil dicatat",
			})

		case "/bottest-token/sendMessage":
			atomic.AddInt32(&tgSent, 1)
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

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	state, err := NewStateManager(statePath)
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "budi", "pass")
	courses := NewCourseManager(client, server.URL, 5*time.Minute)
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "test-token", "12345")

	scanner := NewScanner(auth, courses, presence, nil, state, notifier, 2)
	scanner.minDelay, scanner.maxDelay = 0, 0
	ctx := context.Background()

	// 1. First scan: should submit presence
	attended, err := scanner.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("first scan err: %v", err)
	}
	if attended != 1 {
		t.Errorf("expected 1 attended, got %d", attended)
	}
	if atomic.LoadInt32(&presenceSubmitted) != 1 {
		t.Errorf("expected 1 submission, got %d", atomic.LoadInt32(&presenceSubmitted))
	}
	if atomic.LoadInt32(&tgSent) != 1 {
		t.Errorf("expected 1 telegram notification, got %d", atomic.LoadInt32(&tgSent))
	}

	// 2. Second scan: should skip already attended key
	attended2, err := scanner.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("second scan err: %v", err)
	}
	if attended2 != 0 {
		t.Errorf("expected 0 attended on second scan, got %d", attended2)
	}
	if atomic.LoadInt32(&presenceSubmitted) != 1 {
		t.Errorf("expected no additional submission, got %d", atomic.LoadInt32(&presenceSubmitted))
	}

	// 3. /today should contain course name, dosen, and key
	todayReply := scanner.HandleTelegramCommand(ctx, "/today")
	if !strings.Contains(todayReply, "Algoritma &amp; Pemrograman") || !strings.Contains(todayReply, "Dr. Tech") || !strings.Contains(todayReply, "test-key-501") {
		t.Errorf("expected /today to contain course name, dosen, and key, got %q", todayReply)
	}
}

func TestScanner_StatusAndThreadSafety(t *testing.T) {
	state, err := NewStateManager(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, "http://dummy", "user", "pass")
	courses := NewCourseManager(client, "http://dummy", 10*time.Minute)
	presence := NewPresenceEngine(client, "http://dummy")
	notifier := NewTelegramNotifier(client, "http://dummy", "token", "123")

	scanner := NewScanner(auth, courses, presence, nil, state, notifier, 1)

	status := scanner.Status()
	if !status.LastScanTime.IsZero() {
		t.Errorf("expected zero last scan time, got %v", status.LastScanTime)
	}
	if status.TotalAttended != 0 {
		t.Errorf("expected 0 total attended, got %d", status.TotalAttended)
	}
}

func TestScanner_PresenceDelay(t *testing.T) {
	var presenceSubmitted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login" method="post"><input type="text" name="username" /><input type="password" name="password" /></form>`)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))
		case "/api/auth/refresh":
			w.WriteHeader(http.StatusOK)
		case "/api/auth/config":
			json.NewEncoder(w).Encode(map[string]any{"tahun_aktif": 2024, "semester_aktif": 1})
		case "/api/kuliah":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"nomor":       601,
					"jenisSchema": 0,
					"dosen":       "Dr. Delay",
					"matakuliah":  map[string]any{"nama": "Sistem Terdistribusi"},
				},
			})
		case "/api/presensi/aktif-kuliah":
			json.NewEncoder(w).Encode([]map[string]any{{"key": "delay-key-601"}})
		case "/api/presensi/mahasiswa":
			presenceSubmitted.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"sukses": true, "pesan": "Hadir"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	state, err := NewStateManager(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "budi", "pass")
	courses := NewCourseManager(client, server.URL, 5*time.Minute)
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "token", "chat")

	scanner := NewScanner(auth, courses, presence, nil, state, notifier, 1)

	// Verify default delay is 2s to 10s
	minD, maxD := scanner.PresenceDelay()
	if minD != 2*time.Second || maxD != 10*time.Second {
		t.Errorf("expected default delay 2s-10s, got %v-%v", minD, maxD)
	}

	// 1. Verify delay executes within [min, max] range
	scanner.minDelay = 50 * time.Millisecond
	scanner.maxDelay = 80 * time.Millisecond
	start := time.Now()
	attended, err := scanner.ScanOnce(context.Background())
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if attended != 1 {
		t.Errorf("expected 1 attended, got %d", attended)
	}
	if presenceSubmitted.Load() != 1 {
		t.Errorf("expected 1 submission, got %d", presenceSubmitted.Load())
	}
	if duration < 45*time.Millisecond {
		t.Errorf("expected delay at least 50ms, got %v", duration)
	}

	// 2. Verify context cancellation aborts during delay
	state2, err := NewStateManager(filepath.Join(t.TempDir(), "state2.json"))
	if err != nil {
		t.Fatal(err)
	}
	scanner2 := NewScanner(auth, courses, presence, nil, state2, notifier, 1)
	scanner2.minDelay = 500 * time.Millisecond
	scanner2.maxDelay = 1 * time.Second

	ctxCancel, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = scanner2.ScanOnce(ctxCancel)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("expected context error on cancelled scan, got %v", err)
	}
}

func TestScanner_MaterialsAndRosterCommands(t *testing.T) {
	var sentMessages []string
	var msgMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `
					<form id="fm1" action="/cas/login" method="post">
						<input type="text" name="username" />
						<input type="password" name="password" />
					</form>
				`)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))
		case "/api/auth/refresh":
			w.WriteHeader(http.StatusOK)
		case "/api/auth/config":
			json.NewEncoder(w).Encode(map[string]any{"tahun_aktif": 2024, "semester_aktif": 1})
		case "/api/kuliah":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"nomor":       501,
					"jenisSchema": 0,
					"dosen":       "Dr. Tech",
					"matakuliah":  map[string]any{"nama": "Algoritma & Pemrograman"},
				},
			})
		case "/api/materi":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":    1,
					"title": "Bab 1: Dasar Algoritma",
					"path":  "https://ethol.pens.ac.id/storage/materi/bab1.pdf",
					"tipe":  1,
				},
			})
		case "/api/video":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":    2,
					"judul": "Video Kuliah 1",
					"path":  "https://youtube.com/watch?v=algo1",
				},
			})
		case "/api/presensi/aktif-kuliah":
			json.NewEncoder(w).Encode([]map[string]any{{"key": "active-key-501"}})
		case "/api/presensi/daftar-mahasiswa-hadir-kuliah":
			json.NewEncoder(w).Encode([]map[string]any{
				{"nrp": "3120600001", "nama": "Budi"},
				{"nrp": "3120600002", "nama": "Siti"},
			})
		case "/api/presensi/daftar-mahasiswa-tidak-hadir-kuliah":
			json.NewEncoder(w).Encode([]map[string]any{
				{"nrp": "3120600003", "nama": "Dewi"},
			})
		case "/api/presensi/jumlah-mahasiswa-per-kuliah":
			json.NewEncoder(w).Encode(map[string]any{"jumlah": 25})
		case "/api/presensi/mahasiswa":
			json.NewEncoder(w).Encode(map[string]any{"sukses": true, "pesan": "Presensi berhasil"})
		case "/bottest-token/sendMessage":
			var payload struct {
				Text string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			msgMu.Lock()
			sentMessages = append(sentMessages, payload.Text)
			msgMu.Unlock()
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

	state, err := NewStateManager(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "budi", "pass")
	courses := NewCourseManager(client, server.URL, 5*time.Minute)
	presence := NewPresenceEngine(client, server.URL)
	academic := NewAcademicManager(client, server.URL, 5*time.Minute)
	notifier := NewTelegramNotifier(client, server.URL, "test-token", "12345")

	scanner := NewScanner(auth, courses, presence, academic, state, notifier, 1)
	scanner.minDelay, scanner.maxDelay = 0, 0
	ctx := context.Background()

	// 1. Test /help contains /materi and /presensi_kelas
	helpResp := scanner.HandleTelegramCommand(ctx, "/help")
	if !strings.Contains(helpResp, "/materi") || !strings.Contains(helpResp, "/presensi_kelas") {
		t.Errorf("expected /help to mention /materi and /presensi_kelas, got: %s", helpResp)
	}

	// 2. Test /materi command
	materiResp := scanner.HandleTelegramCommand(ctx, "/materi")
	if !strings.Contains(materiResp, "Algoritma &amp; Pemrograman") ||
		!strings.Contains(materiResp, "Bab 1: Dasar Algoritma") ||
		!strings.Contains(materiResp, "Video Kuliah 1") {
		t.Errorf("unexpected /materi response: %s", materiResp)
	}

	// 3. Test /presensi_kelas command with active class
	rosterResp := scanner.HandleTelegramCommand(ctx, "/presensi_kelas")
	if !strings.Contains(rosterResp, "Algoritma &amp; Pemrograman") ||
		!strings.Contains(rosterResp, "active-key-501") ||
		!strings.Contains(rosterResp, "2 / 25 Mahasiswa Hadir") ||
		!strings.Contains(rosterResp, "Budi") ||
		!strings.Contains(rosterResp, "Belum Presensi (1)") ||
		!strings.Contains(rosterResp, "Dewi") {
		t.Errorf("unexpected /presensi_kelas response: %s", rosterResp)
	}

	// 4. Test presence success notification includes roster count
	attended, err := scanner.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce error: %v", err)
	}
	if attended != 1 {
		t.Errorf("expected 1 attended, got %d", attended)
	}

	msgMu.Lock()
	defer msgMu.Unlock()
	if len(sentMessages) != 1 {
		t.Fatalf("expected 1 telegram message sent, got %d", len(sentMessages))
	}
	if !strings.Contains(sentMessages[0], "Presensi berhasil (2/25 hadir)") {
		t.Errorf("expected enriched roster count in success alert, got: %s", sentMessages[0])
	}
}

func TestScannerCalculateScanInterval(t *testing.T) {
	scanner := &Scanner{}
	base := 60 * time.Second

	// Check bounds
	minExpected := 51 * time.Second
	maxExpected := 69 * time.Second

	distinct := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		got := scanner.calculateScanInterval(base)
		if got < minExpected || got > maxExpected {
			t.Fatalf("interval %v outside expected range [%v, %v]", got, minExpected, maxExpected)
		}
		distinct[got] = true
	}

	if len(distinct) <= 1 {
		t.Errorf("expected jitter variance, but got only %d distinct value(s)", len(distinct))
	}

	// Non-positive interval preserved
	if got := scanner.calculateScanInterval(0); got != 0 {
		t.Errorf("expected 0, got %v", got)
	}
}

func TestPrepareCourseQueue(t *testing.T) {
	courses := []Course{
		{Nomor: 101, Matakuliah: map[string]any{"nama": "Math"}},
		{Nomor: 102, Matakuliah: map[string]any{"nama": "Physics"}},
		{Nomor: 103, Matakuliah: map[string]any{"nama": "Chemistry"}},
		{Nomor: 104, Matakuliah: map[string]any{"nama": "Biology"}},
		{Nomor: 105, Matakuliah: map[string]any{"nama": "History"}},
	}

	// 1. Edge cases
	if len(prepareCourseQueue(nil, nil, 0)) != 0 {
		t.Errorf("expected empty queue for nil input")
	}
	single := []Course{{Nomor: 101}}
	if res := prepareCourseQueue(nil, single, 101); len(res) != 1 || res[0].Nomor != 101 {
		t.Errorf("expected single element queue")
	}

	// 2. Active course pinning: active 103 must always be at index 0
	orders := make(map[string]bool)
	var dstBuf []Course
	for i := 0; i < 50; i++ {
		queue := prepareCourseQueue(dstBuf, courses, 103)
		dstBuf = queue
		if len(queue) != len(courses) {
			t.Fatalf("expected len %d, got %d", len(courses), len(queue))
		}
		if queue[0].Nomor != 103 {
			t.Fatalf("expected active course 103 at index 0, got %d", queue[0].Nomor)
		}
		var ids []string
		for _, c := range queue[1:] {
			ids = append(ids, fmt.Sprintf("%d", c.Nomor))
		}
		orders[strings.Join(ids, ",")] = true
	}
	if len(orders) <= 1 {
		t.Errorf("expected shuffled remaining queue, got only %d distinct order(s)", len(orders))
	}

	// 3. No active course: entire queue shuffled
	allOrders := make(map[string]bool)
	for i := 0; i < 50; i++ {
		queue := prepareCourseQueue(nil, courses, 0)
		var ids []string
		for _, c := range queue {
			ids = append(ids, fmt.Sprintf("%d", c.Nomor))
		}
		allOrders[strings.Join(ids, ",")] = true
	}
	if len(allOrders) <= 1 {
		t.Errorf("expected shuffled full queue, got only %d distinct order(s)", len(allOrders))
	}
}

func TestScannerWorkerStagger(t *testing.T) {
	scanner := NewScanner(nil, nil, nil, nil, nil, nil, 2)
	minS, maxS := scanner.WorkerStagger()
	if minS != 50*time.Millisecond || maxS != 250*time.Millisecond {
		t.Errorf("expected default stagger [50ms, 250ms], got [%v, %v]", minS, maxS)
	}

	scanner.minStagger = 10 * time.Millisecond
	scanner.maxStagger = 50 * time.Millisecond
	distinct := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		d := scanner.calculateWorkerStagger()
		if d < 10*time.Millisecond || d > 50*time.Millisecond {
			t.Fatalf("stagger %v outside range [10ms, 50ms]", d)
		}
		distinct[d] = true
	}
	if len(distinct) <= 1 {
		t.Errorf("expected jitter variance, got only %d distinct value(s)", len(distinct))
	}
}

func TestScanner_StatusFormattingDetails(t *testing.T) {
	ctx := context.Background()
	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	stateFile := filepath.Join(t.TempDir(), "state.json")
	state, err := NewStateManager(stateFile)
	if err != nil {
		t.Fatal(err)
	}

	// Record a presence today
	todayKey := fmt.Sprintf("%s-course101-session1", TodayDate(NowWIB()))
	if err := state.AddRecord(PresenceRecord{
		Key:        todayKey,
		CourseName: "Algoritma",
		Time:       NowWIB().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, "http://dummy", "user", "pass")
	courses := NewCourseManager(client, "http://dummy", 10*time.Minute)
	presence := NewPresenceEngine(client, "http://dummy")
	notifier := NewTelegramNotifier(client, "http://dummy", "token", "123")
	scanner := NewScanner(auth, courses, presence, nil, state, notifier, 2)

	// 1. Initial status before scan or login
	reply := scanner.HandleTelegramCommand(ctx, "/status")
	if !strings.Contains(reply, "Status Sistem") {
		t.Errorf("expected 'Status Sistem', got: %s", reply)
	}
	if !strings.Contains(reply, "Pengguna:</b> Belum login") {
		t.Errorf("expected 'Belum login', got: %s", reply)
	}
	if !strings.Contains(reply, "Hari Ini:</b> 1 entri") {
		t.Errorf("expected '1 entri', got: %s", reply)
	}
	if strings.Contains(reply, "Diagnostik:") || strings.Contains(reply, "Worker:</b>") {
		t.Errorf("expected diagnostik/worker removed from /status, got: %s", reply)
	}
	if !strings.Contains(reply, "Hasil:</b> -") {
		t.Errorf("expected 'Hasil:</b> -', got: %s", reply)
	}

	// 2. Set last scan info (successful)
	scanner.statusMu.Lock()
	scanner.lastScanTime = NowWIB()
	scanner.lastScanDur = 150 * time.Millisecond
	scanner.lastScanErr = nil
	scanner.lastAttended = 1
	scanner.statusMu.Unlock()

	reply = scanner.HandleTelegramCommand(ctx, "/status")
	if !strings.Contains(reply, "Hasil:</b> ✅ Berhasil (1 sesi baru)") {
		t.Errorf("expected success scan result, got: %s", reply)
	}
	if !strings.Contains(reply, "150ms") {
		t.Errorf("expected scan duration in reply, got: %s", reply)
	}

	// 3. Set last scan info (failure)
	scanner.statusMu.Lock()
	scanner.lastScanErr = errors.New("connection timeout")
	scanner.statusMu.Unlock()

	reply = scanner.HandleTelegramCommand(ctx, "/status")
	if !strings.Contains(reply, "Hasil:</b> ❌ Gagal (connection timeout)") {
		t.Errorf("expected failure scan result, got: %s", reply)
	}
}

func TestScannerAutoPresenceDisabled(t *testing.T) {
	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, "http://dummy", "user", "pass")
	courses := NewCourseManager(client, "http://dummy", 10*time.Minute)
	notifier := NewTelegramNotifier(client, "http://dummy", "token", "123")

	// presence = nil and state = nil represents auto-presence disabled mode
	scanner := NewScanner(auth, courses, nil, nil, nil, notifier, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Gated commands should return disabled message
	for _, cmd := range []string{"/check", "/pause", "/resume", "/today"} {
		reply := scanner.HandleTelegramCommand(ctx, cmd)
		if !strings.Contains(reply, "Auto-presensi tidak aktif") {
			t.Errorf("cmd %s: expected disabled notice, got %q", cmd, reply)
		}
	}

	// Help should omit presence-specific commands
	helpReply := scanner.HandleTelegramCommand(ctx, "/help")
	for _, omitted := range []string{"/check", "/pause", "/resume", "/today"} {
		if strings.Contains(helpReply, omitted) {
			t.Errorf("help reply should not contain %s when auto-presence is disabled, got %q", omitted, helpReply)
		}
	}
	// Help should still include info/academic commands
	for _, expected := range []string{"/status", "/courses", "/whoami", "/ping", "/help"} {
		if !strings.Contains(helpReply, expected) {
			t.Errorf("help reply should contain %s, got %q", expected, helpReply)
		}
	}

	// Status should indicate auto-presence is disabled
	statusReply := scanner.HandleTelegramCommand(ctx, "/status")
	if !strings.Contains(statusReply, "Tidak Aktif") && !strings.Contains(statusReply, "tidak aktif") {
		t.Errorf("expected status to show inactive auto-presence, got: %s", statusReply)
	}

	// ScanOnce should return an error
	_, scanErr := scanner.ScanOnce(ctx)
	if scanErr == nil {
		t.Errorf("expected ScanOnce to fail when presence is disabled, got nil")
	}

	// Run should exit cleanly when context is cancelled
	runDone := make(chan error, 1)
	runCtx, runCancel := context.WithCancel(context.Background())
	go func() {
		runDone <- scanner.Run(runCtx)
	}()
	runCancel()

	select {
	case err := <-runDone:
		if err != nil {
			t.Errorf("expected Run to exit cleanly, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Run timed out waiting for context cancellation")
	}
}

func TestScannerComputeScanPlanUnauthorizedRetry(t *testing.T) {
	var (
		authAttempts atomic.Int32
		configCalls  atomic.Int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login" method="post"><input name="username"/><input name="password"/></form>`)
				return
			}
			authAttempts.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))
		case "/api/auth/config":
			calls := configCalls.Add(1)
			if calls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"tahun_aktif": 2024, "semester_aktif": 1})
		case "/api/kuliah":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"nomor":       501,
					"jenisSchema": 0,
					"dosen":       "Dr. Tech",
					"matakuliah":  map[string]any{"nama": "Algoritma"},
				},
			})
		case "/api/jadwal/jadwal-online":
			json.NewEncoder(w).Encode([]map[string]any{})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	courses := NewCourseManager(client, server.URL, 10*time.Minute)
	academic := NewAcademicManager(client, server.URL, 10*time.Minute)
	state, err := NewStateManager(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")

	scanner := NewScanner(auth, courses, presence, academic, state, notifier, 1)
	ctx := context.Background()

	plan := scanner.computeScanPlan(ctx, time.Now())
	if authAttempts.Load() != 1 {
		t.Errorf("expected 1 auth attempt upon 401 in computeScanPlan, got %d", authAttempts.Load())
	}
	if len(plan.Courses) == 0 && configCalls.Load() < 2 {
		t.Errorf("expected plan to succeed with retried active period, got 0 courses and %d config calls", configCalls.Load())
	}
}

func TestScanner_OutageNotification_ServerErrorAndRecovery(t *testing.T) {
	var (
		serverFail   atomic.Bool
		sentMessages []string
		sentMu       sync.Mutex
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/bottoken/sendMessage":
			var payload tgSendMessagePayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sentMu.Lock()
			sentMessages = append(sentMessages, payload.Text)
			sentMu.Unlock()
			w.Write([]byte(`{"ok":true}`))
			return

		case "/api/auth/refresh":
			if serverFail.Load() {
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte("502 Bad Gateway"))
				return
			}
			w.WriteHeader(http.StatusOK)

		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)

		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login" method="post"><input type="text" name="username"/><input type="password" name="password"/></form>`)
				return
			}
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)

		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))

		case "/api/kuliah":
			if serverFail.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{})
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	if _, err := auth.Login(context.Background()); err != nil {
		t.Fatal(err)
	}

	courses := NewCourseManager(client, server.URL, 10*time.Minute)
	state, err := NewStateManager(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")
	scanner := NewScanner(auth, courses, presence, nil, state, notifier, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initial success - no alerts
	scanner.handleScanSuccess(ctx)
	sentMu.Lock()
	if len(sentMessages) != 0 {
		t.Fatalf("expected 0 messages on initial success, got %d", len(sentMessages))
	}
	sentMu.Unlock()

	// 2. Server fails with 502
	err502 := fmt.Errorf("refresh session failed: HTTP 502 (Bad Gateway)")
	scanner.handleScanError(ctx, err502)

	sentMu.Lock()
	if len(sentMessages) != 1 {
		t.Fatalf("expected 1 error alert, got %d", len(sentMessages))
	}
	if !strings.Contains(sentMessages[0], "GANGGUAN SERVER ETHOL") {
		t.Errorf("expected GANGGUAN SERVER ETHOL in alert, got %s", sentMessages[0])
	}
	sentMu.Unlock()

	// 3. Server continues failing - de-duplicate, no second alert
	scanner.handleScanError(ctx, err502)
	sentMu.Lock()
	if len(sentMessages) != 1 {
		t.Fatalf("expected still 1 alert (de-duplicated), got %d", len(sentMessages))
	}
	sentMu.Unlock()

	// 4. Server recovers
	scanner.handleScanSuccess(ctx)
	sentMu.Lock()
	if len(sentMessages) != 2 {
		t.Fatalf("expected 2 messages (error + recovery), got %d", len(sentMessages))
	}
	if !strings.Contains(sentMessages[1], "LAYANAN ETHOL PULIH") {
		t.Errorf("expected LAYANAN ETHOL PULIH in recovery, got %s", sentMessages[1])
	}
	sentMu.Unlock()

	// 5. Success continues - no second recovery message
	scanner.handleScanSuccess(ctx)
	sentMu.Lock()
	if len(sentMessages) != 2 {
		t.Fatalf("expected still 2 messages, got %d", len(sentMessages))
	}
	sentMu.Unlock()
}

func TestScanner_OutageNotification_AuthFailure(t *testing.T) {
	var (
		sentMessages []string
		sentMu       sync.Mutex
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/bottoken/sendMessage" {
			var payload tgSendMessagePayload
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sentMu.Lock()
			sentMessages = append(sentMessages, payload.Text)
			sentMu.Unlock()
			w.Write([]byte(`{"ok":true}`))
			return
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	courses := NewCourseManager(client, server.URL, 10*time.Minute)
	state, err := NewStateManager(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")
	scanner := NewScanner(auth, courses, presence, nil, state, notifier, 1)

	ctx := context.Background()

	// 1. Auth error occurs
	authErr := fmt.Errorf("ensure session: %w", ErrUnauthorized)
	scanner.handleScanError(ctx, authErr)

	sentMu.Lock()
	if len(sentMessages) != 1 {
		t.Fatalf("expected 1 auth failure alert, got %d", len(sentMessages))
	}
	if !strings.Contains(sentMessages[0], "GAGAL AUTENTIKASI") {
		t.Errorf("expected GAGAL AUTENTIKASI in alert, got %s", sentMessages[0])
	}
	sentMu.Unlock()

	// 2. Auth error repeats - suppressed
	scanner.handleScanError(ctx, authErr)
	sentMu.Lock()
	if len(sentMessages) != 1 {
		t.Fatalf("expected still 1 alert (de-duplicated), got %d", len(sentMessages))
	}
	sentMu.Unlock()

	// 3. Successful scan resets auth failure state
	scanner.handleScanSuccess(ctx)

	// 4. Next auth error triggers alert again
	scanner.handleScanError(ctx, authErr)
	sentMu.Lock()
	if len(sentMessages) != 2 {
		t.Fatalf("expected 2 auth failure alerts after reset, got %d", len(sentMessages))
	}
	sentMu.Unlock()
}

func setupMockScannerWith10Courses(tb testing.TB) (*httptest.Server, *Scanner) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case r.URL.Path == "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `
					<form id="fm1" action="/cas/login" method="post">
						<input type="text" name="username" />
						<input type="password" name="password" />
					</form>
				`)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case r.URL.Path == "/api/auth/refresh":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))
		case r.URL.Path == "/api/auth/config":
			w.Write([]byte(`{"tahun_aktif":2024,"semester_aktif":1}`))
		case r.URL.Path == "/api/kuliah":
			var courses []map[string]any
			for i := 1; i <= 10; i++ {
				courses = append(courses, map[string]any{
					"nomor":       100 + i,
					"jenisSchema": 0,
					"dosen":       fmt.Sprintf("Dosen %d", i),
					"matakuliah":  map[string]any{"nama": fmt.Sprintf("Course %d", i)},
				})
			}
			json.NewEncoder(w).Encode(courses)
		case r.URL.Path == "/api/presensi/aktif-kuliah":
			w.Write([]byte(`[]`))
		case r.URL.Path == "/api/jadwal/jadwal-online":
			w.Write([]byte(`[{"nomor":1,"kuliah":101,"hari":"Senin","jam_awal":"08:00","jam_akhir":"10:00","matakuliah":{"nama":"Course 1"},"dosen":"Dosen 1"}]`))
		case r.URL.Path == "/api/tugas":
			w.Write([]byte(`[{"nomor":1,"judul":"Tugas 1","waktu":"2026-09-10 10:00:00","tutup":0,"submission":[]}]`))
		case r.URL.Path == "/api/materi":
			w.Write([]byte(`[{"nomor":1,"judul":"Materi 1"}]`))
		case r.URL.Path == "/api/video":
			w.Write([]byte(`[]`))
		case r.URL.Path == "/api/presensi/stat-beranda-mahasiswa":
			w.Write([]byte(`{"sukses":true,"data":{"totalSesi":16,"rataHadir":100}}`))
		case r.URL.Path == "/api/presensi/riwayat":
			w.Write([]byte(`[{"nomor":1,"tanggal":"10-09-2026","waktu":"08:00"}]`))
		case r.URL.Path == "/api/presensi/get-tanggal-presensi-dosen-per-semester":
			w.Write([]byte(`[{"waktu_indonesia":"10-09-2026 08:00","tanggal":"10-09-2026"}]`))
		case r.URL.Path == "/api/presensi/daftar-mahasiswa-hadir-kuliah":
			w.Write([]byte(`[{"nrp":"3120600001","nama":"Budi"}]`))
		case r.URL.Path == "/api/presensi/jumlah-mahasiswa-per-kuliah":
			w.Write([]byte(`{"data":{"jumlah":30}}`))
		case r.URL.Path == "/api/pengumuman-admin":
			w.Write([]byte(`[{"id":1,"judul":"Pengumuman","isi":"Isi"}]`))
		case r.URL.Path == "/api/ujian/daftar-ujian":
			w.Write([]byte(`[]`))
		case strings.Contains(r.URL.Path, "/sendMessage"):
			w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		default:
			w.Write([]byte(`[]`))
		}
	}))

	client, err := NewHTTPClient()
	if err != nil {
		tb.Fatal(err)
	}

	dir := tb.TempDir()
	statePath := filepath.Join(dir, "state.json")
	state, err := NewStateManager(statePath)
	if err != nil {
		tb.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "budi", "pass")
	courses := NewCourseManager(client, server.URL, 5*time.Minute)
	academic := NewAcademicManager(client, server.URL, 5*time.Minute)
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")

	scanner := NewScanner(auth, courses, presence, academic, state, notifier, 4)
	scanner.minDelay = 0
	scanner.maxDelay = 0
	scanner.minStagger = 0
	scanner.maxStagger = 0

	return server, scanner
}

func BenchmarkScanner_ScanOnce_10Courses(b *testing.B) {
	server, scanner := setupMockScannerWith10Courses(b)
	defer server.Close()
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, err := scanner.ScanOnce(ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestSoakMemory(t *testing.T) {
	server, scanner := setupMockScannerWith10Courses(t)
	defer server.Close()
	ctx := context.Background()

	commands := []string{"/status", "/jadwal", "/tugas", "/presensi_kelas", "/rekap", "/materi", "/ujian", "/pengumuman", "/debug"}

	// Warmup 10 cycles
	for i := 0; i < 10; i++ {
		_, _ = scanner.ScanOnce(ctx)
		for _, cmd := range commands {
			_ = scanner.HandleTelegramCommand(ctx, cmd)
		}
	}

	runtime.GC()
	runtime.GC()
	var mStart runtime.MemStats
	runtime.ReadMemStats(&mStart)
	goroutinesStart := runtime.NumGoroutine()

	// Soak run: 100 scan cycles + 100 command cycles
	for i := 0; i < 100; i++ {
		_, err := scanner.ScanOnce(ctx)
		if err != nil {
			t.Fatalf("scan error: %v", err)
		}
		for _, cmd := range commands {
			reply := scanner.HandleTelegramCommand(ctx, cmd)
			if reply == "" {
				t.Fatalf("empty reply for command %s", cmd)
			}
		}
	}

	runtime.GC()
	runtime.GC()
	var mEnd runtime.MemStats
	runtime.ReadMemStats(&mEnd)
	goroutinesEnd := runtime.NumGoroutine()

	t.Logf("=== SOAK TEST MEMORY REPORT ===")
	t.Logf("HeapInuse:   start=%d B (%.2f KB), end=%d B (%.2f KB), delta=%+d B",
		mStart.HeapInuse, float64(mStart.HeapInuse)/1024,
		mEnd.HeapInuse, float64(mEnd.HeapInuse)/1024,
		int64(mEnd.HeapInuse)-int64(mStart.HeapInuse))
	t.Logf("HeapAlloc:   start=%d B (%.2f KB), end=%d B (%.2f KB), delta=%+d B",
		mStart.HeapAlloc, float64(mStart.HeapAlloc)/1024,
		mEnd.HeapAlloc, float64(mEnd.HeapAlloc)/1024,
		int64(mEnd.HeapAlloc)-int64(mStart.HeapAlloc))
	t.Logf("HeapObjects: start=%d, end=%d, delta=%+d",
		mStart.HeapObjects, mEnd.HeapObjects, int64(mEnd.HeapObjects)-int64(mStart.HeapObjects))
	t.Logf("Goroutines:  start=%d, end=%d, delta=%+d",
		goroutinesStart, goroutinesEnd, goroutinesEnd-goroutinesStart)
	t.Logf("TotalAlloc:  delta=%d B (%.2f MB)",
		mEnd.TotalAlloc-mStart.TotalAlloc, float64(mEnd.TotalAlloc-mStart.TotalAlloc)/(1024*1024))

	// Retention and leak assertions
	const maxHeapGrowth = 400 * 1024 // 400 KB
	if deltaAlloc := int64(mEnd.HeapAlloc) - int64(mStart.HeapAlloc); deltaAlloc > maxHeapGrowth {
		t.Errorf("potential memory leak: HeapAlloc grew %d B (> %d B limit)", deltaAlloc, maxHeapGrowth)
	}
	if goroutinesEnd > goroutinesStart {
		t.Errorf("goroutine leak: started with %d, ended with %d", goroutinesStart, goroutinesEnd)
	}
}

func TestScanner_NotifyStartup(t *testing.T) {
	var sentPayload tgSendMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bottoken/sendMessage" {
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

	auth := NewAuthManager(client, server.URL, "budi", "pass")
	courses := NewCourseManager(client, server.URL, 5*time.Minute)
	academic := NewAcademicManager(client, server.URL, 5*time.Minute)
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")
	stateFile := filepath.Join(t.TempDir(), "state.json")
	state, err := NewStateManager(stateFile)
	if err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(auth, courses, presence, academic, state, notifier, 4)

	err = scanner.NotifyStartup(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatalf("NotifyStartup failed: %v", err)
	}

	if !strings.Contains(sentPayload.Text, "ETHOLD BOT AKTIF") {
		t.Errorf("expected header, got: %s", sentPayload.Text)
	}
	if !strings.Contains(sentPayload.Text, "v1.0.0") {
		t.Errorf("expected version, got: %s", sentPayload.Text)
	}
	if !strings.Contains(sentPayload.Text, "Auto-Presence (4 workers) [Direct Chat]") {
		t.Errorf("expected auto-presence info with direct chat tag, got: %s", sentPayload.Text)
	}

	nilScanner := NewScanner(auth, courses, presence, academic, state, nil, 4)
	if err := nilScanner.NotifyStartup(context.Background(), "v1.0.0"); err != nil {
		t.Errorf("expected nil error for nil notifier, got: %v", err)
	}
}


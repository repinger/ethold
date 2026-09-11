package ethol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	scanner.SetPresenceDelay(0, 0)
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

func TestScanner_HandleTelegramCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"nomor":1001,"nama":"Budi Santoso","nipnrp":"3120600001"}`))
		case "/api/auth/config":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"tahun_aktif":2024,"semester_aktif":1}`))
		case "/api/kuliah":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"nomor":501,"jenisSchema":0,"dosen":"Dr. Tech","matakuliah":{"nama":"Algoritma & Pemrograman"}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	state, err := NewStateManager(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	courses := NewCourseManager(client, server.URL, 10*time.Minute)
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")

	scanner := NewScanner(auth, courses, presence, nil, state, notifier, 1)
	ctx := context.Background()

	// 1. /ping
	reply := scanner.HandleTelegramCommand(ctx, "/ping")
	if reply != "🏓 Pong!" {
		t.Errorf("expected pong, got %q", reply)
	}

	// 2. /help
	reply = scanner.HandleTelegramCommand(ctx, "/help")
	for _, expected := range []string{"/status", "/check", "/courses", "/whoami", "/today", "/pause", "/resume"} {
		if !strings.Contains(reply, expected) {
			t.Errorf("expected help text to contain %s, got %q", expected, reply)
		}
	}

	// 3. /status initial
	reply = scanner.HandleTelegramCommand(ctx, "/status")
	if !strings.Contains(reply, "Status:</b> Aktif") {
		t.Errorf("expected status to show Aktif, got %q", reply)
	}

	// 4. /pause and /resume
	reply = scanner.HandleTelegramCommand(ctx, "/pause")
	if !strings.Contains(reply, "Pemindaian Otomatis Dijeda") {
		t.Errorf("expected pause message, got %q", reply)
	}
	if !scanner.Status().Paused {
		t.Errorf("expected scanner status to be paused")
	}

	reply = scanner.HandleTelegramCommand(ctx, "/status")
	if !strings.Contains(reply, "Dijeda") {
		t.Errorf("expected status to show Dijeda, got %q", reply)
	}

	reply = scanner.HandleTelegramCommand(ctx, "/resume")
	if !strings.Contains(reply, "Pemindaian Otomatis Dilanjutkan") {
		t.Errorf("expected resume message, got %q", reply)
	}
	if scanner.Status().Paused {
		t.Errorf("expected scanner status not to be paused")
	}

	// 5. /whoami
	reply = scanner.HandleTelegramCommand(ctx, "/whoami")
	if !strings.Contains(reply, "Budi Santoso") || !strings.Contains(reply, "3120600001") {
		t.Errorf("expected whoami reply to contain user info, got %q", reply)
	}

	// 6. /courses
	reply = scanner.HandleTelegramCommand(ctx, "/courses")
	if !strings.Contains(reply, "Algoritma &amp; Pemrograman") || !strings.Contains(reply, "Dr. Tech") {
		t.Errorf("expected courses reply to contain course info, got %q", reply)
	}

	// 7. /today
	reply = scanner.HandleTelegramCommand(ctx, "/today")
	if !strings.Contains(reply, "Belum ada presensi yang tercatat hari ini") {
		t.Errorf("expected empty today reply, got %q", reply)
	}

	todayStr := TodayDate(NowWIB())
	_ = state.Add(todayStr + "_presensi-key-99")
	reply = scanner.HandleTelegramCommand(ctx, "/today")
	if !strings.Contains(reply, "presensi-key-99") {
		t.Errorf("expected today reply to contain added key, got %q", reply)
	}

	_ = state.AddRecord(PresenceRecord{
		Key:        todayStr + "_presensi-key-100",
		CourseName: "Struktur Data",
		Dosen:      "Prof. Algo",
	})
	reply = scanner.HandleTelegramCommand(ctx, "/today")
	if !strings.Contains(reply, "Struktur Data") || !strings.Contains(reply, "Prof. Algo") || !strings.Contains(reply, "presensi-key-100") {
		t.Errorf("expected today reply to contain course metadata, got %q", reply)
	}

	// 8. unknown command
	reply = scanner.HandleTelegramCommand(ctx, "/unknown")
	if !strings.Contains(reply, "Perintah tidak dikenal") {
		t.Errorf("expected unknown command reply, got %q", reply)
	}
}

func TestScanner_AcademicCommands(t *testing.T) {
	var (
		presenceCheckOrder []string
		orderMu            sync.Mutex
	)

	now := NowWIB()
	dayNames := map[time.Weekday]string{
		time.Monday:    "Senin",
		time.Tuesday:   "Selasa",
		time.Wednesday: "Rabu",
		time.Thursday:  "Kamis",
		time.Friday:    "Jumat",
		time.Saturday:  "Sabtu",
		time.Sunday:    "Minggu",
	}
	todayHari := dayNames[now.Weekday()]

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
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi Santoso","nipnrp":"3120600001"}`))
		case "/api/auth/config":
			w.Write([]byte(`{"tahun_aktif":2024,"semester_aktif":1}`))
		case "/api/kuliah":
			w.Write([]byte(`[
				{"nomor":501,"jenisSchema":0,"dosen":"Dr. Tech","matakuliah":{"nama":"Algoritma & Pemrograman"}},
				{"nomor":502,"jenisSchema":0,"dosen":"Dr. OS","matakuliah":{"nama":"Sistem Operasi"}}
			]`))
		case "/api/jadwal/jadwal-online":
			fmt.Fprintf(w, `[
				{
					"hari": "%s",
					"jam_awal": "00:00",
					"jam_akhir": "23:59",
					"matakuliah": "Sistem Operasi",
					"dosen": "Dr. OS",
					"ruang": "Online",
					"nomor": 201,
					"kuliah": 502
				}
			]`, todayHari)
		case "/api/tugas":
			w.Write([]byte(`[
				{
					"title": "Tugas OS 1",
					"deadline_indonesia": "15-09-2024 23:59 WIB",
					"submission_time": null,
					"tutup": "0"
				}
			]`))
		case "/api/presensi/riwayat":
			w.Write([]byte(`[{"tanggal":"09-09-2024"}]`))
		case "/api/presensi/get-tanggal-presensi-dosen-per-semester":
			w.Write([]byte(`[{"waktu_indonesia":"09-09-2024"}]`))
		case "/api/presensi/aktif-kuliah":
			orderMu.Lock()
			presenceCheckOrder = append(presenceCheckOrder, r.URL.Query().Get("kuliah"))
			orderMu.Unlock()
			w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	state, err := NewStateManager(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	courses := NewCourseManager(client, server.URL, 10*time.Minute)
	presence := NewPresenceEngine(client, server.URL)
	academic := NewAcademicManager(client, server.URL, 10*time.Minute)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")

	scanner := NewScanner(auth, courses, presence, academic, state, notifier, 1)
	scanner.SetPresenceDelay(0, 0)
	ctx := context.Background()

	// 1. /jadwal
	replyJadwal := scanner.HandleTelegramCommand(ctx, "/jadwal")
	if !strings.Contains(replyJadwal, "Sistem Operasi") {
		t.Errorf("expected /jadwal to contain 'Sistem Operasi', got: %s", replyJadwal)
	}

	// 2. /tugas
	replyTugas := scanner.HandleTelegramCommand(ctx, "/tugas")
	if !strings.Contains(replyTugas, "Tugas OS 1") {
		t.Errorf("expected /tugas to contain 'Tugas OS 1', got: %s", replyTugas)
	}

	// 3. /rekap
	replyRekap := scanner.HandleTelegramCommand(ctx, "/rekap")
	if !strings.Contains(replyRekap, "Kehadiran") {
		t.Errorf("expected /rekap to contain 'Kehadiran', got: %s", replyRekap)
	}

	// 4. /relogin
	replyRelogin := scanner.HandleTelegramCommand(ctx, "/relogin")
	if !strings.Contains(replyRelogin, "Budi Santoso") || !strings.Contains(replyRelogin, "Berhasil") {
		t.Errorf("expected /relogin to contain success and user name, got: %s", replyRelogin)
	}

	// 5. /help includes all new commands
	replyHelp := scanner.HandleTelegramCommand(ctx, "/help")
	for _, cmd := range []string{"/jadwal", "/tugas", "/rekap", "/relogin"} {
		if !strings.Contains(replyHelp, cmd) {
			t.Errorf("expected /help to mention %s, got: %s", cmd, replyHelp)
		}
	}

	// 6. Active course prioritization in ScanOnce
	_, err = scanner.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}
	orderMu.Lock()
	defer orderMu.Unlock()
	if len(presenceCheckOrder) < 2 {
		t.Fatalf("expected at least 2 presence checks, got %d", len(presenceCheckOrder))
	}
	// Course 502 (Sistem Operasi) was active in schedule, so it must be checked first!
	if presenceCheckOrder[0] != "502" {
		t.Errorf("expected active course 502 to be checked first, got %s (order: %v)", presenceCheckOrder[0], presenceCheckOrder)
	}
}

func TestScanner_UnauthorizedRetry(t *testing.T) {
	var (
		jadwalAttempts int32
		tugasAttempts  int32
		rekapAttempts  int32
		coursesAttempts int32
		loginCount     int32
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
			atomic.AddInt32(&loginCount, 1)
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi Santoso","nipnrp":"3120600001"}`))
		case "/api/auth/config":
			w.Write([]byte(`{"tahun_aktif":2024,"semester_aktif":1}`))
		case "/api/kuliah":
			if atomic.AddInt32(&coursesAttempts, 1) == 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`[{"nomor":501,"jenisSchema":0,"dosen":"Dr. Tech","matakuliah":{"nama":"Algoritma & Pemrograman"}}]`))
		case "/api/jadwal/jadwal-online":
			if atomic.AddInt32(&jadwalAttempts, 1) == 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`[{"hari":"Senin","jam_awal":"08:00","jam_akhir":"10:00","matakuliah":"Algoritma & Pemrograman","nomor":1,"kuliah":501}]`))
		case "/api/tugas":
			if atomic.AddInt32(&tugasAttempts, 1) == 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`[{"title":"Tugas 1","deadline_indonesia":"15-09-2024 23:59 WIB","submission_time":null,"tutup":"0"}]`))
		case "/api/presensi/riwayat":
			if atomic.AddInt32(&rekapAttempts, 1) == 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`[{"tanggal":"09-09-2024"}]`))
		case "/api/presensi/get-tanggal-presensi-dosen-per-semester":
			w.Write([]byte(`[{"waktu_indonesia":"09-09-2024"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	state, err := NewStateManager(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	courses := NewCourseManager(client, server.URL, 0) // zero ttl so each call fetches
	presence := NewPresenceEngine(client, server.URL)
	academic := NewAcademicManager(client, server.URL, 0)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")

	scanner := NewScanner(auth, courses, presence, academic, state, notifier, 1)
	ctx := context.Background()

	// Initial login
	if _, err := auth.Login(ctx); err != nil {
		t.Fatal(err)
	}
	initialLogins := atomic.LoadInt32(&loginCount)

	// Test /courses retry on 401
	replyCourses := scanner.HandleTelegramCommand(ctx, "/courses")
	if !strings.Contains(replyCourses, "Algoritma &amp; Pemrograman") {
		t.Errorf("expected /courses to succeed after retry, got: %s", replyCourses)
	}
	if atomic.LoadInt32(&coursesAttempts) < 2 {
		t.Errorf("expected courses retry, attempts: %d", atomic.LoadInt32(&coursesAttempts))
	}

	// Test /jadwal retry on 401
	replyJadwal := scanner.HandleTelegramCommand(ctx, "/jadwal")
	if !strings.Contains(replyJadwal, "Algoritma") {
		t.Errorf("expected /jadwal to succeed after retry, got: %s", replyJadwal)
	}
	if atomic.LoadInt32(&jadwalAttempts) < 2 {
		t.Errorf("expected jadwal retry, attempts: %d", atomic.LoadInt32(&jadwalAttempts))
	}

	// Test /tugas retry on 401
	replyTugas := scanner.HandleTelegramCommand(ctx, "/tugas")
	if !strings.Contains(replyTugas, "Tugas 1") {
		t.Errorf("expected /tugas to succeed after retry, got: %s", replyTugas)
	}
	if atomic.LoadInt32(&tugasAttempts) < 2 {
		t.Errorf("expected tugas retry, attempts: %d", atomic.LoadInt32(&tugasAttempts))
	}

	// Test /rekap retry on 401
	replyRekap := scanner.HandleTelegramCommand(ctx, "/rekap")
	if !strings.Contains(replyRekap, "Kehadiran") {
		t.Errorf("expected /rekap to succeed after retry, got: %s", replyRekap)
	}
	if atomic.LoadInt32(&rekapAttempts) < 2 {
		t.Errorf("expected rekap retry, attempts: %d", atomic.LoadInt32(&rekapAttempts))
	}

	// Verify that logins occurred to refresh sessions
	if atomic.LoadInt32(&loginCount) <= initialLogins {
		t.Errorf("expected session refresh logins, got total %d (initial %d)", atomic.LoadInt32(&loginCount), initialLogins)
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

	// Verify default delay is 10s to 30s
	minD, maxD := scanner.PresenceDelay()
	if minD != 10*time.Second || maxD != 30*time.Second {
		t.Errorf("expected default delay 10s-30s, got %v-%v", minD, maxD)
	}

	// 1. Verify delay executes within [min, max] range
	scanner.SetPresenceDelay(50*time.Millisecond, 80*time.Millisecond)
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
	scanner2.SetPresenceDelay(500*time.Millisecond, 1*time.Second)

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
	scanner.SetPresenceDelay(0, 0)
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
		!strings.Contains(rosterResp, "Budi") {
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
	if len(prepareCourseQueue(nil, 0)) != 0 {
		t.Errorf("expected empty queue for nil input")
	}
	single := []Course{{Nomor: 101}}
	if res := prepareCourseQueue(single, 101); len(res) != 1 || res[0].Nomor != 101 {
		t.Errorf("expected single element queue")
	}

	// 2. Active course pinning: active 103 must always be at index 0
	orders := make(map[string]bool)
	for i := 0; i < 50; i++ {
		queue := prepareCourseQueue(courses, 103)
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
		queue := prepareCourseQueue(courses, 0)
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

	scanner.SetWorkerStagger(-1, -10)
	minS, maxS = scanner.WorkerStagger()
	if minS != 0 || maxS != 0 {
		t.Errorf("expected [0, 0] after negative stagger, got [%v, %v]", minS, maxS)
	}

	scanner.SetWorkerStagger(10*time.Millisecond, 50*time.Millisecond)
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


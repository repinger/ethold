package ethol

import (
	"context"
	"encoding/json"
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
	if !strings.Contains(reply, "Scanning Otomatis Dijeda") {
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
	if !strings.Contains(reply, "Scanning Otomatis Dilanjutkan") {
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
	_ = state.AddRecord(PresenceRecord{Key: todayStr + "_presensi-key-99"})
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
	scanner.minDelay, scanner.maxDelay = 0, 0
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
		jadwalAttempts  int32
		tugasAttempts   int32
		rekapAttempts   int32
		coursesAttempts int32
		loginCount      int32
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

func TestScanner_Check_NonBlockingAndCooldown(t *testing.T) {
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
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))
		case "/api/auth/config":
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
		case "/api/presensi/aktif-kuliah":
			json.NewEncoder(w).Encode([]any{})
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
	state, err := NewStateManager(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	presence := NewPresenceEngine(client, server.URL)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")
	scanner := NewScanner(auth, courses, presence, nil, state, notifier, 1)

	ctx := context.Background()

	// 1. In-progress scan simulation
	scanner.scanMu.Lock()
	inProgressReply := scanner.HandleTelegramCommand(ctx, "/check")
	scanner.scanMu.Unlock()

	if !strings.Contains(inProgressReply, "Sedang Berlangsung") {
		t.Errorf("expected in-progress response, got: %s", inProgressReply)
	}

	// 2. Normal scan
	normalReply := scanner.HandleTelegramCommand(ctx, "/check")
	if !strings.Contains(normalReply, "Selesai") {
		t.Errorf("expected completed scan response, got: %s", normalReply)
	}

	// 3. Immediate repeated check triggers cooldown
	cooldownReply := scanner.HandleTelegramCommand(ctx, "/check")
	if !strings.Contains(cooldownReply, "Baru Saja Selesai") {
		t.Errorf("expected cooldown response, got: %s", cooldownReply)
	}
}

func TestScanner_Relogin_Cooldown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login" method="post"><input name="username"/><input name="password"/></form>`)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))
		default:
			http.NotFound(w, r)
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

	reply1 := scanner.HandleTelegramCommand(ctx, "/relogin")
	if !strings.Contains(reply1, "Berhasil") {
		t.Errorf("expected first relogin to succeed, got: %s", reply1)
	}

	reply2 := scanner.HandleTelegramCommand(ctx, "/relogin")
	if !strings.Contains(reply2, "Dibatasi") {
		t.Errorf("expected second relogin to be rate limited/cooldown, got: %s", reply2)
	}
}

func TestScanner_Roster_Cache(t *testing.T) {
	var checkCalls atomic.Int32
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
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Write([]byte(`{"nomor":1001,"nama":"Budi","nipnrp":"3120600001"}`))
		case "/api/auth/config":
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
		case "/api/presensi/aktif-kuliah":
			checkCalls.Add(1)
			json.NewEncoder(w).Encode([]any{})
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

	reply1 := scanner.HandleTelegramCommand(ctx, "/presensi_kelas")
	callsAfter1 := checkCalls.Load()
	if callsAfter1 != 1 {
		t.Errorf("expected 1 call to check course presence, got %d", callsAfter1)
	}

	reply2 := scanner.HandleTelegramCommand(ctx, "/presensi_kelas")
	callsAfter2 := checkCalls.Load()
	if callsAfter2 != callsAfter1 {
		t.Errorf("expected cached roster response, calls increased from %d to %d", callsAfter1, callsAfter2)
	}
	if reply1 != reply2 {
		t.Errorf("expected identical cached reply, got %q vs %q", reply1, reply2)
	}
}

package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type memSnapshot struct {
	label       string
	rss         uint64
	heapAlloc   uint64
	heapInuse   uint64
	heapObjects uint64
	goroutines  int
	totalAlloc  uint64
}

func readMemSnapshot(label string) memSnapshot {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return memSnapshot{
		label:       label,
		rss:         getProcessRSS(),
		heapAlloc:   m.HeapAlloc,
		heapInuse:   m.HeapInuse,
		heapObjects: m.HeapObjects,
		goroutines:  runtime.NumGoroutine(),
		totalAlloc:  m.TotalAlloc,
	}
}

func getProcessRSS() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 2 {
		pages, _ := strconv.ParseUint(fields[1], 10, 64)
		return pages * uint64(os.Getpagesize())
	}
	return 0
}

func (s memSnapshot) String() string {
	return fmt.Sprintf("%-24s | RSS: %6.2f MB | HeapAlloc: %6.2f KB | HeapInuse: %6.2f KB | Objects: %5d | Goroutines: %2d | TotalAlloc: %6.2f MB",
		s.label,
		float64(s.rss)/(1024*1024),
		float64(s.heapAlloc)/1024,
		float64(s.heapInuse)/1024,
		s.heapObjects,
		s.goroutines,
		float64(s.totalAlloc)/(1024*1024),
	)
}

func setupConfigurableMockScanner(tb testing.TB, concurrency int, academicOnly bool) (*httptest.Server, *Scanner) {
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
					"matakuliah":  fmt.Sprintf("Mata Kuliah %d", i),
					"nomor_dosen": 200 + i,
					"dosen":       fmt.Sprintf("Dosen %d", i),
					"hari":        "Senin",
					"jam":         "08:00 - 10:00",
					"ruang":       "R101",
					"paralel":     "A",
				})
			}
			data, _ := json.Marshal(courses)
			w.Write(data)
		case strings.HasPrefix(r.URL.Path, "/api/presensi/mhs"):
			w.Write([]byte(`{"keterangan":"sudah_presensi","status":"hadir"}`))
		case strings.HasPrefix(r.URL.Path, "/api/jadwal/kuliah"):
			w.Write([]byte(`[{"nomor":101,"matakuliah":"Mata Kuliah 1","hari":"Senin","jam":"08:00 - 10:00","ruang":"R101"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/tugas"):
			w.Write([]byte(`[{"nomor":1,"judul":"Tugas 1","deadline":"2026-10-01 23:59:00","status":0}]`))
		case strings.HasPrefix(r.URL.Path, "/api/materi"):
			w.Write([]byte(`[{"nomor":1,"judul":"Materi 1","file":"materi1.pdf"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/video"):
			w.Write([]byte(`[{"nomor":1,"judul":"Video 1","url":"https://example.com/video1"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/pengumuman"):
			w.Write([]byte(`[{"nomor":1,"judul":"Pengumuman 1","isi":"Konten pengumuman penting 1"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/ujian"):
			w.Write([]byte(`[{"nomor":1,"matakuliah":"Mata Kuliah 1","tanggal":"2026-10-15","jam":"08:00 - 10:00"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/presensi/mahasiswa"):
			w.Write([]byte(`{"total_hadir":14,"total_pertemuan":16,"persentase":87.5}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))

	client, err := NewHTTPClient()
	if err != nil {
		tb.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "budi", "pass")
	courses := NewCourseManager(client, server.URL, 5*time.Minute)
	academic := NewAcademicManager(client, server.URL, 5*time.Minute)
	var presence *PresenceEngine
	if !academicOnly {
		presence = NewPresenceEngine(client, server.URL)
	}
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")
	stateFile := filepath.Join(tb.TempDir(), "state.json")
	state, err := NewStateManager(stateFile)
	if err != nil {
		tb.Fatal(err)
	}

	scanner := NewScanner(auth, courses, presence, academic, state, notifier, concurrency)
	return server, scanner
}

func TestMemoryProfilingBaseline(t *testing.T) {
	ctx := context.Background()
	commands := []string{"/status", "/jadwal", "/tugas", "/presensi_kelas", "/rekap", "/materi", "/ujian", "/pengumuman", "/debug"}

	t.Run("Scenario1_AutoPresence_C4", func(t *testing.T) {
		server, scanner := setupConfigurableMockScanner(t, 4, false)
		defer server.Close()

		t.Log("\n" + strings.Repeat("=", 120))
		t.Log("SCENARIO 1: Auto-Presence Mode (Concurrency = 4)")
		t.Log(strings.Repeat("=", 120))

		s0 := readMemSnapshot("1. Initial (Startup)")
		t.Log(s0.String())

		for i := 0; i < 10; i++ {
			_, _ = scanner.ScanOnce(ctx)
			for _, cmd := range commands {
				_ = scanner.HandleTelegramCommand(ctx, cmd)
			}
		}
		s10 := readMemSnapshot("2. After 10 cycles")
		t.Log(s10.String())

		for i := 0; i < 90; i++ {
			_, _ = scanner.ScanOnce(ctx)
			for _, cmd := range commands {
				_ = scanner.HandleTelegramCommand(ctx, cmd)
			}
		}
		s100 := readMemSnapshot("3. After 100 cycles")
		t.Log(s100.String())

		time.Sleep(100 * time.Millisecond)
		sIdle := readMemSnapshot("4. After idle")
		t.Log(sIdle.String())
	})

	t.Run("Scenario2_AutoPresence_C8", func(t *testing.T) {
		server, scanner := setupConfigurableMockScanner(t, 8, false)
		defer server.Close()

		t.Log("\n" + strings.Repeat("=", 120))
		t.Log("SCENARIO 2: Auto-Presence Mode (Concurrency = 8)")
		t.Log(strings.Repeat("=", 120))

		c8_0 := readMemSnapshot("1. Initial (Startup)")
		t.Log(c8_0.String())

		for i := 0; i < 100; i++ {
			_, _ = scanner.ScanOnce(ctx)
			for _, cmd := range commands {
				_ = scanner.HandleTelegramCommand(ctx, cmd)
			}
		}
		c8_100 := readMemSnapshot("2. After 100 cycles")
		t.Log(c8_100.String())

		time.Sleep(100 * time.Millisecond)
		c8_idle := readMemSnapshot("3. After idle")
		t.Log(c8_idle.String())
	})

	t.Run("Scenario3_AcademicOnly_C4", func(t *testing.T) {
		server, scanner := setupConfigurableMockScanner(t, 4, true)
		defer server.Close()

		t.Log("\n" + strings.Repeat("=", 120))
		t.Log("SCENARIO 3: Academic-Only Mode (Concurrency = 4, presence = nil)")
		t.Log(strings.Repeat("=", 120))

		acad_0 := readMemSnapshot("1. Initial (Startup)")
		t.Log(acad_0.String())

		for i := 0; i < 100; i++ {
			for _, cmd := range commands {
				_ = scanner.HandleTelegramCommand(ctx, cmd)
			}
		}
		acad_100 := readMemSnapshot("2. After 100 cycles")
		t.Log(acad_100.String())

		time.Sleep(100 * time.Millisecond)
		acad_idle := readMemSnapshot("3. After idle")
		t.Log(acad_idle.String())
		t.Log(strings.Repeat("=", 120))
	})
}

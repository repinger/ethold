package ethol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewAcademicManager(t *testing.T) {
	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, "https://ethol.pens.ac.id", 5*time.Minute)
	if am == nil {
		t.Fatal("expected non-nil AcademicManager")
	}
	if am.baseURL != "https://ethol.pens.ac.id" {
		t.Errorf("expected baseURL https://ethol.pens.ac.id, got %s", am.baseURL)
	}
	if am.ttl != 5*time.Minute {
		t.Errorf("expected ttl 5m, got %v", am.ttl)
	}
	if am.processedNotifIDs == nil {
		t.Errorf("expected initialized notification tracking collections")
	}

	am.InvalidateTasksCache()
	am.InvalidateMaterialsCache()
	am.InvalidateAttendanceCache()
}

func TestParseCount(t *testing.T) {
	cases := []struct {
		name     string
		input    any
		expected int
	}{
		{"nil", nil, 0},
		{"int", 42, 42},
		{"float64", float64(25.0), 25},
		{"string valid", "123", 123},
		{"string invalid", "abc", 0},
		{"string empty", "", 0},
		{"bool", true, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCount(tc.input)
			if got != tc.expected {
				t.Errorf("parseCount(%v) = %d, expected %d", tc.input, got, tc.expected)
			}
		})
	}
}

func TestAcademicManager_ConcurrentCourseFetches(t *testing.T) {
	var inFlight, maxInFlight int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if cur <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/tugas"):
			w.Write([]byte(`[{"nomor": 1, "judul": "Task 1", "waktu": "2026-09-10 10:00:00"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/materi"):
			w.Write([]byte(`[{"nomor": 1, "judul": "Materi 1"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/video"):
			w.Write([]byte(`[{"nomor": 1, "judul": "Video 1"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/presensi/riwayat"):
			w.Write([]byte(`[{"nomor": 1, "tanggal": "10-09-2026", "waktu": "08:00"}]`))
		case strings.HasPrefix(r.URL.Path, "/api/presensi/get-tanggal-presensi-dosen-per-semester"):
			w.Write([]byte(`[{"waktu_indonesia": "10-09-2026 08:00", "tanggal": "10-09-2026"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()

	courses := []Course{
		{Nomor: 101, JenisSchema: 1, Matakuliah: "Course A"},
		{Nomor: 102, JenisSchema: 1, Matakuliah: "Course B"},
		{Nomor: 103, JenisSchema: 1, Matakuliah: "Course C"},
	}

	// 1. Verify GetPendingTasks runs concurrently
	atomic.StoreInt32(&maxInFlight, 0)
	tasks, err := am.GetPendingTasks(ctx, courses)
	if err != nil {
		t.Fatalf("GetPendingTasks error: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if maxTasks := atomic.LoadInt32(&maxInFlight); maxTasks < 2 {
		t.Errorf("expected GetPendingTasks to run concurrently (maxInFlight >= 2), got %d", maxTasks)
	}

	// 2. Verify GetCourseMaterials runs concurrently
	atomic.StoreInt32(&maxInFlight, 0)
	materials, err := am.GetCourseMaterials(ctx, courses)
	if err != nil {
		t.Fatalf("GetCourseMaterials error: %v", err)
	}
	if len(materials) != 3 {
		t.Fatalf("expected 3 materials, got %d", len(materials))
	}
	if maxMat := atomic.LoadInt32(&maxInFlight); maxMat < 2 {
		t.Errorf("expected GetCourseMaterials to run concurrently (maxInFlight >= 2), got %d", maxMat)
	}

	// 3. Verify GetCourseVideos runs concurrently
	atomic.StoreInt32(&maxInFlight, 0)
	videos, err := am.GetCourseVideos(ctx, courses)
	if err != nil {
		t.Fatalf("GetCourseVideos error: %v", err)
	}
	if len(videos) != 3 {
		t.Fatalf("expected 3 videos, got %d", len(videos))
	}
	if maxVid := atomic.LoadInt32(&maxInFlight); maxVid < 2 {
		t.Errorf("expected GetCourseVideos to run concurrently (maxInFlight >= 2), got %d", maxVid)
	}

	// 4. Verify getAttendanceStatsAt runs concurrently
	atomic.StoreInt32(&maxInFlight, 0)
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, WIBLocation)
	stats, err := am.getAttendanceStatsAt(ctx, now, 2026, 1, 12345, courses)
	if err != nil {
		t.Fatalf("getAttendanceStatsAt error: %v", err)
	}
	if len(stats.Breakdown) != 3 {
		t.Fatalf("expected 3 breakdown items, got %d", len(stats.Breakdown))
	}
	if maxStats := atomic.LoadInt32(&maxInFlight); maxStats < 2 {
		t.Errorf("expected getAttendanceStatsAt to run concurrently (maxInFlight >= 2), got %d", maxStats)
	}
}

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

	am.InvalidateAttendanceCache()
	stats := am.CacheStats()
	if stats.ExamsCount != 0 || stats.AnnouncementsCount != 0 {
		t.Errorf("expected 0 initial exams and announcements cache count, got %+v", stats)
	}
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

func setupAcademicBenchmarkServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/jadwal/jadwal-online"):
			w.Write([]byte(`[
				{"nomor":1,"kuliah":101,"hari":"Senin","jam_awal":"08:00","jam_akhir":"10:00","ruang":"A101","matakuliah":{"nama":"Algoritma"},"dosen":"Dr. Dosen 1"},
				{"nomor":2,"kuliah":102,"hari":"Selasa","jam_awal":"10:00","jam_akhir":"12:00","ruang":"A102","matakuliah":{"nama":"Basis Data"},"dosen":"Dr. Dosen 2"},
				{"nomor":3,"kuliah":103,"hari":"Rabu","jam_awal":"13:00","jam_akhir":"15:00","ruang":"A103","matakuliah":{"nama":"Jaringan Komputer"},"dosen":"Dr. Dosen 3"},
				{"nomor":4,"kuliah":104,"hari":"Kamis","jam_awal":"08:00","jam_akhir":"10:00","ruang":"A104","matakuliah":{"nama":"Sistem Operasi"},"dosen":"Dr. Dosen 4"},
				{"nomor":5,"kuliah":105,"hari":"Jumat","jam_awal":"09:00","jam_akhir":"11:00","ruang":"A105","matakuliah":{"nama":"Rekayasa Perangkat Lunak"},"dosen":"Dr. Dosen 5"}
			]`))
		case strings.HasPrefix(r.URL.Path, "/api/tugas"):
			w.Write([]byte(`[
				{"nomor":1,"judul":"Tugas 1","waktu":"2026-09-10 10:00:00","tutup":0,"submission":[]},
				{"nomor":2,"judul":"Tugas 2","waktu":"2026-09-12 10:00:00","tutup":0,"submission":[]},
				{"nomor":3,"judul":"Tugas 3","waktu":"2026-09-15 10:00:00","tutup":0,"submission":[]}
			]`))
		case strings.HasPrefix(r.URL.Path, "/api/materi"):
			w.Write([]byte(`[
				{"nomor":1,"judul":"Materi 1 Pengantar","keterangan":"Bab 1"},
				{"nomor":2,"judul":"Materi 2 Pembahasan","keterangan":"Bab 2"},
				{"nomor":3,"judul":"Materi 3 Lanjutan","keterangan":"Bab 3"}
			]`))
		case strings.HasPrefix(r.URL.Path, "/api/video"):
			w.Write([]byte(`[
				{"nomor":1,"judul":"Video 1","url":"https://youtube.com/v1"},
				{"nomor":2,"judul":"Video 2","url":"https://youtube.com/v2"}
			]`))
		case strings.HasPrefix(r.URL.Path, "/api/presensi/stat-beranda-mahasiswa"):
			w.Write([]byte(`{"sukses":true,"data":{"totalSesi":16,"rataHadir":100}}`))
		case strings.HasPrefix(r.URL.Path, "/api/presensi/riwayat"):
			w.Write([]byte(`[
				{"nomor":1,"tanggal":"10-09-2026","waktu":"08:00"},
				{"nomor":2,"tanggal":"17-09-2026","waktu":"08:00"}
			]`))
		case strings.HasPrefix(r.URL.Path, "/api/presensi/get-tanggal-presensi-dosen-per-semester"):
			w.Write([]byte(`[
				{"waktu_indonesia":"10-09-2026 08:00","tanggal":"10-09-2026"},
				{"waktu_indonesia":"17-09-2026 08:00","tanggal":"17-09-2026"}
			]`))
		case strings.HasPrefix(r.URL.Path, "/api/pengumuman-admin"):
			w.Write([]byte(`[
				{"id":1,"judul":"Pengumuman 1","isi":"Isi pengumuman 1","tanggal_indonesia":"10 September 2026","is_pinned":1},
				{"id":2,"judul":"Pengumuman 2","isi":"Isi pengumuman 2","tanggal_indonesia":"11 September 2026","is_important":1},
				{"id":3,"judul":"Pengumuman 3","isi":"Isi pengumuman 3","tanggal_indonesia":"12 September 2026"}
			]`))
		case strings.HasPrefix(r.URL.Path, "/api/ujian/daftar-ujian"):
			w.Write([]byte(`[
				{"nomor":1,"matakuliah":{"nama":"Algoritma"},"ujian":{"mulai":"2026-10-01 08:00","selesai":"2026-10-01 10:00","ruang":"A101"}},
				{"nomor":2,"matakuliah":{"nama":"Basis Data"},"ujian":{"mulai":"2026-10-02 08:00","selesai":"2026-10-02 10:00","ruang":"A102"}}
			]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
}

func BenchmarkAcademic_FetchDecode_Schedule(b *testing.B) {
	server := setupAcademicBenchmarkServer()
	defer server.Close()
	client, err := NewHTTPClient()
	if err != nil {
		b.Fatal(err)
	}
	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		am.mu.Lock()
		clear(am.scheduleCache)
		am.mu.Unlock()
		_, err := am.GetSchedule(ctx, 2024, 1)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAcademic_FetchDecode_Tasks(b *testing.B) {
	server := setupAcademicBenchmarkServer()
	defer server.Close()
	client, err := NewHTTPClient()
	if err != nil {
		b.Fatal(err)
	}
	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()
	courses := []Course{
		{Nomor: 101, Matakuliah: "Algoritma"},
		{Nomor: 102, Matakuliah: "Basis Data"},
	}

	b.ResetTimer()
	for b.Loop() {
		am.mu.Lock()
		clear(am.taskCache)
		am.mu.Unlock()
		_, err := am.GetPendingTasks(ctx, courses)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAcademic_FetchDecode_Materials(b *testing.B) {
	server := setupAcademicBenchmarkServer()
	defer server.Close()
	client, err := NewHTTPClient()
	if err != nil {
		b.Fatal(err)
	}
	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()
	courses := []Course{
		{Nomor: 101, Matakuliah: "Algoritma"},
		{Nomor: 102, Matakuliah: "Basis Data"},
	}

	b.ResetTimer()
	for b.Loop() {
		am.mu.Lock()
		clear(am.materialCache)
		am.mu.Unlock()
		_, err := am.GetCourseMaterials(ctx, courses)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAcademic_FetchDecode_Announcements(b *testing.B) {
	server := setupAcademicBenchmarkServer()
	defer server.Close()
	client, err := NewHTTPClient()
	if err != nil {
		b.Fatal(err)
	}
	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		am.mu.Lock()
		am.announcementCache = announcementCacheEntry{}
		am.mu.Unlock()
		_, err := am.GetAnnouncements(ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAcademic_FetchDecode_Exams(b *testing.B) {
	server := setupAcademicBenchmarkServer()
	defer server.Close()
	client, err := NewHTTPClient()
	if err != nil {
		b.Fatal(err)
	}
	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		am.mu.Lock()
		clear(am.examCache)
		am.mu.Unlock()
		_, err := am.GetExams(ctx, 2024, 1, 1)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAcademic_FetchDecode_AttendanceStats(b *testing.B) {
	server := setupAcademicBenchmarkServer()
	defer server.Close()
	client, err := NewHTTPClient()
	if err != nil {
		b.Fatal(err)
	}
	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()
	courses := []Course{
		{Nomor: 101, Matakuliah: "Algoritma", Dosen: "Dosen 1"},
		{Nomor: 102, Matakuliah: "Basis Data", Dosen: "Dosen 2"},
	}
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, WIBLocation)

	b.ResetTimer()
	for b.Loop() {
		am.InvalidateAttendanceCache()
		_, err := am.getAttendanceStatsAt(ctx, now, 2024, 1, 1001, courses)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestAcademicManager_Eviction(t *testing.T) {
	ttl := 100 * time.Millisecond
	am := NewAcademicManager(nil, "http://localhost", ttl)

	expiredTime := time.Now().Add(-200 * time.Millisecond)
	freshTime := time.Now()

	am.mu.Lock()
	am.scheduleCache["exp"] = scheduleCacheEntry{timestamp: expiredTime}
	am.scheduleCache["fresh"] = scheduleCacheEntry{timestamp: freshTime}
	am.taskCache[1] = taskCacheEntry{timestamp: expiredTime}
	am.taskCache[2] = taskCacheEntry{timestamp: freshTime}
	am.materialCache[1] = materialCacheEntry{timestamp: expiredTime}
	am.materialCache[2] = materialCacheEntry{timestamp: freshTime}
	am.videoCache[1] = videoCacheEntry{timestamp: expiredTime}
	am.videoCache[2] = videoCacheEntry{timestamp: freshTime}
	am.attendanceCache["exp"] = attendanceCacheEntry{timestamp: expiredTime}
	am.attendanceCache["fresh"] = attendanceCacheEntry{timestamp: freshTime}
	am.examCache["exp"] = examCacheEntry{timestamp: expiredTime}
	am.examCache["fresh"] = examCacheEntry{timestamp: freshTime}
	am.announcementCache = announcementCacheEntry{items: []AnnouncementItem{{ID: 1}}, timestamp: expiredTime}
	am.mu.Unlock()

	stats := am.CacheStats()
	if stats.SchedulesCount != 1 || stats.TasksCount != 1 || stats.MaterialsCount != 1 || stats.VideosCount != 1 || stats.AttendanceCount != 1 || stats.ExamsCount != 1 || stats.AnnouncementsCount != 0 {
		t.Fatalf("unexpected cache stats before sweep: %+v", stats)
	}

	am.SweepExpired()

	am.mu.RLock()
	defer am.mu.RUnlock()
	if _, ok := am.scheduleCache["fresh"]; len(am.scheduleCache) != 1 || !ok {
		t.Errorf("scheduleCache not correctly evicted: %+v", am.scheduleCache)
	}
	if _, ok := am.taskCache[2]; len(am.taskCache) != 1 || !ok {
		t.Errorf("taskCache not correctly evicted: %+v", am.taskCache)
	}
	if _, ok := am.materialCache[2]; len(am.materialCache) != 1 || !ok {
		t.Errorf("materialCache not correctly evicted: %+v", am.materialCache)
	}
	if _, ok := am.videoCache[2]; len(am.videoCache) != 1 || !ok {
		t.Errorf("videoCache not correctly evicted: %+v", am.videoCache)
	}
	if _, ok := am.attendanceCache["fresh"]; len(am.attendanceCache) != 1 || !ok {
		t.Errorf("attendanceCache not correctly evicted: %+v", am.attendanceCache)
	}
	if _, ok := am.examCache["fresh"]; len(am.examCache) != 1 || !ok {
		t.Errorf("examCache not correctly evicted: %+v", am.examCache)
	}
	if !am.announcementCache.timestamp.IsZero() {
		t.Errorf("announcementCache not evicted")
	}
}

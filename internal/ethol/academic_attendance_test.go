package ethol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcademicManager_Attendance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/presensi/riwayat":
			w.Write([]byte(`[{"tanggal":"09-09-2024"}]`))
		case "/api/presensi/get-tanggal-presensi-dosen-per-semester":
			w.Write([]byte(`[{"waktu_indonesia":"09-09-2024"},{"waktu_indonesia":"02-09-2024"}]`))
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
	courses := []Course{
		{Nomor: 501, JenisSchema: 0, Matakuliah: "Basis Data", Dosen: "Ir. Dosen"},
	}

	// 1. Test Attendance Stats (1 student presence out of 2 lecturer meetings = 50.0%)
	now := time.Date(2024, 9, 9, 10, 0, 0, 0, WIBLocation)
	stats, err := am.getAttendanceStatsAt(ctx, now, 2024, 1, 1001, courses)
	if err != nil {
		t.Fatalf("getAttendanceStatsAt error: %v", err)
	}
	if stats.Percentage != 50.0 {
		t.Errorf("expected 50.0%% attendance, got %f", stats.Percentage)
	}

	// 2. Test FormatAttendanceStatsText
	statsText, err := am.FormatAttendanceStatsText(ctx, now, 2024, 1, 1001, courses)
	if err != nil {
		t.Fatalf("FormatAttendanceStatsText error: %v", err)
	}
	if !strings.Contains(statsText, "50.0%") || !strings.Contains(statsText, "Basis Data") {
		t.Errorf("unexpected stats text: %s", statsText)
	}

	// 3. Test empty courses edge case
	emptyStatsText, err := am.FormatAttendanceStatsText(ctx, now, 2024, 1, 1001, nil)
	if err != nil {
		t.Fatalf("FormatAttendanceStatsText empty error: %v", err)
	}
	if !strings.Contains(emptyStatsText, "Tidak ada mata kuliah yang terdaftar") {
		t.Errorf("expected empty stats message, got %s", emptyStatsText)
	}
}

func TestAcademicManager_Attendance_BerandaStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/presensi/stat-beranda-mahasiswa":
			w.Write([]byte(`{"sukses": true, "data": {"totalSesi": 40, "rataHadir": 95}}`))
		case "/api/presensi/riwayat":
			w.Write([]byte(`[{"tanggal":"09-09-2024"}]`))
		case "/api/presensi/get-tanggal-presensi-dosen-per-semester":
			w.Write([]byte(`[{"waktu_indonesia":"09-09-2024"}]`))
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
	courses := []Course{{Nomor: 501, Matakuliah: "Basis Data", Dosen: "Ir. Dosen"}}
	stats, err := am.getAttendanceStatsAt(context.Background(), time.Now().In(WIBLocation), 2026, 1, 1001, courses)
	if err != nil {
		t.Fatalf("getAttendanceStatsAt error: %v", err)
	}
	if stats.Percentage != 95.0 {
		t.Errorf("expected official percentage 95.0%% from stat-beranda-mahasiswa, got %f", stats.Percentage)
	}
}

func TestAcademicManager_Attendance_Caching(t *testing.T) {
	var riwayatCalls, dosenCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/presensi/riwayat":
			atomic.AddInt32(&riwayatCalls, 1)
			w.Write([]byte(`[{"tanggal":"09-09-2024"}]`))
		case "/api/presensi/get-tanggal-presensi-dosen-per-semester":
			atomic.AddInt32(&dosenCalls, 1)
			w.Write([]byte(`[{"waktu_indonesia":"09-09-2024"}]`))
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
	courses := []Course{
		{Nomor: 501, JenisSchema: 0, Matakuliah: "Basis Data", Dosen: "Ir. Dosen"},
	}

	stats1, err := am.getAttendanceStatsAt(ctx, time.Now().In(WIBLocation), 2024, 1, 1001, courses)
	if err != nil {
		t.Fatalf("first getAttendanceStatsAt error: %v", err)
	}
	if stats1 == nil || len(stats1.Breakdown) != 1 {
		t.Fatalf("unexpected stats1: %+v", stats1)
	}
	if calls := atomic.LoadInt32(&riwayatCalls); calls != 1 {
		t.Fatalf("expected 1 riwayat call, got %d", calls)
	}
	if calls := atomic.LoadInt32(&dosenCalls); calls != 1 {
		t.Fatalf("expected 1 dosen call, got %d", calls)
	}

	stats2, err := am.getAttendanceStatsAt(ctx, time.Now().In(WIBLocation), 2024, 1, 1001, courses)
	if err != nil {
		t.Fatalf("second getAttendanceStatsAt error: %v", err)
	}
	if stats2 == nil || len(stats2.Breakdown) != 1 {
		t.Fatalf("unexpected stats2: %+v", stats2)
	}
	if calls := atomic.LoadInt32(&riwayatCalls); calls != 1 {
		t.Errorf("expected still 1 riwayat call due to caching, got %d", calls)
	}
	if calls := atomic.LoadInt32(&dosenCalls); calls != 1 {
		t.Errorf("expected still 1 dosen call due to caching, got %d", calls)
	}

	am.InvalidateAttendanceCache()
	_, _ = am.getAttendanceStatsAt(ctx, time.Now().In(WIBLocation), 2024, 1, 1001, courses)
	if calls := atomic.LoadInt32(&riwayatCalls); calls != 2 {
		t.Errorf("expected 2 riwayat calls after invalidation, got %d", calls)
	}
	if calls := atomic.LoadInt32(&dosenCalls); calls != 2 {
		t.Errorf("expected 2 dosen calls after invalidation, got %d", calls)
	}
}

func TestAcademicManager_AttendanceRoster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/presensi/daftar-mahasiswa-hadir-kuliah":
			w.Write([]byte(`[
				{"nrp": "3122500001", "nama": "Ahmad Fauzi"},
				{"nrp": "3122500002", "nama": "Budi Santoso"}
			]`))
		case "/api/presensi/daftar-mahasiswa-tidak-hadir-kuliah":
			w.Write([]byte(`[
				{"nrp": "3122500003", "nama": "Citra Dewi"}
			]`))
		case "/api/presensi/jumlah-mahasiswa-per-kuliah":
			w.Write([]byte(`{"jumlah": 30}`))
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
	course := Course{Nomor: 501, JenisSchema: 0, Matakuliah: "Algoritma Pemrograman"}

	attendees, absentees, total, err := am.GetAttendanceRoster(ctx, course, "TESTKEY123")
	if err != nil {
		t.Fatalf("GetAttendanceRoster error: %v", err)
	}
	if len(attendees) != 2 {
		t.Fatalf("expected 2 attendees, got %d", len(attendees))
	}
	if attendees[0].NRP != "3122500001" || attendees[0].Nama != "Ahmad Fauzi" {
		t.Errorf("unexpected attendee 0: %+v", attendees[0])
	}
	if len(absentees) != 1 {
		t.Fatalf("expected 1 absentee, got %d", len(absentees))
	}
	if absentees[0].NRP != "3122500003" || absentees[0].Nama != "Citra Dewi" {
		t.Errorf("unexpected absentee 0: %+v", absentees[0])
	}
	if total != 30 {
		t.Errorf("expected 30 total enrolled, got %d", total)
	}

	// Format roster text with attendees and absentees
	txt := FormatRosterText(course, "TESTKEY123", attendees, absentees, total)
	if !strings.Contains(txt, "Algoritma Pemrograman") || !strings.Contains(txt, "2 / 30 Mahasiswa Hadir") || !strings.Contains(txt, "Ahmad Fauzi") {
		t.Errorf("unexpected formatted roster text: %s", txt)
	}
	if !strings.Contains(txt, "Belum Presensi (1)") || !strings.Contains(txt, "Citra Dewi") {
		t.Errorf("expected roster text to include absentees section: %s", txt)
	}

	// Format roster text without attendees but with absentees
	unattendedTxt := FormatRosterText(course, "EMPTYKEY", nil, absentees, total)
	if !strings.Contains(unattendedTxt, "Belum ada mahasiswa yang tercatat hadir") || !strings.Contains(unattendedTxt, "Belum Presensi (1)") {
		t.Errorf("unexpected unattended roster text: %s", unattendedTxt)
	}

	// Format roster text without attendees and without absentees
	emptyTxt := FormatRosterText(course, "EMPTYKEY", nil, nil, total)
	if !strings.Contains(emptyTxt, "Belum ada mahasiswa yang tercatat hadir") {
		t.Errorf("unexpected empty roster text: %s", emptyTxt)
	}

	// Format roster text when everyone has attended
	allAttended := make([]RosterItem, 30)
	for i := range allAttended {
		allAttended[i] = RosterItem{NRP: fmt.Sprintf("NRP%d", i+1), Nama: fmt.Sprintf("Mhs %d", i+1)}
	}
	allTxt := FormatRosterText(course, "ALLKEY", allAttended, nil, 30)
	if !strings.Contains(allTxt, "Semua mahasiswa sudah hadir") {
		t.Errorf("expected all attended message in: %s", allTxt)
	}

	// Format roster text with >100 attendees and >100 absentees
	largeList := make([]RosterItem, 120)
	for i := range largeList {
		largeList[i] = RosterItem{NRP: fmt.Sprintf("NRP%d", i+1), Nama: fmt.Sprintf("Mhs %d", i+1)}
	}
	largeTxt := FormatRosterText(course, "LARGEKEY", largeList, largeList, 240)
	if !strings.Contains(largeTxt, "100. NRP100 - <b>Mhs 100</b>") {
		t.Errorf("expected 100th attendee in roster text, got: %s", largeTxt)
	}
	if strings.Contains(largeTxt, "101. NRP101") {
		t.Errorf("expected attendee 101 to be truncated")
	}
	if !strings.Contains(largeTxt, "... dan 20 mahasiswa lainnya") {
		t.Errorf("expected truncation summary in roster text, got: %s", largeTxt)
	}

	// Test 401 Unauthorized
	unauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer unauthServer.Close()

	unauthAM := NewAcademicManager(client, unauthServer.URL, 5*time.Minute)
	if _, _, _, err := unauthAM.GetAttendanceRoster(ctx, course, "KEY"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for roster, got %v", err)
	}
}

func TestAcademicManager_Attendance_NomorDosen(t *testing.T) {
	var requestedDosenParam string
	var lecturerCallCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/presensi/riwayat":
			w.Write([]byte(`[{"tanggal":"24-09-2026"},{"tanggal":"17-09-2026"}]`))
		case "/api/presensi/get-tanggal-presensi-dosen-per-semester":
			atomic.AddInt32(&lecturerCallCount, 1)
			requestedDosenParam = r.URL.Query().Get("dosen")
			w.Write([]byte(`[{"waktu_indonesia":"24-09-2026"},{"waktu_indonesia":"17-09-2026"}]`))
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
	courses := []Course{
		{Nomor: 221581, Matakuliah: "Workshop Matematika", Dosen: "Mike Oxmall", NomorDosen: 6769},
		{Nomor: 221167, Matakuliah: "Proyek Akhir-1", Dosen: "Dosen Pengampu", NomorDosen: nil},
	}

	now := time.Date(2026, 9, 26, 12, 0, 0, 0, WIBLocation)
	statsText, err := am.FormatAttendanceStatsText(ctx, now, 2026, 1, 23573, courses)
	if err != nil {
		t.Fatalf("FormatAttendanceStatsText failed: %v", err)
	}

	if requestedDosenParam != "6769" {
		t.Errorf("expected dosen param '6769', got %q", requestedDosenParam)
	}
	if calls := atomic.LoadInt32(&lecturerCallCount); calls != 1 {
		t.Errorf("expected exactly 1 lecturer history call (Proyek Akhir-1 skipped), got %d", calls)
	}
	if !strings.Contains(statsText, "Kehadiran: 2/2 (100.0%)") {
		t.Errorf("expected 'Kehadiran: 2/2 (100.0%%)' in stats text, got:\n%s", statsText)
	}
	if !strings.Contains(statsText, "Kehadiran: 2/0 (100.0%)") {
		t.Errorf("expected Proyek Akhir-1 to have 'Kehadiran: 2/0 (100.0%%)', got:\n%s", statsText)
	}
}

func BenchmarkFormatRosterText(b *testing.B) {
	course := Course{Nomor: 501, Matakuliah: "Algoritma Pemrograman"}
	attendees := make([]RosterItem, 25)
	for i := range attendees {
		attendees[i] = RosterItem{NRP: fmt.Sprintf("31225000%02d", i+1), Nama: fmt.Sprintf("Mahasiswa %d", i+1)}
	}
	absentees := make([]RosterItem, 5)
	for i := range absentees {
		absentees[i] = RosterItem{NRP: fmt.Sprintf("31225000%02d", i+26), Nama: fmt.Sprintf("Mahasiswa %d", i+26)}
	}

	b.ResetTimer()
	for b.Loop() {
		_ = FormatRosterText(course, "KEY123", attendees, absentees, 30)
	}
}

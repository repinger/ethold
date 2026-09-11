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

func TestAcademicManager_ScheduleAndActiveCourse(t *testing.T) {
	var scheduleCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/jadwal/jadwal-online":
			atomic.AddInt32(&scheduleCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
				{
					"hari": "Senin",
					"jam_awal": "08:00",
					"jam_akhir": "10:30",
					"matakuliah": "Workshop Sistem Informasi",
					"dosen": "Ir. Budi M.T.",
					"ruang": "HH-101",
					"nomor": 101,
					"kuliah": 501
				},
				{
					"hari": "Selasa",
					"jam_awal": "13:00",
					"jam_akhir": "15:00",
					"matakuliah": "Jaringan Komputer",
					"dosen": "Dr. Siti",
					"ruang": "Online",
					"nomor": 102,
					"kuliah": 502
				}
			]`))
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

	// 1. Test GetSchedule
	items, err := am.GetSchedule(ctx, 2024, 1)
	if err != nil {
		t.Fatalf("GetSchedule error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 schedule items, got %d", len(items))
	}

	// Verify cache lock prevents duplicate network requests
	itemsCached, err := am.GetSchedule(ctx, 2024, 1)
	if err != nil {
		t.Fatalf("GetSchedule cached error: %v", err)
	}
	if len(itemsCached) != 2 {
		t.Fatalf("expected 2 cached items, got %d", len(itemsCached))
	}
	if calls := atomic.LoadInt32(&scheduleCalls); calls != 1 {
		t.Errorf("expected 1 network call due to cache, got %d", calls)
	}

	// 2. Test GetActiveCourse (-15 min before 08:00 is 07:45, +20 min after 10:30 is 10:50)
	courses := []Course{
		{Nomor: 501, Matakuliah: "Workshop Sistem Informasi"},
		{Nomor: 502, Matakuliah: "Jaringan Komputer"},
	}

	// Senin at 08:15 WIB -> active
	mondayActive := time.Date(2024, 9, 9, 8, 15, 0, 0, WIBLocation) // Sep 9 2024 is Monday
	active := am.GetActiveCourse(ctx, mondayActive, 2024, 1, courses)
	if active == nil || active.Nomor != 501 {
		t.Errorf("expected course 501 to be active, got %+v", active)
	}

	// Senin at 07:45 WIB (exact -15 min boundary) -> active
	mondayBoundaryStart := time.Date(2024, 9, 9, 7, 45, 0, 0, WIBLocation)
	activeStart := am.GetActiveCourse(ctx, mondayBoundaryStart, 2024, 1, courses)
	if activeStart == nil || activeStart.Nomor != 501 {
		t.Errorf("expected course 501 active at start boundary, got %+v", activeStart)
	}

	// Senin at 10:50 WIB (exact +20 min boundary) -> active
	mondayBoundaryEnd := time.Date(2024, 9, 9, 10, 50, 0, 0, WIBLocation)
	activeEnd := am.GetActiveCourse(ctx, mondayBoundaryEnd, 2024, 1, courses)
	if activeEnd == nil || activeEnd.Nomor != 501 {
		t.Errorf("expected course 501 active at end boundary, got %+v", activeEnd)
	}

	// Senin at 11:30 WIB -> not active
	mondayInactive := time.Date(2024, 9, 9, 11, 30, 0, 0, WIBLocation)
	inactive := am.GetActiveCourse(ctx, mondayInactive, 2024, 1, courses)
	if inactive != nil {
		t.Errorf("expected no active course, got %+v", inactive)
	}

	// 3. Test FormatScheduleText
	txt, err := am.FormatScheduleText(ctx, mondayActive, 2024, 1)
	if err != nil {
		t.Fatalf("FormatScheduleText error: %v", err)
	}
	if !strings.Contains(txt, "SENIN (HARI INI)") || !strings.Contains(txt, "HH-101") {
		t.Errorf("unexpected schedule text: %s", txt)
	}
}

func TestAcademicManager_ScheduleClockAndEscaping(t *testing.T) {
	// 1. Test parseClockToTime with dot notation
	base := time.Date(2024, 9, 9, 0, 0, 0, 0, WIBLocation)
	tm, err := parseClockToTime("08.30", base)
	if err != nil {
		t.Fatalf("parseClockToTime error: %v", err)
	}
	if tm.Hour() != 8 || tm.Minute() != 30 {
		t.Errorf("expected 08:30, got %02d:%02d", tm.Hour(), tm.Minute())
	}

	// 2. Test FormatScheduleText escaping dayName
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{
				"hari": "<script>Senin</script>",
				"jam_awal": "08.00",
				"jam_akhir": "10.00",
				"matakuliah": "Test Course"
			}
		]`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	txt, err := am.FormatScheduleText(context.Background(), base, 2024, 1)
	if err != nil {
		t.Fatalf("FormatScheduleText error: %v", err)
	}
	if strings.Contains(txt, "<script>") {
		t.Errorf("expected HTML escaped dayName, got unescaped: %s", txt)
	}
	if !strings.Contains(txt, "&lt;SCRIPT&gt;SENIN&lt;/SCRIPT&gt;") {
		t.Errorf("expected escaped script tags, got: %s", txt)
	}
}

func TestAcademicManager_GetActiveCourse_Priority(t *testing.T) {
	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	// Schedule item: nomor 501, kuliah 502
	// Course A: Nomor 501, Matakuliah: "Course A"
	// Course B: Nomor 502, Matakuliah: "Course B"
	// Should match Course B by item.Kuliah, NOT Course A by item.Nomor
	serverMatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{
				"hari": "Senin",
				"jam_awal": "08:00",
				"jam_akhir": "10:00",
				"matakuliah": "Course B",
				"nomor": 501,
				"kuliah": 502
			}
		]`))
	}))
	defer serverMatch.Close()

	amMatch := NewAcademicManager(client, serverMatch.URL, 5*time.Minute)
	ctx := context.Background()
	courses := []Course{
		{Nomor: 501, Matakuliah: "Course A"},
		{Nomor: 502, Matakuliah: "Course B"},
	}

	mondayActive := time.Date(2024, 9, 9, 8, 30, 0, 0, WIBLocation)
	active := amMatch.GetActiveCourse(ctx, mondayActive, 2024, 1, courses)
	if active == nil || active.Nomor != 502 {
		t.Errorf("expected course 502 matched by item.Kuliah, got %+v", active)
	}

	// Fallback to name when item.Kuliah == 0
	serverNameMatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{
				"hari": "Senin",
				"jam_awal": "08:00",
				"jam_akhir": "10:00",
				"matakuliah": "course a",
				"nomor": 999,
				"kuliah": 0
			}
		]`))
	}))
	defer serverNameMatch.Close()

	amNameMatch := NewAcademicManager(client, serverNameMatch.URL, 5*time.Minute)
	activeName := amNameMatch.GetActiveCourse(ctx, mondayActive, 2024, 1, courses)
	if activeName == nil || activeName.Nomor != 501 {
		t.Errorf("expected course 501 matched by normalized name, got %+v", activeName)
	}

	// Fallback to item.Nomor only when item.Kuliah == 0 and name didn't match
	serverNomorFallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{
				"hari": "Senin",
				"jam_awal": "08:00",
				"jam_akhir": "10:00",
				"matakuliah": null,
				"nomor": 501,
				"kuliah": 0
			}
		]`))
	}))
	defer serverNomorFallback.Close()

	amNomorFallback := NewAcademicManager(client, serverNomorFallback.URL, 5*time.Minute)
	activeNomor := amNomorFallback.GetActiveCourse(ctx, mondayActive, 2024, 1, courses)
	if activeNomor == nil || activeNomor.Nomor != 501 {
		t.Errorf("expected course 501 matched by item.Nomor when item.Kuliah == 0, got %+v", activeNomor)
	}

	// Do NOT match item.Nomor when item.Kuliah > 0 and no match for kuliah or name
	serverNoFalseMatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{
				"hari": "Senin",
				"jam_awal": "08:00",
				"jam_akhir": "10:00",
				"matakuliah": "Completely Unrelated",
				"nomor": 501,
				"kuliah": 777
			}
		]`))
	}))
	defer serverNoFalseMatch.Close()

	amNoFalseMatch := NewAcademicManager(client, serverNoFalseMatch.URL, 5*time.Minute)
	activeNoMatch := amNoFalseMatch.GetActiveCourse(ctx, mondayActive, 2024, 1, courses)
	if activeNoMatch != nil {
		t.Errorf("expected no active course when item.Kuliah > 0 doesn't match, got %+v", activeNoMatch)
	}
}

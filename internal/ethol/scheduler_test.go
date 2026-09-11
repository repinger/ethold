package ethol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestComputeScanWindows(t *testing.T) {
	monday := time.Date(2024, 1, 15, 6, 0, 0, 0, WIBLocation) // 2024-01-15 is Monday

	courses := []Course{
		{Nomor: 101, NamaMatakuliah: "Matematika Diskrit"},
		{Nomor: 102, NamaMatakuliah: "Algoritma & Struktur Data"},
		{Nomor: 103, NamaMatakuliah: "Basis Data"},
	}

	// 1. Empty items
	if windows := ComputeScanWindows(nil, courses, monday); len(windows) != 0 {
		t.Fatalf("expected 0 windows for nil items, got %d", len(windows))
	}

	// 2. Item on different day (Tuesday)
	tuesdayItem := []ScheduleItem{
		{Hari: "Selasa", JamAwal: "08:00", JamAkhir: "09:40", Kuliah: 101},
	}
	if windows := ComputeScanWindows(tuesdayItem, courses, monday); len(windows) != 0 {
		t.Fatalf("expected 0 windows for Tuesday item on Monday, got %d", len(windows))
	}

	// 3. Single item
	singleItem := []ScheduleItem{
		{Hari: "Senin", JamAwal: "08:00", JamAkhir: "09:40", Kuliah: 101},
	}
	windows := ComputeScanWindows(singleItem, courses, monday)
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	expectedStart := time.Date(2024, 1, 15, 7, 45, 0, 0, WIBLocation)
	expectedEnd := time.Date(2024, 1, 15, 10, 0, 0, 0, WIBLocation)
	if !windows[0].Start.Equal(expectedStart) {
		t.Errorf("expected start %v, got %v", expectedStart, windows[0].Start)
	}
	if !windows[0].End.Equal(expectedEnd) {
		t.Errorf("expected end %v, got %v", expectedEnd, windows[0].End)
	}
	if len(windows[0].Courses) != 1 || windows[0].Courses[0].Nomor != 101 {
		t.Errorf("expected course 101, got %v", windows[0].Courses)
	}

	// 4. Overlapping items merged
	overlapping := []ScheduleItem{
		{Hari: "Senin", JamAwal: "08:00", JamAkhir: "09:40", Kuliah: 101},
		{Hari: "Senin", JamAwal: "09:30", JamAkhir: "11:10", Kuliah: 102},
	}
	merged := ComputeScanWindows(overlapping, courses, monday)
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged window, got %d", len(merged))
	}
	expectedEndMerged := time.Date(2024, 1, 15, 11, 30, 0, 0, WIBLocation)
	if !merged[0].Start.Equal(expectedStart) {
		t.Errorf("expected merged start %v, got %v", expectedStart, merged[0].Start)
	}
	if !merged[0].End.Equal(expectedEndMerged) {
		t.Errorf("expected merged end %v, got %v", expectedEndMerged, merged[0].End)
	}
	if len(merged[0].Courses) != 2 {
		t.Fatalf("expected 2 courses in merged window, got %d", len(merged[0].Courses))
	}

	// 5. Two non-overlapping items
	separate := []ScheduleItem{
		{Hari: "Senin", JamAwal: "08:00", JamAkhir: "09:40", Kuliah: 101},
		{Hari: "Senin", JamAwal: "13:00", JamAkhir: "14:40", Kuliah: 103},
	}
	sepWindows := ComputeScanWindows(separate, courses, monday)
	if len(sepWindows) != 2 {
		t.Fatalf("expected 2 separate windows, got %d", len(sepWindows))
	}
	if sepWindows[0].Courses[0].Nomor != 101 || sepWindows[1].Courses[0].Nomor != 103 {
		t.Errorf("unexpected courses in separate windows: %v, %v", sepWindows[0].Courses, sepWindows[1].Courses)
	}
}

func TestNextScanPlan(t *testing.T) {
	w1Start := time.Date(2024, 1, 15, 7, 45, 0, 0, WIBLocation)
	w1End := time.Date(2024, 1, 15, 10, 0, 0, 0, WIBLocation)
	w2Start := time.Date(2024, 1, 15, 12, 45, 0, 0, WIBLocation)
	w2End := time.Date(2024, 1, 15, 15, 0, 0, 0, WIBLocation)

	windows := []ScanWindow{
		{Start: w1Start, End: w1End, Courses: []Course{{Nomor: 101}}},
		{Start: w2Start, End: w2End, Courses: []Course{{Nomor: 102}}},
	}

	// 1. No windows (empty) -> BackgroundInterval, no courses, InWindow false
	plan := NextScanPlan(time.Date(2024, 1, 15, 6, 0, 0, 0, WIBLocation), nil)
	if plan.InWindow || plan.Interval != BackgroundInterval || len(plan.Courses) != 0 {
		t.Errorf("expected background plan for nil windows, got %+v", plan)
	}

	// 2. Far before first window (06:00, > 15m to 07:45) -> BackgroundInterval, full sweep
	plan = NextScanPlan(time.Date(2024, 1, 15, 6, 0, 0, 0, WIBLocation), windows)
	if plan.InWindow || plan.Interval != BackgroundInterval || len(plan.Courses) != 0 {
		t.Errorf("expected background plan far before window, got %+v", plan)
	}

	// 3. Near first window (07:35, gap = 10m <= BackgroundInterval) -> wait exactly gap (10m)
	plan = NextScanPlan(time.Date(2024, 1, 15, 7, 35, 0, 0, WIBLocation), windows)
	if plan.InWindow || plan.Interval != 10*time.Minute || len(plan.Courses) != 0 {
		t.Errorf("expected 10m wait to first window, got %+v", plan)
	}

	// 4. Inside first window (08:30) -> ActiveInterval (60s), target courses, InWindow true
	plan = NextScanPlan(time.Date(2024, 1, 15, 8, 30, 0, 0, WIBLocation), windows)
	if !plan.InWindow || plan.Interval != ActiveInterval || len(plan.Courses) != 1 || plan.Courses[0].Nomor != 101 {
		t.Errorf("expected active window plan, got %+v", plan)
	}

	// 5. Between windows, far from next (10:15, gap = 2h30m > 15m) -> BackgroundInterval
	plan = NextScanPlan(time.Date(2024, 1, 15, 10, 15, 0, 0, WIBLocation), windows)
	if plan.InWindow || plan.Interval != BackgroundInterval || len(plan.Courses) != 0 {
		t.Errorf("expected background plan between windows, got %+v", plan)
	}

	// 6. Between windows, near next (12:40, gap = 5m <= 15m) -> 5m wait
	plan = NextScanPlan(time.Date(2024, 1, 15, 12, 40, 0, 0, WIBLocation), windows)
	if plan.InWindow || plan.Interval != 5*time.Minute || len(plan.Courses) != 0 {
		t.Errorf("expected 5m wait between windows, got %+v", plan)
	}

	// 7. After all windows (16:00) -> BackgroundInterval, full sweep
	plan = NextScanPlan(time.Date(2024, 1, 15, 16, 0, 0, 0, WIBLocation), windows)
	if plan.InWindow || plan.Interval != BackgroundInterval || len(plan.Courses) != 0 {
		t.Errorf("expected background plan after windows, got %+v", plan)
	}
}

func TestTelegramNotifier(t *testing.T) {
	var sentPayload tgSendMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot12345/sendMessage" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&sentPayload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	tn := NewTelegramNotifier(client, server.URL, "12345", "999888")
	ctx := context.Background()

	err = tn.NotifyPresenceSuccess(ctx, "Matematika Diskrit", "Dr. Budi", "key-999", "Presensi berhasil")
	if err != nil {
		t.Fatalf("notify presence success err: %v", err)
	}

	if sentPayload.ChatID != "999888" {
		t.Errorf("expected chat id 999888, got %s", sentPayload.ChatID)
	}
	if sentPayload.ParseMode != "HTML" {
		t.Errorf("expected HTML parse mode, got %s", sentPayload.ParseMode)
	}
}

func TestCalculateJitter(t *testing.T) {
	// 1. Non-positive base / fraction
	if got := calculateJitter(0, 0.2); got != 0 {
		t.Errorf("expected 0 for base 0, got %v", got)
	}
	if got := calculateJitter(-10*time.Second, 0.2); got != -10*time.Second {
		t.Errorf("expected -10s for negative base, got %v", got)
	}
	if got := calculateJitter(10*time.Second, 0); got != 10*time.Second {
		t.Errorf("expected 10s for fraction 0, got %v", got)
	}
	if got := calculateJitter(10*time.Second, -0.1); got != 10*time.Second {
		t.Errorf("expected 10s for negative fraction, got %v", got)
	}

	// 2. Bound checks
	base := 100 * time.Second
	fraction := 0.10 // ±10s -> [90s, 110s]
	distinct := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		val := calculateJitter(base, fraction)
		if val < 90*time.Second || val > 110*time.Second {
			t.Fatalf("jitter %v out of bounds [90s, 110s]", val)
		}
		distinct[val] = true
	}
	if len(distinct) <= 1 {
		t.Errorf("expected jitter variance, got %d distinct value", len(distinct))
	}
}


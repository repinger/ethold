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

func TestAcademicManager_Tasks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tugas":
			w.Write([]byte(`[
				{
					"title": "Tugas 1: ERD Design",
					"deadline_indonesia": "15-09-2024 23:59 WIB",
					"submission_time": null,
					"tutup": "0"
				},
				{
					"title": "Tugas Selesai",
					"deadline_indonesia": "10-09-2024 23:59 WIB",
					"submission_time": "2024-09-10 10:00:00",
					"tutup": "0"
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
	courses := []Course{
		{Nomor: 501, JenisSchema: 0, Matakuliah: "Basis Data", Dosen: "Ir. Dosen"},
	}

	// 1. Test GetPendingTasks
	tasks, err := am.GetPendingTasks(ctx, courses)
	if err != nil {
		t.Fatalf("GetPendingTasks error: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Tugas 1: ERD Design" {
		t.Errorf("unexpected tasks: %+v", tasks)
	}

	// 2. Test FormatTasksText
	tasksText, err := am.FormatTasksText(ctx, courses)
	if err != nil {
		t.Fatalf("FormatTasksText error: %v", err)
	}
	if !strings.Contains(tasksText, "Tugas 1: ERD Design") || !strings.Contains(tasksText, "https://ethol.pens.ac.id/mahasiswa/matakuliah/501/tugas") {
		t.Errorf("unexpected tasks text: %s", tasksText)
	}

	// 3. Test empty tasks & empty courses edge cases
	emptyTasksText, err := am.FormatTasksText(ctx, nil)
	if err != nil {
		t.Fatalf("FormatTasksText empty error: %v", err)
	}
	if !strings.Contains(emptyTasksText, "Tidak ada tugas aktif") {
		t.Errorf("expected empty tasks message, got %s", emptyTasksText)
	}
}

func TestIsTaskSubmitted(t *testing.T) {
	cases := []struct {
		val      any
		expected bool
	}{
		{nil, false},
		{false, false},
		{true, true},
		{0, false},
		{1, true},
		{float64(0), false},
		{float64(1), true},
		{"", false},
		{"null", false},
		{"0", false},
		{"false", false},
		{"true", true},
		{"1", true},
		{"2024-09-10 10:00:00", true},
	}
	for _, tc := range cases {
		got := isTaskSubmitted(tc.val)
		if got != tc.expected {
			t.Errorf("isTaskSubmitted(%v) = %v; expected %v", tc.val, got, tc.expected)
		}
	}
}

func TestAcademicManager_Tasks_Caching(t *testing.T) {
	var taskCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tugas":
			atomic.AddInt32(&taskCalls, 1)
			w.Write([]byte(`[{"title":"Task 1","tutup":"0"}]`))
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

	tasks1, err := am.GetPendingTasks(ctx, courses)
	if err != nil {
		t.Fatalf("first GetPendingTasks error: %v", err)
	}
	if len(tasks1) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks1))
	}
	if calls := atomic.LoadInt32(&taskCalls); calls != 1 {
		t.Fatalf("expected 1 task call, got %d", calls)
	}

	tasks2, err := am.GetPendingTasks(ctx, courses)
	if err != nil {
		t.Fatalf("second GetPendingTasks error: %v", err)
	}
	if len(tasks2) != 1 {
		t.Fatalf("expected 1 task on second call, got %d", len(tasks2))
	}
	if calls := atomic.LoadInt32(&taskCalls); calls != 1 {
		t.Errorf("expected still 1 task call due to caching, got %d", calls)
	}
}

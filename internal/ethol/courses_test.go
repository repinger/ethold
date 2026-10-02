package ethol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCourseManager_Refresh_DoesNotBlockReadLock(t *testing.T) {
	refreshStarted := make(chan struct{})
	allowRefreshFinish := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/config":
			close(refreshStarted)
			<-allowRefreshFinish
			w.Write([]byte(`{"tahun_aktif": "2026", "semester_aktif": "1"}`))
		case "/api/kuliah":
			w.Write([]byte(`[{"nomor": 1, "matakuliah": "Go Programming"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	cm := NewCourseManager(client, server.URL, 5*time.Minute)
	// Seed cache
	cm.mu.Lock()
	cm.cache = []Course{{Nomor: 999, Matakuliah: "Existing Course"}}
	cm.activeYear = 2025
	cm.activeSemester = 2
	cm.lastUpdated = time.Now()
	cm.mu.Unlock()

	ctx := context.Background()

	// Start Refresh in background which hangs until allowRefreshFinish
	refreshDone := make(chan struct{})
	go func() {
		_, _ = cm.Refresh(ctx)
		close(refreshDone)
	}()

	// Wait until network request has started in Refresh
	<-refreshStarted

	// Concurrent read should NOT be blocked by ongoing network request
	readDone := make(chan struct{})
	go func() {
		y, s, err := cm.ActivePeriod(ctx)
		if err != nil {
			t.Errorf("ActivePeriod error: %v", err)
		}
		if y != 2025 || s != 2 {
			t.Errorf("unexpected period: %d, %d", y, s)
		}
		close(readDone)
	}()

	var blocked bool
	select {
	case <-readDone:
		// Passed: read completed while refresh is still waiting on network
	case <-time.After(100 * time.Millisecond):
		blocked = true
	}

	close(allowRefreshFinish)
	<-refreshDone
	if blocked {
		t.Fatal("ActivePeriod was blocked by ongoing network refresh")
	}
}

func TestCourseManager_Refresh_ConcurrentDeduplication(t *testing.T) {
	var configCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/config":
			atomic.AddInt32(&configCalls, 1)
			time.Sleep(30 * time.Millisecond)
			w.Write([]byte(`{"tahun_aktif": "2026", "semester_aktif": "1"}`))
		case "/api/kuliah":
			w.Write([]byte(`[{"nomor": 1, "matakuliah": "Go Programming"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	cm := NewCourseManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()

	// Launch 3 concurrent refreshes on cold cache
	const workers = 3
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func() {
			courses, err := cm.Refresh(ctx)
			if err != nil || len(courses) != 1 {
				t.Errorf("unexpected refresh result: %v, len=%d", err, len(courses))
			}
			done <- struct{}{}
		}()
	}

	for i := 0; i < workers; i++ {
		<-done
	}

	if calls := atomic.LoadInt32(&configCalls); calls != 1 {
		t.Errorf("expected 1 network call due to deduplication, got %d", calls)
	}
}

func TestCourseManager_CacheImmutability(t *testing.T) {
	cm := &CourseManager{
		cache: []Course{
			{Nomor: 1, Matakuliah: "Original"},
		},
		lastUpdated: time.Now(),
		ttl:         5 * time.Minute,
	}

	cached := cm.CachedCourses()
	cached[0].Matakuliah = "Mutated"

	again := cm.CachedCourses()
	if again[0].Matakuliah != "Original" {
		t.Errorf("CachedCourses returned aliased slice: got %v, want Original", again[0].Matakuliah)
	}

	got, err := cm.GetCourses(t.Context())
	if err != nil {
		t.Fatalf("GetCourses: %v", err)
	}
	got[0].Matakuliah = "MutatedAgain"

	again2 := cm.CachedCourses()
	if again2[0].Matakuliah != "Original" {
		t.Errorf("GetCourses returned aliased slice: got %v, want Original", again2[0].Matakuliah)
	}
}

func TestCourseName_Parsing(t *testing.T) {
	cases := []struct {
		name     string
		course   Course
		expected string
	}{
		{
			name:     "string nama_matakuliah",
			course:   Course{Nomor: 1, NamaMatakuliah: "Algoritma"},
			expected: "Algoritma",
		},
		{
			name:     "map nama",
			course:   Course{Nomor: 2, Matakuliah: map[string]any{"nama": "Basis Data"}},
			expected: "Basis Data",
		},
		{
			name:     "map matakuliah",
			course:   Course{Nomor: 3, Matakuliah: map[string]any{"matakuliah": "Jaringan Komputer"}},
			expected: "Jaringan Komputer",
		},
		{
			name:     "fallback to nomor",
			course:   Course{Nomor: 4},
			expected: "Kuliah #4",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.course.CourseName()
			if got != tc.expected {
				t.Errorf("CourseName() = %q, want %q", got, tc.expected)
			}
		})
	}
}

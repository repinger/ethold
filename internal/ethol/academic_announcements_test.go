package ethol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcademicManager_Announcements(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/pengumuman-admin" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&calls, 1)
		w.Write([]byte(`[
			{
				"id": 1,
				"judul": "Libur Nasional",
				"isi_pengumuman": "<p>Kampus libur pada tanggal <b>17 Agustus</b>.</p>",
				"tanggal_indonesia": "16-08-2026",
				"is_important": 0,
				"is_pinned": 0
			},
			{
				"id": 2,
				"judul": "Pengisian KRS",
				"isi_pengumuman": "Harap mengisi KRS tepat waktu.",
				"tanggal_indonesia": "01-08-2026",
				"is_important": 1,
				"is_pinned": 1
			}
		]`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()

	// 1. Fetch announcements and verify pinned/important sorting
	items, err := am.GetAnnouncements(ctx)
	if err != nil {
		t.Fatalf("GetAnnouncements error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 announcements, got %d", len(items))
	}
	if items[0].ID != 2 || items[0].IsPinned != 1 {
		t.Errorf("expected pinned announcement first, got ID %d", items[0].ID)
	}

	// 2. Verify cache prevents duplicate calls
	_, _ = am.GetAnnouncements(ctx)
	if c := atomic.LoadInt32(&calls); c != 1 {
		t.Errorf("expected 1 call due to cache, got %d", c)
	}

	// 3. Test FormatAnnouncementsText
	txt, err := am.FormatAnnouncementsText(ctx)
	if err != nil {
		t.Fatalf("FormatAnnouncementsText error: %v", err)
	}
	if !strings.Contains(txt, "[PINNED]") || !strings.Contains(txt, "Pengisian KRS") || !strings.Contains(txt, "Libur Nasional") {
		t.Errorf("unexpected FormatAnnouncementsText output:\n%s", txt)
	}
	if strings.Contains(txt, "<p>") || strings.Contains(txt, "<b>") && !strings.Contains(txt, "<b>Libur") {
		t.Errorf("expected HTML tags in isi_pengumuman to be stripped, got: %s", txt)
	}

	// 4. Test empty announcements
	emptyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer emptyServer.Close()

	emptyAM := NewAcademicManager(client, emptyServer.URL, 5*time.Minute)
	emptyTxt, err := emptyAM.FormatAnnouncementsText(ctx)
	if err != nil {
		t.Fatalf("empty FormatAnnouncementsText error: %v", err)
	}
	if !strings.Contains(emptyTxt, "Tidak ada pengumuman aktif") {
		t.Errorf("expected empty announcement message, got: %s", emptyTxt)
	}

	// 5. Test 401 Unauthorized
	unauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer unauthServer.Close()

	unauthAM := NewAcademicManager(client, unauthServer.URL, 5*time.Minute)
	if _, err := unauthAM.GetAnnouncements(ctx); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
}

func TestStripHTMLTags(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"Hello World", "Hello World"},
		{"<p>Hello <b>World</b></p>", "Hello World"},
		{"Line 1<br/>Line 2", "Line 1 Line 2"},
		{"   Lots   of   spaces   ", "Lots of spaces"},
	}
	for _, tc := range cases {
		got := stripHTMLTags(tc.input)
		if got != tc.expected {
			t.Errorf("stripHTMLTags(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

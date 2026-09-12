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

func TestAcademicManager_MaterialsAndVideos(t *testing.T) {
	var materiCalls, videoCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/materi":
			atomic.AddInt32(&materiCalls, 1)
			w.Write([]byte(`[
				{
					"id": 1,
					"title": "Slide Pertemuan 1",
					"path": "https://ethol.pens.ac.id/storage/materi/p1.pdf",
					"tipe": 1,
					"created_indonesia": "01-09-2024"
				},
				{
					"id": 2,
					"title": "Referensi Web",
					"path": "https://example.com/ref",
					"tipe": 2,
					"created_indonesia": "02-09-2024"
				}
			]`))
		case "/api/video":
			atomic.AddInt32(&videoCalls, 1)
			w.Write([]byte(`[
				{
					"id": 10,
					"judul": "Rekaman Pertemuan 1",
					"path": "https://youtube.com/watch?v=123",
					"created_indonesia": "01-09-2024"
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
		{Nomor: 501, JenisSchema: 0, Matakuliah: "Struktur Data"},
	}

	// 1. Fetch materials
	mats, err := am.GetCourseMaterials(ctx, courses)
	if err != nil {
		t.Fatalf("GetCourseMaterials error: %v", err)
	}
	if len(mats) != 2 {
		t.Fatalf("expected 2 materials, got %d", len(mats))
	}
	if mats[0].ItemTitle() != "Slide Pertemuan 1" || mats[0].Tipe != 1 {
		t.Errorf("unexpected mats[0]: %+v", mats[0])
	}
	if mats[1].ItemTitle() != "Referensi Web" || mats[1].Tipe != 2 {
		t.Errorf("unexpected mats[1]: %+v", mats[1])
	}

	// 2. Fetch videos
	vids, err := am.GetCourseVideos(ctx, courses)
	if err != nil {
		t.Fatalf("GetCourseVideos error: %v", err)
	}
	if len(vids) != 1 || vids[0].ItemTitle() != "Rekaman Pertemuan 1" {
		t.Fatalf("unexpected vids: %+v", vids)
	}

	// 3. Verify caching prevents extra requests
	_, _ = am.GetCourseMaterials(ctx, courses)
	if calls := atomic.LoadInt32(&materiCalls); calls != 1 {
		t.Errorf("expected 1 materi call due to caching, got %d", calls)
	}
	_, _ = am.GetCourseVideos(ctx, courses)
	if calls := atomic.LoadInt32(&videoCalls); calls != 1 {
		t.Errorf("expected 1 video call due to caching, got %d", calls)
	}

	// 4. Test FormatMaterialsText
	txt, err := am.FormatMaterialsText(ctx, courses)
	if err != nil {
		t.Fatalf("FormatMaterialsText error: %v", err)
	}
	if !strings.Contains(txt, "Struktur Data") || !strings.Contains(txt, "Slide Pertemuan 1") || !strings.Contains(txt, "Rekaman Pertemuan 1") {
		t.Errorf("unexpected formatted materials text: %s", txt)
	}

	// 5. Test empty courses
	emptyTxt, err := am.FormatMaterialsText(ctx, nil)
	if err != nil {
		t.Fatalf("FormatMaterialsText nil courses error: %v", err)
	}
	if !strings.Contains(emptyTxt, "Tidak ada mata kuliah yang terdaftar") {
		t.Errorf("unexpected empty courses text: %s", emptyTxt)
	}

	// 6. Test 401 Unauthorized
	unauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer unauthServer.Close()

	unauthAM := NewAcademicManager(client, unauthServer.URL, 5*time.Minute)
	if _, err := unauthAM.GetCourseMaterials(ctx, courses); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for materials, got %v", err)
	}
	if _, err := unauthAM.GetCourseVideos(ctx, courses); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for videos, got %v", err)
	}
}

func TestAcademicManager_FormatMaterialsText_Concurrent(t *testing.T) {
	var materiInFlight, videoInFlight int32
	var overlapDetected int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/materi") {
			atomic.AddInt32(&materiInFlight, 1)
			if atomic.LoadInt32(&videoInFlight) > 0 {
				atomic.StoreInt32(&overlapDetected, 1)
			}
			time.Sleep(30 * time.Millisecond)
			if atomic.LoadInt32(&videoInFlight) > 0 {
				atomic.StoreInt32(&overlapDetected, 1)
			}
			atomic.AddInt32(&materiInFlight, -1)
			w.Write([]byte(`[{"nomor": 1, "judul": "Materi 1"}]`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/video") {
			atomic.AddInt32(&videoInFlight, 1)
			if atomic.LoadInt32(&materiInFlight) > 0 {
				atomic.StoreInt32(&overlapDetected, 1)
			}
			time.Sleep(30 * time.Millisecond)
			if atomic.LoadInt32(&materiInFlight) > 0 {
				atomic.StoreInt32(&overlapDetected, 1)
			}
			atomic.AddInt32(&videoInFlight, -1)
			w.Write([]byte(`[{"nomor": 1, "judul": "Video 1"}]`))
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	am := NewAcademicManager(client, server.URL, 5*time.Minute)
	ctx := context.Background()
	courses := []Course{
		{Nomor: 201, JenisSchema: 1, Matakuliah: "Operating Systems"},
	}

	txt, err := am.FormatMaterialsText(ctx, courses)
	if err != nil {
		t.Fatalf("FormatMaterialsText error: %v", err)
	}
	if !strings.Contains(txt, "Materi 1") || !strings.Contains(txt, "Video 1") {
		t.Fatalf("expected formatted text to contain materi and video, got: %s", txt)
	}
	if atomic.LoadInt32(&overlapDetected) == 0 {
		t.Errorf("expected GetCourseMaterials and GetCourseVideos to run concurrently in FormatMaterialsText")
	}
}

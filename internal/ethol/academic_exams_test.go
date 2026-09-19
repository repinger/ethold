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

func TestAcademicManager_Exams(t *testing.T) {
	var utsCalls, uasCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/ujian/daftar-ujian" {
			http.NotFound(w, r)
			return
		}

		jenis := r.URL.Query().Get("jenis")
		switch jenis {
		case "1":
			atomic.AddInt32(&utsCalls, 1)
			w.Write([]byte(`[
				{
					"nomor": 101,
					"matakuliah": {"nama": "Workshop Sistem Informasi"},
					"dosen": {"nama": "Budi", "gelar_dpn": "Ir.", "gelar_blk": "M.T."},
					"ruang": "HH-101",
					"ujian": {
						"nomor": 501,
						"mulai": "2026-10-15 08:00:00",
						"selesai": "2026-10-15 10:00:00",
						"mulai_indonesia": "15-10-2026 08:00 WIB",
						"selesai_indonesia": "15-10-2026 10:00 WIB"
					}
				}
			]`))
		case "2":
			atomic.AddInt32(&uasCalls, 1)
			w.Write([]byte(`[
				{
					"nomor": 102,
					"matakuliah": "Jaringan Komputer",
					"dosen": "Dr. Siti",
					"ruang": "Online",
					"ujian": {
						"nomor": 502,
						"mulai": "2026-12-20 13:00:00",
						"selesai": "2026-12-20 15:00:00",
						"mulai_indonesia": "20-12-2026 13:00 WIB",
						"selesai_indonesia": "20-12-2026 15:00 WIB"
					}
				}
			]`))
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

	// 1. Test GetExams for UTS
	uts, err := am.GetExams(ctx, 2026, 1, 1)
	if err != nil {
		t.Fatalf("GetExams UTS error: %v", err)
	}
	if len(uts) != 1 {
		t.Fatalf("expected 1 UTS item, got %d", len(uts))
	}
	if uts[0].CourseName() != "Workshop Sistem Informasi" {
		t.Errorf("expected CourseName 'Workshop Sistem Informasi', got %q", uts[0].CourseName())
	}
	if uts[0].LecturerName() != "Ir. Budi, M.T." {
		t.Errorf("expected LecturerName 'Ir. Budi, M.T.', got %q", uts[0].LecturerName())
	}

	// 2. Verify cache prevents duplicate calls
	_, _ = am.GetExams(ctx, 2026, 1, 1)
	if calls := atomic.LoadInt32(&utsCalls); calls != 1 {
		t.Errorf("expected 1 network call for UTS due to cache, got %d", calls)
	}

	// 3. Test FormatExamsText
	txt, err := am.FormatExamsText(ctx, 2026, 1)
	if err != nil {
		t.Fatalf("FormatExamsText error: %v", err)
	}
	if !strings.Contains(txt, "UJIAN TENGAH SEMESTER (UTS)") ||
		!strings.Contains(txt, "UJIAN AKHIR SEMESTER (UAS)") ||
		!strings.Contains(txt, "HH-101") ||
		!strings.Contains(txt, "Dr. Siti") {
		t.Errorf("unexpected FormatExamsText output:\n%s", txt)
	}

	// 4. Test empty exams response
	emptyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer emptyServer.Close()

	emptyAM := NewAcademicManager(client, emptyServer.URL, 5*time.Minute)
	emptyTxt, err := emptyAM.FormatExamsText(ctx, 2026, 1)
	if err != nil {
		t.Fatalf("FormatExamsText empty error: %v", err)
	}
	if !strings.Contains(emptyTxt, "Belum ada jadwal UTS maupun UAS") {
		t.Errorf("expected empty exam message, got: %s", emptyTxt)
	}

	// 5. Test 401 Unauthorized
	unauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer unauthServer.Close()

	unauthAM := NewAcademicManager(client, unauthServer.URL, 5*time.Minute)
	if _, err := unauthAM.GetExams(ctx, 2026, 1, 1); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
}

package ethol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCourseAndPresenceEngine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/config":
			json.NewEncoder(w).Encode(map[string]any{
				"tahun_aktif":    2024,
				"semester_aktif": 1,
			})

		case "/api/kuliah":
			if r.URL.Query().Get("tahun") != "2024" || r.URL.Query().Get("semester") != "1" {
				http.Error(w, "bad params", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"nomor":           101,
					"jenisSchema":     0,
					"kuliah_asal":     101,
					"dosen":           "Dr. Eng",
					"nama_matakuliah": map[string]any{"nama": "Sistem Operasi"},
				},
				{
					"nomor":        102,
					"jenisSchema": 0,
					"dosen":        "Ir. Budi",
					"matakuliah":   "Jaringan Komputer",
				},
			})

		case "/api/presensi/aktif-kuliah":
			kuliah := r.URL.Query().Get("kuliah")
			switch kuliah {
			case "101":
				// Array format
				json.NewEncoder(w).Encode([]map[string]any{{"key": "session-101"}})
			case "102":
				// Single object format
				json.NewEncoder(w).Encode(map[string]any{"key": "session-102"})
			default:
				json.NewEncoder(w).Encode(map[string]any{"key": nil})
			}

		case "/api/presensi/mahasiswa":
			var payload presenceSubmitPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if payload.Key == "session-101" {
				json.NewEncoder(w).Encode(map[string]any{
					"sukses": true,
					"pesan":  "Presensi berhasil dicatat",
				})
			} else {
				json.NewEncoder(w).Encode(map[string]any{
					"sukses": false,
					"pesan":  "Presensi sudah pernah dilakukan",
				})
			}

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 1. Test CourseManager
	cm := NewCourseManager(client, server.URL, 5*time.Minute)
	courses, err := cm.GetCourses(ctx)
	if err != nil {
		t.Fatalf("get courses failed: %v", err)
	}
	if len(courses) != 2 {
		t.Fatalf("expected 2 courses, got %d", len(courses))
	}
	if courses[0].CourseName() != "Sistem Operasi" {
		t.Errorf("expected Sistem Operasi, got %s", courses[0].CourseName())
	}
	if courses[1].CourseName() != "Jaringan Komputer" {
		t.Errorf("expected Jaringan Komputer, got %s", courses[1].CourseName())
	}

	// 2. Test PresenceEngine CheckCourse
	pe := NewPresenceEngine(client, server.URL)
	key101, open101, err := pe.CheckCourse(ctx, courses[0])
	if err != nil || !open101 || key101 != "session-101" {
		t.Errorf("expected session-101 open, got key=%s open=%v err=%v", key101, open101, err)
	}

	key102, open102, err := pe.CheckCourse(ctx, courses[1])
	if err != nil || !open102 || key102 != "session-102" {
		t.Errorf("expected session-102 open, got key=%s open=%v err=%v", key102, open102, err)
	}

	// 3. Test PresenceEngine Submit
	msg, ok, err := pe.Submit(ctx, courses[0], key101, 9999)
	if err != nil || !ok {
		t.Errorf("submit expected success, got ok=%v msg=%s err=%v", ok, msg, err)
	}

	msg2, ok2, err := pe.Submit(ctx, courses[1], key102, 9999)
	if err != nil || !ok2 {
		// Even if sukses: false, "sudah pernah" is treated as success
		t.Errorf("submit with 'sudah' expected ok=true, got ok=%v msg=%s err=%v", ok2, msg2, err)
	}
}

func TestExtractPresenceKey(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"array with key", `[{"key": "key-1"}]`, "key-1"},
		{"array empty", `[]`, ""},
		{"array without key", `[{"foo": "bar"}]`, ""},
		{"object with key", `{"key": "key-2"}`, "key-2"},
		{"object null key", `{"key": null}`, ""},
		{"object empty", `{}`, ""},
		{"invalid json", `invalid`, ""},
		{"number key", `{"key": 123}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPresenceKey([]byte(tt.raw))
			if got != tt.want {
				t.Errorf("extractPresenceKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func BenchmarkExtractPresenceKey_Array(b *testing.B) {
	raw := []byte(`[{"key": "session-101"}]`)
	b.ResetTimer()
	for b.Loop() {
		_ = extractPresenceKey(raw)
	}
}

func BenchmarkExtractPresenceKey_Object(b *testing.B) {
	raw := []byte(`{"key": "session-102"}`)
	b.ResetTimer()
	for b.Loop() {
		_ = extractPresenceKey(raw)
	}
}

func BenchmarkCheckCourse(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"key": "session-101"}]`))
	}))
	defer server.Close()

	client, _ := NewHTTPClient()
	engine := NewPresenceEngine(client, server.URL)
	c := Course{Nomor: 101}
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, _, _ = engine.CheckCourse(ctx, c)
	}
}

func BenchmarkExtractPresenceKey_Empty(b *testing.B) {
	// ponytail: empty array payload, add malformed JSON when parser resilience benchmarked
	raw := []byte(`[]`)
	b.ResetTimer()
	for b.Loop() {
		_ = extractPresenceKey(raw)
	}
}

package ethol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestScanner_HandleTelegramCommand_Debug(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `
					<form id="fm1" action="/cas/login" method="post">
						<input type="text" name="username" />
						<input type="password" name="password" />
					</form>
				`)
				return
			}
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"nomor":1001,"nama":"Budi Santoso","nipnrp":"3120600001"}`))
		case "/api/auth/config":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"tahun_aktif":2024,"semester_aktif":1}`))
		case "/api/kuliah":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"nomor":501,"jenisSchema":0,"dosen":"Dr. Tech","matakuliah":{"nama":"Algoritma & Pemrograman"}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "test_state.json")
	state, err := NewStateManager(statePath)
	if err != nil {
		t.Fatal(err)
	}

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	courses := NewCourseManager(client, server.URL, 10*time.Minute)
	presence := NewPresenceEngine(client, server.URL)
	academic := NewAcademicManager(client, server.URL, 10*time.Minute)
	notifier := NewTelegramNotifier(client, server.URL, "token", "123")

	ctx := context.Background()
	_, err = auth.Login(ctx)
	if err != nil {
		t.Fatalf("auth login failed: %v", err)
	}
	_, err = courses.GetCourses(ctx)
	if err != nil {
		t.Fatalf("get courses failed: %v", err)
	}

	scanner := NewScanner(auth, courses, presence, academic, state, notifier, 2)

	// Verify /debug command
	reply := scanner.HandleTelegramCommand(ctx, "/debug")

	// Runtime info checks
	if !strings.Contains(reply, runtime.Version()) {
		t.Errorf("expected reply to contain Go version %s, got: %s", runtime.Version(), reply)
	}
	pidStr := fmt.Sprintf("PID:</b> <code>%d</code>", os.Getpid())
	if !strings.Contains(reply, pidStr) {
		t.Errorf("expected reply to contain %s, got: %s", pidStr, reply)
	}
	if !strings.Contains(reply, "Goroutines:") {
		t.Errorf("expected reply to contain Goroutines stat, got: %s", reply)
	}
	if !strings.Contains(reply, "Memory (Alloc):") {
		t.Errorf("expected reply to contain Memory stats, got: %s", reply)
	}

	// Scanner / Engine checks
	if !strings.Contains(reply, "Workers:</b> 2") {
		t.Errorf("expected reply to contain Workers: 2, got: %s", reply)
	}
	if !strings.Contains(reply, "Status:</b> Aktif") {
		t.Errorf("expected reply to contain Status: Aktif, got: %s", reply)
	}

	// Storage & Cache checks
	if !strings.Contains(reply, statePath) {
		t.Errorf("expected reply to contain state path %s, got: %s", statePath, reply)
	}
	if !strings.Contains(reply, "Courses Cache:</b> 1") {
		t.Errorf("expected reply to contain Courses Cache: 1, got: %s", reply)
	}

	// CAS / User checks
	if !strings.Contains(reply, "Budi Santoso") || !strings.Contains(reply, "3120600001") {
		t.Errorf("expected reply to contain user info, got: %s", reply)
	}

	// Verify /help contains /debug
	helpReply := scanner.HandleTelegramCommand(ctx, "/help")
	if !strings.Contains(helpReply, "/debug") {
		t.Errorf("expected /help to mention /debug, got: %s", helpReply)
	}
}

func TestScanner_HandleTelegramCommand_Debug_NilAcademic(t *testing.T) {
	state, err := NewStateManager(filepath.Join(t.TempDir(), "test_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(nil, nil, nil, nil, state, nil, 1)
	ctx := context.Background()

	reply := scanner.HandleTelegramCommand(ctx, "/debug")
	if !strings.Contains(reply, "Diagnostik") {
		t.Errorf("expected debug header even with nil dependencies, got: %s", reply)
	}
	if !strings.Contains(reply, "Tidak tersedia") {
		t.Errorf("expected reply to handle nil user/courses/academic gracefully, got: %s", reply)
	}
}

package ethol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCASAuthFlow(t *testing.T) {
	var (
		ssoSubmitted  bool
		tokenValidate bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)

		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `
					<html>
					<body>
					<form id="fm1" action="/cas/login?service=test" method="post">
						<input type="hidden" name="lt" value="LT-12345" />
						<input type="hidden" name="execution" value="e1s1" />
						<input type="hidden" name="_eventId" value="submit" />
						<input type="text" name="username" />
						<input type="password" name="password" />
					</form>
					</body>
					</html>
				`)
				return
			}
			if r.Method == http.MethodPost {
				if err := r.ParseForm(); err != nil {
					t.Errorf("parse form err: %v", err)
				}
				if r.FormValue("username") != "testuser" || r.FormValue("password") != "testpass" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if r.FormValue("lt") != "LT-12345" || r.FormValue("_eventId") != "submit" {
					t.Errorf("missing hidden fields: %v", r.Form)
				}
				ssoSubmitted = true
				http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "session-ok", Path: "/"})
				http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
				return
			}

		case "/api/auth/validasi-token":
			tokenValidate = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"nomor":1234,"nama":"Budi Santoso","nipnrp":"3120600001"}`))

		case "/api/auth/refresh":
			w.WriteHeader(http.StatusOK)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("new http client: %v", err)
	}

	auth := NewAuthManager(client, server.URL, "testuser", "testpass")
	ctx := context.Background()

	user, err := auth.Login(ctx)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	if !ssoSubmitted {
		t.Error("CAS form was not submitted")
	}
	if !tokenValidate {
		t.Error("token was not validated")
	}
	if user.Nomor != 1234 || user.Nama != "Budi Santoso" || user.NipNrp != "3120600001" {
		t.Errorf("unexpected user struct: %+v", user)
	}

	// Test EnsureSession (should succeed via refresh)
	if err := auth.EnsureSession(ctx); err != nil {
		t.Errorf("ensure session failed: %v", err)
	}
}

func TestAuthManager_Relogin(t *testing.T) {
	var loginCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)

		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `
					<form id="fm1" action="/cas/login?service=test" method="post">
						<input type="hidden" name="lt" value="LT-999" />
						<input type="text" name="username" />
						<input type="password" name="password" />
					</form>
				`)
				return
			}
			loginCount++
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: fmt.Sprintf("session-%d", loginCount), Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)

		case "/api/auth/validasi-token":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"nomor":5678,"nama":"Siti Rahma","nipnrp":"3120600002"}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("new http client: %v", err)
	}

	auth := NewAuthManager(client, server.URL, "user2", "pass2")
	ctx := context.Background()

	// Initial login
	user, err := auth.Login(ctx)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if loginCount != 1 || user.Nama != "Siti Rahma" {
		t.Fatalf("unexpected user or login count: %d, %+v", loginCount, user)
	}

	// Call Relogin to force re-authentication and cookie reset
	reUser, err := auth.Relogin(ctx)
	if err != nil {
		t.Fatalf("relogin failed: %v", err)
	}
	if loginCount != 2 {
		t.Errorf("expected 2 CAS logins after relogin, got %d", loginCount)
	}
	if reUser == nil || reUser.Nama != "Siti Rahma" {
		t.Errorf("unexpected user from relogin: %+v", reUser)
	}
}

func TestAuthManager_Relogin_ConcurrentWithClientDo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login?service=test" method="post"><input type="hidden" name="lt" value="LT-1"/><input type="text" name="username"/><input type="password" name="password"/></form>`)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "ETHOL_SESS", Value: "sess", Path: "/"})
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"nomor":1,"nama":"Tester","nipnrp":"123"}`))
		case "/api/test":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("new http client: %v", err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := auth.Login(ctx); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/test", nil)
				if err != nil {
					return
				}
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}
	}()

	for i := 0; i < 5; i++ {
		if _, err := auth.Relogin(ctx); err != nil {
			t.Fatalf("relogin iteration %d failed: %v", i, err)
		}
	}
	cancel()
	<-done
}

func TestAuthManager_Login_ServiceDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/cas-redirect" {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("new http client: %v", err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	_, err = auth.Login(context.Background())
	if err == nil {
		t.Fatal("expected error on 502 Bad Gateway, got nil")
	}
	if strings.Contains(err.Error(), "CAS form fm1 not found") {
		t.Errorf("expected fast-fail HTTP status error, got masked HTML parse error: %v", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("expected error to mention 502, got: %v", err)
	}
}

func TestAuthManager_Login_CASServerError(t *testing.T) {
	var tokenValidateCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login?service=test" method="post"><input type="hidden" name="lt" value="LT-1"/><input type="text" name="username"/><input type="password" name="password"/></form>`)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("CAS Internal Server Error"))
		case "/api/auth/validasi-token":
			tokenValidateCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("new http client: %v", err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	_, err = auth.Login(context.Background())
	if err == nil {
		t.Fatal("expected error on CAS 500, got nil")
	}
	if tokenValidateCalled {
		t.Error("expected token validation to be skipped when CAS POST returns 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention 500, got: %v", err)
	}
}

func TestAuthManager_EnsureSession_ServiceDownDoesNotFullLogin(t *testing.T) {
	var (
		fullLoginCount int
		refreshCount   int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			fullLoginCount++
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login?service=test" method="post"><input type="hidden" name="lt" value="LT-1"/><input type="text" name="username"/><input type="password" name="password"/></form>`)
				return
			}
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"nomor":1,"nama":"Tester","nipnrp":"123"}`))
		case "/api/auth/refresh":
			refreshCount++
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("503 Service Unavailable"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("new http client: %v", err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	ctx := context.Background()

	// Initial login
	if _, err := auth.Login(ctx); err != nil {
		t.Fatalf("initial login failed: %v", err)
	}
	if fullLoginCount != 1 {
		t.Fatalf("expected 1 initial full login, got %d", fullLoginCount)
	}

	// Force EnsureSession to run refresh
	auth.mu.Lock()
	auth.lastLogin = time.Now().Add(-1 * time.Hour)
	auth.mu.Unlock()

	err = auth.EnsureSession(ctx)
	if err == nil {
		t.Fatal("expected error when refresh returns 503, got nil")
	}
	if fullLoginCount != 1 {
		t.Errorf("expected full login NOT to be called on 503 refresh failure, got fullLoginCount=%d", fullLoginCount)
	}
}

func TestAuthManager_EnsureSession_401TriggersFullLogin(t *testing.T) {
	var (
		fullLoginCount int
		refreshCount   int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cas-redirect":
			fullLoginCount++
			http.Redirect(w, r, "/cas/login?service=test", http.StatusFound)
		case "/cas/login":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form id="fm1" action="/cas/login?service=test" method="post"><input type="hidden" name="lt" value="LT-1"/><input type="text" name="username"/><input type="password" name="password"/></form>`)
				return
			}
			http.Redirect(w, r, "/api/auth/validasi-token", http.StatusFound)
		case "/api/auth/validasi-token":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"nomor":1,"nama":"Tester","nipnrp":"123"}`))
		case "/api/auth/refresh":
			refreshCount++
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("new http client: %v", err)
	}

	auth := NewAuthManager(client, server.URL, "user", "pass")
	ctx := context.Background()

	// Initial login
	if _, err := auth.Login(ctx); err != nil {
		t.Fatalf("initial login failed: %v", err)
	}
	if fullLoginCount != 1 {
		t.Fatalf("expected 1 initial full login, got %d", fullLoginCount)
	}

	// Expire lastLogin to force check
	auth.mu.Lock()
	auth.lastLogin = time.Now().Add(-1 * time.Hour)
	auth.mu.Unlock()

	// 401 should trigger full login
	err = auth.EnsureSession(ctx)
	if err != nil {
		t.Fatalf("expected EnsureSession to succeed via full login on 401, got: %v", err)
	}
	if fullLoginCount != 2 {
		t.Errorf("expected 2 full logins (initial + after 401), got %d", fullLoginCount)
	}
}

func BenchmarkExtractCASForm(b *testing.B) {
	htmlData := `
<!DOCTYPE html>
<html>
<head><title>CAS Login</title><meta charset="utf-8"/></head>
<body>
<div id="container">
	<div class="header"><h1>ETHOL CAS SSO</h1></div>
	<div class="content">
		<form id="fm1" action="/cas/login?service=test" method="post">
			<input type="hidden" name="lt" value="LT-1234567890" />
			<input type="hidden" name="execution" value="e1s1" />
			<input type="hidden" name="_eventId" value="submit" />
			<div class="row">
				<label for="username">Username</label>
				<input type="text" id="username" name="username" value="" />
			</div>
			<div class="row">
				<label for="password">Password</label>
				<input type="password" id="password" name="password" value="" />
			</div>
			<div class="row btn">
				<input type="submit" name="submit" value="LOGIN" />
			</div>
		</form>
	</div>
</div>
</body>
</html>`
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = extractCASForm(strings.NewReader(htmlData))
	}
}

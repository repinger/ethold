package ethol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

package ethol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

type UserInfo struct {
	Nomor  int    `json:"nomor"`
	Nama   string `json:"nama"`
	NipNrp string `json:"nipnrp"`
}

type AuthManager struct {
	mu       sync.Mutex
	client   *http.Client
	baseURL  string
	username string
	password string
	user     *UserInfo
}

func NewAuthManager(client *http.Client, baseURL, username, password string) *AuthManager {
	if baseURL == "" {
		baseURL = "https://ethol.pens.ac.id"
	}
	return &AuthManager{
		client:   client,
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
	}
}

func (a *AuthManager) User() *UserInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.user
}

func (a *AuthManager) Login(ctx context.Context) (*UserInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	slog.Info("Starting CAS SSO authentication")

	// 1. GET cas-redirect
	redirURL := a.baseURL + "/api/auth/cas-redirect"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, redirURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create redirect req: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch cas redirect: %w", err)
	}
	defer resp.Body.Close()

	// 2. Stream-parse CAS form id="fm1"
	actionRel, formValues, err := extractCASForm(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse cas login form: %w", err)
	}

	postURL, err := resp.Request.URL.Parse(actionRel)
	if err != nil {
		return nil, fmt.Errorf("resolve action url: %w", err)
	}

	// Fill form fields
	formValues.Set("username", a.username)
	formValues.Set("password", a.password)
	formValues.Set("_eventId", "submit")
	formValues.Set("submit", "LOGIN")

	// 3. POST credentials
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL.String(), strings.NewReader(formValues.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create cas post req: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	postResp, err := a.client.Do(postReq)
	if err != nil {
		return nil, fmt.Errorf("post cas credentials: %w", err)
	}
	io.Copy(io.Discard, postResp.Body)
	postResp.Body.Close()

	// 4. Validate token
	valURL := a.baseURL + "/api/auth/validasi-token"
	valReq, err := http.NewRequestWithContext(ctx, http.MethodGet, valURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create validate token req: %w", err)
	}

	valResp, err := a.client.Do(valReq)
	if err != nil {
		return nil, fmt.Errorf("validate token request: %w", err)
	}
	defer valResp.Body.Close()

	if valResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token validation failed: HTTP %d", valResp.StatusCode)
	}

	var user UserInfo
	if err := json.NewDecoder(valResp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode user info: %w", err)
	}

	a.user = &user
	slog.Info("Authentication successful", "name", user.Nama, "nrp", user.NipNrp, "nomor", user.Nomor)
	return &user, nil
}

// Relogin forces re-authentication with CAS SSO, resetting session cookies and user info.
// ponytail: fresh memory cookiejar replaces active session. upgrade path: persist cookies to disk if daemon restarts need session reuse.
func (a *AuthManager) Relogin(ctx context.Context) (*UserInfo, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("reset cookie jar: %w", err)
	}

	a.mu.Lock()
	if a.client != nil {
		a.client.Jar = jar
	}
	a.user = nil
	a.mu.Unlock()

	return a.Login(ctx)
}

func (a *AuthManager) EnsureSession(ctx context.Context) error {
	a.mu.Lock()
	hasUser := a.user != nil
	a.mu.Unlock()

	if !hasUser {
		_, err := a.Login(ctx)
		return err
	}

	refreshURL := a.baseURL + "/api/auth/refresh"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, nil)
	if err == nil {
		resp, doErr := a.client.Do(req)
		if doErr == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}

	// Token refresh failed or returned non-200, perform full login
	_, err = a.Login(ctx)
	return err
}

func extractCASForm(r io.Reader) (string, url.Values, error) {
	z := html.NewTokenizer(r)
	var (
		inForm bool
		action string
		values = make(url.Values)
	)

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); errors.Is(err, io.EOF) {
				if action == "" && len(values) == 0 {
					return "", nil, errors.New("CAS form fm1 not found")
				}
				return action, values, nil
			} else {
				return "", nil, err
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if !inForm && tok.Data == "form" {
				var isFM1 bool
				var formAction string
				for _, attr := range tok.Attr {
					if attr.Key == "id" && attr.Val == "fm1" {
						isFM1 = true
					}
					if attr.Key == "action" {
						formAction = attr.Val
					}
				}
				if isFM1 {
					inForm = true
					action = formAction
				}
			} else if inForm && tok.Data == "input" {
				var name, val string
				for _, attr := range tok.Attr {
					if attr.Key == "name" {
						name = attr.Val
					}
					if attr.Key == "value" {
						val = attr.Val
					}
				}
				if name != "" {
					values.Set(name, val)
				}
			}

		case html.EndTagToken:
			tok := z.Token()
			if inForm && tok.Data == "form" {
				return action, values, nil
			}
		}
	}
}

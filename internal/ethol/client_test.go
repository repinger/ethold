package ethol

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

var legitUserAgents = func() []string {
	uas := make([]string, len(browserProfiles))
	for i, p := range browserProfiles {
		uas[i] = p.userAgent
	}
	return uas
}()

func TestNewHTTPClient_UserAgent(t *testing.T) {
	var gotUA string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if _, err := client.Do(req); err != nil {
		t.Fatalf("client.Do: %v", err)
	}

	if !slices.Contains(legitUserAgents, gotUA) {
		t.Errorf("got unexpected User-Agent: %q", gotUA)
	}
}

func TestNewHTTPClient_PreservesExplicitUserAgent(t *testing.T) {
	var gotUA string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("User-Agent", "CustomAgent/1.0")

	if _, err := client.Do(req); err != nil {
		t.Fatalf("client.Do: %v", err)
	}

	if gotUA != "CustomAgent/1.0" {
		t.Errorf("got %q, want %q", gotUA, "CustomAgent/1.0")
	}
}

func TestNewHTTPClient_TargetHostHeaders(t *testing.T) {
	var captured http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if _, err := client.Do(req); err != nil {
		t.Fatalf("client.Do: %v", err)
	}

	if got := captured.Get("Accept-Language"); got != "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7" {
		t.Errorf("Accept-Language = %q, want id-ID preference", got)
	}
	if got := captured.Get("Accept"); got != "application/json, text/plain, */*" {
		t.Errorf("Accept = %q, want default browser accept", got)
	}
	if got := captured.Get("Sec-Fetch-Site"); got != "same-origin" {
		t.Errorf("Sec-Fetch-Site = %q, want same-origin", got)
	}

	ua := captured.Get("User-Agent")
	secChUa := captured.Get("Sec-CH-UA")
	if slices.Contains([]string{"Firefox", "Safari"}, ua) && secChUa != "" {
		t.Errorf("Firefox/Safari should not send Sec-CH-UA, got %q", secChUa)
	}
}

func TestNewHTTPClient_ExternalHostSkipsTargetHeaders(t *testing.T) {
	var captured http.Header
	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	// Intercept transport to inspect external host without making real network call
	transport := client.Transport.(*headerTransport)
	testTrans := &testRoundTripper{
		fn: func(req *http.Request) (*http.Response, error) {
			captured = req.Header.Clone()
			return &http.Response{StatusCode: http.StatusOK}, nil
		},
	}
	client.Transport = &headerTransport{
		base:    testTrans,
		profile: transport.profile,
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.telegram.org/bot123/getMe", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if _, err := client.Do(req); err != nil {
		t.Fatalf("client.Do: %v", err)
	}

	if got := captured.Get("User-Agent"); got == "" {
		t.Error("User-Agent should be set on external host")
	}
	if got := captured.Get("Accept-Language"); got != "" {
		t.Errorf("Accept-Language should be empty on external host, got %q", got)
	}
	if got := captured.Get("Sec-Fetch-Site"); got != "" {
		t.Errorf("Sec-Fetch-Site should be empty on external host, got %q", got)
	}
	if got := captured.Get("Sec-CH-UA"); got != "" {
		t.Errorf("Sec-CH-UA should be empty on external host, got %q", got)
	}
}

func TestNewHTTPClient_PreservesExplicitHeaders(t *testing.T) {
	var captured http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewHTTPClient()
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en-US")

	if _, err := client.Do(req); err != nil {
		t.Fatalf("client.Do: %v", err)
	}

	if got := captured.Get("Accept"); got != "text/html" {
		t.Errorf("Accept = %q, want text/html", got)
	}
	if got := captured.Get("Accept-Language"); got != "en-US" {
		t.Errorf("Accept-Language = %q, want en-US", got)
	}
}

type testRoundTripper struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.fn(req)
}

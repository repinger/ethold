package ethol

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
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

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

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

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("User-Agent", "CustomAgent/1.0")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

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

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	if got := captured.Get("Accept-Language"); got != "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7" {
		t.Errorf("Accept-Language = %q, want id-ID preference", got)
	}
	if got := captured.Get("Accept"); got != "application/json, text/plain, */*" {
		t.Errorf("Accept = %q, want default browser accept", got)
	}
	if got := captured.Get("Sec-Fetch-Site"); got != "same-origin" {
		t.Errorf("Sec-Fetch-Site = %q, want same-origin", got)
	}
	if got := captured.Get("Origin"); got != ts.URL {
		t.Errorf("Origin = %q, want %q", got, ts.URL)
	}
	if got := captured.Get("Referer"); got != ts.URL+"/" {
		t.Errorf("Referer = %q, want %q", got, ts.URL+"/")
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

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.telegram.org/bot123/getMe", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

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
	if got := captured.Get("Origin"); got != "" {
		t.Errorf("Origin should be empty on external host, got %q", got)
	}
	if got := captured.Get("Referer"); got != "" {
		t.Errorf("Referer should be empty on external host, got %q", got)
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

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en-US")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()

	if got := captured.Get("Accept"); got != "text/html" {
		t.Errorf("Accept = %q, want text/html", got)
	}
	if got := captured.Get("Accept-Language"); got != "en-US" {
		t.Errorf("Accept-Language = %q, want en-US", got)
	}
}

func TestHeaderTransport_RoundTrip_DoesNotMutateRequest(t *testing.T) {
	transport := &headerTransport{
		base: &testRoundTripper{
			fn: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			},
		},
		profile: browserProfiles[0],
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://ethol.pens.ac.id/api/test", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if len(req.Header) != 0 {
		t.Fatalf("expected empty headers before RoundTrip, got %v", req.Header)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if len(req.Header) != 0 {
		t.Errorf("original req.Header mutated: got %v, want empty", req.Header)
	}
}

type testRoundTripper struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.fn(req)
}

func TestHeaderTransport_ProfileRotation(t *testing.T) {
	now := time.Now()
	r1 := nextProfileRotation(now)
	if r1.Before(now.Add(minProfileTTL)) || r1.After(now.Add(maxProfileTTL)) {
		t.Errorf("rotation %v out of range [%v, %v]", r1, now.Add(minProfileTTL), now.Add(maxProfileTTL))
	}

	transport := &headerTransport{
		base: &testRoundTripper{
			fn: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK}, nil
			},
		},
		profile:    browserProfiles[0],
		nextRotate: now.Add(-1 * time.Second), // expired
	}

	p := transport.currentProfile()
	if !slices.Contains(legitUserAgents, p.userAgent) {
		t.Errorf("got unexpected User-Agent after rotation: %q", p.userAgent)
	}
	if !transport.nextRotate.After(now) {
		t.Errorf("nextRotate was not advanced into future: %v", transport.nextRotate)
	}
}

func TestIsPENSHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"pens.ac.id", true},
		{"ethol.pens.ac.id", true},
		{"CAS.PENS.AC.ID", true},
		{"api.telegram.org", false},
		{"notpens.ac.id", false},
		{"localhost", false},
		{"127.0.0.1", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			if got := isPENSHost(tc.host); got != tc.want {
				t.Errorf("isPENSHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func generateTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Root CA"},
		NotBefore:             time.Now().Add(-365 * 24 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return cert, key
}

func generateTestLeafCert(t *testing.T, issuer *x509.Certificate, issuerKey *ecdsa.PrivateKey, dnsName string, notBefore, notAfter time.Time, selfSigned bool) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	parent := issuer
	signKey := issuerKey
	if selfSigned {
		parent = tmpl
		signKey = key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
}

func TestNewHTTPClient_ExpiredCertHandling(t *testing.T) {
	caCert, caKey := generateTestCA(t)
	rootPool := x509.NewCertPool()
	rootPool.AddCert(caCert)

	now := time.Now()
	validWindowStart := now.Add(-24 * time.Hour)
	validWindowEnd := now.Add(24 * time.Hour)
	expiredStart := now.Add(-48 * time.Hour)
	expiredEnd := now.Add(-1 * time.Hour)
	futureStart := now.Add(24 * time.Hour)
	futureEnd := now.Add(48 * time.Hour)

	tests := []struct {
		name       string
		reqHost    string
		cert       tls.Certificate
		allowExp   bool
		wantAccept bool
	}{
		{
			name:       "valid cert accepted when disabled",
			reqHost:    "ethol.pens.ac.id",
			cert:       generateTestLeafCert(t, caCert, caKey, "ethol.pens.ac.id", validWindowStart, validWindowEnd, false),
			allowExp:   false,
			wantAccept: true,
		},
		{
			name:       "expired PENS cert rejected when disabled",
			reqHost:    "ethol.pens.ac.id",
			cert:       generateTestLeafCert(t, caCert, caKey, "ethol.pens.ac.id", expiredStart, expiredEnd, false),
			allowExp:   false,
			wantAccept: false,
		},
		{
			name:       "expired PENS cert accepted when enabled",
			reqHost:    "ethol.pens.ac.id",
			cert:       generateTestLeafCert(t, caCert, caKey, "ethol.pens.ac.id", expiredStart, expiredEnd, false),
			allowExp:   true,
			wantAccept: true,
		},
		{
			name:       "expired non-PENS cert rejected even when enabled",
			reqHost:    "api.telegram.org",
			cert:       generateTestLeafCert(t, caCert, caKey, "api.telegram.org", expiredStart, expiredEnd, false),
			allowExp:   true,
			wantAccept: false,
		},
		{
			name:       "expired self-signed PENS cert rejected even when enabled",
			reqHost:    "ethol.pens.ac.id",
			cert:       generateTestLeafCert(t, caCert, caKey, "ethol.pens.ac.id", expiredStart, expiredEnd, true),
			allowExp:   true,
			wantAccept: false,
		},
		{
			name:       "expired PENS cert with mismatched hostname rejected even when enabled",
			reqHost:    "ethol.pens.ac.id",
			cert:       generateTestLeafCert(t, caCert, caKey, "other.pens.ac.id", expiredStart, expiredEnd, false),
			allowExp:   true,
			wantAccept: false,
		},
		{
			name:       "premature PENS cert rejected even when enabled",
			reqHost:    "ethol.pens.ac.id",
			cert:       generateTestLeafCert(t, caCert, caKey, "ethol.pens.ac.id", futureStart, futureEnd, false),
			allowExp:   true,
			wantAccept: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			srv.TLS = &tls.Config{
				Certificates: []tls.Certificate{tc.cert},
			}
			srv.StartTLS()
			defer srv.Close()

			client, err := NewHTTPClient(ClientConfig{
				InsecureAllowExpiredCert: tc.allowExp,
				RootCAs:                  rootPool,
			})
			if err != nil {
				t.Fatalf("NewHTTPClient: %v", err)
			}

			// Route requests for tc.reqHost to the local test TLS server
			ht := client.Transport.(*headerTransport)
			baseTransport := ht.base.(*http.Transport)
			srvAddr := srv.Listener.Addr().String()
			baseTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, srvAddr)
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+tc.reqHost+"/", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}

			resp, err := client.Do(req)
			if tc.wantAccept {
				if err != nil {
					t.Fatalf("expected request to succeed, got error: %v", err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Errorf("status = %d, want 200", resp.StatusCode)
				}
			} else {
				if err == nil {
					resp.Body.Close()
					t.Fatal("expected TLS verification error, but request succeeded")
				}
			}
		})
	}
}

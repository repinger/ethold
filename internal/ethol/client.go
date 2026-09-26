package ethol

import (
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

type browserProfile struct {
	userAgent       string
	secChUa         string
	secChUaMobile   string
	secChUaPlatform string
}

// ponytail: static legit desktop browser profiles. upgrade path: load from env or remote feed if ETHOL bans signatures.
var browserProfiles = []browserProfile{
	{
		userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		secChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"Windows"`,
	},
	{
		userAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		secChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"macOS"`,
	},
	{
		userAgent:       "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		secChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"Linux"`,
	},
	{
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	},
	{
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0",
	},
	{
		userAgent: "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
	},
	{
		userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
	},
	{
		userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
		secChUa:         `"Microsoft Edge";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"Windows"`,
	},
	{
		userAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
		secChUa:         `"Microsoft Edge";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		secChUaMobile:   "?0",
		secChUaPlatform: `"macOS"`,
	},
}

const (
	minProfileTTL = 12 * time.Hour
	maxProfileTTL = 24 * time.Hour
)

type headerTransport struct {
	base       http.RoundTripper
	mu         sync.RWMutex
	profile    browserProfile
	nextRotate time.Time
}

func (t *headerTransport) currentProfile() browserProfile {
	t.mu.RLock()
	if time.Now().Before(t.nextRotate) {
		p := t.profile
		t.mu.RUnlock()
		return p
	}
	t.mu.RUnlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Now().Before(t.nextRotate) {
		return t.profile
	}
	t.profile = randomBrowserProfile()
	t.nextRotate = nextProfileRotation(time.Now())
	return t.profile
}

func nextProfileRotation(now time.Time) time.Time {
	diff := maxProfileTTL - minProfileTTL
	jitter := time.Duration(rand.Int64N(int64(diff) + 1))
	return now.Add(minProfileTTL + jitter)
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	profile := t.currentProfile()
	setDefaultHeader(req.Header, "User-Agent", profile.userAgent)

	if isTargetHost(req.URL.Hostname()) {
		setDefaultHeader(req.Header, "Accept", "application/json, text/plain, */*")
		setDefaultHeader(req.Header, "Accept-Language", "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7")
		setDefaultHeader(req.Header, "Sec-Fetch-Site", "same-origin")
		setDefaultHeader(req.Header, "Sec-Fetch-Mode", "cors")
		setDefaultHeader(req.Header, "Sec-Fetch-Dest", "empty")
		if req.URL.Scheme != "" && req.URL.Host != "" {
			origin := req.URL.Scheme + "://" + req.URL.Host
			setDefaultHeader(req.Header, "Origin", origin)
			setDefaultHeader(req.Header, "Referer", origin+"/")
		}
		if profile.secChUa != "" {
			setDefaultHeader(req.Header, "Sec-CH-UA", profile.secChUa)
			setDefaultHeader(req.Header, "Sec-CH-UA-Mobile", profile.secChUaMobile)
			setDefaultHeader(req.Header, "Sec-CH-UA-Platform", profile.secChUaPlatform)
		}
	}

	if !isDevBuild {
		return t.base.RoundTrip(req)
	}

	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	dur := time.Since(start)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	devLogHTTP(req.Method, req.URL.String(), status, dur, err)
	return resp, err
}

func setDefaultHeader(h http.Header, key, value string) {
	if value != "" && h.Get(key) == "" {
		h.Set(key, value)
	}
}

func isTargetHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "pens.ac.id" || strings.HasSuffix(host, ".pens.ac.id")
}

func randomBrowserProfile() browserProfile {
	return browserProfiles[rand.IntN(len(browserProfiles))]
}

type syncCookieJar struct {
	mu  sync.RWMutex
	jar http.CookieJar
}

func newSyncCookieJar() (*syncCookieJar, error) {
	inner, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &syncCookieJar{jar: inner}, nil
}

func (s *syncCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.jar.SetCookies(u, cookies)
}

func (s *syncCookieJar) Cookies(u *url.URL) []*http.Cookie {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jar.Cookies(u)
}

func (s *syncCookieJar) Reset() error {
	newInner, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.jar = newInner
	s.mu.Unlock()
	return nil
}

func NewHTTPClient() (*http.Client, error) {
	jar, err := newSyncCookieJar()
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 6,
		IdleConnTimeout:     45 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &http.Client{
		Jar: jar,
		Transport: &headerTransport{
			base:       transport,
			profile:    randomBrowserProfile(),
			nextRotate: nextProfileRotation(time.Now()),
		},
		Timeout: 30 * time.Second,
	}, nil
}

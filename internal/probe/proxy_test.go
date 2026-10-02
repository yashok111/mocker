package probe

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxyTargetPolicy(t *testing.T) {
	for _, target := range []string{"http://evil.test", "http://api.test@evil.test", "http://api.test?url=x", "http://api.test/#fragment", "file:///etc/passwd", "http://169.254.169.254"} {
		if _, err := ValidateProxyTarget(target, []string{"http://api.test", "http://169.254.169.254"}); err == nil {
			t.Errorf("allowed %s", target)
		}
	}
	if _, err := ValidateProxyTarget("https://api.test/v1", []string{"https://api.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateProxyTarget("http://api.test", nil); err == nil {
		t.Fatal("empty allowlist allowed network")
	}
}

func TestProxyForwardsExactPathBodyStripsHeadersAndDoesNotRedirect(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.RequestURI() != "/v1/users/a%2Fb?q=2&q=1" {
			t.Errorf("URI %s", r.URL.RequestURI())
		}
		for _, name := range []string{"Authorization", "Cookie", "X-Forwarded-For", "X-Remove", "X-Mocker-Force", "Origin"} {
			if r.Header.Get(name) != "" {
				t.Errorf("leaked %s", name)
			}
		}
		w.Header().Set("Location", "http://other.test/secret")
		w.Header().Set("Connection", "X-Remove")
		w.Header().Set("X-Remove", "secret")
		w.Header().Set("Set-Cookie", "session=secret")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	r := httptest.NewRequest(http.MethodPost, "http://mocker/users/a%2Fb?q=2&q=1", strings.NewReader("payload"))
	for _, name := range []string{"Authorization", "Cookie", "X-Forwarded-For", "X-Remove", "X-Mocker-Force", "Origin"} {
		r.Header.Set(name, "secret")
	}
	r.Header.Set("Connection", "X-Remove")
	got, err := ProxyExchange(t.Context(), upstream.URL+"/v1", r, []byte("payload"), ProxyOptions{Allowlist: []string{upstream.URL}, MaxResponse: 1024, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got.Status != 302 || got.Header.Get("X-Remove") != "" || got.Header.Get("Set-Cookie") != "" {
		t.Fatalf("calls=%d response=%+v", calls, got)
	}
}

func TestProxyLimitsTimeoutAndCancellation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer upstream.Close()
	opts := ProxyOptions{Allowlist: []string{upstream.URL}, MaxResponse: 10, Timeout: 30 * time.Millisecond}
	for _, path := range []string{"/big", "/slow"} {
		_, err := ProxyExchange(t.Context(), upstream.URL, httptest.NewRequest(http.MethodGet, path, nil), nil, opts)
		if err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ProxyExchange(ctx, upstream.URL, httptest.NewRequest(http.MethodGet, "/", nil), nil, opts); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestProxyTLSAndExplicitCredentials(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("Cookie") != "session=test" {
			t.Error("explicit credentials not forwarded")
		}
		w.Header().Set("Set-Cookie", "session=next; Domain=example.com; Path=/v1; HttpOnly")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer test")
	r.Header.Set("Cookie", "session=test")
	opts := ProxyOptions{Allowlist: []string{upstream.URL}, MaxResponse: 1024, Timeout: time.Second, ForwardAuth: true, ForwardCookies: true, CookiePath: "/w/demo/"}
	if _, err := ProxyExchange(t.Context(), upstream.URL, r, nil, opts); err == nil {
		t.Fatal("trusted unknown TLS certificate")
	}
	opts.CAPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	response, err := ProxyExchange(t.Context(), upstream.URL, r, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	cookie := response.Header.Get("Set-Cookie")
	if strings.Contains(cookie, "Domain=") || !strings.Contains(cookie, "Path=/w/demo/") {
		t.Fatalf("cookie %s", cookie)
	}
}
func TestProxyRejectsLoopbackDNSAndStreaming(t *testing.T) {
	if _, err := ProxyExchange(t.Context(), "http://localhost:9", httptest.NewRequest(http.MethodGet, "/", nil), nil, ProxyOptions{Allowlist: []string{"http://localhost:9"}, MaxResponse: 100, Timeout: time.Second}); err == nil {
		t.Fatal("DNS loopback accepted")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: x\n\n"))
	}))
	defer upstream.Close()
	if _, err := ProxyExchange(t.Context(), upstream.URL, httptest.NewRequest(http.MethodGet, "/", nil), nil, ProxyOptions{Allowlist: []string{upstream.URL}, MaxResponse: 100, Timeout: time.Second}); err == nil {
		t.Fatal("stream accepted")
	}
}

func TestProxyNeverForwardsOrSetsMockerAdminCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Cookie"), "mocker_session") || r.Header.Get("X-CSRF-Token") != "" {
			t.Errorf("admin credential leaked: %v", r.Header)
		}
		if r.Header.Get("Cookie") != "app=allowed" {
			t.Errorf("application cookie lost: %s", r.Header.Get("Cookie"))
		}
		w.Header().Add("Set-Cookie", "mocker_session=attacker; Path=/")
		w.Header().Add("Set-Cookie", "app=next; Path=/")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", "mocker_session=private; app=allowed")
	r.Header.Set("X-CSRF-Token", "private")
	response, err := ProxyExchange(t.Context(), upstream.URL, r, nil, ProxyOptions{Allowlist: []string{upstream.URL}, MaxResponse: 100, Timeout: time.Second, ForwardCookies: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(response.Header.Values("Set-Cookie"), ";"), "mocker_session") {
		t.Fatal("upstream can set admin cookie")
	}
}

func TestProxyHeadPreservesRepresentationLength(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1234")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	response, err := ProxyExchange(t.Context(), upstream.URL, httptest.NewRequest(http.MethodHead, "/", nil), nil, ProxyOptions{Allowlist: []string{upstream.URL}, MaxResponse: 100, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Content-Length") != "1234" {
		t.Fatal("HEAD representation length lost")
	}
}

func TestProxyRejectsUnsolicitedUpstreamUpgrade(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		_ = buf.Flush()
	}))
	defer upstream.Close()
	_, err := ProxyExchange(t.Context(), upstream.URL, httptest.NewRequest(http.MethodGet, "/", nil), nil, ProxyOptions{Allowlist: []string{upstream.URL}, MaxResponse: 100, Timeout: time.Second})
	if err == nil {
		t.Fatal("accepted unsolicited protocol upgrade")
	}
}

package mockplane

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/recordproxy"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

// proxyConnFixture serves workspace "nova" through a REAL http.Server — the
// bug this file pins lives in net/http's per-connection state, which
// httptest.NewRecorder does not have — as a record proxy (timeoutSeconds 1)
// in front of an upstream that answers only when its caller gives up.
// The server carries cmd/mocker's own timeouts, so the connection lifecycle
// is the deployed one. The returned client holds at most ONE connection to
// the plane, so every request after the first rides the connection the
// earlier ones used, exactly as an edge proxy's keep-alive pool does.
// tune adjusts the installation config once the fixture rows exist (the
// spec import answers to MaxBody too), before the plane serves anything.
func proxyConnFixture(t *testing.T, tune ...func(*config.Config)) (*http.Client, string) {
	t.Helper()
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(release) })
	cfg.ProxyAllowlist = []string{upstream.URL}
	sr := specs.NewRepo(db, cfg)
	specID := resourceIntegrationImport(t, sr, "proxy conn", rebindDocA)
	id := resourceIntegrationWorkspace(t, db, "nova", specID, domain.Settings{Seed: 1})
	pr := recordproxy.NewRepo(db)
	c := recordproxy.DefaultConfig()
	c.Mode = "record"
	c.Upstream = upstream.URL
	c.TimeoutSeconds = 1
	c.ForwardAuth = true
	if _, err := pr.Save(t.Context(), id, c); err != nil {
		t.Fatal(err)
	}
	for _, f := range tune {
		f(cfg)
	}
	p := New(cfg, workspaces.NewRepo(db), sr, runtimeTestLogger())
	p.SetProxy(recordproxy.NewService(pr, cfg))
	plane := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.ServeSlug(w, r, "nova")
	}))
	plane.Config.ReadHeaderTimeout = 5 * time.Second
	plane.Config.IdleTimeout = 120 * time.Second
	plane.Config.WriteTimeout = 30 * time.Second
	plane.Start()
	t.Cleanup(plane.Close)
	transport := &http.Transport{MaxConnsPerHost: 1}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}, plane.URL
}

func proxyConnGet(t *testing.T, ctx context.Context, client *http.Client, url string) (int, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if _, err := io.Copy(io.Discard, res.Body); err != nil {
		return 0, err
	}
	return res.StatusCode, nil
}

// requireHealthyAfter asserts the workspace keeps answering after a proxied
// request ended badly: the reported bug was every later request on the
// connection answering 500 "resolve workspace: scan workspace: context
// canceled" in under a millisecond, until the connection went away.
func requireHealthyAfter(t *testing.T, client *http.Client, base, what string) {
	t.Helper()
	for _, path := range []string{"/__mocker/health", "/__mocker/health"} {
		code, err := proxyConnGet(t, t.Context(), client, base+path)
		if err != nil || code == http.StatusInternalServerError {
			t.Fatalf("after %s: GET %s = %d, %v", what, path, code, err)
		}
	}
}

func TestProxyUpstreamTimeoutLeavesTheConnectionUsable(t *testing.T) {
	client, base := proxyConnFixture(t)
	if code, err := proxyConnGet(t, t.Context(), client, base+"/__mocker/health"); err != nil || code != http.StatusOK {
		t.Fatalf("health before: %d %v", code, err)
	}
	if code, err := proxyConnGet(t, t.Context(), client, base+"/api/status"); err != nil || code != http.StatusGatewayTimeout {
		t.Fatalf("slow upstream: %d %v, want 504", code, err)
	}
	requireHealthyAfter(t, client, base, "an upstream timeout")
}

func TestProxyClientCancelLeavesTheWorkspaceUsable(t *testing.T) {
	client, base := proxyConnFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := proxyConnGet(t, ctx, client, base+"/api/status"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("client cancel: %v", err)
	}
	// Outlive the proxy's own timeout too, so whatever the aborted handler
	// does when the upstream exchange gives up has happened before we look.
	time.Sleep(1500 * time.Millisecond)
	requireHealthyAfter(t, client, base, "a client cancel")
}

// TestProxyBodyRequestsKeepTheUploadBoundAndTheConnection pins the other
// half of the read-deadline rule: a request WITH a body still has its upload
// bounded by the proxy timeout (a stalled upload is answered, not parked
// until the client gives up), and a body that arrived in full followed by an
// upstream timeout leaves the connection as usable as a bodyless one does.
func TestProxyBodyRequestsKeepTheUploadBoundAndTheConnection(t *testing.T) {
	client, base := proxyConnFixture(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/api/upload", strings.NewReader(`{"n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("slow upstream with a body: %d, want 504", res.StatusCode)
	}
	requireHealthyAfter(t, client, base, "an upstream timeout with a body")

	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Ten bytes promised, five sent, then silence. Multipart, because the
	// plane's own body capture (reqbody.go, its 30 s deadline) never reads
	// one: the proxy's read is the first and its timeout is the bound.
	if _, err := io.WriteString(conn, "POST /api/upload HTTP/1.1\r\nHost: nova\r\nContent-Type: multipart/form-data; boundary=b\r\nContent-Length: 10\r\n\r\n12345"); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	stalled, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("stalled upload was not answered: %v", err)
	}
	_ = stalled.Body.Close()
	if stalled.StatusCode != http.StatusBadRequest || !stalled.Close {
		t.Fatalf("stalled upload: %d close=%t, want 400 and a closed connection", stalled.StatusCode, stalled.Close)
	}
}

// TestProxyOversizedBodyLeavesTheConnectionUsable: a body over MaxBody is
// answered 413, and the connection it came on is closed rather than kept
// for the next request. Two routes to the 413: a JSON body the mock plane's
// capture already read to EOF (net/http's background read is running before
// the proxy looks), and a multipart body the capture skips, which the proxy
// reads only partway. The proxy's read deadline stays armed on a refused
// body (it bounds net/http's drain of the rest), so on the first route it
// sits on that background read: today the handler returns long before it
// fires and net/http resets it, but any slow work after the refusal would
// bring back the poisoned keep-alive (measured: a 1.5 s pause before the 413
// gave 500 on the next request). Closing the connection makes that
// independent of timing. The wait outlives the 1 s deadline either way.
func TestProxyOversizedBodyLeavesTheConnectionUsable(t *testing.T) {
	client, base := proxyConnFixture(t, func(cfg *config.Config) { cfg.MaxBody = 16 })
	for _, contentType := range []string{"application/json", "multipart/form-data; boundary=b"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/api/upload", strings.NewReader(strings.Repeat("x", 100)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", contentType)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusRequestEntityTooLarge || !res.Close {
			t.Fatalf("%s: %d close=%t, want 413 and a closed connection", contentType, res.StatusCode, res.Close)
		}
		time.Sleep(1500 * time.Millisecond)
		requireHealthyAfter(t, client, base, "a 413 for "+contentType)
	}
}

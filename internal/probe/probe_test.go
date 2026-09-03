package probe_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/probe"
)

// TestHealth_response covers the ordinary case: a target that answers
// promptly with a 2xx body is reported as KindResponse with that status and
// body intact.
func TestHealth_response(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"workspace":"alex","revision":3}`))
	}))
	defer srv.Close()

	out := probe.Health(context.Background(), srv.URL)

	if out.Kind != probe.KindResponse {
		t.Fatalf("Kind = %q, want %q", out.Kind, probe.KindResponse)
	}
	if out.Status != http.StatusOK {
		t.Fatalf("Status = %d, want 200", out.Status)
	}
	if !strings.Contains(string(out.Body), `"workspace":"alex"`) {
		t.Fatalf("Body = %q, want it to contain the workspace slug", out.Body)
	}
}

// TestHealth_timeout is the test that would fail without an explicit,
// short-lived deadline on the call: a target that never answers must be
// reported as KindTimeout rather than hanging the caller (an admin request)
// for as long as the target chooses to say nothing. The server's own handler
// sleeps far longer than the context deadline given to Health, so any
// implementation that (bug) ignores ctx and waits for a real response — or
// that relies on a fixed http.Client.Timeout instead of the caller's own
// short deadline — fails this test by timing out the TEST ITSELF, not by
// reporting the wrong Kind.
func TestHealth_timeout(t *testing.T) {
	t.Parallel()

	const handlerDelay = 300 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(handlerDelay)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close() // blocks until the slow handler above actually returns — no leaked goroutine.

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	out := probe.Health(ctx, srv.URL)
	elapsed := time.Since(start)

	if out.Kind != probe.KindTimeout {
		t.Fatalf("Kind = %q, want %q", out.Kind, probe.KindTimeout)
	}
	if elapsed >= handlerDelay {
		t.Fatalf("Health took %s, at least as long as the handler's own %s sleep — it waited for the target instead of honouring the deadline", elapsed, handlerDelay)
	}
}

// TestHealth_redirectNotFollowed is the test that would fail without
// CheckRedirect refusing to follow: it proves the redirect TARGET is never
// dialed at all, not merely that the reported Kind happens to look right.
// DESIGN's own reasoning (this slice's request digest, and §15's neighbouring
// SSRF discussion) is that a redirect is the one mechanism by which this
// package's fixed, server-built target could be turned into an arbitrary
// one — so the strongest possible assertion is "the second server's handler
// was never invoked", not just "the status came back as 302".
func TestHealth_redirectNotFollowed(t *testing.T) {
	t.Parallel()

	var redirectTargetHits atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectTargetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer srv.Close()

	out := probe.Health(context.Background(), srv.URL)

	if out.Kind != probe.KindResponse {
		t.Fatalf("Kind = %q, want %q", out.Kind, probe.KindResponse)
	}
	if out.Status != http.StatusFound {
		t.Fatalf("Status = %d, want 302 (the redirect's OWN status, not one followed further)", out.Status)
	}
	if hits := redirectTargetHits.Load(); hits != 0 {
		t.Fatalf("redirect target was dialed %d time(s) — the client followed the redirect instead of refusing to", hits)
	}
}

// TestHealth_networkError covers a target that refuses the connection
// outright (a closed listener, indistinguishable here from DNS failure or a
// dropped connection — see [probe.KindNetworkError]'s own doc comment for why
// this package does not try to tell those apart).
func TestHealth_networkError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close() // closed before Health ever dials it: connection refused.

	out := probe.Health(context.Background(), url)

	if out.Kind != probe.KindNetworkError {
		t.Fatalf("Kind = %q, want %q", out.Kind, probe.KindNetworkError)
	}
}

// TestHealth_bodyCapped proves the body read is bounded: a target answering
// with far more than the health shape ever needs must not have all of it
// read into memory.
func TestHealth_bodyCapped(t *testing.T) {
	t.Parallel()

	const oversized = 64 * 1024 // far past maxBodyBytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(make([]byte, oversized)) //nolint:errcheck,gosec // the point of this test is what the CLIENT reads, not whether every byte of an intentionally oversized write lands
	}))
	defer srv.Close()

	out := probe.Health(context.Background(), srv.URL)

	if out.Kind != probe.KindResponse {
		t.Fatalf("Kind = %q, want %q", out.Kind, probe.KindResponse)
	}
	if len(out.Body) >= oversized {
		t.Fatalf("Body length = %d, want it capped well below the %d bytes the target sent", len(out.Body), oversized)
	}
}

// TestReadiness_sendsTheGivenHost is the healthcheck's whole reason for a
// second entry point: the dispatcher routes by Host, so a dial of the
// loopback address must carry MOCKER_ADMIN_HOST or it lands on no plane at
// all. The server here records what Host it saw and the test pins it to the
// value passed, not the URL's own authority.
func TestReadiness_sendsTheGivenHost(t *testing.T) {
	t.Parallel()

	var seenHost atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost.Store(r.Host)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	out := probe.Readiness(context.Background(), srv.URL+"/readyz", "mocker.local")

	if out.Kind != probe.KindResponse || out.Status != http.StatusOK {
		t.Fatalf("Kind/Status = %q/%d, want response/200", out.Kind, out.Status)
	}
	if got, _ := seenHost.Load().(string); got != "mocker.local" {
		t.Fatalf("server saw Host %q, want %q (the URL's own authority is %q)", got, "mocker.local", srv.Listener.Addr())
	}
}

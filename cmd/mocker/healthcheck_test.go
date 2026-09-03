package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

func TestHealthcheckTarget(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		":8080":          "http://127.0.0.1:8080/readyz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/readyz",
		"[::]:8080":      "http://127.0.0.1:8080/readyz",
		"10.0.0.5:8080":  "http://10.0.0.5:8080/readyz",
		"localhost:8080": "http://localhost:8080/readyz",
		"[::1]:8080":     "http://[::1]:8080/readyz",
	}
	for in, want := range cases {
		got, err := healthcheckTarget(in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "8080", "host", ":8080@evil.example", ":http"} {
		if _, err := healthcheckTarget(bad); err == nil {
			t.Errorf("%q: want an error, got none", bad)
		}
	}
}

// TestRunHealthcheck stands a server up on a loopback port, points
// MOCKER_ADDR-shaped config at it and pins the outcomes a compose
// HEALTHCHECK can see. The READY case is the one that proves the Host
// override: the handler answers 200 only to Host "mocker.local", so a
// subcommand that sent the loopback authority instead would get the 404
// branch and fail. The wrong-host case pins the other direction — a
// mismatch is a failure, never a pass — and the 503 and closed-port cases
// pin that "not ready" and "not reachable" are both exit 1 with a message
// that says which.
func TestRunHealthcheck(t *testing.T) {
	t.Parallel()

	// atomic: the handler goroutine reads it while the test goroutine
	// flips it between requests, and -race tracks no happens-before across
	// a TCP round trip.
	var ready atomic.Bool
	ready.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "mocker.local" {
			http.Error(w, `{"error":"unknown host"}`, http.StatusNotFound)
			return
		}
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"database not ready"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	cfg := &config.Config{Addr: ":" + port, AdminHost: "mocker.local"}
	if err := runHealthcheck(cfg, &stderr); err != nil {
		t.Fatalf("ready server: %v", err)
	}
	if !strings.Contains(stderr.String(), "answers ready") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	ready.Store(false)
	if err := runHealthcheck(cfg, &stderr); err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("503 server: err = %v, want a status 503 error", err)
	}

	ready.Store(true)
	wrongHost := &config.Config{Addr: ":" + port, AdminHost: "nobody.example"}
	if err := runHealthcheck(wrongHost, &stderr); err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("wrong admin host: err = %v, want a status 404 error", err)
	}

	unreachable := &config.Config{Addr: "127.0.0.1:1", AdminHost: "mocker.local"}
	if err := runHealthcheck(unreachable, &stderr); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("closed port: err = %v, want not reachable", err)
	}
}

package wsmock

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsmock_test.go observes the seam itself over a real socket (decisions.md
// mocker-p6d-websocket A5): Accept through an Unwrap-chained wrapper, the
// refusal on a writer that cannot hijack, the read limit's 1009, and the
// close-status mapping.

// unwrapOnly is a wrapper that, like httpx.StatusRecorder, implements
// Unwrap and NOT Hijacker — the shape the library must walk through.
type unwrapOnly struct{ http.ResponseWriter }

func (u unwrapOnly) Unwrap() http.ResponseWriter { return u.ResponseWriter }

func echoServer(t *testing.T, maxFrame int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Accept(unwrapOnly{w}, r, AcceptOptions{MaxFrame: maxFrame})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			typ, p, err := c.Read(r.Context())
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			werr := c.Write(ctx, typ, p)
			cancel()
			if werr != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAccept_throughAnUnwrapOnlyWrapper(t *testing.T) {
	srv := echoServer(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, status, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), DialOptions{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if status != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want 101", status)
	}
	if err := c.Write(ctx, Text, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, p, err := c.Read(ctx)
	if err != nil || typ != Text || string(p) != `{"a":1}` {
		t.Fatalf("read = (%v, %q, %v), want the text frame back", typ, p, err)
	}
	if err := c.Write(ctx, Binary, []byte{1, 2, 3}); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	typ, p, err = c.Read(ctx)
	if err != nil || typ != Binary || len(p) != 3 {
		t.Fatalf("read binary = (%v, %v, %v), want the binary frame back", typ, p, err)
	}
	if err := c.Close(StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestCanHijack_isFalseOnARecorderAndAcceptRefusesFirst(t *testing.T) {
	rec := httptest.NewRecorder()
	if CanHijack(rec) || CanHijack(unwrapOnly{rec}) {
		t.Fatal("a ResponseRecorder must not report as hijackable, wrapped or not")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := Accept(rec, req, AcceptOptions{}); !errors.Is(err, ErrCannotHijack) {
		t.Fatalf("Accept on a recorder = %v, want ErrCannotHijack", err)
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("Accept wrote %d/%q before refusing — the caller owns the refusal body", rec.Code, rec.Body.String())
	}
}

func TestReadLimit_closesWith1009(t *testing.T) {
	srv := echoServer(t, 1024)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), DialOptions{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if err := c.Write(ctx, Text, []byte(strings.Repeat("x", 2048))); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err = c.Read(ctx)
	if got := CloseStatus(err); got != StatusMessageTooBig {
		t.Fatalf("close status after an oversized frame = %d (%v), want 1009", got, err)
	}
}

func TestCloseStatus_noStatusOnAnOrdinaryError(t *testing.T) {
	if got := CloseStatus(errors.New("boom")); got != NoStatus {
		t.Fatalf("CloseStatus(plain error) = %d, want NoStatus", got)
	}
}

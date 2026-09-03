// reqbody_test.go is a WHITE-BOX test file (package mockplane, not
// mockplane_test): [captureRequestBody], [capturedBody], [restoredBody],
// [tryParseBody], [isMultipart], [requestBodyCap] and [overridesInputFor]
// are all unexported, and this file is the direct proof of reqbody.go's own
// contract — independent of the HTTP plumbing plane_test.go and
// livestate_test.go already exercise it through.
package mockplane

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// poisonReader panics on any Read — proof that [captureRequestBody] never
// even TRIES to read a body it has already decided to skip (GET/HEAD/DELETE,
// multipart): a real network body only gets drained lazily by whatever
// eventually reads it, so a spurious Read here would be visible as a panic
// long before any assertion could catch a wasted allocation.
type poisonReader struct{}

func (poisonReader) Read([]byte) (int, error) { panic("captureRequestBody must not read this body") }
func (poisonReader) Close() error             { return nil }

func TestRequestBodyCap(t *testing.T) {
	tests := []struct {
		name           string
		trafficMaxBody int64
		want           int64
	}{
		{"below the floor is raised to it", 1024, minRequestBodyCap},
		{"exactly the floor stays the floor", minRequestBodyCap, minRequestBodyCap},
		{"above the floor passes through", 1 << 20, 1 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestBodyCap(tt.trafficMaxBody); got != tt.want {
				t.Errorf("requestBodyCap(%d) = %d, want %d", tt.trafficMaxBody, got, tt.want)
			}
		})
	}
}

func TestCaptureRequestBody_NoBody(t *testing.T) {
	tests := []struct {
		name string
		req  func() *http.Request
	}{
		{"nil Body", func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, "http://x/", nil)
			req.Body = nil
			return req
		}},
		{"http.NoBody", func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, "http://x/", nil)
			req.Body = http.NoBody
			return req
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, err := captureRequestBody(httptest.NewRecorder(), tt.req(), 1024)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if cb.parseOK || cb.truncated || len(cb.bytes) != 0 {
				t.Errorf("cb = %+v, want the zero value", cb)
			}
		})
	}
}

// TestCaptureRequestBody_GETHEADDELETE_NeverReads is the direct proof of
// "GET/HEAD/DELETE with no body must cost nothing": a [poisonReader] in
// r.Body would panic the moment anything tried to Read it.
func TestCaptureRequestBody_GETHEADDELETE_NeverReads(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "http://x/", nil)
			req.Body = poisonReader{}

			cb, err := captureRequestBody(httptest.NewRecorder(), req, 1024)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(cb.bytes) != 0 || cb.truncated || cb.parseOK {
				t.Errorf("cb = %+v, want the zero value", cb)
			}
		})
	}
}

// TestCaptureRequestBody_Multipart_NeverReads proves DESIGN §8's "multipart
// не трогаем" literally: a multipart body is never read at all, not merely
// "read but not parsed".
func TestCaptureRequestBody_Multipart_NeverReads(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://x/", nil)
	req.Body = poisonReader{}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")

	cb, err := captureRequestBody(httptest.NewRecorder(), req, 1024)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(cb.bytes) != 0 || cb.truncated || cb.parseOK {
		t.Errorf("cb = %+v, want the zero value", cb)
	}
}

// TestCaptureRequestBody_WithinCap proves the ordinary case: a body under
// the cap is captured whole, marked not truncated, parsed as JSON, and
// still fully readable back off the restored r.Body afterward.
func TestCaptureRequestBody_WithinCap(t *testing.T) {
	body := `{"flag":true,"n":3}`
	req := httptest.NewRequest(http.MethodPost, "http://x/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	cb, err := captureRequestBody(httptest.NewRecorder(), req, 1024)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if cb.truncated {
		t.Error("truncated = true, want false")
	}
	if string(cb.bytes) != body {
		t.Errorf("bytes = %q, want %q", cb.bytes, body)
	}
	if !cb.parseOK {
		t.Fatal("parseOK = false, want true")
	}
	m, ok := cb.parsed.(map[string]any)
	if !ok || m["flag"] != true {
		t.Errorf("parsed = %#v, want a map with flag=true", cb.parsed)
	}

	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(restored) != body {
		t.Errorf("restored r.Body = %q, want the untouched original %q", restored, body)
	}
}

// TestCaptureRequestBody_OverCap_TruncatesButRestoresEverything proves the
// two halves of the truncation contract at once: what THIS capture keeps is
// bounded at exactly cap bytes, while what a DOWNSTREAM reader gets back
// through the restored r.Body is the entire original body, cap or no cap —
// truncation only ever bounds capture's own buffer, never what the client
// actually sent downstream.
func TestCaptureRequestBody_OverCap_TruncatesButRestoresEverything(t *testing.T) {
	const capBytes = 16
	body := bytes.Repeat([]byte("x"), capBytes+50) // well past the cap
	req := httptest.NewRequest(http.MethodPost, "http://x/", bytes.NewReader(body))

	cb, err := captureRequestBody(httptest.NewRecorder(), req, capBytes)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !cb.truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(cb.bytes) != capBytes {
		t.Errorf("kept %d bytes, want exactly the cap (%d) — this is the assertion the digest's own \"1MB body\" case is about", len(cb.bytes), capBytes)
	}
	if cb.parseOK {
		t.Error("parseOK = true, want false — a truncated body must never be parsed, even if the kept prefix happens to look like valid JSON")
	}

	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if !bytes.Equal(restored, body) {
		t.Errorf("restored r.Body has %d bytes, want the full original %d — truncation must never lose bytes for a downstream reader", len(restored), len(body))
	}
}

// TestCaptureRequestBody_ExactlyAtCap_IsNotTruncated proves the boundary:
// a body of exactly cap bytes is the largest body that does NOT overflow.
func TestCaptureRequestBody_ExactlyAtCap_IsNotTruncated(t *testing.T) {
	const capBytes = 8
	body := bytes.Repeat([]byte("y"), capBytes)
	req := httptest.NewRequest(http.MethodPost, "http://x/", bytes.NewReader(body))

	cb, err := captureRequestBody(httptest.NewRecorder(), req, capBytes)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if cb.truncated {
		t.Error("truncated = true, want false: a body of exactly cap bytes must not overflow")
	}
	if len(cb.bytes) != capBytes {
		t.Errorf("kept %d bytes, want %d", len(cb.bytes), capBytes)
	}
}

// TestCaptureRequestBody_1MBBodyNeverBuffersMoreThanTheCap is the digest's
// own bullet, verbatim: "a request with a 1 MB body against a workspace
// with no when[] anywhere: the plane must not have buffered more than the
// cap (assert the cap, not the timing)".
func TestCaptureRequestBody_1MBBodyNeverBuffersMoreThanTheCap(t *testing.T) {
	const capBytes = minRequestBodyCap // the default cap a bare MOCKER_TRAFFIC_MAX_BODY floors to
	body := bytes.Repeat([]byte("z"), 1<<20)
	req := httptest.NewRequest(http.MethodPost, "http://x/", bytes.NewReader(body))

	cb, err := captureRequestBody(httptest.NewRecorder(), req, capBytes)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !cb.truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(cb.bytes) != capBytes {
		t.Fatalf("buffered %d bytes, want exactly the cap (%d), never the full 1 MB body", len(cb.bytes), capBytes)
	}
}

// TestCaptureRequestBody_RestoreClosesOriginal proves Close is forwarded to
// the ORIGINAL body, never swallowed by a NopCloser-style wrapper.
func TestCaptureRequestBody_RestoreClosesOriginal(t *testing.T) {
	orig := &closeTrackingReader{Reader: strings.NewReader("hello")}
	req := httptest.NewRequest(http.MethodPost, "http://x/", nil)
	req.Body = orig

	if _, err := captureRequestBody(httptest.NewRecorder(), req, 1024); err != nil {
		t.Fatalf("captureRequestBody: %v", err)
	}
	if err := req.Body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !orig.closed {
		t.Error("original body was never closed")
	}
}

type closeTrackingReader struct {
	io.Reader
	closed bool
}

func (c *closeTrackingReader) Close() error {
	c.closed = true
	return nil
}

// erroringReader always fails with a non-EOF error — standing in for
// http.MaxBytesReader tripping MOCKER_MAX_BODY mid-read, the one real-world
// case [captureRequestBody]'s own doc comment names.
type erroringReader struct{ err error }

func (e erroringReader) Read([]byte) (int, error) { return 0, e.err }
func (e erroringReader) Close() error             { return nil }

func TestCaptureRequestBody_PropagatesReadError(t *testing.T) {
	wantErr := errors.New("http: request body too large")
	req := httptest.NewRequest(http.MethodPost, "http://x/", nil)
	req.Body = erroringReader{err: wantErr}

	cb, err := captureRequestBody(httptest.NewRecorder(), req, 1024)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v propagated, not folded into \"no body\"", err, wantErr)
	}
	if cb != nil {
		t.Errorf("cb = %+v, want nil on a real read error", cb)
	}
}

// TestCaptureRequestBody_ParseTolerance is DESIGN §8's own parsing table,
// proven directly: application/json and text/plain and no Content-Type at
// all are all tried as JSON; anything else — even a body that WOULD parse —
// is left untouched. A parse failure is never an error (BodyOK false is the
// whole answer), matching "не отвечаем 400".
func TestCaptureRequestBody_ParseTolerance(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantParseOK bool
	}{
		{"application/json, valid", "application/json", `{"a":1}`, true},
		{"application/json, invalid — not a 400, just BodyOK=false", "application/json", `not json`, false},
		{"application/json with charset param", "application/json; charset=utf-8", `{"a":1}`, true},
		{"text/plain carrying JSON", "text/plain", `{"a":1}`, true},
		{"no Content-Type at all, carrying JSON — DESIGN §8's own tolerance", "", `{"a":1}`, true},
		{"no Content-Type, not JSON", "", `plain text`, false},
		{"application/xml is never attempted even though the body is valid JSON", "application/xml", `{"a":1}`, false},
		{"multipart is filtered before this function is ever called; not exercised here", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://x/", strings.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			cb, err := captureRequestBody(httptest.NewRecorder(), req, 1024)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if cb.parseOK != tt.wantParseOK {
				t.Errorf("parseOK = %v, want %v (parsed=%#v)", cb.parseOK, tt.wantParseOK, cb.parsed)
			}
		})
	}
}

func TestOverridesInputFor(t *testing.T) {
	t.Run("no capturedBody attached: Query/Header still populated, BodyOK false", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://x/?verbose=1", nil)
		req.Header.Set("X-Test", "yes")

		in := overridesInputFor(req)
		if in.Query.Get("verbose") != "1" {
			t.Errorf("query = %v, want verbose=1", in.Query)
		}
		if in.Header.Get("X-Test") != "yes" {
			t.Errorf("header = %v, want X-Test=yes", in.Header)
		}
		if in.BodyOK {
			t.Error("BodyOK = true, want false: no capturedBody was ever attached")
		}
	})

	t.Run("a parsed capturedBody threads Body/BodyOK through", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://x/", nil)
		req = attachCapturedBody(req, &capturedBody{parsed: map[string]any{"flag": true}, parseOK: true})

		in := overridesInputFor(req)
		if !in.BodyOK {
			t.Fatal("BodyOK = false, want true")
		}
		m, ok := in.Body.(map[string]any)
		if !ok || m["flag"] != true {
			t.Errorf("Body = %#v, want the attached map", in.Body)
		}
	})

	t.Run("a truncated capturedBody never surfaces as BodyOK", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://x/", nil)
		req = attachCapturedBody(req, &capturedBody{bytes: []byte(`{"flag"`), truncated: true})

		in := overridesInputFor(req)
		if in.BodyOK {
			t.Error("BodyOK = true, want false: a truncated body must never look parsed")
		}
	})
}

// TestCaptureRequestBody_SmallBodyDoesNotAllocateTheFullCap is round-1
// review finding 6, proven directly: a tiny body against a large cap must
// not leave cb.bytes backed by a capBytes-sized array. The old
// make([]byte, capBytes+1)-then-io.ReadFull shape always allocated the FULL
// cap up front regardless of what actually arrived, and handed a sub-slice
// of THAT array back out — so cap(cb.bytes) tracked capBytes, not len. The
// fix (io.ReadAll over a bounded LimitReader, then a right-sized copy) must
// make cap(cb.bytes) track exactly len(cb.bytes), even when capBytes is
// huge.
func TestCaptureRequestBody_SmallBodyDoesNotAllocateTheFullCap(t *testing.T) {
	const capBytes = 10 << 20 // 10 MiB — MOCKER_TRAFFIC_MAX_BODY's default-ish ceiling
	body := []byte("a")       // one byte
	req := httptest.NewRequest(http.MethodPost, "http://x/", bytes.NewReader(body))

	cb, err := captureRequestBody(httptest.NewRecorder(), req, capBytes)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(cb.bytes) != 1 {
		t.Fatalf("len(cb.bytes) = %d, want 1", len(cb.bytes))
	}
	if cap(cb.bytes) != len(cb.bytes) {
		t.Errorf("cap(cb.bytes) = %d, len = %d — a 1-byte body must not pin a larger backing array "+
			"(the old code pinned capBytes+1 = %d bytes here, regardless of the real body size)",
			cap(cb.bytes), len(cb.bytes), capBytes+1)
	}
}

// TestCaptureRequestBody_SlowBodyIsBoundedByReadDeadline is round-1 review
// finding 1's other half, proven end to end over a real TCP connection (a
// deadline set through http.ResponseController does nothing against
// httptest.NewRecorder — it only takes effect on a real server connection,
// which is exactly the shape the finding's attack uses): a client that
// completes its headers and then sends nothing further must not hold
// captureRequestBody's read open forever. requestBodyReadDeadline is
// shrunk for the duration of this test so it does not have to wait out the
// real 30s ceiling to prove the deadline fires.
func TestCaptureRequestBody_SlowBodyIsBoundedByReadDeadline(t *testing.T) {
	orig := requestBodyReadDeadline
	requestBodyReadDeadline = 50 * time.Millisecond
	defer func() { requestBodyReadDeadline = orig }()

	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := captureRequestBody(w, r, 1024)
		done <- err
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Headers only, promising a 100-byte body that never follows — the
	// slow-body shape the finding describes ("valid headers ... and then
	// stops sending").
	req := "POST / HTTP/1.1\r\nHost: x\r\nContent-Type: application/octet-stream\r\nContent-Length: 100\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write headers: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("captureRequestBody returned nil error, want a read-deadline error for a body that never arrived")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("captureRequestBody did not return within 5s of a 50ms read deadline — the deadline was never armed")
	}
}

package admin_test

// stream_handlers_test.go covers P6a's two routes (decisions.md
// mocker-p6a-sse): GET /api/workspaces/{id}/traffic/stream and
// GET /api/stream/stats. Every test that opens a stream runs it over a REAL
// socket (D21) — an httptest.Server around the admin handler and a live
// http.Client — because httptest.ResponseRecorder cannot take a write
// deadline and therefore exercises exactly one path of the handler, D9's
// 501, which is the one test here that uses it.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// streamTestServer is [testServer] plus the three things a stream test
// reaches past the HTTP surface for: the registry (Stats), the recorder
// (Record + Flush is how a row comes to exist and how the registry is
// nudged, exactly as in production), and a real listener.
type streamTestServer struct {
	*testServer
	reg  *stream.Registry
	rec  *traffic.Recorder
	live *httptest.Server
}

// newStreamTestServer builds a fully wired admin.Server the way
// cmd/mocker/main.go does — recorder, registry as the recorder's Notifier,
// SetStream with opts — and serves it on a real port. wire=false leaves
// SetStream uncalled, the "no registry in this deployment" state. The
// listener's own WriteTimeout is set DELIBERATELY SHORT (300 ms, against
// production's 30 s) so that any test holding a stream open longer than
// that proves the per-connection exemption (DESIGN §30.6, D4) rather than
// merely not tripping the global value.
func newStreamTestServer(t *testing.T, opts admin.StreamOptions, wire bool) *streamTestServer {
	t.Helper()
	cfg := testConfig(t)
	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := admin.New(cfg, sessions, ws, db, log)

	rec := traffic.NewRecorder(db, log, traffic.Options{})
	srv.SetTraffic(rec)
	reg := stream.NewRegistry()
	if wire {
		rec.SetNotifier(reg)
		srv.SetStream(reg, nil, opts)
	}

	live := httptest.NewUnstartedServer(srv.Handler())
	live.Config.WriteTimeout = 300 * time.Millisecond
	live.Start()
	// Registry BEFORE the listener (D13's order): Close cancels every live
	// connection and joins its handler, so the listener's own Close below
	// has nothing left to wait for — and goleak sees no handler goroutine
	// outlive the test.
	t.Cleanup(func() {
		reg.Close()
		live.Close()
	})

	return &streamTestServer{
		testServer: &testServer{handler: srv.Handler(), db: db, cfg: cfg},
		reg:        reg,
		rec:        rec,
		live:       live,
	}
}

func fastStreamOptions() admin.StreamOptions {
	return admin.StreamOptions{Ping: time.Hour, FrameTimeout: 2 * time.Second, SessionRecheck: time.Hour}
}

// liveResp is an *http.Response whose Body is closed by t.Cleanup — wrapped
// so that a caller which deliberately leaves a stream open for the test's
// whole life is not misread by the bodyclose linter as a leaked body.
type liveResp struct {
	*http.Response
}

// openStream issues the GET over the real socket and returns the response
// unread. The caller reads frames off resp.Body with [sseScanner] and may
// close it early (idempotent); t.Cleanup closes it as a backstop.
func (ts *streamTestServer) openStream(t *testing.T, cookie *http.Cookie, path string, headers map[string]string) liveResp {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.live.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "mocker.local"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := ts.live.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return liveResp{resp}
}

// waitOpen polls the registry until n connections are live: the handler
// registers on its own schedule, not the client's.
func (ts *streamTestServer) waitOpen(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for ts.reg.Stats().Open != n {
		if time.Now().After(deadline) {
			t.Fatalf("registry Open never reached %d (at %d)", n, ts.reg.Stats().Open)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// recordAndFlush is how a traffic row comes to exist in production: the
// mock plane enqueues, the recorder's writer commits the batch, and the
// registry is nudged AFTER the commit.
func (ts *streamTestServer) recordAndFlush(t *testing.T, wsID int64, path string) {
	t.Helper()
	ts.rec.Record(traffic.Event{WorkspaceID: wsID, TS: time.Now(), Method: "GET", Path: path, Status: 200})
	if err := ts.rec.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

// sseScanner yields one SSE frame per Scan (everything up to the blank
// line), with a buffer sized for D6's largest legal frame — 200 rows of
// bodies capped at the 8 KiB default — so a test never fails on a frame the
// server is entitled to send.
func sseScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.Index(data, []byte("\n\n")); i >= 0 {
			return i + 2, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	return sc
}

// trafficFrame is one decoded `event: traffic` frame.
type trafficFrame struct {
	id   int64
	data struct {
		Rows []struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
		} `json:"rows"`
		LastID  int64 `json:"lastId"`
		Dropped int64 `json:"dropped"`
	}
}

// nextTrafficFrame reads frames until an `event: traffic` one arrives,
// skipping `:` comments, and decodes it. It fails the test on EOF or after
// the deadline.
func nextTrafficFrame(t *testing.T, sc *bufio.Scanner) trafficFrame {
	t.Helper()
	type result struct {
		fr trafficFrame
		ok bool
	}
	ch := make(chan result, 1)
	go func() {
		for sc.Scan() {
			raw := sc.Text()
			if strings.HasPrefix(raw, ":") {
				continue
			}
			var fr trafficFrame
			var dataLine string
			for _, line := range strings.Split(raw, "\n") {
				switch {
				case strings.HasPrefix(line, "id: "):
					fmt.Sscan(strings.TrimPrefix(line, "id: "), &fr.id)
				case strings.HasPrefix(line, "data: "):
					dataLine = strings.TrimPrefix(line, "data: ")
				}
			}
			if !strings.Contains(raw, "event: traffic") || dataLine == "" {
				continue
			}
			if err := json.Unmarshal([]byte(dataLine), &fr.data); err != nil {
				ch <- result{}
				return
			}
			ch <- result{fr: fr, ok: true}
			return
		}
		ch <- result{}
	}()
	select {
	case r := <-ch:
		if !r.ok {
			t.Fatalf("stream ended (or carried an undecodable frame) before a traffic frame arrived: %v", sc.Err())
		}
		return r.fr
	case <-time.After(5 * time.Second):
		t.Fatal("no traffic frame within 5s")
	}
	return trafficFrame{}
}

// waitEOF reads the body to its end and reports whether that happened
// within d.
func waitEOF(body io.Reader, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, body)
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func errorCodeOf(t *testing.T, body io.Reader) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(body)
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode error envelope %q: %v", raw, err)
	}
	return env.Error.Code
}

func TestStreamTraffic_refusalsBeforeTheHandshake(t *testing.T) {
	t.Parallel()
	ts := newStreamTestServer(t, fastStreamOptions(), true)
	cookie, _, wsID, _ := ts.createWorkspace(t, "stream-refusals", "ws")

	// 401: no session at all.
	resp := ts.openStream(t, nil, fmt.Sprintf("/api/workspaces/%d/traffic/stream", int64(wsID)), nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no cookie: status = %d, want 401", resp.StatusCode)
	}
	if got := errorCodeOf(t, resp.Body); got != "unauthorized" {
		t.Errorf("no cookie: code = %q, want unauthorized", got)
	}

	// 404: a workspace that does not exist.
	resp = ts.openStream(t, cookie, "/api/workspaces/424242/traffic/stream", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown workspace: status = %d, want 404", resp.StatusCode)
	}

	// 400: a non-numeric id.
	resp = ts.openStream(t, cookie, "/api/workspaces/abc/traffic/stream", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad id: status = %d, want 400", resp.StatusCode)
	}

	// None of the refusals registered a connection.
	if got := ts.reg.Stats().Open; got != 0 {
		t.Errorf("Open after three refusals = %d, want 0", got)
	}
}

func TestStreamTraffic_noRegistryWiredAnswers503(t *testing.T) {
	t.Parallel()
	ts := newStreamTestServer(t, fastStreamOptions(), false)
	cookie, _, wsID, _ := ts.createWorkspace(t, "stream-unwired", "ws")

	resp := ts.openStream(t, cookie, fmt.Sprintf("/api/workspaces/%d/traffic/stream", int64(wsID)), nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := errorCodeOf(t, resp.Body); got != "service_unavailable" {
		t.Errorf("code = %q, want service_unavailable", got)
	}

	statsResp := ts.openStream(t, cookie, "/api/stream/stats", nil)
	if statsResp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("stats status = %d, want 503 with no registry wired", statsResp.StatusCode)
	}
}

// TestStreamTraffic_deliversRowsAndResumesOnLastEventID is D5, D6 and D7 end
// to end over a real socket: the handshake headers, a frame delivered on a
// nudge with the poll view as its payload and the cursor as its id, no CORS
// allowance on the response (D2), and a reconnect carrying Last-Event-ID
// receiving only what came after it even though its URL says since=0.
func TestStreamTraffic_deliversRowsAndResumesOnLastEventID(t *testing.T) {
	t.Parallel()
	ts := newStreamTestServer(t, fastStreamOptions(), true)
	cookie, _, wsFloat, _ := ts.createWorkspace(t, "stream-rows", "ws")
	wsID := int64(wsFloat)
	path := fmt.Sprintf("/api/workspaces/%d/traffic/stream?since=0", wsID)

	resp := ts.openStream(t, cookie, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	for name := range resp.Header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("response carries %s — the admin plane must emit no cross-origin allowance on this route (D2)", name)
		}
	}
	ts.waitOpen(t, 1)

	sc := sseScanner(resp.Body)
	ts.recordAndFlush(t, wsID, "/first")
	fr := nextTrafficFrame(t, sc)
	if len(fr.data.Rows) != 1 || fr.data.Rows[0].Path != "/first" {
		t.Fatalf("first frame rows = %+v, want exactly the /first row", fr.data.Rows)
	}
	if fr.id != fr.data.LastID || fr.id != fr.data.Rows[0].ID {
		t.Fatalf("frame id = %d, data.lastId = %d, row id = %d: the SSE id must be the cursor (D6)", fr.id, fr.data.LastID, fr.data.Rows[0].ID)
	}
	first := fr.id

	// Two more rows, one flush: one nudge, one frame carrying both, in id
	// order, with lastId at the newest.
	ts.rec.Record(traffic.Event{WorkspaceID: wsID, TS: time.Now(), Method: "GET", Path: "/second", Status: 200})
	ts.recordAndFlush(t, wsID, "/third")
	fr = nextTrafficFrame(t, sc)
	if len(fr.data.Rows) != 2 || fr.data.Rows[0].Path != "/second" || fr.data.Rows[1].Path != "/third" {
		t.Fatalf("second frame rows = %+v, want /second then /third", fr.data.Rows)
	}
	if fr.id != first+2 {
		t.Fatalf("second frame id = %d, want %d", fr.id, first+2)
	}
	_ = resp.Body.Close()
	ts.waitOpen(t, 0)

	// The browser's reconnect: same URL (since=0), Last-Event-ID set. D7
	// says the header wins, so the replay starts AFTER `first`, not at 0.
	resp2 := ts.openStream(t, cookie, path, map[string]string{"Last-Event-ID": fmt.Sprint(first)})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("reconnect status = %d, want 200", resp2.StatusCode)
	}
	ts.waitOpen(t, 1)
	sc2 := sseScanner(resp2.Body)
	ts.recordAndFlush(t, wsID, "/fourth")
	fr = nextTrafficFrame(t, sc2)
	paths := make([]string, 0, len(fr.data.Rows))
	for _, r := range fr.data.Rows {
		paths = append(paths, r.Path)
	}
	if strings.Join(paths, ",") != "/second,/third,/fourth" {
		t.Fatalf("resumed frame paths = %v, want /second,/third,/fourth — everything after Last-Event-ID and nothing before it", paths)
	}
}

// TestStreamTraffic_outlivesTheServersWriteTimeout is DESIGN §30.6's
// exemption, observed: the listener's WriteTimeout is 300 ms
// (newStreamTestServer), the connection is held for four times that with
// pings flowing, and a frame still arrives at the end. A handler that
// inherited the global deadline would have been cut at 300 ms.
func TestStreamTraffic_outlivesTheServersWriteTimeout(t *testing.T) {
	t.Parallel()
	opts := fastStreamOptions()
	opts.Ping = 100 * time.Millisecond
	ts := newStreamTestServer(t, opts, true)
	cookie, _, wsFloat, _ := ts.createWorkspace(t, "stream-deadline", "ws")
	wsID := int64(wsFloat)

	resp := ts.openStream(t, cookie, fmt.Sprintf("/api/workspaces/%d/traffic/stream", wsID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ts.waitOpen(t, 1)
	sc := sseScanner(resp.Body)

	pings := 0
	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !sc.Scan() {
			t.Fatalf("stream ended after %d ping(s): %v — the handler did not exempt itself from the listener's 300ms WriteTimeout", pings, sc.Err())
		}
		if strings.HasPrefix(sc.Text(), ":") {
			pings++
		}
	}
	if pings < 6 {
		t.Fatalf("only %d pings in 1.2s at a 100ms interval", pings)
	}
	ts.recordAndFlush(t, wsID, "/late")
	fr := nextTrafficFrame(t, sc)
	if len(fr.data.Rows) != 1 || fr.data.Rows[0].Path != "/late" {
		t.Fatalf("frame after 1.2s = %+v, want the /late row", fr.data.Rows)
	}
}

// TestStreamTraffic_sessionRecheckClosesALoggedOutConnection is D11's
// session half: a logout in "another tab" (an in-process POST /api/auth/logout
// with the same cookie) closes the stream on the next recheck tick.
func TestStreamTraffic_sessionRecheckClosesALoggedOutConnection(t *testing.T) {
	t.Parallel()
	opts := fastStreamOptions()
	opts.SessionRecheck = 50 * time.Millisecond
	ts := newStreamTestServer(t, opts, true)
	cookie, csrf, wsFloat, _ := ts.createWorkspace(t, "stream-logout", "ws")
	wsID := int64(wsFloat)

	resp := ts.openStream(t, cookie, fmt.Sprintf("/api/workspaces/%d/traffic/stream", wsID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ts.waitOpen(t, 1)

	// Still open across a couple of ticks while the session is valid.
	time.Sleep(150 * time.Millisecond)
	if got := ts.reg.Stats().Open; got != 1 {
		t.Fatalf("Open with a valid session = %d, want 1 — the recheck must not close a live session", got)
	}

	rec := ts.do(jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/logout", nil, cookie, csrf))
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("logout: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if !waitEOF(resp.Body, 3*time.Second) {
		t.Fatal("stream still open 3s after logout — the session is re-validated only at the handshake")
	}
	ts.waitOpen(t, 0)
}

// TestStreamTraffic_workspaceRecheckClosesOnAReissuedID is D11's workspace
// half and A18 in-process: W is created LAST so its id is the one SQLite
// reissues (workspaces.id is INTEGER PRIMARY KEY, and a bare rowid is
// max+1); a stream is opened on W; W is deleted; a new workspace is created
// and asserted to carry W's id; traffic is recorded against the NEW
// workspace at once — and the connection closes having delivered not one
// row of it. An id-only recheck would keep serving.
func TestStreamTraffic_workspaceRecheckClosesOnAReissuedID(t *testing.T) {
	t.Parallel()
	opts := fastStreamOptions()
	opts.SessionRecheck = 50 * time.Millisecond
	ts := newStreamTestServer(t, opts, true)
	cookie, csrf, wFloat, _ := ts.createWorkspace(t, "stream-reissue", "w")
	wID := int64(wFloat)

	resp := ts.openStream(t, cookie, fmt.Sprintf("/api/workspaces/%d/traffic/stream", wID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ts.waitOpen(t, 1)

	del := ts.do(jsonRequest(t, http.MethodDelete, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wID), nil, cookie, csrf))
	if del.Code != http.StatusNoContent && del.Code != http.StatusOK {
		t.Fatalf("delete workspace: status = %d; body = %s", del.Code, del.Body.String())
	}
	create := ts.do(jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces", map[string]string{"name": "impostor"}, cookie, csrf))
	if create.Code != http.StatusCreated {
		t.Fatalf("create replacement: status = %d; body = %s", create.Code, create.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID != wID {
		t.Fatalf("replacement id = %d, want W's own %d — the fixture must actually reissue the id for this test to mean anything", created.ID, wID)
	}
	// IMMEDIATELY: the recheck may close the connection on its very next
	// tick, and a row recorded after the close leaks nothing.
	ts.recordAndFlush(t, created.ID, "/impostor-row")

	sc := sseScanner(resp.Body)
	done := make(chan string, 1)
	go func() {
		for sc.Scan() {
			if strings.Contains(sc.Text(), "event: traffic") {
				done <- sc.Text()
				return
			}
		}
		done <- ""
	}()
	select {
	case frame := <-done:
		if frame != "" {
			t.Fatalf("the connection opened against the deleted workspace received a frame of the replacement's traffic: %q", frame)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream still open 3s after its workspace was deleted and its id reissued — the recheck compares ids, not identity")
	}
	ts.waitOpen(t, 0)
}

// TestStreamTraffic_unsupportedWriterAnswers501 is D9 and D21's ONE
// recorder-based test: httptest.ResponseRecorder implements http.Flusher
// but not SetWriteDeadline, so the handler must answer 501
// streaming_unsupported before writing a frame, count it, and not leave a
// connection registered.
func TestStreamTraffic_unsupportedWriterAnswers501(t *testing.T) {
	t.Parallel()
	ts := newStreamTestServer(t, fastStreamOptions(), true)
	cookie, _, wsFloat, _ := ts.createWorkspace(t, "stream-501", "ws")

	req := jsonRequest(t, http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/traffic/stream", int64(wsFloat)), nil, cookie, "")
	rec := ts.do(req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body = %s", rec.Code, rec.Body.String())
	}
	if got := errorCodeOf(t, rec.Body); got != "streaming_unsupported" {
		t.Errorf("code = %q, want streaming_unsupported", got)
	}
	st := ts.reg.Stats()
	if st.RefusedUnsupported != 1 || st.Open != 0 || st.RefusedCap != 0 {
		t.Errorf("stats after the refusal = %+v, want refusedUnsupported 1, open 0, refusedCap 0", st)
	}
}

// TestStreamTraffic_capRefusesTheSixtyFifth is D8 over real sockets: 64
// connections are admitted, the 65th handshake answers 503 with the
// standard envelope, refusedCap moves by one, and closing them all brings
// open back to zero.
func TestStreamTraffic_capRefusesTheSixtyFifth(t *testing.T) {
	t.Parallel()
	ts := newStreamTestServer(t, fastStreamOptions(), true)
	cookie, _, wsFloat, _ := ts.createWorkspace(t, "stream-cap", "ws")
	path := fmt.Sprintf("/api/workspaces/%d/traffic/stream", int64(wsFloat))

	before := ts.reg.Stats()
	if before.Cap != 64 {
		t.Fatalf("cap = %d, want 64", before.Cap)
	}
	bodies := make([]io.Closer, 0, before.Cap)
	for i := range before.Cap {
		resp := ts.openStream(t, cookie, path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("connection %d: status = %d, want 200", i+1, resp.StatusCode)
		}
		bodies = append(bodies, resp.Body)
	}
	ts.waitOpen(t, before.Cap)

	resp := ts.openStream(t, cookie, path, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("65th: status = %d, want 503", resp.StatusCode)
	}
	if got := errorCodeOf(t, resp.Body); got != "service_unavailable" {
		t.Errorf("65th: code = %q, want service_unavailable", got)
	}
	if st := ts.reg.Stats(); st.RefusedCap != before.RefusedCap+1 || st.Open != before.Cap {
		t.Errorf("stats after the 65th = %+v, want refusedCap +1 and open still %d", st, before.Cap)
	}
	for _, b := range bodies {
		_ = b.Close()
	}
	ts.waitOpen(t, 0)
}

// TestStreamStats covers D15/D16's route: 401 without a session, the six
// fields with a session, and the three-read shape A13 uses — open rises by
// one while a stream is live and returns when it closes, with byWorkspace
// naming the workspace only while it holds a connection.
func TestStreamStats(t *testing.T) {
	t.Parallel()
	ts := newStreamTestServer(t, fastStreamOptions(), true)
	cookie, _, wsFloat, _ := ts.createWorkspace(t, "stream-stats", "ws")
	wsID := int64(wsFloat)

	if resp := ts.openStream(t, nil, "/api/stream/stats", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no cookie: status = %d, want 401", resp.StatusCode)
	}

	read := func() map[string]json.RawMessage {
		t.Helper()
		resp := ts.openStream(t, cookie, "/api/stream/stats", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stats: status = %d, want 200", resp.StatusCode)
		}
		var m map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			t.Fatalf("decode stats: %v", err)
		}
		return m
	}
	openOf := func(m map[string]json.RawMessage) int {
		var n int
		_ = json.Unmarshal(m["open"], &n)
		return n
	}

	first := read()
	for _, field := range []string{"open", "cap", "refusedCap", "refusedUnsupported", "coalescedNudges", "byWorkspace"} {
		if _, ok := first[field]; !ok {
			t.Errorf("stats lacks %q: %s", field, first)
		}
	}
	if string(first["byWorkspace"]) != "[]" {
		t.Errorf("byWorkspace with nothing connected = %s, want [] (never null)", first["byWorkspace"])
	}

	resp := ts.openStream(t, cookie, fmt.Sprintf("/api/workspaces/%d/traffic/stream", wsID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: status = %d", resp.StatusCode)
	}
	ts.waitOpen(t, 1)
	second := read()
	if openOf(second) != openOf(first)+1 {
		t.Errorf("open while live = %d, want %d", openOf(second), openOf(first)+1)
	}
	if want := fmt.Sprintf(`[{"workspaceId":%d,"open":1}]`, wsID); string(second["byWorkspace"]) != want {
		t.Errorf("byWorkspace while live = %s, want %s", second["byWorkspace"], want)
	}

	_ = resp.Body.Close()
	ts.waitOpen(t, 0)
	third := read()
	if openOf(third) != openOf(first) {
		t.Errorf("open after close = %d, want back to %d", openOf(third), openOf(first))
	}
}

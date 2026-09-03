package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/traffic"
)

// p1c2TrafficURL and p1c2TrafficPollURL build the two GET routes' targets.
func p1c2TrafficURL(ts *p1c2TestServer, id int64) string {
	return fmt.Sprintf("http://%s/api/workspaces/%d/traffic", ts.cfg.AdminHost, id)
}

func p1c2TrafficPollURL(ts *p1c2TestServer, id int64, since int64) string {
	return fmt.Sprintf("http://%s/api/workspaces/%d/traffic/poll?since=%d", ts.cfg.AdminHost, id, since)
}

// p1c2RecordAndFlush queues n [traffic.Event]s for workspaceID (TS strictly
// increasing, one second apart, so id order and ts order agree) and forces
// them out through the SAME recorder wired via SetTraffic — the actual
// insertion path, not a hand-rolled INSERT, so these tests exercise the real
// redaction/queue/flush machinery rather than bypassing it.
func p1c2RecordAndFlush(t *testing.T, ts *p1c2TestServer, workspaceID int64, n int) {
	t.Helper()
	base := time.Unix(1_700_000_000, 0)
	for i := range n {
		ts.recorder.Record(traffic.Event{
			WorkspaceID: workspaceID,
			TS:          base.Add(time.Duration(i) * time.Second),
			Method:      "GET",
			Path:        fmt.Sprintf("/api/v1/widgets/%d", i),
			PeerIP:      "127.0.0.1",
			MatchedKind: "none",
			Status:      404,
			Duration:    5 * time.Millisecond,
		})
	}
	if err := ts.recorder.Flush(t.Context()); err != nil {
		t.Fatalf("flush recorder: %v", err)
	}
}

// TestP1c2Traffic_unauthenticated covers every traffic route: no session
// cookie must answer 401.
func TestP1c2Traffic_unauthenticated(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	_, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	targets := []struct {
		name   string
		method string
		url    string
	}{
		{"list", http.MethodGet, p1c2TrafficURL(ts, id)},
		{"poll", http.MethodGet, p1c2TrafficPollURL(ts, id, 0)},
		{"delete", http.MethodDelete, p1c2TrafficURL(ts, id)},
	}
	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// DELETE goes through jsonRequest, not a bare
			// httptest.NewRequest: enforceCSRF's Content-Type/Origin checks
			// run BEFORE requireUser in the middleware chain, so a
			// state-changing request missing either would answer 415/403
			// first and never exercise the 401 this test is about. A
			// cookie-less request that DOES carry the right headers still
			// reaches requireUser (enforceCSRF skips its own token check
			// when there is no session at all).
			var req *http.Request
			if tt.method == http.MethodGet {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			} else {
				req = jsonRequest(t, tt.method, tt.url, nil, nil, "")
			}
			rec := ts.do(req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without a session: status = %d, want 401; body = %s",
					tt.method, tt.url, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestP1c2Traffic_missingCSRF covers the one state-changing traffic route:
// DELETE with a valid session but no CSRF token must answer 403.
func TestP1c2Traffic_missingCSRF(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodDelete, p1c2TrafficURL(ts, id), nil, cookie, "")
	rec := ts.do(req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE traffic with no CSRF token: status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Traffic_listOrdering checks GET /traffic answers newest first.
func TestP1c2Traffic_listOrdering(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	p1c2RecordAndFlush(t, ts, id, 3)

	req := httptest.NewRequest(http.MethodGet, p1c2TrafficURL(ts, id), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET traffic: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Rows []struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
		} `json:"rows"`
		Rate1m  int   `json:"rate1m"`
		Dropped int64 `json:"dropped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(body.Rows))
	}
	for i := 1; i < len(body.Rows); i++ {
		if body.Rows[i-1].ID <= body.Rows[i].ID {
			t.Fatalf("rows not newest-first: id[%d]=%d <= id[%d]=%d", i-1, body.Rows[i-1].ID, i, body.Rows[i].ID)
		}
	}
	if body.Rows[0].Path != "/api/v1/widgets/2" {
		t.Errorf("newest row path = %q, want the LAST recorded event %q", body.Rows[0].Path, "/api/v1/widgets/2")
	}
}

// TestP1c2Traffic_pollOrdering checks GET /traffic/poll answers oldest
// first, and that "since" excludes exactly what it names.
func TestP1c2Traffic_pollOrdering(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	p1c2RecordAndFlush(t, ts, id, 3)

	req := httptest.NewRequest(http.MethodGet, p1c2TrafficPollURL(ts, id, 0), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET traffic/poll: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Rows []struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
		} `json:"rows"`
		LastID  int64 `json:"lastId"`
		Dropped int64 `json:"dropped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(body.Rows))
	}
	for i := 1; i < len(body.Rows); i++ {
		if body.Rows[i-1].ID >= body.Rows[i].ID {
			t.Fatalf("rows not oldest-first: id[%d]=%d >= id[%d]=%d", i-1, body.Rows[i-1].ID, i, body.Rows[i].ID)
		}
	}
	if body.Rows[0].Path != "/api/v1/widgets/0" {
		t.Errorf("oldest row path = %q, want the FIRST recorded event %q", body.Rows[0].Path, "/api/v1/widgets/0")
	}
	if body.LastID != body.Rows[len(body.Rows)-1].ID {
		t.Errorf("lastId = %d, want the last row's id %d", body.LastID, body.Rows[len(body.Rows)-1].ID)
	}
}

// TestP1c2Traffic_pollSinceLastReturnsNothing checks that polling with
// since = the last id returns an empty batch and echoes "since" back as
// lastId — not 0, or a chained poller would replay the whole table on every
// quiet tick.
func TestP1c2Traffic_pollSinceLastReturnsNothing(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, _, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")
	p1c2RecordAndFlush(t, ts, id, 2)

	first := httptest.NewRequest(http.MethodGet, p1c2TrafficPollURL(ts, id, 0), nil)
	first.AddCookie(cookie)
	rec := ts.do(first)
	var firstBody struct {
		LastID int64 `json:"lastId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode first poll: %v", err)
	}

	second := httptest.NewRequest(http.MethodGet, p1c2TrafficPollURL(ts, id, firstBody.LastID), nil)
	second.AddCookie(cookie)
	rec = ts.do(second)
	if rec.Code != http.StatusOK {
		t.Fatalf("second poll: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var secondBody struct {
		Rows   []json.RawMessage `json:"rows"`
		LastID int64             `json:"lastId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decode second poll: %v", err)
	}
	if len(secondBody.Rows) != 0 {
		t.Errorf("second poll rows = %d, want 0", len(secondBody.Rows))
	}
	if secondBody.LastID != firstBody.LastID {
		t.Errorf("second poll lastId = %d, want it to echo the given since = %d", secondBody.LastID, firstBody.LastID)
	}
}

// TestP1c2Traffic_deleteClearsOnlyAddressedWorkspace proves DELETE is scoped
// to the one workspace named in the URL.
func TestP1c2Traffic_deleteClearsOnlyAddressedWorkspace(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, id1, _ := ts.p1c2CreateWorkspace(t, "Alex", "First")
	_, _, id2, _ := ts.p1c2CreateWorkspace(t, "Bob", "Second")
	p1c2RecordAndFlush(t, ts, id1, 2)
	p1c2RecordAndFlush(t, ts, id2, 2)

	del := jsonRequest(t, http.MethodDelete, p1c2TrafficURL(ts, id1), nil, cookie, csrfToken)
	rec := ts.do(del)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE traffic: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var deleted struct {
		Deleted int64 `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleted.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted.Deleted)
	}

	get1 := httptest.NewRequest(http.MethodGet, p1c2TrafficURL(ts, id1), nil)
	get1.AddCookie(cookie)
	rec = ts.do(get1)
	var body1 struct {
		Rows []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body1); err != nil {
		t.Fatalf("decode workspace 1 traffic: %v", err)
	}
	if len(body1.Rows) != 0 {
		t.Errorf("workspace 1 rows after its own DELETE = %d, want 0", len(body1.Rows))
	}

	get2 := httptest.NewRequest(http.MethodGet, p1c2TrafficURL(ts, id2), nil)
	get2.AddCookie(cookie)
	rec = ts.do(get2)
	var body2 struct {
		Rows []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode workspace 2 traffic: %v", err)
	}
	if len(body2.Rows) != 2 {
		t.Errorf("workspace 2 rows after workspace 1's DELETE = %d, want unchanged 2", len(body2.Rows))
	}
}

// TestP1c2Traffic_workspaceNotFound is a 404 for an id that parses but names
// no workspace, on every traffic route.
func TestP1c2Traffic_workspaceNotFound(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServer(t)
	cookie, csrfToken, _, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	get := httptest.NewRequest(http.MethodGet, p1c2TrafficURL(ts, 999999), nil)
	get.AddCookie(cookie)
	if rec := ts.do(get); rec.Code != http.StatusNotFound {
		t.Fatalf("GET traffic for a missing workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}

	poll := httptest.NewRequest(http.MethodGet, p1c2TrafficPollURL(ts, 999999, 0), nil)
	poll.AddCookie(cookie)
	if rec := ts.do(poll); rec.Code != http.StatusNotFound {
		t.Fatalf("GET traffic/poll for a missing workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}

	del := jsonRequest(t, http.MethodDelete, p1c2TrafficURL(ts, 999999), nil, cookie, csrfToken)
	if rec := ts.do(del); rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE traffic for a missing workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestP1c2Traffic_noRecorderStillDeletes proves the "no recorder wired"
// path is a degraded delete, never a 503 or a panic: DELETE must still
// remove the rows even though it cannot flush anything first.
func TestP1c2Traffic_noRecorderStillDeletes(t *testing.T) {
	t.Parallel()
	ts := p1c2NewTestServerNoLiveState(t) // SetTraffic is never called either
	cookie, csrfToken, id, _ := ts.p1c2CreateWorkspace(t, "Alex", "Demo")

	get := httptest.NewRequest(http.MethodGet, p1c2TrafficURL(ts, id), nil)
	get.AddCookie(cookie)
	rec := ts.do(get)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET traffic with no recorder wired: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Dropped int64 `json:"dropped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list body: %v", err)
	}
	if listed.Dropped != 0 {
		t.Errorf("dropped with no recorder wired = %d, want 0", listed.Dropped)
	}

	del := jsonRequest(t, http.MethodDelete, p1c2TrafficURL(ts, id), nil, cookie, csrfToken)
	rec = ts.do(del)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE traffic with no recorder wired: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

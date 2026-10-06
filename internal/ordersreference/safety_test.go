package ordersreference

import (
	"bytes"
	"fmt"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"uuid"
)

type harness struct {
	t     *testing.T
	s     *Service
	c     Config
	b     Build
	run   string
	epoch int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, c: Config{DBPath: filepath.Join(t.TempDir(), "fixture.db"), Token: strings.Repeat("x", 32), IsolationID: uuid.New().String(), TargetID: "orders", ConfigVersion: 1}, b: Build{Variant: "fixed", ServiceVersion: "test", SourceTreeHash: strings.Repeat("a", 64), BuildHash: strings.Repeat("b", 64)}}
	var e error
	h.s, e = Open(h.c, h.b)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { h.s.Close() })
	return h
}
func (h *harness) f() p.Fence {
	return p.Fence{RunID: h.run, StepID: uuid.New().String(), RequestKey: uuid.New().String(), Epoch: h.epoch, IdentityHash: h.s.identityHash}
}
func (h *harness) send(ep p.Endpoint, body any, status int) []byte {
	h.t.Helper()
	method, path, e := ep.Route(h.run)
	if e != nil {
		h.t.Fatal(e)
	}
	var raw []byte
	if body != nil {
		raw, e = p.Encode(body)
		if e != nil {
			h.t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+h.c.Token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.s.ServeHTTP(w, r)
	if w.Code != status {
		h.t.Fatalf("%s: got %d want %d: %s", ep, w.Code, status, w.Body.String())
	}
	return w.Body.Bytes()
}
func (h *harness) reset() p.ResetRequest {
	h.t.Helper()
	h.run = uuid.New().String()
	r := p.ResetRequest{Fence: h.f(), FixtureHash: p.FixtureHash(), Authorization: p.ResetAuthorization{ID: uuid.New().String(), Version: 1, TargetID: h.c.TargetID, ConfigVersion: 1, IdentityHash: h.s.identityHash, IsolationID: h.c.IsolationID, AllowReset: true}}
	h.send(p.ResetEndpoint, r, 200)
	h.epoch++
	return r
}
func (h *harness) arm() p.FailureRequest {
	r := p.FailureRequest{Fence: h.f(), Point: p.FailurePoint, Count: 1}
	h.send(p.FailureEndpoint, r, 200)
	return r
}
func (h *harness) order() p.OrderRequest {
	return p.OrderRequest{Fence: h.f(), BusinessKey: uuid.New().String(), Attempt: 1, Order: p.Fixture().Order}
}

func TestFencesRecoveryAndRestart(t *testing.T) {
	h := newHarness(t)
	reset := h.reset()
	arm := h.arm()
	order := h.order()
	failed := h.send(p.OrderEndpoint, order, 503)
	changed := order
	changed.BusinessKey = uuid.New().String()
	h.send(p.OrderEndpoint, changed, 409)
	changed = order
	changed.RequestKey = uuid.New().String()
	h.send(p.OrderEndpoint, changed, 409)
	changed = order
	changed.RequestKey = uuid.New().String()
	changed.StepID = uuid.New().String()
	changed.Attempt = 2
	changed.BusinessKey = uuid.New().String()
	h.send(p.OrderEndpoint, changed, 409)
	oldRun := h.run
	h.reset()
	newRun := h.run
	h.run = oldRun
	h.send(p.FailureEndpoint, arm, 200)
	h.send(p.ResetEndpoint, reset, 200)
	if !bytes.Equal(failed, h.send(p.OrderEndpoint, order, 503)) {
		t.Fatal("duplicate reply changed")
	}
	changed = order
	changed.RequestKey = uuid.New().String()
	changed.StepID = uuid.New().String()
	changed.Attempt = 2
	h.send(p.OrderEndpoint, changed, 409)
	var old p.Journal
	if e := p.Decode(h.send(p.JournalEndpoint, nil, 200), &old, p.JournalLimit); e != nil {
		t.Fatal(e)
	}
	if old.CurrentEpoch != 2 || old.Epoch != 1 || len(old.Charges) != 1 || len(old.Orders) != 0 {
		t.Fatal(old)
	}
	previousIdentity := h.s.identityHash
	if e := h.s.Close(); e != nil {
		t.Fatal(e)
	}
	var e error
	h.s, e = Open(h.c, h.b)
	if e != nil {
		t.Fatal(e)
	}
	if previousIdentity == h.s.identityHash {
		t.Fatal("instance reused")
	}
	if !bytes.Equal(failed, h.send(p.OrderEndpoint, order, 503)) {
		t.Fatal("restart lost receipt")
	}
	h.run = newRun
	h.send(p.FailureEndpoint, p.FailureRequest{Fence: h.f(), Point: p.FailurePoint, Count: 1}, 409)
	h.reset()
	h.arm()
}

func TestOuterRollbackAndConcurrentDelivery(t *testing.T) {
	h := newHarness(t)
	h.reset()
	h.arm()
	order := h.order()
	// Simulate failure at the final durable receipt write, after payment/savepoint work.
	if _, e := h.s.db.Exec("CREATE TRIGGER fail_receipt BEFORE INSERT ON requests WHEN NEW.status=503 BEGIN SELECT RAISE(ABORT,'test crash boundary'); END"); e != nil {
		t.Fatal(e)
	}
	h.send(p.OrderEndpoint, order, 500)
	for _, table := range []string{"charges", "orders"} {
		var n int
		if e := h.s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatalf("orphan %s %d %v", table, n, e)
		}
	}
	var consumed int
	if e := h.s.db.QueryRow("SELECT consumed FROM arms").Scan(&consumed); e != nil || consumed != 0 {
		t.Fatalf("trigger escaped rollback %d %v", consumed, e)
	}
	if _, e := h.s.db.Exec("DROP TRIGGER fail_receipt"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	replies := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			status, raw, e := h.s.mutate(t.Context(), p.OrderEndpoint, order.Fence, order)
			replies <- fmt.Sprintf("%d %s %v", status, raw, e)
		})
	}
	wg.Wait()
	close(replies)
	first := ""
	for r := range replies {
		if first == "" {
			first = r
		}
		if r != first || !strings.HasPrefix(r, "503 ") {
			t.Fatal(r)
		}
	}
	var j p.Journal
	if e := p.Decode(h.send(p.JournalEndpoint, nil, 200), &j, p.JournalLimit); e != nil {
		t.Fatal(e)
	}
	if len(j.Charges) != 1 || len(j.Receipts) != 3 || len(j.Events) != 5 {
		t.Fatal(j)
	}
}

func TestClosedHTTPAndDatabaseOwnership(t *testing.T) {
	h := newHarness(t)
	if other, e := Open(h.c, h.b); e == nil {
		other.Close()
		t.Fatal("second writer accepted")
	}
	for _, tc := range []struct {
		method, path, body, token string
		status                    int
	}{
		{"GET", "/__mocker_test/identity", "", "", 401},
		{"GET", "/__mocker_test/identity?x=1", "", h.c.Token, 400},
		{"GET", "/__mocker_test/identity", "{}", h.c.Token, 400},
		{"POST", "/orders", "{}", h.c.Token, 400},
		{"POST", "/orders", strings.Repeat("x", p.BodyLimit+1), h.c.Token, 400},
		{"GET", "/orders", "", h.c.Token, 404},
		{"GET", "/other", "", h.c.Token, 404},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.s.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %d", tc.path, w.Code)
		}
	}
}

func TestConsentAndEpochCAS(t *testing.T) {
	h := newHarness(t)
	h.run = uuid.New().String()
	base := p.ResetRequest{Fence: h.f(), FixtureHash: p.FixtureHash(), Authorization: p.ResetAuthorization{ID: uuid.New().String(), Version: 7, TargetID: h.c.TargetID, ConfigVersion: 1, IdentityHash: h.s.identityHash, IsolationID: h.c.IsolationID, AllowReset: true}}
	bad := base
	bad.Authorization.ConfigVersion = 2
	h.send(p.ResetEndpoint, bad, 403)
	bad = base
	bad.Authorization.AllowReset = false
	h.send(p.ResetEndpoint, bad, 400)
	bad = base
	bad.Authorization.IsolationID = uuid.New().String()
	h.send(p.ResetEndpoint, bad, 403)
	h.send(p.ResetEndpoint, base, 200)
	h.epoch = 1
	var raw []byte
	if e := h.s.db.QueryRow("SELECT authorization FROM runs WHERE id=?", h.run).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	want, _ := p.Encode(base.Authorization)
	if !bytes.Equal(raw, want) {
		t.Fatal("consent was not retained exactly")
	}
	stale := base
	stale.RunID = uuid.New().String()
	stale.StepID = uuid.New().String()
	stale.RequestKey = uuid.New().String()
	h.run = stale.RunID
	h.send(p.ResetEndpoint, stale, 409)
	// Two fresh resets with the same preceding epoch: exactly one may commit.
	one := base
	one.Fence = h.f()
	two := one
	two.RunID = uuid.New().String()
	two.RequestKey = uuid.New().String()
	two.StepID = uuid.New().String()
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, r := range []p.ResetRequest{one, two} {
		wg.Go(func() {
			status, _, e := h.s.mutate(t.Context(), p.ResetEndpoint, r.Fence, r)
			if e != nil {
				if pe, ok := e.(*protocolError); ok {
					status = pe.status
				}
			}
			results <- status
		})
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for status := range results {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
}

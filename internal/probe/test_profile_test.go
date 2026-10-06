package probe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

const replayID = "11111111-1111-4111-8111-111111111111"
const replayKey = "22222222-2222-4222-8222-222222222222"
const replayToken = "0123456789abcdef0123456789abcdef"

func replayTarget(origin string) TestTarget {
	return TestTarget{ID: "orders", Version: 1, Origin: origin, AllowedIPs: []string{"127.0.0.1"}, CredentialRef: "TOKEN", IsolationID: replayID}
}
func replayIdentity() p.Identity {
	return p.Identity{Protocol: p.Version, Service: "orders-reference", ServiceVersion: "v1", TestOnly: true, IsolationID: replayID, InstanceID: replayKey, Variant: "fixed", BuildHash: strings.Repeat("a", 64), SourceTreeHash: strings.Repeat("b", 64), SourcePolicy: p.ManifestPolicy, FixtureHash: p.FixtureHash()}
}
func replayClient(t *testing.T, h http.HandlerFunc) p.Transport {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, e := NewTestProfileClient(t.Context(), replayTarget(s.URL), func(string) string { return replayToken })
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func replayWrite(w http.ResponseWriter, status int, v any) {
	raw, _ := p.Encode(v)
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func TestTestProfileMethods(t *testing.T) {
	identity := replayIdentity()
	hash, _ := p.IdentityHash(identity)
	fence := p.Fence{RunID: replayID, StepID: replayID, RequestKey: replayKey, Epoch: 1, IdentityHash: hash}
	reset := p.ResetRequest{Fence: fence, FixtureHash: p.FixtureHash(), Authorization: p.ResetAuthorization{ID: replayKey, Version: 1, TargetID: "orders", ConfigVersion: 1, IdentityHash: hash, IsolationID: replayID, AllowReset: true}}
	arm := p.FailureRequest{Fence: fence, Point: p.FailurePoint, Count: 1}
	order := p.OrderRequest{Fence: fence, BusinessKey: replayID, Attempt: 1, Order: p.Fixture().Order}
	var calls atomic.Int32
	c := replayClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+replayToken || r.URL.RawQuery != "" {
			t.Error("invalid headers/query")
		}
		if r.Method == "GET" {
			if r.ContentLength != 0 || r.Header.Get("Content-Type") != "" {
				t.Error("GET body/headers")
			}
			switch r.URL.Path {
			case "/__mocker_test/identity":
				replayWrite(w, 200, p.IdentityResponse{Identity: identity, IdentityHash: hash, Epoch: 1})
			case "/__mocker_test/runs/" + replayID + "/journal":
				replayWrite(w, 200, p.Journal{Identity: identity, IdentityHash: hash, RunID: replayID, Epoch: 1, CurrentEpoch: 1, FixtureHash: p.FixtureHash(), Complete: true, HighWater: 1, PendingKeys: []string{}, Receipts: []p.Receipt{}, Events: []p.Event{{Sequence: 1, Kind: "reset", StepID: replayID, RequestKey: replayKey}}, Orders: []p.OrderRecord{}, Charges: []p.ChargeRecord{}})
			default:
				t.Error("unexpected GET path")
			}
			return
		}
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("invalid mutation headers")
		}
		receipt := p.Receipt{Fence: fence, HTTPStatus: 200, ResultEpoch: 1, Sequence: 1}
		switch r.URL.Path {
		case "/__mocker_test/runs/" + replayID + "/reset":
			receipt.Endpoint = p.ResetEndpoint
			receipt.Outcome = "reset"
			receipt.ResultEpoch = 2
			receipt.RequestHash, _ = p.RequestHash(p.ResetEndpoint, reset)
		case "/__mocker_test/runs/" + replayID + "/failure":
			receipt.Endpoint = p.FailureEndpoint
			receipt.Outcome = "armed"
			receipt.RequestHash, _ = p.RequestHash(p.FailureEndpoint, arm)
		case "/orders":
			receipt.Endpoint = p.OrderEndpoint
			receipt.Outcome = "persistence_failed"
			receipt.HTTPStatus = 503
			receipt.BusinessKey = replayID
			receipt.Attempt = 1
			receipt.ChargeID = replayKey
			receipt.RequestHash, _ = p.RequestHash(p.OrderEndpoint, order)
		default:
			t.Error("unexpected mutation path")
		}
		replayWrite(w, receipt.HTTPStatus, receipt)
	})
	if r, e := c.Identity(t.Context()); e != nil || r.Payload == nil || !r.Complete {
		t.Fatalf("identity: %+v %v", r, e)
	}
	if r, e := c.Reset(t.Context(), reset); e != nil || r.Payload == nil {
		t.Fatalf("reset: %+v %v", r, e)
	}
	if r, e := c.Arm(t.Context(), arm); e != nil || r.Payload == nil {
		t.Fatalf("arm: %+v %v", r, e)
	}
	if r, e := c.Order(t.Context(), order); e != nil || r.Payload == nil || r.HTTPStatus != 503 {
		t.Fatalf("order: %+v %v", r, e)
	}
	if r, e := c.Journal(t.Context(), replayID); e != nil || r.Payload == nil {
		t.Fatalf("journal: %+v %v", r, e)
	}
	if calls.Load() != 5 {
		t.Fatal("missing calls")
	}
	if _, e := c.Order(t.Context(), p.OrderRequest{}); e == nil {
		t.Fatal("invalid input accepted")
	}
	if _, e := c.Journal(t.Context(), "../identity"); e == nil {
		t.Fatal("path injection accepted")
	}
	reset.Authorization.TargetID = "other"
	if _, e := c.Reset(t.Context(), reset); e == nil {
		t.Fatal("wrong consent accepted")
	}
	if calls.Load() != 5 {
		t.Fatal("invalid request reached network")
	}
}

func TestTestProfileEvidence(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		body              string
		complete, problem bool
	}{
		{"protocol error", 401, `{"protocol":"orders-replay-v1","code":"unauthorized","message":"unauthorized"}`, true, true},
		{"wrong error status", 500, `{"protocol":"orders-replay-v1","code":"unauthorized","message":"unauthorized"}`, true, false},
		{"unknown field", 200, `{"passed":true}`, true, false},
		{"oversize", 200, strings.Repeat("x", p.BodyLimit+1), false, false},
		{"invalid JSON", 200, `{"identity":`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := replayClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); _, _ = fmt.Fprint(w, tc.body) })
			r, e := c.Identity(t.Context())
			if r.Complete != tc.complete || (r.ProtocolError != nil) != tc.problem || (e == nil) != tc.problem || r.Payload != nil || len(r.Body) > p.BodyLimit {
				t.Fatalf("response=%+v err=%v", r, e)
			}
		})
	}
}
func TestTestProfileRedirectAndIncomplete(t *testing.T) {
	var redirected atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer other.Close()
	c := replayClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) })
	if r, e := c.Identity(t.Context()); e == nil || r.HTTPStatus != 302 || redirected.Load() {
		t.Fatalf("redirect followed: %v", e)
	}
	c = replayClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	})
	if r, e := c.Identity(t.Context()); e == nil || r.Complete || string(r.Body) != "short" {
		t.Fatalf("partial evidence: %+v %v", r, e)
	}
}
func TestTestProfileSecretPinAndTimeout(t *testing.T) {
	var resolved int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+replayToken {
			t.Error("secret changed")
		}
		<-r.Context().Done()
	}))
	defer s.Close()
	target := replayTarget(s.URL)
	secret := replayToken
	c, e := NewTestProfileClient(t.Context(), target, func(string) string { resolved++; return secret })
	if e != nil {
		t.Fatal(e)
	}
	secret = "changed"
	target.AllowedIPs[0] = "10.0.0.1"
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if r, e := c.Identity(ctx); e == nil || r.Complete {
		t.Fatal("deadline accepted")
	}
	if resolved != 1 {
		t.Fatal("credential resolved repeatedly")
	}
	actual := c.(*testProfileClient)
	tr := actual.client.Transport.(*http.Transport)
	if tr.Proxy != nil || actual.client.Timeout != 3*time.Second || tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("unsafe transport")
	}
	for _, origin := range []string{"http://localhost:9191", "http://10.0.0.1", "https://8.8.8.8", "http://127.0.0.1/path", "http://127.0.0.1?", "http://127.0.0.1#", "http://user@127.0.0.1"} {
		target := replayTarget(origin)
		if _, e := NewTestProfileClient(t.Context(), target, func(string) string { return replayToken }); e == nil {
			t.Errorf("unsafe origin accepted: %s", origin)
		}
	}
	if _, e := NewTestProfileClient(t.Context(), replayTarget(s.URL), func(string) string { return "short" }); e == nil {
		t.Fatal("weak token accepted")
	}
}

func TestTestProfileRejectsWrongReceipt(t *testing.T) {
	identity := replayIdentity()
	hash, _ := p.IdentityHash(identity)
	input := p.FailureRequest{Fence: p.Fence{RunID: replayID, StepID: replayID, RequestKey: replayKey, Epoch: 1, IdentityHash: hash}, Point: p.FailurePoint, Count: 1}
	requestHash, _ := p.RequestHash(p.FailureEndpoint, input)
	for _, field := range []string{"requestHash", "fence", "status", "endpoint"} {
		t.Run(field, func(t *testing.T) {
			receipt := p.Receipt{Fence: input.Fence, Endpoint: p.FailureEndpoint, RequestHash: requestHash, HTTPStatus: 200, Outcome: "armed", ResultEpoch: 1, Sequence: 1}
			switch field {
			case "requestHash":
				receipt.RequestHash = strings.Repeat("c", 64)
			case "fence":
				receipt.RequestKey = replayID
			case "status":
				receipt.HTTPStatus = 201
			case "endpoint":
				receipt.Endpoint = p.ResetEndpoint
				receipt.Outcome = "reset"
				receipt.ResultEpoch = 2
			}
			c := replayClient(t, func(w http.ResponseWriter, r *http.Request) { replayWrite(w, 200, receipt) })
			r, err := c.Arm(t.Context(), input)
			if err == nil || r.Payload != nil || !r.Complete || len(r.Body) == 0 {
				t.Fatalf("mismatched receipt accepted: %+v %v", r, err)
			}
		})
	}
}

func TestTestProfileJournalLimitAndConfigPin(t *testing.T) {
	c := replayClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", p.JournalLimit+1))
	})
	if r, e := c.Journal(t.Context(), replayID); e == nil || r.Complete || len(r.Body) != p.JournalLimit {
		t.Fatalf("journal cap: len=%d complete=%v err=%v", len(r.Body), r.Complete, e)
	}
	target := replayTarget("http://127.0.0.1:9191")
	first, e := NewTestProfileClient(t.Context(), target, func(string) string { return replayToken })
	if e != nil {
		t.Fatal(e)
	}
	again, e := NewTestProfileClient(t.Context(), target, func(string) string { return replayToken })
	if e != nil {
		t.Fatal(e)
	}
	changed, e := NewTestProfileClient(t.Context(), target, func(string) string { return replayToken + "rotated" })
	if e != nil {
		t.Fatal(e)
	}
	pin := func(c p.Transport) string { return c.(interface{ ConfigHash() string }).ConfigHash() }
	if !p.ValidHash(pin(first)) || pin(first) != pin(again) || pin(first) == pin(changed) {
		t.Fatal("configuration fingerprint failed")
	}
}

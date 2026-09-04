package customep_test

// stream_test.go covers P6b's write-time rules (decisions.md
// mocker-p6b-sse-mock D3, D5, D6): every limit is refused BY NAME through
// the one validator both writers reach (ValidateDraft is the same function
// Create/UpdateExpecting run), never clamped; an sse row is strict about the
// ordinary fields; and a stream row round-trips through the column with its
// document intact.

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

func sseRow(stream *customep.Stream) *customep.Row {
	return &customep.Row{Method: http.MethodGet, Path: "/events", Kind: customep.KindSSE, Stream: stream}
}

func timelineOf(frames ...customep.Frame) *customep.Stream {
	return &customep.Stream{Timeline: &customep.Timeline{Frames: frames}}
}

func TestValidateDraft_refusesEveryLimitByName(t *testing.T) {
	big := strings.Repeat("x", 600)
	one := jsonx.RawMessage(`1`)
	whenA := []overrides.Condition{{In: "body", Name: "a", Op: "exists"}}
	wsRow := func(stream *customep.Stream) *customep.Row {
		return &customep.Row{Method: http.MethodGet, Path: "/e", Kind: customep.KindWS, Stream: stream}
	}
	manyRules := func(n int) []customep.Rule {
		out := make([]customep.Rule, n)
		for i := range out {
			out[i] = customep.Rule{When: whenA, Data: one}
		}
		return out
	}
	withResponses := sseRow(timelineOf(customep.Frame{Data: one}))
	withResponses.Responses = map[string]overrides.Variant{"200": {Mode: "pinned", Body: jsonx.RawMessage(`{}`)}}
	withStatus := sseRow(timelineOf(customep.Frame{Data: one}))
	withStatus.ActiveStatus = 503

	cases := []struct {
		name string
		row  *customep.Row
		want string
	}{
		{"neither behaviour", sseRow(&customep.Stream{}), "needs a timeline or a tick"},
		{"empty timeline", sseRow(&customep.Stream{Timeline: &customep.Timeline{}}), "at least one frame"},
		{"too many frames", sseRow(timelineOf(make([]customep.Frame, customep.MaxTimelineFrames+1)...)), "the cap is 500"},
		{"negative delay", sseRow(timelineOf(customep.Frame{DelayMs: -1, Data: one})), "not in [0,30000]"},
		{"delay over the ceiling", sseRow(timelineOf(customep.Frame{DelayMs: customep.MaxFrameDelayMs + 1, Data: one})), "not in [0,30000]"},
		{"event with a line break", sseRow(timelineOf(customep.Frame{Event: "a\nb", Data: one})), "line break"},
		{"event too long", sseRow(timelineOf(customep.Frame{Event: strings.Repeat("e", 65), Data: one})), "the cap is 64"},
		{"frame without data", sseRow(timelineOf(customep.Frame{})), "data is required"},
		{"frame with invalid JSON", sseRow(timelineOf(customep.Frame{Data: jsonx.RawMessage(`{`)})), "not valid JSON"},
		{"frame over the byte cap", sseRow(timelineOf(customep.Frame{Data: jsonx.RawMessage(`"` + big + `"`)})), "frame cap is 512"},
		{"tick below the floor", sseRow(&customep.Stream{Tick: &customep.Tick{IntervalMs: 99, Schema: jsonx.RawMessage(`{"type":"object"}`)}}), "below the floor of 100"},
		{"tick without schema", sseRow(&customep.Stream{Tick: &customep.Tick{IntervalMs: 100}}), "schema is required"},
		{"tick schema not an object", sseRow(&customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Schema: jsonx.RawMessage(`[]`)}}), "JSON Schema object"},
		{"tick schema null", sseRow(&customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Schema: jsonx.RawMessage(`null`)}}), "got null"},
		{"tick schema with a $ref", sseRow(&customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Schema: jsonx.RawMessage(`{"properties":{"a":{"$ref":"#/x"}}}`)}}), "$ref"},
		{"sse with POST", &customep.Row{Method: http.MethodPost, Path: "/e", Kind: customep.KindSSE, Stream: timelineOf(customep.Frame{Data: one})}, "requires method GET"},
		{"sse with responses", withResponses, "takes no responses"},
		{"sse with activeStatus 503", withStatus, "requires activeStatus 200"},
		{"sse without a document", &customep.Row{Method: http.MethodGet, Path: "/e", Kind: customep.KindSSE}, "stream is required"},
		{"http with a document", &customep.Row{Method: http.MethodGet, Path: "/e", Kind: customep.KindHTTP, Stream: timelineOf(customep.Frame{Data: one})}, `only allowed with kind "sse"`},
		// P6d (decisions.md mocker-p6d-websocket D2, D3): kind ws is served;
		// its own refusals, by name, on both writers.
		// A18 D10.2 added a FIFTH inbound producer, so the sentence this
		// case matches on grew: "a reactive rule, echo or onFrame". The
		// substring moved with it rather than being loosened to something
		// shorter — the point of the case is that the refusal ENUMERATES
		// what a ws stream may carry, and a shorter needle would keep
		// passing once the enumeration went stale again.
		{"ws with nothing", &customep.Row{Method: http.MethodGet, Path: "/e", Kind: customep.KindWS, Stream: &customep.Stream{}}, "a reactive rule, echo or onFrame"},
		{"ws with POST", &customep.Row{Method: http.MethodPost, Path: "/e", Kind: customep.KindWS, Stream: &customep.Stream{Echo: true}}, "requires method GET"},
		{"reactive on sse", sseRow(&customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Schema: jsonx.RawMessage(`{"type":"object"}`)}, Reactive: []customep.Rule{{When: whenA, Data: one}}}), "reactive has no meaning on kind"},
		{"echo on sse", sseRow(&customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Schema: jsonx.RawMessage(`{"type":"object"}`)}, Echo: true}), "echo has no meaning on kind"},
		{"rule without when", wsRow(&customep.Stream{Reactive: []customep.Rule{{Data: one}}}), "at least one condition"},
		{"rule with neither data nor close", wsRow(&customep.Stream{Reactive: []customep.Rule{{When: whenA}}}), "needs data, close or both"},
		{"rule data invalid JSON", wsRow(&customep.Stream{Reactive: []customep.Rule{{When: whenA, Data: jsonx.RawMessage(`{`)}}}), "not valid JSON"},
		{"rule data over the byte cap", wsRow(&customep.Stream{Reactive: []customep.Rule{{When: whenA, Data: jsonx.RawMessage(`"` + big + `"`)}}}), "frame cap is 512"},
		{"close code reserved", wsRow(&customep.Stream{Reactive: []customep.Rule{{When: whenA, Close: &customep.RuleClose{Code: 1005}}}}), "4000..4999"},
		{"close code 3000 range", wsRow(&customep.Stream{Reactive: []customep.Rule{{When: whenA, Close: &customep.RuleClose{Code: 3500}}}}), "4000..4999"},
		{"close reason too long", wsRow(&customep.Stream{Reactive: []customep.Rule{{When: whenA, Close: &customep.RuleClose{Code: 4000, Reason: strings.Repeat("r", 124)}}}}), "the cap is 123"},
		{"too many rules", wsRow(&customep.Stream{Reactive: manyRules(customep.MaxReactiveRules + 1)}), "the cap is 100"},
		{"unknown kind", &customep.Row{Method: http.MethodGet, Path: "/e", Kind: "grpc"}, "unknown kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := customep.ValidateDraft(tc.row, 512)
			if err == nil {
				t.Fatalf("validate: nil, want a refusal naming %q", tc.want)
			}
			if !errors.Is(err, customep.ErrInvalidRow) {
				t.Fatalf("validate: %v does not wrap ErrInvalidRow", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate: %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestValidateDraft_acceptsAndDefaults(t *testing.T) {
	r := &customep.Row{Method: "get", Path: "/events", Kind: customep.KindSSE, Stream: &customep.Stream{
		Timeline: &customep.Timeline{Frames: []customep.Frame{{DelayMs: 0, Event: "hello", Data: jsonx.RawMessage(`{"a": 1}`)}, {DelayMs: 30000, Data: jsonx.RawMessage(`null`)}}, Loop: true},
		Tick:     &customep.Tick{IntervalMs: 100, Event: "price", Schema: jsonx.RawMessage(`{"type":"object","properties":{"p":{"type":"number"}}}`)},
	}}
	if err := customep.ValidateDraft(r, 0); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if r.Method != http.MethodGet || r.ActiveStatus != 200 || r.Responses == nil {
		t.Fatalf("normalised row = %+v, want GET, 200, an empty responses map", r)
	}
	if !r.Stream.ClosesWhenDone() {
		t.Fatal("closeWhenDone nil must read as true")
	}

	// An http row built by a caller that never heard of kinds.
	h := &customep.Row{Method: http.MethodGet, Path: "/plain"}
	if err := customep.ValidateDraft(h, 0); err != nil {
		t.Fatalf("validate http: %v", err)
	}
	if h.Kind != customep.KindHTTP {
		t.Fatalf("kind = %q, want %q by default", h.Kind, customep.KindHTTP)
	}
}

// TestRepo_streamRowRoundTrips: an sse row is stored with its document and
// comes back through every read path with the document intact, an http row
// comes back with kind "http" and no document, an edit that drops the kind
// is refused rather than silently downgrading, and the CHECK constraint
// refuses a stream on an http row at the SQL level too.
func TestRepo_streamRowRoundTrips(t *testing.T) {
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "stream-ws")
	repo := customep.NewRepo(db)

	row := sseRow(&customep.Stream{
		Timeline: &customep.Timeline{Frames: []customep.Frame{{DelayMs: 10, Event: "e", Data: jsonx.RawMessage(`{"k":"v"}`)}}},
		Tick:     &customep.Tick{IntervalMs: 250, Schema: jsonx.RawMessage(`{"type":"object"}`)},
	})
	stored, err := repo.Create(t.Context(), wsID, row)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if stored.Kind != customep.KindSSE || stored.Stream == nil || stored.Stream.Tick == nil || stored.Stream.Tick.IntervalMs != 250 {
		t.Fatalf("stored = %+v, want an sse row with its tick", stored)
	}
	got, err := repo.Get(t.Context(), wsID, stored.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Stream == nil || len(got.Stream.Timeline.Frames) != 1 || got.Stream.Timeline.Frames[0].Event != "e" {
		t.Fatalf("get = %+v, want the timeline back", got.Stream)
	}
	all, err := repo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 || all[0].Kind != customep.KindSSE {
		t.Fatalf("list = %+v, want the one sse row", all)
	}

	editVersion := stored.EditVersion
	_, err = repo.UpdateExpecting(t.Context(), wsID, stored.ID, &editVersion, func(cur *customep.Row) error {
		cur.Kind = ""
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), `only allowed with kind "sse"`) {
		t.Fatalf("update dropping the kind: %v, want a refusal", err)
	}

	plain, err := repo.Create(t.Context(), wsID, &customep.Row{Method: http.MethodGet, Path: "/plain"})
	if err != nil {
		t.Fatalf("create http: %v", err)
	}
	if plain.Kind != customep.KindHTTP || plain.Stream != nil {
		t.Fatalf("http row = kind %q stream %v, want http and nil", plain.Kind, plain.Stream)
	}

	if _, err := db.W.ExecContext(t.Context(), `UPDATE custom_endpoints SET stream = '{}' WHERE id = ?`, plain.ID); err == nil {
		t.Fatal("an http row accepted a stream document at the SQL level — the CHECK of 0005 is missing")
	}
}

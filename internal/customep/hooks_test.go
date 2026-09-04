package customep_test

// hooks_test.go is A18's acceptance for the two stream hooks' WRITE side —
// §A clause 39 of docs/A18-endpoint-functions.md, which is four refusals and
// one acceptance.
//
// The acceptance is not a courtesy case. Two of the four refusals below could
// not fire at all on the pre-A18 validator (D8b(2), D8b(3)), and the fix for
// that is an ORDER; an order is exactly the kind of change that is easy to
// get right in the refusing direction and wrong in the admitting one, which
// is what a Lua-only tick being ACCEPTED observes.

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonx"
)

func hookRow(kind string, s *customep.Stream) *customep.Row {
	return &customep.Row{Method: http.MethodGet, Path: "/events", Kind: kind, ActiveStatus: 200, Stream: s}
}

// TestValidateDraft_hookRefusalsByName is clause 39's four, each matched on
// the words the refusal must carry — never merely on "an error came back",
// which passes when a document is refused for an unrelated reason. The
// `stream.tick.schema is required` needle on the first case is the one that
// would have caught D8b(2): before the reorder, a lua+schema tick was refused
// as schema-missing, which is an error with the wrong words.
func TestValidateDraft_hookRefusalsByName(t *testing.T) {
	schema := jsonx.RawMessage(`{"type":"object"}`)
	for _, tc := range []struct {
		name   string
		row    *customep.Row
		want   string
		unwant string
	}{
		{
			name: "tick lua and schema",
			row: hookRow(customep.KindSSE, &customep.Stream{
				Tick: &customep.Tick{IntervalMs: 100, Lua: "return {}", Schema: schema},
			}),
			want:   "takes lua or schema, not both",
			unwant: "stream.tick.schema is required",
		},
		{
			name: "onFrame and reactive",
			row: hookRow(customep.KindWS, &customep.Stream{
				OnFrame:  "return nil",
				Reactive: []customep.Rule{{When: nil, Data: jsonx.RawMessage(`{}`)}},
			}),
			want: "takes onFrame or reactive, not both",
		},
		{
			name: "onFrame and echo",
			row: hookRow(customep.KindWS, &customep.Stream{
				OnFrame: "return nil", Echo: true,
			}),
			want: "takes onFrame or echo, not both",
		},
		{
			name: "onFrame on sse",
			row: hookRow(customep.KindSSE, &customep.Stream{
				OnFrame: "return nil",
				Tick:    &customep.Tick{IntervalMs: 100, Schema: schema},
			}),
			want: "stream.onFrame has no meaning on kind",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := customep.ValidateDraft(tc.row, 0)
			if !errors.Is(err, customep.ErrInvalidRow) {
				t.Fatalf("err = %v, want ErrInvalidRow", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want it to name %q", err, tc.want)
			}
			if tc.unwant != "" && strings.Contains(err.Error(), tc.unwant) {
				t.Fatalf("err = %q — it answered %q, which is the check that used to run FIRST and hide this one (D8b(2))",
					err, tc.unwant)
			}
		})
	}
}

// TestValidateDraft_aLuaOnlyTickIsAccepted is clause 39's fifth observation
// and the one that fails against an over-wide fix: `stream.tick.schema is
// required` was unconditional, so a tick with lua and no schema was refused
// by a rule that had nothing to do with it.
func TestValidateDraft_aLuaOnlyTickIsAccepted(t *testing.T) {
	row := hookRow(customep.KindSSE, &customep.Stream{
		Tick: &customep.Tick{IntervalMs: 100, Lua: `return {n = ordinal}`},
	})
	if err := customep.ValidateDraft(row, 0); err != nil {
		t.Fatalf("a Lua-only tick was refused: %v", err)
	}
}

// TestValidateDraft_anOnFrameOnlyWSIsAccepted is the same shape one field
// over: onFrame alone is a legal ws stream, because it is a behaviour, and
// the "needs a timeline, a tick, a reactive rule or echo" refusal had to
// learn about it or a hook-only endpoint would be refused as empty.
func TestValidateDraft_anOnFrameOnlyWSIsAccepted(t *testing.T) {
	row := hookRow(customep.KindWS, &customep.Stream{OnFrame: `return "reply", {ok = true}`})
	if err := customep.ValidateDraft(row, 0); err != nil {
		t.Fatalf("a ws row whose only behaviour is onFrame was refused: %v", err)
	}
}

// TestValidateDraft_hookLuaIsCompiledAtWriteTime is D8's promise applied to
// both hooks: this plane always answers, so a deferred parse is a 500 nobody
// asked for. Each case asserts the PARSER's own words reach the message.
func TestValidateDraft_hookLuaIsCompiledAtWriteTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  *customep.Row
		want string
	}{
		{"tick.lua", hookRow(customep.KindSSE, &customep.Stream{
			Tick: &customep.Tick{IntervalMs: 100, Lua: "return }"},
		}), "stream.tick.lua does not compile"},
		{"onFrame", hookRow(customep.KindWS, &customep.Stream{OnFrame: "return }"}), "stream.onFrame does not compile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := customep.ValidateDraft(tc.row, 0)
			if err == nil {
				t.Fatal("unparseable Lua was stored")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want it to name %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "near") {
				t.Fatalf("err = %q, want the parser's own words (a `near '<token>'` is what an author navigates by)", err)
			}
		})
	}
}

package mockplane

// hooks_internal_test.go is the WHITE-BOX half of A18's stream-hook
// acceptance — one clause, and it is here rather than in hooks_test.go for
// one reason: it shortens `previewLuaBudget`, a package var, and this tree's
// precedent for that is a white-box test writing the var directly
// (internal/stream/conn_test.go does exactly this with maxStreamLifetime),
// never a ForTest hook exported from production code. ws_internal_test.go
// keeps the same split for the same reason.

import (
	"context"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// TestPreviewStream_luaTickPastTheBudgetIsLabelledNotRun is A18 clause 45's
// first half. Its named defeat is "the preview can run 50 × 2 s", which is a
// claim about the SUM, so the aggregate budget is what this shortens — not
// luafn.Timeout, which would observe the per-call ceiling instead and leave
// the multiplication the clause is about untested.
//
// Frames past the budget keep their PLACE and are labelled: a shorter list
// would read as "the stream ends here", which is a different and wrong
// statement about the definition.
func TestPreviewStream_luaTickPastTheBudgetIsLabelledNotRun(t *testing.T) {
	original := previewLuaBudget
	previewLuaBudget = 120 * time.Millisecond
	t.Cleanup(func() { previewLuaBudget = original })

	p := respondTestPlane()
	ws := &workspaces.Workspace{ID: 1, Slug: "alex", Revision: 1, Settings: domain.DefaultSettings()}
	// A hook that never returns: the first firing eats the whole aggregate
	// budget and every one after it meets an already-expired context.
	draft := &customep.Row{
		ID: 0, WorkspaceID: 1, Method: "GET", Path: "/events",
		CanonicalPath: router.CanonicalPath("/events"), OverrideOn: true,
		ActiveStatus: 200, Kind: customep.KindSSE,
		Stream: &customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Lua: `while true do end`}},
	}

	pv, err := p.PreviewStream(context.Background(), ws, draft)
	if err != nil {
		t.Fatalf("PreviewStream: %v — a hook that times out is a LABEL on the frame, never an error on the route", err)
	}
	if len(pv.Frames) == 0 {
		t.Fatal("no frames: a frame past the budget keeps its place on the axis")
	}
	for i, fr := range pv.Frames {
		if !fr.NotRun {
			t.Fatalf("frame %d ran; the aggregate budget was 120 ms and one firing never returns", i)
		}
		if len(fr.Data) != 0 {
			t.Fatalf("frame %d carries a body (%s) and is labelled NotRun", i, fr.Data)
		}
		if fr.AtMs != (i+1)*100 {
			t.Fatalf("frame %d is at %d ms, want %d — a labelled frame keeps its PLACE", i, fr.AtMs, (i+1)*100)
		}
	}
	if !pv.NominalRate {
		t.Error("NominalRate = false; a tick.lua draft's rate is a sample even when nothing ran")
	}
}

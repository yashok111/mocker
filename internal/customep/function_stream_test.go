package customep_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// TestValidateDraft_functionOnAStreamIsRefusedByItsOwnName is acceptance
// clause 18 and D8b(1). The ORDER is what it observes, not merely the
// refusal: on the unchanged code a stream row carrying a function met the
// generic "takes no responses" first and D5's own refusal could never fire,
// so a clause asserting the named refusal would have gone red against an
// implementation nobody could call wrong.
func TestValidateDraft_functionOnAStreamIsRefusedByItsOwnName(t *testing.T) {
	for _, kind := range []string{customep.KindSSE, customep.KindWS} {
		t.Run(kind, func(t *testing.T) {
			row := &customep.Row{
				Method:       http.MethodGet,
				Path:         "/events",
				Kind:         kind,
				ActiveStatus: 200,
				Responses: map[string]overrides.Variant{
					"200": {Function: `return 200, {ok = true}`},
				},
				Stream: &customep.Stream{
					Timeline: &customep.Timeline{
						Frames: []customep.Frame{{DelayMs: 10, Data: jsonx.RawMessage(`{"a":1}`)}},
					},
				},
			}
			err := customep.ValidateDraft(row, 0)
			if !errors.Is(err, customep.ErrInvalidRow) {
				t.Fatalf("err = %v, want ErrInvalidRow", err)
			}
			if !strings.Contains(err.Error(), "takes no function") {
				t.Fatalf("err = %q, want the refusal to name the FUNCTION.\n"+
					"A message about the response map is the pre-A18 answer: the generic "+
					"len(Responses) != 0 check ran first and the function was never looked at.", err)
			}
		})
	}
}

// TestValidateDraft_anHTTPRowStillTakesAFunction is the other direction: a
// refusal written one branch too wide would take the feature out of the very
// rows it is for.
func TestValidateDraft_anHTTPRowStillTakesAFunction(t *testing.T) {
	row := &customep.Row{
		Method:       http.MethodPost,
		Path:         "/sign-in",
		Kind:         customep.KindHTTP,
		ActiveStatus: 200,
		Responses: map[string]overrides.Variant{
			"200": {Function: `return 200, {token = "t"}`},
			// A pinned sibling on ANOTHER status is legal and is the shape
			// D1's own sign-in example describes (D5).
			"401": {Mode: "pinned", Body: []byte(`{"error":"bad credentials"}`)},
		},
	}
	if err := customep.ValidateDraft(row, 0); err != nil {
		t.Fatalf("an http row with a function-200 and a pinned-401 was refused: %v", err)
	}
}

// TestRepo_aStoredLuaTickSurvivesReloadAndUpdate is review finding 3. Before
// the fix a Lua-only tick was stored as `"schema":null`, read back as a
// four-byte RawMessage, and every re-validation of the STORED row — Update
// here, and the same path under a checkpoint rollback, a scenario apply and
// a bundle import — refused it as "lua or schema, not both". The update
// touches nothing about the tick on purpose: the row must pass its own
// validation unchanged.
func TestRepo_aStoredLuaTickSurvivesReloadAndUpdate(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := customep.NewRepo(db)
	wsID := insertWorkspace(t, db, "alex")

	created, err := repo.Create(t.Context(), wsID, &customep.Row{
		Method: http.MethodGet, Path: "/ticks", Kind: customep.KindSSE, ActiveStatus: 200,
		Stream: &customep.Stream{Tick: &customep.Tick{IntervalMs: 100, Lua: `return {n = ordinal}`}},
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	got, err := repo.Get(t.Context(), wsID, created.ID)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if len(got.Stream.Tick.Schema) != 0 {
		t.Errorf("Schema read back as %q, want nothing: a Lua tick has no schema", got.Stream.Tick.Schema)
	}
	if _, err := repo.Update(t.Context(), wsID, created.ID, func(cur *customep.Row) error {
		cur.OverrideOn = false
		return nil
	}); err != nil {
		t.Fatalf("Update() of an untouched Lua tick: %v", err)
	}

	// The pre-fix bytes, as a checkpoint or an older DB still holds them:
	// a literal null beside lua must read as absent.
	var legacy customep.Stream
	if err := jsonx.Unmarshal([]byte(`{"tick":{"intervalMs":100,"schema":null,"lua":"return {n = ordinal}"}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if err := customep.ValidateDraft(&customep.Row{
		Method: http.MethodGet, Path: "/legacy", Kind: customep.KindSSE, ActiveStatus: 200, Stream: &legacy,
	}, 0); err != nil {
		t.Fatalf("a stored `\"schema\":null` beside lua was refused: %v", err)
	}
}

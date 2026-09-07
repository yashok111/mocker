package mcp

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestConfirmWorkspaceSlug pins the rule the six D6 tools share via this
// function, in one place, because they are built in four different files by
// four different agents: the argument is checked against the slug the ADMIN
// PLANE reports for that workspace right now, and a mismatch is a REFUSAL —
// an error, with the destructive call never issued. Two more tools —
// decide_resource's decline branch and reset_resource_data (D7 of
// mocker-p3b-resources) — carry the same argument and enforce the identical
// rule without calling this function at all; their own tests
// (mcp_resources_test.go) observe the rule through the admin route each
// wraps, not through this one.
func TestConfirmWorkspaceSlug(t *testing.T) {
	t.Parallel()

	const okBody = `{"id":7,"slug":"orders-api","name":"Orders"}`

	tests := []struct {
		name      string
		confirm   string
		responses []cannedResponse
		wantCalls int
		wantErr   bool
	}{
		{
			name:      "matching slug proceeds",
			confirm:   "orders-api",
			responses: []cannedResponse{{http.StatusOK, okBody}},
			wantCalls: 1,
		},
		{
			name:      "surrounding whitespace is not a mismatch",
			confirm:   "  orders-api\n",
			responses: []cannedResponse{{http.StatusOK, okBody}},
			wantCalls: 1,
		},
		{
			name:      "a different workspace's slug is refused",
			confirm:   "orders-api-2",
			responses: []cannedResponse{{http.StatusOK, okBody}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			name:      "case differences are a mismatch, not a near miss",
			confirm:   "Orders-API",
			responses: []cannedResponse{{http.StatusOK, okBody}},
			wantCalls: 1,
			wantErr:   true,
		},
		{
			// Refused before the read: an empty argument cannot have been
			// produced by having looked the workspace up, which is the
			// whole property D6 buys.
			name:      "empty argument is refused without calling the admin plane",
			confirm:   "   ",
			responses: nil,
			wantCalls: 0,
			wantErr:   true,
		},
		{
			name:      "a workspace that does not exist refuses",
			confirm:   "orders-api",
			responses: []cannedResponse{{http.StatusNotFound, `{"error":{"code":"not_found","message":"workspace not found"}}`}},
			wantCalls: 1,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			caller := &scriptedCaller{t: t, responses: tt.responses}
			err := confirmWorkspaceSlug(opsTestCtx(), newLoopback(caller), "delete_workspace", 7, tt.confirm)

			if (err != nil) != tt.wantErr {
				t.Fatalf("confirmWorkspaceSlug() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(caller.calls) != tt.wantCalls {
				t.Fatalf("made %d admin call(s), want %d: %+v", len(caller.calls), tt.wantCalls, caller.calls)
			}
			if tt.wantCalls == 1 {
				if got := caller.calls[0]; got.method != http.MethodGet || got.path != "/api/workspaces/7" {
					t.Errorf("read %s %s, want GET /api/workspaces/7", got.method, got.path)
				}
			}
			// The refusal must not hand back the answer: echoing the real
			// slug would turn one wrong guess into a two-step oracle and
			// cost exactly the property this guard is bought for.
			if err != nil && strings.Contains(err.Error(), "orders-api\"") && tt.confirm != "orders-api" {
				t.Errorf("refusal echoed the expected slug: %v", err)
			}
		})
	}
}

// TestConfirmSlugDocIsShared keeps the eight tools' schema description a
// single string rather than eight paraphrases — the failure mode
// internal/httpx.BrowserExecutableMediaType exists to record.
func TestConfirmSlugDocIsShared(t *testing.T) {
	t.Parallel()

	if confirmSlugField != "confirmSlug" {
		t.Errorf("confirmSlugField = %q; the eight tools requiring confirmSlug declare this exact JSON field name", confirmSlugField)
	}
	if !strings.Contains(confirmSlugDoc, "mismatch") {
		t.Errorf("confirmSlugDoc must tell the model a mismatch refuses the call; got %q", confirmSlugDoc)
	}
}

// TestConfirmSlugTagsMatchConfirmSlugDoc is the check the eight "copied
// VERBATIM" comments asserted and nothing enforced until 2026-09-07.
//
// [confirmSlugField]'s own doc comment explains why the copies exist at all:
// a Go struct tag cannot reference a constant, and the SDK infers each
// tool's schema from its input type, so embedding a shared struct would put
// one more flattening step between a security argument and its existing at
// all. That trade is fine — what was not fine is that the copies were held
// in step by eight comments saying "verbatim" and by nobody rereading them.
// A tag is a string literal in a place no compiler, linter or existing test
// looked at, so a paraphrase introduced while editing a neighbouring field
// would ship: the model would then be told a slightly different rule by
// different tools for the same argument, which is exactly the failure
// confirmSlugDoc exists to prevent.
//
// reflect is the only reader that can see a tag, so the test uses it
// directly rather than going through the SDK's schema inference: the
// question here is what the SOURCE says, not what a schema generator makes
// of it.
func TestConfirmSlugTagsMatchConfirmSlugDoc(t *testing.T) {
	t.Parallel()

	// Every input struct in this package that declares confirmSlug. Listed
	// by value rather than discovered, deliberately: a discovery walk over
	// the package's exported types would silently pass on the day a ninth
	// tool forgets the argument entirely, which is the other half of what
	// this rule is for; the count at the bottom of this test is that half.
	inputs := []any{
		DeleteScenarioInput{},    // tools_endpoints.go
		DeleteCheckpointInput{},  // tools_history.go
		RollbackWorkspaceInput{}, // tools_history.go — the one documented extension
		ResetOverridesInput{},    // tools_history.go
		DeleteWorkspaceInput{},   // tools_history.go
		ClearTrafficInput{},      // tools_history.go
		DeleteAssetInput{},       // tools_assets.go
		DecideResourceInput{},    // tools_resources.go
		ResetResourceDataInput{}, // tools_resources.go
	}

	// rollback_workspace is the ONE tool whose description says more than
	// the shared rule, and it says it on the record: RollbackWorkspaceInput's
	// own doc comment argues that restoreData:true changes what a mismatch
	// COSTS, not whether the argument is required. It is held to
	// confirmSlugDocLead — the same opening sentence — rather than exempted,
	// so a paraphrase of the shared half is still caught.
	const extended = "RollbackWorkspaceInput"

	found := 0
	for _, in := range inputs {
		typ := reflect.TypeOf(in)
		field, ok := typ.FieldByName("ConfirmSlug")
		if !ok {
			t.Errorf("%s has no ConfirmSlug field", typ.Name())
			continue
		}
		if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); name != confirmSlugField {
			t.Errorf("%s.ConfirmSlug is %q on the wire, want %q", typ.Name(), name, confirmSlugField)
		}
		found++

		got := field.Tag.Get("jsonschema")
		if typ.Name() == extended {
			if !strings.HasPrefix(got, confirmSlugDocLead) {
				t.Errorf("%s.ConfirmSlug's description does not open with confirmSlugDocLead:\n got %q\nwant prefix %q",
					typ.Name(), got, confirmSlugDocLead)
			}
			if got == confirmSlugDoc {
				t.Errorf("%s.ConfirmSlug now equals confirmSlugDoc; the restoreData warning its own doc comment argues for is gone", typ.Name())
			}
			continue
		}
		if got != confirmSlugDoc {
			t.Errorf("%s.ConfirmSlug's jsonschema description has drifted from confirmSlugDoc:\n got %q\nwant %q",
				typ.Name(), got, confirmSlugDoc)
		}
	}

	// The population itself: D6's six plus D7's two plus A6's delete_asset.
	// A ninth tool that needs confirmSlug and is not listed above is a tool
	// whose description nothing checks — the exact hole this test closes.
	if found != 9 {
		t.Errorf("checked %d confirmSlug arguments, want 9 (D6's six, D7's two, A6's delete_asset)", found)
	}
}

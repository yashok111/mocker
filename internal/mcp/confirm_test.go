package mcp

import (
	"net/http"
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

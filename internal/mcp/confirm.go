package mcp

import (
	"context"
	"fmt"
	"strings"
)

// confirmSlugDoc is the jsonschema description EVERY one of the eight tools
// that require confirmSlug — D6 of the mocker-a-mcp gate document's
// original six, plus D7 of mocker-p3b-resources' two more (decide_resource's
// decline branch, reset_resource_data) — must give its confirmSlug
// argument, verbatim, so the eight read as one rule rather than eight
// paraphrases of it. This repository already records what happens when a
// single rule gets two hand copies: internal/httpx.BrowserExecutableMediaType
// exists because the two copies that preceded it admitted the same bad
// value.
const confirmSlugDoc = "The exact slug of the workspace this call is aimed at, as list_workspaces " +
	"or get_workspace reports it. It is checked against the live workspace before anything is " +
	"destroyed; a mismatch refuses the call and changes nothing."

// confirmSlugField is the argument's name on the wire. The eight tools
// declare it as `json:"confirmSlug" jsonschema:"..."` on their own input
// struct — declared eight times rather than embedded, because the SDK
// infers each tool's schema from its input type and an embedded struct is
// one more thing that has to flatten correctly for a security argument to
// exist at all.
const confirmSlugField = "confirmSlug"

// confirmWorkspaceWire decodes just the one field confirmWorkspaceSlug
// compares against, out of workspaceView
// (internal/admin/workspace_handlers.go:42).
type confirmWorkspaceWire struct {
	Slug string `json:"slug"`
}

// confirmWorkspaceSlug is the guard six of the eight destructive tools that
// require confirmSlug — delete_workspace, clear_traffic, delete_checkpoint,
// delete_scenario, rollback_workspace and reset_overrides, D6 of the
// mocker-a-mcp gate document — call FIRST, before issuing their own
// request, returning its error unchanged if it is non-nil. The other two,
// decide_resource's decline branch and reset_resource_data (D7 of
// mocker-p3b-resources), carry the identical argument and the identical
// confirmSlugDoc wording but do NOT call this function: the admin route
// each of them wraps already performs the live-slug comparison itself,
// inside the same transaction that would act on it (tools_resources.go's
// own package doc comment).
//
// The rule, stated once so four different agents in four different files
// cannot each interpret it differently:
//
//   - the argument is called confirmSlug (confirmSlugField), described with
//     confirmSlugDoc, and carries the workspace's slug;
//   - it is compared, exactly and case-sensitively, against the slug the
//     ADMIN PLANE reports for workspaceID right now — read here, through
//     GET /api/workspaces/{id} (which is why toolRoutes lists that route for
//     all six), never taken from the caller's own arguments;
//   - a mismatch, and an empty argument, are a REFUSAL: this returns an
//     error, the tool returns it, nothing is called and nothing changes. It
//     is never a silent no-op reported as success.
//
// What it stops and what it does not, from D6, so nobody mistakes it for
// more than it is: it does NOT stop prompt injection — an agent that has
// been turned reads the slug with list_workspaces and echoes it back in the
// same turn. It DOES stop the far likelier failure of an agent
// autocompleting a plausible workspace id or reaching for the neighbouring
// tool on a flat surface, because the argument cannot be produced without
// having read that specific workspace first.
//
// The refusal deliberately does NOT echo the slug it expected. Printing the
// answer in the refusal would turn one wrong guess into a two-step oracle
// and cost the property the previous paragraph is built on; the message
// says where to read it instead.
func confirmWorkspaceSlug(ctx context.Context, lb *loopback, tool string, workspaceID int64, confirmSlug string) error {
	got := strings.TrimSpace(confirmSlug)
	if got == "" {
		return fmt.Errorf("%s refuses to run without %s: pass the slug of workspace %d exactly as get_workspace reports it",
			tool, confirmSlugField, workspaceID)
	}

	method, path := toolPath(tool, "GET /api/workspaces/{id}", workspaceID)
	var ws confirmWorkspaceWire
	if err := lb.call(ctx, method, path, nil, &ws); err != nil {
		return err
	}

	if got != ws.Slug {
		return fmt.Errorf("%s refused: %s=%q does not name workspace %d — read its slug with get_workspace and pass it exactly; nothing was changed",
			tool, confirmSlugField, confirmSlug, workspaceID)
	}
	return nil
}

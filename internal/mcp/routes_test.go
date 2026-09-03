package mcp

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
)

// TestToolRoutesAgreeWithAdminAllowlist is D12 clause 5 of the mocker-a-mcp
// gate document, and it reads the REAL allowlist through
// admin.MCPAllowedRoutes rather than a copy: a second copy of a security
// allowlist is free to drift from the first, at which point this test passes
// over a statement of the seam nobody enforces. (internal/admin does not
// import internal/mcp — checked, not assumed — so this import edge exists in
// one direction only and no cycle is possible.)
//
// Both directions matter and they catch opposite defects:
//
//   - table ⊆ allowlist stops a tool shipping with a route CallAsMCP will
//     refuse. Nothing else in this package would catch it: every tool test
//     here drives the scriptedCaller fake (tools_ops_test.go), which never
//     consults the allowlist, so the failure would first appear at run time.
//   - allowlist ⊆ table stops the allowlist growing without a tool behind
//     it — reach handed to anything holding the bearer key that nothing in
//     this package ever asked for.
func TestToolRoutesAgreeWithAdminAllowlist(t *testing.T) {
	t.Parallel()

	allowed := admin.MCPAllowedRoutes()
	inAllowlist := make(map[string]bool, len(allowed))
	for _, r := range allowed {
		inAllowlist[r] = true
	}

	inTable := make(map[string][]string)
	for tool, routes := range toolRoutes {
		for _, r := range routes {
			inTable[r] = append(inTable[r], tool)
			if !inAllowlist[r] {
				t.Errorf("tool %q calls %q, which admin.mcpAllowedRoutes does not carry — CallAsMCP would refuse it", tool, r)
			}
		}
	}

	for _, r := range allowed {
		if _, ok := inTable[r]; !ok {
			t.Errorf("admin.mcpAllowedRoutes carries %q, which no tool in toolRoutes calls — reach with nothing behind it", r)
		}
	}
}

// TestMCPAllowedRoutesIsACopy pins the one property the accessor exists to
// have besides existing at all: it must not hand out the package var's own
// backing array, or any caller could rewrite the allowlist in place.
func TestMCPAllowedRoutesIsACopy(t *testing.T) {
	t.Parallel()

	first := admin.MCPAllowedRoutes()
	if len(first) == 0 {
		t.Fatal("admin.MCPAllowedRoutes() returned nothing")
	}
	first[0] = "POST /api/auth/login"

	second := admin.MCPAllowedRoutes()
	if slices.Contains(second, "POST /api/auth/login") {
		t.Error("mutating the returned slice changed the allowlist itself; MCPAllowedRoutes must return a copy")
	}
}

// TestToolRoutesPopulation pins the tool count so a tool added without a
// toolRoutes entry — the one way a tool can reach a route this file does not
// describe — fails here rather than at run time.
func TestToolRoutesPopulation(t *testing.T) {
	t.Parallel()

	if len(toolRoutes) != toolCount {
		names := make([]string, 0, len(toolRoutes))
		for tool := range toolRoutes {
			names = append(names, tool)
		}
		sort.Strings(names)
		t.Errorf("toolRoutes holds %d tools, want %d: %s\n"+
			"a new tool needs a row here AND its route in admin.mcpAllowedRoutes; bump toolCount only together with both",
			len(toolRoutes), toolCount, strings.Join(names, ", "))
	}
}

// TestToolPath covers the three properties every tool phase depends on: the
// method comes out of the template, ids are rendered as digits, and an
// already-escaped opKey is substituted RAW (escaping it twice is the live
// bug renderParam documents).
func TestToolPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tool       string
		template   string
		params     []any
		wantMethod string
		wantPath   string
	}{
		{"no params", "list_workspaces", "GET /api/workspaces", nil, "GET", "/api/workspaces"},
		{"one id", "get_workspace", "GET /api/workspaces/{id}", []any{int64(7)}, "GET", "/api/workspaces/7"},
		{
			"already-escaped opKey stays raw",
			"get_operation", "GET /api/workspaces/{id}/operations/{opKey}",
			[]any{int64(7), "GET%20%2Fusers%2F%7Bid%7D"},
			"GET", "/api/workspaces/7/operations/GET%20%2Fusers%2F%7Bid%7D",
		},
		{
			"two ids",
			"rollback_workspace", "POST /api/workspaces/{id}/rollback/{cid}",
			[]any{int64(3), int64(12)},
			"POST", "/api/workspaces/3/rollback/12",
		},
		{
			"placeholder mid-path",
			"activate_scenario", "POST /api/workspaces/{id}/scenarios/{sid}/activate",
			[]any{int64(3), int64(9)},
			"POST", "/api/workspaces/3/scenarios/9/activate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			method, path := toolPath(tt.tool, tt.template, tt.params...)
			if method != tt.wantMethod || path != tt.wantPath {
				t.Errorf("toolPath(%q, %q) = %q %q, want %q %q", tt.tool, tt.template, method, path, tt.wantMethod, tt.wantPath)
			}
		})
	}
}

// TestToolPathPanics is the "loudly and early" half: every one of these is a
// fixed property of a call site, wrong on every call or on none, so a panic
// is the correct answer and a silently empty path — a 404 in production — is
// not.
func TestToolPathPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tool     string
		template string
		params   []any
	}{
		{"unknown tool", "no_such_tool", "GET /api/workspaces", nil},
		{"route the tool does not declare", "list_workspaces", "DELETE /api/workspaces/{id}", []any{int64(1)}},
		{"too few params", "get_workspace", "GET /api/workspaces/{id}", nil},
		{"too many params", "list_workspaces", "GET /api/workspaces", []any{int64(1)}},
		{"unrenderable param type", "get_workspace", "GET /api/workspaces/{id}", []any{1.5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Errorf("toolPath(%q, %q, %v) did not panic", tt.tool, tt.template, tt.params)
				}
			}()
			_, _ = toolPath(tt.tool, tt.template, tt.params...)
		})
	}
}

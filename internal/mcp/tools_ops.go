// addOperationTools registers the six workspace/spec/operation tools (§C
// tools 1-6 of the MCP slice's context document): list_workspaces,
// create_workspace, list_specs, find_operations, get_operation and
// set_operation_response.
//
// Every tool here is an ADAPTER over the admin plane's own routes (§A6):
// it decodes the JSON the handler in internal/admin actually writes, using a
// PRIVATE wire struct that mirrors only the fields this tool needs — never
// the admin package's own (unexported) response types, which this package
// cannot import anyway. Getting a wire struct's field names right requires
// reading the handler that writes them, not guessing from the route name;
// every wire type below cites the handler and line it was read from.
package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// findOperationsDefaultLimit/findOperationsMaxLimit bound find_operations'
// result page. Unlike internal/admin's own paging constants (100/500,
// spec_handlers.go), the resource being protected here is an agent's own
// context window, not a network payload — a tool result this large defeats
// the reason find_operations exists (§C's compaction rule: "no such
// operation" read as "does not exist" is the failure mode a silent truncation
// causes). Smaller numbers are the right default for a tool meant to locate
// ONE operation, and the caller can always narrow `query` instead of raising
// `limit`.
const (
	findOperationsDefaultLimit = 20
	findOperationsMaxLimit     = 100
)

func addOperationTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "list_workspaces",
		Description: "Lists every workspace in this mocker instance (not just ones the MCP identity " +
			"owns), with its addressable base URL, base path, attached spec id and revision. Reach for " +
			"this first to find a workspace's id before calling any other workspace-scoped tool.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListWorkspaces(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "create_workspace",
		Description: "Creates a new workspace, optionally attached to an already-imported spec by its " +
			"id. Use this to stand up a fresh mock target; call list_specs first if you need to find a " +
			"specId, since a taken name is silently disambiguated and the response reports the slug " +
			"actually assigned. Do not blindly retry this after a lost or timed-out response: the slug " +
			"is optional and gets uniquified from the name when omitted, so a retry creates a SECOND " +
			"workspace under a different slug rather than being refused — there is no unique key here " +
			"the way there is for create_endpoint or create_scenario. Call list_workspaces first to check " +
			"whether the first attempt actually landed.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, handleCreateWorkspace(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_specs",
		Description: "Lists every imported OpenAPI spec by id, name, version, format and base path; " +
			"pass specId to additionally include that spec's operation, degraded-operation and warning " +
			"counts from its import report. Use this to discover a specId for create_workspace, or to " +
			"check how cleanly a spec imported before building a workspace on it.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListSpecs(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "find_operations",
		Description: "Searches a workspace's operations by a case-insensitive substring over method " +
			"and path, returning each match's opKey, declared statuses and whether it already has an " +
			"override. Use this to locate the opKey get_operation and set_operation_response need — " +
			"the result is capped and always says so via returned/total/truncated, so a capped list is " +
			"never mistaken for \"no such operation\".",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleFindOperations(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "get_operation",
		Description: "Reads one operation's merged view (its spec-declared statuses) together with the " +
			"shape of its stored override, if any: mode, media type, recipe count, delay and list-size " +
			"settings — never a pinned response body. Use this to inspect an operation before changing " +
			"it; it never fails just because nobody has overridden the operation yet.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleGetOperation(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "set_operation_response",
		Description: "Forces the HTTP status a workspace serves for one operation, preserving every " +
			"other part of its override — pinned bodies, headers, recipes, delay, list size — exactly " +
			"as they were. Use this to make an operation return a specific status for a test; it always " +
			"turns the override on and re-enables the route, and the result lists exactly what changed. " +
			"editVersion is REQUIRED and must be YOUR OWN expectation from a prior get_operation call — " +
			"never the version this tool reads internally to seed the write, which would make the check " +
			"vacuous. Call get_operation first; if it 404s (no override exists yet), pass editVersion: 0 " +
			"— that IS the correct expectation for a fresh row, not an error condition. A write whose " +
			"editVersion is stale answers with a 409 edit_conflict, carried as a populated conflict field " +
			"(never a generic tool error) holding the current document's compacted shape and its real " +
			"editVersion to retry with.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleSetOperationResponse(lb))
}

// ---- list_workspaces ----

// ListWorkspacesInput takes no arguments: this tool always lists every
// workspace, never just the caller's own (see the tool Description above).
type ListWorkspacesInput struct{}

// WorkspaceLine is one workspace, projected from workspaceView
// (internal/admin/workspace_handlers.go:42). BasePath is pulled out of
// Settings — workspaceView's URL carries no base path at all
// (workspace_handlers.go:52-59: it is the mock plane's ORIGIN, deliberately),
// so without this field an agent has no way to address a workspace whose
// base path is non-empty.
type WorkspaceLine struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	BasePath string `json:"basePath"`
	SpecID   *int64 `json:"specId,omitempty"`
	Revision int64  `json:"revision"`
}

// ListWorkspacesOutput is list_workspaces' declared output schema.
type ListWorkspacesOutput struct {
	Workspaces []WorkspaceLine `json:"workspaces"`
}

// workspaceWire decodes the fields of workspaceView
// (internal/admin/workspace_handlers.go:42-63) this tool actually projects.
// Fields it does not need (ownerId, forkedFrom, scenarioId, the rest of
// Settings, createdAt/updatedAt) are simply absent here — encoding/json (via
// jsonx) ignores JSON object keys with no matching Go field, so this is a
// safe subset, not a fragile exact mirror.
type workspaceWire struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	SpecID   *int64 `json:"specId"`
	Revision int64  `json:"revision"`
	Settings struct {
		BasePath string `json:"basePath"`
	} `json:"settings"`
}

func handleListWorkspaces(lb *loopback) sdk.ToolHandlerFor[ListWorkspacesInput, ListWorkspacesOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, _ ListWorkspacesInput) (*sdk.CallToolResult, ListWorkspacesOutput, error) {
		var wire []workspaceWire
		// ?all=1: the MCP identity (internal/auth.EnsureUser, §B1) is a real
		// but synthetic user row. Without ?all=1, handleListWorkspaces
		// (workspace_handlers.go:134-157) would filter to workspaces THAT
		// IDENTITY owns — which, for a tool whose whole job is discovery,
		// would hide every workspace a human created through the UI. The
		// admin route allowlist matches on the mux PATTERN, not the raw
		// string (§B2 rule 8), so the query string here does not need its
		// own allowlist entry — which is why toolPath hands back a path a
		// caller appends to rather than a finished URL.
		method, path := toolPath("list_workspaces", "GET /api/workspaces")
		if err := lb.call(ctx, method, path+"?all=1", nil, &wire); err != nil {
			return nil, ListWorkspacesOutput{}, err
		}
		out := ListWorkspacesOutput{Workspaces: make([]WorkspaceLine, len(wire))}
		for i, w := range wire {
			out.Workspaces[i] = WorkspaceLine{
				ID:       w.ID,
				Slug:     w.Slug,
				Name:     w.Name,
				URL:      w.URL,
				BasePath: w.Settings.BasePath,
				SpecID:   w.SpecID,
				Revision: w.Revision,
			}
		}
		return nil, out, nil
	}
}

// ---- create_workspace ----

// CreateWorkspaceInput is create_workspace's input. Name has no `omitempty`
// tag, so the SDK's schema inference (github.com/google/jsonschema-go: any
// field without omitempty/omitzero becomes a required property) rejects a
// call missing it before this tool's handler ever runs — earlier and cheaper
// feedback than round-tripping to the admin plane's own "name is required"
// 400 (handleCreateWorkspace, workspace_handlers.go:184-188).
type CreateWorkspaceInput struct {
	Name   string `json:"name"`
	Slug   string `json:"slug,omitempty"`
	SpecID *int64 `json:"specId,omitempty"`
}

// CreateWorkspaceOutput is create_workspace's declared output schema.
type CreateWorkspaceOutput struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	URL  string `json:"url"`
}

func handleCreateWorkspace(lb *loopback) sdk.ToolHandlerFor[CreateWorkspaceInput, CreateWorkspaceOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in CreateWorkspaceInput) (*sdk.CallToolResult, CreateWorkspaceOutput, error) {
		body, err := jsonx.Marshal(struct {
			Name   string `json:"name"`
			Slug   string `json:"slug,omitempty"`
			SpecID *int64 `json:"specId,omitempty"`
		}{Name: in.Name, Slug: in.Slug, SpecID: in.SpecID})
		if err != nil {
			return nil, CreateWorkspaceOutput{}, fmt.Errorf("mcp: encode create_workspace request: %w", err)
		}

		var wire workspaceWire
		method, path := toolPath("create_workspace", "POST /api/workspaces")
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, CreateWorkspaceOutput{}, err
		}
		return nil, CreateWorkspaceOutput{ID: wire.ID, Slug: wire.Slug, URL: wire.URL}, nil
	}
}

// ---- list_specs ----

// ListSpecsInput is list_specs' input. SpecID is a pointer with `omitempty`
// — the idiomatic optional-field shape — because "no specId" and "specId 0"
// are different requests and a plain int64 could not tell them apart.
type ListSpecsInput struct {
	SpecID *int64 `json:"specId,omitempty"`
}

// SpecLine is one spec, projected from specView (spec_handlers.go:38) — id,
// name, version, format, basePath, and NOT title (specView has none, per §0)
// — with the report counts attached only when the caller asked for THIS
// spec's report (list_specs' Description).
type SpecLine struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Format   string `json:"format"`
	BasePath string `json:"basePath"`

	Operations *int `json:"operations,omitempty"`
	Degraded   *int `json:"degraded,omitempty"`
	Warnings   *int `json:"warnings,omitempty"`
}

// ListSpecsOutput is list_specs' declared output schema.
type ListSpecsOutput struct {
	Specs []SpecLine `json:"specs"`
}

// specWire decodes specView's fields this tool projects
// (spec_handlers.go:38-49).
type specWire struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Format   string `json:"format"`
	BasePath string `json:"basePath"`
}

// specReportWire decodes reportView (spec_handlers.go:74-81). Warnings is
// left as raw JSON per entry — this tool only ever needs the COUNT, so
// mirroring warningView's own three fields here would be a second copy of a
// shape this tool has no use for beyond len().
type specReportWire struct {
	Operations int                `json:"operations"`
	Degraded   int                `json:"degraded"`
	Warnings   []jsonx.RawMessage `json:"warnings"`
}

func handleListSpecs(lb *loopback) sdk.ToolHandlerFor[ListSpecsInput, ListSpecsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListSpecsInput) (*sdk.CallToolResult, ListSpecsOutput, error) {
		var wire []specWire
		method, path := toolPath("list_specs", "GET /api/specs")
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListSpecsOutput{}, err
		}
		lines := make([]SpecLine, len(wire))
		for i, sp := range wire {
			lines[i] = SpecLine{ID: sp.ID, Name: sp.Name, Version: sp.Version, Format: sp.Format, BasePath: sp.BasePath}
		}

		if in.SpecID != nil {
			var report specReportWire
			reportMethod, reportPath := toolPath("list_specs", "GET /api/specs/{id}/report", *in.SpecID)
			if err := lb.call(ctx, reportMethod, reportPath, nil, &report); err != nil {
				return nil, ListSpecsOutput{}, err
			}
			ops, degraded, warnings := report.Operations, report.Degraded, len(report.Warnings)
			for i := range lines {
				if lines[i].ID == *in.SpecID {
					lines[i].Operations = &ops
					lines[i].Degraded = &degraded
					lines[i].Warnings = &warnings
				}
			}
		}
		return nil, ListSpecsOutput{Specs: lines}, nil
	}
}

// ---- shared: the merged operations list (find_operations, get_operation) ----

// mergedStatusWire decodes mergedStatusView (override_handlers.go:68-73).
type mergedStatusWire struct {
	Selector   string `json:"selector"`
	HTTPStatus int    `json:"httpStatus"`
	IsDefault  bool   `json:"isDefault"`
	MediaType  string `json:"mediaType,omitempty"`
}

// mergedOpWire decodes mergedOperationView (override_handlers.go:116-122).
// Override is decoded only far enough to know whether one exists
// (opOverrideSummaryView is present, override_handlers.go:86-92, exactly
// when a row exists for this operation) — find_operations only reports a
// bool, and get_operation re-fetches the full document from the
// single-operation route anyway (§C-a), so decoding the summary's OWN
// fields here would be dead code.
type mergedOpWire struct {
	Method   string             `json:"method"`
	Path     string             `json:"path"`
	OpKey    string             `json:"opKey"`
	Statuses []mergedStatusWire `json:"statuses"`
	Override *struct{}          `json:"override,omitempty"`
}

// fetchMergedOperations reads GET .../operations for workspaceID — the ONE
// route (per §C-a) that answers the MERGED view: what the spec declares plus
// whatever override state exists on top. It returns every operation
// (override_handlers.go:158 passes limit=0, specs.Repo.Operations' own
// documented "no LIMIT clause" sentinel), which is why find_operations does
// its own capping client-side rather than asking the admin route to page.
//
// tool is the CALLING tool's own name, because two tools reach this route
// (find_operations and get_operation) and toolPath resolves a route against
// the caller's row in toolRoutes: passing a name that does not declare this
// route panics rather than quietly building a path nobody allowlisted.
func fetchMergedOperations(ctx context.Context, lb *loopback, tool string, workspaceID int64) ([]mergedOpWire, error) {
	var ops []mergedOpWire
	method, path := toolPath(tool, "GET /api/workspaces/{id}/operations", workspaceID)
	if err := lb.call(ctx, method, path, nil, &ops); err != nil {
		return nil, err
	}
	return ops, nil
}

func findMergedOp(ops []mergedOpWire, opKey string) (mergedOpWire, bool) {
	for _, op := range ops {
		if op.OpKey == opKey {
			return op, true
		}
	}
	return mergedOpWire{}, false
}

// ---- find_operations ----

// FindOperationsInput is find_operations' input. Query filters by a
// case-insensitive substring over "METHOD path"; Limit narrows the page but
// can never widen it past findOperationsMaxLimit.
type FindOperationsInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Query       string `json:"query,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// OperationLine is one operation, compact enough to scan a whole match list
// at once: Statuses is bare HTTP status codes (get_operation gives the
// richer per-status shape once the caller has picked ONE operation to
// inspect), and Overridden is only a bool — the override's own shape is
// get_operation's job.
type OperationLine struct {
	OpKey      string `json:"opKey"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Statuses   []int  `json:"statuses"`
	Overridden bool   `json:"overridden"`
}

// FindOperationsOutput is find_operations' declared output schema. Total
// counts rows MATCHING Query (not every operation in the workspace) and
// Truncated is true whenever the match count exceeds what was returned —
// never truncated silently (§C, the tool table's compaction rule): a model
// told "no such operation" because a cap hid it would wrongly conclude the
// operation does not exist.
type FindOperationsOutput struct {
	Operations []OperationLine `json:"operations"`
	Returned   int             `json:"returned"`
	Total      int             `json:"total"`
	Truncated  bool            `json:"truncated"`
}

func handleFindOperations(lb *loopback) sdk.ToolHandlerFor[FindOperationsInput, FindOperationsOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in FindOperationsInput) (*sdk.CallToolResult, FindOperationsOutput, error) {
		ops, err := fetchMergedOperations(ctx, lb, "find_operations", in.WorkspaceID)
		if err != nil {
			return nil, FindOperationsOutput{}, err
		}

		q := strings.ToLower(strings.TrimSpace(in.Query))
		matched := make([]mergedOpWire, 0, len(ops))
		for _, op := range ops {
			if q == "" || strings.Contains(strings.ToLower(op.Method+" "+op.Path), q) {
				matched = append(matched, op)
			}
		}

		limit := findOperationsDefaultLimit
		if in.Limit > 0 {
			limit = in.Limit
		}
		if limit > findOperationsMaxLimit {
			limit = findOperationsMaxLimit
		}

		total := len(matched)
		returned := total
		if returned > limit {
			returned = limit
		}

		lines := make([]OperationLine, returned)
		for i := 0; i < returned; i++ {
			op := matched[i]
			statuses := make([]int, len(op.Statuses))
			for j, st := range op.Statuses {
				statuses[j] = st.HTTPStatus
			}
			lines[i] = OperationLine{
				OpKey:      op.OpKey,
				Method:     op.Method,
				Path:       op.Path,
				Statuses:   statuses,
				Overridden: op.Override != nil,
			}
		}

		return nil, FindOperationsOutput{
			Operations: lines,
			Returned:   returned,
			Total:      total,
			Truncated:  returned < total,
		}, nil
	}
}

// ---- get_operation ----

// GetOperationInput is get_operation's input. OpKey is taken verbatim — see
// operationPath's comment on why it must never be re-escaped.
type GetOperationInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	OpKey       string `json:"opKey"`
}

// MergedStatus is one spec-declared response, projected from
// mergedStatusWire.
type MergedStatus struct {
	Selector   string `json:"selector"`
	HTTPStatus int    `json:"httpStatus"`
	IsDefault  bool   `json:"isDefault"`
	MediaType  string `json:"mediaType,omitempty"`
}

// ListSizeView is overrides.ListSize's wire shape (overrides.go:92-95),
// reused verbatim as both this tool's decode target and its output
// projection — the two shapes are identical, so a second type would only be
// a second thing to keep in sync.
type ListSizeView struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// ResponseShape is one override response's SHAPE, never its content: mode,
// declared media type, whether a body is pinned, and how many recipes are
// bound — not the pinned body itself and not the recipe definitions. §C's
// compaction rule ("returns no response body by default") is why HasBody is
// a bool instead of the body's bytes.
type ResponseShape struct {
	Mode        string `json:"mode"`
	MediaType   string `json:"mediaType,omitempty"`
	HasBody     bool   `json:"hasBody"`
	RecipeCount int    `json:"recipeCount"`
	// Function is the variant's Lua source, verbatim — the one field of a
	// variant this projection carries in full rather than as a shape. The
	// compaction rule (§C) keeps a pinned BODY out because a body is data of
	// any size; a function is the contract the agent itself authored, and
	// the guide's prescribed flow is read-then-write-the-whole-document, so
	// a read that hid it made every such write DELETE the Lua with nothing in
	// any tool output saying a function had existed. Review finding 5.
	Function string `json:"function,omitempty"`
}

// OverrideDetail is the stored override document's shape, projected from
// overrideDetailWire — present on GetOperationOutput only when a row exists
// for the operation (§C-a: a 404 from the single-operation route means "no
// override", not an error, and is reported by leaving this nil).
type OverrideDetail struct {
	OverrideOn   bool                     `json:"overrideOn"`
	RouteOff     bool                     `json:"routeOff"`
	ActiveStatus *int                     `json:"activeStatus,omitempty"`
	DelayMs      *int                     `json:"delayMs,omitempty"`
	ListSize     *ListSizeView            `json:"listSize,omitempty"`
	ValidateReq  *bool                    `json:"validateReq,omitempty"`
	Responses    map[string]ResponseShape `json:"responses,omitempty"`
	UpdatedAt    int64                    `json:"updatedAt"`
	// EditVersion is A3's per-row compare-and-swap token (mocker-a3-cas D5):
	// this is the projection get_operation answers with, so it is the ONE
	// read that actually precedes set_operation_variant's and
	// set_operation_response's writes — not a list projection, because this
	// operation has none (find_operations reports only a bool, never this
	// shape). A caller with no stored override (Override absent on
	// GetOperationOutput, a 404 from the single-operation route) has no
	// EditVersion to read at all: D7's rule is that the 404 ITSELF is the
	// absent-row expectation, and the value it implies is 0 — see this
	// package's two operation write tools' own Descriptions.
	EditVersion int64 `json:"editVersion"`
}

// OperationDetail is get_operation's merged-row-plus-override projection.
type OperationDetail struct {
	OpKey    string          `json:"opKey"`
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Statuses []MergedStatus  `json:"statuses"`
	Override *OverrideDetail `json:"override,omitempty"`
}

// GetOperationOutput is get_operation's declared output schema.
type GetOperationOutput struct {
	Operation OperationDetail `json:"operation"`
}

// variantDetailWire decodes one entry of overrideDetailWire.Responses — the
// per-status fields of overrides.Variant (overrides.go:67-76) this tool
// projects the SHAPE of. Body is decoded only to measure its length
// (HasBody); it is never copied into ResponseShape or returned to the model.
type variantDetailWire struct {
	Mode      string                      `json:"mode"`
	MediaType string                      `json:"mediaType,omitempty"`
	Body      jsonx.RawMessage            `json:"body,omitempty"`
	Recipes   map[string]jsonx.RawMessage `json:"recipes,omitempty"`
	Function  string                      `json:"function,omitempty"`
}

// overrideDetailWire decodes overrideDocView's overrideMutableFields
// (override_handlers.go:210-219) — the full stored override document GET
// .../operations/{opKey} answers (override_handlers.go:258-285).
type overrideDetailWire struct {
	OverrideOn   bool                         `json:"overrideOn"`
	RouteOff     bool                         `json:"routeOff"`
	ActiveStatus *int                         `json:"activeStatus,omitempty"`
	Responses    map[string]variantDetailWire `json:"responses"`
	ListSize     *ListSizeView                `json:"listSize,omitempty"`
	DelayMs      *int                         `json:"delayMs,omitempty"`
	ValidateReq  *bool                        `json:"validateReq,omitempty"`
	UpdatedAt    int64                        `json:"updatedAt"`
	// EditVersion sits beside overrideMutableFields on the admin wire (A3's
	// sibling-never-member rule), which is exactly why it decodes here
	// rather than inside variantDetailWire: this struct already mirrors
	// overrideDocView's own top level (override_handlers.go), and this
	// field is also present, unrenamed, on PUT's 409 conflict payload
	// (overrideConflictDetails) — the same wire struct decodes both a
	// successful GET and set_operation_variant/set_operation_response's
	// typed conflict Document (decodeOperationConflict below).
	EditVersion int64 `json:"editVersion"`
}

// newOverrideDetail projects w down to ResponseShape per status — never the
// pinned body or the recipe definitions themselves (§C's compaction rule).
func newOverrideDetail(w overrideDetailWire) *OverrideDetail {
	resp := make(map[string]ResponseShape, len(w.Responses))
	for status, v := range w.Responses {
		mode := v.Mode
		if mode == "" {
			mode = "generated" // overrides.Variant.Mode's own documented default (overrides.go:68).
		}
		resp[status] = ResponseShape{
			Mode:        mode,
			MediaType:   v.MediaType,
			HasBody:     len(v.Body) > 0,
			RecipeCount: len(v.Recipes),
			Function:    v.Function,
		}
	}
	return &OverrideDetail{
		OverrideOn:   w.OverrideOn,
		RouteOff:     w.RouteOff,
		ActiveStatus: w.ActiveStatus,
		DelayMs:      w.DelayMs,
		ListSize:     w.ListSize,
		ValidateReq:  w.ValidateReq,
		Responses:    resp,
		UpdatedAt:    w.UpdatedAt,
		EditVersion:  w.EditVersion,
	}
}

// OperationConflictDetail is D6's conflict payload for the two write tools
// that share op_overrides — set_operation_variant and
// set_operation_response — compacted the same way OverrideDetail already is
// for get_operation (mocker-a3-cas D5: "compaction wins" at the MCP hop, so
// the conflict document is the compacted shape plus editVersion, never the
// round-trippable pinned-body document HTTP callers get). Document is nil
// exactly when Gone is true (D6's tombstone) — never a present Document
// carrying a null editVersion, which would conflate "changed" with "gone".
type OperationConflictDetail struct {
	Gone     bool            `json:"gone"`
	Document *OverrideDetail `json:"document,omitempty"`
}

// decodeOperationConflict decodes a 409 edit_conflict's `details` for
// PUT .../operations/{opKey} — either editConflictGone{gone:true} or
// overrideConflictDetails (overrideMutableFields + editVersion,
// override_handlers.go) — into OperationConflictDetail. overrideDetailWire
// decodes both a successful GET's document and this conflict shape: both
// are overrideMutableFields plus editVersion on the wire, so one wire type
// serves both call sites, matching newOverrideDetail's own reuse from GET.
func decodeOperationConflict(details jsonx.RawMessage) (*OperationConflictDetail, error) {
	if detailsAreGone(details) {
		return &OperationConflictDetail{Gone: true}, nil
	}
	var wire overrideDetailWire
	if err := jsonx.Unmarshal(details, &wire); err != nil {
		return nil, fmt.Errorf("mcp: decode edit_conflict details: %w", err)
	}
	return &OperationConflictDetail{Document: newOverrideDetail(wire)}, nil
}

func handleGetOperation(lb *loopback) sdk.ToolHandlerFor[GetOperationInput, GetOperationOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in GetOperationInput) (*sdk.CallToolResult, GetOperationOutput, error) {
		ops, err := fetchMergedOperations(ctx, lb, "get_operation", in.WorkspaceID)
		if err != nil {
			return nil, GetOperationOutput{}, err
		}
		row, ok := findMergedOp(ops, in.OpKey)
		if !ok {
			// A genuinely unknown opKey — as opposed to §C-a's "no override
			// yet", handled below — so this IS reported as a tool error: the
			// model asked about an operation that does not exist in this
			// workspace's spec at all.
			return nil, GetOperationOutput{}, fmt.Errorf("workspace %d has no operation with opKey %q", in.WorkspaceID, in.OpKey)
		}

		detail := OperationDetail{OpKey: row.OpKey, Method: row.Method, Path: row.Path}
		// mergedStatusWire and MergedStatus share an identical field set, so
		// this is a plain type conversion rather than a field-by-field copy
		// (staticcheck S1016) — the compiler itself would refuse to compile
		// this line the day the two shapes diverge.
		detail.Statuses = make([]MergedStatus, len(row.Statuses))
		for i, st := range row.Statuses {
			detail.Statuses[i] = MergedStatus(st)
		}

		// §C-a: GET .../operations/{opKey} answers the FULL stored override
		// document, or 404 when none exists — NOT the merged view (that's
		// the call above). do, not call: this tool must branch on the exact
		// status rather than let any non-2xx become a uniform tool error, or
		// this handler would 404 on exactly the operation an agent most
		// wants to look at — one nobody has overridden yet.
		getMethod, getPath := toolPath("get_operation", "GET /api/workspaces/{id}/operations/{opKey}", in.WorkspaceID, in.OpKey)
		status, body, err := lb.do(ctx, getMethod, getPath, nil)
		if err != nil {
			return nil, GetOperationOutput{}, err
		}
		switch status {
		case http.StatusNotFound:
			// No override exists. detail.Override stays nil — the merged
			// list above already confirmed the OPERATION exists, so this is
			// not an error condition at all.
		case http.StatusOK:
			var wire overrideDetailWire
			if err := jsonx.Unmarshal(body, &wire); err != nil {
				return nil, GetOperationOutput{}, fmt.Errorf("mcp: decode override document: %w", err)
			}
			detail.Override = newOverrideDetail(wire)
		default:
			return nil, GetOperationOutput{}, toolErr(status, body)
		}

		return nil, GetOperationOutput{Operation: detail}, nil
	}
}

// ---- set_operation_response ----

// SetOperationResponseInput is set_operation_response's input. Status has no
// `omitempty` — the SDK's schema inference (see CreateWorkspaceInput's
// comment) makes it a required property, so a call omitting it is rejected
// before this tool's handler runs; Status: 0 is a legal (if unusual) value,
// which `omitempty` would have silently swallowed on the wire.
type SetOperationResponseInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	OpKey       string `json:"opKey"`
	Status      int    `json:"status"`
	// EditVersion is A3's REQUIRED compare-and-swap expectation
	// (mocker-a3-cas D10/D11): the CALLER's own — from a prior
	// get_operation, or 0 when that read 404'd (see this tool's own
	// Description). This tool's internal GET (below) also reads a version,
	// but forwarding THAT one to the PUT would make the check vacuous — the
	// guarded window would shrink to microseconds inside this one call
	// instead of covering the model's actual read-then-decide window (D11).
	// A plain, non-pointer int64: jsonschema-go follows pointers and allows
	// an explicit JSON null for them, so a *int64 here would be required
	// AND nullable, and a model sending null would silently get an
	// unguarded write (see OverrideDocumentInput's identical reasoning for
	// OverrideOn/RouteOff, this package's other required-bool precedent).
	EditVersion int64 `json:"editVersion"`
}

// SetOperationResponseOutput is set_operation_response's declared output
// schema. Changed is always a non-nil (possibly empty) slice — an idempotent
// call that changes nothing reports that honestly as an empty list, not as a
// JSON null the model has to special-case. EditVersion and Conflict are A3's
// pair (mocker-a3-cas D5/D6): EditVersion is present on every successful
// write, so a caller can write again without re-reading; Conflict is present
// only when the write lost the compare-and-swap, never both at once.
type SetOperationResponseOutput struct {
	OpKey        string                   `json:"opKey"`
	ActiveStatus int                      `json:"activeStatus"`
	Changed      []string                 `json:"changed"`
	EditVersion  int64                    `json:"editVersion,omitempty"`
	Conflict     *OperationConflictDetail `json:"conflict,omitempty"`
}

// overridePutBody is the request body PUT .../operations/{opKey} accepts —
// overrideMutableFields (override_handlers.go:210-219) — with every field
// this tool does not itself interpret (Responses, ListSize, FailDirective)
// kept as RAW JSON rather than re-modeled through
// internal/overrides.Variant/Condition/Recipe. §C-b: PUT is a FULL
// REPLACEMENT, so anything decoded-then-re-encoded through a lossy
// intermediate type would silently drop whatever field that type forgot —
// exactly the "destroys pinned bodies, headers, recipes…" failure §C-b
// warns about. Round-tripping as raw bytes cannot lose a field this package
// does not know about.
type overridePutBody struct {
	OverrideOn    bool                        `json:"overrideOn"`
	RouteOff      bool                        `json:"routeOff"`
	ActiveStatus  *int                        `json:"activeStatus,omitempty"`
	Responses     map[string]jsonx.RawMessage `json:"responses,omitempty"`
	ListSize      jsonx.RawMessage            `json:"listSize,omitempty"`
	DelayMs       *int                        `json:"delayMs,omitempty"`
	FailDirective jsonx.RawMessage            `json:"failDirective,omitempty"`
	ValidateReq   *bool                       `json:"validateReq,omitempty"`
	// EditVersion sits beside the document fields above, never inside a
	// nested object — putOperationRequest's own sibling rule
	// (override_handlers.go, mocker-a3-cas D7). Set from in.EditVersion
	// (the CALLER's expectation), never from a version this handler read
	// internally — see SetOperationResponseInput.EditVersion's own comment.
	EditVersion int64 `json:"editVersion"`
}

func handleSetOperationResponse(lb *loopback) sdk.ToolHandlerFor[SetOperationResponseInput, SetOperationResponseOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in SetOperationResponseInput) (*sdk.CallToolResult, SetOperationResponseOutput, error) {
		getMethod, getPath := toolPath("set_operation_response", "GET /api/workspaces/{id}/operations/{opKey}", in.WorkspaceID, in.OpKey)
		putMethod, putPath := toolPath("set_operation_response", "PUT /api/workspaces/{id}/operations/{opKey}", in.WorkspaceID, in.OpKey)
		changed := make([]string, 0, 3)

		// GET first (§C-b): PUT is a full replacement, so every field this
		// tool is not explicitly asked to change must be read back and
		// resent whole, or it is destroyed.
		status, body, err := lb.do(ctx, getMethod, getPath, nil)
		if err != nil {
			return nil, SetOperationResponseOutput{}, err
		}

		var doc overridePutBody
		switch status {
		case http.StatusNotFound:
			// §C-b's pristine-operation case: nothing stored yet, so doc
			// stays at its zero value and every "before" below is implicitly
			// false/nil/absent — seed a fresh document rather than failing.
			changed = append(changed, "overrideOn: false -> true")
		case http.StatusOK:
			if err := jsonx.Unmarshal(body, &doc); err != nil {
				return nil, SetOperationResponseOutput{}, fmt.Errorf("mcp: decode override document: %w", err)
			}
			if !doc.OverrideOn {
				changed = append(changed, "overrideOn: false -> true")
			}
			if doc.RouteOff {
				changed = append(changed, "routeOff: true -> false")
			}
			switch {
			case doc.ActiveStatus == nil:
				changed = append(changed, fmt.Sprintf("activeStatus: (none) -> %d", in.Status))
			case *doc.ActiveStatus != in.Status:
				changed = append(changed, fmt.Sprintf("activeStatus: %d -> %d", *doc.ActiveStatus, in.Status))
			}
		default:
			return nil, SetOperationResponseOutput{}, toolErr(status, body)
		}

		// §C-b's second rule, applied UNCONDITIONALLY on every write through
		// this tool: overrideOn:true and routeOff:false. A PUT that set only
		// activeStatus on a document with overrideOn:false (or routeOff:true)
		// returns 200 and changes nothing an observer can see — the mock
		// plane keeps serving the old result, or a 404 — which §C-b calls
		// the worst outcome in this slice: a tool reporting success while
		// the product did not move.
		doc.OverrideOn = true
		doc.RouteOff = false
		doc.ActiveStatus = &in.Status
		// The CALLER's own expectation, not this function's internal GET's
		// (status/doc above) — see SetOperationResponseInput.EditVersion's
		// own comment on why forwarding the internal read would be vacuous.
		doc.EditVersion = in.EditVersion

		putBody, err := jsonx.Marshal(doc)
		if err != nil {
			return nil, SetOperationResponseOutput{}, fmt.Errorf("mcp: encode override document: %w", err)
		}
		var putWire struct {
			EditVersion int64 `json:"editVersion"`
		}
		details, err := writeEditGuarded(ctx, lb, putMethod, putPath, putBody, &putWire)
		if err != nil {
			return nil, SetOperationResponseOutput{}, err
		}
		if details != nil {
			conflict, cerr := decodeOperationConflict(details)
			if cerr != nil {
				return nil, SetOperationResponseOutput{}, cerr
			}
			return nil, SetOperationResponseOutput{OpKey: in.OpKey, Conflict: conflict}, nil
		}

		return nil, SetOperationResponseOutput{
			OpKey: in.OpKey, ActiveStatus: in.Status, Changed: changed, EditVersion: putWire.EditVersion,
		}, nil
	}
}

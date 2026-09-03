// addTrafficTools registers the three live-state/traffic tools (§C tools
// 7-9 of the MCP slice's context document): set_session_directive,
// list_traffic and override_from_traffic.
//
// Like tools_ops.go, every tool here is an ADAPTER over the admin plane's
// own routes (§A6): it decodes the JSON the handler in internal/admin
// actually writes, through a PRIVATE wire struct that mirrors only the
// fields this tool needs — never the admin package's own (unexported)
// response types, which this package cannot import anyway. Every wire type
// below cites the handler and file it was read from.
//
// set_session_directive is the one tool here that also builds an OUTBOUND
// wire shape from scratch (livestate.Directive's own union target). It does
// so with a private jsonx.RawMessage-based helper rather than by importing
// internal/livestate and using Directive/Target directly — not because that
// import would be illegal (livestate is a leaf package, stdlib+jsonx only,
// and importing it would create no cycle), but to keep this file at arm's
// length from every domain package exactly like tools_ops.go already is,
// so a reader of both files sees one convention rather than two.
package mcp

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// listTrafficDefaultLimit/listTrafficMaxLimit mirror
// internal/admin/traffic_handlers.go's own trafficDefaultLimit (100) and
// trafficMaxLimit (500) exactly, rather than reusing them (both are
// unexported in package admin, and this package cannot import them anyway
// — §A6). Computing the effective limit HERE, the same way the admin route
// would, rather than leaving "limit" unset on the request and letting the
// server apply its own default, is what lets this tool report an honest
// `limit` in its own output: hasMore is only meaningful when this tool
// itself knows exactly what ceiling produced `returned`.
const (
	listTrafficDefaultLimit = 100
	listTrafficMaxLimit     = 500
)

func addTrafficTools(s *sdk.Server, lb *loopback) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "set_session_directive",
		Description: "Forces or clears a temporary, RAM-only condition on a workspace's LIVE session: pin " +
			"the HTTP status for one operation or every operation, make the next N requests fail (or exactly " +
			"one), hold responses for a fixed delay, or pause them until released. This never touches a " +
			"stored override and never bumps the workspace's revision — it is for a transient test condition, " +
			"not a lasting change. Pass clearAll to remove EVERY directive currently in force at once, or " +
			"clear:true with a target (and optionally an action) to remove just that target's directives " +
			"— a pause on it is released. Scenario switching lives on its own dedicated route as of P2b " +
			"(activate/deactivate a saved scenario) and is not offered here.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleSetSessionDirective(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "list_traffic",
		Description: "Lists the most recent requests the mock plane actually served for one workspace — " +
			"method, path, status, duration, what matched, and a per-row truncated flag meaning THIS row's " +
			"own body was cut (never that the list itself was capped — see hasMore for that) — but never " +
			"request or response bodies. Pass trafficId to fetch exactly one entry's full body and headers " +
			"instead. The plain list is always a page of the table, not the whole thing, so check " +
			"returned/limit/hasMore rather than assuming everything came back. Every call also answers " +
			"lastId, a traffic row id: pass it back as since on the NEXT call to read only what arrived " +
			"after it, oldest first, instead of re-fetching the recent page and diffing ids by hand — this " +
			"reorders rows (oldest first, not newest first) but changes no field's value on any row.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, handleListTraffic(lb))

	sdk.AddTool(s, &sdk.Tool{
		Name: "override_from_traffic",
		Description: "Converts one observed traffic row into a pinned override on the operation it matched, " +
			"so future requests replay exactly what this row's response was. Use this right after spotting " +
			"an interesting response via list_traffic; it refuses — reporting the specific reason — any row " +
			"whose body was truncated, redacted or suppressed, or that never matched a spec operation, so " +
			"pick a different trafficId when it does.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, handleOverrideFromTraffic(lb))
}

// ---- set_session_directive ----

// SetSessionDirectiveInput is set_session_directive's input. The target
// union (livestate.Target: the bare string "*", or {method,path}) is
// flattened into three fields — All/Method/Path — rather than modeled as a
// nested oneOf, matching every other tool in this package's Input structs
// (flat fields, never a nested object the model has to construct itself).
//
// Deliberately absent: a "scenario" field. As of P2b, DESIGN §12's scenario
// switch has its own dedicated admin route (POST
// .../scenarios/{sid}/activate, .../scenarios/deactivate — internal/admin's
// scenario_handlers.go) and the session route this tool adapts answers 400
// for a "scenario" key rather than the 501 it used to (A17 of the P2b
// slice's own context document). The field stays absent anyway: adding it
// now would only let a client name a scenario at the wrong endpoint and get
// refused, which is no better than a field that could only ever 501 — see
// this tool's own Description.
type SetSessionDirectiveInput struct {
	WorkspaceID int64 `json:"workspaceId"`

	// ClearAll removes EVERY directive in force for the workspace (§C-e:
	// DELETE .../session clears all of them, not one) — named ClearAll
	// rather than "clear" precisely so nobody reads it as narrower than it
	// is. When true, every field below is ignored.
	ClearAll bool `json:"clearAll,omitempty"`
	// Clear (A13) removes the directives on the target below — every action
	// on it, or only Action when given — and releases a pause parked on it.
	// Status, Ms, Once and N are ignored.
	Clear bool `json:"clear,omitempty"`

	// Target — required unless ClearAll. All targets every operation
	// ("*" on the wire); otherwise both Method and Path must be set.
	All    bool   `json:"all,omitempty"`
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"` // relative path, no base path — e.g. "/widgets/{id}"

	// Action — required unless ClearAll: one of "status", "fail", "delay",
	// "pause" (livestate.Action's own four values, §C-f). Status/Ms/Once/N
	// are validated by the admin route itself (which action needs which of
	// them, and their bounds) — this tool passes them through and surfaces
	// that route's own 400 message verbatim on a bad combination, rather
	// than re-deriving internal/livestate's validation client-side.
	Action string `json:"action,omitempty"`
	Status int    `json:"status,omitempty"` // status|fail only, 100..599
	Ms     int    `json:"ms,omitempty"`     // delay only, 1..30000
	Once   bool   `json:"once,omitempty"`   // fail only: fire exactly once
	N      int    `json:"n,omitempty"`      // fail only: how many requests to fail
}

// SessionTarget is one directive's target, projected from the wire's union
// (the bare string "*", or {method,path}) into an explicit All flag plus
// Method/Path — the same flattening SetSessionDirectiveInput uses on the
// way in, so a caller reads back exactly the shape it can resend.
type SessionTarget struct {
	All    bool   `json:"all,omitempty"`
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
}

// SessionDirectiveLine is one directive as GET/POST .../session reports it.
// Status/Ms/Once/N carry omitempty here even though the wire's own
// livestate.Directive.Once/N do not (livestate.go's own comment: they are
// always present on that type's wire form) — this is a compaction choice on
// the way OUT, not a fidelity gap: a "fail" directive's Once/N are
// meaningless noise on a "delay" or "pause" line, and omitting a genuine
// zero/false loses nothing a reader could have acted on.
type SessionDirectiveLine struct {
	Target SessionTarget `json:"target"`
	Action string        `json:"action"`
	Status int           `json:"status,omitempty"`
	Ms     int           `json:"ms,omitempty"`
	Once   bool          `json:"once,omitempty"`
	N      int           `json:"n,omitempty"`
	SetAt  string        `json:"setAt"`
}

// SetSessionDirectiveOutput is set_session_directive's declared output
// schema: the directive list now in force (§C's table row 7), empty after a
// ClearAll by definition (§C-e — there is nothing left to list). Cleared is
// populated only by the ClearAll path (DELETE's own wire shape has no
// directive list to echo back, only a count); omitempty keeps it out of a
// POST's response entirely rather than reporting a fabricated 0.
type SetSessionDirectiveOutput struct {
	Directives []SessionDirectiveLine `json:"directives"`
	Cleared    int                    `json:"cleared,omitempty"`
}

// sessionDirectiveWire decodes one entry of sessionListView.Directives
// (internal/admin/livestate_handlers.go:75-77 — whose own field type is
// []livestate.Directive, and livestate.Directive's doc comment is explicit
// that the wire shape belongs to THAT package, not to any one handler,
// because two decoders — this admin route and the mock plane's own POST
// {prefix}/state — must never drift on it). Target is kept as raw JSON
// because it is a union on the wire (livestate.Target.MarshalJSON: the bare
// string "*", or {"method","path"}); decodeSessionTarget below resolves it
// without this package importing internal/livestate.Target's own
// UnmarshalJSON, which reading a value this package never re-encodes into a
// livestate type has no need of.
type sessionDirectiveWire struct {
	Target jsonx.RawMessage `json:"target"`
	Action string           `json:"action"`
	Status int              `json:"status,omitempty"`
	Ms     int              `json:"ms,omitempty"`
	Once   bool             `json:"once"`
	N      int              `json:"n"`
	SetAt  string           `json:"setAt"`
}

// sessionListWire decodes sessionListView (livestate_handlers.go:75-77) —
// GET and a successful POST's shared wire shape.
type sessionListWire struct {
	Directives []sessionDirectiveWire `json:"directives"`
}

// sessionClearedWire decodes sessionClearedView (livestate_handlers.go:80-82)
// — DELETE's wire shape.
type sessionClearedWire struct {
	Cleared int `json:"cleared"`
}

// decodeSessionTarget resolves one directive's raw target JSON into
// SessionTarget. The wire only ever produces two shapes — the bare string
// "*", or {"method","path"} — because livestate.Target.UnmarshalJSON
// rejects everything else before a directive is ever stored
// (livestate.go:84-103), so anything this function cannot parse as one of
// those two is an admin-side wire change this tool has not been taught
// about yet, not a caller mistake.
func decodeSessionTarget(raw jsonx.RawMessage) (SessionTarget, error) {
	var s string
	if err := jsonx.Unmarshal(raw, &s); err == nil {
		if s != "*" {
			return SessionTarget{}, fmt.Errorf("mcp: unexpected session target string %q (want \"*\")", s)
		}
		return SessionTarget{All: true}, nil
	}
	var obj struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	if err := jsonx.Unmarshal(raw, &obj); err != nil {
		return SessionTarget{}, fmt.Errorf("mcp: decode session target %s: %w", raw, err)
	}
	return SessionTarget{Method: obj.Method, Path: obj.Path}, nil
}

// encodeSessionTarget builds the exact wire shape
// livestate.Target.MarshalJSON itself produces (livestate.go:70-78) from
// this tool's own flat All/Method/Path input fields — the bare string "*"
// when All, {"method","path"} otherwise — so the outbound POST body decodes
// cleanly through livestate.Target.UnmarshalJSON without this package
// importing internal/livestate at all (see this file's own header comment).
func encodeSessionTarget(all bool, method, path string) (jsonx.RawMessage, error) {
	if all {
		b, err := jsonx.Marshal("*")
		return b, err
	}
	b, err := jsonx.Marshal(struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}{Method: method, Path: path})
	return b, err
}

// toDirectiveLine projects one decoded wire directive into the tool's own
// output shape.
func toDirectiveLine(w sessionDirectiveWire) (SessionDirectiveLine, error) {
	target, err := decodeSessionTarget(w.Target)
	if err != nil {
		return SessionDirectiveLine{}, err
	}
	return SessionDirectiveLine{
		Target: target,
		Action: w.Action,
		Status: w.Status,
		Ms:     w.Ms,
		Once:   w.Once,
		N:      w.N,
		SetAt:  w.SetAt,
	}, nil
}

// directivePostBody is the request body POST .../session accepts: the same
// Target/Action/Status/Ms/Once/N shape livestate.Directive itself marshals
// to. SetAt is server-assigned and never sent; Scenario is never sent at
// all (see SetSessionDirectiveInput's own doc comment on why this tool
// offers no such field).
type directivePostBody struct {
	Target jsonx.RawMessage `json:"target"`
	Action string           `json:"action"`
	Status int              `json:"status,omitempty"`
	Ms     int              `json:"ms,omitempty"`
	Once   bool             `json:"once,omitempty"`
	N      int              `json:"n,omitempty"`
}

func handleSetSessionDirective(lb *loopback) sdk.ToolHandlerFor[SetSessionDirectiveInput, SetSessionDirectiveOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in SetSessionDirectiveInput) (*sdk.CallToolResult, SetSessionDirectiveOutput, error) {
		if in.ClearAll {
			// §C-e: DELETE clears EVERY directive for the workspace — there
			// is nothing else this call could mean, so every other field on
			// in is ignored rather than validated. The resulting directive
			// list is empty by definition: no second GET is needed to know
			// that.
			var wire sessionClearedWire
			method, path := toolPath("set_session_directive", "DELETE /api/workspaces/{id}/session", in.WorkspaceID)
			if err := lb.call(ctx, method, path, nil, &wire); err != nil {
				return nil, SetSessionDirectiveOutput{}, err
			}
			return nil, SetSessionDirectiveOutput{Directives: []SessionDirectiveLine{}, Cleared: wire.Cleared}, nil
		}

		// Cheaper, earlier feedback than round-tripping to the admin
		// route's own 400 (mirrors CreateWorkspaceInput's own comment in
		// tools_ops.go on why a required-shaped check belongs here): Action
		// has no schema-level "required" (it must stay optional so a
		// ClearAll-only call validates), so this tool enforces it itself
		// for the one case that actually needs it.
		if in.Action == "" && !in.Clear {
			return nil, SetSessionDirectiveOutput{}, fmt.Errorf(
				"action is required unless clearAll or clear is set (one of: status, fail, delay, pause)")
		}
		if !in.All && (in.Method == "" || in.Path == "") {
			return nil, SetSessionDirectiveOutput{}, fmt.Errorf(
				"target must be either all:true, or both method and path")
		}

		target, err := encodeSessionTarget(in.All, in.Method, in.Path)
		if err != nil {
			return nil, SetSessionDirectiveOutput{}, fmt.Errorf("mcp: encode session target: %w", err)
		}
		if in.Clear {
			// A13: the same DELETE with a body — one target, optionally one
			// action. The list answered is what remains.
			body, err := jsonx.Marshal(struct {
				Target jsonx.RawMessage `json:"target"`
				Action string           `json:"action,omitempty"`
			}{target, in.Action})
			if err != nil {
				return nil, SetSessionDirectiveOutput{}, fmt.Errorf("mcp: encode session clear: %w", err)
			}
			var cleared sessionClearedWire
			method, path := toolPath("set_session_directive", "DELETE /api/workspaces/{id}/session", in.WorkspaceID)
			if err := lb.call(ctx, method, path, body, &cleared); err != nil {
				return nil, SetSessionDirectiveOutput{}, err
			}
			var wire sessionListWire
			method, path = toolPath("set_session_directive", "GET /api/workspaces/{id}/session", in.WorkspaceID)
			if err := lb.call(ctx, method, path, nil, &wire); err != nil {
				return nil, SetSessionDirectiveOutput{}, err
			}
			lines := make([]SessionDirectiveLine, len(wire.Directives))
			for i, d := range wire.Directives {
				line, err := toDirectiveLine(d)
				if err != nil {
					return nil, SetSessionDirectiveOutput{}, err
				}
				lines[i] = line
			}
			return nil, SetSessionDirectiveOutput{Directives: lines, Cleared: cleared.Cleared}, nil
		}
		body, err := jsonx.Marshal(directivePostBody{
			Target: target, Action: in.Action, Status: in.Status, Ms: in.Ms, Once: in.Once, N: in.N,
		})
		if err != nil {
			return nil, SetSessionDirectiveOutput{}, fmt.Errorf("mcp: encode session directive: %w", err)
		}

		var wire sessionListWire
		method, path := toolPath("set_session_directive", "POST /api/workspaces/{id}/session", in.WorkspaceID)
		if err := lb.call(ctx, method, path, body, &wire); err != nil {
			return nil, SetSessionDirectiveOutput{}, err
		}

		lines := make([]SessionDirectiveLine, len(wire.Directives))
		for i, d := range wire.Directives {
			line, err := toDirectiveLine(d)
			if err != nil {
				return nil, SetSessionDirectiveOutput{}, err
			}
			lines[i] = line
		}
		return nil, SetSessionDirectiveOutput{Directives: lines}, nil
	}
}

// ---- list_traffic ----

// ListTrafficInput is list_traffic's input. TrafficID switches this tool
// from a page of lines to a single entry WITH its bodies (§C7) — a pointer
// with omitempty because "no trafficId" and "trafficId 0" are different
// requests (the same reasoning ListSpecsInput.SpecID's own comment gives).
//
// Since is A4's own addition (decisions.md mocker-a4-mcp-reach D6): a
// pointer for the identical reason TrafficID is one — "no since" (the
// plain, newest-first list this tool has always answered) and "since 0"
// (an incremental read from the very beginning of the table) are different
// requests, and only a pointer lets this struct tell them apart. When set,
// it takes priority over the plain-list path the same way TrafficID takes
// priority over Since (checked first in handleListTraffic) — a caller
// combining trafficId with since is asking for one specific entry, and
// Since would be meaningless beside it.
type ListTrafficInput struct {
	WorkspaceID int64  `json:"workspaceId"`
	Limit       int    `json:"limit,omitempty"`
	TrafficID   *int64 `json:"trafficId,omitempty"`
	Since       *int64 `json:"since,omitempty" jsonschema:"Optional cursor: a traffic row id from a previous call's lastId. When set, only rows with a strictly greater id are returned, oldest first, instead of the plain newest-first page."`
}

// TrafficLine is one traffic row. ReqHeaders/ReqBody/RespBody are populated
// ONLY by the single-entry fetch (TrafficID set) — never by the plain list
// — which is §C's compaction rule made structural rather than a promise: a
// dedicated test marshals this type's output with no TrafficID and asserts
// none of those three keys appear at all.
type TrafficLine struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	// DurationMs, not omitempty: a genuinely instant (0ms) response is real
	// information, not an absent field.
	DurationMs  float64 `json:"durationMs"`
	MatchedKind string  `json:"matchedKind"`
	MatchedID   *int64  `json:"matchedId,omitempty"`
	// Truncated is per-ROW: THIS entry's own body was cut by the
	// recorder's own MaxBody bound (internal/traffic/recorder.go:139;
	// Row.Truncated's own doc comment, traffic.go:133-135). It is NOT the
	// list's own cap — that is ListTrafficOutput's Returned/Limit/HasMore —
	// and the tool's own Description spells out the distinction because
	// find_operations (tools_ops.go) publishes a "truncated" with the
	// OTHER meaning (the list was capped) and a model reading both tools'
	// output needs to know they disagree.
	Truncated bool `json:"truncated"`
	// Notes are traffic.Row's own pinned tokens (NoteRedacted,
	// NoteTruncatedReq, NoteTruncatedRsp, NoteSuppressed, "dropped:N" —
	// traffic.go:143-149), split for easy scanning rather than left as one
	// comma-joined string. This is what lets a caller see an
	// override_from_traffic refusal (§C-d) coming before ever calling it.
	Notes []string `json:"notes,omitempty"`

	ReqHeaders map[string]string `json:"reqHeaders,omitempty"`
	ReqBody    string            `json:"reqBody,omitempty"`
	RespBody   string            `json:"respBody,omitempty"`
}

// ListTrafficOutput is list_traffic's declared output schema. Returned,
// Limit and HasMore carry NO omitempty, matching FindOperationsOutput's own
// Truncated in tools_ops.go: a false HasMore is exactly as meaningful as a
// true one, and omitempty would silently drop it from the wire whenever
// there happened to be no more rows — indistinguishable, to a model reading
// raw JSON, from the field never having existed. §C's compaction rule: an
// exact Total is not obtainable here the way it is for find_operations
// (GET .../traffic clamps and reports no count of what it did not send —
// traffic_handlers.go:70), so this tool reports what IS knowable — the page
// it asked for and got — rather than inventing a population count it does
// not have.
//
// LastID is A4's own addition (D6), also with NO omitempty for the
// identical reason: 0 (an empty table, or Since itself was 0 with nothing
// new since) is exactly as meaningful as a nonzero cursor. It is populated
// on EVERY path this tool answers through — the plain list, the since-page
// and the single-entry fetch — never only the new one, because a caller
// chaining calls should not have to branch on which shape of the previous
// response it got.
type ListTrafficOutput struct {
	Rows     []TrafficLine `json:"rows"`
	Returned int           `json:"returned"`
	Limit    int           `json:"limit"`
	HasMore  bool          `json:"hasMore"`
	LastID   int64         `json:"lastId"`
}

// trafficRowWire decodes the fields of traffic.Row (internal/traffic/
// traffic.go:115-131) this tool actually projects — TS, PeerIP and FwdIP
// are simply absent, a safe subset exactly like workspaceWire's own comment
// in tools_ops.go describes, since jsonx (via encoding/json) ignores JSON
// keys with no matching Go field. It is the SAME wire shape both GET
// .../traffic and GET .../traffic/poll answer (trafficListView.Rows,
// trafficPollView.Rows — traffic_handlers.go:71,81 — both marshal
// traffic.Row directly), so one wire type covers both routes.
type trafficRowWire struct {
	ID          int64             `json:"id"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	MatchedKind string            `json:"matchedKind"`
	MatchedID   *int64            `json:"matchedId,omitempty"`
	Status      int               `json:"status"`
	DurationMS  float64           `json:"durationMs"`
	ReqHeaders  map[string]string `json:"reqHeaders,omitempty"`
	ReqBody     string            `json:"reqBody,omitempty"`
	RespBody    string            `json:"respBody,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	Truncated   bool              `json:"truncated"`
}

// trafficListWire decodes trafficListView (traffic_handlers.go:70-74).
// Rate1m and Dropped are absent — §C's table lists this tool's return
// projection exhaustively and neither is in it.
type trafficListWire struct {
	Rows []trafficRowWire `json:"rows"`
}

// trafficPollWire decodes trafficPollView (traffic_handlers.go:80-84).
// Dropped is absent for the same reason trafficListWire omits
// Rate1m/Dropped. LastID is present as of A4 (D6) — fetchOneTrafficRow's
// own single-entry path never reads it (that path already knows the id it
// asked for matched), but handleListTraffic's new since-branch does, and
// one wire type still covers both callers of this route.
type trafficPollWire struct {
	Rows   []trafficRowWire `json:"rows"`
	LastID int64            `json:"lastId"`
}

// newTrafficLine projects row into a TrafficLine with NO bodies or
// headers — the caller (handleListTraffic) sets those three fields itself,
// and only on the single-entry path, so this constructor cannot be
// accidentally reused into leaking them onto every row of a plain list.
func newTrafficLine(row trafficRowWire) TrafficLine {
	return TrafficLine{
		ID:          row.ID,
		Method:      row.Method,
		Path:        row.Path,
		Status:      row.Status,
		DurationMs:  row.DurationMS,
		MatchedKind: row.MatchedKind,
		MatchedID:   row.MatchedID,
		Truncated:   row.Truncated,
		Notes:       splitNotes(row.Notes),
	}
}

// splitNotes turns traffic.Row's comma-separated Notes column into a slice
// a model can scan or match against directly, without re-deriving
// traffic.Row.HasNote's own split rule (traffic.go:157-164) a second time.
// An empty string yields a nil slice — omitempty on TrafficLine.Notes then
// drops the key entirely, rather than publishing a slice holding one empty
// string.
func splitNotes(notes string) []string {
	if notes == "" {
		return nil
	}
	return strings.Split(notes, ",")
}

// fetchOneTrafficRow is §C7's one careful composition, spelled out in full
// because it is the one place in this file that can silently return the
// WRONG row. There is no GET /api/workspaces/{id}/traffic/{tid} route at
// all (traffic.Repo.Get exists, internal/traffic/repo.go:66, but nothing in
// internal/admin exposes it as a route — §A5, no new routes this slice may
// add), so the only way to fetch one entry WITH its bodies is
// GET .../traffic/poll?since=<trafficID-1>&limit=1 — and traffic.Repo.Since
// returns rows with id > since, OLDEST FIRST (repo.go:53). If trafficID
// itself was pruned (deleted, or aged out past cfg.TrafficRetention), that
// call does NOT come back empty: it silently returns the NEXT surviving row
// instead, because "id > since" is satisfied by any later row. A caller
// that trusted the response's shape alone — "poll returned exactly one row,
// so that must be the one I asked for" — would report a stranger's request
// as the one the model asked about. The id comparison below is the entire
// fix: report "no such entry" whenever the returned row's id does not match
// exactly, including when nothing came back at all.
func fetchOneTrafficRow(ctx context.Context, lb *loopback, workspaceID, trafficID int64) (trafficRowWire, error) {
	method, base := toolPath("list_traffic", "GET /api/workspaces/{id}/traffic/poll", workspaceID)
	path := fmt.Sprintf("%s?since=%d&limit=1", base, trafficID-1)
	var wire trafficPollWire
	if err := lb.call(ctx, method, path, nil, &wire); err != nil {
		return trafficRowWire{}, err
	}
	if len(wire.Rows) == 0 || wire.Rows[0].ID != trafficID {
		return trafficRowWire{}, fmt.Errorf(
			"no such entry: traffic id %d not found in workspace %d (it may have been pruned)", trafficID, workspaceID)
	}
	return wire.Rows[0], nil
}

func handleListTraffic(lb *loopback) sdk.ToolHandlerFor[ListTrafficInput, ListTrafficOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in ListTrafficInput) (*sdk.CallToolResult, ListTrafficOutput, error) {
		if in.TrafficID != nil {
			row, err := fetchOneTrafficRow(ctx, lb, in.WorkspaceID, *in.TrafficID)
			if err != nil {
				return nil, ListTrafficOutput{}, err
			}
			line := newTrafficLine(row)
			// Bodies and headers are set ONLY here, on the single-entry
			// path — exactly the one case §C names as the exception to
			// "returns no response body by default".
			line.ReqHeaders = row.ReqHeaders
			line.ReqBody = row.ReqBody
			line.RespBody = row.RespBody
			// LastID is the row's own id: fetchOneTrafficRow already
			// verified it matches *in.TrafficID exactly, so this cursor is
			// "everything up to and including the entry just fetched",
			// consistent with what LastID means on the other two paths.
			return nil, ListTrafficOutput{Rows: []TrafficLine{line}, Returned: 1, Limit: 1, HasMore: false, LastID: row.ID}, nil
		}

		limit := listTrafficDefaultLimit
		if in.Limit > 0 {
			limit = in.Limit
		}
		if limit > listTrafficMaxLimit {
			limit = listTrafficMaxLimit
		}

		// A4 (D6): Since set switches this tool onto GET .../traffic/poll,
		// the route that has always had a real incremental cursor keyed on
		// the traffic row id (traffic_handlers.go:131-158) — never onto a
		// since-flavored variant of GET .../traffic, which trafficListView
		// has no cursor field for at all. Rows on this path come back
		// OLDEST first (traffic.Repo.Since's own order) and carry rate1m
		// nowhere, both properties of the route being called, not of this
		// branch reordering anything.
		if in.Since != nil {
			method, base := toolPath("list_traffic", "GET /api/workspaces/{id}/traffic/poll", in.WorkspaceID)
			path := fmt.Sprintf("%s?since=%d&limit=%d", base, *in.Since, limit)
			var wire trafficPollWire
			if err := lb.call(ctx, method, path, nil, &wire); err != nil {
				return nil, ListTrafficOutput{}, err
			}

			lines := make([]TrafficLine, len(wire.Rows))
			for i, row := range wire.Rows {
				lines[i] = newTrafficLine(row)
			}
			return nil, ListTrafficOutput{
				Rows:     lines,
				Returned: len(lines),
				Limit:    limit,
				HasMore:  len(lines) == limit,
				LastID:   wire.LastID,
			}, nil
		}

		method, base := toolPath("list_traffic", "GET /api/workspaces/{id}/traffic", in.WorkspaceID)
		path := fmt.Sprintf("%s?limit=%d", base, limit)
		var wire trafficListWire
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, ListTrafficOutput{}, err
		}

		lines := make([]TrafficLine, len(wire.Rows))
		for i, row := range wire.Rows {
			// newTrafficLine ONLY — never touching ReqBody/RespBody/
			// ReqHeaders here is what keeps a plain list call from ever
			// carrying a body, even though the admin route's own row
			// carries them (§C's compaction rule; TestListTraffic_
			// noBodyFields marshals this exact output to prove it).
			lines[i] = newTrafficLine(row)
		}

		// A4 (D6): the no-since path calls GET .../traffic, whose
		// trafficListView carries no LastID field at all — only
		// trafficPollView does. wire.Rows is NEWEST first (handleListTraffic's
		// own doc comment on trafficListView), so the cheap correct
		// derivation is the FIRST row's id, not the last; an empty page
		// answers 0, the same "nothing to chain from yet" value Since:0
		// would answer against an empty table.
		var lastID int64
		if len(lines) > 0 {
			lastID = wire.Rows[0].ID
		}

		return nil, ListTrafficOutput{
			Rows:     lines,
			Returned: len(lines),
			Limit:    limit,
			HasMore:  len(lines) == limit,
			LastID:   lastID,
		}, nil
	}
}

// ---- override_from_traffic ----

// OverrideFromTrafficInput is override_from_traffic's input.
type OverrideFromTrafficInput struct {
	WorkspaceID int64 `json:"workspaceId"`
	TrafficID   int64 `json:"trafficId"`
}

// OverrideFromTrafficOutput is override_from_traffic's declared output
// schema, projected from toOverrideView (internal/admin/from_traffic.go:72-76).
type OverrideFromTrafficOutput struct {
	OpKey    string `json:"opKey"`
	Status   int    `json:"status"`
	Revision int64  `json:"revision"`
}

// toOverrideWire decodes toOverrideView (from_traffic.go:72-76) — the
// success shape of POST .../traffic/{tid}/to-override.
type toOverrideWire struct {
	OpKey    string `json:"opKey"`
	Status   int    `json:"status"`
	Revision int64  `json:"revision"`
}

func handleOverrideFromTraffic(lb *loopback) sdk.ToolHandlerFor[OverrideFromTrafficInput, OverrideFromTrafficOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in OverrideFromTrafficInput) (*sdk.CallToolResult, OverrideFromTrafficOutput, error) {
		method, path := toolPath("override_from_traffic", "POST /api/workspaces/{id}/traffic/{tid}/to-override", in.WorkspaceID, in.TrafficID)

		var wire toOverrideWire
		// lb.call, not lb.do: every non-2xx here is genuinely a tool error
		// to report, never a status this handler must branch on itself —
		// 404 (no such traffic row), 409 (§C-d's refusal: the row's body
		// was truncated, redacted or suppressed, or it matched nothing to
		// pin to — from_traffic.go:114,121,130,135,140), 500. toolErr
		// (loopback_client.go) already surfaces a 4xx's own
		// httpx.ErrorBody.Error.Message verbatim, which IS the refusal
		// reason §C-d asks this tool to project so the model can pick a
		// different trafficId — no special-casing of 409 needed here.
		if err := lb.call(ctx, method, path, nil, &wire); err != nil {
			return nil, OverrideFromTrafficOutput{}, err
		}
		// toOverrideWire and OverrideFromTrafficOutput share an identical
		// field set, so this is a plain type conversion rather than a
		// field-by-field copy (staticcheck S1016) — exactly the same move
		// tools_ops.go's handleGetOperation makes for mergedStatusWire ->
		// MergedStatus, and for the same reason: the compiler refuses to
		// compile this line the day the two shapes diverge.
		return nil, OverrideFromTrafficOutput(wire), nil
	}
}

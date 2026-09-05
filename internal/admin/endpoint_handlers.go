// Endpoint handlers implement DESIGN §14 screen 6's custom-endpoint CRUD,
// including the PUT editor A1 of the mocker-a-mcp gate document adds:
// routes an operator defines from nothing
// rather than importing from a spec. Every write goes through
// [customep.Repo], which owns HARD RULE 5's own transaction
// (custom_endpoints write + workspaces.revision bump, one db.Write) exactly
// like [overrides.Repo] does for op_overrides — nothing here bumps the
// revision itself, and nothing here re-validates a Variant beyond what
// [customep.Row]'s own decode path already does (the same
// overrides.ValidateResponses gate op_overrides writes through — see
// customep's own package doc comment for why there is exactly one
// implementation, not two).
//
// The two conversions that build a customep.Row FROM an observed traffic
// row — "create an endpoint from this request" — live in from_traffic.go,
// not here: they share this file's wire shape ([endpointView]) but resolve
// their own body/status, so keeping them in one file would blur which half
// is "an operator typed this" and which half is "the plane observed this".
package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
)

// endpointView is the wire shape of one custom_endpoints row — GET's list
// entries and POST's created-row response share it, since both describe the
// same row and nothing here has a reason for them to drift apart.
type endpointView struct {
	ID            int64                        `json:"id"`
	Method        string                       `json:"method"`
	Path          string                       `json:"path"`
	CanonicalPath string                       `json:"canonicalPath"`
	OverrideOn    bool                         `json:"overrideOn"`
	RouteOff      bool                         `json:"routeOff"`
	ActiveStatus  int                          `json:"activeStatus"`
	Responses     map[string]overrides.Variant `json:"responses"`
	ListSize      *overrides.ListSize          `json:"listSize,omitempty"`
	DelayMs       *int                         `json:"delayMs,omitempty"`
	// Kind and Stream are P6b's (decisions.md mocker-p6b-sse-mock D3, D6):
	// "http" for every row that predates them, "sse" for a stream, whose
	// document rides in Stream and is absent for an http row.
	Kind   string           `json:"kind"`
	Stream *customep.Stream `json:"stream,omitempty"`
	// ReqSchema and Operation are P7a's (DESIGN §34.3): the request
	// schema (preserved since P1c-2, validated and exported since P7a)
	// and the OpenAPI operation fields the contract carries.
	ReqSchema jsonx.RawMessage    `json:"reqSchema,omitempty"`
	Operation *customep.Operation `json:"operation,omitempty"`
	CreatedAt int64               `json:"createdAt"`
	UpdatedAt int64               `json:"updatedAt"`
	// EditVersion is A3's per-row compare-and-swap token (D5): this one wire
	// type is GET's list entry, POST's created-row response AND PUT's write
	// response, so growing it here covers all three at once — D5's "a
	// caller can write twice without re-reading" promise for the PUT route.
	EditVersion int64 `json:"editVersion"`
}

func newEndpointView(row *customep.Row) endpointView {
	return endpointView{
		ID:            row.ID,
		Method:        row.Method,
		Path:          row.Path,
		CanonicalPath: row.CanonicalPath,
		OverrideOn:    row.OverrideOn,
		RouteOff:      row.RouteOff,
		ActiveStatus:  row.ActiveStatus,
		Responses:     row.Responses,
		ListSize:      row.ListSize,
		DelayMs:       row.DelayMs,
		Kind:          row.Kind,
		Stream:        row.Stream,
		ReqSchema:     row.ReqSchema,
		Operation:     row.Operation,
		CreatedAt:     row.CreatedAt.Unix(),
		UpdatedAt:     row.UpdatedAt.Unix(),
		EditVersion:   row.EditVersion,
	}
}

// endpointListView is GET's wire shape.
type endpointListView struct {
	Endpoints []endpointView `json:"endpoints"`
}

// handleListEndpoints answers GET /api/workspaces/{id}/endpoints: every
// custom endpoint of the workspace, in the same source_order/id order the
// route table itself would build them in (DESIGN §8 rule 4).
func (s *Server) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	rows, err := s.customepRepo.ForWorkspace(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("list endpoints", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list endpoints")
		return
	}
	out := make([]endpointView, len(rows))
	for i, row := range rows {
		out[i] = newEndpointView(row)
	}
	httpx.JSON(w, http.StatusOK, endpointListView{Endpoints: out})
}

// createEndpointRequest is POST's request body: an operator writing a route
// and its single response from scratch. A zero Status defaults to 200
// (below) rather than being rejected — an operator building a "happy path"
// stub has no reason to type the most common status by hand.
type createEndpointRequest struct {
	Method    string           `json:"method"`
	Path      string           `json:"path"`
	Status    int              `json:"status"`
	Body      jsonx.RawMessage `json:"body"`
	MediaType string           `json:"mediaType"`
	// BodyRef is A6's "asset:<name>" (DESIGN §32.3): the endpoint's pinned
	// response IS that uploaded asset, served verbatim under its stored
	// type — exclusive with body and mediaType, refused by name beside
	// them, the same rule overrides.ValidateVariant applies to the variant
	// this builds. The PUT editor takes the variant whole (responses[]),
	// so it needs no field of its own.
	BodyRef string `json:"bodyRef,omitempty"`
	// Kind and Stream are P6b's (D3, D6): omitted kind is "http" and the
	// row is exactly what it was before; "sse" requires method GET, no
	// status/body/mediaType (a stream has no single response) and a Stream
	// document, all refused by name through customep's one validator.
	Kind   string           `json:"kind,omitempty"`
	Stream jsonx.RawMessage `json:"stream,omitempty"`
	// Schema, ReqSchema and Operation are P7a's (DESIGN §34.3): the
	// response's inline JSON Schema (generated instead of a pinned body —
	// exclusive with neither, §8's mode rule decides which serves), the
	// request body's schema, and the OpenAPI operation fields the export
	// writes. Every `$ref` in any of them must resolve against the bound
	// spec at write time (D6).
	Schema    jsonx.RawMessage    `json:"schema,omitempty"`
	ReqSchema jsonx.RawMessage    `json:"reqSchema,omitempty"`
	Operation *customep.Operation `json:"operation,omitempty"`
	// Function is A18's (D5, D8): the Lua that PRODUCES this endpoint's
	// response instead of one being assembled for it. It needs a field of
	// its own here for the same reason BodyRef does — this request builds
	// ONE variant from flat fields, where the PUT editor takes the variant
	// whole through responses[] and gets the field off overrides.Variant.
	// Every refusal is the variant validator's: exclusive with body,
	// bodyRef, mediaType, recipes and schemaPatch, refused by name on a
	// stream row, and compiled at write time.
	Function string `json:"function,omitempty"`
}

// codeOperationIDTaken is P7a's 409 (decisions.md D3): the submitted
// operation.operationId is already held by another custom row of the
// workspace or by an operation of the bound spec, and the export cannot
// write two operations under one id.
const codeOperationIDTaken = "operation_id_taken"

// endpointRowFromCreate builds the row a POST creates, by kind. A stream
// row (P6b/P6d): no pinned response at all — the shape (GET, no responses,
// status 200) is customep's D6 rule, refused by name through Create, the
// same validator the preview route and the MCP tool reach — and since P7a
// no schema or reqSchema either (a stream has no JSON body to describe),
// while its `operation` travels exactly as an http row's does, because a
// stream row IS an operation in the export (D7.6). Any other kind —
// "http", omitted, a typo — builds the ordinary row and lets customep
// refuse it by name where it must. The error is the 400's message.
func endpointRowFromCreate(body *createEndpointRequest, status int, maxFrameBytes int64) (*customep.Row, error) {
	if body.Kind != customep.KindSSE && body.Kind != customep.KindWS {
		return httpEndpointRowFromCreate(body, status)
	}
	if body.Status != 0 || len(body.Body) > 0 || body.MediaType != "" || len(body.Schema) > 0 || len(body.ReqSchema) > 0 {
		return nil, fmt.Errorf("kind %q takes no status, body or mediaType — nor a schema or reqSchema — a stream's frames come from its stream document", body.Kind)
	}
	if body.Function != "" {
		// A18 D5, refused HERE and not left to the variant validator: this
		// branch never builds a Responses map at all, so customep's own
		// function_on_stream check (which walks that map) would find
		// nothing and the field would be silently dropped. A stream's Lua
		// goes in the stream document, and the message says which field.
		// Refused under the same name the row validator gives
		// responses[].function on a stream (customep's
		// refuseFunctionOnStream), so both doors answer function_on_stream.
		return nil, fmt.Errorf("%w: %w: kind %q takes no function — a stream is not a request/response, and its Lua goes in stream.tick.lua or stream.onFrame instead",
			customep.ErrInvalidRow, customep.ErrFunctionOnStream, body.Kind)
	}
	draft, err := endpointRowFromDraft(body.Method, body.Path, body.Kind, body.Stream, maxFrameBytes)
	if err != nil {
		return nil, err
	}
	draft.Operation = body.Operation
	return draft, nil
}

// httpEndpointRowFromCreate builds the row a POST of kind http creates.
// P7a: the created variant is PINNED only when the caller gave it a body
// to pin (a body, or an asset reference). With a schema and no body it is
// generated — the whole point of §34.3 — and with neither it is the
// empty-bodied row create has always made. Split out of
// handleCreateEndpoint so the handler's branching stays the request's
// refusals and not the row's shape.
func httpEndpointRowFromCreate(body *createEndpointRequest, status int) (*customep.Row, error) {
	streamDoc, err := decodeStreamDoc(body.Stream)
	if err != nil {
		return nil, err
	}
	variant := overrides.Variant{
		Body: body.Body, MediaType: body.MediaType, BodyRef: body.BodyRef,
		Schema: body.Schema, Function: body.Function,
	}
	// A18: a function variant is NEITHER pinned nor generated — the function
	// replaces response assembly entirely (D5), and marking it "pinned"
	// would send the serve path looking for a body to pin. The mode stays
	// empty and the exclusivity checks below refuse every field that would
	// have made the old condition true, so this branch can only be reached
	// with a lone function.
	switch {
	case body.Function != "":
	case len(body.Body) > 0 || body.BodyRef != "" || len(body.Schema) == 0:
		variant.Mode = "pinned"
	}
	return &customep.Row{
		Method:       body.Method,
		Path:         body.Path,
		Kind:         body.Kind,
		Stream:       streamDoc,
		ActiveStatus: status,
		ReqSchema:    body.ReqSchema,
		Operation:    body.Operation,
		Responses: map[string]overrides.Variant{
			strconv.Itoa(status): variant,
		},
	}, nil
}

// defaultCreateEndpointStatus is what an omitted Status becomes.
const defaultCreateEndpointStatus = 200

// handleCreateEndpoint answers POST /api/workspaces/{id}/endpoints: an
// explicit endpoint an operator writes from nothing. [customep.Repo.Create]
// runs the same shape validation op_overrides writes through
// (overrides.ValidateResponses), so a malformed method/path/recipe surfaces
// here as [customep.ErrInvalidRow] rather than a panic three calls up.
func (s *Server) handleCreateEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	var body createEndpointRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	status := body.Status
	if status == 0 {
		status = defaultCreateEndpointStatus
	}
	// Same write-time guard PUT .../operations/{opKey} applies (override_
	// handlers.go's dangerousMediaType): a custom endpoint serves an
	// operator-chosen body under an operator-chosen Content-Type on the SAME
	// origin the admin session's cookie is sent to in path-routing mode,
	// exactly the risk that check exists to close off. This call was already
	// unqualified by mode; the override PUT's matching check has been brought
	// into line with it, so one rule now reads the same way at both write
	// boundaries and the message below is worded identically.
	if dangerousMediaType(body.MediaType) {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, fmt.Sprintf(
			"mediaType %q is browser-executable and cannot be stored", body.MediaType))
		return
	}
	if body.BodyRef != "" && (len(body.Body) > 0 || body.MediaType != "") {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"bodyRef and body/mediaType are exclusive: the asset's own bytes and type are served")
		return
	}

	// Round-1 review finding 3: from_traffic.go's handleToEndpoint already
	// refuses to create a custom endpoint at a (method, path) an op_overrides
	// row already occupies — the SAME cross-table rule DESIGN §8 states once
	// for both, since router.compareRoutes gives a custom route priority at
	// equal specificity and would silently strand that override's when[],
	// activeStatus, recipes and pinned body unreachable. Before this fix,
	// only the traffic-conversion path enforced it: an operator could get
	// the identical forbidden state by hand, through this manual create
	// route, with no error at all. handleUpdateEndpoint shares this exact
	// check (refuseIfOverrideConflict below) rather than restating it — an
	// edit that MOVES an endpoint onto such a key reaches the identical
	// state.
	if s.refuseIfOverrideConflict(w, r, ws.ID, body.Method, body.Path) {
		return
	}

	row, derr := endpointRowFromCreate(&body, status, s.customepRepo.MaxFrameBytes)
	if derr != nil {
		// refusalCode: a stream draft's named refusals (on_frame_and_echo,
		// tick_lua_and_schema, …) surface HERE, before the repo, and must
		// carry the same code the repo's own mapper gives them.
		httpx.Err(w, http.StatusBadRequest, refusalCode(derr), derr.Error())
		return
	}
	// P7a (D6): a `$ref` is never STORED dangling — checked against the
	// document the workspace is bound to right now, before the write.
	if s.refuseUnresolvedRefs(w, r, ws, row) {
		return
	}

	stored, err := s.customepRepo.Create(r.Context(), ws.ID, row)
	if err != nil {
		s.answerCreateEndpointError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, newEndpointView(stored))
}

// answerCreateEndpointError maps [customep.Repo.Create]'s error set to wire
// codes. Shared by handleCreateEndpoint and from_traffic.go's
// handleToEndpoint — both call Create and both need the same mapping,
// [customep.ErrConflict] above all: a caller must not see one 409 shape from
// the manual-create route and a different one from the traffic conversion.
func (s *Server) answerCreateEndpointError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, customep.ErrConflict):
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, customep.ErrOperationIDTaken):
		httpx.Err(w, http.StatusConflict, codeOperationIDTaken, err.Error())
	case errors.Is(err, customep.ErrWorkspaceNotFound):
		// A race with a concurrent workspace delete, not a client mistake —
		// loadWorkspace already confirmed existence moments earlier.
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, customep.ErrInvalidRow):
		httpx.Err(w, http.StatusBadRequest, refusalCode(err), err.Error())
	default:
		s.log.Error("create endpoint", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to create endpoint")
	}
}

// refuseIfOverrideConflict answers 409 and reports true when workspaceID
// already has an op_overrides row at (method, path) — the cross-table rule
// handleCreateEndpoint's own comment states in full (DESIGN §8:
// router.compareRoutes gives a custom route priority at equal specificity,
// which would silently strand that override's when[], activeStatus,
// recipes and pinned body). handleUpdateEndpoint shares it rather than
// restating it: an edit that MOVES an endpoint onto such a key reaches the
// identical forbidden state a brand-new create would, and customep.Repo
// cannot see the other table to refuse it itself. On a genuine failure to
// even check (as opposed to a found conflict), it answers 500 and still
// reports true — either way the caller has already been answered and must
// return without touching customep.Repo.
func (s *Server) refuseIfOverrideConflict(w http.ResponseWriter, r *http.Request, workspaceID int64, method, path string) bool {
	_, err := s.overridesRepo.Get(r.Context(), workspaceID, overrides.OpKey(method, path))
	switch {
	case err == nil:
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"an override already exists for this method and path; a custom endpoint would silently not serve")
		return true
	case errors.Is(err, overrides.ErrInvalidOpKey):
		// An empty method or a path without its leading slash reaches this
		// check BEFORE customep's own validation and used to come out of
		// the branch below as a 500 with an error log; it is the caller's
		// shape mistake and answers as one.
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "method must be non-empty and path must start with /")
		return true
	case !errors.Is(err, overrides.ErrNotFound):
		s.log.Error("check override conflict", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to check for a conflicting override")
		return true
	}
	return false
}

// updateEndpointRequest is PUT's request body: [endpointView] MINUS every
// server-owned field (id, canonicalPath, createdAt, updatedAt) — D4 of the
// mocker-a-mcp gate document names this shape explicitly and rejects the
// obvious alternative, [createEndpointRequest]'s narrower shape: a PUT that
// replaced the row with THAT shape would silently discard Responses, which
// is the whole of what a custom endpoint serves. PUT is a FULL REPLACEMENT
// of the endpoint's definition, not a partial merge — an operator (or an
// agent building this body) resends every field it wants kept, exactly as
// handlePutOperation already requires for op_overrides.
//
// OverrideOn and RouteOff are *bool, not bool, because "field omitted" must
// read differently from "field explicitly false", and a plain bool cannot
// express that distinction at all. An omitted overrideOn means true (the
// same default [customep.Repo.Create] hardcodes for a brand-new row,
// repo.go:104) and an omitted routeOff means false. The asymmetry is
// [customep.Repo.Create]'s own comment's, unchanged by moving from create to
// edit: a wrongly ENABLED endpoint serves and is visible in traffic, while a
// wrongly DISABLED one is a 404 indistinguishable from a route that was
// never created — so a client that omits overrideOn while editing an
// endpoint an operator had switched off silently switches it back on.
type updateEndpointRequest struct {
	Method       string                       `json:"method"`
	Path         string                       `json:"path"`
	OverrideOn   *bool                        `json:"overrideOn"`
	RouteOff     *bool                        `json:"routeOff"`
	ActiveStatus int                          `json:"activeStatus"`
	Responses    map[string]overrides.Variant `json:"responses"`
	ListSize     *overrides.ListSize          `json:"listSize,omitempty"`
	DelayMs      *int                         `json:"delayMs,omitempty"`
	// Kind and Stream (P6b D3, D6): a full replacement carries the kind
	// too — an omitted kind is "http", so a PUT that forgets it turns a
	// stream row back into an http row and is refused for carrying a
	// stream with kind http, never silently.
	Kind   string           `json:"kind,omitempty"`
	Stream jsonx.RawMessage `json:"stream,omitempty"`
	// ReqSchema and Operation are P7a's (DESIGN §34.3); the response
	// schema rides inside Responses, on the variant. A full replacement
	// carries them like every other field: omitted means cleared.
	ReqSchema jsonx.RawMessage    `json:"reqSchema,omitempty"`
	Operation *customep.Operation `json:"operation,omitempty"`
	// EditVersion is A3's REQUIRED compare-and-swap expectation (D10): a
	// nil pointer means the caller omitted the field and is rejected BY
	// NAME below, never treated as "no expectation" — that state exists
	// only at customep.Repo's own unguarded Update verb (D7), never on
	// this wire. Unlike op_overrides, 0 is REFUSED here rather than
	// meaningful (D7: a custom_endpoints row addressed by {eid} always
	// already exists), but that refusal is customep.Repo.UpdateExpecting's
	// job, not this handler's — the field stays a bare *int64 like the
	// other three single-object requests.
	EditVersion *int64 `json:"editVersion"`
}

// defaultUpdateEndpointOverrideOn and defaultUpdateEndpointRouteOff are what
// an omitted OverrideOn/RouteOff become — see [updateEndpointRequest]'s own
// doc comment for why the two defaults differ and why a *bool is what makes
// expressing "omitted" possible at all.
const (
	defaultUpdateEndpointOverrideOn = true
	defaultUpdateEndpointRouteOff   = false
)

// switches reads the two optional booleans of a full replacement with
// their creation-time defaults (A1's rule: an omitted overrideOn reads as
// true, an omitted routeOff as false).
func (b *updateEndpointRequest) switches() (overrideOn, routeOff bool) {
	overrideOn, routeOff = defaultUpdateEndpointOverrideOn, defaultUpdateEndpointRouteOff
	if b.OverrideOn != nil {
		overrideOn = *b.OverrideOn
	}
	if b.RouteOff != nil {
		routeOff = *b.RouteOff
	}
	return overrideOn, routeOff
}

// endpointConflictDetails is PUT .../endpoints/{eid}'s conflict payload
// (D6): every field [updateEndpointRequest] accepts on the way in, as the
// server currently holds it — plain bool rather than *bool, since a stored
// row has no "omitted" state to preserve — plus the version the server
// actually holds.
type endpointConflictDetails struct {
	Method       string                       `json:"method"`
	Path         string                       `json:"path"`
	OverrideOn   bool                         `json:"overrideOn"`
	RouteOff     bool                         `json:"routeOff"`
	ActiveStatus int                          `json:"activeStatus"`
	Responses    map[string]overrides.Variant `json:"responses"`
	ListSize     *overrides.ListSize          `json:"listSize,omitempty"`
	DelayMs      *int                         `json:"delayMs,omitempty"`
	// Kind and Stream (P6e): a stream row's current document is its stream,
	// and a conflict payload that omitted it could not seed the editor with
	// what the other writer saved — the reload would silently present the
	// operator's own stale draft as "the current version".
	Kind   string           `json:"kind"`
	Stream *customep.Stream `json:"stream,omitempty"`
	// ReqSchema and Operation (P7a): the same reason Kind/Stream are
	// here — a conflict payload that omitted them could not seed the
	// editor with what the other writer saved.
	ReqSchema   jsonx.RawMessage    `json:"reqSchema,omitempty"`
	Operation   *customep.Operation `json:"operation,omitempty"`
	EditVersion int64               `json:"editVersion"`
}

// answerEndpointEditConflict writes PUT .../endpoints/{eid}'s 409 for a lost
// compare-and-swap. conflict.Current is boxed by
// [customep.Repo.UpdateExpecting] as a plain customep.Row (never a pointer),
// so the type assertion below is the one place this handler translates the
// sentinel's untyped payload into the route's declared wire shape.
func (s *Server) answerEndpointEditConflict(w http.ResponseWriter, conflict *store.EditConflictError) {
	if conflict.Gone {
		httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
			"custom endpoint was deleted by another write", editConflictGone{Gone: true})
		return
	}
	row, ok := conflict.Current.(customep.Row)
	if !ok {
		s.log.Error("endpoint edit conflict: unexpected payload type", "type", fmt.Sprintf("%T", conflict.Current))
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to build conflict details")
		return
	}
	httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
		"custom endpoint was changed by another write",
		endpointConflictDetails{
			Method: row.Method, Path: row.Path, OverrideOn: row.OverrideOn, RouteOff: row.RouteOff,
			ActiveStatus: row.ActiveStatus, Responses: row.Responses, ListSize: row.ListSize, DelayMs: row.DelayMs,
			Kind: row.Kind, Stream: row.Stream,
			ReqSchema: row.ReqSchema, Operation: row.Operation,
			EditVersion: row.EditVersion,
		})
}

// handleUpdateEndpoint answers PUT /api/workspaces/{id}/endpoints/{eid}: a
// full replacement of one custom endpoint's definition. It runs the SAME
// validation handleCreateEndpoint runs — the browser-executable media-type
// guard (looped over every response here, since PUT's body carries a whole
// Responses map rather than create's single status/body/mediaType triple,
// the same shape difference handlePutOperation's own loop exists for) and
// the cross-table refusal against a conflicting op_overrides row
// (refuseIfOverrideConflict) — before ever reaching [customep.Repo.Update],
// which cannot see the other table.
//
// [customep.Repo.Update]'s mutate closure only assigns the fields this
// route's wire shape carries; FailDirective and ValidateReq (P2's
// PRESERVED-ONLY fields, set by no handler) come back untouched because
// mutate never mentions them and Update reads the current row before
// calling mutate rather than starting from a zero Row. ReqSchema left that
// group with P7a: it is on the wire now, and a PUT that omits it CLEARS
// it, like every other field of a full replacement — which is why the
// custom-endpoints screen passes the row's own reqSchema and operation
// back untouched on every edit.
func (s *Server) handleUpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	eid, ok := parsePathInt64(w, r, "eid")
	if !ok {
		return
	}

	var body updateEndpointRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if body.EditVersion == nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "editVersion is required")
		return
	}

	// Same write-time guard handleCreateEndpoint applies, generalized to a
	// map: PUT's body carries a whole Responses set, not create's single
	// status/body/mediaType triple, so every entry needs the check —
	// mirrors handlePutOperation's identical loop for op_overrides.
	for status, v := range body.Responses {
		if dangerousMediaType(v.MediaType) {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, fmt.Sprintf(
				"responses[%s]: mediaType %q is browser-executable and cannot be stored", status, v.MediaType))
			return
		}
	}

	if s.refuseIfOverrideConflict(w, r, ws.ID, body.Method, body.Path) {
		return
	}

	// P7a (D6): the submitted row's own schemas, against the bound spec,
	// before the compare-and-swap opens.
	if s.refuseUnresolvedRefs(w, r, ws, &customep.Row{
		Method: body.Method, Path: body.Path, Responses: body.Responses,
		ReqSchema: body.ReqSchema, Operation: body.Operation,
	}) {
		return
	}

	overrideOn, routeOff := body.switches()

	streamDoc, err := decodeStreamDoc(body.Stream)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}

	stored, err := s.customepRepo.UpdateExpecting(r.Context(), ws.ID, eid, body.EditVersion, func(cur *customep.Row) error {
		cur.Method = body.Method
		cur.Path = body.Path
		cur.OverrideOn = overrideOn
		cur.RouteOff = routeOff
		cur.ActiveStatus = body.ActiveStatus
		cur.Responses = body.Responses
		cur.ListSize = body.ListSize
		cur.DelayMs = body.DelayMs
		cur.Kind = body.Kind
		cur.Stream = streamDoc
		cur.ReqSchema = body.ReqSchema
		cur.Operation = body.Operation
		return nil
	})
	var conflict *store.EditConflictError
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, newEndpointView(stored))
	case errors.As(err, &conflict):
		s.answerEndpointEditConflict(w, conflict)
	case errors.Is(err, customep.ErrConflict):
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict, err.Error())
	case errors.Is(err, customep.ErrOperationIDTaken):
		httpx.Err(w, http.StatusConflict, codeOperationIDTaken, err.Error())
	case errors.Is(err, customep.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "endpoint not found")
	case errors.Is(err, customep.ErrWorkspaceNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, customep.ErrInvalidRow):
		httpx.Err(w, http.StatusBadRequest, refusalCode(err), err.Error())
	default:
		s.log.Error("update endpoint", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to update endpoint")
	}
}

// handleDeleteEndpoint answers DELETE /api/workspaces/{id}/endpoints/{eid}.
func (s *Server) handleDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}
	eid, ok := parsePathInt64(w, r, "eid")
	if !ok {
		return
	}

	err := s.customepRepo.Delete(r.Context(), ws.ID, eid)
	switch {
	case err == nil:
		httpx.NoContent(w)
	case errors.Is(err, customep.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "endpoint not found")
	case errors.Is(err, customep.ErrWorkspaceNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	default:
		s.log.Error("delete endpoint", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to delete endpoint")
	}
}

// parsePathInt64 extracts and validates the {name} path value as a positive
// int64 — the same shape parseWorkspaceID enforces for {id}, generalized for
// the two other numeric path segments this slice adds ({eid}, {tid}). Shared
// by this file and from_traffic.go rather than duplicated, since both need
// exactly this check and nothing route-specific.
func parsePathInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || v <= 0 {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid "+name)
		return 0, false
	}
	return v, true
}

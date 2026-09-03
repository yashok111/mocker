// Override handlers implement DESIGN §14's per-operation override surface
// (lines 828-862): a merged view of every operation in the workspace's spec
// next to whatever op_overrides row already exists for it, and singular
// GET/PUT/DELETE routes over one operation's row addressed by its OpKey.
//
// Every write here goes through [overrides.Repo], which owns HARD RULE 5's
// single-transaction revision bump (op_overrides write + workspaces.revision
// increment, one db.Write, never workspaces.Repo.Update from inside it) —
// nothing in this file writes op_overrides or workspaces.revision itself, and
// nothing here re-validates a recipe beyond what [overrides.Row]'s own decode
// path already does: that validation is the single gate both this handler's
// PUT and the auth-preset apply handler (preset_handlers.go) write through.
package admin

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonpatch"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// codeEditConflict is A3's wire code for a per-row compare-and-swap refusal
// (D6 of the mocker-a3-cas decision document): declared ONCE, here, and
// admin-locally rather than in internal/httpx — the tree's own precedent is
// preview_handlers.go's identical package-local const block for its five
// route-specific codes (codePreviewInvalidDraft and neighbours), and
// internal/httpx holds only the SHARED code set api/openapi.json and then
// web/src/api/errors.ts mirror. Five routes in this package emit this code;
// nothing outside internal/admin ever does.
const codeEditConflict = "edit_conflict"

// codeSchemaOnOverride is P7a's one new refusal on this route (D2): an
// inline response `schema` is a custom endpoint's field, never an
// override's.
const codeSchemaOnOverride = "schema_on_override"

// editConflictGone is D6's tombstone shape for the "gone" branch of an edit
// conflict: the target row was deleted between the caller's read and its
// write, so there is no current document to hand back — only the fact
// (Gone: true) and an EXPLICITLY NULL editVersion, never an omitted field,
// so a caller cannot mistake "the server didn't say" for "the version is
// zero". Shared by all four single-object routes' conflict responses
// (override, endpoint, workspace, scenario); the preset's own conflict
// shape (D12's staleVersions map) is a set, not a tombstone, and belongs to
// preset_handlers.go instead.
type editConflictGone struct {
	Gone        bool   `json:"gone"`
	EditVersion *int64 `json:"editVersion"`
}

// loadWorkspace resolves {id}, answering 404 for an id that parses but names
// no workspace. Shared by every handler in this file and in
// preset_handlers.go: both surfaces are scoped to one workspace exactly the
// way every other admin route already is (see workspace_handlers.go's
// ownership note — any logged-in caller may read or edit any workspace).
func (s *Server) loadWorkspace(w http.ResponseWriter, r *http.Request, id int64) (*workspaces.Workspace, bool) {
	ws, err := s.ws.ByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, workspaces.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
			return nil, false
		}
		s.log.Error("load workspace", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load workspace")
		return nil, false
	}
	return ws, true
}

// opKeyFromPath re-escapes the {opKey} path value back into the
// percent-encoded form [overrides.ParseOpKey] expects.
//
// Go 1.22's http.ServeMux pattern matching splits the request path on its
// ESCAPED form, so a "%2F" inside one segment never acts as a path separator
// — exactly the property [overrides.OpKey]'s own doc comment relies on to
// fit "method + relative path" into one URL segment. But
// [http.Request.PathValue] hands the matched segment back ALREADY DECODED
// (verified against this exact ServeMux behavior, not assumed). Re-escaping
// it here before handing it to the overrides package reconstructs an input
// [overrides.ParseOpKey] decodes back to byte-for-byte the same string
// PathValue gave us — url.PathEscape/PathUnescape are exact inverses of each
// other for ANY input, including one that happens to contain a literal "%"
// that a second, accidental decode could otherwise misinterpret as its own
// escape sequence.
func opKeyFromPath(r *http.Request) string {
	return url.PathEscape(r.PathValue("opKey"))
}

// mergedStatusView is one declared response of an operation, as the spec
// itself describes it — independent of whatever override may exist.
type mergedStatusView struct {
	Selector   string `json:"selector"`
	HTTPStatus int    `json:"httpStatus"`
	IsDefault  bool   `json:"isDefault"`
	MediaType  string `json:"mediaType,omitempty"`
}

// opOverrideResponseSummary is the merged view's per-status override summary:
// enough to show "this status is pinned/generated with N recipes" without
// shipping every recipe and every pinned body for every operation in the
// workspace on one list request.
type opOverrideResponseSummary struct {
	Mode        string `json:"mode"`
	RecipeCount int    `json:"recipeCount"`
	// HasSchemaPatch is P7b's (decisions.md mocker-p7-api-design D12): the
	// «Контракт» tab marks a spec operation «изменено» when its override
	// carries a patched schema or a pinned response, and the list view is
	// the one read it has for that — the patch itself stays behind GET
	// .../operations/{opKey}, as before.
	HasSchemaPatch bool `json:"hasSchemaPatch"`
}

// opOverrideSummaryView is the override half of one [mergedOperationView],
// present only when a row actually exists for that operation.
type opOverrideSummaryView struct {
	OverrideOn   bool                                 `json:"overrideOn"`
	RouteOff     bool                                 `json:"routeOff"`
	ActiveStatus *int                                 `json:"activeStatus,omitempty"`
	Responses    map[string]opOverrideResponseSummary `json:"responses"`
	UpdatedAt    int64                                `json:"updatedAt"`
	// EditVersion is A3's per-row compare-and-swap token (D5): one of the
	// five reads that grow it, and the reason it grows here rather than
	// only on overrideDocView below — the LIST and the DETAIL are two
	// different structs, and a caller of GET .../operations that never
	// opens the detail route still needs a version to send to the PUT.
	EditVersion int64 `json:"editVersion"`
}

func newOpOverrideSummaryView(row *overrides.Row) *opOverrideSummaryView {
	resp := make(map[string]opOverrideResponseSummary, len(row.Responses))
	for status, v := range row.Responses {
		mode := v.Mode
		if mode == "" {
			mode = "generated" // Variant.Mode's own documented default.
		}
		resp[status] = opOverrideResponseSummary{Mode: mode, RecipeCount: len(v.Recipes), HasSchemaPatch: len(v.SchemaPatch) > 0}
	}
	return &opOverrideSummaryView{
		OverrideOn:   row.OverrideOn,
		RouteOff:     row.RouteOff,
		ActiveStatus: row.ActiveStatus,
		Responses:    resp,
		UpdatedAt:    row.UpdatedAt.Unix(),
		EditVersion:  row.EditVersion,
	}
}

// mergedOperationView is one row of GET .../operations: what the spec
// declares for this operation, plus the override state on top of it when one
// exists. This is what the UI (P1d) renders as the operations list, and what
// a human reads to check the auth preset actually landed.
type mergedOperationView struct {
	Method   string                 `json:"method"`
	Path     string                 `json:"path"`
	OpKey    string                 `json:"opKey"`
	Statuses []mergedStatusView     `json:"statuses"`
	Override *opOverrideSummaryView `json:"override,omitempty"`
}

// handleListOperations answers GET /api/workspaces/{id}/operations: every
// operation of the workspace's spec, merged with its override state.
//
// A workspace with no spec answers an empty list, not a 404 — there is
// nothing wrong with the workspace, there is simply nothing to list yet.
//
// Built from exactly three queries regardless of how many operations the
// spec has (specs.Repo.Operations with limit<=0 for "every row",
// specs.Repo.Variants, overrides.Repo.ForWorkspace) — never one query per
// operation, which is the N+1 DESIGN §18 forbids and which, on the
// acceptance document's 130 operations, would be 130 extra round trips on
// every load of this screen.
func (s *Server) handleListOperations(w http.ResponseWriter, r *http.Request) {
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
	if ws.SpecID == nil {
		httpx.JSON(w, http.StatusOK, []mergedOperationView{})
		return
	}
	specID := *ws.SpecID

	// limit<=0 is specs.Repo.Operations' own documented "no LIMIT clause at
	// all" sentinel — deliberately, not a magic number invented here: a view
	// that silently truncated which operations exist would lie about the
	// workspace's own spec.
	ops, err := s.specsRepo.Operations(r.Context(), specID, 0, 0)
	if err != nil {
		s.log.Error("list operations for merged view", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list operations")
		return
	}
	variants, err := s.specsRepo.Variants(r.Context(), specID)
	if err != nil {
		s.log.Error("list response variants for merged view", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list response variants")
		return
	}
	rows, err := s.overridesRepo.ForWorkspace(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("list overrides for merged view", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list overrides")
		return
	}

	out := make([]mergedOperationView, len(ops))
	for i, op := range ops {
		key := overrides.OpKey(op.Method, op.Path)
		rv := variants[op.ID]
		statuses := make([]mergedStatusView, len(rv))
		for j, v := range rv {
			statuses[j] = mergedStatusView{
				Selector: v.Selector, HTTPStatus: v.HTTPStatus, IsDefault: v.IsDefault, MediaType: v.MediaType,
			}
		}
		view := mergedOperationView{Method: op.Method, Path: op.Path, OpKey: key, Statuses: statuses}
		if row, ok := rows[key]; ok {
			view.Override = newOpOverrideSummaryView(row)
		}
		out[i] = view
	}
	httpx.JSON(w, http.StatusOK, out)
}

// overrideMutableFields is the part of an op_overrides row PUT actually
// writes — every field [overrides.Row] owns except the (method, path)
// identity, which {opKey} in the URL supplies, and the metadata (updatedAt)
// only a GET response adds on top. Embedded into [overrideDocView] for
// GET/PUT responses and decoded on its own as PUT's request body, so the
// mutable field list is written exactly once instead of twice in a way that
// could drift.
//
// A PUT that supplies Responses[status].When, .SchemaPatch or the row's own
// FailDirective (none of which this slice interprets) round-trips them
// unchanged: this struct decodes them into the same fields [overrides.Row]
// stores byte-for-byte, and [overrides.Repo.Put] writes exactly what it is
// given — nothing here re-derives or drops a field this slice does not
// implement.
type overrideMutableFields struct {
	OverrideOn    bool                         `json:"overrideOn"`
	RouteOff      bool                         `json:"routeOff"`
	ActiveStatus  *int                         `json:"activeStatus,omitempty"`
	Responses     map[string]overrides.Variant `json:"responses"`
	ListSize      *overrides.ListSize          `json:"listSize,omitempty"`
	DelayMs       *int                         `json:"delayMs,omitempty"`
	FailDirective jsonx.RawMessage             `json:"failDirective,omitempty"`
	ValidateReq   *bool                        `json:"validateReq,omitempty"`
}

// overrideDocView is the wire shape of one op_overrides row: PUT's request
// shape plus the (method, path, opKey) identity and updatedAt metadata GET
// adds so a UI can show what it is looking at without a second request.
//
// EditVersion sits BESIDE overrideMutableFields, never inside it (D7's
// "sibling, never a member" — overrideMutableFields is also
// previewRequestWire.Draft's whole shape, and a field added inside it would
// land on the preview route this slice deliberately excludes).
type overrideDocView struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	OpKey  string `json:"opKey"`
	overrideMutableFields
	UpdatedAt   int64 `json:"updatedAt"`
	EditVersion int64 `json:"editVersion"`
}

// newOverrideMutableFields extracts row's mutable document, shared by
// newOverrideDocView (GET/PUT's wrapper) and the PUT conflict payload below
// (D6: the conflict's "current document" is the same fields, boxed
// differently, never a third hand-copy of the field list).
func newOverrideMutableFields(row *overrides.Row) overrideMutableFields {
	return overrideMutableFields{
		OverrideOn:    row.OverrideOn,
		RouteOff:      row.RouteOff,
		ActiveStatus:  row.ActiveStatus,
		Responses:     row.Responses,
		ListSize:      row.ListSize,
		DelayMs:       row.DelayMs,
		FailDirective: row.FailDirective,
		ValidateReq:   row.ValidateReq,
	}
}

func newOverrideDocView(row *overrides.Row) overrideDocView {
	return overrideDocView{
		Method:                row.Method,
		Path:                  row.Path,
		OpKey:                 overrides.OpKey(row.Method, row.Path),
		overrideMutableFields: newOverrideMutableFields(row),
		UpdatedAt:             row.UpdatedAt.Unix(),
		EditVersion:           row.EditVersion,
	}
}

// overridePutView is what a successful PUT answers: the stored document,
// plus the workspace's new revision — so a caller can tell whether the mock
// plane has already picked this edit up (the runtime cache keys off exactly
// (workspace, revision); see [overrides.Repo.Put]'s own doc comment).
// EditVersion rides in through the embedded overrideDocView, so a caller can
// write twice without re-reading between them (D5).
type overridePutView struct {
	overrideDocView
	Revision int64 `json:"revision"`
}

// putOperationRequest is PUT .../operations/{opKey}'s request body — the ONE
// route of the five whose body has no pre-existing named wrapper schema (the
// endpoint/workspace/scenario requests all extend one that already exists in
// api/openapi.json's components.schemas). Its body on the wire today is a
// bare $ref to OverrideMutableFields, so a sibling token has nowhere to sit
// without a genuinely new type — this one, composing the untouched
// overrideMutableFields with EditVersion beside it (D7).
type putOperationRequest struct {
	overrideMutableFields
	// EditVersion is REQUIRED (D10): a nil pointer means the caller omitted
	// the field and handlePutOperation rejects it BY NAME below — "no
	// expectation" exists only at the repository verb (D7), never on any
	// wire. &0 is legal and distinct: it means "I expect no row yet",
	// meaningful because op_overrides is the one table in this population
	// where a PUT legitimately upserts from nothing (D7).
	EditVersion *int64 `json:"editVersion"`
}

// overrideConflictDetails is PUT .../operations/{opKey}'s conflict payload
// (D6): every field the route accepts on the way in — overrideMutableFields,
// untouched — plus the version the server actually holds. Round-trippable:
// a caller can resend this exact document, with EditVersion swapped in as
// its new expectation, and have the retry succeed.
type overrideConflictDetails struct {
	overrideMutableFields
	EditVersion int64 `json:"editVersion"`
}

// answerOverrideEditConflict writes PUT .../operations/{opKey}'s 409 for a
// lost compare-and-swap. conflict.Current is boxed by
// [overrides.Repo.PutExpecting] as a plain overrides.Row (never a pointer —
// see that function's own doc comment), so the type assertion below is the
// one place this handler translates the sentinel's untyped payload into the
// route's declared wire shape.
func (s *Server) answerOverrideEditConflict(w http.ResponseWriter, conflict *store.EditConflictError) {
	if conflict.Gone {
		httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
			"operation override was deleted by another write", editConflictGone{Gone: true})
		return
	}
	row, ok := conflict.Current.(overrides.Row)
	if !ok {
		s.log.Error("override edit conflict: unexpected payload type", "type", fmt.Sprintf("%T", conflict.Current))
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to build conflict details")
		return
	}
	httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
		"operation override was changed by another write",
		overrideConflictDetails{overrideMutableFields: newOverrideMutableFields(&row), EditVersion: row.EditVersion})
}

// handleGetOperation answers GET .../operations/{opKey}: the full stored
// override document for one operation, or 404 when none exists.
func (s *Server) handleGetOperation(w http.ResponseWriter, r *http.Request) {
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

	row, err := s.overridesRepo.Get(r.Context(), ws.ID, opKeyFromPath(r))
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, newOverrideDocView(row))
	case errors.Is(err, overrides.ErrInvalidOpKey):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid operation key")
	case errors.Is(err, overrides.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "no override for this operation")
	default:
		s.log.Error("get override", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load override")
	}
}

// dangerousMediaType is [httpx.BrowserExecutableMediaType] at this plane's
// WRITE boundary: the value an operator typed, checked before it can reach the
// database at all. The rule itself lives in internal/httpx because the mock
// plane applies the same one to the EFFECTIVE type at serve time, and two
// copies of a security rule are two things to get wrong — this one had a
// second copy, and both missed the same bypass.
//
// It asks nothing about the variant's MODE, and the name says so on purpose:
// it used to, and that was a hole. Mode is mutable after the fact — the
// traffic-to-override conversion flips a stored "generated" variant to
// "pinned" in place — so a guard that only refused text/html on a variant
// ALREADY pinned let the identical row in through the front door and pinned
// it a moment later. Whether a media type may be stored cannot depend on a
// field the next write can change.
func dangerousMediaType(mediaType string) bool {
	return httpx.BrowserExecutableMediaType(mediaType)
}

// schemaPatchDoc decodes one side of a schemaPatch comparison into a
// document [reflect.DeepEqual] can compare, for the strict ingress gate
// ([gateSchemaPatch]). Nil, a whitespace-only slice and the four bytes
// "null" are three of the wire's own spellings of "no patch"; a JSON `[]`
// is the fourth ([jsonpatch.Patch.Empty]'s doc comment draws the identical
// line for the same reason: an operator clearing a patch and a variant that
// never had one must compare equal). All four normalize to the same nil
// value here.
func schemaPatchDoc(raw jsonx.RawMessage) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var v any
	if err := jsonx.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	if arr, ok := v.([]any); ok && len(arr) == 0 {
		return nil, nil
	}
	return v, nil
}

// schemaPatchChanged decides whether stored and submitted are the SAME
// document, decoded — never compared as bytes. A byte comparison is wrong,
// not merely stricter: the editor round-trips this field on every save
// without rendering it, and that round trip is provably not
// byte-preserving (Go escapes '&' as & where JS does not; JS
// renormalises "1.0" to "1" and writes an overflowed number back as
// "null"). A byte comparison would fire on a patch nobody touched and hand
// the operator an unclearable 400 through the very mechanism meant to
// prevent one.
//
// The two decode failures are asymmetric and the order below is not
// incidental:
//
//  1. Submitted decodes first. A document nobody can decode is a different
//     document by definition, and it is caller-supplied — the one thing
//     this gate exists to refuse — so this reports "changed" regardless of
//     what stored holds.
//  2. Only once submitted decodes: a stored document that fails to decode
//     is already inert on the read path (the variant already serves
//     unpatched), so this reports "unchanged" and lets a legacy row stay
//     editable, whatever submitted contains.
//  3. Otherwise both sides decoded: report "changed" only on a genuine
//     structural difference.
func schemaPatchChanged(stored, submitted jsonx.RawMessage) bool {
	submittedDoc, err := schemaPatchDoc(submitted)
	if err != nil {
		return true
	}
	storedDoc, err := schemaPatchDoc(stored)
	if err != nil {
		return false
	}
	return !reflect.DeepEqual(storedDoc, submittedDoc)
}

// gateSchemaPatch is the strict patch gate: the ingress door a
// caller-supplied Variant.SchemaPatch passes through, and the only one —
// every other writer of this column (from-traffic, the auth preset,
// checkpoint restore, both bundle directions) carries bytes it did not
// receive from a caller and goes through [overrides.Repo.Put] with its own
// mutator, never through this one, so none of them is gated.
//
// Called from inside the Put mutator, BEFORE it overwrites cur.Responses
// with body.Responses: one line later the STORED side of the comparison is
// gone, replaced by the submitted one, and both sides would read identical
// to any comparison.
//
// A patch that is unchanged from what is already stored is NOT
// re-validated, however malformed its shape — the gate exists to catch a
// caller-typed mistake, not to retroactively enforce a rule this project
// did not have when an earlier row was written. Only a genuinely changed
// document is re-parsed, with [jsonpatch.Parse] — the same parser
// [internal/mockplane]'s runtime build applies at — so this handler and the
// serving path can never disagree about which patches are legal.
func gateSchemaPatch(cur *overrides.Row, body overrideMutableFields) error {
	for status, v := range body.Responses {
		var stored jsonx.RawMessage
		if sv, ok := cur.Responses[status]; ok {
			stored = sv.SchemaPatch
		}
		if !schemaPatchChanged(stored, v.SchemaPatch) {
			continue
		}
		// The message names the offending RFC 6902 operation
		// ("unsupported patch operation \"move\"") because a generic
		// "invalid request body" is what decodeJSON's
		// DisallowUnknownFields already answers for an unrelated failure
		// (an unknown JSON key), and a caller reading a 400 needs to tell
		// the two apart. The bounds (patch size, operation count) are
		// Parse's own, not reimplemented here.
		if _, perr := jsonpatch.Parse(v.SchemaPatch); perr != nil {
			return fmt.Errorf("%w: responses[%s].schemaPatch: %w", overrides.ErrInvalidRow, status, perr)
		}
	}
	return nil
}

// handlePutOperation answers PUT .../operations/{opKey}: a FULL replacement
// of that operation's override document. Every field [overrideMutableFields]
// carries is written wholesale — Responses included — because that is what
// "full replacement" means; a UI that wants to keep an existing status's
// pinned body or preserved when[]/schemaPatch/failDirective must GET first
// and resend them, exactly as it must resend any other field it wants kept.
//
// Validation (unknown status key, unknown mode, an invalid recipe per
// [recipes.Recipe.Validate], an inverted listSize/negative delayMs) all
// happens inside [overrides.Repo.Put] — the SAME gate both this handler and
// [Server.handleApplyAuthPreset] write through — and comes back as
// [overrides.ErrInvalidRow], answered here as 400 with that error's own
// message, which already names the offending response status and pattern.
// Nothing here can turn a malformed body into a 500 or a panic.
//
// One more check rides inside the same mutator, ahead of the wholesale
// field assignment below: [gateSchemaPatch], the strict ingress gate on
// Variant.SchemaPatch. It fires only when a response's schemaPatch is a
// genuinely different document from what is already stored — see its own
// doc comment for why a byte comparison would be wrong, not merely
// stricter — and its rejection is the same [overrides.ErrInvalidRow] path
// every other validation failure here already answers.
func (s *Server) handlePutOperation(w http.ResponseWriter, r *http.Request) {
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

	var body putOperationRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	// D10: required on all five routes. A nil pointer here is "the caller
	// omitted the field" — never "no expectation", which is a state only
	// the repository verb can express (D7) — so it is rejected BY NAME,
	// not silently forwarded as an unguarded write.
	if body.EditVersion == nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "editVersion is required")
		return
	}
	// A response's MediaType is the one thing this slice lets an operator make
	// the mock plane claim verbatim (respond.go serves a pinned body under it,
	// unvalidated) — reject a browser-executable one here, at the write
	// boundary, before [overrides.Repo.Put] can ever store it (see
	// [dangerousMediaType]'s doc comment for why).
	//
	// Deliberately NOT qualified by v.Mode. It was, and the qualifier was a
	// hole: mode is mutable after the fact, so {"mode":"generated",
	// "mediaType":"text/html"} sailed through here and the traffic-to-override
	// conversion then flipped that same stored variant to "pinned" — landing
	// exactly the row this check exists to keep out, by a path this check never
	// saw. A media type this plane will not serve has no business being stored
	// under any mode; a "generated" variant claiming text/html was never
	// meaningful anyway, since generation emits JSON built from the schema.
	for status, v := range body.Responses {
		if dangerousMediaType(v.MediaType) {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, fmt.Sprintf(
				"responses[%s]: mediaType %q is browser-executable and cannot be stored", status, v.MediaType))
			return
		}
		// P7a (decisions.md mocker-p7-api-design D2): `schema` is a CUSTOM
		// endpoint's field — a spec operation already has a response
		// schema, and the way to change it is schemaPatch, which applies
		// to the resolved one instead of replacing it. Refused BY NAME
		// here rather than by a second validator inside
		// internal/overrides, because the field must stay legal on the
		// shared Variant type a custom endpoint's own responses use.
		if len(v.Schema) > 0 {
			httpx.Err(w, http.StatusBadRequest, codeSchemaOnOverride, fmt.Sprintf(
				"responses[%s]: schema belongs to a custom endpoint; a spec operation already has one — change it with schemaPatch", status))
			return
		}
	}

	stored, revision, err := s.overridesRepo.PutExpecting(r.Context(), ws.ID, opKeyFromPath(r), body.EditVersion, func(cur *overrides.Row) error {
		// The comparison MUST run before cur.Responses is overwritten below:
		// this is the only point in the whole tree where the STORED side is
		// still visible ahead of the write. One line later it is gone,
		// replaced by the submitted one, and both sides would read
		// identical to any comparison.
		if gerr := gateSchemaPatch(cur, body.overrideMutableFields); gerr != nil {
			return gerr
		}
		cur.OverrideOn = body.OverrideOn
		cur.RouteOff = body.RouteOff
		cur.ActiveStatus = body.ActiveStatus
		cur.Responses = body.Responses
		cur.ListSize = body.ListSize
		cur.DelayMs = body.DelayMs
		cur.FailDirective = body.FailDirective
		cur.ValidateReq = body.ValidateReq
		return nil
	})
	var conflict *store.EditConflictError
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, overridePutView{overrideDocView: newOverrideDocView(stored), Revision: revision})
	case errors.As(err, &conflict):
		s.answerOverrideEditConflict(w, conflict)
	case errors.Is(err, overrides.ErrInvalidOpKey):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid operation key")
	case errors.Is(err, overrides.ErrWorkspaceNotFound):
		// A race with a concurrent workspace delete, not a client mistake —
		// loadWorkspace already confirmed existence moments earlier, so this
		// is the same 404 that lookup would give a request arriving now.
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, overrides.ErrInvalidRow):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
	default:
		s.log.Error("put override", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to save override")
	}
}

// deleteOverrideView is what a DELETE that actually removed a row answers:
// just the new revision, the one thing a caller could not already have.
type deleteOverrideView struct {
	Revision int64 `json:"revision"`
}

// handleDeleteOperation answers DELETE .../operations/{opKey}.
//
// [overrides.Repo.Delete] treats "nothing there to delete" as a success that
// leaves the revision exactly where it was (its own doc comment) rather than
// as an error — deleting an override that does not exist is exactly what the
// caller wanted (no override), and there is none. This handler tells the two
// outcomes apart by comparing the returned revision against ws.Revision
// (read moments earlier by loadWorkspace, before the delete ran): unchanged
// means nothing was removed, answered 204 with nothing to report; bumped
// means a row was actually deleted, answered 200 with the new revision so a
// caller can tell the mock plane has picked the removal up.
func (s *Server) handleDeleteOperation(w http.ResponseWriter, r *http.Request) {
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

	revision, deleted, err := s.overridesRepo.Delete(r.Context(), ws.ID, opKeyFromPath(r))
	switch {
	case err == nil && !deleted:
		httpx.NoContent(w)
	case err == nil:
		httpx.JSON(w, http.StatusOK, deleteOverrideView{Revision: revision})
	case errors.Is(err, overrides.ErrInvalidOpKey):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid operation key")
	case errors.Is(err, overrides.ErrWorkspaceNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	default:
		s.log.Error("delete override", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to delete override")
	}
}

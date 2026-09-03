// Preview handlers implement P2f's one route: POST
// /api/workspaces/{id}/preview. It takes a DRAFT operation override — the
// same shape PUT .../operations/{opKey} would save — and answers with the
// response the mock plane WOULD serve for that operation once the draft is
// saved, writing nothing, bumping no revision and touching no cache.
//
// This file owns EVERY check the draft must pass before it is ever handed
// to [mockplane.Plane.Preview] (D7): the row's own validity
// ([overrides.ValidateResponses]), a browser-executable mediaType
// ([dangerousMediaType]) and a CHANGED schemaPatch ([gateSchemaPatch]) —
// the identical three gates [Server.handlePutOperation] runs, in the
// identical order, so a draft that previews clean is a draft PUT would
// actually accept. Two of the three are unexported in this package, which
// is exactly why they run here and not inside the plane (D2).
package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/workspaces"
)

// The six admin-local wire codes the preview route's own taxonomy answers
// with (D6's rows 1-5, plus D12's row 5a). They live here, not in
// [internal/httpx], for the same reason unsupported_media_type/rate_limited/
// service_unavailable already do (N9): none of them is a code any OTHER
// route on this plane could ever answer, so a shared constant would buy
// nothing but an extra hop to read the value. Six wire codes against FIVE
// [domain.ErrPreview*] sentinels, deliberately: codePreviewInvalidDraft is
// handler-local (row 1, [Server.validatePreviewDraft]'s own gates) and has
// no sentinel of its own — [domain.ErrPreviewResourceServes] is the fifth
// sentinel, mapped to codePreviewResourceServes below.
const (
	codePreviewInvalidDraft       = "invalid_draft"
	codePreviewCustomEndpointWins = "custom_endpoint_wins"
	codePreviewNoSpec             = "no_spec"
	codePreviewOperationNotFound  = "operation_not_found"
	codePreviewMissingPathParam   = "missing_path_param"
	// codePreviewResourceServes is D12's row 5a: the located route is taken
	// over by a confirmed resource (GET X, GET X/{} always; DELETE X/{} and
	// POST X per the eligibility rule [mockplane.previewResourceServes]
	// implements) — see [domain.ErrPreviewResourceServes]'s own doc comment
	// for the full eligibility rule and its position in the taxonomy.
	codePreviewResourceServes = "resource_serves"
)

// Previewer is [mockplane.Plane.Preview] as this package needs it — one
// method, so this file never has to import internal/mockplane (A9). D2
// pins the signature verbatim; internal/admin only ever calls through this
// interface, never names the concrete type.
type Previewer interface {
	Preview(ctx context.Context, ws *workspaces.Workspace, req domain.PreviewRequest) (domain.PreviewResult, error)
}

// SetPreviewer wires p as the Server's [Previewer]. Like SetLiveState and
// SetTraffic (see either's own doc comment), it exists as a setter rather
// than a New parameter: New's signature is shared with
// cmd/mocker/main.go's five call sites, and cmd/mocker passes the SAME
// *mockplane.Plane instance that already serves real traffic — not a
// second one built just for this route.
//
// A Server whose SetPreviewer is never called is not a bug: the route
// answers 503 rather than a nil-interface panic, exactly the shape a test
// harness with no reason to wire it needs (mirrors SetLiveState's own
// contract, livestate_handlers.go).
func (s *Server) SetPreviewer(p Previewer) {
	s.previewer = p
}

// previewRequestWire is the wire shape of the POST body, decoded via
// [decodeJSON] (unknown fields rejected) exactly like every other write
// route on this plane. Draft is [overrideMutableFields] — the SAME type
// PUT decodes into — so this handler can run PUT's own three gates on it
// verbatim rather than decoding into a second, drift-prone shape (D2's own
// warning: two earlier drafts of this decision got exactly this wrong).
//
// Status is a *string, not a *int, because the wire example D10 pins it
// against is a quoted "404" — an operator picking a status TAB, which
// matches the string keys [overrideMutableFields.Responses] is keyed by,
// not the bare integer the RESPONSE document's own "status" field is
// (that one really is the resolved HTTP status, D5). Parsed to an int only
// once it is known to be exactly three decimal digits, immediately below.
type previewRequestWire struct {
	OpKey      string                `json:"opKey"`
	Draft      overrideMutableFields `json:"draft"`
	Status     *string               `json:"status,omitempty"`
	Query      string                `json:"query,omitempty"`
	Headers    map[string]string     `json:"headers,omitempty"`
	Body       jsonx.RawMessage      `json:"body,omitempty"`
	PathParams map[string]string     `json:"pathParams,omitempty"`
}

// refusedView is [domain.RefusedReason] re-tagged for the wire: that type
// lives in internal/domain, which A10 holds to primitives/[]byte/
// map[string]string ONLY — carrying its own json tags there would be the
// package deciding a wire shape neither plane may impose on the other, so
// the tags live here, on this plane's OWN view of the value, instead.
type refusedView struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

// previewResultView is D5's response document, pinned field-for-field:
// Body/Encoding OMITTED (not null) whenever NoBody, RouteOff or Refused is
// set — Body is a *string for exactly that reason, so a legitimately EMPTY
// but otherwise successful body (D6 row 11's own "add a field the spec
// does not have" case) still marshals as `"body":""`, distinct from the
// key being absent altogether. Refused and ShadowedBy are always PRESENT,
// `null` when there is nothing to report — D5's own presence rule for
// them — so neither carries omitempty.
type previewResultView struct {
	Status             int                 `json:"status"`
	StatusSource       domain.StatusSource `json:"statusSource"`
	MediaType          string              `json:"mediaType,omitempty"`
	Headers            map[string]string   `json:"headers,omitempty"`
	Encoding           string              `json:"encoding,omitempty"`
	Body               *string             `json:"body,omitempty"`
	NoBody             bool                `json:"noBody"`
	RouteOff           bool                `json:"routeOff"`
	Refused            *refusedView        `json:"refused"`
	SchemaPatchApplied bool                `json:"schemaPatchApplied"`
	RecipesBound       int                 `json:"recipesBound"`
	DelayMs            int                 `json:"delayMs"`
	ShadowedBy         *string             `json:"shadowedBy"`
	// Notes is A6's: the traffic-note vocabulary a real request would have
	// recorded ("asset_missing"), "" when none — absent on the wire then.
	Notes string `json:"notes,omitempty"`
}

// newPreviewResultView builds the wire document from [domain.PreviewResult]
// verbatim — no field is recomputed, only re-tagged and re-shaped for
// presence (see previewResultView's own doc comment).
func newPreviewResultView(result domain.PreviewResult) previewResultView {
	view := previewResultView{
		Status:             result.Status,
		StatusSource:       result.StatusSource,
		MediaType:          result.MediaType,
		Headers:            result.Headers,
		NoBody:             result.NoBody,
		Notes:              result.Notes,
		RouteOff:           result.RouteOff,
		SchemaPatchApplied: result.SchemaPatchApplied,
		RecipesBound:       result.RecipesBound,
		DelayMs:            result.DelayMs,
	}
	if result.Refused != nil {
		view.Refused = &refusedView{Reason: result.Refused.Reason, Detail: result.Refused.Detail}
	}
	if result.ShadowedBy != "" {
		shadowedBy := result.ShadowedBy
		view.ShadowedBy = &shadowedBy
	}
	// D5's presence rule: Body/Encoding present exactly when neither
	// NoBody, RouteOff nor Refused is set — the same three-way gate
	// [mockplane.Plane.Preview] itself already applies before it ever sets
	// result.Body, restated here only because the WIRE presence (key
	// absent vs. key present) is this layer's own decision, not the
	// domain type's.
	if !result.NoBody && !result.RouteOff && result.Refused == nil {
		var encoded string
		if utf8.Valid(result.Body) {
			view.Encoding = "utf8"
			encoded = string(result.Body)
		} else {
			view.Encoding = "base64"
			encoded = base64.StdEncoding.EncodeToString(result.Body)
		}
		view.Body = &encoded
	}
	return view
}

// wireError is a status/code/message triple a handler can build ahead of
// time and answer with in one line — pulled out so
// [Server.validatePreviewDraft] and [Server.parsePreviewStatus] can report
// a failure as a return value instead of writing to w themselves, keeping
// [Server.handlePreviewOperation]'s own branching (gocyclo, N6) to the
// steps that are genuinely its own: routing to the right helper and, for
// the one call that actually renders anything, mapping [Server.previewer]'s
// four taxonomy sentinels.
type wireError struct {
	status  int
	code    string
	message string
}

func (s *Server) writeWireError(w http.ResponseWriter, e *wireError) {
	httpx.Err(w, e.status, e.code, e.message)
}

// validatePreviewDraft runs D7's three PUT-equivalent gates, in PUT's own
// order, plus the two inline row-level checks D7 requires because this
// route can reach them no other way (method/path are excluded: opKey
// supplies both and cannot express either violation):
//
//  1. dangerousMediaType, over the draft's own Responses — exactly where
//     PUT runs it, right after decode and ahead of its own Repo.Put call.
//  2. the currently stored row for this opKey, read once — [gateSchemaPatch]
//     needs it (D7: "the stored row … which preview has already read to
//     build its runtime"). ErrNotFound means no row was ever saved; the
//     zero row constructed here mirrors [overrides.Repo.Put]'s own seed for
//     that case (repo.go's "a zero row … when there is none") so
//     gateSchemaPatch's map lookups never see a nil map. An opKey that
//     fails to parse is caught here too, via the identical
//     [overrides.ErrInvalidOpKey] path [Server.handleGetOperation] answers
//     400 with.
//  3. gateSchemaPatch(cur, draft) — a changed, unparsable RFC 6902 patch.
//  4. ValidateResponses(draft.Responses) — the row's own shape: status
//     keys, mode, base64 body, pinned size, recipe count, when[] shapes.
//  5. listSize's range, delayMs's sign.
//
// Every failure answers with codePreviewInvalidDraft (D6's row 1: "a draft
// that cannot be saved should not be told anything else about itself"),
// except an opKey that fails to parse or a database error on the read,
// neither of which is about the draft's own shape.
func (s *Server) validatePreviewDraft(ctx context.Context, ws *workspaces.Workspace, wire previewRequestWire) *wireError {
	for status, v := range wire.Draft.Responses {
		if dangerousMediaType(v.MediaType) {
			return &wireError{http.StatusBadRequest, codePreviewInvalidDraft, fmt.Sprintf(
				"responses[%s]: mediaType %q is browser-executable and cannot be stored", status, v.MediaType)}
		}
	}

	cur, err := s.overridesRepo.Get(ctx, ws.ID, wire.OpKey)
	switch {
	case err == nil:
	case errors.Is(err, overrides.ErrInvalidOpKey):
		return &wireError{http.StatusBadRequest, httpx.CodeBadRequest, "invalid operation key"}
	case errors.Is(err, overrides.ErrNotFound):
		cur = &overrides.Row{Responses: map[string]overrides.Variant{}}
	default:
		s.log.Error("preview: load stored override", "workspace", ws.Slug, "err", err)
		return &wireError{http.StatusInternalServerError, httpx.CodeInternal, "failed to load stored override"}
	}

	if perr := gateSchemaPatch(cur, wire.Draft); perr != nil {
		return &wireError{http.StatusBadRequest, codePreviewInvalidDraft, perr.Error()}
	}
	if verr := overrides.ValidateResponses(wire.Draft.Responses); verr != nil {
		return &wireError{http.StatusBadRequest, codePreviewInvalidDraft, verr.Error()}
	}
	if ls := wire.Draft.ListSize; ls != nil && (ls.Min < 0 || ls.Max < ls.Min) {
		return &wireError{http.StatusBadRequest, codePreviewInvalidDraft, fmt.Sprintf(
			"listSize has an inverted or negative range [%d,%d]", ls.Min, ls.Max)}
	}
	if wire.Draft.DelayMs != nil && *wire.Draft.DelayMs < 0 {
		return &wireError{http.StatusBadRequest, codePreviewInvalidDraft, fmt.Sprintf(
			"delayMs must not be negative, got %d", *wire.Draft.DelayMs)}
	}
	return nil
}

// parsePreviewStatus parses the wire's optional "status" (a quoted 3-digit
// string, previewRequestWire's own doc comment on why it is a *string and
// not a *int) into the *int [domain.PreviewRequest.Status] carries. nil in,
// nil out.
func parsePreviewStatus(raw *string) (*int, *wireError) {
	if raw == nil {
		return nil, nil
	}
	n, err := strconv.Atoi(*raw)
	if err != nil || len(*raw) != 3 || n < 0 {
		return nil, &wireError{http.StatusBadRequest, httpx.CodeBadRequest, "status must be a 3-digit status code"}
	}
	return &n, nil
}

// answerPreviewResult maps [Server.previewer.Preview]'s result to the wire:
// a 200 with the rendered document, one of D6/D12's five taxonomy sentinels
// (rows 2-5 and 5a, each's own HTTP status), or — for anything else, which
// no taxonomy row assigns to this entry point (an opKey that stopped
// parsing between [Server.validatePreviewDraft]'s own read and this call, a
// buildRuntime infra failure) — a logged 500, the identical shape
// [Server.handlePutOperation]'s own default case already uses.
func (s *Server) answerPreviewResult(w http.ResponseWriter, ws *workspaces.Workspace, result domain.PreviewResult, err error) {
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, newPreviewResultView(result))
	case errors.Is(err, domain.ErrPreviewCustomEndpointWins):
		httpx.Err(w, http.StatusConflict, codePreviewCustomEndpointWins, err.Error())
	case errors.Is(err, domain.ErrPreviewNoSpec):
		httpx.Err(w, http.StatusBadRequest, codePreviewNoSpec, err.Error())
	case errors.Is(err, domain.ErrPreviewOperationNotFound):
		httpx.Err(w, http.StatusNotFound, codePreviewOperationNotFound, err.Error())
	case errors.Is(err, domain.ErrPreviewMissingPathParam):
		httpx.Err(w, http.StatusBadRequest, codePreviewMissingPathParam, err.Error())
	case errors.Is(err, domain.ErrPreviewResourceServes):
		httpx.Err(w, http.StatusConflict, codePreviewResourceServes, err.Error())
	default:
		s.log.Error("preview", "workspace", ws.Slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to render preview")
	}
}

// handlePreviewOperation answers POST /api/workspaces/{id}/preview.
//
// Once [Server.validatePreviewDraft] passes, the validated draft gets
// RE-MARSHALLED (never forwarded as the caller's raw bytes) into
// [domain.PreviewRequest.Draft] — D2's own reason: previewRequestWire has
// exactly eight tagged fields, so no stray key the caller sent can reach
// [overrides.Row]'s six exported-but-untagged fields on the other side of
// the seam.
func (s *Server) handlePreviewOperation(w http.ResponseWriter, r *http.Request) {
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
	if s.previewer == nil {
		httpx.Err(w, http.StatusServiceUnavailable, codeServiceUnavailable,
			"preview is not available: no Previewer was wired")
		return
	}

	var wire previewRequestWire
	if err := decodeJSON(r, &wire); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}

	if werr := s.validatePreviewDraft(r.Context(), ws, wire); werr != nil {
		s.writeWireError(w, werr)
		return
	}
	status, werr := parsePreviewStatus(wire.Status)
	if werr != nil {
		s.writeWireError(w, werr)
		return
	}

	draft, merr := jsonx.Marshal(wire.Draft)
	if merr != nil {
		// wire.Draft decoded successfully moments ago and every field of
		// it has already been validated — reaching here means a bug in
		// this handler's own re-marshal, never something a caller typed.
		s.log.Error("preview: re-marshal validated draft", "workspace", ws.Slug, "err", merr)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to encode draft")
		return
	}

	result, perr := s.previewer.Preview(r.Context(), ws, domain.PreviewRequest{
		// A6 (D7): the asset_url base from THIS request, through the one
		// constructor the mock plane also uses, so a previewed body carries
		// the URL a real request on this deployment would.
		AssetBase:  httpx.WorkspaceURL(r, s.cfg, ws.Slug) + s.cfg.ReservedPrefix + "/assets/",
		OpKey:      wire.OpKey,
		Draft:      draft,
		Status:     status,
		Query:      wire.Query,
		Headers:    wire.Headers,
		Body:       wire.Body,
		PathParams: wire.PathParams,
	})
	s.answerPreviewResult(w, ws, result, perr)
}

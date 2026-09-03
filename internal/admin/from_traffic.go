// From-traffic implements DESIGN §14 screen 8's two conversions: turning one
// observed traffic row into either a pinned edit on the operation it hit
// («создать правку из этого ответа», [Server.handleToOverride]) or a custom
// endpoint carrying the request it hit nothing for
// («создать endpoint из этого запроса», [Server.handleToEndpoint]).
//
// THE TWO RESOLVE THEIR KEY DIFFERENTLY, and that is the entire point of
// having two handlers instead of one parameterized by a "mode" flag:
//
//   - handleToOverride keys on the OPERATION'S OWN TEMPLATE PATH
//     ("/widgets/{widgetId}") — the only shape [overrides.OpKey] can ever
//     produce a lookup hit against, resolved via traffic.matched_id ->
//     [specs.Repo.OperationByID] -> that row's own Path. Stripping the
//     workspace's base path off the traffic row's CONCRETE path
//     ("/api/v1/widgets/7" -> "/widgets/7") would build a key no route will
//     ever produce again: the written row would sit orphaned, and a test
//     asserting against that stripped path would pass VACUOUSLY on any
//     operation with no {param} in it at all — this file never does that.
//   - handleToEndpoint keys on the CONCRETE path with the base path
//     STRIPPED ("/api/v1/legacy/ping" -> "/legacy/ping"): a custom endpoint
//     is exactly the literal route somebody asked for, and the base path is
//     glued back on at match time the same way it is for every spec
//     operation (DESIGN §7 step 3).
//
// Both live here, in the one layer that holds ws.Settings AND both repos
// (overridesRepo, customepRepo) — internal/overrides must never import
// internal/customep or internal/workspaces, and internal/traffic must never
// import either, so nowhere else in this codebase CAN resolve this.
package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// toEndpointStatusOn404 and toEndpointBodyOn404 are the PINNED status/body
// rule DESIGN §14 spells out for handleToEndpoint verbatim: an observed 404
// becomes a 200 carrying the observed body, or "{}" when there was none —
// the operator is creating the endpoint precisely because it was missing, so
// re-serving the 404 would be a no-op. ANY OTHER observed status is
// preserved exactly as it was. This is not this handler's choice to make
// differently; it is written down once, here, so a second conversion added
// later cannot pick a different rule for the same situation.
const (
	toEndpointStatusOn404 = http.StatusOK
	toEndpointBodyOn404   = "{}"
)

// noBodyStatuses mirrors mockplane/respond.go's own noBody rule (204 No
// Content, 205 Reset Content): the two statuses that HTTP itself says carry
// no body. handleToOverride refuses to pin one — there is nothing observed
// to pin.
var noBodyStatuses = map[int]bool{
	http.StatusNoContent:    true,
	http.StatusResetContent: true,
}

// toOverrideView is handleToOverride's success wire shape.
type toOverrideView struct {
	OpKey    string `json:"opKey"`
	Status   int    `json:"status"`
	Revision int64  `json:"revision"`
}

// handleToOverride answers POST /api/workspaces/{id}/traffic/{tid}/to-override.
func (s *Server) handleToOverride(w http.ResponseWriter, r *http.Request) {
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
	tid, ok := parsePathInt64(w, r, "tid")
	if !ok {
		return
	}

	row, err := s.trafficRepo.Get(r.Context(), ws.ID, tid)
	if err != nil {
		if errors.Is(err, traffic.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "traffic row not found")
			return
		}
		s.log.Error("to-override: load traffic row", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load traffic row")
		return
	}

	// Every refusal below is a 409: the row exists and was read fine, it is
	// simply not a legitimate source for a pinned edit. Order follows the
	// task's own list — "nothing to pin to" before "the body can't be
	// trusted" before "there is no body at all" — so the error message
	// always names the FIRST reason, not whichever happened to be checked
	// last.
	if row.MatchedKind != "operation" || row.MatchedID == nil {
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"traffic row did not match a spec operation; nothing to pin an edit to")
		return
	}
	op, err := s.specsRepo.OperationByID(r.Context(), *row.MatchedID)
	if err != nil {
		if errors.Is(err, specs.ErrNotFound) {
			httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
				"the operation this row matched no longer exists (a re-import orphaned it)")
			return
		}
		s.log.Error("to-override: resolve operation", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to resolve operation")
		return
	}
	if ws.SpecID == nil || op.SpecID != *ws.SpecID {
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"the operation this row matched belongs to a different spec than the workspace's own")
		return
	}
	if row.Truncated || row.Redacted() {
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"traffic row's body was truncated or redacted; pinning it would ship a lie")
		return
	}
	if noBodyStatuses[row.Status] {
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"observed status carries no body to pin")
		return
	}

	respBody, encoding := encodeBodyForVariant([]byte(row.RespBody))
	statusKey := strconv.Itoa(row.Status)
	key := overrides.OpKey(op.Method, op.Path)

	_, revision, err := s.overridesRepo.Put(r.Context(), ws.ID, key, func(cur *overrides.Row) error {
		// Repo.Put defaults OverrideOn true only for a BRAND NEW row — an
		// existing row could have been switched off (override_on=false),
		// and pinning a body onto it without also flipping this would store
		// a response that never serves. Set explicitly, every time.
		cur.OverrideOn = true
		if cur.Responses == nil {
			cur.Responses = map[string]overrides.Variant{}
		}
		// Mutate the EXISTING variant at this status in place — never
		// replace it with a fresh overrides.Variant{}. The auth preset
		// writes its JWT recipe into exactly responses["200"] of the login
		// operation (preset_handlers.go's variant.Recipes[b.DataPath] =
		// b.Recipe), and that same key is also where When, Headers,
		// MediaType and SchemaPatch an operator hand-edited would live. A
		// fresh struct literal here would discard all five the instant the
		// operator clicked "создать правку из этого ответа" on exactly the
		// row that mattered most: the mocked login response, silently
		// breaking the token it returns while every other bar stays green.
		cur.Responses[statusKey] = pinObservedBody(cur.Responses[statusKey], respBody, encoding)
		return nil
	})
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, toOverrideView{OpKey: key, Status: row.Status, Revision: revision})
	case errors.Is(err, overrides.ErrWorkspaceNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, overrides.ErrInvalidRow):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
	default:
		s.log.Error("to-override: put override", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to save override")
	}
}

// pinObservedBody turns the variant already stored at a status into the pinned
// form the conversion is meant to produce: it changes the three fields that
// describe the observed response and NOTHING else.
//
// Mutating the existing value rather than replacing it with a fresh
// overrides.Variant{} is the whole point. The auth preset writes its JWT
// recipe into exactly responses["200"] of the login operation
// (preset_handlers.go's variant.Recipes[b.DataPath] = b.Recipe), and that same
// key is where a When, Headers, MediaType or SchemaPatch an operator
// hand-edited would live. A struct literal here discarded all five the instant
// the operator clicked «создать правку из этого ответа» on exactly the row that
// mattered most — the mocked login response — silently breaking the token it
// returns while every other bar stayed green.
//
// MediaType is the one field not preserved unconditionally, and the reason is
// worth keeping even though it now describes a closed hole. handlePutOperation
// used to refuse a browser-executable type only when the variant was ALREADY
// pinned, so {"mode":"generated","mediaType":"text/html"} was accepted and this
// function then flipped that very row to "pinned" — landing exactly what the
// write-boundary guard existed to keep out, by a path it never saw. That guard
// no longer asks about mode, so no writer can produce the input any more.
//
// The drop stays anyway, as depth rather than as the fix: [overrides.Repo.Put]
// is reached by more than one caller, [overrides.ValidateVariant] does not
// inspect MediaType at all, and a row stored before that guard was widened is
// still readable here. It costs one comparison to make this function safe
// independently of what wrote the row it is mutating.
//
// Split out of handleToOverride so the rule and its one exception read as a
// unit — and so the function stays under the complexity bar it sat exactly at.
func pinObservedBody(existing overrides.Variant, body jsonx.RawMessage, encoding string) overrides.Variant {
	existing.Mode = "pinned"
	existing.Body = body
	existing.BodyEncoding = encoding
	if dangerousMediaType(existing.MediaType) {
		existing.MediaType = ""
	}
	return existing
}

// toEndpointView is handleToEndpoint's success wire shape.
type toEndpointView struct {
	ID       int64  `json:"id"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Revision int64  `json:"revision"`
}

// handleToEndpoint answers POST /api/workspaces/{id}/traffic/{tid}/to-endpoint.
func (s *Server) handleToEndpoint(w http.ResponseWriter, r *http.Request) {
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
	tid, ok := parsePathInt64(w, r, "tid")
	if !ok {
		return
	}

	row, err := s.trafficRepo.Get(r.Context(), ws.ID, tid)
	if err != nil {
		if errors.Is(err, traffic.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "traffic row not found")
			return
		}
		s.log.Error("to-endpoint: load traffic row", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load traffic row")
		return
	}

	// Round-1 review finding 2: handleToOverride refuses a truncated or
	// redacted row (its own identical check, a few lines above in this
	// file) because pinning it "would ship a lie" — a suppressed/redacted
	// row's stored body is empty or replaced, never what the client
	// actually got back. This handler creates a NEW route from that same
	// body, so the exact same lie applies: nothing before this fixed
	// version stopped a redacted 401-login row from becoming a 201 custom
	// endpoint serving an empty pinned body, with no error telling the
	// admin the capture was degraded. Checked first, before this handler's
	// own path/conflict checks, so the reason reported is always "the
	// source can't be trusted" rather than something path-shaped.
	if row.Truncated || row.Redacted() {
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"traffic row's body was truncated or redacted; pinning it would ship a lie")
		return
	}

	relPath, ok := stripBasePath(row.Path, ws.Settings.BasePath)
	if !ok {
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"traffic row's path does not start with the workspace's current base path")
		return
	}

	// Round-3 review finding 1: row.Path is [mockplane.NormalizeSegments]
	// rejoined into one string for DISPLAY ONLY (mockplane/plane.go's own
	// warning on that function) — an encoded slash (%2F) inside a single
	// real segment is textually indistinguishable, once rejoined, from a
	// genuine boundary between two segments. Re-splitting relPath on "/"
	// here, to hand the pieces to customep as a brand-new route, can
	// therefore invent segment boundaries nobody actually requested. If
	// that invented shape happens to land on an operation this workspace's
	// spec already declares, the new custom endpoint SHADOWS it (router.go
	// gives Custom routes priority on a tie, and customep's own package doc
	// says a custom route canonically equal to a spec operation is the
	// documented override, not a conflict) — for an operation the caller
	// who sent the original request never touched and the admin never saw.
	// The check below refuses that specific shape: it does not (and, given
	// only the lossy display string, cannot in general) recover the TRUE
	// original segmentation, but it can tell a lie about that segmentation
	// apart from the truth whenever the truth is already on record — the
	// row's own MatchedID, set live by the router against the real,
	// pre-rejoin segments (routes.go's markTrafficMatch, called BEFORE this
	// lossy string is ever built). A collision with any OTHER operation
	// than the one this row actually, provably matched is refused rather
	// than guessed at.
	if conflictOp, err := s.shadowedOperation(r.Context(), ws, row, relPath); err != nil {
		s.log.Error("to-endpoint: check operation shape conflict", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to check for a shadowed operation")
		return
	} else if conflictOp {
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"this traffic row's recorded path is ambiguous (it may contain an encoded slash) and re-splitting it "+
				"would shadow a spec operation this request never matched; refusing rather than silently overriding it")
		return
	}

	// The PINNED status/body rule (toEndpointStatusOn404's own doc comment):
	// only a literal observed 404 is rewritten, and only there does an empty
	// observed body become the literal "{}" rather than staying empty.
	status := row.Status
	bodyBytes := []byte(row.RespBody)
	if status == http.StatusNotFound {
		status = toEndpointStatusOn404
		if len(bodyBytes) == 0 {
			bodyBytes = []byte(toEndpointBodyOn404)
		}
	}
	respBody, encoding := encodeBodyForVariant(bodyBytes)

	// The cross-table rule (DESIGN §8): an op_overrides row and a custom
	// endpoint on the same (method, path) is forbidden, checked HERE because
	// this is the one layer holding both repos.
	if _, err := s.overridesRepo.Get(r.Context(), ws.ID, overrides.OpKey(row.Method, relPath)); err == nil {
		httpx.Err(w, http.StatusConflict, httpx.CodeConflict,
			"an override already exists for this method and path; a custom endpoint would silently not serve")
		return
	} else if !errors.Is(err, overrides.ErrNotFound) {
		s.log.Error("to-endpoint: check override conflict", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to check for a conflicting override")
		return
	}

	newRow := &customep.Row{
		Method:       row.Method,
		Path:         relPath,
		ActiveStatus: status,
		Responses: map[string]overrides.Variant{
			strconv.Itoa(status): {Mode: "pinned", Body: respBody, BodyEncoding: encoding},
		},
	}
	stored, err := s.customepRepo.Create(r.Context(), ws.ID, newRow)
	if err != nil {
		s.answerCreateEndpointError(w, err)
		return
	}

	// Create bumps workspaces.revision inside its own transaction but does
	// not hand the new value back (unlike overrides.Repo.Put) — a plain,
	// separate read off the reader pool gets it, no nested db.Write
	// involved (HARD RULE 5 is about writes calling writes, not a read
	// after one has already committed).
	updated, err := s.ws.ByID(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("to-endpoint: reload workspace revision", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "endpoint created but failed to report the new revision")
		return
	}
	httpx.JSON(w, http.StatusCreated, toEndpointView{
		ID: stored.ID, Method: stored.Method, Path: stored.Path, Revision: updated.Revision,
	})
}

// stripBasePath removes basePath (already normalized: "" or a leading-slash,
// no-trailing-slash prefix — [domain.Settings.Normalize]) from path,
// reporting false when path does not actually match it — a base path
// changed after the request was recorded is a 409, never a guess.
//
// The comparison is SEGMENT-WISE, not a string prefix (D12): basePath may
// carry a {param} segment (P3h), and "/orgs/7/quizzes" neither starts with
// nor equals "/orgs/{orgId}" as text — a base parameter segment matches ANY
// single path segment at that position, every other basePath segment must
// match literally. Parameter positions come from [router.BaseParamIndexes],
// the ONE owner of "which segments of a base path are parameters" (D7.1,
// D12) — this function is not a second reader of that rule, it only splits
// path and basePath into segments (on "/", dropping empties, the identical
// split BaseParamIndexes' own doc comment says its indices are computed
// against) to compare position by position.
func stripBasePath(path, basePath string) (string, bool) {
	if basePath == "" {
		return path, true
	}
	idx, _, valid := router.BaseParamIndexes(basePath)
	if !valid {
		// A basePath whose own brace shape is invalid cannot be trusted to
		// say which segments are parameters — refuse rather than guess.
		return "", false
	}
	baseSegs := pathSegments(basePath)
	pathSegs := pathSegments(path)
	if len(pathSegs) < len(baseSegs) {
		return "", false
	}
	isParam := make(map[int]bool, len(idx))
	for _, i := range idx {
		isParam[i] = true
	}
	for i, seg := range baseSegs {
		if isParam[i] {
			continue
		}
		if pathSegs[i] != seg {
			return "", false
		}
	}
	rest := pathSegs[len(baseSegs):]
	if len(rest) == 0 {
		return "/", true
	}
	return "/" + strings.Join(rest, "/"), true
}

// shadowedOperation reports whether relPath — a fresh re-split, by THIS
// handler, of row's lossy display-string path — lands on the same segment
// shape (method plus segment count, static segments literal, {param}
// segments wildcard, exactly [router.Table.Match]'s own rule) as a spec
// operation OTHER than the one row actually, verifiably matched live.
//
// "Verifiably" means row.MatchedKind=="operation" with a MatchedID equal to
// the colliding operation's own id: that fact was set by routes.go's
// markTrafficMatch at serve time, against the router's real pre-rejoin
// segments, before row.Path's lossy string ever existed — so a collision
// with THAT SAME operation only confirms what the router already proved,
// while a collision with anything else is exactly the forged-boundary shape
// this handler's own call site warns about, and gets refused.
//
// ws.SpecID==nil (no spec attached) has nothing to shadow and always
// reports false; a workspace's operations are cheap to page fully (a spec
// is imported by one operator, not sized for this request's hot path the
// way the mock plane's own route table is).
func (s *Server) shadowedOperation(ctx context.Context, ws *workspaces.Workspace, row *traffic.Row, relPath string) (bool, error) {
	if ws.SpecID == nil {
		return false, nil
	}
	ops, err := s.specsRepo.Operations(ctx, *ws.SpecID, 0, 0)
	if err != nil {
		return false, err
	}
	method := strings.ToUpper(row.Method)
	candidate := pathSegments(relPath)
	for _, op := range ops {
		if strings.ToUpper(op.Method) != method {
			continue
		}
		if !shapeMatches(pathSegments(op.CanonicalPath), candidate) {
			continue
		}
		if row.MatchedKind == "operation" && row.MatchedID != nil && *row.MatchedID == op.ID {
			continue // the router already matched THIS operation live; not a forgery
		}
		return true, nil
	}
	return false, nil
}

// pathSegments splits an already-decoded absolute path on "/", dropping
// empty parts — the same boundary rule [router.Table.Match] and
// [router.CanonicalPath] both apply when they split a path, and the same
// split [router.BaseParamIndexes] documents its own returned indices are
// computed against, so a position from that function lines up with a
// position here without a second, possibly-disagreeing split rule. It is
// the ONE owner of that rule in this file: P3h shipped a byte-identical
// splitPathSegments beside it, which nothing tied to this one and no lint
// in this tree would have caught drifting apart. It must NOT
// percent-decode: relPath went through that exactly once already, inside
// mockplane, when [mockplane.NormalizeSegments] built the segments that
// row.Path was (lossily) rejoined from.
func pathSegments(path string) []string {
	raw := strings.Split(path, "/")
	segs := make([]string, 0, len(raw))
	for _, s := range raw {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}

// shapeMatches reports whether candidate satisfies pattern under
// [router.Table.Match]'s own rule: equal segment count, and every static
// pattern segment equal to candidate's at that position — "{}" (an
// operation's canonical form of any {param} segment) matches anything.
func shapeMatches(pattern, candidate []string) bool {
	if len(pattern) != len(candidate) {
		return false
	}
	for i, p := range pattern {
		if p != "{}" && p != candidate[i] {
			return false
		}
	}
	return true
}

// encodeBodyForVariant turns raw observed bytes into an
// [overrides.Variant]'s Body/BodyEncoding pair. Variant.Body is a
// jsonx.RawMessage: encoding/json requires it hold syntactically valid JSON
// to marshal into the stored responses column at all, so valid-JSON bytes
// are kept LITERAL (mode "pinned" with BodyEncoding "" serves ov.Body
// byte-for-byte, exactly matching what was observed) while anything else —
// plain text, HTML, binary — is base64-encoded into a JSON string, the one
// shape [overrides.ValidateVariant] and mockplane's own pinnedBody both
// already know how to decode. Empty input stays empty: nil Body, no
// encoding, a caller's own status-specific rule decides what that means.
func encodeBodyForVariant(raw []byte) (body jsonx.RawMessage, encoding string) {
	if len(raw) == 0 {
		return nil, ""
	}
	if jsonx.Valid(raw) {
		return jsonx.RawMessage(raw), ""
	}
	// jsonx.Marshal of a string can only fail on a value that cannot be
	// represented as JSON at all (never a plain string), so the error is
	// discarded rather than threaded through a signature no caller could
	// usefully react to.
	encoded, _ := jsonx.Marshal(base64.StdEncoding.EncodeToString(raw))
	return jsonx.RawMessage(encoded), "base64"
}

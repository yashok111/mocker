// Preset handlers implement the auth-preset surface DESIGN §10 requires but
// §14 does not name a route for (see the deviation note on GET/POST
// .../auth-preset in server.go's route registration): a preview call that
// derives a proposed set of recipe bindings from the workspace's spec
// without writing anything, and an apply call that writes exactly the
// (possibly edited) list the operator approved — never a re-derivation of
// its own, which is exactly the silent-override-of-a-human-edit §10 forbids.
package admin

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/yashok111/mocker/internal/authpreset"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/store"
)

// handleGetAuthPreset answers GET /api/workspaces/{id}/auth-preset: a
// [authpreset.Proposal] derived fresh from the workspace's current spec and
// settings. It writes NOTHING — see [authpreset.Derive]'s own doc comment —
// which is the whole reason this is a separate route from the apply POST
// below rather than one call that previews and writes in the same request.
//
// A workspace with no spec attached cannot propose anything meaningful (there
// is no schema to walk), but it still answers 200 with an empty proposal and
// a note explaining why, and it still mints a sample JWT from the workspace's
// own identity/auth settings — those need no spec at all — rather than
// answering an error for a state that is entirely ordinary (a workspace is
// created before a spec is attached to it).
func (s *Server) handleGetAuthPreset(w http.ResponseWriter, r *http.Request) {
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
		sampleJWT, err := recipes.MintJWT(ws.Settings.Auth, ws.Settings.Identity, nil, 0, time.Now())
		if err != nil {
			s.log.Error("mint sample jwt for spec-less workspace", "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to build auth preset")
			return
		}
		empty := &authpreset.Proposal{
			SampleJWT: sampleJWT,
			Notes:     []string{"workspace has no spec attached; nothing to propose"},
		}
		empty.NormalizeForWire()
		// No bindings, so no opKey could ever need an entry — a non-nil
		// EMPTY map (never nil, which marshals to `null`; see the
		// zero-binding branch of handleApplyAuthPreset for the identical
		// reasoning on the write side).
		httpx.JSON(w, http.StatusOK, authPresetView{Proposal: *empty, EditVersions: map[string]int64{}})
		return
	}
	specID := *ws.SpecID

	// Same three-query shape handleListOperations uses, for the same reason:
	// one call each for the operation list, the response variants and (here,
	// instead of overrides) the normalized document — never a query or a
	// parse per operation.
	ops, err := s.specsRepo.Operations(r.Context(), specID, 0, 0)
	if err != nil {
		s.log.Error("list operations for auth preset", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load operations")
		return
	}
	variants, err := s.specsRepo.Variants(r.Context(), specID)
	if err != nil {
		s.log.Error("list response variants for auth preset", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load response variants")
		return
	}

	// The same derivation mockplane/runtime.go's buildRuntime (steps 1-3)
	// uses, verbatim: the stored bytes are already dialect-normalized, so
	// re-running openapi.Load here is a pure re-parse, not a second,
	// potentially-diverging normalization pass.
	normalized, err := s.specsRepo.Normalized(r.Context(), specID)
	if err != nil {
		s.log.Error("load normalized document for auth preset", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load spec document")
		return
	}
	doc, _, err := openapi.Load(normalized)
	if err != nil {
		// Already-normalized, already-parsed-once-at-import-time bytes
		// failing to re-parse means the stored document itself is broken —
		// a server-side fact, never something this request did wrong.
		s.log.Error("re-load normalized document for auth preset", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load spec document")
		return
	}
	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	var authOps []authpreset.Operation
	for _, op := range ops {
		for _, v := range variants[op.ID] {
			authOps = append(authOps, authpreset.Operation{
				Method:    op.Method,
				Path:      op.Path,
				Status:    v.HTTPStatus,
				SchemaPtr: v.SchemaPtr,
				OpPointer: v.OpPointer,
			})
		}
	}

	// set is the WORKSPACE settings (domain.Settings), never
	// domain.AuthSettings alone — Derive needs both Identity and Auth, and
	// the two are adjacent struct parameters in exactly the shape P0's
	// post-mortem warns gets swapped silently.
	proposal, err := authpreset.Derive(resolver, doc, authOps, ws.Settings, time.Now())
	if err != nil {
		s.log.Error("derive auth preset", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to derive auth preset")
		return
	}
	proposal.NormalizeForWire()

	// D5/D8: this GET has no ForWorkspace read today — the apply handler's
	// was the only one in this file, and it moves inside PutManyExpecting's
	// transaction below. This read is new, added solely to build
	// editVersions; it changes nothing and the route stays read-only, which
	// scripts/smoke.sh asserts by name.
	existing, err := s.overridesRepo.ForWorkspace(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("load overrides for auth preset", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load existing overrides")
		return
	}
	editVersions := buildPresetEditVersions(proposal.Bindings, existing)

	httpx.JSON(w, http.StatusOK, authPresetView{Proposal: *proposal, EditVersions: editVersions})
}

// authPresetView is what GET .../auth-preset answers: the derived proposal,
// unchanged, plus EditVersions sitting BESIDE it (D5/D7's sibling rule —
// never a member of the embedded document). Keyed by opKey, one entry per
// op_overrides ROW (never per binding — D5), it carries 0 for an operation
// that has no row yet rather than omitting the key, because D7's absent-row
// expectation IS 0 and an absent map entry means something else entirely
// ("this opKey was not in the proposal"). POST .../auth-preset below expects
// the identical map back, under the identical name, as its own required
// expectation.
type authPresetView struct {
	authpreset.Proposal
	EditVersions map[string]int64 `json:"editVersions"`
}

// buildPresetEditVersions computes ONE entry per opKey the given bindings
// resolve to (never per binding — two bindings on one operation still
// collapse to the one op_overrides row they'd both write, D5), reading each
// row's current edit_version from existing or defaulting to 0 when the
// opKey has no row at all yet.
func buildPresetEditVersions(bindings []authpreset.Binding, existing map[string]*overrides.Row) map[string]int64 {
	versions := make(map[string]int64, len(bindings))
	for _, b := range bindings {
		key := overrides.OpKey(b.Method, b.Path)
		if row, ok := existing[key]; ok {
			versions[key] = row.EditVersion
		} else {
			versions[key] = 0
		}
	}
	return versions
}

// applyAuthPresetBody is what POST /auth-preset accepts: the SAME
// [authpreset.Binding] shape GET's Proposal.Bindings returns, filtered
// and/or edited by the operator. A Binding carries no opKey — that
// percent-encoding belongs to internal/overrides, which authpreset must not
// import (see [authpreset.Binding]'s own doc comment) — so this handler
// computes it itself via [overrides.OpKey].
//
// EditVersions is decoded as a plain (never pointer) map on purpose (D5/D12):
// a Go map already distinguishes an ABSENT field (nil) from a SENT-EMPTY one
// (`{}`, non-nil), the same distinction the four single-object routes need a
// `*int64` for. Its requiredness is enforced in the HANDLER, not here, and
// deliberately not via an unconditional non-nil check either — the
// zero-binding short-circuit below must still answer 200 with nothing to
// lose, so only the non-empty-bindings path may refuse a missing map.
type applyAuthPresetBody struct {
	Bindings     []authpreset.Binding `json:"bindings"`
	EditVersions map[string]int64     `json:"editVersions"`
}

// applyAuthPresetView is what a successful apply answers: how many bindings
// landed (so a caller can confirm a filtered subset applied as exactly that
// subset, not the full derived list), the workspace's new revision, and (A3,
// D5/D8) the fresh per-row edit_version of every op_overrides row this call
// touched — the preset's write response carries no rows of its own, so this
// map is the only way a caller can write the SAME operation again without
// re-reading first.
type applyAuthPresetView struct {
	Applied      int              `json:"applied"`
	Revision     int64            `json:"revision"`
	EditVersions map[string]int64 `json:"editVersions"`
}

// presetConflictDetails is D12's set-valued conflict payload for
// POST .../auth-preset: NOT editVersions (a different name, a different Go
// type, and it appears only in a 409 — D12 is explicit that conflating the
// two would force the request schema to accept nulls it must reject). Keyed
// by ONLY the opKeys that disagreed — never the whole table — with a nil
// value meaning that row is gone. The contrast that matters is nil versus NO
// ENTRY: an absent opKey did not disagree and needs nothing from the caller.
type presetConflictDetails struct {
	StaleVersions map[string]*int64 `json:"staleVersions"`
}

// handleApplyAuthPreset answers POST /api/workspaces/{id}/auth-preset: it
// writes EXACTLY the bindings the request body carries, grouped into
// [overrides.Row] values by operation and merged into any existing row's
// Responses (never replacing the row wholesale) so an operator who already
// pinned, say, a 409 body on the same operation does not lose it to an
// unrelated binding on a 200. It never re-derives a preset of its own and
// applies that instead — DESIGN §10 requires the operator's edits to the
// preview survive intact, and silently re-deriving would erase them.
//
// Every binding's recipe is validated up front with
// [recipes.Recipe.Validate]; every row is then written in ONE
// [overrides.Repo.PutMany] call, so a 40-binding preset bumps the workspace's
// revision exactly once instead of once per binding (PutMany's own doc
// comment) — applying it through the single-operation PUT this many times
// would rebuild the mock plane's runtime that many times too.
func (s *Server) handleApplyAuthPreset(w http.ResponseWriter, r *http.Request) { //nolint:gocyclo // one arm per refusal reason, and each names the reason it refuses
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

	var body applyAuthPresetBody
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if len(body.Bindings) == 0 {
		// Nothing to write: answer the current revision rather than pay for
		// a PutMany transaction (and a spurious revision bump — PutMany
		// bumps unconditionally, unlike Delete's "nothing changed" no-op)
		// that would invalidate the mock plane's cached runtime for no
		// actual change. Deliberately UNGUARDED (D5): nothing is written, so
		// there is no edit to lose, and PutManyExpecting is never reached on
		// this path — but the response still owes a non-nil EMPTY map, never
		// nil (which marshals to `null`, not `{}`, and would round-trip
		// invalid through internal/mcp's ApplyAuthPresetOutput).
		httpx.JSON(w, http.StatusOK, applyAuthPresetView{Applied: 0, Revision: ws.Revision, EditVersions: map[string]int64{}})
		return
	}

	for i, b := range body.Bindings {
		if b.Method == "" || b.Path == "" || b.Path[0] != '/' {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
				fmt.Sprintf("binding %d: method/path must name an operation", i))
			return
		}
		if b.DataPath == "" {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
				fmt.Sprintf("binding %d (%s %s): dataPath is required", i, b.Method, b.Path))
			return
		}
		if err := b.Recipe.Validate(); err != nil {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
				fmt.Sprintf("binding %d (%s %s %s): %v", i, b.Method, b.Path, b.DataPath, err))
			return
		}
	}

	// Every opKey the submitted bindings resolve to must have its own entry
	// in body.EditVersions (D5): a missing entry is a 400 naming the key,
	// never a silently unguarded row. Computed from the bindings alone —
	// this does not need the existing rows, only the keys they'd touch —
	// and iterated in SORTED order so which key a request with several
	// omissions is refused for is deterministic, not map-order flaky.
	touched := make(map[string]struct{})
	for _, b := range body.Bindings {
		touched[overrides.OpKey(b.Method, b.Path)] = struct{}{}
	}
	touchedKeys := make([]string, 0, len(touched))
	for k := range touched {
		touchedKeys = append(touchedKeys, k)
	}
	sort.Strings(touchedKeys)
	// Scoped to touchedKeys, not the caller's raw body.EditVersions: the GET
	// response (D5) hands the operator a version for every binding in the
	// derived proposal, and the UI naturally forwards that same map even
	// after the operator filters the submitted bindings down to a subset.
	// PutManyExpecting checks every key ITS expect map names, so passing the
	// unfiltered map would refuse this call over a row it never intended to
	// touch and that some OTHER write moved between the GET and this POST.
	expect := make(map[string]int64, len(touchedKeys))
	for _, key := range touchedKeys {
		v, ok := body.EditVersions[key]
		if !ok {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest,
				fmt.Sprintf("editVersions is required and must name opKey %q", key))
			return
		}
		expect[key] = v
	}

	// The merge below is handed to PutManyExpecting's callback UNCHANGED
	// from what it always did — it just reads "current" from the argument
	// PutManyExpecting supplies (drained inside its own transaction) instead
	// of a ForWorkspace call this handler used to make itself. Read/check
	// and write are now atomic (D8): two concurrent applies over the same
	// row can no longer both pass the check and both commit.
	merge := func(current map[string]*overrides.Row) ([]*overrides.Row, error) {
		rowsByKey := make(map[string]*overrides.Row)
		for _, b := range body.Bindings {
			key := overrides.OpKey(b.Method, b.Path)
			row, ok := rowsByKey[key]
			if !ok {
				if cur, present := current[key]; present {
					row = cur
				} else {
					row = &overrides.Row{
						Method:     b.Method,
						Path:       b.Path,
						OverrideOn: true,
						Responses:  map[string]overrides.Variant{},
					}
				}
				rowsByKey[key] = row
			}

			status := strconv.Itoa(b.Status)
			variant := row.Responses[status] // zero Variant when this status has no entry yet
			if variant.Recipes == nil {
				variant.Recipes = map[string]recipes.Recipe{}
			}
			variant.Recipes[b.DataPath] = b.Recipe
			row.Responses[status] = variant // Variant is a map value, not a pointer: write it back
		}

		// Sorted so PutMany's write order (and this handler's own behavior)
		// is deterministic run to run — matters for tests, not for
		// correctness, since each row is a distinct (workspace, method,
		// path) key regardless of order.
		keys := make([]string, 0, len(rowsByKey))
		for k := range rowsByKey {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		rows := make([]*overrides.Row, len(keys))
		for i, k := range keys {
			rows[i] = rowsByKey[k]
		}
		return rows, nil
	}

	newVersions, revision, err := s.overridesRepo.PutManyExpecting(r.Context(), ws.ID, expect, merge)
	var conflict *store.EditConflictError
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, applyAuthPresetView{Applied: len(body.Bindings), Revision: revision, EditVersions: newVersions})
	case errors.As(err, &conflict):
		stale, ok := conflict.Current.(map[string]*int64)
		if !ok {
			s.log.Error("auth preset edit conflict: unexpected payload type", "type", fmt.Sprintf("%T", conflict.Current))
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to build conflict details")
			return
		}
		httpx.ErrDetails(w, http.StatusConflict, codeEditConflict,
			"one or more auth preset bindings were changed by another write", presetConflictDetails{StaleVersions: stale})
	case errors.Is(err, overrides.ErrWorkspaceNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "workspace not found")
	case errors.Is(err, overrides.ErrInvalidRow):
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
	default:
		s.log.Error("apply auth preset", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to apply auth preset")
	}
}

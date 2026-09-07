package admin

import (
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/resources"
)

// A11 (2026-09-02): the admin plane's two writes into a confirmed family's
// rows — PUT and DELETE /api/workspaces/{id}/resources/{family}/entities/{key}
// — the siblings of A4's read. Until now the ONLY writers of entity rows
// were the mock plane's anonymous POST X (which mints the next key, never a
// chosen one) and DELETE X/{}, plus the wholesale verbs (confirm, reseed,
// clear, a restoreData rollback). "Give user 42 status blocked" had no
// verb. It still is the read corner's shape — addressed by route_family and
// a key, never by resources.id or entities.id (an id survives neither a
// decline-then-reconfirm nor a restore) — and still not DESIGN.md:936's
// full CRUD by :rid, recorded in CARVE-OUTS.md beside A4's own entry.
//
// Neither route bumps revision (an entity write changes nothing the
// runtime cache keys on, D13 clause 23) and neither takes an auto
// checkpoint (they change only entities — the same reasoning reset-data's
// own comment gives), so the undo for a wrong Set is a checkpoint taken
// before it, restored with restoreData: true.

const (
	// codeEntityNotFound is DELETE's 404 for a key the family does not
	// hold — the same code the mock plane's own detail route answers. It is
	// NOT a resources sentinel: "no such row" is a false return, not an
	// error, so it has no row in resources' entity-write table.
	codeEntityNotFound = "entity_not_found"
)

// entityWriteRefusal is the ONE place this plane turns a resources write
// failure into an answer. The STATUS and the human message are the admin
// plane's own choice — the mock plane deliberately answers differently for
// the same sentinel (ErrResourceGone falls through to the generator there,
// D13 clause 34/R37) — but the CODE comes from resources' entity-write
// table, so the two planes and the Lua host cannot drift apart on the word
// the way they used to: `write_busy` alone was a bare literal at four
// sites in two packages, with no constant behind any of them.
//
// idType is only read by the one message that names it. ok is false for
// anything the table does not name, which both callers answer as their own
// 500 after logging — exactly what the default arm of each switch did.
//
// Delete returns only ErrResourceGone and ErrWriteBusy, so sharing this
// with the DELETE handler adds arms it cannot reach rather than changing
// any answer it can.
func entityWriteRefusal(err error, idType string) (status int, code, message string, ok bool) {
	code, ok = resources.WriteRefusalCode(err)
	if !ok {
		return 0, "", "", false
	}
	switch {
	case errors.Is(err, resources.ErrResourceGone):
		return http.StatusNotFound, code, "unknown route family", true
	case errors.Is(err, resources.ErrEntityLimit):
		return http.StatusConflict, code, "resource is at its entity limit (rows or bytes)", true
	case errors.Is(err, resources.ErrEntityKeyConflict):
		return http.StatusConflict, code, "an entity with this key already exists in another base scope of the family", true
	case errors.Is(err, resources.ErrEntityKeyNotCanonical):
		return http.StatusBadRequest, code,
			"entity key must be the canonical form of the family's id type (" + idType + ")", true
	case errors.Is(err, resources.ErrWriteBusy):
		return http.StatusServiceUnavailable, code, "writer busy, try again", true
	}
	// The table named it and this switch did not: a row was added to
	// resources without a status here. A 500 with the table's code is
	// honest about both halves — the name is right, the status is not
	// chosen — and the completeness test in resources is what stops it
	// from ever shipping.
	return http.StatusInternalServerError, code, "unhandled resource write refusal", true
}

// entityKeyRe is what a key may be: a URL segment's unreserved characters,
// 1..128 of them. The mock plane's own keys are decimal integers; a family
// whose id type is a string may hold any of these. "/" is out because the
// key is a path segment on both planes, "." alone and ".." because they are
// path syntax, not names.
var entityKeyRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,128}$`)

// setResourceEntityBody is PUT's request body.
type setResourceEntityBody struct {
	Data         map[string]any `json:"data"`
	ScopeKey     string         `json:"scopeKey"`
	BaseScopeKey string         `json:"baseScopeKey"`
}

// deleteResourceEntityBody is DELETE's request body — the scope the key is
// addressed under; both default to "", the top-level scope.
type deleteResourceEntityBody struct {
	ScopeKey     string `json:"scopeKey"`
	BaseScopeKey string `json:"baseScopeKey"`
}

// setResourceEntityView is PUT's answer: the stored row and whether the
// call inserted it (true) or replaced an existing one.
type setResourceEntityView struct {
	Row     resourceEntityView `json:"row"`
	Created bool               `json:"created"`
}

func entityKeyFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.PathValue("key")
	if key == "." || key == ".." || !entityKeyRe.MatchString(key) {
		// The same code the canonical-form refusal answers
		// (resources.ErrEntityKeyNotCanonical): both say "this key cannot
		// address a row", and a caller has no use for telling the segment
		// alphabet apart from the id type.
		httpx.Err(w, http.StatusBadRequest, resources.CodeEntityInvalidKey,
			"entity key must be 1..128 characters of [A-Za-z0-9._~-] and not \".\" or \"..\"")
		return "", false
	}
	return key, true
}

// handleSetResourceEntity answers PUT .../resources/{family}/entities/{key}.
func (s *Server) handleSetResourceEntity(w http.ResponseWriter, r *http.Request) {
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
	family := r.PathValue("family")
	key, ok := entityKeyFromPath(w, r)
	if !ok {
		return
	}

	var body setResourceEntityBody
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}
	if body.Data == nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "data is required and must be a JSON object")
		return
	}

	res, err := s.confirmedResourceByFamily(r.Context(), ws.ID, family)
	if err != nil {
		s.log.Error("set resource entity: resolve family", "workspace", ws.Slug, "family", family, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to resolve resource family")
		return
	}
	if res == nil {
		httpx.Err(w, http.StatusNotFound, codeResourceUnknownFamily, "unknown route family")
		return
	}

	row, created, err := s.resourcesRepo.Set(r.Context(), res.ID,
		resources.ScopeKey(body.BaseScopeKey), resources.ScopeKey(body.ScopeKey),
		key, res.IDField, res.Wrapper.IDType, body.Data)
	if err != nil {
		if status, code, message, named := entityWriteRefusal(err, res.Wrapper.IDType); named {
			httpx.Err(w, status, code, message)
			return
		}
		s.log.Error("set resource entity", "workspace", ws.Slug, "family", family, "key", key, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to write resource entity")
		return
	}
	httpx.JSON(w, http.StatusOK, setResourceEntityView{
		Row: resourceEntityView{
			ID: row.ID, EntityKey: row.EntityKey, ScopeKey: row.ScopeKey, BaseScopeKey: row.BaseScopeKey,
			Data: row.Data, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		},
		Created: created,
	})
}

// handleDeleteResourceEntity answers DELETE .../resources/{family}/entities/{key}.
// A body is optional (a DELETE with none addresses the top-level scope);
// when present it names the scope. No confirmSlug: the mock plane's
// anonymous DELETE X/{} removes the same one row with no confirmation at
// all, and the field guards verbs that destroy MANY rows of
// workspace-created data.
func (s *Server) handleDeleteResourceEntity(w http.ResponseWriter, r *http.Request) {
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
	family := r.PathValue("family")
	key, ok := entityKeyFromPath(w, r)
	if !ok {
		return
	}

	var body deleteResourceEntityBody
	if err := decodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}

	res, err := s.confirmedResourceByFamily(r.Context(), ws.ID, family)
	if err != nil {
		s.log.Error("delete resource entity: resolve family", "workspace", ws.Slug, "family", family, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to resolve resource family")
		return
	}
	if res == nil {
		httpx.Err(w, http.StatusNotFound, codeResourceUnknownFamily, "unknown route family")
		return
	}

	deleted, err := s.resourcesRepo.Delete(r.Context(), res.ID,
		resources.ScopeKey(body.BaseScopeKey), resources.ScopeKey(body.ScopeKey), key)
	if err != nil {
		if status, code, message, named := entityWriteRefusal(err, res.Wrapper.IDType); named {
			httpx.Err(w, status, code, message)
			return
		}
		s.log.Error("delete resource entity", "workspace", ws.Slug, "family", family, "key", key, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to delete resource entity")
		return
	}
	if !deleted {
		httpx.Err(w, http.StatusNotFound, codeEntityNotFound, "no entity with that key in that scope")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

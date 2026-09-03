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
	// hold — the same code the mock plane's own detail route answers.
	codeEntityNotFound = "entity_not_found"
	// codeEntityLimit is the 409 both routes answer over a cap, the same
	// code and status the mock plane's POST gives.
	codeEntityLimit = "entity_limit"
	// codeEntityInvalidKey refuses a key outside the segment alphabet, or
	// one that is not the canonical form of the family's id type.
	codeEntityInvalidKey = "invalid_entity_key"
	// codeEntityKeyConflict is PUT's 409 for a key that already exists in
	// another base scope of the family (resources.ErrEntityKeyConflict).
	codeEntityKeyConflict = "entity_key_conflict"
)

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
		httpx.Err(w, http.StatusBadRequest, codeEntityInvalidKey,
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
		switch {
		case errors.Is(err, resources.ErrResourceGone):
			httpx.Err(w, http.StatusNotFound, codeResourceUnknownFamily, "unknown route family")
		case errors.Is(err, resources.ErrEntityLimit):
			httpx.Err(w, http.StatusConflict, codeEntityLimit, "resource is at its entity limit (rows or bytes)")
		case errors.Is(err, resources.ErrEntityKeyConflict):
			httpx.Err(w, http.StatusConflict, codeEntityKeyConflict, "an entity with this key already exists in another base scope of the family")
		case errors.Is(err, resources.ErrEntityKeyNotCanonical):
			httpx.Err(w, http.StatusBadRequest, codeEntityInvalidKey,
				"entity key must be the canonical form of the family's id type ("+res.Wrapper.IDType+")")
		case errors.Is(err, resources.ErrWriteBusy):
			httpx.Err(w, http.StatusServiceUnavailable, "write_busy", "writer busy, try again")
		default:
			s.log.Error("set resource entity", "workspace", ws.Slug, "family", family, "key", key, "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to write resource entity")
		}
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
		switch {
		case errors.Is(err, resources.ErrResourceGone):
			httpx.Err(w, http.StatusNotFound, codeResourceUnknownFamily, "unknown route family")
		case errors.Is(err, resources.ErrWriteBusy):
			httpx.Err(w, http.StatusServiceUnavailable, "write_busy", "writer busy, try again")
		default:
			s.log.Error("delete resource entity", "workspace", ws.Slug, "family", family, "key", key, "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to delete resource entity")
		}
		return
	}
	if !deleted {
		httpx.Err(w, http.StatusNotFound, codeEntityNotFound, "no entity with that key in that scope")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

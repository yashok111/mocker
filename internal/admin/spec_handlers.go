// Spec handlers implement DESIGN §14's /api/specs surface: importing an
// OpenAPI document, listing and inspecting imported specs, paging their
// indexed operations, reading the import report, and deleting a spec.
//
// Every handler here answers through [specs.Repo] (constructed once in
// [Server.New]) and never reaches into [store.DB] directly — the same split
// [Server.ws] already keeps for workspaces. Ownership is the same
// "logged-in, not per-user" model DESIGN §15 uses everywhere on this plane
// (see the note atop workspace_handlers.go): any authenticated caller may
// import, read or delete any spec.
package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/specs"
)

// Paging defaults for GET /api/specs/{id}/operations. defaultOperationsLimit
// is used when the caller omits ?limit=; maxOperationsLimit is a hard
// ceiling no request can exceed, however large a value it asks for — a
// runaway UI bug or a scripted client must not be able to force one query to
// load an entire large spec's operations in a single response.
const (
	defaultOperationsLimit = 100
	maxOperationsLimit     = 500
)

// specView is the wire shape of a [specs.Spec]. It never includes the raw or
// normalized document bytes — those are multi-KB blobs no admin screen needs
// alongside a spec's metadata, and [specs.Repo] itself never loads them
// outside the two methods (Import, Report) that actually need them.
type specView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Format    string `json:"format"`
	Source    string `json:"source"`
	SourceRef string `json:"sourceRef"`
	BasePath  string `json:"basePath"`
	Hash      string `json:"hash"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy *int64 `json:"createdBy"`
}

func newSpecView(sp *specs.Spec) specView {
	return specView{
		ID:        sp.ID,
		Name:      sp.Name,
		Version:   sp.Version,
		Format:    sp.Format,
		Source:    sp.Source,
		SourceRef: sp.SourceRef,
		BasePath:  sp.BasePath,
		Hash:      sp.Hash,
		CreatedAt: sp.CreatedAt.Unix(),
		CreatedBy: sp.CreatedBy,
	}
}

// warningView is the wire shape of an [openapi.Warning].
type warningView struct {
	Pointer string `json:"pointer"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// reportView is the wire shape of an [openapi.Report].
type reportView struct {
	Format         string        `json:"format"`
	BasePath       string        `json:"basePath"`
	BasePathOrigin string        `json:"basePathOrigin"`
	Warnings       []warningView `json:"warnings"`
	Operations     int           `json:"operations"`
	Degraded       int           `json:"degraded"`
}

func newReportView(rep *openapi.Report) *reportView {
	if rep == nil {
		return nil
	}
	warnings := make([]warningView, len(rep.Warnings))
	for i, w := range rep.Warnings {
		warnings[i] = warningView{Pointer: w.Pointer, Code: w.Code, Message: w.Message}
	}
	return &reportView{
		Format:         string(rep.Format),
		BasePath:       rep.BasePath,
		BasePathOrigin: string(rep.BasePathOrigin),
		Warnings:       warnings,
		Operations:     rep.Operations,
		Degraded:       rep.Degraded,
	}
}

// specImportView is what POST /api/specs answers: the stored (or, on a
// duplicate, pre-existing) spec, whether this call actually inserted a new
// row, and the report [specs.Repo.Import] computed while loading the
// document — handing it back here saves the admin UI an immediate follow-up
// GET .../report right after upload.
type specImportView struct {
	specView
	Duplicate bool        `json:"duplicate"`
	Report    *reportView `json:"report"`
}

func newSpecImportView(result *specs.ImportResult, duplicate bool) specImportView {
	return specImportView{
		specView:  newSpecView(result.Spec),
		Duplicate: duplicate,
		Report:    newReportView(result.Report),
	}
}

// operationView is the wire shape of a [specs.Operation].
type operationView struct {
	ID            int64   `json:"id"`
	SpecID        int64   `json:"specId"`
	Method        string  `json:"method"`
	Path          string  `json:"path"`
	CanonicalPath string  `json:"canonicalPath"`
	OperationID   *string `json:"operationId"`
	Summary       *string `json:"summary"`
	Tag           *string `json:"tag"`
	SourceOrder   int64   `json:"sourceOrder"`
	Pointer       string  `json:"pointer"`
	ParseError    *string `json:"parseError"`
}

func newOperationView(op *specs.Operation) operationView {
	return operationView{
		ID:            op.ID,
		SpecID:        op.SpecID,
		Method:        op.Method,
		Path:          op.Path,
		CanonicalPath: op.CanonicalPath,
		OperationID:   op.OperationID,
		Summary:       op.Summary,
		Tag:           op.Tag,
		SourceOrder:   op.SourceOrder,
		Pointer:       op.Pointer,
		ParseError:    op.ParseError,
	}
}

// specImportBody is POST /api/specs's request shape. Document carries the
// WHOLE spec document as a JSON string (not multipart, not a raw body) —
// deliberately, per DESIGN §8: the admin CSRF gate in security.go hard-
// requires Content-Type: application/json, and loosening that for one upload
// route would reopen exactly the CSRF hole that requirement exists to close.
type specImportBody struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Document string `json:"document"`
}

// handleImportSpec imports a document as a new spec (POST /api/specs).
//
// source "url" is deferred to a later phase: it is refused here with 400 rather than
// attempted, and this handler makes no outbound request of any kind for any
// source value. A [specs.ErrDuplicate] result is NOT an error response: DESIGN
// §7's dedup-by-hash means re-uploading the same bytes is a normal, idempotent
// action, answered 200 with the pre-existing spec and duplicate:true rather
// than 409 — a fresh import instead answers 201.
func (s *Server) handleImportSpec(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}

	var body specImportBody
	if err := decodeJSON(r, &body); err != nil {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		return
	}

	switch body.Source {
	case "upload":
		// proceed below.
	case "url":
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "URL import is not available yet (a later phase)")
		return
	default:
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, `source must be "upload"`)
		return
	}
	if strings.TrimSpace(body.Document) == "" {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "document is required")
		return
	}

	userID := user.ID
	result, err := s.specsRepo.Import(r.Context(), specs.ImportInput{
		Name: body.Name,
		// SourceRef stays empty: P1a's only accepted source is "upload",
		// which — unlike a future url or bundle-entry source — has no
		// reference to record.
		Source:    body.Source,
		SourceRef: "",
		Document:  []byte(body.Document),
		CreatedBy: &userID,
	})
	if errors.Is(err, specs.ErrDuplicate) {
		httpx.JSON(w, http.StatusOK, newSpecImportView(result, true))
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, specs.ErrTooLarge):
			httpx.Err(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "document exceeds the maximum allowed size")
		case errors.Is(err, specs.ErrTooManyOperations):
			httpx.Err(w, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge, "document has too many operations to import")
		case errors.Is(err, specs.ErrNotADocument):
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "input is not a valid OpenAPI document")
		case errors.Is(err, specs.ErrUnsupportedFormat):
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "unsupported format: Swagger 2.0 and YAML input land in a later phase")
		default:
			// HARD RULE 8: the import invariant covers the DOCUMENT, not the
			// database. A locked or full database is a genuine server fault,
			// and reporting it as 400 would send the operator hunting a bad
			// upload that was never the problem.
			s.log.Error("import spec", "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to import spec")
		}
		return
	}
	httpx.JSON(w, http.StatusCreated, newSpecImportView(result, false))
}

// handleListSpecs answers every imported spec (GET /api/specs).
func (s *Server) handleListSpecs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}

	list, err := s.specsRepo.List(r.Context())
	if err != nil {
		s.log.Error("list specs", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list specs")
		return
	}
	out := make([]specView, len(list))
	for i, sp := range list {
		out[i] = newSpecView(sp)
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleGetSpec answers one spec by id (GET /api/specs/{id}).
func (s *Server) handleGetSpec(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseSpecID(w, r)
	if !ok {
		return
	}

	sp, err := s.specsRepo.ByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, specs.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "spec not found")
			return
		}
		s.log.Error("get spec", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load spec")
		return
	}
	httpx.JSON(w, http.StatusOK, newSpecView(sp))
}

// handleSpecReport answers specID's freshly re-derived import report (GET
// /api/specs/{id}/report). See [specs.Repo.Report]'s doc comment for why this
// is always recomputed rather than read from a stored column.
func (s *Server) handleSpecReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseSpecID(w, r)
	if !ok {
		return
	}

	report, err := s.specsRepo.Report(r.Context(), id)
	if err != nil {
		if errors.Is(err, specs.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "spec not found")
			return
		}
		s.log.Error("spec report", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load spec report")
		return
	}
	httpx.JSON(w, http.StatusOK, newReportView(report))
}

// handleSpecOperations answers a page of specID's indexed operations (GET
// /api/specs/{id}/operations?limit=&offset=). limit defaults to
// defaultOperationsLimit and is clamped to maxOperationsLimit; offset
// defaults to 0. Both are passed straight through to [specs.Repo.Operations],
// which pages in SQL — this handler never loads a full operation set just to
// slice it.
func (s *Server) handleSpecOperations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseSpecID(w, r)
	if !ok {
		return
	}
	// A missing spec must answer 404, not 200 with an empty page: Operations
	// itself has no way to distinguish "spec absent" from "spec present with
	// zero operations", since its WHERE clause matches nothing either way.
	if _, err := s.specsRepo.ByID(r.Context(), id); err != nil {
		if errors.Is(err, specs.ErrNotFound) {
			httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "spec not found")
			return
		}
		s.log.Error("lookup spec for operations", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to load spec")
		return
	}

	limit := defaultOperationsLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		// n < 1, not n < 0: [specs.Repo.Operations] treats limit <= 0 as its
		// "no LIMIT clause at all" sentinel, so ?limit=0 must be rejected
		// here rather than let it slip through as a request for zero rows —
		// otherwise it silently becomes a request for EVERY row, bypassing
		// maxOperationsLimit entirely.
		if err != nil || n < 1 {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	if limit > maxOperationsLimit {
		limit = maxOperationsLimit
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid offset")
			return
		}
		offset = n
	}

	ops, err := s.specsRepo.Operations(r.Context(), id, limit, offset)
	if err != nil {
		s.log.Error("list spec operations", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list operations")
		return
	}
	out := make([]operationView, len(ops))
	for i, op := range ops {
		out[i] = newOperationView(op)
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleDeleteSpec deletes a spec (DELETE /api/specs/{id}). A spec still
// attached to at least one workspace is refused with 409, naming the
// attached workspaces' slugs in the error details — [specs.ErrAttached] is a
// bare sentinel and carries nothing itself, so the slugs are fetched with a
// separate call.
func (s *Server) handleDeleteSpec(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseSpecID(w, r)
	if !ok {
		return
	}

	err := s.specsRepo.Delete(r.Context(), id)
	switch {
	case err == nil:
		httpx.NoContent(w)
	case errors.Is(err, specs.ErrNotFound):
		httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, "spec not found")
	case errors.Is(err, specs.ErrAttached):
		slugs, aerr := s.specsRepo.AttachedWorkspaces(r.Context(), id)
		if aerr != nil {
			s.log.Error("list attached workspaces", "err", aerr)
			slugs = nil
		}
		httpx.ErrDetails(w, http.StatusConflict, httpx.CodeConflict, "spec is attached to a workspace", slugs)
	default:
		s.log.Error("delete spec", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to delete spec")
	}
}

// parseSpecID extracts and validates the {id} path value, answering 400 and
// reporting failure on anything that is not a positive integer. Mirrors
// parseWorkspaceID in workspace_handlers.go.
func parseSpecID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid spec id")
		return 0, false
	}
	return id, true
}

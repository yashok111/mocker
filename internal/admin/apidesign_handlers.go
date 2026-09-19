package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/specs"
)

func designActor(r *http.Request) string {
	if _, ok := sessionFrom(r.Context()); ok {
		return "ui"
	}
	return "mcp"
}

func (s *Server) designError(w http.ResponseWriter, err error) {
	if invalid, ok := errors.AsType[*apidesign.InvalidError](err); ok {
		httpx.ErrDetails(w, 400, "design_invalid", err.Error(), invalid)
		return
	}
	if conflict, ok := errors.AsType[*apidesign.ConflictError](err); ok {
		httpx.ErrDetails(w, 409, "design_conflict", err.Error(), conflict)
		return
	}
	switch {
	case errors.Is(err, apidesign.ErrNotFound):
		httpx.Err(w, 404, httpx.CodeNotFound, "Проект или версия не найдены")
	case errors.Is(err, apidesign.ErrForbidden):
		httpx.Err(w, 403, httpx.CodeForbidden, "Публикацию подтверждает аналитик в UI")
	case errors.Is(err, apidesign.ErrConflict):
		httpx.Err(w, 409, "design_conflict", "Исходный workspace изменился во время импорта; повторите импорт")
	case errors.Is(err, specs.ErrTooLarge):
		httpx.Err(w, 413, httpx.CodeTooLarge, "Документ превышает допустимый размер")
	default:
		s.log.Error("API design", "err", err)
		httpx.Err(w, 500, httpx.CodeInternal, "Не удалось выполнить действие с проектом")
	}
}
func designPathID(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil || id <= 0 {
		httpx.Err(w, 404, httpx.CodeNotFound, "Проект или версия не найдены")
		return 0, false
	}
	return id, true
}
func (s *Server) designRequest(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if _, ok := s.requireUser(w, r); !ok {
		return 0, false
	}
	return designPathID(w, r, "id")
}
func (s *Server) designBody(w http.ResponseWriter, r *http.Request, body any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBody)
	return decodeBody(w, r, body)
}
func (s *Server) designURLs(r *http.Request, d *apidesign.Design) error {
	draft, err := s.ws.ByID(r.Context(), d.DraftWorkspaceID)
	if err != nil {
		return err
	}
	published, err := s.ws.ByID(r.Context(), d.PublishedWorkspaceID)
	if err != nil {
		return err
	}
	d.DraftURL = s.workspaceURL(r, draft)
	d.PublishedURL = s.workspaceURL(r, published)
	return nil
}
func (s *Server) designDetailResponse(w http.ResponseWriter, r *http.Request, status int, d *apidesign.Detail, err error) {
	if err == nil {
		err = s.designURLs(r, &d.Design)
	}
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, status, d)
}

func (s *Server) handleListAPIDesigns(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	list, err := s.designsRepo.List(r.Context())
	if err != nil {
		s.designError(w, err)
		return
	}
	for i := range list {
		if err = s.designURLs(r, &list[i]); err != nil {
			s.designError(w, err)
			return
		}
	}
	httpx.JSON(w, 200, map[string]any{"designs": list})
}
func (s *Server) handleCreateAPIDesign(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name        string  `json:"name"`
		Document    *string `json:"document"`
		WorkspaceID *int64  `json:"workspaceId"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	if body.Document != nil && body.WorkspaceID != nil {
		s.designError(w, &apidesign.InvalidError{Diagnostics: []apidesign.Diagnostic{{Pointer: "/workspaceId", Message: "document и workspaceId нельзя передавать вместе", Severity: "error"}}})
		return
	}
	in := apidesign.CreateInput{Name: body.Name, Source: designActor(r), OwnerID: &user.ID}
	if body.Document != nil {
		in.Document = *body.Document
		if in.Document == "" {
			s.designError(w, &apidesign.InvalidError{Diagnostics: []apidesign.Diagnostic{{Pointer: "/document", Message: "Документ не может быть пустым", Severity: "error"}}})
			return
		}
	}
	if body.WorkspaceID != nil {
		ws, ok := s.loadWorkspace(w, r, *body.WorkspaceID)
		if !ok {
			return
		}
		// Revision is monotonic across every contract mutation. Capture before export
		// and verify it under the final write lock, so independently read layers cannot
		// become an accepted torn snapshot.
		in.WorkspaceID = ws.ID
		in.WorkspaceRevision = ws.Revision
		exportReq := r.Clone(r.Context())
		exportReq.SetPathValue("id", strconv.FormatInt(ws.ID, 10))
		capture := newCaptureResponse()
		s.handleExportOpenAPI(capture, exportReq)
		if capture.status != 200 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(capture.status)
			_, _ = w.Write(capture.body.Bytes())
			return
		}
		in.Document = capture.body.String()
	}
	d, err := s.designsRepo.Create(r.Context(), in)
	s.designDetailResponse(w, r, 201, d, err)
}
func (s *Server) handleGetAPIDesign(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	d, err := s.designsRepo.Detail(r.Context(), id)
	s.designDetailResponse(w, r, 200, d, err)
}
func (s *Server) handleSaveAPIDesignDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64  `json:"expectedVersion"`
		Document        string `json:"document"`
		Summary         string `json:"summary"`
		ChangeSetID     *int64 `json:"changeSetId"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	d, err := s.designsRepo.Save(r.Context(), id, apidesign.SaveInput{ExpectedVersion: body.ExpectedVersion, Document: body.Document, Summary: body.Summary, ChangeSetID: body.ChangeSetID, Source: designActor(r)})
	s.designDetailResponse(w, r, 200, d, err)
}
func (s *Server) handleGetAPIDesignRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	rid, ok := designPathID(w, r, "rid")
	if !ok {
		return
	}
	v, err := s.designsRepo.Revision(r.Context(), id, rid)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, v)
}
func (s *Server) handleGetAPIDesignDiff(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var ids [2]int64
	for i, key := range []string{"fromRevisionId", "toRevisionId"} {
		if raw, exists := r.URL.Query()[key]; exists {
			var err error
			if len(raw) != 1 {
				httpx.Err(w, 404, httpx.CodeNotFound, "Версия не найдена")
				return
			}
			ids[i], err = strconv.ParseInt(raw[0], 10, 64)
			if err != nil || ids[i] <= 0 {
				httpx.Err(w, 404, httpx.CodeNotFound, "Версия не найдена")
				return
			}
		}
	}
	diff, err := s.designsRepo.Diff(r.Context(), id, ids[0], ids[1])
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, diff)
}
func (s *Server) handleValidateAPIDesign(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	if _, err := s.designsRepo.Detail(r.Context(), id); err != nil {
		s.designError(w, err)
		return
	}
	var body struct {
		Document string `json:"document"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	diagnostics, err := s.designsRepo.Validate(body.Document)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"valid": len(diagnostics) == 0, "diagnostics": diagnostics})
}
func (s *Server) handleCreateAPIDesignChangeSet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64  `json:"expectedVersion"`
		Title           string `json:"title"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	set, err := s.designsRepo.CreateChangeSet(r.Context(), id, body.ExpectedVersion, body.Title, designActor(r))
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 201, set)
}
func (s *Server) handleCloseAPIDesignChangeSet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	cid, ok := designPathID(w, r, "cid")
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	set, err := s.designsRepo.CloseChangeSet(r.Context(), id, cid, body.ExpectedVersion)
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, set)
}
func (s *Server) handleRequestAPIDesignReview(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64  `json:"expectedVersion"`
		Summary         string `json:"summary"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	review, err := s.designsRepo.RequestReview(r.Context(), id, body.ExpectedVersion, body.Summary, designActor(r))
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 201, review)
}
func (s *Server) handlePublishAPIDesignReview(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	// Defense in depth: direct handler calls must never turn MCP identity into a
	// human confirmation merely by bypassing the route allowlist.
	if _, ok := sessionFrom(r.Context()); !ok {
		s.designError(w, apidesign.ErrForbidden)
		return
	}
	rid, ok := designPathID(w, r, "rid")
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	release, err := s.designsRepo.Publish(r.Context(), id, rid, body.ExpectedVersion, designActor(r))
	if err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, 200, release)
}
func (s *Server) handleRestoreAPIDesignRevision(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64  `json:"expectedVersion"`
		RevisionID      int64  `json:"revisionId"`
		Summary         string `json:"summary"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	d, err := s.designsRepo.Restore(r.Context(), id, body.RevisionID, body.ExpectedVersion, body.Summary, designActor(r))
	s.designDetailResponse(w, r, 200, d, err)
}

func (s *Server) exportManagedContract(w http.ResponseWriter, r *http.Request, workspaceID int64) bool {
	id, err := s.ws.ManagedDesign(r.Context(), workspaceID)
	if err != nil {
		s.designError(w, err)
		return true
	}
	if id == 0 {
		return false
	}
	detail, err := s.designsRepo.Detail(r.Context(), id)
	if err != nil {
		s.designError(w, err)
		return true
	}
	revision := &detail.Draft
	if workspaceID == detail.Design.PublishedWorkspaceID {
		revision = detail.Published
	}
	if revision == nil {
		httpx.Err(w, 404, httpx.CodeNotFound, "API ещё не опубликован")
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if queryFlag(r, "download") {
		w.Header().Set("Content-Disposition", `attachment; filename="api-contract.json"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(revision.Document))
	return true
}

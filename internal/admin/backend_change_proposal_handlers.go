package admin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/url"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func backendChangeProposalQuery(r *http.Request, detail bool) (url.Values, int, error) {
	if r.ContentLength != 0 || r.TransferEncoding != nil {
		return nil, 0, backendQueryError()
	}
	query, limit, err := backendProposalQuery(r, detail)
	if err != nil {
		return nil, 0, err
	}
	if len(query.Get("cursor")) > 1024 {
		return nil, 0, backendQueryError()
	}
	if query.Has("limit") {
		if _, err := backendPositiveDecimal(query.Get("limit")); err != nil {
			return nil, 0, err
		}
	}
	return query, limit, nil
}

func (s *Server) backendChangeProposalBody(w http.ResponseWriter, r *http.Request, out any) bool {
	var raw jsontext.Value
	if !s.backendNoQuery(w, r) || !s.backendBodyLimit(w, r, &raw, min(s.cfg.MaxBody, int64(backendmodel.MaxChangeProposalCommandBytes))) {
		return false
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		s.backendError(w, backendQueryError())
		return false
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		s.backendError(w, backendQueryError())
		return false
	}
	for _, key := range []string{"baseRevisionId", "proposalRevisionId", "restoreRevisionId"} {
		if value, ok := fields[key]; ok {
			var id string
			if json.Unmarshal(value, &id) != nil || !backendmodel.ValidID(id) {
				s.backendError(w, backendQueryError())
				return false
			}
		}
	}
	if value, ok := fields["candidateHash"]; ok {
		var hash string
		if json.Unmarshal(value, &hash) != nil || !backendSHA256(hash) {
			s.backendError(w, backendQueryError())
			return false
		}
	}
	return true
}
func (s *Server) handleListBackendChangeProposals(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, limit, err := backendChangeProposalQuery(r, false)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.ListChangeProposals(r.Context(), r.PathValue("id"), backendmodel.ChangeProposalListInput{BaseRevisionID: q.Get("baseRevisionId"), Status: q.Get("status"), Limit: limit, Cursor: q.Get("cursor")})
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleCreateBackendChangeProposal(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.CreateChangeProposalInput
	if !s.backendChangeProposalBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.CreateChangeProposal(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendChangeProposal(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, limit, err := backendChangeProposalQuery(r, true)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.GetChangeProposal(r.Context(), r.PathValue("id"), r.PathValue("pid"), backendmodel.GetChangeProposalInput{ProposalRevisionID: q.Get("proposalRevisionId"), Limit: limit, Cursor: q.Get("cursor")})
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handlePreviewBackendChangeProposalCommands(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.PreviewChangeProposalInput
	if !s.backendChangeProposalBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.PreviewChangeProposal(r.Context(), r.PathValue("id"), r.PathValue("pid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleApplyBackendChangeProposalCommands(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.ApplyChangeProposalInput
	if !s.backendChangeProposalBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.ApplyChangeProposal(r.Context(), r.PathValue("id"), r.PathValue("pid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleRestoreBackendChangeProposal(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.RestoreChangeProposalInput
	if !s.backendChangeProposalBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.RestoreChangeProposal(r.Context(), r.PathValue("id"), r.PathValue("pid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

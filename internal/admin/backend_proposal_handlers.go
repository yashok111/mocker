package admin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) backendProposalBody(w http.ResponseWriter, r *http.Request, out any) bool {
	var raw jsontext.Value
	if !s.backendNoQuery(w, r) || !s.backendBodyLimit(w, r, &raw, min(s.cfg.MaxBody, int64(backendmodel.MaxImportBatchBytes))) {
		return false
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		if fault, ok := errors.AsType[*backendmodel.FaultError](err); ok {
			s.backendError(w, fault)
		} else {
			s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Request must match the proposal schema"})
		}
		return false
	}
	return true
}

func backendProposalQuery(r *http.Request, detail bool) (url.Values, int, error) {
	return backendProposalQueryStatuses(r, detail, []string{"draft"})
}
func backendProposalQueryStatuses(r *http.Request, detail bool, statuses []string) (url.Values, int, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, 0, backendQueryError()
	}
	limit := 0
	for key, values := range q {
		if len(values) != 1 {
			return nil, 0, backendQueryError()
		}
		switch key {
		case "limit":
			limit, err = strconv.Atoi(values[0])
			if err != nil || limit < 1 || limit > backendmodel.MaxGraphPageSize {
				return nil, 0, backendQueryError()
			}
		case "cursor":
		case "proposalRevisionId":
			if !detail || !backendmodel.ValidID(values[0]) {
				return nil, 0, backendQueryError()
			}
		case "baseRevisionId":
			if detail || !backendmodel.ValidID(values[0]) {
				return nil, 0, backendQueryError()
			}
		case "status":
			if detail || !slices.Contains(statuses, values[0]) {
				return nil, 0, backendQueryError()
			}
		default:
			return nil, 0, backendQueryError()
		}
	}
	return q, limit, nil
}

func (s *Server) handleListBackendProposals(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, limit, err := backendProposalQuery(r, false)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.ListProposals(r.Context(), r.PathValue("id"), backendmodel.ProposalListInput{BaseRevisionID: q.Get("baseRevisionId"), Status: q.Get("status"), Limit: limit, Cursor: q.Get("cursor")})
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleCreateBackendProposal(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.CreateProposalInput
	if !s.backendProposalBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.CreateProposal(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleGetBackendProposal(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, limit, err := backendProposalQuery(r, true)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.GetProposal(r.Context(), r.PathValue("id"), r.PathValue("pid"), backendmodel.GetProposalInput{ProposalRevisionID: q.Get("proposalRevisionId"), Limit: limit, Cursor: q.Get("cursor")})
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handlePreviewBackendProposalCommands(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.PreviewProposalInput
	if !s.backendProposalBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.PreviewProposal(r.Context(), r.PathValue("id"), r.PathValue("pid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleApplyBackendProposalCommands(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	var in backendmodel.ApplyProposalInput
	if !s.backendProposalBody(w, r, &in) {
		return
	}
	out, err := s.backendRepo.ApplyProposal(r.Context(), r.PathValue("id"), r.PathValue("pid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func backendRouteReadTarget(r *http.Request) backendmodel.BackendReadTarget {
	if r.PathValue("pid") != "" {
		return backendmodel.BackendReadTarget{Proposal: &backendmodel.ProposalReadTarget{ProposalID: r.PathValue("pid"), ProposalRevisionID: r.PathValue("prid")}}
	}
	return backendmodel.BackendReadTarget{RevisionID: r.PathValue("rid")}
}

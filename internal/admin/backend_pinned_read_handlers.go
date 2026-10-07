package admin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) backendProjectionBody(w http.ResponseWriter, r *http.Request, out any, limit int64) bool {
	var raw jsontext.Value
	if !s.backendBodyLimit(w, r, &raw, limit) {
		return false
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		s.backendError(w, backendBodyFault(err, "Request must match the request schema"))
		return false
	}
	return true
}

func backendSHA256(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}
func backendPositiveDecimal(value string) (int64, error) {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return 0, backendQueryError()
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, backendQueryError()
		}
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 1 {
		return 0, backendQueryError()
	}
	return version, nil
}
func backendPinnedReadQuery(r *http.Request, kind string, candidate bool) (url.Values, int, error) {
	if r.ContentLength != 0 || r.TransferEncoding != nil {
		return nil, 0, backendQueryError()
	}
	allowed := []string{}
	switch kind {
	case "evidence":
		allowed = []string{"limit", "cursor", "subjectId", "evidenceId"}
	case "assertions":
		allowed = []string{"limit", "cursor", "recordType", "id", "repositoryId", "providerNamespace"}
	}
	if candidate {
		allowed = append(allowed, "importVersion", "candidateHash")
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, 0, backendQueryError()
	}
	for key, values := range query {
		if len(values) != 1 || !slices.Contains(allowed, key) {
			return nil, 0, backendQueryError()
		}
	}
	limit := 0
	if query.Has("limit") {
		value, err := backendPositiveDecimal(query.Get("limit"))
		if err != nil || value > backendmodel.MaxGraphPageSize {
			return nil, 0, backendQueryError()
		}
		limit = int(value)
	}
	if len(query.Get("cursor")) > 1024 {
		return nil, 0, backendQueryError()
	}
	if err := backendPinnedReadFilters(query); err != nil {
		return nil, 0, err
	}
	return query, limit, nil
}
func backendPinnedReadFilters(query url.Values) error {
	for _, key := range []string{"subjectId", "evidenceId", "id", "repositoryId"} {
		if query.Has(key) && !backendmodel.ValidID(query.Get(key)) {
			return backendQueryError()
		}
	}
	if query.Has("evidenceId") && (query.Has("subjectId") || query.Has("cursor")) {
		return backendQueryError()
	}
	if query.Has("recordType") && !slices.Contains([]string{"node", "edge"}, query.Get("recordType")) {
		return backendQueryError()
	}
	if query.Has("providerNamespace") && query.Get("providerNamespace") == "" {
		return backendQueryError()
	}
	return nil
}
func backendChangeReadTarget(r *http.Request) backendmodel.BackendReadTarget {
	return backendmodel.BackendReadTarget{ChangeProposal: &backendmodel.ProposalReadTarget{ProposalID: r.PathValue("pid"), ProposalRevisionID: r.PathValue("prid")}}
}
func backendCandidateReadTarget(r *http.Request) backendmodel.BackendReadTarget {
	return backendmodel.BackendReadTarget{ImportCandidate: &backendmodel.ImportCandidateReadTarget{ImportID: r.PathValue("iid")}}
}

func (s *Server) backendPinnedRead(w http.ResponseWriter, r *http.Request, kind string, target backendmodel.BackendReadTarget) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	query, limit, err := backendPinnedReadQuery(r, kind, target.ImportCandidate != nil)
	if err != nil {
		s.backendError(w, err)
		return
	}
	if pin := target.ImportCandidate; pin != nil {
		pin.ImportVersion, err = backendPositiveDecimal(query.Get("importVersion"))
		if err != nil || !backendSHA256(query.Get("candidateHash")) {
			s.backendError(w, backendQueryError())
			return
		}
		pin.CandidateHash = query.Get("candidateHash")
	}
	if err = target.Validate(); err != nil {
		s.backendError(w, err)
		return
	}
	var out any
	switch kind {
	case "node":
		out, err = s.backendRepo.ReadNode(r.Context(), r.PathValue("id"), target, r.PathValue("nid"))
	case "evidence":
		out, err = s.backendRepo.ReadEvidence(r.Context(), r.PathValue("id"), target, backendmodel.EvidenceQueryInput{SubjectID: query.Get("subjectId"), EvidenceID: query.Get("evidenceId"), Limit: limit, Cursor: query.Get("cursor")})
	case "coverage":
		out, err = s.backendRepo.ReadCoverage(r.Context(), r.PathValue("id"), target)
	case "assertions":
		out, err = s.backendRepo.ReadAssertions(r.Context(), r.PathValue("id"), target, backendmodel.SourceAssertionsQuery{RecordType: query.Get("recordType"), ID: query.Get("id"), RepositoryID: query.Get("repositoryId"), ProviderNamespace: query.Get("providerNamespace"), Limit: limit, Cursor: query.Get("cursor")})
	}
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}
func (s *Server) handleGetBackendChangeProposalNode(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "node", backendChangeReadTarget(r))
}
func (s *Server) handleGetBackendChangeProposalEvidence(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "evidence", backendChangeReadTarget(r))
}
func (s *Server) handleGetBackendChangeProposalCoverage(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "coverage", backendChangeReadTarget(r))
}
func (s *Server) handleGetBackendChangeProposalAssertions(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "assertions", backendChangeReadTarget(r))
}
func (s *Server) handleGetBackendImportCandidateNode(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "node", backendCandidateReadTarget(r))
}
func (s *Server) handleGetBackendImportCandidateEvidence(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "evidence", backendCandidateReadTarget(r))
}
func (s *Server) handleGetBackendImportCandidateCoverage(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "coverage", backendCandidateReadTarget(r))
}
func (s *Server) handleGetBackendImportCandidateAssertions(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "assertions", backendCandidateReadTarget(r))
}
func (s *Server) handleGetBackendAssertions(w http.ResponseWriter, r *http.Request) {
	s.backendPinnedRead(w, r, "assertions", backendmodel.BackendReadTarget{RevisionID: r.PathValue("rid")})
}

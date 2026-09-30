package admin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/url"
	"strconv"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

func (s *Server) handleCompareBackendRevisions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var raw jsontext.Value
	if !s.backendBody(w, r, &raw) {
		return
	}
	var in backendmodel.CompareRevisionsInput
	if !s.backendImportDecodedBody(w, raw, &in) {
		return
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		s.backendError(w, backendQueryError())
		return
	}
	if !backendmodel.ValidID(in.FromRevisionID) || !backendmodel.ValidID(in.ToRevisionID) {
		s.backendError(w, backendQueryError())
		return
	}
	for _, field := range []struct {
		name, value string
	}{{"recordType", in.RecordType}, {"changeKind", in.ChangeKind}} {
		if _, supplied := fields[field.name]; supplied && field.value == "" {
			s.backendError(w, backendQueryError())
			return
		}
	}
	if _, ok := fields["limit"]; ok && (in.Limit < 1 || in.Limit > backendmodel.MaxGraphPageSize) {
		s.backendError(w, backendQueryError())
		return
	}
	out, err := s.backendRepo.CompareRevisions(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleGetBackendImportChanges(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		s.backendError(w, backendQueryError())
		return
	}
	for key, values := range q {
		if len(values) != 1 || (key != "previewVersion" && key != "recordType" && key != "limit" && key != "cursor") {
			s.backendError(w, backendQueryError())
			return
		}
	}
	in := backendmodel.ImportChangesInput{RecordType: q.Get("recordType"), Cursor: q.Get("cursor")}
	in.PreviewVersion, err = strconv.ParseInt(q.Get("previewVersion"), 10, 64)
	if err != nil || in.PreviewVersion < 1 || (in.RecordType != "source" && in.RecordType != "identity" && in.RecordType != "deletion") {
		s.backendError(w, backendQueryError())
		return
	}
	if value, ok := q["limit"]; ok {
		in.Limit, err = strconv.Atoi(value[0])
		if err != nil || in.Limit < 1 || in.Limit > backendmodel.MaxGraphPageSize {
			s.backendError(w, backendQueryError())
			return
		}
	}
	out, err := s.backendRepo.ImportChanges(r.Context(), r.PathValue("id"), r.PathValue("iid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

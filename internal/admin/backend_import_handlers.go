package admin

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/httpx"
)

// Import/status and graph pages have a separate bound from project history.
func backendImportQuery(r *http.Request, maxLimit int, subject bool) (backendmodel.ListInput, string, error) {
	var in backendmodel.ListInput
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return in, "", backendQueryError()
	}
	for key, values := range q {
		if len(values) != 1 || (key != "limit" && key != "cursor" && !(subject && (key == "subjectId" || key == "evidenceId"))) {
			return in, "", backendQueryError()
		}
	}
	if values, ok := q["limit"]; ok {
		in.Limit, err = strconv.Atoi(values[0])
		if err != nil || in.Limit < 1 || in.Limit > maxLimit {
			return in, "", backendQueryError()
		}
	}
	if values, ok := q["evidenceId"]; ok && (!backendmodel.ValidID(values[0]) || q.Has("subjectId") || q.Has("cursor")) {
		return in, "", backendQueryError()
	}
	if values, ok := q["subjectId"]; ok && !backendmodel.ValidID(values[0]) {
		return in, "", backendQueryError()
	}
	in.Cursor = q.Get("cursor")
	return in, q.Get("subjectId"), nil
}
func backendQueryError() error {
	return &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Only allowed, one-valued query parameters and limits may be supplied"}
}
func (s *Server) backendNoQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		s.backendError(w, backendQueryError())
		return false
	}
	return true
}

func (s *Server) handleBeginBackendImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var in backendmodel.BeginImportInput
	if !s.backendImportBody(w, r, &in, s.cfg.MaxBody) {
		return
	}
	out, err := s.backendRepo.BeginImport(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handlePutBackendImportBatch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var in backendmodel.ImportBatchInput
	if !s.backendImportBody(w, r, &in, min(s.cfg.MaxBody, int64(backendmodel.MaxImportBatchBytes))) {
		return
	}
	out, err := s.backendRepo.PutImportBatch(r.Context(), r.PathValue("id"), r.PathValue("iid"), r.PathValue("bid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handlePreviewBackendImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var in backendmodel.PreviewImportInput
	if !s.backendImportBody(w, r, &in, s.cfg.MaxBody) {
		return
	}
	out, err := s.backendRepo.PreviewImport(r.Context(), r.PathValue("id"), r.PathValue("iid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleCommitBackendImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var in backendmodel.CommitImportInput
	if !s.backendImportBody(w, r, &in, s.cfg.MaxBody) {
		return
	}
	out, err := s.backendRepo.CommitImport(r.Context(), r.PathValue("id"), r.PathValue("iid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleAbortBackendImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var in backendmodel.AbortImportInput
	if !s.backendImportBody(w, r, &in, s.cfg.MaxBody) {
		return
	}
	out, err := s.backendRepo.AbortImport(r.Context(), r.PathValue("id"), r.PathValue("iid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleQueryBackendGraph(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	var raw map[string]jsontext.Value
	if !s.backendBody(w, r, &raw) {
		return
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		s.backendError(w, err)
		return
	}
	var in backendmodel.GraphQueryInput
	if err := backendImportShape(encoded, reflect.TypeFor[backendmodel.GraphQueryInput](), ""); err != nil {
		s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: err.Error()})
		return
	}
	if err := json.Unmarshal(encoded, &in, json.RejectUnknownMembers(true)); err != nil {
		s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Graph query must match the request schema"})
		return
	}
	if _, supplied := raw["limit"]; supplied && (in.Limit < 1 || in.Limit > backendmodel.MaxGraphPageSize) {
		s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Explicit graph limit must be between 1 and 500", Details: map[string]any{"path": "/limit"}})
		return
	}
	forbidden := []string{}
	if in.RecordType == "nodes" {
		forbidden = []string{"from", "to"}
	}
	if in.RecordType == "edges" {
		forbidden = []string{"search", "parentId"}
	}
	for _, key := range []string{"id", "parentId", "from", "to"} {
		if value, ok := raw[key]; ok {
			var id string
			if err := json.Unmarshal(value, &id); err != nil || !backendmodel.ValidID(id) {
				status, code := 422, "backend_import_invalid"
				if key == "id" {
					status, code = 400, "backend_invalid"
				}
				s.backendError(w, &backendmodel.FaultError{Status: status, Code: code, Message: "Selector must be a canonical UUID", Details: map[string]any{"path": "/" + key}})
				return
			}
		}
	}
	for _, key := range forbidden {
		if _, ok := raw[key]; ok {
			s.backendError(w, &backendmodel.FaultError{Status: 422, Code: "backend_import_invalid", Message: "Selector is unavailable for this recordType", Details: map[string]any{"path": "/" + key}})
			return
		}
	}

	if _, selected := raw["id"]; selected {
		for _, key := range []string{"kind", "search", "parentId", "from", "to", "cursor"} {
			if _, supplied := raw[key]; supplied {
				s.backendError(w, backendQueryError())
				return
			}
		}
	}

	out, err := s.backendRepo.QueryGraph(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleListBackendImports(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	in, _, err := backendImportQuery(r, backendmodel.MaxPageSize, false)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.Imports(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleGetBackendImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	in, _, err := backendImportQuery(r, backendmodel.MaxGraphPageSize, false)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.Import(r.Context(), r.PathValue("id"), r.PathValue("iid"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleGetBackendNode(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	out, err := s.backendRepo.Node(r.Context(), r.PathValue("id"), r.PathValue("rid"), r.PathValue("nid"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleGetBackendEvidence(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	in, subject, err := backendImportQuery(r, backendmodel.MaxGraphPageSize, true)
	if err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.Evidence(r.Context(), r.PathValue("id"), r.PathValue("rid"), backendmodel.EvidenceQueryInput{EvidenceID: r.URL.Query().Get("evidenceId"), SubjectID: subject, Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

func (s *Server) handleGetBackendCoverage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if !s.backendNoQuery(w, r) {
		return
	}
	out, err := s.backendRepo.RevisionCoverage(r.Context(), r.PathValue("id"), r.PathValue("rid"))
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

// Domain validation checks meaning. This boundary additionally checks required
// wire members, because decoding an absent bool/string into Go loses presence.
func (s *Server) backendImportBody(w http.ResponseWriter, r *http.Request, out any, maxBytes int64) bool {
	var raw jsontext.Value
	if !s.backendBodyLimit(w, r, &raw, maxBytes) {
		return false
	}
	return s.backendImportDecodedBody(w, raw, out)
}

func (s *Server) backendImportDecodedBody(w http.ResponseWriter, raw jsontext.Value, out any) bool {
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Request must match the import schema"})
		return false
	}
	if err := backendImportShape(raw, reflect.TypeOf(out).Elem(), ""); err != nil {
		s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: err.Error()})
		return false
	}
	return true
}

func backendImportShape(raw jsontext.Value, typ reflect.Type, path string) error {
	raw = bytes.TrimSpace(raw)
	if typ == reflect.TypeFor[jsontext.Value]() {
		return nil
	}
	if typ.Kind() == reflect.Pointer {
		if bytes.Equal(raw, []byte("null")) {
			if strings.HasSuffix(path, "/denominator") {
				return nil
			}
			return fmt.Errorf("%s must not be null", path)
		}
		return backendImportShape(raw, typ.Elem(), path)
	}
	if typ == reflect.TypeFor[backendmodel.BeginImportInput]() {
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if value, supplied := fields["mode"]; supplied {
			var mode string
			if err := json.Unmarshal(value, &mode); err != nil || (mode != "initial" && mode != "reconcile") {
				return fmt.Errorf("%s/mode must be initial or reconcile", path)
			}
		}
	}
	if typ == reflect.TypeFor[backendmodel.ImportCommand]() {
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		var op string
		if err := json.Unmarshal(fields["op"], &op); err != nil {
			return err
		}
		member := map[string]string{"upsert_node": "node", "upsert_edge": "edge", "upsert_evidence": "evidence", "remove": "remove", "map_identity": "identity", "delete_assertion": "deletion"}[op]
		if member == "" || len(fields) != 2 || fields[member] == nil {
			return fmt.Errorf("%s must contain only op and its command member", path)
		}
	}
	bad := func() error { return fmt.Errorf("%s must match the import schema", path) }
	if len(raw) == 0 {
		return bad()
	}
	if typ == reflect.TypeFor[time.Time]() {
		if raw[0] != '"' {
			return bad()
		}
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		if raw[0] != '{' {
			return bad()
		}
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for field := range typ.Fields() {
			tag := field.Tag.Get("json")
			name, options, _ := strings.Cut(tag, ",")
			if name == "-" || !field.IsExported() {
				continue
			}
			value, ok := fields[name]
			if !ok {
				if strings.Contains(options, "omitempty") || strings.Contains(options, "omitzero") {
					continue
				}
				return fmt.Errorf("%s/%s is required", path, name)
			}
			if err := backendImportShape(value, field.Type, path+"/"+name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if raw[0] != '[' {
			return bad()
		}
		var values []jsontext.Value
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for i, value := range values {
			if err := backendImportShape(value, typ.Elem(), fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if raw[0] != '{' {
			return bad()
		}
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for key, value := range fields {
			if err := backendImportShape(value, typ.Elem(), path+"/"+key); err != nil {
				return err
			}
		}
	case reflect.String:
		if raw[0] != '"' {
			return bad()
		}
	case reflect.Bool:
		if raw[0] != 't' && raw[0] != 'f' {
			return bad()
		}
	case reflect.Int, reflect.Int64:
		if raw[0] == 'n' {
			return bad()
		}
	}
	return nil
}

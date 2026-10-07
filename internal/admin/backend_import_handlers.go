package admin

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
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
		if len(values) != 1 || (key != "limit" && key != "cursor" && (!subject || (key != "subjectId" && key != "evidenceId"))) {
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
	if err := backendGraphSelectorError(raw, in.RecordType); err != nil {
		s.backendError(w, err)
		return
	}
	out, err := s.backendRepo.QueryGraph(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.backendError(w, err)
		return
	}
	httpx.JSON(w, 200, out)
}

// backendGraphSelectorError checks which selectors a graph query may combine,
// in the order the route has always refused them: a selector the recordType
// forbids, then a malformed UUID selector, then anything beside an exact id.
func backendGraphSelectorError(raw map[string]jsontext.Value, recordType string) error {
	forbidden := []string{}
	if recordType == "nodes" {
		forbidden = []string{"from", "to"}
	}
	if recordType == "edges" {
		forbidden = []string{"search", "parentId"}
	}
	// Presence first: a selector the recordType forbids is refused whatever
	// its value (backend_graph_selector_contract_test pins 422 for "").
	for _, key := range forbidden {
		if _, ok := raw[key]; ok {
			return &backendmodel.FaultError{Status: 422, Code: "backend_import_invalid", Message: "Selector is unavailable for this recordType", Details: map[string]any{"path": "/" + key}}
		}
	}
	// Review 2026-10-06, F17: a malformed parentId/from/to answered 422
	// backend_import_invalid (an import code on a read) while the same mistake
	// in id, serviceId or sourceSnapshotId is 400 backend_invalid. One class.
	for _, key := range []string{"id", "parentId", "from", "to"} {
		if value, ok := raw[key]; ok {
			var id string
			if err := json.Unmarshal(value, &id); err != nil || !backendmodel.ValidID(id) {
				return &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: "Selector must be a canonical UUID", Details: map[string]any{"path": "/" + key}}
			}
		}
	}
	if _, selected := raw["id"]; selected {
		for _, key := range []string{"kind", "search", "parentId", "from", "to", "cursor"} {
			if _, supplied := raw[key]; supplied {
				return backendQueryError()
			}
		}
	}
	return nil
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
	out, err := s.backendRepo.ReadNode(r.Context(), r.PathValue("id"), backendRouteReadTarget(r), r.PathValue("nid"))
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
	out, err := s.backendRepo.ReadEvidence(r.Context(), r.PathValue("id"), backendRouteReadTarget(r), backendmodel.EvidenceQueryInput{EvidenceID: r.URL.Query().Get("evidenceId"), SubjectID: subject, Limit: in.Limit, Cursor: in.Cursor})
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
	out, err := s.backendRepo.ReadCoverage(r.Context(), r.PathValue("id"), backendRouteReadTarget(r))
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
		s.backendError(w, backendBodyFault(err, "Request must match the import schema"))
		return false
	}
	if err := backendImportShape(raw, reflect.TypeOf(out).Elem(), ""); err != nil {
		s.backendError(w, &backendmodel.FaultError{Status: 400, Code: "backend_invalid", Message: err.Error()})
		return false
	}
	return true
}

// backendImportShape checks raw against the wire shape of typ: required
// members present, no null where Go would silently zero, and each value's JSON
// kind matching the field's. Decoding alone loses presence, so this runs on
// the raw bytes after a successful decode.
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
		if err := beginImportSelectorShape(raw, path); err != nil {
			return err
		}
	}
	if typ == reflect.TypeFor[backendmodel.ImportCommand]() {
		if err := importCommandShape(raw, path); err != nil {
			return err
		}
	}
	if len(raw) == 0 || !backendImportJSONKindMatches(typ, raw[0]) {
		return fmt.Errorf("%s must match the import schema", path)
	}
	if typ == reflect.TypeFor[time.Time]() {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		return backendImportStructShape(raw, typ, path)
	case reflect.Slice:
		return backendImportSliceShape(raw, typ, path)
	case reflect.Map:
		return backendImportMapShape(raw, typ, path)
	}
	return nil
}

// backendImportJSONKindMatches compares a JSON value's first byte with the Go
// kind it decodes into; a time.Time is a struct in Go but a string on the wire.
func backendImportJSONKindMatches(typ reflect.Type, first byte) bool {
	if typ == reflect.TypeFor[time.Time]() {
		return first == '"'
	}
	switch typ.Kind() {
	case reflect.Struct, reflect.Map:
		return first == '{'
	case reflect.Slice:
		return first == '['
	case reflect.String:
		return first == '"'
	case reflect.Bool:
		return first == 't' || first == 'f'
	case reflect.Int, reflect.Int64:
		return first != 'n'
	}
	return true
}

// beginImportSelectorShape refuses an unknown profile or mode by name, so the
// error names the member instead of the generic schema mismatch.
func beginImportSelectorShape(raw jsontext.Value, path string) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value, supplied := fields["profile"]; supplied {
		var profile string
		if err := json.Unmarshal(value, &profile); err != nil || !slices.Contains([]string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile, backendmodel.ComposedProfile}, profile) {
			return fmt.Errorf("%s/profile must select a supported import profile", path)
		}
	}
	if value, supplied := fields["mode"]; supplied {
		var mode string
		if err := json.Unmarshal(value, &mode); err != nil || !slices.Contains([]string{"initial", "reconcile", "composed"}, mode) {
			return fmt.Errorf("%s/mode must select a supported import mode", path)
		}
	}
	return nil
}

// importCommandShape admits a command as exactly op plus the one member that
// op names; the struct carries every member, so decoding cannot tell.
func importCommandShape(raw jsontext.Value, path string) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var op string
	if err := json.Unmarshal(fields["op"], &op); err != nil {
		return err
	}
	member := map[string]string{"upsert_node": "node", "upsert_edge": "edge", "upsert_evidence": "evidence", "remove": "remove", "map_identity": "identity", "delete_assertion": "deletion", "claim_identity": "claimIdentity", "resolve_assertion": "resolution"}[op]
	if member == "" || len(fields) != 2 || fields[member] == nil {
		return fmt.Errorf("%s must contain only op and its command member", path)
	}
	return nil
}

func backendImportStructShape(raw jsontext.Value, typ reflect.Type, path string) error {
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
	return nil
}

func backendImportSliceShape(raw jsontext.Value, typ reflect.Type, path string) error {
	var values []jsontext.Value
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	for i, value := range values {
		if err := backendImportShape(value, typ.Elem(), fmt.Sprintf("%s/%d", path, i)); err != nil {
			return err
		}
	}
	return nil
}

func backendImportMapShape(raw jsontext.Value, typ reflect.Type, path string) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		if err := backendImportShape(value, typ.Elem(), path+"/"+key); err != nil {
			return err
		}
	}
	return nil
}

package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/testauth"
)

func TestBackendArtifactRoutesAndAdmission(t *testing.T) {
	s := loopbackTestServer(t, nil)
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v; want %d", method, path, status, raw, err, want)
		}
		return raw
	}
	var p backendmodel.Project
	if err := json.Unmarshal(call("POST", "/api/backend-projects", `{"name":"Editors","idempotencyKey":"editors"}`, 201), &p); err != nil {
		t.Fatal(err)
	}
	root := "/api/backend-projects/" + p.ID + "/artifacts"
	valid := `{"revisionId":"` + p.CurrentRevisionID + `","artifact":{"kind":"design_scenario","id":"9007199254740993"},"view":"sequence"}`
	call("POST", root+"/query", valid, 404)
	for _, body := range []string{`null`, `{}`, strings.Replace(valid, `}`, `,"extra":true}`, 1), strings.Replace(valid, `"id":"9007199254740993"`, `"id":9007199254740993`, 1), strings.Replace(valid, `"view":"sequence"`, `"view":"sequence","limit":null`, 1), strings.Replace(valid, `"view":"sequence"`, `"view":"sequence","view":"sequence"`, 1)} {
		call("POST", root+"/query", body, 400)
	}
	call("POST", root+"/query?limit=1", valid, 400)
	call("POST", root+"/preview", `{}`, 400)
	call("POST", root+"/commands", `{}`, 400)
	for _, suffix := range []string{"query", "preview", "commands"} {
		call("POST", root+"/"+suffix, strings.Repeat(" ", backendmodel.MaxAPIPinBodyBytes+1), http.StatusRequestEntityTooLarge)
	}
	call("GET", "/api/design-scenarios/9007199254740993/revisions/9223372036854775807/artifact-snapshot", "", 404)
	for _, id := range []string{"01", "+1", "0", "9223372036854775808"} {
		call("GET", "/api/design-scenarios/"+id+"/revisions/1/artifact-snapshot", "", 400)
	}
}
func TestBackendArtifactAuthenticationAndCSRF(t *testing.T) {
	s := loopbackTestServer(t, nil)
	h := s.Handler()
	for _, path := range []string{"/api/backend-projects/00000000-0000-4000-8000-000000000001/artifacts/query", "/api/backend-projects/00000000-0000-4000-8000-000000000001/artifacts/preview", "/api/backend-projects/00000000-0000-4000-8000-000000000001/artifacts/commands", "/api/design-scenarios/1/revisions/1/artifact-snapshot"} {
		method := "POST"
		if strings.Contains(path, "artifact-snapshot") {
			method = "GET"
		}
		r := httptest.NewRequest(method, "http://mocker.local"+path, strings.NewReader(`{}`))
		r.Header.Set("Origin", "http://mocker.local")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("unauth %s %d %s", path, w.Code, w.Body)
		}
	}
	login := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login", strings.NewReader(`{"name":"API pins","password":"`+testauth.Password+`"}`))
	login.Header.Set("Origin", "http://mocker.local")
	login.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, login)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	for _, suffix := range []string{"query", "preview", "commands"} {
		r := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/backend-projects/00000000-0000-4000-8000-000000000001/artifacts/"+suffix, strings.NewReader(`{}`))
		r.AddCookie(cookie)
		r.Header.Set("Origin", "http://mocker.local")
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("missing CSRF %s %d %s", suffix, w.Code, w.Body)
		}
	}
}

func artifactTransportOwnerRows(t *testing.T, s *Server) string {
	t.Helper()
	all := []any{}
	for _, table := range []string{"api_designs", "api_design_revisions", "design_scenarios", "design_scenario_revisions"} {
		func() {
			rows, err := s.db.R.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, table, cols)
			for rows.Next() {
				values := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range values {
					ptrs[i] = &values[i]
				}
				if err = rows.Scan(ptrs...); err != nil {
					t.Fatal(err)
				}
				all = append(all, values)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}()
	}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func artifactTransportScenario(t *testing.T, s *Server) *designscenario.Detail {
	t.Helper()
	doc := designscenario.Document{FormatVersion: 3, Title: "Exact", Participants: []designscenario.Participant{{ID: "p", Name: "Service", Kind: "service"}, {ID: "c", Name: "Client", Kind: "client"}}, Messages: []designscenario.Message{{ID: "m", FromID: "c", ToID: "p", Kind: "request", Label: "Request"}}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}
	out, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Document: doc, FormDrafts: map[string]string{"p": "inert unfinished form"}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func artifactTransportCall(t *testing.T, s *Server, method, path string, input any, want int) []byte {
	t.Helper()
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, body)
	if err != nil || status != want {
		t.Fatalf("%s %s: %d %s %v; want %d", method, path, status, raw, err, want)
	}
	if want == 200 {
		validateBackendImportResponse(t, method, path, raw)
	}
	return raw
}

func TestBackendArtifactQualifiedSnapshot(t *testing.T) {
	s := loopbackTestServer(t, nil)
	for _, table := range []string{"design_scenarios", "design_scenario_revisions"} {
		if _, err := s.db.W.ExecContext(t.Context(), `INSERT INTO sqlite_sequence(name,seq) VALUES(?,9007199254740992)`, table); err != nil {
			t.Fatal(err)
		}
	}
	d := artifactTransportScenario(t, s)
	id, rid := strconv.FormatInt(d.Scenario.ID, 10), strconv.FormatInt(d.Draft.ID, 10)
	if id != "9007199254740993" || rid != "9007199254740993" {
		t.Fatal("wide fixture", id, rid)
	}
	path := "/api/design-scenarios/" + id + "/revisions/" + rid + "/artifact-snapshot"
	before := artifactTransportOwnerRows(t, s)
	raw := artifactTransportCall(t, s, "GET", path, nil, 200)
	var snap DesignScenarioArtifactSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	var document, drafts string
	if err := s.db.R.QueryRowContext(t.Context(), `SELECT document,form_drafts FROM design_scenario_revisions WHERE id=?`, d.Draft.ID).Scan(&document, &drafts); err != nil {
		t.Fatal(err)
	}
	if snap.ScenarioID != id || snap.RevisionID != rid || snap.Version != "1" || snap.ContentHash != d.Draft.Hash || snap.StoredContentHash != d.Draft.Hash || snap.DocumentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(document))) || snap.DocumentJSON != document || snap.FormDraftsJSON != drafts || snap.TypedStatus != "supported" || snap.EnvelopeVerification != "verified" {
		t.Fatal("qualified exact envelope", string(raw))
	}
	if artifactTransportOwnerRows(t, s) != before {
		t.Fatal("snapshot changed owners")
	}
	foreign := artifactTransportScenario(t, s)
	artifactTransportCall(t, s, "GET", "/api/design-scenarios/"+id+"/revisions/"+strconv.FormatInt(foreign.Draft.ID, 10)+"/artifact-snapshot", nil, 404)
	artifactTransportCall(t, s, "GET", path+"?latest=1", nil, 400)
	artifactTransportCall(t, s, "GET", path, map[string]any{}, 400)
	// Controlled retained future bytes in this isolated synthetic owner only.
	rows, err := s.db.R.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name='design_scenario_revisions'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, name := range names {
		if _, err := s.db.W.ExecContext(t.Context(), `DROP TRIGGER "`+name+`"`); err != nil {
			t.Fatal(err)
		}
	}
	future := strings.Replace(document, `{"formatVersion"`, `{"future":{"n":9007199254740993},"formatVersion"`, 1)
	if _, err := s.db.W.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET document=? WHERE id=?`, future, d.Draft.ID); err != nil {
		t.Fatal(err)
	}
	before = artifactTransportOwnerRows(t, s)
	raw = artifactTransportCall(t, s, "GET", path, nil, 200)
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["contentHash"]; ok {
		t.Fatal("unchecked stored hash presented as verified", string(raw))
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.StoredContentHash != d.Draft.Hash || snap.DocumentJSON != future || snap.DocumentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(future))) || snap.TypedStatus != "unsupported" || snap.EnvelopeVerification != "unavailable" || snap.FormDraftsJSON != drafts {
		t.Fatal(string(raw))
	}
	if artifactTransportOwnerRows(t, s) != before {
		t.Fatal("unsupported snapshot wrote owners")
	}
	if _, err := s.db.W.ExecContext(t.Context(), `UPDATE design_scenario_revisions SET document=? WHERE id=?`, document, d.Draft.ID); err != nil {
		t.Fatal(err)
	}
	s.cfg.MaxBody = 1
	artifactTransportCall(t, s, "GET", path, nil, 413)
}

func TestBackendArtifactServiceTransportAndRawReplay(t *testing.T) {
	s := loopbackTestServer(t, nil)
	base, node := backendAPIArtifactFixture(t, s)
	d := artifactTransportScenario(t, s)
	key := backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(d.Scenario.ID, 10)}
	in := backendmodel.PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []backendmodel.ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: key, RevisionID: strconv.FormatInt(d.Draft.ID, 10), EditorBindings: []backendmodel.EditorBindingInput{{Selector: backendmodel.EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{node}}, {Selector: backendmodel.EditorSelector{Kind: "sequence_message", MessageID: "m"}, SourceNodeIDs: []string{node}}}, Reason: "manual"}}}
	root := "/api/backend-projects/" + base.Project.ID + "/artifacts"
	before := artifactTransportOwnerRows(t, s)
	var preview backendmodel.ArtifactPinsPreview
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/preview", in, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.CanApply || len(preview.EditorBindings) != 2 || len(preview.Diff) == 0 {
		t.Fatal("preview", preview)
	}
	foreign := in
	foreign.BaseRevisionID = base.Project.ID
	artifactTransportCall(t, s, "POST", root+"/preview", foreign, 404)
	stale := in
	stale.ExpectedVersion++
	artifactTransportCall(t, s, "POST", root+"/preview", stale, 409)
	blocked := in
	blocked.Commands = append([]backendmodel.ArtifactPinCommand{}, in.Commands...)
	blocked.Commands[0].RevisionID = "9223372036854775807"
	var blockedPreview backendmodel.ArtifactPinsPreview
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/preview", blocked, 200), &blockedPreview); err != nil {
		t.Fatal(err)
	}
	if blockedPreview.CanApply {
		t.Fatal("missing owner revision allowed")
	}
	artifactTransportCall(t, s, "POST", root+"/commands", backendmodel.ApplyArtifactPinsInput{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: blocked.Commands, CandidateHash: blockedPreview.CandidateHash, IdempotencyKey: "blocked"}, 422)
	apply := backendmodel.ApplyArtifactPinsInput{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: in.Commands, CandidateHash: preview.CandidateHash, IdempotencyKey: "exact"}
	bad := apply
	bad.CandidateHash = strings.Repeat("0", 64)
	artifactTransportCall(t, s, "POST", root+"/commands", bad, 409)
	raw := artifactTransportCall(t, s, "POST", root+"/commands", apply, 200)
	var result backendmodel.ArtifactPinsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if artifactTransportOwnerRows(t, s) != before {
		t.Fatal("artifact apply changed owner rows")
	}
	query := backendmodel.ArtifactQueryInput{RevisionID: result.Revision.ID, Artifact: key, View: "sequence", Limit: 1}
	var page backendmodel.ArtifactProjectionPage
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/query", query, 200), &page); err != nil {
		t.Fatal(err)
	}
	if !page.BindingsComplete || len(page.EditorBindings) != 2 || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("paged full roster", page)
	}
	query.Cursor = page.NextCursor
	query.Limit = 2
	artifactTransportCall(t, s, "POST", root+"/query", query, 400)
	query.Limit = 1
	artifactTransportCall(t, s, "POST", root+"/query", query, 200)
	query.Cursor = "broken"
	artifactTransportCall(t, s, "POST", root+"/query", query, 400)
	changed := apply
	changed.Commands = append([]backendmodel.ArtifactPinCommand{}, apply.Commands...)
	changed.Commands[0].Reason = "different request"
	artifactTransportCall(t, s, "POST", root+"/commands", changed, 409)
	// Real comparison validates the additive typed group/editor sides.
	artifactTransportCall(t, s, "POST", "/api/backend-projects/"+base.Project.ID+"/revisions/compare", map[string]any{"fromRevisionId": base.Revision.ID, "toRevisionId": result.Revision.ID}, 200)
	if !bytes.Equal(raw, artifactTransportCall(t, s, "POST", root+"/commands", apply, 200)) {
		t.Fatal("receipt replay differs")
	}

	// A durable receipt can have significant formatting; REST writes the actual stored bytes.
	stored := "\n " + string(raw) + "\n"
	if _, err := s.db.W.ExecContext(t.Context(), `UPDATE backend_command_receipts SET response=? WHERE scope=? AND key=?`, stored, "artifact-pins:"+base.Project.ID, apply.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	raw = []byte(stored)
	if !bytes.Equal(raw, artifactTransportCall(t, s, "POST", root+"/commands", apply, 200)) {
		t.Fatal("REST reserialized original receipt bytes")
	}
	// Reconstruct with no owner service: lookup of the durable receipt comes first.
	s.backendArtifacts = backendmodel.NewArtifactService(s.backendRepo, nil, nil)
	if !bytes.Equal(raw, artifactTransportCall(t, s, "POST", root+"/commands", apply, 200)) {
		t.Fatal("dependency-free replay differs")
	}
	query.Cursor = ""
	query.Limit = 50
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/query", query, 200), &page); err != nil {
		t.Fatal(err)
	}
	if page.Resolution.Status != "unavailable" || !page.BindingsComplete || len(page.EditorBindings) != 2 {
		t.Fatal("lost frozen broken roster", page)
	}

	if _, err := s.db.W.ExecContext(t.Context(), `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.W.ExecContext(t.Context(), `DELETE FROM backend_projects WHERE id=?`, base.Project.ID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, artifactTransportCall(t, s, "POST", root+"/commands", apply, 200)) {
		t.Fatal("receipt depended on live project")
	}
	s.backendArtifacts = nil
	s.scenarioArtifactSnapshots = nil
	if !slices.Contains(s.Ready(), "NewArtifactService") || !slices.Contains(s.Ready(), "ScenarioArtifactSnapshots") {
		t.Fatal("incomplete wiring hidden", s.Ready())
	}
}

func TestBackendArtifactConstructionAdmissionRoster(t *testing.T) {
	s := loopbackTestServer(t, nil)
	base, node := backendAPIArtifactFixture(t, s)
	doc := designscenario.Document{FormatVersion: 3, Title: "Construction", Participants: []designscenario.Participant{{ID: "p", Name: "Service", Kind: "service"}}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}, EventModel: &designscenario.EventModel{Servers: []designscenario.EventServer{{ID: "srv", Name: "Broker", Protocol: "kafka", Auth: "none"}}, Channels: []designscenario.EventChannel{{ID: "ch", Address: "topic", ServerIDs: []string{"srv"}, MessageIDs: []string{"em"}}}, Messages: []designscenario.EventMessage{{ID: "em", Name: strings.Repeat("<", 20000), Examples: []designscenario.EventExample{}}}, Schemas: []designscenario.EventSchema{}, Contracts: []designscenario.EventContract{{ID: "ec", ParticipantID: "p", Operations: []designscenario.EventOperation{}}}}}
	doc.EventModel.Channels = []designscenario.EventChannel{}
	for i := range 80 {
		ch := fmt.Sprint("ch", i)
		doc.EventModel.Channels = append(doc.EventModel.Channels, designscenario.EventChannel{ID: ch, Address: ch, ServerIDs: []string{"srv"}, MessageIDs: []string{"em"}})
		doc.EventModel.Contracts[0].Operations = append(doc.EventModel.Contracts[0].Operations, designscenario.EventOperation{ID: fmt.Sprint("o", i), Action: "send", ChannelID: ch, MessageID: "em"})
	}
	d, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		if invalid, ok := errors.AsType[*designscenario.InvalidError](err); ok {
			t.Fatalf("fixture diagnostics: %+v", invalid.Diagnostics)
		}
		t.Fatal(err)
	}
	key := backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(d.Scenario.ID, 10)}
	in := backendmodel.PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []backendmodel.ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: key, RevisionID: strconv.FormatInt(d.Draft.ID, 10), EditorBindings: []backendmodel.EditorBindingInput{{Selector: backendmodel.EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{node}}}, Reason: "manual"}}}
	root := "/api/backend-projects/" + base.Project.ID + "/artifacts"
	var preview backendmodel.ArtifactPinsPreview
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/preview", in, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.CanApply {
		t.Fatal(preview)
	}
	var result backendmodel.ArtifactPinsResult
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/commands", backendmodel.ApplyArtifactPinsInput{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: in.Commands, CandidateHash: preview.CandidateHash, IdempotencyKey: "construction"}, 200), &result); err != nil {
		t.Fatal(err)
	}
	before := artifactTransportOwnerRows(t, s)
	var page backendmodel.ArtifactProjectionPage
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/query", backendmodel.ArtifactQueryInput{RevisionID: result.Revision.ID, Artifact: key, View: "event_model"}, 200), &page); err != nil {
		t.Fatal(err)
	}
	if page.Complete || len(page.Items) != 0 || len(page.Diagnostics) != 1 || page.Diagnostics[0].Code != "event_model_construction_budget" || !page.BindingsComplete || len(page.EditorBindings) != 1 || len(page.Coverage.TruncatedReasons) != 1 || page.Coverage.TruncatedReasons[0] != "construction_bytes" {
		t.Fatal("admission lost qualified roster", page)
	}
	roster, _ := json.Marshal(page.EditorBindings)
	expected, _ := json.Marshal(preview.EditorBindings)
	if !bytes.Equal(roster, expected) {
		t.Fatal("construction truncated full frozen roster")
	}
	var raw DesignScenarioArtifactSnapshot
	path := "/api/design-scenarios/" + key.ID + "/revisions/" + in.Commands[0].RevisionID + "/artifact-snapshot"
	if err := json.Unmarshal(artifactTransportCall(t, s, "GET", path, nil, 200), &raw); err != nil {
		t.Fatal(err)
	}
	var retained designscenario.Document
	if err := json.Unmarshal([]byte(raw.DocumentJSON), &retained); err != nil {
		t.Fatal(err)
	}
	if raw.EnvelopeVerification != "verified" || raw.ContentHash != page.SelectedPin.ContentHash || retained.EventModel.Messages[0].Name != strings.Repeat("<", 20000) {
		t.Fatal("construction hid exact supported snapshot")
	}
	if artifactTransportOwnerRows(t, s) != before {
		t.Fatal("projection changed owners")
	}
}

func TestBackendArtifactLinkedCopyProjection(t *testing.T) {
	s := loopbackTestServer(t, nil)
	base, _ := backendAPIArtifactFixture(t, s)
	for _, table := range []string{"api_designs", "api_design_revisions"} {
		if _, err := s.db.W.ExecContext(t.Context(), `INSERT INTO sqlite_sequence(name,seq) VALUES(?,9007199254740992)`, table); err != nil {
			t.Fatal(err)
		}
	}
	key := "exact"
	path := "/x"
	apiJSON, err := json.Marshal(map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "Copied key", "version": "1"}, "paths": map[string]any{path: map[string]any{"get": map[string]any{"x-mocker-canvas-operation-id": key, "responses": map[string]any{"200": map[string]any{"description": "OK"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Linked API", Document: string(apiJSON), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.designsRepo.ArtifactSnapshot(t.Context(), d.Design.ID, d.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc := designscenario.Document{FormatVersion: 3, Title: "Scoped copies", Participants: []designscenario.Participant{{ID: "p", Name: "Service", Kind: "service"}, {ID: "c", Name: "Client", Kind: "client"}}, Messages: []designscenario.Message{{ID: "linked-message", FromID: "c", ToID: "p", Kind: "request", Label: "Linked", Operation: &designscenario.OperationBinding{ContractID: "linked", OperationKey: key}}, {ID: "copy-message", FromID: "c", ToID: "p", Kind: "request", Label: "Copy", Operation: &designscenario.OperationBinding{ContractID: "copy", OperationKey: key}}}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{{ID: "linked", Mode: "linked", Document: []byte(snapshot.IdentityDocument), Source: &designscenario.ContractSource{DesignID: snapshot.DesignID, RevisionID: snapshot.RevisionID, Version: snapshot.Version}}, {ID: "copy", Mode: "copy", Document: []byte(snapshot.IdentityDocument), Source: &designscenario.ContractSource{DesignID: snapshot.DesignID, RevisionID: snapshot.RevisionID, Version: snapshot.Version}}}}
	doc.Contracts[1].Source.Version = 0 // Existing COPY provenance may omit a known source version.
	scenario, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	artifact := backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(scenario.Scenario.ID, 10)}
	in := backendmodel.PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []backendmodel.ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: artifact, RevisionID: strconv.FormatInt(scenario.Draft.ID, 10), EditorBindings: []backendmodel.EditorBindingInput{}, Reason: "projection"}}}
	root := "/api/backend-projects/" + base.Project.ID + "/artifacts"
	var preview backendmodel.ArtifactPinsPreview
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/preview", in, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.CanApply {
		t.Fatal(preview)
	}
	var result backendmodel.ArtifactPinsResult
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/commands", backendmodel.ApplyArtifactPinsInput{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: in.Commands, CandidateHash: preview.CandidateHash, IdempotencyKey: "copy"}, 200), &result); err != nil {
		t.Fatal(err)
	}
	before := artifactTransportOwnerRows(t, s)
	var page backendmodel.ArtifactProjectionPage
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/query", backendmodel.ArtifactQueryInput{RevisionID: result.Revision.ID, Artifact: artifact, View: "sequence"}, 200), &page); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range page.Items {
		if item.Data.SequenceMessage == nil {
			continue
		}
		if item.Data.SequenceMessage.Operation.OperationKey != key || item.Locator.Embedded == nil {
			t.Fatal("copied operation key narrowed", item)
		}
		e := item.Locator.Embedded
		seen[e.ContractID] = true
		expectedOriginVersion := strconv.FormatInt(snapshot.Version, 10)
		if e.ContractID == "copy" {
			expectedOriginVersion = "0"
		}
		if e.Origin.DesignID != strconv.FormatInt(snapshot.DesignID, 10) || e.Origin.RevisionID != strconv.FormatInt(snapshot.RevisionID, 10) || e.Origin.Version != expectedOriginVersion {
			t.Fatal("exact origin identity/version changed", e)
		}
		if (e.ContractID == "linked" && e.OriginStatus != "verified") || (e.ContractID == "copy" && e.OriginStatus != "copy") || len(item.SourceNodeIDs) != 0 {
			t.Fatal("copy scope conflated", item)
		}
	}
	if !seen["linked"] || !seen["copy"] {
		t.Fatal("missing scoped copies", page)
	}
	if artifactTransportOwnerRows(t, s) != before {
		t.Fatal("copy projection wrote owners")
	}
}

func TestBackendArtifactApplyReservationTransport(t *testing.T) {
	s := loopbackTestServer(t, nil)
	base, node := backendAPIArtifactFixture(t, s)
	d := artifactTransportScenario(t, s)
	in := backendmodel.PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []backendmodel.ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(d.Scenario.ID, 10)}, RevisionID: strconv.FormatInt(d.Draft.ID, 10), EditorBindings: []backendmodel.EditorBindingInput{{Selector: backendmodel.EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{node}}}, Reason: "manual"}}}
	envelope := map[string]any{"projectId": base.Project.ID, "baseRevisionId": in.BaseRevisionID, "expectedVersion": in.ExpectedVersion, "commands": in.Commands, "candidateHash": strings.Repeat("a", 64), "idempotencyKey": strings.Repeat(`\`, backendmodel.MaxKeyLength)}
	reserve, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(len(reserve))
	root := "/api/backend-projects/" + base.Project.ID + "/artifacts"
	before := artifactTransportOwnerRows(t, s)
	s.cfg.MaxBody = size + backendmodel.ArtifactApplyRPCFramingBytes - 1
	s.backendArtifacts = backendmodel.NewArtifactServiceWithBodyLimit(s.backendRepo, s.designsRepo, s.scenarioArtifactSnapshots, s.cfg.MaxBody)
	raw := artifactTransportCall(t, s, "POST", root+"/preview", in, 413)
	// Exact numeric details are validated without float conversion.
	var details struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Allowed  int64 `json:"allowedBytes"`
				Reserved int64 `json:"reservedApplyBytes"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &details); err != nil || details.Error.Code != "backend_artifact_apply_body_limit" || details.Error.Details.Allowed != size-1 || details.Error.Details.Reserved != size {
		t.Fatal("configured reserve", string(raw), err)
	}
	s.cfg.MaxBody = size + backendmodel.ArtifactApplyRPCFramingBytes
	s.backendArtifacts = backendmodel.NewArtifactServiceWithBodyLimit(s.backendRepo, s.designsRepo, s.scenarioArtifactSnapshots, s.cfg.MaxBody)
	var preview backendmodel.ArtifactPinsPreview
	if err := json.Unmarshal(artifactTransportCall(t, s, "POST", root+"/preview", in, 200), &preview); err != nil || !preview.CanApply {
		t.Fatal(preview, err)
	}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	padded := append([]byte(" \n "), body...)
	status, bytesBody, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", root+"/preview", padded)
	if err != nil || status != 200 {
		t.Fatal("decoded reservation counted whitespace", status, string(bytesBody), err)
	}
	padded = append([]byte(strings.Repeat(" ", int(s.cfg.MaxBody)-len(body)+1)), body...)
	status, bytesBody, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", root+"/preview", padded)
	if err != nil || status != 413 {
		t.Fatal("original padding cap bypassed", status, string(bytesBody), err)
	}
	if artifactTransportOwnerRows(t, s) != before {
		t.Fatal("reserve/padding admission wrote owners")
	}
	apply := backendmodel.ApplyArtifactPinsInput{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: in.Commands, CandidateHash: preview.CandidateHash, IdempotencyKey: strings.Repeat(`\`, backendmodel.MaxKeyLength)}
	original := artifactTransportCall(t, s, "POST", root+"/commands", apply, 200)
	s.cfg.MaxBody = size + backendmodel.ArtifactApplyRPCFramingBytes - 1
	s.backendArtifacts = backendmodel.NewArtifactServiceWithBodyLimit(s.backendRepo, nil, nil, s.cfg.MaxBody)
	if !bytes.Equal(original, artifactTransportCall(t, s, "POST", root+"/commands", apply, 200)) {
		t.Fatal("reservation blocked exact receipt replay")
	}
	artifactTransportCall(t, s, "POST", root+"/query", backendmodel.ArtifactQueryInput{RevisionID: preview.BaseRevisionID, Artifact: in.Commands[0].Artifact, View: "sequence"}, 404)
}

func TestBackendArtifactApplyReservationCompactTransport(t *testing.T) {
	s := loopbackTestServer(t, nil)
	base, node := backendAPIArtifactFixture(t, s)
	doc := designscenario.Document{FormatVersion: 3, Title: "Compact boundary", Participants: []designscenario.Participant{}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}
	for i, n := range []int{50000, 50000, 30000} {
		doc.Participants = append(doc.Participants, designscenario.Participant{ID: strings.Repeat(string(rune('a'+i)), n), Name: strconv.Itoa(i), Kind: "service"})
	}
	d, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := backendmodel.PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []backendmodel.ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(d.Scenario.ID, 10)}, RevisionID: strconv.FormatInt(d.Draft.ID, 10), EditorBindings: []backendmodel.EditorBindingInput{}, Reason: "x"}}}
	for _, p := range doc.Participants {
		in.Commands[0].EditorBindings = append(in.Commands[0].EditorBindings, backendmodel.EditorBindingInput{Selector: backendmodel.EditorSelector{Kind: "participant", ParticipantID: p.ID}, SourceNodeIDs: []string{node}})
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	target := backendmodel.MaxAPIPinBodyBytes - 52
	delta := target - len(raw)
	doc.Participants[2].ID += strings.Repeat("c", delta)
	in.Commands[0].EditorBindings[2].Selector.ParticipantID = doc.Participants[2].ID
	d, err = s.designScenariosRepo.Save(t.Context(), d.Scenario.ID, designscenario.SaveInput{ExpectedVersion: d.Scenario.Version, Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in.Commands[0].RevisionID = strconv.FormatInt(d.Draft.ID, 10)
	raw, err = json.Marshal(in)
	if err != nil || len(raw) != target {
		t.Fatal("actual compact body", len(raw), err)
	}
	before := artifactTransportOwnerRows(t, s)
	root := "/api/backend-projects/" + base.Project.ID + "/artifacts"
	got := artifactTransportCall(t, s, "POST", root+"/preview", in, 413)
	var fault struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Allowed  int64 `json:"allowedBytes"`
				Reserved int64 `json:"reservedApplyBytes"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(got, &fault); err != nil || fault.Error.Code != "backend_artifact_apply_body_limit" || fault.Error.Details.Allowed != backendmodel.MaxAPIPinBodyBytes || fault.Error.Details.Reserved <= backendmodel.MaxAPIPinBodyBytes {
		t.Fatal("compact reservation refusal", string(got), err)
	}
	current, err := s.backendRepo.Get(t.Context(), base.Project.ID)
	if err != nil || current.Version != base.Project.Version || current.CurrentRevisionID != base.Revision.ID || artifactTransportOwnerRows(t, s) != before {
		t.Fatal("compact rejection wrote backend/owners", err)
	}
}

package admin

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/scenarioexport"
)

type exportScenarioStub struct {
	designScenarioService
	revision designscenario.Revision
}

func (s exportScenarioStub) Revision(_ context.Context, id, rid int64) (designscenario.Revision, error) {
	if id != 7 || rid != 11 {
		return designscenario.Revision{}, designscenario.ErrNotFound
	}
	return s.revision, nil
}

func exportTestServer() *Server {
	return &Server{cfg: &config.Config{MaxBody: 1 << 20}, log: slog.New(slog.NewTextHandler(io.Discard, nil)), designScenariosRepo: exportScenarioStub{revision: designscenario.Revision{
		RevisionSummary: designscenario.RevisionSummary{ID: 11, ScenarioID: 7, Hash: "saved-hash"},
		Document:        designscenario.Document{FormatVersion: 1, Title: "Saved", Participants: []designscenario.Participant{{ID: "a", Name: "A", Kind: "service"}}, Messages: []designscenario.Message{}, Contracts: []designscenario.Contract{}, Fragments: []designscenario.Fragment{}}, FormDrafts: map[string]string{},
	}}}
}

func exportRequest(format, rid string, authenticated bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/design-scenarios/7/revisions/"+rid+"/exports/"+format, nil)
	r.SetPathValue("id", "7")
	r.SetPathValue("rid", rid)
	r.SetPathValue("format", format)
	if authenticated {
		r = r.WithContext(withAuthContext(r.Context(), &auth.Session{}, &auth.User{ID: 1}))
	}
	return r
}

func TestScenarioExportReturnsSavedArtifactAndRejectsBadRequests(t *testing.T) {
	s := exportTestServer()
	for _, tt := range []struct {
		format, rid string
		auth        bool
		status      int
	}{{"markdown", "11", true, 200}, {"html", "11", true, 200}, {"mermaid", "11", true, 200}, {"plantuml", "11", true, 200}, {"mermaid", "12", true, 404}, {"unknown", "11", true, 400}, {"mermaid", "11", false, 401}, {"openapi-json", "11", true, 404}} {
		rr := httptest.NewRecorder()
		s.handleExportDesignScenario(rr, exportRequest(tt.format, tt.rid, tt.auth))
		if rr.Code != tt.status {
			t.Fatalf("%+v: %d %s", tt, rr.Code, rr.Body.String())
		}
		if tt.status == 200 {
			var a scenarioexport.Artifact
			if err := jsonx.Unmarshal(rr.Body.Bytes(), &a); err != nil {
				t.Fatal(err)
			}
			if a.RevisionID != 11 || a.SourceHash != "saved-hash" || !strings.Contains(a.Content, "A") {
				t.Fatalf("wrong snapshot: %+v", a)
			}
		}
	}
	s.cfg.MaxBody = 10
	rr := httptest.NewRecorder()
	s.handleExportDesignScenario(rr, exportRequest("mermaid", "11", true))
	if rr.Code != 413 {
		t.Fatalf("limit: %d %s", rr.Code, rr.Body.String())
	}
}

func TestScenarioExportOptionsAndBlockedResponse(t *testing.T) {
	s := exportTestServer()
	rr := httptest.NewRecorder()
	s.handleDesignScenarioExportOptions(rr, exportRequest("mermaid", "11", true))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"contract_missing"`) {
		t.Fatalf("options: %d %s", rr.Code, rr.Body.String())
	}
	s.designScenariosRepo = exportScenarioStub{revision: designscenario.Revision{RevisionSummary: designscenario.RevisionSummary{ID: 11, ScenarioID: 7}}}
	rr = httptest.NewRecorder()
	s.handleExportDesignScenario(rr, exportRequest("mermaid", "11", true))
	if rr.Code != 422 || !strings.Contains(rr.Body.String(), "diagram_empty") {
		t.Fatalf("blocked: %d %s", rr.Code, rr.Body.String())
	}
}

func TestScenarioExportBlockedResponseRespectsLimit(t *testing.T) {
	s := exportTestServer()
	s.designScenariosRepo = exportScenarioStub{revision: designscenario.Revision{RevisionSummary: designscenario.RevisionSummary{ID: 11, ScenarioID: 7}}}
	s.cfg.MaxBody = 200
	rr := httptest.NewRecorder()
	s.handleExportDesignScenario(rr, exportRequest("mermaid", "11", true))
	if rr.Code != 413 {
		t.Fatalf("oversized diagnostics: %d %s", rr.Code, rr.Body.String())
	}
}

func TestScenarioDocumentationExplainsPrintPageLimit(t *testing.T) {
	s := exportTestServer()
	rr := httptest.NewRecorder()
	s.scenarioExportError(rr, fmt.Errorf("%w: %w", scenarioexport.ErrTooLarge, scenarioexport.ErrTooManyPages))
	if rr.Code != 413 || !strings.Contains(rr.Body.String(), "200") || !strings.Contains(rr.Body.String(), "Markdown") {
		t.Fatalf("print limit not explained: %d %s", rr.Code, rr.Body.String())
	}
}

func TestScenarioArchiveStrictBodyAndErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body, rid string
		auth            bool
		status          int
	}{
		{"success", `{"items":[{"format":"mermaid"},{"format":"markdown"}]}`, "11", true, 200},
		{"unauthorized", `{"items":[{"format":"mermaid"}]}`, "11", false, 401},
		{"revision", `{"items":[{"format":"mermaid"}]}`, "12", true, 404},
		{"empty", `{"items":[]}`, "11", true, 400},
		{"null", `null`, "11", true, 400},
		{"duplicate", `{"items":[{"format":"mermaid"},{"format":"mermaid"}]}`, "11", true, 400},
		{"unknown field", `{"items":[{"format":"mermaid"}],"extra":true}`, "11", true, 400},
		{"unknown item field", `{"items":[{"format":"mermaid","extra":true}]}`, "11", true, 400},
		{"trailing", `{"items":[{"format":"mermaid"}]} {}`, "11", true, 400},
		{"svg", `{"items":[{"format":"svg"}]}`, "11", true, 400},
		{"unknown contract", `{"items":[{"format":"openapi-json","contractId":"missing"}]}`, "11", true, 404},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := exportTestServer()
			r := archiveRequest(tt.body, tt.rid, tt.auth)
			w := httptest.NewRecorder()
			s.handleExportDesignScenarioArchive(w, r)
			if w.Code != tt.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if w.Code == 200 {
				var got scenarioexport.ArchiveArtifact
				if err := jsonx.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.ContentBase64 == "" || got.Manifest.Scenario.RevisionID != 11 || got.Manifest.Scenario.SourceHash != "saved-hash" || len(got.Manifest.Files) != 2 {
					t.Fatalf("bad snapshot: %+v", got)
				}
			} else if strings.Contains(w.Body.String(), "contentBase64") {
				t.Fatal("partial archive leaked")
			}
		})
	}
	s := exportTestServer()
	s.cfg.MaxBody = 100
	w := httptest.NewRecorder()
	s.handleExportDesignScenarioArchive(w, archiveRequest(`{"items":[{"format":"mermaid"}]}`, "11", true))
	if w.Code != 413 {
		t.Fatalf("output budget: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.handleExportDesignScenarioArchive(w, archiveRequest(`{"items":[{"format":"`+strings.Repeat("a", 101)+`"}]}`, "11", true))
	if w.Code != 413 {
		t.Fatalf("request budget: %d %s", w.Code, w.Body.String())
	}
	s = exportTestServer()
	s.designScenariosRepo = exportScenarioStub{revision: designscenario.Revision{RevisionSummary: designscenario.RevisionSummary{ID: 11, ScenarioID: 7}}}
	w = httptest.NewRecorder()
	s.handleExportDesignScenarioArchive(w, archiveRequest(`{"items":[{"format":"mermaid"}]}`, "11", true))
	if w.Code != 422 || !strings.Contains(w.Body.String(), "diagram_empty") {
		t.Fatalf("blocked diagnostics: %d %s", w.Code, w.Body.String())
	}
}

func archiveRequest(body, rid string, authenticated bool) *http.Request {
	r := exportRequest("", rid, authenticated)
	r.Method = http.MethodPost
	r.Body = io.NopCloser(strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

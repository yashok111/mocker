package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
)

type designScenarioServiceStub struct {
	designScenarioService
	create func(designscenario.CreateInput) (*designscenario.Detail, error)
}

func (s designScenarioServiceStub) Create(_ context.Context, in designscenario.CreateInput) (*designscenario.Detail, error) {
	return s.create(in)
}

func TestDesignScenarioRoutesAreRegistered(t *testing.T) {
	t.Parallel()
	want := []string{
		"GET /api/design-scenarios",
		"POST /api/design-scenarios",
		"GET /api/design-scenarios/{id}",
		"PUT /api/design-scenarios/{id}/draft",
		"POST /api/design-scenarios/{id}/commands",
		"GET /api/design-scenarios/{id}/revisions/{rid}",
		"GET /api/design-scenarios/{id}/diff",
		"POST /api/design-scenarios/{id}/restore",
		"POST /api/design-scenarios/{id}/validate",
	}

	patterns := make([]string, 0, len((&Server{}).routes()))
	for _, route := range (&Server{}).routes() {
		patterns = append(patterns, route.pattern)
	}
	for _, pattern := range want {
		if !slices.Contains(patterns, pattern) {
			t.Errorf("route %q is not registered", pattern)
		}
	}
}

func TestCreateDesignScenarioUsesAuthenticatedActorAndStrictBody(t *testing.T) {
	t.Parallel()

	var captured designscenario.CreateInput
	s := &Server{
		cfg: &config.Config{MaxBody: 1_000},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		designScenariosRepo: designScenarioServiceStub{create: func(in designscenario.CreateInput) (*designscenario.Detail, error) {
			captured = in
			return &designscenario.Detail{Scenario: designscenario.Scenario{ID: 1, Version: 1}}, nil
		}},
	}
	user := &auth.User{ID: 9}
	body := `{"document":{"formatVersion":1,"title":"Checkout","participants":[],"messages":[],"fragments":[],"contracts":[]},"formDrafts":{"operation:1":"pending"},"summary":"initial"}`
	req := httptest.NewRequest(http.MethodPost, "/api/design-scenarios", strings.NewReader(body))
	req = req.WithContext(withAuthContext(req.Context(), nil, user))
	rec := httptest.NewRecorder()

	s.handleCreateDesignScenario(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if captured.Source != "mcp" || captured.OwnerID == nil || *captured.OwnerID != user.ID {
		t.Fatalf("service actor = source %q owner %v", captured.Source, captured.OwnerID)
	}
	if captured.Document.Title != "Checkout" || captured.FormDrafts["operation:1"] != "pending" {
		t.Fatalf("service input = %+v", captured)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/design-scenarios", strings.NewReader(`{"document":{},"unknown":true}`))
	req = req.WithContext(withAuthContext(req.Context(), &auth.Session{}, user))
	rec = httptest.NewRecorder()
	s.handleCreateDesignScenario(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", rec.Code, rec.Body.String())
	}

	s.cfg.MaxBody = 20
	req = httptest.NewRequest(http.MethodPost, "/api/design-scenarios", strings.NewReader(body))
	req = req.WithContext(withAuthContext(req.Context(), &auth.Session{}, user))
	rec = httptest.NewRecorder()
	s.handleCreateDesignScenario(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateDesignScenarioColorWireContract(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		color string
		valid bool
	}{
		{"hex", `"#aBcDeF"`, true},
		{"empty", `""`, false},
		{"short", `"#abc"`, false},
		{"transparent", `"#11223344"`, false},
		{"named", `"red"`, false},
		{"null", `null`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			called := false
			s := &Server{
				cfg: &config.Config{MaxBody: 2_000},
				log: slog.New(slog.NewTextHandler(io.Discard, nil)),
				designScenariosRepo: designScenarioServiceStub{create: func(in designscenario.CreateInput) (*designscenario.Detail, error) {
					called = true
					if in.Document.Participants[0].Color != "#aBcDeF" || in.Document.Messages[0].Color != "#aBcDeF" || in.Document.Messages[0].ArrowColor != "#aBcDeF" {
						t.Fatalf("color lost at REST boundary: %+v", in.Document)
					}
					return &designscenario.Detail{Draft: designscenario.Revision{Document: in.Document}}, nil
				}},
			}
			body := `{"document":{"formatVersion":1,"title":"Colors","participants":[{"id":"api","name":"API","kind":"service","description":"","color":COLOR}],"messages":[{"id":"call","fromId":"api","toId":"api","kind":"request","label":"Call","description":"","color":COLOR,"arrowColor":COLOR}],"fragments":[],"contracts":[]}}`
			req := httptest.NewRequest(http.MethodPost, "/api/design-scenarios", strings.NewReader(strings.ReplaceAll(body, "COLOR", tt.color)))
			req = req.WithContext(withAuthContext(req.Context(), nil, &auth.User{ID: 9}))
			rec := httptest.NewRecorder()
			s.handleCreateDesignScenario(rec, req)
			wantStatus := http.StatusBadRequest
			if tt.valid {
				wantStatus = http.StatusCreated
			}
			if rec.Code != wantStatus || called != tt.valid {
				t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
			}
			if tt.valid && !strings.Contains(rec.Body.String(), `"arrowColor":"#aBcDeF"`) {
				t.Fatalf("response lost color: %s", rec.Body.String())
			}
		})
	}
}

func TestDesignScenarioErrorMapsStructuredDetailsAndHidesInternalErrors(t *testing.T) {
	t.Parallel()
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{
			name:       "invalid",
			err:        &designscenario.InvalidError{Diagnostics: []designscenario.Diagnostic{{Pointer: "/title", Message: "required", Severity: "error"}}},
			wantStatus: http.StatusBadRequest,
			wantCode:   "design_scenario_invalid",
			wantDetail: "/title",
		},
		{
			name:       "scenario conflict",
			err:        &designscenario.ConflictError{Version: 4, DraftRevisionID: 8},
			wantStatus: http.StatusConflict,
			wantCode:   "design_scenario_conflict",
			wantDetail: `"version":4`,
		},
		{
			name:       "linked API conflict",
			err:        &designscenario.LinkedConflictError{ContractID: "orders", DesignID: 3, Version: 7, DraftRevisionID: 11},
			wantStatus: http.StatusConflict,
			wantCode:   "design_scenario_conflict",
			wantDetail: `"contractId":"orders"`,
		},
		{name: "missing", err: designscenario.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: httpx.CodeNotFound},
		{name: "too large", err: designscenario.ErrTooLarge, wantStatus: http.StatusRequestEntityTooLarge, wantCode: httpx.CodeTooLarge},
		{name: "internal", err: errors.New("database secret"), wantStatus: http.StatusInternalServerError, wantCode: httpx.CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.designScenarioError(rec, tt.err)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var envelope httpx.ErrorBody
			if err := jsonx.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != tt.wantCode {
				t.Errorf("code=%q want %q", envelope.Error.Code, tt.wantCode)
			}
			if tt.wantDetail != "" && !strings.Contains(rec.Body.String(), tt.wantDetail) {
				t.Errorf("body=%s, want detail %s", rec.Body.String(), tt.wantDetail)
			}
			if tt.name == "internal" && strings.Contains(rec.Body.String(), "secret") {
				t.Errorf("internal error leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestDesignScenarioRESTRejectsMalformedCommandUnionWithoutChangingVersion(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	source := loopbackTestSrc()
	create := []byte(`{"document":{"formatVersion":1,"title":"Original","participants":[],"messages":[],"fragments":[],"contracts":[]}}`)
	status, body, err := s.CallAsMCP(t.Context(), source, http.MethodPost, "/api/design-scenarios", create)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s err=%v", status, body, err)
	}
	var created designscenario.Detail
	if err := jsonx.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		body string
	}{
		{name: "missing title", body: `{"expectedVersion":1,"commands":[{"type":"set_title"}]}`},
		{name: "irrelevant label", body: `{"expectedVersion":1,"commands":[{"type":"set_title","title":"Wanted","label":"ignored"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body, err := s.CallAsMCP(t.Context(), source, http.MethodPost,
				fmt.Sprintf("/api/design-scenarios/%d/commands", created.Scenario.ID), []byte(tt.body))
			if err != nil || status != http.StatusBadRequest {
				t.Fatalf("command: status=%d body=%s err=%v", status, body, err)
			}
			status, body, err = s.CallAsMCP(t.Context(), source, http.MethodGet,
				fmt.Sprintf("/api/design-scenarios/%d", created.Scenario.ID), nil)
			if err != nil || status != http.StatusOK {
				t.Fatalf("readback: status=%d body=%s err=%v", status, body, err)
			}
			var detail designscenario.Detail
			if err := jsonx.Unmarshal(body, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Scenario.Version != 1 || detail.Draft.Document.Title != "Original" {
				t.Fatalf("rejected command changed state: %+v", detail)
			}
		})
	}
}

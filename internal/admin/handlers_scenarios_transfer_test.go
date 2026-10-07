package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
)

type transferStub struct {
	designScenarioService
	calls int
}

func (s *transferStub) ExportTransfer(_ context.Context, ids []int64, history bool) (designscenario.TransferBundle, error) {
	s.calls++
	return designscenario.TransferBundle{Kind: "mocker.scenarios", FormatVersion: 1, Scenarios: []designscenario.TransferScenario{}}, nil
}
func (s *transferStub) ImportTransfer(_ context.Context, bundle designscenario.TransferBundle, relink bool) (designscenario.TransferResult, error) {
	s.calls++
	return designscenario.TransferResult{Scenarios: []designscenario.Scenario{{ID: 42}}}, nil
}
func TestScenarioTransferHTTPGuards(t *testing.T) {
	stub := &transferStub{}
	s := &Server{cfg: &config.Config{MaxBody: 2000000}, designScenariosRepo: stub}
	for _, tc := range []struct {
		path, body string
		handler    http.HandlerFunc
	}{
		{"/api/design-scenarios/transfer-export", `{"scenarioIds":[1],"includeHistory":true}`, s.handleExportScenarioTransfer},
		{"/api/design-scenarios/transfer-import", `{"bundle":{"kind":"mocker.scenarios","formatVersion":1,"scenarios":[]},"relink":true}`, s.handleImportScenarioTransfer},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		rec := httptest.NewRecorder()
		tc.handler(rec, req)
		if rec.Code != 401 {
			t.Fatalf("unauthenticated = %d", rec.Code)
		}
		req = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req = req.WithContext(withAuthContext(req.Context(), &auth.Session{}, &auth.User{ID: 1}))
		rec = httptest.NewRecorder()
		tc.handler(rec, req)
		if rec.Code != 200 && rec.Code != 201 {
			t.Fatalf("authorized = %d: %s", rec.Code, rec.Body.String())
		}
		before := stub.calls
		req = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"unknown":true}`))
		req = req.WithContext(withAuthContext(req.Context(), &auth.Session{}, &auth.User{ID: 1}))
		rec = httptest.NewRecorder()
		tc.handler(rec, req)
		if rec.Code != 400 || stub.calls != before {
			t.Fatal("unknown fields accepted")
		}
	}
}

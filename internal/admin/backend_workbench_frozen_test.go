package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

// TestBackendWorkbenchFrozen_hidesEveryWorkbenchRoute pins the 2026-10-12
// freeze from both sides of the admin plane: a frozen server answers 404 on
// the workbench prefix (over HTTP and over CallAsMCP, which share one mux)
// and tells the panel through /api/me, while a thawed one still serves it.
func TestBackendWorkbenchFrozen_hidesEveryWorkbenchRoute(t *testing.T) {
	t.Parallel()
	for _, frozen := range []bool{true, false} {
		ts := newTestServerCfg(t, func(c *config.Config) { c.BackendWorkbenchFrozen = frozen })
		cookie, csrf := ts.login(t, "alice")

		rec := ts.do(jsonRequest(t, http.MethodGet, "http://mocker.local/api/backend-projects", nil, cookie, csrf))
		wantStatus := http.StatusOK
		if frozen {
			wantStatus = http.StatusNotFound
		}
		if rec.Code != wantStatus {
			t.Fatalf("frozen=%v: GET /api/backend-projects = %d, want %d; body = %s", frozen, rec.Code, wantStatus, rec.Body.String())
		}
		if frozen && !strings.Contains(rec.Body.String(), "MOCKER_BACKEND_WORKBENCH") {
			t.Errorf("frozen 404 does not name the switch: %s", rec.Body.String())
		}

		rec = ts.do(jsonRequest(t, http.MethodGet, "http://mocker.local/api/me", nil, cookie, ""))
		var me struct {
			Config struct {
				BackendWorkbench *bool `json:"backendWorkbench"`
			} `json:"config"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.Config.BackendWorkbench == nil {
			t.Fatalf("frozen=%v: /api/me config.backendWorkbench missing: %v %s", frozen, err, rec.Body.String())
		}
		if *me.Config.BackendWorkbench == frozen {
			t.Errorf("frozen=%v: /api/me config.backendWorkbench = %v", frozen, *me.Config.BackendWorkbench)
		}

		// A workspace route is untouched either way.
		rec = ts.do(jsonRequest(t, http.MethodGet, "http://mocker.local/api/workspaces", nil, cookie, ""))
		if rec.Code != http.StatusOK {
			t.Errorf("frozen=%v: GET /api/workspaces = %d", frozen, rec.Code)
		}
	}
}

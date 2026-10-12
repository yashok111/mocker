package admin

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

func TestBackendWorkbenchFrozen_callAsMCPLosesTheRoutes(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, func(c *config.Config) { c.BackendWorkbenchFrozen = true })
	status, _, err := srv.CallAsMCP(t.Context(), loopbackTestSrc(), http.MethodGet, "/api/backend-projects/capabilities", nil)
	if err != nil {
		t.Fatalf("CallAsMCP: %v", err)
	}
	if status != http.StatusNotFound {
		t.Fatalf("frozen CallAsMCP GET capabilities = %d, want 404", status)
	}
}

// TestBackendWorkbenchPrefixCoversEveryWorkbenchHandler is the reason one
// path prefix is enough to freeze the workbench: every row whose handler is a
// workbench handler sits under it, and nothing else does.
func TestBackendWorkbenchPrefixCoversEveryWorkbenchHandler(t *testing.T) {
	t.Parallel()
	n := 0
	for _, rt := range (&Server{}).routes() {
		name := runtime.FuncForPC(reflect.ValueOf(rt.handler).Pointer()).Name()
		isWorkbenchHandler := strings.Contains(name, "Backend")
		if isWorkbenchHandler != isBackendWorkbenchRoute(rt.pattern) {
			t.Errorf("%s (%s): workbench handler = %v, under %s = %v", rt.pattern, name, isWorkbenchHandler, backendWorkbenchPrefix, !isWorkbenchHandler)
		}
		if isWorkbenchHandler {
			n++
		}
	}
	if n == 0 {
		t.Fatal("no workbench routes found; the freeze test is vacuous")
	}
	rec := httptest.NewRecorder()
	handleBackendWorkbenchFrozen(rec, httptest.NewRequest(http.MethodGet, "/api/backend-projects", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("frozen handler = %d", rec.Code)
	}
}

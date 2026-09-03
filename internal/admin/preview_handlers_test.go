package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// stubPreviewer is an [admin.Previewer] whose Preview always answers err,
// regardless of the request — enough to prove the WIRE MAPPING
// [admin.Server.answerPreviewResult] applies to one taxonomy sentinel,
// without needing a real *mockplane.Plane, a spec or an override behind it.
type stubPreviewer struct {
	err error
}

func (p stubPreviewer) Preview(context.Context, *workspaces.Workspace, domain.PreviewRequest) (domain.PreviewResult, error) {
	return domain.PreviewResult{}, p.err
}

// newPreviewTestServer builds a *testServer identical to newTestServer's
// own (admin_test.go), plus the underlying *admin.Server itself — which
// plain newTestServer does not hand back — so this file's own test can call
// [admin.Server.SetPreviewer] before issuing any request. Kept as a
// separate helper, in this file, rather than a change to newTestServer or
// testServer's own fields: this section's scope is preview_handlers.go and
// preview_handlers_test.go, not admin_test.go.
func newPreviewTestServer(t *testing.T) (*testServer, *admin.Server) {
	t.Helper()
	cfg := testConfig(t)
	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := admin.New(cfg, sessions, ws, db, log)
	return &testServer{handler: srv.Handler(), db: db, cfg: cfg}, srv
}

// TestHandler_previewOperation_resourceServes is D12's own admin-surface
// assertion, and the ONE thing that would otherwise go unnoticed by every
// other bar: [admin.Server.answerPreviewResult]'s default branch answers a
// logged 500 for ANY unmapped error, so a [domain.ErrPreviewResourceServes]
// left out of that switch would still compile, still lint clean, and still
// leave `make test` green — just as a 500, not the 409 resource_serves this
// clause exists to pin.
func TestHandler_previewOperation_resourceServes(t *testing.T) {
	t.Parallel()
	ts, srv := newPreviewTestServer(t)
	cookie, csrfToken, id, _ := ts.createWorkspace(t, "Alex", "Demo")

	srv.SetPreviewer(stubPreviewer{err: domain.ErrPreviewResourceServes})

	body := map[string]any{
		"opKey": "GET /widgets",
		"draft": map[string]any{},
	}
	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/preview", int64(id)), body, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("preview of a resource-served route: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}

	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errBody.Error.Code != "resource_serves" {
		t.Errorf("error code = %q, want %q", errBody.Error.Code, "resource_serves")
	}
}

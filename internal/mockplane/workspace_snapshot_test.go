package mockplane_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestServeWorkspaceSnapshotNeverDispatchesControls(t *testing.T) {
	ws := &workspaces.Workspace{ID: 1, Slug: "snapshot", Settings: domain.DefaultSettings()}
	plane := newPlane(ws)
	for _, path := range []string{"/__mocker/health", "/%5F%5Fmocker/health", "//__mocker//health"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		live := httptest.NewRecorder()
		plane.ServeSlug(live, request, ws.Slug)
		if live.Code != http.StatusOK {
			t.Fatalf("live control status=%d body=%s", live.Code, live.Body.String())
		}
		snapshot := httptest.NewRecorder()
		plane.ServeWorkspace(snapshot, request, ws)
		if snapshot.Code != http.StatusNotFound {
			t.Fatalf("snapshot exposed control path=%q status=%d body=%s", path, snapshot.Code, snapshot.Body.String())
		}
	}
}

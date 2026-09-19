package mockplane

import (
	"context"
	"net/http"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/workspaces"
)

type managedWorkspaceSource interface {
	ManagedDesign(context.Context, int64) (int64, error)
}

func (p *Plane) refuseManagedControl(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	source, ok := p.src.(managedWorkspaceSource)
	if !ok {
		return false
	}
	id, err := source.ManagedDesign(r.Context(), ws.ID)
	if err != nil {
		p.log.Error("managed workspace lookup", "err", err)
		httpx.Err(w, 500, httpx.CodeInternal, "failed to read workspace")
		return true
	}
	if id == 0 {
		return false
	}
	httpx.ErrDetails(w, http.StatusConflict, "managed_workspace", "Изменяйте этот мок через редактор API", map[string]int64{"designId": id})
	return true
}

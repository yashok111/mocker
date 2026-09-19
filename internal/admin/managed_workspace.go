package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/httpx"
)

// Every legacy workspace mutation enters here through the shared route mux,
// including MCP and RAM-only controls. Only read-like POSTs and a fork are exempt.
func (s *Server) withManagedWorkspaceGuard(pattern string, next http.HandlerFunc) http.HandlerFunc {
	method, path, _ := strings.Cut(pattern, " ")
	if !strings.HasPrefix(path, "/api/workspaces/{id}") || method == http.MethodGet || method == http.MethodHead {
		return next
	}
	switch path {
	case "/api/workspaces/{id}/fork", "/api/workspaces/{id}/preview", "/api/workspaces/{id}/endpoints/preview", "/api/workspaces/{id}/probe":
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			next(w, r)
			return
		}
		designID, err := s.ws.ManagedDesign(r.Context(), id)
		if err != nil {
			s.log.Error("read managed workspace membership", "err", err)
			httpx.Err(w, 500, httpx.CodeInternal, "failed to read workspace")
			return
		}
		if designID != 0 {
			httpx.ErrDetails(w, 409, "managed_workspace", "Изменяйте этот мок через редактор API", map[string]int64{"designId": designID})
			return
		}
		next(w, r)
	}
}

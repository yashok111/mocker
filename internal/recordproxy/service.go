package recordproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/probe"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// The connection stays bounded while allowing separate upload and upstream
// budgets, plus time to store and send the final response (including a 504).
const proxyResponseWriteBudget = 30 * time.Second

type Service struct {
	repo   *Repo
	policy *config.Config
}

func NewService(repo *Repo, policy *config.Config) *Service {
	return &Service{repo: repo, policy: policy}
}

// ServeProxy returns false only when ordinary mock execution was selected.
func (s *Service) ServeProxy(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, operation string, auth bool, capture func([]byte) (int, error)) bool {
	c, err := s.repo.Get(r.Context(), ws.ID)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "proxy_config_failed", "Не удалось прочитать настройки прокси")
		return true
	}
	mode := c.EffectiveMode(operation)
	if mode == "off" {
		return false
	}
	w.Header().Set("X-Mocker-Proxy-Mode", mode)
	if len(r.URL.EscapedPath()) > 8192 {
		httpx.Err(w, http.StatusRequestURITooLong, "proxy_path_too_long", "Путь запроса превышает 8192 байта")
		return true
	}
	if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" {
		httpx.Err(w, http.StatusBadRequest, "proxy_protocol_unsupported", "Прокси поддерживает обычные HTTP-запросы")
		return true
	}
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	controller := http.NewResponseController(w)
	now := time.Now()
	_ = controller.SetWriteDeadline(now.Add(2*timeout + proxyResponseWriteBudget))
	_ = controller.SetReadDeadline(now.Add(timeout))
	var body []byte
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, s.policy.MaxBody+1))
		if err != nil {
			var limitError *http.MaxBytesError
			if errors.As(err, &limitError) {
				httpx.Err(w, 413, "proxy_request_too_large", "Тело запроса превышает лимит")
				return true
			}
			httpx.Err(w, http.StatusBadRequest, "proxy_request_failed", "Не удалось прочитать тело запроса")
			return true
		}
	}
	if int64(len(body)) > s.policy.MaxBody {
		httpx.Err(w, http.StatusRequestEntityTooLarge, "proxy_request_too_large", "Тело запроса превышает лимит")
		return true
	}
	// A malformed query must not alias a valid request after URL.Query drops
	// malformed pairs. Forwarding it would make replay identity ambiguous.
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		httpx.Err(w, http.StatusBadRequest, "proxy_query_invalid", "Некорректные параметры запроса")
		return true
	}
	key := RequestKey(fmt.Sprintf("%s|auth=%t|cookies=%t", strings.TrimRight(c.Upstream, "/"), c.ForwardAuth, c.ForwardCookies), r, body)
	if mode == "replay" {
		s.replay(w, r, ws.ID, key, c.Version, auth)
		return true
	}
	cookiePath := "/"
	if s.policy.Routing == config.RoutingPath {
		cookiePath = "/w/" + ws.Slug + "/"
	}
	response, err := probe.ProxyExchange(r.Context(), c.Upstream, r, body, probe.ProxyOptions{Allowlist: s.policy.ProxyAllowlist, CAPEM: s.policy.ProxyCAPEM, Timeout: timeout, MaxResponse: s.policy.MaxResponse, ForwardAuth: c.ForwardAuth, ForwardCookies: c.ForwardCookies, CookiePath: cookiePath})
	if err != nil {
		code := 502
		var nerr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &nerr) && nerr.Timeout()) {
			code = 504
		}
		httpx.Err(w, code, "proxy_upstream_failed", "Не удалось получить ответ upstream: проверьте адрес, доступ, сертификат и лимиты")
		return true
	}
	if mode == "record" {
		s.record(w, r, ws, c, key, response, auth, capture)
	}
	for name, values := range response.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(response.Status)
	_, _ = w.Write(response.Body)
	return true
}
func (s *Service) record(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, c Config, key string, response probe.ProxyResponse, auth bool, capture func([]byte) (int, error)) {
	status := "skipped"
	defer func() { w.Header().Set("X-Mocker-Recording", status) }()
	if auth {
		status = "skipped-auth"
		return
	}
	ct := response.Header.Get("Content-Type")
	if response.Header.Get("Content-Encoding") != "" || (len(response.Body) > 0 && (!strings.Contains(strings.ToLower(ct), "json") || !jsonx.Valid(response.Body))) {
		status = "skipped-non-json"
		return
	}
	redacted, changed := traffic.RedactBody(response.Body, ct)
	rec := Recording{Key: key, Method: r.Method, Path: r.URL.EscapedPath(), Status: response.Status, ContentType: ct, Body: redacted, Redacted: changed}
	stored, err := s.repo.Record(r.Context(), ws.ID, c, rec)
	if err != nil {
		if errors.Is(err, ErrLimit) {
			status = "skipped-quota"
		} else if errors.Is(err, ErrConflict) {
			status = "skipped-config-changed"
		} else {
			status = "failed"
		}
		return
	}
	if !stored {
		status = "kept-first"
		return
	}
	status = "saved"
	if changed {
		status = "saved-redacted"
	}
	if c.CaptureEntities && r.Method == http.MethodGet && response.Status >= 200 && response.Status < 300 && capture != nil {
		n, err := capture(redacted)
		w.Header().Set("X-Mocker-Entities-Imported", strconv.Itoa(n))
		if err != nil {
			w.Header().Set("X-Mocker-Entities-Result", "partial-or-refused")
		} else {
			w.Header().Set("X-Mocker-Entities-Result", "saved")
		}
	}
}

func (s *Service) replay(w http.ResponseWriter, r *http.Request, workspaceID int64, key string, version int64, auth bool) {
	rec, err := s.repo.Lookup(r.Context(), workspaceID, key, version)
	if errors.Is(err, ErrNotFound) || auth {
		httpx.Err(w, http.StatusNotFound, "proxy_replay_miss", "Для этого запроса нет сохранённого ответа")
		return
	}
	if errors.Is(err, ErrConflict) {
		httpx.Err(w, http.StatusConflict, "proxy_config_changed", "Настройки прокси изменились во время запроса; повторите запрос")
		return
	}
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "proxy_replay_failed", "Не удалось прочитать запись")
		return
	}
	if int64(len(rec.Body)) > s.policy.MaxResponse {
		httpx.Err(w, http.StatusBadGateway, "proxy_response_too_large", "Записанный ответ превышает текущий лимит")
		return
	}
	if rec.ContentType != "" {
		w.Header().Set("Content-Type", rec.ContentType)
	}
	w.Header().Set("X-Mocker-Recording", "replayed")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(rec.Body)
}

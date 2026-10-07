package recordproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
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
	if errors.Is(err, probe.ErrProxyPathDotSegment) {
		httpx.Err(w, http.StatusBadRequest, "proxy_path_invalid", "Путь запроса содержит сегменты . или ..")
		return true
	}
	if err != nil {
		code := 502
		var nerr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &nerr) && nerr.Timeout()) {
			code = 504
		}
		httpx.Err(w, code, "proxy_upstream_failed", "Не удалось получить ответ upstream: проверьте адрес, доступ, сертификат и лимиты")
		return true
	}
	servedType, ok := servableType(response.Header.Get("Content-Type"), response.Header.Get("Content-Encoding"), response.Body)
	if !ok {
		if mode == "record" {
			w.Header().Set("X-Mocker-Recording", "skipped-unsafe-type")
		}
		refuseUnsafeType(w)
		return true
	}
	if mode == "record" {
		s.record(w, r, ws, c, key, response, auth, capture)
	}
	for name, values := range response.Header {
		w.Header()[name] = values
	}
	if servedType != "" {
		w.Header().Set("Content-Type", servedType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
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
	// A JSON ESSENCE, parsed, not a substring of the header: review
	// 2026-10-06, F178 found text/plain; x=json recorded, and
	// traffic.RedactBody then sent that JSON body to the text redactor, so a
	// "password" field was stored in clear. An empty body is still recorded
	// under whatever type it carried: the serve gate above already refused an
	// executable one, and replay gates the stored type again.
	if response.Header.Get("Content-Encoding") != "" || (len(response.Body) > 0 && (!jsonEssence(ct) || !jsonx.Valid(response.Body))) {
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
		// Redacted bytes are not data (review 2026-10-06, F180): capturing
		// them upserted every secret-named field — sort_key, api_key — as the
		// literal "[redacted]" and still said `saved`, and an id field ending
		// in _key refused the whole batch. The same rule refuses turning
		// redacted traffic into overrides (traffic.Row.Redacted). Capturing
		// the unredacted body instead was rejected: it would write the very
		// secrets redaction exists to keep out of the store.
		if changed {
			w.Header().Set("X-Mocker-Entities-Imported", "0")
			w.Header().Set("X-Mocker-Entities-Result", "skipped-redacted")
			return
		}
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
	// The stored type is gated again at serve time (review 2026-10-06, F178):
	// a recording made before the record-time gate existed carried whatever
	// the upstream sent, and replay is the path that serves it long after.
	// rec.Body is JSON or empty by the record gate, so a missing type is
	// never sniffed into HTML — but nosniff still makes that the browser's
	// rule too.
	if httpx.BrowserExecutableMediaType(rec.ContentType) {
		refuseUnsafeType(w)
		return
	}
	if rec.ContentType != "" {
		w.Header().Set("Content-Type", rec.ContentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Mocker-Recording", "replayed")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(rec.Body)
}

// servableType decides the Content-Type a proxied upstream response goes out
// under, and whether it may go out at all (review 2026-10-06, F178). The
// passthrough loop used to copy the upstream type verbatim, so text/html,
// image/svg+xml or the comma-smuggled application/json,text/html reached a
// browser from the mock plane — same origin as the admin session under
// MOCKER_ROUTING=path. The RULE is httpx.BrowserExecutableMediaType, the one
// every other mock-plane serving path applies; this only resolves which type
// it must judge.
//
// An untyped body is the subtle case: net/http's server fills a missing
// Content-Type from DetectContentType before the first write, and nosniff
// does not stop it, so an untyped HTML body would still go out as text/html.
// The sniffed type is therefore judged here and set explicitly, which keeps
// ordinary untyped bodies on exactly the type they were served under before.
// An encoded untyped body is never sniffed by net/http, and the browser would
// sniff the decoded bytes, so it is pinned to application/octet-stream.
func servableType(contentType, contentEncoding string, body []byte) (string, bool) {
	switch {
	case contentType != "":
	case len(body) == 0:
		return "", true
	case contentEncoding != "":
		contentType = "application/octet-stream"
	default:
		contentType = http.DetectContentType(body)
	}
	if httpx.BrowserExecutableMediaType(contentType) {
		return "", false
	}
	return contentType, true
}

// refuseUnsafeType answers 502: the upstream (or a stored recording of it)
// produced a response the mock plane must not serve. httpx.Err sets its own
// JSON type and nosniff.
func refuseUnsafeType(w http.ResponseWriter) {
	httpx.Err(w, http.StatusBadGateway, "proxy_upstream_unsafe_type", "Upstream вернул тип содержимого, который браузер исполняет (HTML, SVG, XML); такой ответ не отдаётся")
}

// jsonEssence reports a parsed media type whose SUBTYPE names JSON
// (application/json, application/problem+json, text/json). Judging the
// subtype rather than the whole header is what keeps a parameter such as
// x=json from passing, and every type it accepts lands in traffic.RedactBody's
// JSON branch. It parses: a type the stdlib parser rejects is not one
// recording can reason about.
func jsonEssence(contentType string) bool {
	essence, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	_, sub, _ := strings.Cut(essence, "/")
	return strings.Contains(sub, "json")
}

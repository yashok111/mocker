package mockplane

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// A spec rule is one complete response source. An active higher-layer row
// masks the whole graph, even if that row only sets a delay or a when clause.
// The caller has already applied session state and resolved a pause exactly
// once; a fallback must return to that same request without repeating either.
func (p *Plane) evaluateResponseRule(r *http.Request, rt *runtime, route *router.Route, overrideActive bool, effect livestate.Effect) (*responserules.Simulation, error) {
	program := rt.responseRules[overrides.OpKey(route.Method, route.Path)]
	if program == nil {
		return nil, nil
	}
	markResponseRule(r, program.ID(), "", false)
	if effect.Status != 0 {
		markResponseRule(r, "", "response_rule_shadowed_session", false)
		return nil, nil
	}
	if overrideActive {
		markResponseRule(r, "", "response_rule_shadowed_override", false)
		return nil, nil
	}
	body, available, truncated := responseRuleBody(r)
	input, rejected, err := responserules.LiveInput(r.Context(), r.URL.Query(), r.Header, body, available && !truncated)
	markResponseRule(r, "", "", rejected || truncated)
	if err != nil {
		return nil, err
	}
	result, err := program.Evaluate(r.Context(), input)
	if err != nil {
		markResponseRule(r, "", "response_rule_failed", false)
		return nil, err
	}
	markResponseRule(r, "", "response_rule_"+result.Outcome, false)
	return &result, nil
}

// Eligibility stays identical to the existing capture policy. cb.parsed is
// intentionally unused: its historical float64 decode would round large
// integers and collapse number spellings that the shared rule decoder preserves.
func responseRuleBody(r *http.Request) (body []byte, available, truncated bool) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodDelete:
		return nil, false, false
	}
	contentType := r.Header.Get("Content-Type")
	if contentType != "" {
		typ, sub, ok := splitMediaType(contentType)
		if !ok || typ+"/"+sub != "application/json" && typ+"/"+sub != "text/plain" {
			return nil, false, false
		}
	}
	cb := capturedBodyFromContext(r)
	if cb == nil || len(cb.bytes) == 0 {
		return nil, false, false
	}
	return cb.bytes, true, cb.truncated
}

func responseRuleDelayMs(sessionMs int, rowMs *int, settingsMs int, result *responserules.Simulation) int {
	base := effectiveDelayMs(sessionMs, rowMs, settingsMs)
	if result == nil || result.TotalDelayMs == nil || sessionMs > 0 {
		return base
	}
	// Clamp each nonnegative operand before addition so even an invalid
	// in-memory settings value cannot overflow and bypass the shared limit.
	maximum := int(maxSimulatedDelay / time.Millisecond)
	return min(maximum, min(maximum, max(0, base))+min(maximum, max(0, *result.TotalDelayMs)))
}

func (p *Plane) writeResponseRule(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, route *router.Route, response responserules.Response) {
	// Compilation already admitted the immutable payload. Keep the same
	// shared admission at the serve boundary so a future response source
	// cannot bypass managed-header or browser-executable media protections.
	if err := responserules.CheckResponse(response); err != nil || dangerousResolvedMediaType(response.MediaType) || response.Status < 200 || response.Status > 599 {
		markResponseRule(r, "", "response_rule_failed", false)
		httpx.Err(w, http.StatusInternalServerError, "response_rule_failed", "response rule contains an unsafe response")
		return
	}
	noBody := response.Status == http.StatusNoContent || response.Status == http.StatusResetContent || response.Status == http.StatusNotModified
	if !noBody && !acceptable(r.Header.Get("Accept"), response.MediaType) {
		markResponseRule(r, "", "response_rule_not_acceptable", false)
		httpx.Err(w, http.StatusNotAcceptable, "not_acceptable", fmt.Sprintf(
			"%s %s only offers %q for this response; Accept %q excludes it",
			route.Method, route.CanonicalPath, response.MediaType, r.Header.Get("Accept")))
		return
	}
	var body string
	if !noBody && response.BodyJSON != nil {
		body = *response.BodyJSON
	}
	if int64(len(body)) > p.cfg.MaxResponse {
		markResponseRule(r, "", "response_rule_too_large", false)
		httpx.Err(w, http.StatusInternalServerError, "response_rule_too_large", "response rule body exceeds MOCKER_MAX_RESPONSE")
		return
	}
	for _, header := range response.Headers {
		w.Header().Set(header.Name, header.Value)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !noBody {
		w.Header().Set("Content-Type", response.MediaType)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}
	w.WriteHeader(response.Status)
	if body != "" {
		// G705: a validated static JSON response is the purpose of this
		// branch. The media guard above prevents browser-executable output.
		if _, err := w.Write([]byte(body)); err != nil { //nolint:gosec
			p.log.Debug("write response rule", "workspace", ws.Slug, "err", err)
		}
	}
}

func markResponseRule(r *http.Request, id, outcome string, bodyRejected bool) {
	tm, ok := r.Context().Value(trafficMatchCtxKey{}).(*trafficMatch)
	if !ok {
		return
	}
	if id != "" {
		tm.responseRuleID = id
	}
	if outcome != "" {
		tm.responseRuleOutcome = outcome
	}
	tm.responseRuleBodyRejected = tm.responseRuleBodyRejected || bodyRejected
}

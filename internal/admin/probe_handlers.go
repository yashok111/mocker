// Probe handlers implement the SERVER side of DESIGN §14 screen 4's
// two-sided «Проверить» — see internal/probe's own package doc for why two
// dials at the same URL, one from mocker itself and one from the caller's
// browser (web/src/connect/probe.ts's runProbe), answer two different
// questions and both need reporting.
//
// POST rather than GET: this route has a real side effect (an outbound
// network call to an address outside mocker's own process), and DESIGN §15
// treats every such action as state-changing the same way a database write
// is — reachable only behind [Server.enforceCSRF], never by a bare
// cross-site <img>/<link> GET that needs no script and no CSRF bypass to
// fire at all.
package admin

import (
	"context"
	"net/http"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/probe"
)

// serverProbeView is POST /api/workspaces/{id}/probe's wire shape — the
// server-side twin of web/src/connect/probe.ts's ProbeResult, deliberately
// kept to the same five "kind" values so the admin UI can compare its own
// browser-side result against this one without translating between two
// different vocabularies. Only the fields that make sense for a given kind
// are populated; the rest are omitted rather than sent as zero values, the
// same discipline probe.ts's own discriminated union enforces on the
// TypeScript side.
type serverProbeView struct {
	Kind      string `json:"kind"`
	Status    int    `json:"status,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	Message   string `json:"message,omitempty"`
}

// probeHealthBody mirrors internal/mockplane/plane.go's own unexported
// `health` struct, field for field. It cannot be imported directly — that
// type is unexported, and internal/mockplane is not a package this file has
// any other reason to depend on — so its wire shape is pinned here the same
// way probe.ts's own isHealthBody guard pins an independent copy for the
// browser side. Spec is deliberately not read here: this handler compares
// only workspace and revision, exactly what probe.ts's interpretProbe does.
type probeHealthBody struct {
	OK        bool   `json:"ok"`
	Workspace string `json:"workspace"`
	Revision  int64  `json:"revision"`
}

// probeErrorEnvelope mirrors httpx.Err's own wire shape ({"error":{"code",
// "message"}}) for pulling a message out of a non-2xx body the same way
// probe.ts's isErrorEnvelope does for the browser side — the reserved-prefix
// health route answers through the identical envelope on failure.
type probeErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// handleProbeWorkspace answers POST /api/workspaces/{id}/probe: mocker
// dialling ws's own externally reachable URL (workspaceURL — see that
// method's doc comment for why it is built from the request and cfg alone,
// never from anything in this request's body) at cfg.ReservedPrefix +
// "/health", the exact same target probe.ts's runProbe dials from the
// browser for the same workspace.
//
// This handler itself always answers 200 once the workspace is found: the
// TARGET's own failure — refused connection, timeout, wrong body, a non-2xx
// status — is reported INSIDE the response body via serverProbeView.Kind,
// never as this route's own HTTP status. A caller comparing this against the
// browser's result needs both outcomes to actually arrive, not one masked
// behind a generic 5xx.
func (s *Server) handleProbeWorkspace(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	target := s.workspaceURL(r, ws) + s.cfg.ReservedPrefix + "/health"

	// probe.Timeout, not a caller-chosen value: DESIGN's own health route is
	// the only thing on the other end of this call, and letting an admin
	// request pick its own probe timeout would be a knob nothing on this
	// screen needs. r.Context() as the parent means a client that gives up
	// (navigates away, closes the tab) stops the outbound dial too, rather
	// than leaving it to run to completion for no one.
	ctx, cancel := context.WithTimeout(r.Context(), probe.Timeout)
	defer cancel()
	outcome := probe.Health(ctx, target)

	httpx.JSON(w, http.StatusOK, interpretProbeOutcome(outcome, ws.Slug))
}

// interpretProbeOutcome turns a raw probe.Outcome into the wire shape,
// branch for branch mirroring web/src/connect/probe.ts's interpretProbe.
// The two sides of DESIGN §14's check must reach the same verdict from
// equivalent bytes, or the UI's "the two disagree, here is why" step (see
// ConnectPanel.tsx) would be comparing results built from two different
// definitions of "ok".
func interpretProbeOutcome(outcome probe.Outcome, expectedSlug string) serverProbeView {
	if outcome.Kind == probe.KindTimeout {
		return serverProbeView{Kind: "timeout"}
	}
	if outcome.Kind == probe.KindNetworkError {
		return serverProbeView{Kind: "network-error"}
	}
	// outcome.Kind == probe.KindResponse — the only remaining value.

	if outcome.Status < 200 || outcome.Status >= 300 {
		return serverProbeView{Kind: "http-error", Status: outcome.Status, Message: probeErrorMessage(outcome.Body, outcome.Status)}
	}

	var body probeHealthBody
	// An unparseable body, or one missing the workspace field DESIGN's own
	// health response always carries, is treated as "not the health shape at
	// all" — the same fallback probe.ts's isHealthBody type guard produces
	// for a 2xx that answers with something else entirely.
	if err := jsonx.Unmarshal(outcome.Body, &body); err != nil || body.Workspace == "" {
		return serverProbeView{Kind: "http-error", Status: outcome.Status, Message: "сервер ответил, но не тем, что ожидалось"}
	}
	if body.Workspace != expectedSlug {
		return serverProbeView{Kind: "wrong-workspace", Workspace: body.Workspace}
	}
	return serverProbeView{Kind: "ok", Workspace: body.Workspace, Revision: body.Revision}
}

// probeErrorMessage extracts a message from a non-2xx probe body the same
// way probe.ts's isErrorEnvelope does, falling back to the status text when
// the body is not (or does not carry) an error envelope.
func probeErrorMessage(body []byte, status int) string {
	var env probeErrorEnvelope
	if err := jsonx.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	return http.StatusText(status)
}

// Package probe is the SERVER side of DESIGN §14 screen 4's two-sided
// «Проверить»: mocker dialling a workspace's own externally reachable URL
// itself, the way any other machine in the contour would reach it — as
// opposed to web/src/connect/probe.ts's browser-side fetch of the exact same
// URL. Two dials at the exact same target answer two different questions:
// the server's success proves DNS, routing and (for TLS-terminated
// deployments) the certificate chain all work; the browser's success
// ADDITIONALLY proves this particular browser trusts the contour's root
// certificate and that CORS is configured correctly. A server-yes/browser-no
// split is therefore not an ambiguous network error — it is specifically
// "install the contour's root CA in this browser", which is why the caller
// (internal/admin) needs both outcomes reported in the same vocabulary
// (see [Kind]) rather than one opaque boolean.
//
// This is the first outgoing HTTP client anywhere in this tree, which is
// exactly why every property below carries its own reasoning: nothing else
// in the codebase to imitate, and nothing here is incidental.
package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// Timeout bounds one probe's entire round trip — dial, TLS handshake,
// response headers and this package's own capped body read all happen
// inside ONE deadline. [Health]'s caller (internal/admin) derives its
// request context from this constant rather than the package hard-coding
// http.Client.Timeout, so a test can supply its own short deadline against a
// deliberately slow httptest.Server without waiting out three real seconds
// (see probe_test.go's timeout case). Three seconds is generous slack for a
// healthy target — DESIGN's own health route does no I/O of its own — while
// still keeping a hung dial from holding the admin request (and the
// goroutine serving it) open for anything close to indefinitely.
const Timeout = 3 * time.Second

// maxBodyBytes caps how much of a probe response this package ever reads.
// The caller needs only the status and, on a 2xx, the small JSON health body
// DESIGN specifies (well under a hundred bytes) — never an unbounded amount
// of it, and never for any other reason: this package's whole contract is
// "read the status and the error, discard the body" (see the request digest
// this slice was built from). A misconfigured or hostile target that
// answers a health probe with a large or endless stream must not turn a
// diagnostic click into a memory or bandwidth sink.
const maxBodyBytes = 4096

// Kind is what dialling a target produced, before any interpretation of the
// body — mirrors web/src/connect/probe.ts's ProbeOutcome one-to-one, so the
// admin API and the browser's own runProbe describe an outcome in the same
// three words on both sides of DESIGN §14's check.
type Kind string

const (
	// KindResponse means a response — of ANY status, including a redirect
	// this package refused to follow (see newClient's CheckRedirect) —
	// actually came back. Interpreting Status/Body into "ok" vs "http-error"
	// is the caller's job (internal/admin), exactly as interpretProbe does
	// on the TypeScript side for the browser's own fetch.
	KindResponse Kind = "response"
	// KindNetworkError covers everything that is not a clean response and
	// not a timeout: DNS failure, connection refused, TLS trust failure, a
	// dropped connection mid-request. Go's client does not distinguish these
	// from each other any more usefully than a browser's fetch does, so —
	// deliberately, matching probe.ts's own network-error branch — neither
	// does this package.
	KindNetworkError Kind = "network-error"
	// KindTimeout means the caller's context deadline (built from Timeout)
	// fired before a response arrived.
	KindTimeout Kind = "timeout"
)

// Outcome is what dialling target produced — the Go-side twin of
// web/src/connect/probe.ts's ProbeOutcome.
type Outcome struct {
	Kind Kind
	// Status and Body are only meaningful when Kind == KindResponse.
	// Body is capped at maxBodyBytes and best-effort — a read error partway
	// through still yields whatever bytes were read rather than discarding
	// them, since a truncated health body is still informative to the
	// caller's own "does this parse as health JSON" check.
	Status int
	Body   []byte
}

// newClient builds an http.Client scoped to exactly ONE probe call — never
// stored, never reused across requests. Every property answers one of this
// slice's explicit requirements:
//
//   - CheckRedirect refuses to follow, answering with http.ErrUseLastResponse
//     so client.Do returns the redirect's own response (status and body)
//     rather than turning it into a Go error. A redirect is the ONE
//     mechanism by which this package's fixed, server-built target (see
//     internal/admin's handleProbeWorkspace, which builds it from the
//     workspace's own URL plus the configured reserved prefix — never from
//     request input) could be turned into an arbitrary one this package
//     never agreed to reach. Not following it, and reporting its status as
//     an ordinary non-2xx response, keeps the fixed target fixed.
//   - Transport.DisableKeepAlives is true: a probe answers "can THIS request
//     reach the target right now", and a result shaped by whatever
//     connection happened to be idling in a shared pool — possibly opened
//     before a DNS change, a cert rotation, or a network partition healed —
//     would not answer that question honestly. Every probe dials fresh and
//     the connection is torn down the moment the response is read, matching
//     what a hand-run `curl` (no keep-alive across separate invocations)
//     would do.
//
// Timeout is set by the caller through ctx (see [Health]), not here: a
// http.Client.Timeout field would fire from the moment the client is
// constructed, indistinguishable in this package from a ctx the caller
// already bounded — one deadline, owned by the caller, is simpler than two
// that could disagree.
func newClient(tlsConf *tls.Config) *http.Client {
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig:   tlsConf,
		},
	}
}

// Health dials target — expected to be a workspace's own
// {url}{reservedPrefix}/health, built entirely server-side by the one caller
// this package has (see [newClient]'s CheckRedirect comment for why that
// matters) — and reports what happened. ctx's deadline is what bounds the
// call; the caller is expected to derive it from [Timeout] (internal/admin
// does exactly that), and a test supplies its own short deadline directly so
// [KindTimeout] is reachable in well under a second (see probe_test.go).
func Health(ctx context.Context, target string) Outcome {
	return dial(ctx, target, "", nil)
}

// Readiness dials target with an explicit Host header — the SECOND caller
// this package has, and the reason the client lives in one package at all:
// CLAUDE.md promised that when another outgoing HTTP client appeared it
// would land here, next to the timeout, the redirect refusal and the body
// cap, rather than as a fresh http.Client somewhere else. The caller is
// `mocker healthcheck` (cmd/mocker), the container's own healthcheck: the
// image is distroless, there is no curl or shell to run one, so the binary
// dials its own listener — target is http://<loopback>:<MOCKER_ADDR port>/readyz
// and host is MOCKER_ADMIN_HOST, because the dispatcher decides by Host and
// a loopback address alone is nobody's workspace and not the admin plane
// either. Same client, same properties (one deadline owned by the caller, no
// redirect, no keep-alive, capped body): a healthcheck that followed a
// redirect or reused a stale connection would answer a different question
// than "does this process answer right now".
func Readiness(ctx context.Context, target, host string) Outcome {
	return dial(ctx, target, host, nil)
}

// ReadinessTLS is Readiness over https with ONE trusted root — the THIRD
// caller, `mocker setup` (cmd/mocker), which waits for the stack it just
// started behind Caddy's local CA. rootPEM is that CA's root.crt as the
// wizard exported it from the caddy container; the TLS ServerName is host,
// because the wizard dials the loopback address while the certificate is
// issued for the admin host name. Same client otherwise: no redirect, no
// keep-alive, capped body, deadline owned by the caller. A rootPEM that
// does not parse answers KindNetworkError rather than falling back to the
// system pool — the wizard must not report "ready" on a certificate the
// browser will not trust either.
func ReadinessTLS(ctx context.Context, target, host string, rootPEM []byte) Outcome {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(rootPEM) {
		return Outcome{Kind: KindNetworkError}
	}
	return dial(ctx, target, host, &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12})
}

// dial is the one request path both entry points share. host, when
// non-empty, overrides the Host header the URL would otherwise imply.
func dial(ctx context.Context, target, host string, tlsConf *tls.Config) Outcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		// Only reachable if target itself is malformed. This package's one
		// caller builds it from a workspace's already-validated URL plus a
		// config value validated at startup (internal/config), so this is
		// defensive, not an expected path — but a malformed URL is still a
		// caller-side problem, not a network one, so it is reported the same
		// way any other "could not reach it" case is.
		return Outcome{Kind: KindNetworkError}
	}
	if host != "" {
		req.Host = host
	}

	resp, err := newClient(tlsConf).Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Outcome{Kind: KindTimeout}
		}
		// net.Error.Timeout() catches the case http.Client's own internal
		// machinery reports a deadline as something other than a bare
		// context.DeadlineExceeded (observed for a dial that times out
		// before the request context itself is checked again) — belt and
		// braces so a slow target is never misreported as an ordinary
		// network error.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return Outcome{Kind: KindTimeout}
		}
		return Outcome{Kind: KindNetworkError}
	}
	defer func() { _ = resp.Body.Close() }()

	// Never read more than maxBodyBytes, and never propagate a read error as
	// a failed probe: the caller's only use for Body is "does this parse as
	// the health shape", and a body that fails to fully read but still
	// starts with valid JSON is still worth attempting to parse.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	return Outcome{Kind: KindResponse, Status: resp.StatusCode, Body: body}
}

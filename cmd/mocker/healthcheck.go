package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/probe"
)

// runHealthcheck implements the "healthcheck" subcommand: the container's
// own health probe, run by docker compose (`test: ["CMD", "/mocker",
// "healthcheck"]`) inside the SAME environment the server started from.
//
// It exists because the runtime image is distroless/static: no shell, no
// curl, no wget, so docker-compose.yml could declare no healthcheck at all
// and every orchestrator saw the container as merely "running". Reading the
// configuration through config.Load — the same call the server made — is
// what makes the probe address correct by construction: MOCKER_ADDR says
// where the listener is and MOCKER_ADMIN_HOST is the Host header without
// which the dispatcher routes a loopback dial to no plane at all.
//
// It dials /readyz, not /healthz: readiness is the one that pings the
// database, and "healthy" to compose means "the sidecar in front of this
// container may start sending traffic" (docker-compose.tls.yml's Caddy
// waits on service_healthy) — a process that answers liveness while its
// database is not open yet is precisely the state that answer must not be
// given in. The client is internal/probe's, the tree's only outgoing HTTP
// client, deliberately: see probe.Readiness for why a second one was not
// written here.
//
// Exit status is the whole interface — 0 on a 200 with the health body's
// `ok: true`, 1 on anything else, with one line on stderr saying which —
// because that is all a HEALTHCHECK can read.
func runHealthcheck(cfg *config.Config, stderr io.Writer) error {
	target, err := healthcheckTarget(cfg.Addr)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), probe.Timeout)
	defer cancel()

	out := probe.Readiness(ctx, target, cfg.AdminHost)
	switch out.Kind {
	case probe.KindTimeout:
		return fmt.Errorf("%s: no answer within %s", target, probe.Timeout)
	case probe.KindNetworkError:
		return fmt.Errorf("%s: not reachable", target)
	case probe.KindResponse:
	default:
		return fmt.Errorf("%s: unexpected probe outcome %q", target, out.Kind)
	}
	if out.Status != http.StatusOK {
		return fmt.Errorf("%s: status %d: %s", target, out.Status, string(out.Body))
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := jsonx.Unmarshal(out.Body, &body); err != nil || !body.OK {
		return fmt.Errorf("%s: 200 but the body is not a health document: %s", target, string(out.Body))
	}
	_, _ = fmt.Fprintf(stderr, "ok: %s answers ready\n", target)
	return nil
}

// healthcheckTarget turns MOCKER_ADDR into the loopback URL of /readyz.
// ":8080" and "0.0.0.0:8080" both listen on every interface, and a probe
// from inside the same network namespace reaches them on 127.0.0.1; a
// concrete address ("10.0.0.5:8080") is dialled as given, because that is
// the only interface the server bound. An unspecified IPv6 wildcard ("[::]")
// is folded to IPv4 loopback too — Go's dual-stack listener on "::" accepts
// 127.0.0.1, and a distroless image has no guarantee of ::1 being up.
func healthcheckTarget(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("MOCKER_ADDR %q: %w", addr, err)
	}
	if port == "" {
		return "", errors.New("MOCKER_ADDR has no port")
	}
	// A bare decimal, the same rule httpx.RequestPort applies to a request's
	// Host: net.SplitHostPort splits on the LAST colon and hands back
	// whatever followed with a nil error, so ":8080@evil.example" would
	// otherwise be spliced into a URL whose authority is evil.example. The
	// value is environment-controlled, so this is the tree's one hard rule
	// about URL parts, not a privilege boundary.
	for _, c := range port {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("MOCKER_ADDR port %q is not a bare decimal", port)
		}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/readyz", nil
}

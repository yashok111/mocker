package httpx

import (
	"net/http"

	"github.com/yashok111/mocker/internal/config"
)

// WorkspacePathPrefix is the path-mode (MOCKER_ROUTING=path, DESIGN §16)
// stand-in for a workspace subdomain: /w/{slug}/... on the single shared
// origin. It lived as two identical unexported consts (internal/server,
// internal/admin) until A6 needed a third reader; both now point here.
const WorkspacePathPrefix = "/w/"

// WorkspaceURL builds a workspace's externally reachable base URL, with no
// trailing slash, from the request that is being served:
//
//	host mode: <scheme>://<slug>.<cfg.BaseDomain>[:port]
//	path mode: <scheme>://<cfg.AdminHost>[:port]/w/<slug>
//
// It is the ONE construction of that string (A6, mocker-a6-assets D7): the
// admin API's workspace record, the asset_url recipe on the mock plane and
// the preview route all call it, so they cannot disagree — the alternative
// was two copies and a test that they agree, the shape mediatype.go was
// written to remove. TWO PIECES COME FROM THE REQUEST and internal/admin's
// probe route DIALS the result, so each is whitelisted rather than merely
// read, and neither guard may be relaxed without re-reading this paragraph:
//
//   - the scheme through [ForwardedProto] — only "https" from a trusted
//     proxy or a TLS connection, else "http", because a scheme spliced into
//     a URL string can smuggle an authority of its own;
//   - the port through [RequestPort] — a bare decimal or nothing, because
//     net.SplitHostPort splits on the LAST colon and hands back whatever
//     followed with a nil error: `Host: mocker.local:9@evil.example` gave
//     the port "9@evil.example" and the assembled URL parsed with
//     evil.example as its HOST (a read-SSRF pivot through the probe route,
//     pinned by TestP1c2WorkspaceView_urlRefusesAnInjectedPort).
//
// The host is NEVER r.Host: cfg.BaseDomain/cfg.AdminHost plus the slug are
// what a workspace is addressed by, and r.Host is only consulted for its
// port.
func WorkspaceURL(r *http.Request, cfg *config.Config, slug string) string {
	scheme := ForwardedProto(r, cfg.TrustProxy)
	port := RequestPort(r) // "" when r.Host carries no port, or one that is not a bare number

	if cfg.Routing == config.RoutingPath {
		host := cfg.AdminHost
		if port != "" {
			host += ":" + port
		}
		return scheme + "://" + host + WorkspacePathPrefix + slug
	}

	host := slug + "." + cfg.BaseDomain
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}

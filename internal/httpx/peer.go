package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/yashok111/mocker/internal/config"
)

// Peer describes where a request came from.
//
// Both fields are recorded in traffic (DESIGN §13): the immediate peer always,
// the forwarded address only when the deployment said which proxy to believe.
// With an open mock plane the source is the only thing standing between "a
// colleague's e2e run wrote into your workspace" and a ghost.
type Peer struct {
	// Addr is the immediate TCP peer. Always populated when parsable.
	Addr netip.Addr
	// Forwarded is the client address taken from X-Forwarded-For, valid only
	// when Trusted is true.
	Forwarded netip.Addr
	// Trusted reports whether the forwarding headers were believed.
	Trusted bool
}

// String renders the address recorded as peer_ip.
func (p Peer) String() string {
	if p.Addr.IsValid() {
		return p.Addr.String()
	}
	return ""
}

// Client returns the address to attribute the request to: the forwarded one
// when trusted, the peer otherwise.
func (p Peer) Client() netip.Addr {
	if p.Trusted && p.Forwarded.IsValid() {
		return p.Forwarded
	}
	return p.Addr
}

// ResolvePeer works out who is calling, honouring the trust policy.
//
// Untrusted X-Forwarded-For is dropped, not merely deprioritised: recording an
// attacker-controlled address next to a real one invites reading the wrong one
// later.
func ResolvePeer(r *http.Request, trust config.TrustProxy) Peer {
	p := Peer{Addr: remoteAddr(r)}
	if !trust.Enabled || !p.Addr.IsValid() || !trust.TrustsPeer(p.Addr) {
		return p
	}

	chain := forwardedChain(r)
	if len(chain) == 0 {
		return p
	}
	// Hop-count mode counts from the right: the last entry was appended by the
	// nearest proxy, so N hops back is the address that proxy saw.
	idx := len(chain) - 1
	if trust.Hops > 0 {
		idx = len(chain) - trust.Hops
	}
	if idx < 0 {
		idx = 0
	}
	if addr, err := netip.ParseAddr(chain[idx]); err == nil {
		p.Forwarded, p.Trusted = addr.Unmap(), true
	}
	return p
}

func remoteAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func forwardedChain(r *http.Request) []string {
	var out []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		for part := range strings.SplitSeq(header, ",") {
			if v := strings.TrimSpace(part); v != "" {
				out = append(out, strings.Trim(v, "[]"))
			}
		}
	}
	return out
}

// ForwardedProto reports the scheme the client used, honouring the trust
// policy. It decides whether the session cookie may carry Secure: behind a
// TLS-terminating proxy the request arrives as plain http, and reading the
// header from an untrusted peer would let a client turn Secure off.
//
// The return value is whitelisted to exactly "http" or "https" — never the
// raw header. [Server.workspaceURL] (internal/admin/workspace_handlers.go)
// splices this string straight into "<scheme>://<host>..." with no further
// parsing, so any other value becomes the URL's scheme verbatim. A caller
// sending "X-Forwarded-Proto: http://evil.example/" used to come back out
// unchanged (only lower-cased, trimmed, cut at the first comma) and turned
// the probe dial (POST /api/workspaces/{id}/probe, internal/admin/
// probe_handlers.go) into an SSRF primitive: url.Parse("http://evil.example/
// ://alex.mock.corp/__mocker/health") resolves Host to evil.example, not the
// workspace — url.URL{Scheme:...}.String() would not have helped either,
// since the injected scheme smuggles its own authority through either
// construction. Anything other than an exact "https" match is a proxy
// misconfiguration (or an attacker) and is folded to "http", the same
// fallback an absent header already got.
func ForwardedProto(r *http.Request, trust config.TrustProxy) string {
	if r.TLS != nil {
		return "https"
	}
	peer := remoteAddr(r)
	if trust.Enabled && peer.IsValid() && trust.TrustsPeer(peer) {
		proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
		proto, _, _ = strings.Cut(proto, ",")
		if strings.TrimSpace(proto) == "https" {
			return "https"
		}
	}
	return "http"
}

// RequestPort returns the port from r.Host, or "" when there is none —
// validated as a bare decimal number, which is the whole point of it existing
// rather than callers reaching for net.SplitHostPort themselves.
//
// net.SplitHostPort does NOT validate the port: it splits on the last colon
// and hands back whatever followed, with a nil error. For a value a caller
// then splices back into a URL string that is an authority-injection hole, and
// it was a live one. `Host: admin.corp:9@evil.example` split cleanly into
// host "admin.corp" and port "9@evil.example"; config.normalizeHost strips
// everything after the last colon too, so the dispatcher still recognised the
// admin host and let the request through; and the rebuilt string
// "http://alex.mock.corp:9@evil.example" parses with "alex.mock.corp:9" as
// USERINFO and evil.example as the host. The probe route then dialled it and
// reflected the answer, which is a read-SSRF pivot out of this box.
//
// This is the same class as [ForwardedProto] one field over — a request header
// spliced into a URL — and it lives beside it so the pair is findable. A port
// is at most five digits; anything else is a proxy misconfiguration or an
// attacker, and both get "" and the scheme's default port.
func RequestPort(r *http.Request) string {
	_, port, err := net.SplitHostPort(r.Host)
	if err != nil || port == "" || len(port) > 5 {
		return ""
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return port
}

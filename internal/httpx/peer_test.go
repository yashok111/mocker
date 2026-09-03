package httpx_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/httpx"
)

// trustedFrom builds a TrustProxy that believes forwarding headers from
// exactly the given peer address — the minimal config an admin deployment
// documents in .env.example for MOCKER_TRUST_PROXY.
func trustedFrom(t *testing.T, peer string) config.TrustProxy {
	t.Helper()
	addr := netip.MustParseAddr(peer)
	return config.TrustProxy{
		Enabled: true,
		CIDRs:   []netip.Prefix{netip.PrefixFrom(addr, addr.BitLen())},
	}
}

// TestForwardedProto_whitelistsScheme is the regression test for the probe
// SSRF: [internal/admin.Server.workspaceURL] splices this return value
// straight into "<scheme>://<host>..." with no further validation, so
// ForwardedProto is the only gate standing between an attacker-controlled
// X-Forwarded-Proto and an attacker-chosen dial target. Every case here
// must come back as exactly "http" or "https" — never the raw header —
// or the probe (POST /api/workspaces/{id}/probe) can be redirected to dial
// an arbitrary host via the classic "smuggle an authority inside the
// scheme" trick: url.Parse("http://evil.example/://alex.mock.corp/health")
// resolves Host to evil.example, not the workspace.
func TestForwardedProto_whitelistsScheme(t *testing.T) {
	t.Parallel()

	trust := trustedFrom(t, "203.0.113.7") // TEST-NET-3, stands in for the reverse proxy

	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"exact https passes through", "https", "https"},
		{"exact http stays http", "http", "http"},
		{"case-insensitive https", "HTTPS", "https"},
		{"whitespace around https", "  https  ", "https"},
		{"first of a comma list, https", "https, http", "https"},
		{"first of a comma list, http", "http, https", "http"},
		{
			name:   "scheme-smuggled authority is not passed through",
			header: "http://evil.example/",
			want:   "http",
		},
		{
			name:   "https-prefixed smuggled authority is not passed through",
			header: "https://evil.example/",
			want:   "http", // not an exact "https" match, so it folds to the safe default
		},
		{"unknown scheme folds to http", "ftp", "http"},
		{"empty header folds to http", "", "http"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = "203.0.113.7:12345"
			if tc.header != "" {
				r.Header.Set("X-Forwarded-Proto", tc.header)
			}
			if got := httpx.ForwardedProto(r, trust); got != tc.want {
				t.Errorf("ForwardedProto(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// TestForwardedProto_untrustedPeerIgnoresHeader confirms the header is not
// even consulted from a peer the trust policy does not name — the pre-fix
// behaviour already had this right, and the scheme whitelist above must not
// regress it.
func TestForwardedProto_untrustedPeerIgnoresHeader(t *testing.T) {
	t.Parallel()

	trust := trustedFrom(t, "203.0.113.7")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.1:12345" // TEST-NET-2, not in trust.CIDRs
	r.Header.Set("X-Forwarded-Proto", "https")

	if got := httpx.ForwardedProto(r, trust); got != "http" {
		t.Errorf("ForwardedProto from untrusted peer = %q, want %q", got, "http")
	}
}

// TestForwardedProto_tlsShortCircuitsHeader confirms a directly-terminated
// TLS connection reports https regardless of any (untrusted) header, same
// as before this fix.
func TestForwardedProto_tlsShortCircuitsHeader(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("X-Forwarded-Proto", "http")

	if got := httpx.ForwardedProto(r, config.TrustProxy{}); got != "https" {
		t.Errorf("ForwardedProto with r.TLS set = %q, want %q", got, "https")
	}
}

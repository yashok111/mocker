package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/domain"
)

// ProxyOptions is installation policy plus one request's bounded execution budget.
// An exact allowlisted origin explicitly permits private corporate addresses.
type ProxyOptions struct {
	Allowlist      []string
	CAPEM          []byte
	Timeout        time.Duration
	MaxResponse    int64
	ForwardAuth    bool
	ForwardCookies bool
	CookiePath     string
}
type ProxyResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

var ErrProxyResponseLimit = errors.New("upstream response exceeds the configured limit")

func ValidateProxyURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(u.Host, "%*") {
		return nil, errors.New("upstream must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid upstream port")
		}
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && forbiddenProxyIP(ip) {
		return nil, errors.New("upstream address is prohibited")
	}
	return u, nil
}
func proxyOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
func forbiddenProxyIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// ValidateProxyTarget checks the configured destination, never an incoming Host.
func ValidateProxyTarget(raw string, allowlist []string) (*url.URL, error) {
	u, err := ValidateProxyURL(raw)
	if err != nil {
		return nil, err
	}
	for _, entry := range allowlist {
		a, err := ValidateProxyURL(entry)
		if err == nil && (a.Path == "" || a.Path == "/") && proxyOrigin(a) == proxyOrigin(u) {
			return u, nil
		}
	}
	return nil, errors.New("upstream origin is not in MOCKER_PROXY_ALLOWLIST")
}
func ValidateProxyAllowlist(entries []string) error {
	for _, entry := range entries {
		u, err := ValidateProxyURL(entry)
		if err != nil {
			return err
		}
		if u.Path != "" && u.Path != "/" {
			return errors.New("proxy allowlist entries must be origins without paths")
		}
	}
	return nil
}

func stripProxyHeaders(h http.Header) {
	for _, value := range h.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			h.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(name)
	}
	for name := range h {
		n := strings.ToLower(name)
		if strings.HasPrefix(n, "x-forwarded-") || strings.HasPrefix(n, "x-mocker-") || n == "forwarded" {
			h.Del(name)
		}
	}
}

// ErrProxyPathDotSegment refuses an incoming path with a "." or ".." segment
// before anything is sent; the caller answers it as a client error.
var ErrProxyPathDotSegment = errors.New("proxy path contains a dot segment")

// hasDotSegment reports a "." or ".." segment in an escaped path, judged
// after percent-decoding each segment (%2e%2e is ".." to every server that
// normalises) and after splitting on a backslash too, which some upstream
// servers treat as a separator (..%5Cadmin). Dots inside a name (a..b,
// .hidden) are ordinary characters and pass. An undecodable segment is
// refused as well: what the upstream would make of it cannot be predicted.
func hasDotSegment(escapedPath string) bool {
	for segment := range strings.SplitSeq(escapedPath, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return true
		}
		for part := range strings.SplitSeq(decoded, `\`) {
			if part == "." || part == ".." {
				return true
			}
		}
	}
	return false
}

// ProxyExchange reads a bounded complete response before returning. Redirects
// and automatic retries are disabled, including retries of GET requests.
func ProxyExchange(ctx context.Context, target string, incoming *http.Request, body []byte, opts ProxyOptions) (ProxyResponse, error) {
	u, err := ValidateProxyTarget(target, opts.Allowlist)
	if err != nil {
		return ProxyResponse{}, err
	}
	if opts.MaxResponse <= 0 || opts.Timeout <= 0 {
		return ProxyResponse{}, errors.New("invalid proxy limits")
	}
	if incoming.Method == http.MethodConnect || incoming.Header.Get("Upgrade") != "" {
		return ProxyResponse{}, errors.New("HTTP upgrade is not supported")
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	// Concatenation preserves encoded slashes; ResolveReference would allow an
	// incoming authority or absolute path to replace the configured destination.
	// The same intent needs the dot-segment refusal (review 2026-10-06, F179):
	// Go's client sends /v1/team-a/../../admin as is, and the upstream
	// normalises it to /admin, outside the configured prefix.
	if hasDotSegment(incoming.URL.EscapedPath()) {
		return ProxyResponse{}, ErrProxyPathDotSegment
	}
	escaped := strings.TrimRight(u.EscapedPath(), "/") + "/" + strings.TrimPrefix(incoming.URL.EscapedPath(), "/")
	u.Path, err = url.PathUnescape(escaped)
	if err != nil {
		return ProxyResponse{}, err
	}
	u.RawPath = escaped
	u.RawQuery = incoming.URL.RawQuery
	req, err := http.NewRequestWithContext(ctx, incoming.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		return ProxyResponse{}, err
	}
	req.GetBody = nil
	req.Header = ProxyRequestHeaders(incoming.Header, opts.ForwardAuth, opts.ForwardCookies)
	tr, err := newProxyTransport(u.Hostname(), opts)
	if err != nil {
		return ProxyResponse{}, err
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req) // #nosec G704 -- exact origin allowlist and checked IP dialer above; no redirects or environment proxy.
	if err != nil {
		return ProxyResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusSwitchingProtocols {
		return ProxyResponse{}, errors.New("upstream protocol upgrade is not supported")
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return ProxyResponse{}, errors.New("streaming upstream responses are not supported")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, opts.MaxResponse+1))
	if err != nil {
		return ProxyResponse{}, err
	}
	if int64(len(data)) > opts.MaxResponse {
		return ProxyResponse{}, ErrProxyResponseLimit
	}
	headers := proxyResponseHeaders(resp, incoming.Method, opts)
	return ProxyResponse{Status: resp.StatusCode, Header: headers, Body: data}, nil
}

// ValidateProxyCA checks certificates once when loading the installation policy.
func ValidateProxyCA(pem []byte) error {
	if len(pem) > 0 && !x509.NewCertPool().AppendCertsFromPEM(pem) {
		return fmt.Errorf("MOCKER_PROXY_CA_FILE contains no certificates")
	}
	return nil
}

// ProxyRequestHeaders removes transport metadata and mocker credentials. It
// is also used by replay identity so an admin login does not change a key.
func ProxyRequestHeaders(in http.Header, forwardAuth, forwardCookies bool) http.Header {
	h := in.Clone()
	stripProxyHeaders(h)
	for _, name := range []string{"Origin", "Referer", "Accept-Encoding", "Content-Length", "Expect", "X-CSRF-Token"} {
		h.Del(name)
	}
	if !forwardAuth {
		h.Del("Authorization")
		h.Del("X-Api-Key")
	}
	h.Del("Cookie")
	if forwardCookies {
		cookies := (&http.Request{Header: in}).Cookies()
		req := &http.Request{Header: h}
		for _, c := range cookies {
			if c.Name != domain.AdminSessionCookieName {
				req.AddCookie(c)
			}
		}
	}
	return h
}

func newProxyTransport(host string, opts ProxyOptions) (*http.Transport, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}
	if len(opts.CAPEM) > 0 && !roots.AppendCertsFromPEM(opts.CAPEM) {
		return nil, errors.New("invalid proxy CA certificate")
	}
	literal, literalErr := netip.ParseAddr(host)
	tr := &http.Transport{
		TLSClientConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DisableCompression:    true,
		ResponseHeaderTimeout: opts.Timeout, TLSHandshakeTimeout: opts.Timeout,
		MaxResponseHeaderBytes: 64 << 10,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if forbiddenProxyIP(ip) || (ip.IsLoopback() && (literalErr != nil || !literal.Unmap().IsLoopback())) {
					return nil, errors.New("DNS resolved to a prohibited address")
				}
			}
			var last error
			for _, ip := range ips {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			if last == nil {
				last = errors.New("upstream has no addresses")
			}
			return nil, last
		},
	}
	return tr, nil
}

func proxyResponseHeaders(resp *http.Response, method string, opts ProxyOptions) http.Header {
	cookies := resp.Cookies()
	headers := resp.Header.Clone()
	stripProxyHeaders(headers)
	headers.Del("Set-Cookie")
	if method != http.MethodHead {
		headers.Del("Content-Length")
	}
	// Mocker owns CORS, not the upstream. Do not let a response replace it.
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			headers.Del(name)
		}
	}
	if opts.ForwardCookies {
		for _, c := range cookies {
			if c.Name == domain.AdminSessionCookieName {
				continue
			}
			c.Domain = ""
			c.Path = opts.CookiePath
			if c.Path == "" {
				c.Path = "/"
			}
			headers.Add("Set-Cookie", c.String())
		}
	}
	return headers
}

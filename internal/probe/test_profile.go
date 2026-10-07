package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

// TestTarget is an operator-owned connection definition, never public input.
// Config aliases this type to preserve its existing dependency on probe.
// It stores only a credential reference; the resolved secret lives in the client.
type TestTarget struct {
	ID            string   `json:"id"`
	Version       int64    `json:"version"`
	Origin        string   `json:"origin"`
	AllowedIPs    []string `json:"allowedIPs"`
	CredentialRef string   `json:"credentialRef"`
	IsolationID   string   `json:"isolationId"`
}

// Validate checks the definition in a fixed order — identity, credential
// reference, origin, allowlist, then the origin against the allowlist — and
// the first failure wins; the helpers below are those steps, in that order.
func (t TestTarget) Validate() error {
	if t.ID == "" || strings.TrimSpace(t.ID) != t.ID || t.Version < 1 || !p.ValidID(t.IsolationID) {
		return errors.New("invalid target identity/version")
	}
	if err := validateCredentialRef(t.CredentialRef); err != nil {
		return err
	}
	u, err := parseTargetOrigin(t.Origin)
	if err != nil {
		return err
	}
	seen, err := allowlistSet(t.AllowedIPs)
	if err != nil {
		return err
	}
	ip, e := netip.ParseAddr(u.Hostname())
	if e == nil && (ip.Zone() != "" || !seen[ip.Unmap()]) {
		return errors.New("origin IP is not allowed")
	}
	if u.Scheme == "http" && (e != nil || !ip.IsLoopback()) {
		return errors.New("HTTP requires a loopback literal origin")
	}
	return nil
}

// validateCredentialRef accepts an environment-variable-shaped name: the
// reference is resolved by name at startup, so anything else is a typo.
func validateCredentialRef(ref string) error {
	if ref == "" {
		return errors.New("credential reference required")
	}
	for i, c := range ref {
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (i == 0 || c < '0' || c > '9') {
			return errors.New("invalid credential reference")
		}
	}
	return nil
}

// parseTargetOrigin admits a bare scheme://host[:port] and nothing more: any
// path, query, fragment or userinfo would be silently dropped or reinterpreted
// once a request path is appended to the origin.
func parseTargetOrigin(origin string) (*url.URL, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("target must be an exact HTTP(S) origin")
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("invalid target port")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid target port")
		}
	}
	return u, nil
}

// allowlistSet returns the allowlist as a set of unmapped addresses, refusing
// an empty list, a zoned or public address, and a duplicate.
func allowlistSet(allowed []string) (map[netip.Addr]bool, error) {
	if len(allowed) == 0 {
		return nil, errors.New("target requires explicit IP allowlist")
	}
	seen := map[netip.Addr]bool{}
	for _, raw := range allowed {
		ip, e := netip.ParseAddr(raw)
		if e != nil || ip.Zone() != "" || (!ip.IsLoopback() && !ip.IsPrivate()) || seen[ip.Unmap()] {
			return nil, errors.New("allowlist requires unique private or loopback IP literals")
		}
		seen[ip.Unmap()] = true
	}
	return seen, nil
}

type testProfileClient struct {
	target TestTarget
	secret string
	client *http.Client
}

var _ p.Transport = (*testProfileClient)(nil)

// ConfigHash is an opaque startup-only pin. Store it privately with the registry
// version to detect silent rebinding or credential rotation; never expose it in
// profiles, public responses or reports. It includes the credential digest.
func (c *testProfileClient) ConfigHash() string {
	hash, _ := p.Hash("orders-target-config-v1", struct {
		Target           TestTarget `json:"target"`
		CredentialDigest string     `json:"credentialDigest"`
	}{c.target, p.HashBytes([]byte(c.secret))})
	return hash
}

// NewTestProfileClient resolves credentials and DNS exactly once at startup.
// The pinned address is used for all connections; TLS still verifies the origin host.
func NewTestProfileClient(ctx context.Context, target TestTarget, resolve func(string) string) (p.Transport, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if resolve == nil {
		return nil, errors.New("credential resolver required")
	}
	secret := resolve(target.CredentialRef)
	if len(secret) < 32 || strings.ContainsAny(secret, "\r\n\t ") {
		return nil, errors.New("target credential must be at least 32 bytes without whitespace")
	}
	u, _ := url.Parse(target.Origin)
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("target DNS resolution failed")
	}
	allowed := make([]netip.Addr, 0, len(target.AllowedIPs))
	for _, raw := range target.AllowedIPs {
		ip, _ := netip.ParseAddr(raw)
		allowed = append(allowed, ip.Unmap())
	}
	for _, ip := range addresses {
		if !slices.Contains(allowed, ip.Unmap()) {
			return nil, errors.New("resolved target IP is not allowed")
		}
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	pinned := net.JoinHostPort(addresses[0].Unmap().String(), port)
	dialer := &net.Dialer{Timeout: Timeout}
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: Timeout, ResponseHeaderTimeout: Timeout, MaxResponseHeaderBytes: p.BodyLimit,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, pinned)
		},
	}
	target.AllowedIPs = slices.Clone(target.AllowedIPs)
	return &testProfileClient{target: target, secret: secret, client: &http.Client{
		Transport: transport, Timeout: Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *testProfileClient) Identity(ctx context.Context) (p.Response[p.IdentityResponse], error) {
	r, err := c.request[p.IdentityResponse](ctx, p.IdentityEndpoint, "", nil, p.BodyLimit)
	if err == nil && r.Payload != nil && r.Payload.Identity.IsolationID != c.target.IsolationID {
		r.Payload = nil
		return r, errors.New("target isolation mismatch")
	}
	return r, err
}
func (c *testProfileClient) Reset(ctx context.Context, v p.ResetRequest) (p.Response[p.Receipt], error) {
	if v.Authorization.TargetID != c.target.ID || v.Authorization.ConfigVersion != c.target.Version || v.Authorization.IsolationID != c.target.IsolationID {
		return p.Response[p.Receipt]{}, errors.New("reset authorization target mismatch")
	}
	return c.mutate(ctx, p.ResetEndpoint, v.Fence, v)
}
func (c *testProfileClient) Arm(ctx context.Context, v p.FailureRequest) (p.Response[p.Receipt], error) {
	return c.mutate(ctx, p.FailureEndpoint, v.Fence, v)
}
func (c *testProfileClient) Order(ctx context.Context, v p.OrderRequest) (p.Response[p.Receipt], error) {
	return c.mutate(ctx, p.OrderEndpoint, v.Fence, v)
}
func (c *testProfileClient) Journal(ctx context.Context, id string) (p.Response[p.Journal], error) {
	r, err := c.request[p.Journal](ctx, p.JournalEndpoint, id, nil, p.JournalLimit)
	if err == nil && r.Payload != nil && (r.Payload.RunID != id || r.Payload.Identity.IsolationID != c.target.IsolationID) {
		r.Payload = nil
		return r, errors.New("journal target/run mismatch")
	}
	return r, err
}
func (c *testProfileClient) mutate(ctx context.Context, endpoint p.Endpoint, fence p.Fence, v any) (p.Response[p.Receipt], error) {
	hash, err := p.RequestHash(endpoint, v)
	if err != nil {
		return p.Response[p.Receipt]{}, err
	}
	raw, err := p.Encode(v)
	if err != nil || len(raw) > p.BodyLimit {
		return p.Response[p.Receipt]{}, errors.New("invalid request encoding/size")
	}
	r, err := c.request[p.Receipt](ctx, endpoint, fence.RunID, raw, p.BodyLimit)
	if err == nil && r.Payload != nil {
		receipt := r.Payload
		if receipt.Endpoint != endpoint || receipt.Fence != fence || receipt.RequestHash != hash || receipt.HTTPStatus != r.HTTPStatus {
			r.Payload = nil
			return r, errors.New("receipt request/status mismatch")
		}
	}
	return r, err
}

func (c *testProfileClient) request[T any](ctx context.Context, endpoint p.Endpoint, runID string, raw []byte, limit int) (p.Response[T], error) {
	var out p.Response[T]
	method, path, err := endpoint.Route(runID)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.target.Origin+path, bytes.NewReader(raw))
	if err != nil {
		return out, errors.New("invalid replay request")
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return out, fmt.Errorf("replay transport failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only: the body is fully read below
	out.HTTPStatus = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if len(body) > limit {
		out.Body = body[:limit]
		return out, errors.New("replay response exceeds limit")
	}
	out.Body = body
	if err != nil {
		return out, errors.New("replay response incomplete")
	}
	out.Complete = true
	success := resp.StatusCode == http.StatusOK
	if endpoint == p.OrderEndpoint {
		success = resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusServiceUnavailable
	}
	if success {
		var payload T
		if err := p.Decode(body, &payload, limit); err != nil {
			return out, errors.New("invalid replay response")
		}
		out.Payload = &payload
		return out, nil
	}
	var problem p.ErrorResponse
	if err := p.Decode(body, &problem, limit); err != nil {
		return out, errors.New("invalid replay error response")
	}
	expected := map[string]int{"invalid_request": 400, "unauthorized": 401, "forbidden": 403, "not_found": 404, "identity_mismatch": 409, "epoch_mismatch": 409, "idempotency_conflict": 409, "run_conflict": 409, "in_progress": 409, "journal_limit": 413, "internal_error": 500}
	if resp.StatusCode != expected[problem.Code] && (problem.Code != "invalid_request" || resp.StatusCode != http.StatusRequestEntityTooLarge) {
		return out, errors.New("replay error status mismatch")
	}
	out.ProtocolError = &problem
	return out, nil
}

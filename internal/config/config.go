// Package config loads and validates mocker's configuration from the
// environment. Every knob is a MOCKER_* variable (DESIGN §16); nothing is read
// from a file, so the container stays configurable by `docker run -e` alone.
//
// Validation is strict and happens once at startup: a closed corporate contour
// is exactly the place where a service that boots with a half-configured host
// map and 404s an hour later is worse than one that refuses to start.
package config

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Routing selects how a request is attributed to a workspace.
type Routing string

const (
	// RoutingHost is the normal mode: <slug>.<BaseDomain> (DESIGN §2).
	RoutingHost Routing = "host"
	// RoutingPath is the emergency mode for contours without wildcard DNS;
	// it breaks absolute paths, Location and cookie Path (DESIGN §16).
	RoutingPath Routing = "path"
)

// AuthMode selects the admin-plane identity provider (DESIGN §15).
type AuthMode string

const (
	AuthShared AuthMode = "shared-password"
)

// minMCPKeyLen is the shortest MOCKER_MCP_KEY [Load] accepts. It guards
// against a one-character key, not against brute force: the endpoint has no
// rate limit by design (a key-holder is already trusted, DESIGN §18's model
// for every other route), so the key itself has to be the whole defense.
const minMCPKeyLen = 32

// TrustProxy describes how much of X-Forwarded-* may be believed.
//
// Off by default on purpose: with an open mock plane the request source is the
// only compensating control, and a direct client that can forge
// X-Forwarded-For can pin its own writes on a colleague (DESIGN §15).
type TrustProxy struct {
	// Enabled is false unless CIDRs were configured. A hop count with no
	// CIDRs never sets this on its own — see [parseTrustProxy].
	Enabled bool
	// Hops is the number of trailing X-Forwarded-For entries to skip. Zero
	// means "use the nearest entry" (equivalent to Hops: 1); it is orthogonal
	// to which peers are trusted, which CIDRs alone decides.
	Hops int
	// CIDRs lists proxy addresses whose forwarding headers are believed.
	// Never empty when Enabled is true.
	CIDRs []netip.Prefix
}

// Config is the fully-validated runtime configuration.
type Config struct {
	Addr string
	// LogLevel selects slog's level: "debug", "info", "warn" or "error"
	// (DESIGN §16). Validated here so main never has to fall back silently on
	// a typo'd value.
	LogLevel string

	BaseDomain     string
	AdminHost      string
	Routing        Routing
	ReservedPrefix string

	AuthMode           AuthMode
	SharedPasswordHash string

	// MCPKey is MOCKER_MCP_KEY, the bearer credential for POST /mcp — the
	// MCP (Model Context Protocol) endpoint on the admin host. Empty (the
	// default) means /mcp is not mounted at all: cmd/mocker/main.go only
	// calls admin.Server.SetMCP when this is non-empty, so an unconfigured
	// deployment has no MCP surface to even 401 against, let alone attack.
	// Separate from SharedPasswordHash so revoking one credential does not
	// lock out the other. Validated below rather than in a dedicated
	// mcpKey() helper (unlike size/count) because the rule is a single
	// length check, not a parseable shape worth its own function.
	MCPKey string

	// DefaultSpecID is specs.id of a spec ALREADY imported before this
	// process starts (DESIGN §14 screen 2: "Первый вход"). An operator
	// cannot know a spec's row id before importing it, but they can read it
	// straight off the "Специфи" list (or the import response) right after
	// they import it, then set MOCKER_DEFAULT_SPEC to that number for the
	// next deploy — unlike a name (specs.name has no UNIQUE constraint,
	// DESIGN §13, so a name can be ambiguous or match nothing) or a path to
	// a document (the container has no filesystem access to an operator's
	// spec file, and importing raw bytes from an env var at boot would be a
	// second, unaudited import path next to the one the admin API already
	// has). Zero means unset — the pre-existing behavior (no auto-create).
	DefaultSpecID int64
	DataDir       string

	MaxBody     int64
	MaxResponse int64
	// MaxAsset caps one uploaded file, MaxAssetsTotal a workspace's stored
	// sum (A6, DESIGN §32.2). MaxAsset may not exceed MaxBody: the
	// dispatcher's http.MaxBytesReader would refuse every upload at the cap
	// with a 413 naming the wrong variable.
	MaxAsset       int64
	MaxAssetsTotal int64
	MaxEntities    int
	TrafficMaxBody int64

	TrafficRetention    int
	CheckpointRetention int
	// SuggestionRetention is MOCKER_SUGGESTION_RETENTION: how many
	// resource_suggestions generations [specs.Repo.Rederive] keeps per
	// spec_id, oldest pruned first, inside the same transaction as the
	// insert (decisions.md §D5). 0 means keep every generation, the same
	// "0 means do less" shape CheckpointRetention's own 0 already uses.
	SuggestionRetention int
	// CheckpointDebounce is MOCKER_CHECKPOINT_DEBOUNCE in seconds: the minimum
	// gap [checkpoints.Repo.Auto] enforces between two "auto" rows for the
	// same workspace. 0 disables the trigger outright (no auto rows are ever
	// written) rather than being clamped up to some floor -- the same "0
	// means do less" shape CheckpointRetention's own 0 already uses.
	CheckpointDebounce int
	RuntimeCache       int

	// StreamPing, StreamFrameTimeout and StreamSessionRecheck are P6a's
	// three variables (decisions.md mocker-p6a-sse D12): MOCKER_STREAM_PING,
	// MOCKER_STREAM_FRAME_TIMEOUT and MOCKER_STREAM_SESSION_RECHECK, integer
	// SECONDS read by count() exactly as CheckpointDebounce is — this file
	// parses no Go duration strings and one knob's worth of new parsing is
	// not worth a second spelling of the same idea — and converted to a
	// time.Duration at the package boundary (cmd/mocker/main.go), never
	// inside internal/stream. Unlike every other count() here, 0 is REFUSED
	// for all three (Load's own check below): a zero ping or recheck
	// interval is a ticker that fires continuously and a zero per-frame
	// deadline expires every write before it is attempted, and there is no
	// "disabled" reading to give any of them.
	StreamPing           int
	StreamFrameTimeout   int
	StreamSessionRecheck int

	// StreamMaxConns, StreamMaxLifetime and StreamTrafficFrames are P6b's
	// three (decisions.md mocker-p6b-sse-mock D8; DESIGN §30.11), the mock
	// plane's: live SSE mock connections per WORKSPACE (0 refuses every
	// stream handshake outright — the one MOCKER_STREAM_* value whose zero
	// means something), seconds a mock stream may live (>= 1, like the
	// three above; the admin feed keeps its own constant), and what of a
	// connection the traffic recorder stores: "off" (no body) or "first"
	// (the first frame), or "all" (A14: every frame each way, into the
	// connection's ONE row, bounded by StreamTrafficMaxFrames and
	// StreamTrafficMaxBytes — §30.13's "own retention budget": the row
	// count is unchanged, so frames can never evict ordinary rows, and the
	// two caps bound what one row can hold).
	StreamMaxConns      int
	StreamMaxLifetime   int
	StreamTrafficFrames string
	// StreamTrafficMaxFrames and StreamTrafficMaxBytes (A14) are "all"'s
	// per-row budget: how many frames each way one connection's row keeps
	// (>= 1) and how many bytes each way (>= 1kb). Read only under "all";
	// "first" keeps its TrafficMaxBody cap.
	StreamTrafficMaxFrames int
	StreamTrafficMaxBytes  int64

	// StreamMaxFrame, StreamSendBudget and StreamOrigins are P6d's three
	// (decisions.md mocker-p6d-websocket D5; DESIGN §30.11), WebSocket's:
	// the INBOUND frame cap (a frame over it closes the connection with
	// 1009; outbound stays under MaxResponse), the per-connection byte
	// budget of the reactive/echo reply queue (a reply over it is dropped
	// and counted, never blocks), and the Origin allowlist — empty means
	// any origin, which is the mock plane's contract; a non-empty list
	// refuses a handshake whose Origin is present and not listed, before
	// the upgrade, and a request with NO Origin (every non-browser client)
	// is always allowed. Parsed like MOCKER_URL_IMPORT_ALLOWLIST (splitList);
	// every element must be scheme://host[:port] or startup fails.
	StreamMaxFrame   int64
	StreamSendBudget int64
	StreamOrigins    []string

	TrustProxy         TrustProxy
	URLImportAllowlist []string

	// Dev relaxes cookie hardening so the admin UI works over plain http on
	// localhost. Never set it in a deployment.
	Dev bool
}

// CookieSecure reports whether session cookies carry the Secure attribute.
func (c *Config) CookieSecure() bool { return !c.Dev }

// DBPath is the SQLite file inside the data volume.
func (c *Config) DBPath() string { return c.DataDir + "/mocker.db" }

// Load reads the environment and validates it. The returned error, if any,
// joins every problem found so a misconfigured deployment is fixed in one pass
// instead of one restart per variable.
func Load() (*Config, error) {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	c := &Config{
		Addr:           env("MOCKER_ADDR", ":8080"),
		LogLevel:       strings.ToLower(strings.TrimSpace(env("MOCKER_LOG_LEVEL", "info"))),
		BaseDomain:     strings.ToLower(strings.Trim(env("MOCKER_BASE_DOMAIN", ""), ". ")),
		AdminHost:      strings.ToLower(strings.Trim(env("MOCKER_ADMIN_HOST", ""), ". ")),
		Routing:        Routing(env("MOCKER_ROUTING", string(RoutingHost))),
		ReservedPrefix: env("MOCKER_RESERVED_PREFIX", "/__mocker"),

		AuthMode:           AuthMode(env("MOCKER_AUTH_MODE", string(AuthShared))),
		SharedPasswordHash: env("MOCKER_SHARED_PASSWORD_HASH", ""),

		MCPKey: env("MOCKER_MCP_KEY", ""),

		DataDir: strings.TrimRight(env("MOCKER_DATA_DIR", "/data"), "/"),

		Dev: boolEnv("MOCKER_DEV"),
	}

	c.DefaultSpecID = defaultSpecID(env("MOCKER_DEFAULT_SPEC", ""), &errs)

	c.MaxBody = size("MOCKER_MAX_BODY", "10mb", &errs)
	c.MaxResponse = size("MOCKER_MAX_RESPONSE", "4mb", &errs)
	c.TrafficMaxBody = size("MOCKER_TRAFFIC_MAX_BODY", "8kb", &errs)
	// D11: 1000, not the 10000 this document advertised before P3h wired
	// the field into anything — the code has enforced 1000 since P3a
	// (internal/resources' own row cap), and defaulting the KNOB to the
	// number the DOCUMENTS advertised while the CODE enforced a tenth of
	// it would raise the real ceiling tenfold on every upgraded
	// installation the moment this default reaches it, silently.
	c.MaxEntities = count("MOCKER_MAX_ENTITIES", 1000, &errs)
	c.TrafficRetention = count("MOCKER_TRAFFIC_RETENTION", 1000, &errs)
	c.CheckpointRetention = count("MOCKER_CHECKPOINT_RETENTION", 20, &errs)
	c.SuggestionRetention = count("MOCKER_SUGGESTION_RETENTION", 3, &errs)
	c.CheckpointDebounce = count("MOCKER_CHECKPOINT_DEBOUNCE", 300, &errs)
	c.RuntimeCache = count("MOCKER_RUNTIME_CACHE", 32, &errs)
	c.StreamPing = count("MOCKER_STREAM_PING", 15, &errs)
	c.StreamFrameTimeout = count("MOCKER_STREAM_FRAME_TIMEOUT", 5, &errs)
	c.StreamSessionRecheck = count("MOCKER_STREAM_SESSION_RECHECK", 60, &errs)
	// count() accepts 0 — that is how MOCKER_CHECKPOINT_DEBOUNCE=0 means
	// "disabled" — but zero means nothing for these three and is dangerous
	// in three different ways (see the Config fields' own comment), so each
	// is floored at 1 here, with the message shape every other refused
	// value in this file uses.
	c.StreamMaxConns = count("MOCKER_STREAM_MAX_CONNS", 200, &errs)
	c.StreamMaxLifetime = count("MOCKER_STREAM_MAX_LIFETIME", 900, &errs)
	c.StreamTrafficFrames = strings.ToLower(strings.TrimSpace(env("MOCKER_STREAM_TRAFFIC_FRAMES", "off")))
	switch c.StreamTrafficFrames {
	case "off", "first", "all":
	default:
		fail("MOCKER_STREAM_TRAFFIC_FRAMES: want off, first or all, got %q", c.StreamTrafficFrames)
	}
	loadStreamTraffic(c, &errs, fail)
	for _, v := range []struct {
		key string
		val int
	}{
		{"MOCKER_STREAM_PING", c.StreamPing},
		{"MOCKER_STREAM_FRAME_TIMEOUT", c.StreamFrameTimeout},
		{"MOCKER_STREAM_SESSION_RECHECK", c.StreamSessionRecheck},
		{"MOCKER_STREAM_MAX_LIFETIME", c.StreamMaxLifetime},
	} {
		if v.val < 1 {
			fail("%s: want a positive integer number of seconds, got %d", v.key, v.val)
		}
	}
	loadStreamWS(c, &errs, fail)
	loadAssets(c, &errs, fail)

	tp, err := parseTrustProxy(env("MOCKER_TRUST_PROXY", "off"))
	if err != nil {
		fail("MOCKER_TRUST_PROXY: %w", err)
	}
	c.TrustProxy = tp
	c.URLImportAllowlist = splitList(env("MOCKER_URL_IMPORT_ALLOWLIST", ""))

	switch c.Routing {
	case RoutingHost, RoutingPath:
	default:
		fail("MOCKER_ROUTING: want %q or %q, got %q", RoutingHost, RoutingPath, c.Routing)
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		fail("MOCKER_LOG_LEVEL: want debug, info, warn or error, got %q", c.LogLevel)
	}

	if c.AdminHost == "" {
		fail("MOCKER_ADMIN_HOST is required")
	}
	if c.Routing == RoutingHost {
		switch {
		case c.BaseDomain == "":
			fail("MOCKER_BASE_DOMAIN is required when MOCKER_ROUTING=host")
		case c.AdminHost == c.BaseDomain:
			fail("MOCKER_ADMIN_HOST must differ from MOCKER_BASE_DOMAIN")
		case strings.HasSuffix(c.AdminHost, "."+c.BaseDomain):
			// Otherwise the admin host looks like a workspace slug and the
			// mock plane swallows the admin UI.
			fail("MOCKER_ADMIN_HOST (%s) must not sit under MOCKER_BASE_DOMAIN (%s)", c.AdminHost, c.BaseDomain)
		}
	}

	// Trim FIRST, then validate: "//" used to pass the check below ("has
	// the / prefix and is not /") and then trim to "", and an empty prefix
	// matches every request, so the mock plane answered only control
	// routes.
	c.ReservedPrefix = strings.TrimRight(c.ReservedPrefix, "/")
	if !strings.HasPrefix(c.ReservedPrefix, "/") || c.ReservedPrefix == "" || strings.Contains(c.ReservedPrefix, "//") {
		fail("MOCKER_RESERVED_PREFIX must be a non-empty path starting with / (no empty segment), got %q", env("MOCKER_RESERVED_PREFIX", "/__mocker"))
	}
	checkHostsAndFloors(c, fail)

	switch c.AuthMode {
	case AuthShared:
		if c.SharedPasswordHash == "" {
			fail("MOCKER_SHARED_PASSWORD_HASH is required for MOCKER_AUTH_MODE=shared-password (generate with `mocker hash-password`)")
		}
	default:
		fail("MOCKER_AUTH_MODE: unsupported mode %q", c.AuthMode)
	}

	// A non-empty key shorter than minMCPKeyLen is a misconfiguration, not a
	// weak-but-usable credential: this project fails on the ground rather
	// than degrading (see the package doc comment), and a one-character key
	// would otherwise mount a real door with a trivially guessable lock.
	// Empty is exempt — that is "feature off", not "feature on and weak".
	if c.MCPKey != "" && len(c.MCPKey) < minMCPKeyLen {
		fail("MOCKER_MCP_KEY: must be at least %d bytes when set, got %d", minMCPKeyLen, len(c.MCPKey))
	}

	if c.DataDir == "" {
		fail("MOCKER_DATA_DIR must not be empty")
	}

	return c, errors.Join(errs...)
}

// IsWorkspaceHost reports whether host addresses a workspace and returns its
// slug. It is the single place that knows the host layout, so the request path
// (DESIGN §6) cannot drift from validation above.
//
// host may carry a port; matching is case-insensitive. Only a single label is
// accepted before the base domain: deeper names are somebody else's vhost.
func (c *Config) IsWorkspaceHost(host string) (slug string, ok bool) {
	h := normalizeHost(host)
	if c.BaseDomain == "" || h == "" {
		return "", false
	}
	rest, found := strings.CutSuffix(h, "."+c.BaseDomain)
	if !found || rest == "" || strings.Contains(rest, ".") {
		return "", false
	}
	return rest, true
}

// IsAdminHost reports whether host addresses the admin plane.
func (c *Config) IsAdminHost(host string) bool {
	return c.AdminHost != "" && normalizeHost(host) == c.AdminHost
}

// normalizeHost lowercases a Host header and strips the port and trailing dot.
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i+1:], "]") {
		// A bare IPv6 literal has no port; a bracketed one ends in ']'.
		if !strings.Contains(h, "]") || strings.LastIndexByte(h, ']') < i {
			h = h[:i]
		}
	}
	h = strings.Trim(h, "[]")
	return strings.TrimSuffix(h, ".")
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func boolEnv(key string) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// defaultSpecID parses MOCKER_DEFAULT_SPEC as a specs.id — see
// [Config.DefaultSpecID]'s doc comment for why the value denotes an id and
// not a name or a document. Empty means "unset" (0), the pre-existing
// no-auto-create behavior; anything present that is not a positive integer
// is a startup-time configuration error, not a silent no-op — DESIGN's own
// "если есть MOCKER_DEFAULT_SPEC" only makes sense read as "if it names a
// real spec", so a typo'd value must fail loudly here rather than quietly
// never auto-create anything. Whether the id actually names a spec in the
// database is checked later, once the database is open (cmd/mocker/main.go)
// — Load has no database handle to check it here.
func defaultSpecID(raw string, errs *[]error) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		*errs = append(*errs, fmt.Errorf("MOCKER_DEFAULT_SPEC: want a positive integer spec id, got %q", raw))
		return 0
	}
	return n
}

func count(key string, def int, errs *[]error) int {
	raw := env(key, "")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		*errs = append(*errs, fmt.Errorf("%s: want a non-negative integer, got %q", key, raw))
		return def
	}
	return n
}

func size(key, def string, errs *[]error) int64 {
	raw := env(key, def)
	n, err := ParseSize(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", key, err))
		n, _ = ParseSize(def)
	}
	return n
}

// ParseSize accepts "4096", "8kb", "10mb", "1gb" (case- and space-insensitive)
// and returns bytes.
func ParseSize(raw string) (int64, error) {
	s := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), " ", ""))
	mult := int64(1)
	for _, suffix := range []struct {
		name string
		mult int64
	}{{"kb", 1 << 10}, {"mb", 1 << 20}, {"gb", 1 << 30}, {"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"b", 1}} {
		if rest, found := strings.CutSuffix(s, suffix.name); found {
			s, mult = rest, suffix.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > math.MaxInt64/mult {
		// The overflow check is not decoration: past it the product wraps
		// negative and a negative size reaches http.MaxBytesReader.
		return 0, fmt.Errorf("want a size like 10mb, got %q", raw)
	}
	return n * mult, nil
}

// parseTrustProxy accepts "off", or a comma-separated list of proxy
// CIDRs/addresses — the peers whose X-Forwarded-* headers are actually
// believed — with an optional bare integer mixed in anywhere in the list
// naming the hop count (how many trailing X-Forwarded-For entries to walk
// back once a listed peer is matched; defaults to 1, the nearest entry, when
// omitted). E.g. "10.0.0.0/8" or "1,10.0.0.0/8".
//
// A hop count with no CIDRs/addresses is refused (round-1 review finding 4):
// on its own a hop count only says how deep into a chain to read, never which
// peer to trust reading it FROM, so accepting it alone used to make
// TrustsPeer believe every direct caller — reinstating the exact
// X-Forwarded-For forgery MOCKER_TRUST_PROXY exists to prevent (DESIGN §15).
func parseTrustProxy(raw string) (TrustProxy, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || s == "off" || s == "false" || s == "0" {
		return TrustProxy{}, nil
	}

	tp := TrustProxy{Enabled: true}
	hopsSet := false
	for _, item := range splitList(s) {
		if n, err := strconv.Atoi(item); err == nil {
			if n < 1 {
				return TrustProxy{}, fmt.Errorf("hop count must be >= 1, got %d", n)
			}
			if hopsSet {
				return TrustProxy{}, fmt.Errorf("hop count given more than once")
			}
			tp.Hops, hopsSet = n, true
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			tp.CIDRs = append(tp.CIDRs, p)
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return TrustProxy{}, fmt.Errorf("want off, a CIDR/address or a hop count; %q is neither", item)
		}
		tp.CIDRs = append(tp.CIDRs, netip.PrefixFrom(addr, addr.BitLen()))
	}
	if len(tp.CIDRs) == 0 {
		return TrustProxy{}, fmt.Errorf(
			"a hop count alone grants blanket trust to any direct client; list at least one proxy CIDR/address too")
	}
	return tp, nil
}

// TrustsPeer reports whether forwarding headers from addr may be believed.
//
// Every Enabled TrustProxy has at least one CIDR: [parseTrustProxy] refuses
// to build one without (round-1 review finding 4). A hop count alone cannot
// answer "is this peer my reverse proxy" — it only says how deep into an
// ALREADY-trusted chain to read — so trusting on hop count alone would trust
// literally any direct client, which is exactly the X-Forwarded-For forgery
// MOCKER_TRUST_PROXY exists to prevent.
func (t TrustProxy) TrustsPeer(addr netip.Addr) bool {
	if !t.Enabled {
		return false
	}
	for _, p := range t.CIDRs {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// loadStreamTraffic reads A14's two: "all"'s per-row budget. A zero frame
// count would be "all" that records nothing — "off" spelled confusingly —
// so it is refused, the same shape the three P6a timers use for their zero;
// the byte cap is floored at 1kb like every other size here. Its own
// function for the reason loadStreamWS is: Load's branch count is the
// specification and gocyclo counts it.
func loadStreamTraffic(c *Config, errs *[]error, fail func(format string, args ...any)) {
	c.StreamTrafficMaxFrames = count("MOCKER_STREAM_TRAFFIC_MAX_FRAMES", 200, errs)
	if c.StreamTrafficMaxFrames < 1 {
		fail("MOCKER_STREAM_TRAFFIC_MAX_FRAMES: want at least 1, got %d", c.StreamTrafficMaxFrames)
	}
	c.StreamTrafficMaxBytes = size("MOCKER_STREAM_TRAFFIC_MAX_BYTES", "64kb", errs)
	if c.StreamTrafficMaxBytes < 1024 {
		fail("MOCKER_STREAM_TRAFFIC_MAX_BYTES: want at least 1kb, got %d bytes", c.StreamTrafficMaxBytes)
	}
}

// loadStreamWS reads P6d's three WebSocket variables (decisions.md
// mocker-p6d-websocket D5) — its own function so that Load stays under the
// cyclomatic ceiling rather than carrying a nolint. The two byte sizes are
// floored at 1kb — a frame cap below a kilobyte refuses every JSON object of
// any use, and a budget below it drops every reply — and the origin list is
// validated element by element, because an element that is not
// scheme://host[:port] would silently never match and turn the allowlist
// into a denylist of one.
func loadStreamWS(c *Config, errs *[]error, fail func(format string, args ...any)) {
	c.StreamMaxFrame = size("MOCKER_STREAM_MAX_FRAME", "64kb", errs)
	c.StreamSendBudget = size("MOCKER_STREAM_SEND_BUDGET", "256kb", errs)
	for _, v := range []struct {
		key string
		val int64
	}{
		{"MOCKER_STREAM_MAX_FRAME", c.StreamMaxFrame},
		{"MOCKER_STREAM_SEND_BUDGET", c.StreamSendBudget},
	} {
		if v.val < 1024 {
			fail("%s: want at least 1kb, got %d bytes", v.key, v.val)
		}
	}
	c.StreamOrigins = splitList(env("MOCKER_STREAM_ORIGINS", ""))
	for i, o := range c.StreamOrigins {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
			fail("MOCKER_STREAM_ORIGINS: element %d (%q) must be scheme://host[:port] with scheme http or https", i+1, o)
			continue
		}
		c.StreamOrigins[i] = strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
	}
}

func splitList(raw string) []string {
	return slices.Collect(strings.FieldsSeq(strings.ReplaceAll(raw, ",", " ")))
}

// loadAssets reads A6's two variables (decisions.md mocker-a6-assets D2) —
// its own function for the reason loadStreamWS is. MOCKER_MAX_ASSET caps
// one file (floor 1kb: a smaller cap refuses every real picture and reads
// as a typo); MOCKER_MAX_ASSETS_TOTAL caps a workspace's sum and may not be
// below the per-file cap, or no file at the cap could ever be stored. The
// per-file cap may not exceed MOCKER_MAX_BODY, because the dispatcher's
// body limit runs first and would answer a 413 that names the wrong knob.
func loadAssets(c *Config, errs *[]error, fail func(format string, args ...any)) {
	c.MaxAsset = size("MOCKER_MAX_ASSET", "8mb", errs)
	c.MaxAssetsTotal = size("MOCKER_MAX_ASSETS_TOTAL", "64mb", errs)
	if c.MaxAsset < 1024 {
		fail("MOCKER_MAX_ASSET: want at least 1kb, got %d bytes", c.MaxAsset)
	}
	if c.MaxAssetsTotal < c.MaxAsset {
		fail("MOCKER_MAX_ASSETS_TOTAL (%d) must not be below MOCKER_MAX_ASSET (%d)", c.MaxAssetsTotal, c.MaxAsset)
	}
	if c.MaxAsset > c.MaxBody {
		fail("MOCKER_MAX_ASSET (%d) must not exceed MOCKER_MAX_BODY (%d): the request body limit would refuse every upload at the cap first", c.MaxAsset, c.MaxBody)
	}
}

// Limits is the read-only projection of every ceiling and budget an
// operator or an agent can hit from the outside (A9, 2026-09-02): the
// numbers behind a 413, a refused stream draft, a 503 over the connection
// cap. It exists so that neither the panel nor an MCP client has to guess
// them from a variable name — the panel reads it off ServerConfigView
// (login and GET /api/me) and the agent through get_server_config, and both
// read THIS function, so the two can never disagree. Bytes are bytes (the
// parsed value, not the "8mb" spelling), seconds are seconds, exactly as
// the fields above hold them.
type Limits struct {
	MaxBodyBytes          int64  `json:"maxBodyBytes"`
	MaxResponseBytes      int64  `json:"maxResponseBytes"`
	MaxAssetBytes         int64  `json:"maxAssetBytes"`
	MaxAssetsTotalBytes   int64  `json:"maxAssetsTotalBytes"`
	MaxEntities           int    `json:"maxEntities"`
	TrafficMaxBodyBytes   int64  `json:"trafficMaxBodyBytes"`
	TrafficRetention      int    `json:"trafficRetention"`
	CheckpointRetention   int    `json:"checkpointRetention"`
	CheckpointDebounceSec int    `json:"checkpointDebounceSec"`
	StreamMaxConns        int    `json:"streamMaxConns"`
	StreamMaxLifetimeSec  int    `json:"streamMaxLifetimeSec"`
	StreamMaxFrameBytes   int64  `json:"streamMaxFrameBytes"`
	StreamSendBudgetBytes int64  `json:"streamSendBudgetBytes"`
	StreamPingSec         int    `json:"streamPingSec"`
	StreamFrameTimeoutSec int    `json:"streamFrameTimeoutSec"`
	StreamTrafficFrames   string `json:"streamTrafficFrames"`
	// A14: "all"'s per-row budget.
	StreamTrafficMaxFrames int   `json:"streamTrafficMaxFrames"`
	StreamTrafficMaxBytes  int64 `json:"streamTrafficMaxBytes"`
}

// Limits returns the effective ceilings of this process.
func (c *Config) Limits() Limits {
	return Limits{
		MaxBodyBytes:           c.MaxBody,
		MaxResponseBytes:       c.MaxResponse,
		MaxAssetBytes:          c.MaxAsset,
		MaxAssetsTotalBytes:    c.MaxAssetsTotal,
		MaxEntities:            c.MaxEntities,
		TrafficMaxBodyBytes:    c.TrafficMaxBody,
		TrafficRetention:       c.TrafficRetention,
		CheckpointRetention:    c.CheckpointRetention,
		CheckpointDebounceSec:  c.CheckpointDebounce,
		StreamMaxConns:         c.StreamMaxConns,
		StreamMaxLifetimeSec:   c.StreamMaxLifetime,
		StreamMaxFrameBytes:    c.StreamMaxFrame,
		StreamSendBudgetBytes:  c.StreamSendBudget,
		StreamPingSec:          c.StreamPing,
		StreamFrameTimeoutSec:  c.StreamFrameTimeout,
		StreamTrafficFrames:    c.StreamTrafficFrames,
		StreamTrafficMaxFrames: c.StreamTrafficMaxFrames,
		StreamTrafficMaxBytes:  c.StreamTrafficMaxBytes,
	}
}

// checkHostsAndFloors holds two shape checks Load gained in the 2026-09-03
// audit, kept out of Load's own branch count. The dispatcher compares a
// request's host with its port STRIPPED (normalizeHost), so an admin host
// or base domain carrying a port or a scheme can never match a request:
// the admin plane would 404 forever and the container's own healthcheck,
// which sends that exact Host, would fail with it. And the three body
// sizes get the same 1kb floor the stream and asset sizes already have: a
// zero starts a server where every body and every frame is over its cap,
// which nothing would say out loud.
func checkHostsAndFloors(c *Config, fail func(format string, args ...any)) {
	for _, h := range []struct{ name, value string }{{"MOCKER_ADMIN_HOST", c.AdminHost}, {"MOCKER_BASE_DOMAIN", c.BaseDomain}} {
		if strings.ContainsAny(h.value, ":/[]") {
			fail("%s must be a bare host name without port or scheme, got %q", h.name, h.value)
		}
	}
	for _, sz := range []struct {
		name  string
		value int64
	}{{"MOCKER_MAX_BODY", c.MaxBody}, {"MOCKER_MAX_RESPONSE", c.MaxResponse}, {"MOCKER_TRAFFIC_MAX_BODY", c.TrafficMaxBody}} {
		if sz.value < 1<<10 {
			fail("%s must be at least 1kb, got %d", sz.name, sz.value)
		}
	}
}

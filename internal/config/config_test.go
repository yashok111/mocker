package config_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

// setBaseEnv sets every environment variable [config.Load] refuses to start
// without, so a test can vary just MOCKER_TRUST_PROXY and still get past
// validation for everything else.
func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MOCKER_ADMIN_HOST", "mocker.local")
	t.Setenv("MOCKER_BASE_DOMAIN", "mock.local")
	t.Setenv("MOCKER_AUTH_MODE", "shared-password")
	// Load only checks this is non-empty; it never parses the hash.
	t.Setenv("MOCKER_SHARED_PASSWORD_HASH", "$argon2id$v=19$m=65536,t=1,p=4$c29tZXNhbHQ$c29tZWhhc2g")
	t.Setenv("MOCKER_DATA_DIR", t.TempDir())
}

// TestLoad_TrustProxy is round-1 review finding 4's regression at the
// MOCKER_TRUST_PROXY env var: a bare hop count with no CIDR/address must be
// rejected at startup, not silently accepted as blanket trust.
func TestLoad_TrustProxy(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantErr   bool
		wantHops  int
		wantCIDRs int
	}{
		{name: "off", raw: "off", wantErr: false},
		{name: "empty defaults to off", raw: "", wantErr: false},
		{
			name:    "bare hop count alone is refused",
			raw:     "1",
			wantErr: true,
		},
		{
			name:    "hop count without any CIDR is refused even when large",
			raw:     "3",
			wantErr: true,
		},
		{
			name:      "CIDR alone is accepted, hop count defaults to 0 (nearest entry)",
			raw:       "10.0.0.0/8",
			wantErr:   false,
			wantHops:  0,
			wantCIDRs: 1,
		},
		{
			name:      "hop count combined with a CIDR is accepted",
			raw:       "1,10.0.0.0/8",
			wantErr:   false,
			wantHops:  1,
			wantCIDRs: 1,
		},
		{
			name:      "order does not matter",
			raw:       "10.0.0.0/8,2",
			wantErr:   false,
			wantHops:  2,
			wantCIDRs: 1,
		},
		{
			name:    "hop count given twice is refused",
			raw:     "1,2,10.0.0.0/8",
			wantErr: true,
		},
		{
			name:    "garbage is neither a hop count nor a CIDR/address",
			raw:     "not-a-thing",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("MOCKER_TRUST_PROXY", tt.raw)

			cfg, err := config.Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() with MOCKER_TRUST_PROXY=%q: error = nil, want an error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() with MOCKER_TRUST_PROXY=%q: unexpected error: %v", tt.raw, err)
			}
			if cfg.TrustProxy.Hops != tt.wantHops {
				t.Errorf("TrustProxy.Hops = %d, want %d", cfg.TrustProxy.Hops, tt.wantHops)
			}
			if len(cfg.TrustProxy.CIDRs) != tt.wantCIDRs {
				t.Errorf("len(TrustProxy.CIDRs) = %d, want %d", len(cfg.TrustProxy.CIDRs), tt.wantCIDRs)
			}
		})
	}
}

// TestLoad_MCPKey covers MOCKER_MCP_KEY's contract (MCP slice §A2): unset
// means "no /mcp surface" (empty, Load still succeeds — DESIGN §16's absent-
// variable-means-off default), a key at or above the 32-byte floor is
// accepted as-is, and anything shorter is a startup-time failure rather than
// a route silently guarded by a key an attacker could brute force.
func TestLoad_MCPKey(t *testing.T) {
	tests := []struct {
		name    string
		raw     string // unset env var when empty
		want    string
		wantErr bool
	}{
		{name: "unset defaults to empty, no error", raw: "", want: ""},
		{name: "exactly 32 bytes accepted", raw: strings.Repeat("k", 32), want: strings.Repeat("k", 32)},
		{name: "well over 32 bytes accepted", raw: strings.Repeat("k", 64), want: strings.Repeat("k", 64)},
		{name: "31 bytes rejected", raw: strings.Repeat("k", 31), wantErr: true},
		{name: "single character rejected", raw: "x", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			if tt.raw != "" {
				t.Setenv("MOCKER_MCP_KEY", tt.raw)
			}

			cfg, err := config.Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error for MOCKER_MCP_KEY of length %d", len(tt.raw))
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if cfg.MCPKey != tt.want {
				t.Errorf("MCPKey = %q, want %q", cfg.MCPKey, tt.want)
			}
		})
	}
}

// TestLoad_DefaultSpecID covers MOCKER_DEFAULT_SPEC's contract: unset means
// "skip auto-create" (0, and Load must still succeed — the exact behavior a
// deployment with the variable absent has today), a positive integer is
// accepted as-is, and anything else (non-numeric, zero, negative) is a
// startup-time error rather than a silently-ignored value — DESIGN §14
// screen 2's auto-create must fail loudly on a typo, not act as if it were
// never configured.
func TestLoad_DefaultSpecID(t *testing.T) {
	tests := []struct {
		name    string
		raw     string // unset env var when empty
		want    int64
		wantErr bool
	}{
		{name: "unset defaults to 0, no error", raw: "", want: 0},
		{name: "positive integer accepted", raw: "42", want: 42},
		{name: "single digit accepted", raw: "1", want: 1},
		{name: "zero rejected", raw: "0", wantErr: true},
		{name: "negative rejected", raw: "-3", wantErr: true},
		{name: "non-numeric rejected", raw: "widgets-api", wantErr: true},
		{name: "float rejected", raw: "4.5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			if tt.raw != "" {
				t.Setenv("MOCKER_DEFAULT_SPEC", tt.raw)
			}

			cfg, err := config.Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error for MOCKER_DEFAULT_SPEC=%q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if cfg.DefaultSpecID != tt.want {
				t.Errorf("DefaultSpecID = %d, want %d", cfg.DefaultSpecID, tt.want)
			}
		})
	}
}

// TestLoad_CheckpointDebounce covers MOCKER_CHECKPOINT_DEBOUNCE's contract
// (P2d): unset defaults to 300 seconds, an explicit 0 is accepted as-is and
// means the debounce trigger is disabled entirely (not clamped up to some
// floor), any other non-negative integer is accepted as seconds, and
// anything negative or non-numeric is a startup-time error via the shared
// count() helper -- the same helper MOCKER_CHECKPOINT_RETENTION already uses.
func TestLoad_CheckpointDebounce(t *testing.T) {
	tests := []struct {
		name    string
		raw     string // unset env var when empty
		want    int
		wantErr bool
	}{
		{name: "unset defaults to 300, no error", raw: "", want: 300},
		{name: "explicit zero disables the trigger", raw: "0", want: 0},
		{name: "positive integer accepted", raw: "60", want: 60},
		{name: "negative rejected", raw: "-1", wantErr: true},
		{name: "non-numeric rejected", raw: "soon", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			if tt.raw != "" {
				t.Setenv("MOCKER_CHECKPOINT_DEBOUNCE", tt.raw)
			}

			cfg, err := config.Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error for MOCKER_CHECKPOINT_DEBOUNCE=%q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if cfg.CheckpointDebounce != tt.want {
				t.Errorf("CheckpointDebounce = %d, want %d", cfg.CheckpointDebounce, tt.want)
			}
		})
	}
}

// TestLoad_MaxEntitiesDefault is D11's own regression guard: the code has
// enforced 1000 entities per resource since P3a
// (internal/resources.maxEntityRows, now internal/resources.Repo.maxEntityRows),
// while this field's own DEFAULT stayed 10000 — the number the documents
// advertised and nothing enforced — until P3h wired the field into the cap.
// Wiring config.MaxEntities into resources.NewRepo without also dropping
// this default would have raised the REAL, enforced ceiling tenfold on
// every upgraded installation the moment this field started being read at
// all; this test pins the default at the value the code has always
// enforced, not the value the documents used to advertise.
func TestLoad_MaxEntitiesDefault(t *testing.T) {
	tests := []struct {
		name    string
		raw     string // unset env var when empty
		want    int
		wantErr bool
	}{
		{name: "unset defaults to 1000, not the old 10000", raw: "", want: 1000},
		{name: "explicit value accepted verbatim", raw: "50", want: 50},
		{name: "explicit zero accepted (no non-negative floor above zero)", raw: "0", want: 0},
		{name: "negative rejected", raw: "-1", wantErr: true},
		{name: "non-numeric rejected", raw: "lots", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			if tt.raw != "" {
				t.Setenv("MOCKER_MAX_ENTITIES", tt.raw)
			}

			cfg, err := config.Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error for MOCKER_MAX_ENTITIES=%q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if cfg.MaxEntities != tt.want {
				t.Errorf("MaxEntities = %d, want %d", cfg.MaxEntities, tt.want)
			}
		})
	}
}

// TestTrustProxy_TrustsPeer_requiresCIDRs is round-1 review finding 4's
// regression at [config.TrustProxy.TrustsPeer] itself: an Enabled TrustProxy
// with no CIDRs configured — the exact hop-count-only shape [config.Load] no
// longer produces, but the shape TrustsPeer must not special-case into
// blanket trust regardless of how it was built — must trust NOBODY, not
// everybody.
func TestTrustProxy_TrustsPeer_requiresCIDRs(t *testing.T) {
	addr := netip.MustParseAddr("203.0.113.7") // TEST-NET-3, arbitrary "attacker" address

	tests := []struct {
		name string
		tp   config.TrustProxy
		want bool
	}{
		{
			name: "disabled trusts nobody",
			tp:   config.TrustProxy{Enabled: false},
			want: false,
		},
		{
			name: "enabled with hops but no CIDRs trusts nobody (the finding 4 bug)",
			tp:   config.TrustProxy{Enabled: true, Hops: 1},
			want: false,
		},
		{
			name: "enabled with a matching CIDR trusts the peer",
			tp:   config.TrustProxy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
			want: true,
		},
		{
			name: "enabled with a non-matching CIDR does not trust the peer",
			tp:   config.TrustProxy{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tp.TrustsPeer(addr); got != tt.want {
				t.Errorf("TrustsPeer(%v) = %v, want %v", addr, got, tt.want)
			}
		})
	}
}

// TestLoad_StreamVariables covers P6a's three variables (decisions.md
// mocker-p6a-sse D12): integer seconds read through the shared count()
// helper exactly as MOCKER_CHECKPOINT_DEBOUNCE is, defaulting to 15 / 5 /
// 60, and — unlike every other count() in this file — refusing 0: a zero
// ping or recheck interval is a ticker that fires continuously and a zero
// frame deadline expires every write before it is attempted, and none of
// the three has a "disabled" reading to give.
func TestLoad_StreamVariables(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		setBaseEnv(t)
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error = %v, want nil", err)
		}
		if cfg.StreamPing != 15 || cfg.StreamFrameTimeout != 5 || cfg.StreamSessionRecheck != 60 {
			t.Errorf("defaults = %d/%d/%d, want 15/5/60", cfg.StreamPing, cfg.StreamFrameTimeout, cfg.StreamSessionRecheck)
		}
	})

	for _, key := range []string{"MOCKER_STREAM_PING", "MOCKER_STREAM_FRAME_TIMEOUT", "MOCKER_STREAM_SESSION_RECHECK"} {
		t.Run(key+" zero refused", func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(key, "0")
			if _, err := config.Load(); err == nil {
				t.Fatalf("Load() error = nil, want a refusal for %s=0", key)
			} else if !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() error = %v, want it to name %s", err, key)
			}
		})
		t.Run(key+" negative refused", func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(key, "-3")
			if _, err := config.Load(); err == nil {
				t.Fatalf("Load() error = nil, want a refusal for %s=-3", key)
			}
		})
		t.Run(key+" duration string refused", func(t *testing.T) {
			// Integer seconds only, the one duration shape this file
			// parses; "5s" is exactly the spelling an operator might guess.
			setBaseEnv(t)
			t.Setenv(key, "5s")
			if _, err := config.Load(); err == nil {
				t.Fatalf("Load() error = nil, want a refusal for %s=5s", key)
			}
		})
	}

	t.Run("non-default values reach the config", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("MOCKER_STREAM_PING", "3")
		t.Setenv("MOCKER_STREAM_FRAME_TIMEOUT", "4")
		t.Setenv("MOCKER_STREAM_SESSION_RECHECK", "7")
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error = %v, want nil", err)
		}
		if cfg.StreamPing != 3 || cfg.StreamFrameTimeout != 4 || cfg.StreamSessionRecheck != 7 {
			t.Errorf("values = %d/%d/%d, want 3/4/7", cfg.StreamPing, cfg.StreamFrameTimeout, cfg.StreamSessionRecheck)
		}
	})
}

// A14: "all" is accepted and carries a per-row budget of its own.
func TestLoad_StreamTrafficFramesAll(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		setBaseEnv(t)
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.StreamTrafficFrames != "off" || cfg.StreamTrafficMaxFrames != 200 || cfg.StreamTrafficMaxBytes != 64<<10 {
			t.Errorf("defaults = %q/%d/%d, want off/200/65536", cfg.StreamTrafficFrames, cfg.StreamTrafficMaxFrames, cfg.StreamTrafficMaxBytes)
		}
		lim := cfg.Limits()
		if lim.StreamTrafficMaxFrames != 200 || lim.StreamTrafficMaxBytes != 64<<10 {
			t.Errorf("Limits() = %+v, want the two budgets projected", lim)
		}
	})
	t.Run("all accepted", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("MOCKER_STREAM_TRAFFIC_FRAMES", "ALL")
		t.Setenv("MOCKER_STREAM_TRAFFIC_MAX_FRAMES", "5")
		t.Setenv("MOCKER_STREAM_TRAFFIC_MAX_BYTES", "2kb")
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error = %v, want all accepted", err)
		}
		if cfg.StreamTrafficFrames != "all" || cfg.StreamTrafficMaxFrames != 5 || cfg.StreamTrafficMaxBytes != 2048 {
			t.Errorf("got %q/%d/%d", cfg.StreamTrafficFrames, cfg.StreamTrafficMaxFrames, cfg.StreamTrafficMaxBytes)
		}
	})
	for _, tc := range []struct{ key, val string }{
		{"MOCKER_STREAM_TRAFFIC_MAX_FRAMES", "0"},
		{"MOCKER_STREAM_TRAFFIC_MAX_BYTES", "512"},
		{"MOCKER_STREAM_TRAFFIC_FRAMES", "some"},
	} {
		t.Run(tc.key+"="+tc.val+" refused", func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(tc.key, tc.val)
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("Load() error = %v, want a refusal naming %s", err, tc.key)
			}
		})
	}
}

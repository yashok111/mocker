package admin

import (
	"net/netip"
	"strconv"
	"testing"
	"time"
)

// TestRateLimiter_freshNamesDoNotBuyAFreshBudget pins the 2026-09-03 audit
// finding: the (address, name) key exists so a flood under one name cannot
// lock a colleague out — but on its own it let a caller who rotates the
// name per attempt guess the ONE shared password at any rate. The
// per-address bucket bounds every name from one address together.
func TestRateLimiter_freshNamesDoNotBuyAFreshBudget(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(10, time.Minute)
	now := time.Now()
	addr := netip.MustParseAddr("10.0.0.1")

	allowed := 0
	for i := range 10*addrLimitMultiplier + 20 {
		if l.allow(loginKey{addr: addr, name: "u" + strconv.Itoa(i)}, now) {
			allowed++
		}
	}
	if allowed != 10*addrLimitMultiplier {
		t.Fatalf("rotating names from one address: %d attempts allowed, want exactly %d", allowed, 10*addrLimitMultiplier)
	}

	// Another address is untouched.
	if !l.allow(loginKey{addr: netip.MustParseAddr("10.0.0.2"), name: "u0"}, now) {
		t.Fatal("a different address must keep its own budget")
	}
}

// TestRateLimiter_nameBucketStillIsolatesColleagues keeps the property the
// name half of the key was added for: one name's exhaustion does not touch
// another name behind the same address.
func TestRateLimiter_nameBucketStillIsolatesColleagues(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(10, time.Minute)
	now := time.Now()
	addr := netip.MustParseAddr("10.0.0.1")
	for range 10 {
		if !l.allow(loginKey{addr: addr, name: "noise"}, now) {
			t.Fatal("the first 10 attempts under one name must pass")
		}
	}
	if l.allow(loginKey{addr: addr, name: "noise"}, now) {
		t.Fatal("the 11th attempt under one name must be refused")
	}
	if !l.allow(loginKey{addr: addr, name: "alex"}, now) {
		t.Fatal("a colleague's name behind the same address must still pass")
	}
}

// TestRateLimiter_bucketMapIsCapped: past maxLoginBuckets a new key is
// refused rather than stored, and an expired window frees the room again.
func TestRateLimiter_bucketMapIsCapped(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(10, time.Minute)
	now := time.Now()
	// Distinct addresses so the per-address ceiling is never the reason.
	for i := range maxLoginBuckets / 2 {
		a := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})
		l.allow(loginKey{addr: a, name: "n"}, now) // one name bucket + one address bucket each
	}
	if got := len(l.buckets); got != maxLoginBuckets {
		t.Fatalf("buckets = %d, want the cap %d", got, maxLoginBuckets)
	}
	if l.allow(loginKey{addr: netip.MustParseAddr("192.168.1.1"), name: "new"}, now) {
		t.Fatal("a new key at the cap must be refused, not stored")
	}
	if !l.allow(loginKey{addr: netip.MustParseAddr("192.168.1.1"), name: "new"}, now.Add(2*time.Minute)) {
		t.Fatal("after the window every bucket is pruned and a new key fits again")
	}
}

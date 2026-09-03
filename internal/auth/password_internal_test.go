// This file's tests are white-box (package auth), unlike this package's
// black-box siblings in auth_test.go: proving the argon2 concurrency bound
// (round-1 review finding 5) means observing argonSem itself, which nothing
// exported surfaces, and a purely timing-based black-box test would be
// flaky across CI hardware with a different core count.
package auth

import (
	"sync"
	"testing"
	"time"
)

// TestVerifyPassword_argonConcurrencyBound proves argonSem actually gates
// VerifyPassword: launch far more concurrent callers than argonConcurrency
// and sample len(argonSem) — the number of in-flight argon2 calls, since
// every holder occupies exactly one slot for the duration of its
// argon2.IDKey call — throughout the burst. It must never exceed
// argonConcurrency; that is the actual property that stands between a login
// flood and an OOM (finding 5). Never observing the cap actually reached is
// only logged, not failed: on unusually slow or fast hardware the sampler
// might miss the peak, but that says nothing about whether the bound itself
// held.
func TestVerifyPassword_argonConcurrencyBound(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword(): %v", err)
	}

	const callers = argonConcurrency * 3
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-start
			if _, err := VerifyPassword(encoded, "wrong password"); err != nil {
				t.Errorf("VerifyPassword(): %v", err)
			}
		}()
	}

	stop := make(chan struct{})
	sampled := make(chan struct{})
	var maxObserved int
	var sawCap bool
	go func() {
		defer close(sampled)
		for {
			// len() on a channel takes the channel's own lock internally, so
			// this is safe to call concurrently with the sends/receives in
			// VerifyPassword's acquire/release — no race, just a snapshot.
			if n := len(argonSem); n > maxObserved {
				maxObserved = n
			}
			if len(argonSem) == argonConcurrency {
				sawCap = true
			}
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Microsecond):
			}
		}
	}()

	close(start) // release every goroutine at once, to actually contend
	wg.Wait()
	close(stop)
	<-sampled

	if maxObserved > argonConcurrency {
		t.Fatalf("observed %d concurrent argon2 verifications in flight, want <= argonConcurrency (%d)",
			maxObserved, argonConcurrency)
	}
	if !sawCap {
		t.Logf("never observed argonSem at its %d-slot capacity with %d concurrent callers (max observed %d) — "+
			"the bound held, but this run didn't prove much contention", argonConcurrency, callers, maxObserved)
	}
}

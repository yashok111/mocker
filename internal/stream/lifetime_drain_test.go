package stream

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestServe_lifetimeExpiresWhileDraining is the 2026-09-03 audit's stream
// finding: drain's non-blocking select used to RECEIVE from lifetime.C and
// return nil, Serve read nil as "keep going", and a time.Timer never fires
// twice — so under exactly the steady traffic that keeps drain iterating,
// D10's lifetime was silently lost and the connection lived until the peer
// left. The source below answers a FULL page forever; the shortened
// lifetime must still end the connection.
func TestServe_lifetimeExpiresWhileDraining(t *testing.T) {
	// Same discipline as TestServe_lifetimeExpiry: no t.Parallel, and the
	// var is restored only after Serve's own goroutine is joined.
	original := maxStreamLifetime
	maxStreamLifetime = 150 * time.Millisecond

	reg := NewRegistry()
	defer reg.Close()

	var last atomic.Int64
	fullPages := func(_ context.Context, since int64) ([]byte, int64, int, error) {
		id := last.Add(1)
		// Every page comes back exactly full, so drain never sees a short
		// page and keeps reading until something ELSE stops it.
		return []byte(`{"rows":[],"lastId":0,"dropped":0}`), id, MaxFrameRows, nil
	}

	st := openTestStream(t, reg, ServeConfig{
		Read: fullPages, Ping: time.Hour, FrameTimeout: time.Second, SessionRecheck: time.Hour,
	})
	waitForOpen(t, reg, 1)
	reg.Notify([]int64{1})

	select {
	case <-st.done:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve never returned: the lifetime timer was consumed by drain and lost")
	}
	_ = st.resp.Body.Close()
	t.Cleanup(func() { maxStreamLifetime = original })

	if got := last.Load(); got < 2 {
		t.Fatalf("read %d page(s); want the connection to have been draining full pages when the lifetime fired", got)
	}
}

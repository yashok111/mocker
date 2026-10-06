package backendblob

import (
	"bytes"
	"fmt"
	"testing"
)

// Optional measurement tooling only. B6.3's bounded verification does not run
// benchmarks; capture hardware, Go version, raw output and sample count later.
func BenchmarkRawStorageKey(b *testing.B) {
	for _, size := range []int{1024, 1 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			raw := bytes.Repeat([]byte("x"), size)
			d := Domain{"graph", "6", "node"}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				_ = Key(d, raw)
			}
		})
	}
}

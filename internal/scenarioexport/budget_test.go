package scenarioexport

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestResponseBudgetMatchesJSONEscaping(t *testing.T) {
	for _, content := range []string{"", "wrap:test", "\"\\\b\f\n\r\t\x00<>&\u2028\u2029", "Привет😀", string([]byte{0xff})} {
		d := Diagnostic{Code: "invalid", Severity: "error", Message: content, Target: &Target{Kind: "message", ID: "m1"}}
		for _, value := range []any{Artifact{Content: content, Diagnostics: []Diagnostic{d}}, OptionsResponse{Options: []Option{{Format: Mermaid, Ready: true, Diagnostics: []Diagnostic{d}}}}, httpx.ErrorBody{Error: httpx.ErrorDetail{Code: "blocked", Details: &BlockedError{Diagnostics: []Diagnostic{d}}}}} {
			raw, err := jsonx.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := New(nil, int64(len(raw))).CheckResponse(value); err != nil {
				t.Fatalf("exact budget %q: %v", raw, err)
			}
			if err := New(nil, int64(len(raw)-1)).CheckResponse(value); !errors.Is(err, ErrTooLarge) {
				t.Fatalf("undersized budget: %v", err)
			}
		}
	}
}

func TestResponseBudgetDoesNotAllocateEscapedContent(t *testing.T) {
	value := Artifact{Content: strings.Repeat("<", 1<<20)}
	// Track bytes, since a few giant allocations also exhaust memory.
	result := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			if !errors.Is(New(nil, 256).CheckResponse(value), ErrTooLarge) {
				b.Fatal("limit")
			}
		}
	})
	if result.AllocedBytesPerOp() > 8192 {
		t.Fatalf("preflight bytes/op: %d", result.AllocedBytesPerOp())
	}
}

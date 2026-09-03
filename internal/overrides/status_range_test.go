package overrides

import (
	"errors"
	"testing"
)

// TestNormalizeAndValidate_refusesAStatusNetHTTPCannotWrite pins the
// 2026-09-03 audit finding: a stored activeStatus (or responses key)
// outside 100..599 reached http.ResponseWriter.WriteHeader verbatim on
// every mock-plane request to the operation, which PANICS there — a 500
// with a stack trace per request, not one bad response.
func TestNormalizeAndValidate_refusesAStatusNetHTTPCannotWrite(t *testing.T) {
	t.Parallel()
	for _, status := range []int{0, 99, 600, 999, -1} {
		s := status
		err := normalizeAndValidate(&Row{Method: "get", Path: "/x", ActiveStatus: &s})
		if !errors.Is(err, ErrInvalidRow) {
			t.Errorf("activeStatus %d: err = %v, want ErrInvalidRow", status, err)
		}
	}
	for _, status := range []int{100, 200, 418, 599} {
		s := status
		if err := normalizeAndValidate(&Row{Method: "get", Path: "/x", ActiveStatus: &s}); err != nil {
			t.Errorf("activeStatus %d: err = %v, want ok", status, err)
		}
	}
	for _, key := range []string{"099", "600", "999"} {
		err := ValidateResponses(map[string]Variant{key: {}})
		if !errors.Is(err, ErrInvalidRow) {
			t.Errorf("responses key %q: err = %v, want ErrInvalidRow", key, err)
		}
	}
	if err := ValidateResponses(map[string]Variant{"599": {}, "100": {}}); err != nil {
		t.Errorf("responses keys 100/599: err = %v, want ok", err)
	}
}

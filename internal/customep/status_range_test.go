package customep

import (
	"errors"
	"testing"
)

// TestNormalizeAndValidate_refusesAStatusNetHTTPCannotWrite: the same
// range overrides enforces, through the same function — see
// overrides.ValidHTTPStatus for why a stored status outside it is a
// panic per request on the mock plane.
func TestNormalizeAndValidate_refusesAStatusNetHTTPCannotWrite(t *testing.T) {
	t.Parallel()
	for _, status := range []int{99, 600, 999, -1} {
		err := normalizeAndValidate(&Row{Method: "GET", Path: "/x", ActiveStatus: status}, 1<<20)
		if !errors.Is(err, ErrInvalidRow) {
			t.Errorf("status %d: err = %v, want ErrInvalidRow", status, err)
		}
	}
	row := &Row{Method: "GET", Path: "/x"}
	if err := normalizeAndValidate(row, 1<<20); err != nil || row.ActiveStatus != defaultActiveStatus {
		t.Errorf("zero status: err = %v, status = %d; want the default %d", err, row.ActiveStatus, defaultActiveStatus)
	}
}

package domain

import "testing"

// A slug is a DNS label; RFC 1123 refuses a label ending in a hyphen, and
// slugPattern used to admit one — and UniqueSlug's own fallback minted
// "ws-" for a name with no usable letter.
func TestSlug_noTrailingHyphen(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"ab-", "a-", "-ab", "ws-"} {
		if slugPattern.MatchString(bad) {
			t.Errorf("slugPattern admits %q", bad)
		}
	}
	for _, good := range []string{"ab", "a-b", "a1", "x-y-z"} {
		if !slugPattern.MatchString(good) {
			t.Errorf("slugPattern refuses %q", good)
		}
	}
	got, err := UniqueSlug("!!!", func(string) (bool, error) { return false, nil })
	if err != nil || got != "ws" {
		t.Fatalf("UniqueSlug(\"!!!\") = %q, %v; want \"ws\"", got, err)
	}
}

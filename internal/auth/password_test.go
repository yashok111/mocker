package auth

import "testing"

// ValidatePasswordHash refuses the parameters argon2.IDKey panics on, at
// startup rather than on the first login.
func TestValidatePasswordHash(t *testing.T) {
	t.Parallel()
	good, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePasswordHash(good); err != nil {
		t.Fatalf("a hash this build minted: %v", err)
	}
	for _, bad := range []string{
		"", "not-a-hash",
		"$argon2id$v=19$m=65536,t=0,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=0$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=4,t=3,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaA",
	} {
		if err := ValidatePasswordHash(bad); err == nil {
			t.Errorf("ValidatePasswordHash(%q) = nil, want an error", bad)
		}
	}
}

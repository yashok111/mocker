package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (DESIGN §15). time=1 with memory=64MiB keeps a login on
// commodity hardware fast while still costing real memory per guess;
// threads=4 matches a typical container CPU limit. Values are not tuned
// per-hash — see [decodePHC], which reads the cost parameters back out of the
// encoded string instead of assuming these constants still apply.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// argonConcurrency bounds how many argon2id verifications may run at once,
// process-wide (round-1 review finding 5). Each call allocates argonMemory
// (64 MiB); the login rate limiter's per-key window bounds how fast any ONE
// key can trigger a verification, but a key is (client address, submitted
// name) — an attacker who can present many distinct keys, whether many real
// source addresses or (absent finding 4's fix) forged ones, could otherwise
// still drive unbounded PARALLEL verifications and OOM the process. A
// request over the cap just waits its turn for a slot rather than failing
// outright, trading latency for the memory bound.
const argonConcurrency = 8

// argonSem is the semaphore [VerifyPassword] acquires around the actual
// argon2.IDKey call. A plain buffered channel, not a context-aware wait: the
// computation itself is a fixed, short, synchronous cost with no cancellation
// point inside argon2.IDKey to honor anyway, so there is nothing extra a
// context would buy here.
var argonSem = make(chan struct{}, argonConcurrency)

// ErrBadPassword is returned for any failed credential check: a wrong
// password, or — deliberately, see [SharedPassword.Login] — a malformed
// configured hash. It is never returned for a reason that would let a caller
// tell those two apart.
var ErrBadPassword = errors.New("auth: bad password")

// HashPassword derives a PHC-encoded argon2id hash from plain, with a fresh
// random salt: "$argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>". The encoding
// is self-describing — algorithm, version and cost parameters travel with
// the hash — so [VerifyPassword] never has to assume today's tuning still
// matches a hash minted under an older one.
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}
	sum := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

// VerifyPassword checks plain against a PHC-encoded argon2id hash produced by
// [HashPassword]. Parameters are parsed back out of encoded rather than
// assumed from the package constants, so a hash minted under a different
// cost setting still verifies against the cost it was actually hashed with.
// The digest comparison is constant-time.
// ValidatePasswordHash decodes encoded the way VerifyPassword will and
// refuses parameters argon2 itself would PANIC on (time < 1, threads < 1) or
// that no `mocker hash-password` output carries (memory below 8 KiB per
// thread, the library's own floor). Called once at startup: a hash the
// server cannot verify against is otherwise discovered as a 500 (a panic
// inside argon2.IDKey) or as every login answering 401 forever, with no
// line in the log naming the variable.
func ValidatePasswordHash(encoded string) error {
	m, t, p, _, hash, err := decodePHC(encoded)
	if err != nil {
		return err
	}
	switch {
	case t < 1:
		return errors.New("auth: password hash declares t=0 iterations; argon2 requires at least 1")
	case p < 1:
		return errors.New("auth: password hash declares p=0 threads; argon2 requires at least 1")
	case m < 8*uint32(p):
		return fmt.Errorf("auth: password hash declares m=%d KiB, below argon2's floor of 8 KiB per thread", m)
	case len(hash) == 0:
		return errors.New("auth: password hash carries an empty digest")
	}
	return nil
}

func VerifyPassword(encoded, plain string) (bool, error) {
	memory, iterations, threads, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false, err
	}
	argonSem <- struct{}{}
	defer func() { <-argonSem }()
	got := argon2.IDKey([]byte(plain), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// decodePHC parses "$argon2id$v=19$m=...,t=...,p=...$salt$hash" as produced
// by [HashPassword]. A malformed or tampered string — wrong field count,
// wrong algorithm tag, unparsable parameters, invalid base64 — is reported as
// an error rather than panicking or silently comparing against zero-value
// parameters, since encoded is attacker-reachable via a stored/edited hash.
func decodePHC(encoded string) (memory, iterations uint32, threads uint8, salt, hash []byte, err error) {
	parts := strings.Split(encoded, "$")
	// "$argon2id$v=19$m=...$salt$hash" splits into 6 fields; the first is
	// empty because the string starts with the separator.
	if len(parts) != 6 || parts[0] != "" {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: malformed password hash")
	}
	if parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: unsupported hash algorithm %q", parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: malformed password hash version: %w", err)
	}
	if version != argon2.Version {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}

	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: malformed password hash params: %w", err)
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: malformed password hash salt: %w", err)
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: malformed password hash digest: %w", err)
	}
	return m, t, p, salt, hash, nil
}

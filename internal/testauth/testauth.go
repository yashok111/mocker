// Package testauth is the one owner of the password every test server,
// manager and MCP fixture in this tree is configured with, and of a
// PRE-MINTED hash for it.
//
// Why a constant hash and not auth.HashPassword at fixture time: the
// production parameters are argon2id at m=64 MiB, and under the race
// detector one hash costs ~110 ms of CPU (12 ms without) — measured
// 2026-09-03 with a benchmark. internal/admin alone builds ~170 test servers
// and logs into most of them, so hashing at fixture time plus the login's
// verify was ~30 CPU-seconds of every race run, on the package that is the
// suite's critical path. The hash below is argon2id at m=4 MiB, t=1, p=4
// over a fixed salt: sixteen times cheaper to verify, and the PHC string is
// self-describing, so auth.VerifyPassword reads the parameters from it and
// no production code learns anything about tests. The tests that exercise
// hashing ITSELF (internal/auth's password tests) keep calling
// auth.HashPassword — this package is for fixtures that merely need a valid
// credential to log in with.
//
// A leaf: no imports, no tests of its own, nothing for goleak to watch.
package testauth

// Password is the plaintext every fixture logs in with.
const Password = "correct horse battery staple"

// Hash is argon2id(Password) at m=4096 KiB, t=1, p=4, salt "mocker-test-salt",
// PHC-encoded exactly as auth.HashPassword encodes. Re-mint it with
// golang.org/x/crypto/argon2.IDKey and the same encoding if Password ever
// changes; auth.VerifyPassword(Hash, Password) is what proves it still holds.
const Hash = "$argon2id$v=19$m=4096,t=1,p=4$bW9ja2VyLXRlc3Qtc2FsdA$aLBfjFIMWx/V6xMOPpJFfr3s1YoZlf/q+3RKxQ1QJ2I"

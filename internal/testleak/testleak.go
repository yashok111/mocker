// Package testleak wires go.uber.org/goleak into this module's test binaries.
//
// It exists so the ignore list is written down ONCE, with its reasons, instead
// of being copied into every package's TestMain — where the copies drift, and
// where the next person adds a broad ignore to make a red run green without
// anyone seeing that the list grew.
//
// It is test-support only, like internal/testspec: nothing in cmd/ or the
// serving path imports it, so it never reaches the binary.
package testleak

import (
	"testing"

	"go.uber.org/goleak"
)

// options are goroutines the RUNTIME parks for the lifetime of a process, not
// leaks any test could close. Each one is here because it was observed, not
// because it seemed likely:
//
//   - database/sql keeps a connection opener and a connection resetter alive
//     per *sql.DB until that DB is closed, and a test that shares one open
//     database across subtests legitimately still holds it when goleak runs.
//   - modernc.org/sqlite is a pure-Go VFS; its interrupt handler is parked the
//     same way.
//
// Anything NOT on this list is a real finding. Resist adding to it: a leak
// that shows up here is usually a Run/Close protocol that returns before its
// own goroutines do, which is exactly the class this check exists to catch.
var options = []goleak.Option{
	goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	goleak.IgnoreTopFunction("database/sql.(*DB).connectionResetter"),
	goleak.IgnoreAnyFunction("modernc.org/sqlite.(*conn).interrupt"),
}

// VerifyTestMain runs m and then fails the package if any goroutine outlived
// it. Call it as the whole body of a package's TestMain:
//
//	func TestMain(m *testing.M) { testleak.VerifyTestMain(m) }
func VerifyTestMain(m *testing.M) {
	goleak.VerifyTestMain(m, options...)
}

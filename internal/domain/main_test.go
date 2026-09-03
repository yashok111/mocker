package domain

import (
	"testing"

	"github.com/yashok111/mocker/internal/testleak"
)

// TestMain wires goleak the same way every package with tests in this tree
// does (CLAUDE.md, "goleak is in every package with tests") — settings_test.go
// is this package's first test file, so this is its first TestMain too.
func TestMain(m *testing.M) { testleak.VerifyTestMain(m) }

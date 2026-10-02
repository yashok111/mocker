package traffic

import (
	"strings"
	"testing"
)

func TestRedactionPreservesExactNumbers(t *testing.T) {
	out, changed := RedactJSONBody([]byte(`{"id":9007199254740993,"password":"secret"}`))
	if !changed || !strings.Contains(string(out), "9007199254740993") {
		t.Fatalf("numeric identity changed: %s", out)
	}
}

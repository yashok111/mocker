package bundle_test

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
)

// TestDecode_readsV4 was P7a D9's own promise and is now its REFUSAL, and the
// name is kept on purpose so anybody reading `git log` on this file finds the
// reversal rather than a test that quietly changed subject.
//
// P7a read v4 because A16 had shipped an installer the day before, making a
// colleague's v4 checkpoint plausible. A18's owner weighed that against the
// invariant — each version reads exactly the version before it — and chose the
// invariant (docs/A18-endpoint-functions.md D5, where this document's own
// recommendation was the opposite and is recorded as overruled). The operator's
// route for such a document is to re-export it from a build that still reads
// it, and the cost is a named entry in CARVE-OUTS.md.
//
// The fixture is UNCHANGED, which is the point: bumping its literal to 5 would
// have left a green suite that had stopped testing anything about v4 at all,
// and that cheap fix is what acceptance clause 59 exists to refuse.
func TestDecode_readsV4(t *testing.T) {
	raw := []byte(`{
		"mockerBundle": 4,
		"workspace": {"name": "old", "settings": {"basePath": "/api"}},
		"basePath": "/api", "spec": {"hash":"","name":"","inline":null},
		"overrides": [],
		"endpoints": [{"method":"GET","path":"/widgets","overrideOn":true,"routeOff":false,
			"activeStatus":200,"responses":{},"sourceOrder":1,"kind":"http","stream":null}],
		"resources": [], "decisions": [], "entities": null
	}`)
	_, err := bundle.Decode(raw)
	if err == nil {
		t.Fatal("Decode accepted a v4 document; A18 D5 moved minVersion to 5 and it must be refused")
	}
	// BY NAME, not merely refused: an operator meeting this error has to be
	// able to tell "too old" from a malformed document, and the version is the
	// one thing that tells them.
	if !strings.Contains(err.Error(), "mockerBundle 4") {
		t.Fatalf("Decode(v4) = %v, want the refusal to name the version it read", err)
	}
}

// TestEncode_v4RowRoundTripsWithoutAnOperationKey: a row that declares no
// operation must encode to the same bytes it did before P7a — `omitempty`
// is what keeps a v4 document and a v5 document over the same row equal
// except for the version number.
func TestEncode_v4RowRoundTripsWithoutAnOperationKey(t *testing.T) {
	b := bundle.Bundle{
		MockerBundle: bundle.CurrentVersion,
		BasePath:     "/api",
		Endpoints: []bundle.EndpointEntry{{
			Method: "GET", Path: "/widgets", OverrideOn: true, ActiveStatus: 200,
			SourceOrder: 1, Kind: "http", Stream: []byte("null"),
		}},
	}
	b.Workspace.Name = "old"
	b.Workspace.Settings.BasePath = "/api"
	out, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(string(out), `"operation"`) {
		t.Errorf("encoded document carries an operation key for a row that declares none: %s", out)
	}
}

// TestValidate_refusesASchemaOnAnOverrideEntry is P7a D2's second door: the
// admin route refuses `schema` on a spec operation's variant by name, and
// a document restored or imported must not smuggle one into op_overrides.
func TestValidate_refusesASchemaOnAnOverrideEntry(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"mockerBundle": 5,
		"workspace": {"name": "x", "settings": {}},
		"basePath": "", "spec": {"hash":"","name":"","inline":null},
		"overrides": [{"method":"GET","path":"/a","overrideOn":true,"routeOff":false,
			"responses":{"200":{"mode":"generated","schema":{"type":"object"}}}}],
		"endpoints": [], "resources": [], "decisions": [], "entities": null
	}`)
	if _, err := bundle.Decode(raw); err == nil || !strings.Contains(err.Error(), "schema belongs to a custom endpoint") {
		t.Fatalf("Decode accepted a schema on an override entry: err = %v", err)
	}
}

package bundle_test

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
)

// TestDecode_readsV4 is P7a D9's own promise, in the shape the decision
// names: A16 shipped an installer the day before this slice, so a v4
// checkpoint or scenario written by a colleague's build must still decode
// — with a nil Operation and no schema, which is exactly what those rows
// hold. A refusal here would strand that history with no migration path.
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
	b, err := bundle.Decode(raw)
	if err != nil {
		t.Fatalf("Decode a v4 document: %v", err)
	}
	if len(b.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(b.Endpoints))
	}
	if len(b.Endpoints[0].Operation) != 0 {
		t.Errorf("operation = %q, want absent on a v4 document", b.Endpoints[0].Operation)
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

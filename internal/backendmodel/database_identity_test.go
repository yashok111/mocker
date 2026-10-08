package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
)

func TestDatabaseIdentityTypedUnknownAndLegacy(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{`"orders"`, true}, {`{"status":"known","value":"orders"}`, true},
		{`{"status":"unknown","reason":"Configured at deployment"}`, true},
		{`null`, false}, {`""`, false}, {`{"status":"known","value":""}`, false},
		{`{"status":"known","value":null}`, false}, {`{"status":"unknown"}`, false},
		{`{"status":"unknown","reason":""}`, false}, {`{"status":"unknown","reason":"missing","value":"guess"}`, false},
	} {
		t.Run(test.value, func(t *testing.T) {
			raw := jsontext.Value(`{"dialect":"postgresql","databaseName":` + test.value + `,"qualifiedName":"logical-store","nativeDefinition":null,"analysisStatus":"complete","gaps":[]}`)
			facet, err := decodeRelationalFacetMode("datastore", raw, true, false)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
			if err == nil {
				encoded, err := json.Marshal(facet)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]jsontext.Value
				if err := json.Unmarshal(encoded, &fields); err != nil {
					t.Fatal(err)
				}
				original, _ := canonicalJSON(jsontext.Value(test.value))
				actual, _ := canonicalJSON(fields["databaseName"])
				if string(original) != string(actual) {
					t.Fatal("wire representation changed")
				}
			}
		})
	}
}

func TestDatabaseIdentityUnknownSurvivesCommitAndSource6(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "unknown-database")
	input := relationalFixtureInput(t, p, "postgresql", "v1")
	input.Profile = EventsProfile
	input.Manifest.Provider.Profiles = []string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, EventsProfile}
	session, err := r.BeginImport(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	commands := relationalFixture(t, p, session, "postgresql", "v1")
	expected := jsontext.Value(`{"status":"unknown","reason":"Configured at deployment"}`)
	for _, cmd := range commands {
		if cmd.Node == nil || cmd.Node.Kind != "datastore" {
			continue
		}
		var descriptor struct {
			Facets map[string]map[string]jsontext.Value `json:"facets"`
		}
		if err := json.Unmarshal(cmd.Node.Attributes["relational"], &descriptor); err != nil {
			t.Fatal(err)
		}
		for _, facet := range descriptor.Facets {
			facet["databaseName"] = expected
		}
		cmd.Node.Attributes["relational"] = relationalRaw(t, descriptor)
	}
	preview, ids := stageRelational(t, r, p, session, commands, "unknown")
	base, err := commitFixture(t, r, p, session, preview, "unknown-commit")
	if err != nil {
		t.Fatal(err)
	}
	checkNode := func(revision string, sourceAdmission bool) {
		t.Helper()
		n, err := r.Node(t.Context(), p.ID, revision, ids["database:orders"])
		if err != nil {
			t.Fatal(err)
		}
		facets, _, err := relationalFacetObject(n.Kind, n.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range facets {
			facet, err := decodeRelationalFacetMode("datastore", raw, true, sourceAdmission)
			if err != nil {
				t.Fatal(err)
			}
			actualHash, _ := requestDigest(facet.DatabaseName)
			expectedHash, _ := requestDigest(expected)
			if actualHash != expectedHash {
				t.Fatalf("unknown identity changed: %s", facet.DatabaseName)
			}
		}
	}
	checkNode(base.Revision.ID, true)
	next := source6Input(t, &base.Project)
	next.Manifest.RepositoryName = "extra-source"
	next.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
	_, composed := commitSource6Fixture(t, r, &base.Project, next)
	// Composed facets carry assertion provenance separately from their values.
	checkNode(composed.Revision.ID, false)
}

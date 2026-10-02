package backendmodel

import (
	"slices"
	"testing"
)

func runtimeProfileInput(in BeginImportInput, extension bool) BeginImportInput {
	in.Profile = "runtime-flow-v1"
	in.Manifest.Provider.Profiles = []string{GraphProfile, RelationalProfile, "runtime-flow-v1"}
	if in.GraphScope != nil {
		in.GraphScope.Profile = in.Profile
	}
	if extension {
		in.ProfileExtension = &ImportProfileExtension{FromProfile: RelationalProfile, ToProfile: in.Profile}
	}
	return in
}

func TestRuntimeProfileInitialExactDeclarations(t *testing.T) {
	for _, profiles := range [][]string{{GraphProfile, RelationalProfile}, {GraphProfile, RuntimeProfile}, {RelationalProfile, RuntimeProfile}, {GraphProfile, RelationalProfile, RuntimeProfile, RuntimeProfile}, {GraphProfile, RelationalProfile, RuntimeProfile, "foreign"}} {
		r, _ := testRepo(t)
		p := createProject(t, r, "create")
		in := runtimeProfileInput(firstImportFixture(p), false)
		in.Manifest.Provider.Profiles = profiles
		_, err := r.BeginImport(t.Context(), p.ID, in)
		assertFault(t, err, "backend_incompatible_provider")
	}
}

func TestRuntimeProfileInitialAndExtension(t *testing.T) {
	for _, extension := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "explicit source2 extension"}[extension], func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			in := runtimeProfileInput(firstImportFixture(p), false)
			if extension {
				s, err := r.BeginImport(t.Context(), p.ID, relationalInput(firstImportFixture(p), false))
				if err != nil {
					t.Fatal(err)
				}
				v := previewFixture(t, r, p, s)
				out, err := commitFixture(t, r, p, s, v, "base")
				if err != nil {
					t.Fatal(err)
				}
				p = &out.Project
				in = runtimeProfileInput(repeatInput(p, s.RepositoryID), true)
			}
			in.IdempotencyKey = "runtime-begin"
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			v := previewFixture(t, r, p, s)
			out, err := commitFixture(t, r, p, s, v, "runtime-commit")
			if err != nil || out.Revision.SchemaVersion != "3" {
				t.Fatalf("runtime commit: %+v %v", out, err)
			}
			g, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
			if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Ownership.Profile != GraphProfile {
				t.Fatalf("foundation ownership changed: %+v %v", g, err)
			}
		})
	}
	if !slices.Equal(SupportedModelSchemaVersions(), []string{"1", "2", "3", "4"}) {
		t.Fatal("schema3 missing")
	}
	if slices.Contains(SupportedNodeKindsForProfile(RelationalProfile), "flow") || !slices.Contains(SupportedNodeKindsForProfile("runtime-flow-v1"), "flow") {
		t.Fatal("runtime kinds leaked or missing")
	}
}

func TestRuntimeProfileRejectsImplicitDowngradeAndProviderChange(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		source1    bool
		change     func(*BeginImportInput)
	}{
		{"missing extension", "backend_unsupported_scope", false, func(in *BeginImportInput) { in.ProfileExtension = nil }},
		{"direct source1", "backend_unsupported_scope", true, nil},
		{"wrong extension", "backend_unsupported_scope", false, func(in *BeginImportInput) { in.ProfileExtension.FromProfile = GraphProfile }},
		{"provider name", "backend_incompatible_provider", false, func(in *BeginImportInput) { in.Manifest.Provider.Name = "foreign" }},
		{"provider version", "backend_incompatible_provider", false, func(in *BeginImportInput) { in.Manifest.Provider.Version = "foreign" }},
		{"provider namespace", "backend_incompatible_provider", false, func(in *BeginImportInput) { in.Manifest.Provider.Namespace = "foreign" }},
		{"provider method", "backend_incompatible_provider", false, func(in *BeginImportInput) { in.Manifest.Provider.Method = "manual" }},
		{"extra profile", "backend_incompatible_provider", false, func(in *BeginImportInput) {
			in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, "foreign")
		}},
		{"foreign repository", "backend_unsupported_scope", false, func(in *BeginImportInput) { in.RepositoryID = new("11111111-1111-4111-8111-111111111111") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, p, old, _ := committedBase(t)
			if !tc.source1 {
				p, old = extensionFixture(t, r, p, old)
			}
			in := runtimeProfileInput(repeatInput(p, old.RepositoryID), true)
			in.IdempotencyKey = "invalid-runtime"
			if tc.change != nil {
				tc.change(&in)
			}
			before := profilePublishedState(t, r, p)
			_, err := r.BeginImport(t.Context(), p.ID, in)
			assertFault(t, err, tc.code)
			if profilePublishedState(t, r, p) != before {
				t.Fatal("refusal mutated source state")
			}
		})
	}
}

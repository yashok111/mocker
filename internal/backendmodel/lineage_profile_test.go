package backendmodel

import (
	"slices"
	"testing"
)

func lineageProfileInput(in BeginImportInput, extension bool) BeginImportInput {
	in = runtimeProfileInput(in, false)
	in.Profile = "field-lineage-v1"
	in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, in.Profile)
	if in.GraphScope != nil {
		in.GraphScope.Profile = in.Profile
	}
	if extension {
		in.ProfileExtension = &ImportProfileExtension{FromProfile: RuntimeProfile, ToProfile: in.Profile}
	}
	return in
}
func TestLineageProfileTransitions(t *testing.T) {
	for _, baseProfile := range []string{"", GraphProfile, RelationalProfile, RuntimeProfile, "field-lineage-v1"} {
		name := baseProfile
		if name == "" {
			name = "initial"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			var old *ImportSession
			if baseProfile != "" {
				in := firstImportFixture(p)
				switch baseProfile {
				case RelationalProfile:
					in = relationalInput(in, false)
				case RuntimeProfile:
					in = runtimeProfileInput(in, false)
				case "field-lineage-v1":
					in = lineageProfileInput(in, false)
				}
				var err error
				old, err = r.BeginImport(t.Context(), p.ID, in)
				if err != nil {
					t.Fatal(err)
				}
				v := previewFixture(t, r, p, old)
				out, err := commitFixture(t, r, p, old, v, "base")
				if err != nil {
					t.Fatal(err)
				}
				p = &out.Project
			}
			in := lineageProfileInput(firstImportFixture(p), false)
			if old != nil {
				in = lineageProfileInput(repeatInput(p, old.RepositoryID), baseProfile != "field-lineage-v1")
			}
			in.IdempotencyKey = "lineage-begin"
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if baseProfile == GraphProfile || baseProfile == RelationalProfile {
				assertFault(t, err, "backend_unsupported_scope")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			v := previewFixture(t, r, p, s)
			out, err := commitFixture(t, r, p, s, v, "lineage-commit")
			if err != nil {
				t.Fatal(err)
			}
			if out.Revision.SchemaVersion != "4" {
				t.Fatalf("schema=%s", out.Revision.SchemaVersion)
			}
			g, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
			if err != nil || g.Nodes[0].Ownership.Profile != GraphProfile {
				t.Fatalf("ownership drift: %+v %v", g, err)
			}
			down := runtimeProfileInput(repeatInput(&out.Project, s.RepositoryID), false)
			down.IdempotencyKey = "down"
			_, err = r.BeginImport(t.Context(), p.ID, down)
			assertFault(t, err, "backend_unsupported_scope")
		})
	}
	if !slices.Equal(SupportedModelSchemaVersions(), []string{"1", "2", "3", "4", "5", "6"}) {
		t.Fatal("missing schema4")
	}
}
func TestLineageProfileExactDeclaration(t *testing.T) {
	for _, profiles := range [][]string{{GraphProfile, RelationalProfile, RuntimeProfile}, {GraphProfile, RelationalProfile, RuntimeProfile, "field-lineage-v1", "field-lineage-v1"}, {GraphProfile, RelationalProfile, RuntimeProfile, "field-lineage-v1", "foreign"}} {
		r, _ := testRepo(t)
		p := createProject(t, r, "create")
		in := lineageProfileInput(firstImportFixture(p), false)
		in.Manifest.Provider.Profiles = profiles
		_, err := r.BeginImport(t.Context(), p.ID, in)
		assertFault(t, err, "backend_incompatible_provider")
	}
}

func TestLineageExtensionRejectsNonexactProfilesAndImplicitUpgrade(t *testing.T) {
	for _, mode := range []string{"duplicate profile", "foreign profile", "missing extension", "provider changed"} {
		t.Run(mode, func(t *testing.T) {
			r, out, old, _ := runtimeCommitted(t)
			in := lineageProfileInput(runtimeRepeat(&out.Project, old.RepositoryID), true)
			in.IdempotencyKey = "bad-extension"
			code := "backend_incompatible_provider"
			switch mode {
			case "duplicate profile":
				in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, LineageProfile)
			case "foreign profile":
				in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, "foreign")
			case "missing extension":
				in.ProfileExtension = nil
				code = "backend_unsupported_scope"
			case "provider changed":
				in.Manifest.Provider.Version = "different"
			}
			_, err := r.BeginImport(t.Context(), out.Project.ID, in)
			assertFault(t, err, code)
		})
	}
}

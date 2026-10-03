package backendmodel

import "testing"

func eventsProfileInput(in BeginImportInput, extension bool) BeginImportInput {
	in = lineageProfileInput(in, false)
	in.Profile = "events-service-v1"
	in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, in.Profile)
	if in.GraphScope != nil {
		in.GraphScope.Profile = in.Profile
	}
	if extension {
		in.ProfileExtension = &ImportProfileExtension{FromProfile: LineageProfile, ToProfile: in.Profile}
	}
	return in
}
func TestEventsProfileTransitions(t *testing.T) {
	for _, baseProfile := range []string{"", GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, "events-service-v1"} {
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
				case LineageProfile:
					in = lineageProfileInput(in, false)
				case "events-service-v1":
					in = eventsProfileInput(in, false)
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
			in := eventsProfileInput(firstImportFixture(p), false)
			if old != nil {
				in = eventsProfileInput(repeatInput(p, old.RepositoryID), baseProfile != "events-service-v1")
			}
			in.IdempotencyKey = "lineage-begin"
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if baseProfile == GraphProfile || baseProfile == RelationalProfile || baseProfile == RuntimeProfile {
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
			if out.Revision.SchemaVersion != "5" {
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
}
func TestEventsProfileExactDeclarationAndProvider(t *testing.T) {
	for _, mode := range []string{"initial missing", "initial duplicate", "initial foreign", "extension missing", "provider changed", "downgrade lineage"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			in := eventsProfileInput(firstImportFixture(p), false)
			code := "backend_incompatible_provider"
			if mode == "extension missing" || mode == "provider changed" {
				base, err := r.BeginImport(t.Context(), p.ID, lineageProfileInput(firstImportFixture(p), false))
				if err != nil {
					t.Fatal(err)
				}
				v := previewFixture(t, r, p, base)
				out, err := commitFixture(t, r, p, base, v, "base")
				if err != nil {
					t.Fatal(err)
				}
				p = &out.Project
				in = eventsProfileInput(repeatInput(p, base.RepositoryID), true)
				if mode == "extension missing" {
					in.ProfileExtension = nil
					code = "backend_unsupported_scope"
				} else {
					in.Manifest.Provider.Version = "changed"
				}
			}
			switch mode {
			case "initial missing":
				in.Manifest.Provider.Profiles = in.Manifest.Provider.Profiles[:4]
			case "initial duplicate":
				in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, EventsProfile)
			case "initial foreign":
				in.Manifest.Provider.Profiles = append(in.Manifest.Provider.Profiles, "foreign")
			case "downgrade lineage":
				r, out, base, _ := eventsCommitted(t)
				down := lineageProfileInput(repeatInput(&out.Project, base.RepositoryID), false)
				down.IdempotencyKey = "events-down"
				_, err := r.BeginImport(t.Context(), out.Project.ID, down)
				assertFault(t, err, "backend_unsupported_scope")
				return
			}
			in.IdempotencyKey = "invalid-events"
			_, err := r.BeginImport(t.Context(), p.ID, in)
			assertFault(t, err, code)
		})
	}
}

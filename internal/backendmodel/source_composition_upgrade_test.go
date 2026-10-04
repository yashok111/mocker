package backendmodel

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestSource6LegacyProofBasis(t *testing.T) {
	for _, pointer := range []string{"/id", "/externalKey", "/evidenceIds/0", "/ownership/providerNamespace", "/name", ""} {
		name := pointer
		if name == "" {
			name = "record-proof"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "legacy")
			old, err := r.BeginImport(t.Context(), p.ID, eventsProfileInput(firstImportFixture(p), false))
			if err != nil {
				t.Fatal(err)
			}
			commands := fixtureCommands(old)
			if pointer != "" {
				commands[1].Evidence.PropertyPath = new(pointer)
			}
			b := sendCommands(t, r, p, old, 1, "legacy", commands...)
			base := commitStaged(t, r, p, old, b.AcceptedVersion, "legacy-commit")
			before := immutableBytes(t, r)
			in := source6Input(t, &base.Project)
			in.IdempotencyKey = "upgrade"
			in.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
			in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: old.RepositoryID}
			in.Manifest.Provider.Namespace = "provider-b"
			_, out := commitSource6Fixture(t, r, &base.Project, in)
			graph, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(graph.LegacyProofBases) != 1 {
				t.Fatalf("bases=%+v", graph.LegacyProofBases)
			}
			basis := graph.LegacyProofBases[0]
			derived, err := deriveLegacyProofBasis(t.Context(), r.db.R, p.ID, base.Revision.ID, basis.RecordType, basis.RecordID, basis.EvidenceID)
			if err != nil || derived.BasisHash != basis.BasisHash {
				t.Fatalf("standalone basis differs: %+v %v", derived, err)
			}
			support := "historical_metadata"
			if pointer == "/name" {
				support = "legacy_semantic"
			}
			if pointer == "" {
				support = "legacy_record"
			}
			if basis.Support != support {
				t.Fatalf("support=%s", basis.Support)
			}
			raw, err := loadLegacyProofBasis(t.Context(), r.db.R, basis)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw.EvidenceDocument) != string(graph.RawEvidence[basis.EvidenceID]) {
				t.Fatal("legacy proof bytes changed")
			}
			if support == "historical_metadata" && !strings.Contains(strings.Join(out.Revision.Coverage.Gaps, " "), "legacy_metadata_only") {
				t.Fatal("metadata proof became semantic support")
			}
			if support == "historical_metadata" {
				for _, f := range graph.Currentness {
					if f.RecordID == basis.RecordID {
						if f.Own.Status != "stale" || !strings.Contains(strings.Join(f.Own.Reasons, " "), "legacy_metadata_only") {
							t.Fatalf("metadata-only claim presented semantic currentness: %+v", f)
						}
						for _, field := range f.Fields {
							if field.Own.Status == "current" {
								t.Fatalf("metadata-only field presented current: %+v", field)
							}
						}
					}
				}
			}
			after := immutableBytes(t, r)
			for key, value := range before {
				if after[key] != value {
					t.Fatalf("legacy row rewritten: %s", key)
				}
			}
		})
	}
}

func TestSource6RetainedPartitionExtension(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "legacy")
	old, err := r.BeginImport(t.Context(), p.ID, eventsProfileInput(firstImportFixture(p), false))
	if err != nil {
		t.Fatal(err)
	}
	base := commitStaged(t, r, p, old, putFixture(t, r, p, old).AcceptedVersion, "legacy-commit")
	in := source6Input(t, &base.Project)
	in.IdempotencyKey = "upgrade"
	in.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
	in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: old.RepositoryID}
	in.Manifest.Provider.Namespace = "provider-b"
	_, out := commitSource6Fixture(t, r, &base.Project, in)
	next := source6Input(t, &out.Project)
	next.IdempotencyKey = "extend-a"
	next.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: old.RepositoryID, ProviderNamespace: old.Manifest.Provider.Namespace}
	next.Manifest = old.Manifest
	next.Manifest.Provider.Profiles = append(next.Manifest.Provider.Profiles, ComposedProfile)
	if _, err := r.BeginImport(t.Context(), p.ID, next); err == nil {
		t.Fatal("retained five-profile provider extended implicitly")
	}
	next.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
	s, latest := commitSource6Fixture(t, r, &out.Project, next)
	replay, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil || replay.ID == s.ID {
		t.Fatalf("old begin replay=%+v %v", replay, err)
	}
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, latest.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.SourceVector.Partitions) != 2 {
		t.Fatal("partition removed")
	}
	next.ExpectedVersion = latest.Project.Version
	next.BaseRevisionID = latest.Revision.ID
	next.IdempotencyKey = "repeat-extension"
	if _, err := r.BeginImport(t.Context(), p.ID, next); err == nil {
		t.Fatal("repeated extension accepted")
	}
	next.ProfileExtension = nil
	next.IdempotencyKey = "ordinary"
	if _, err := r.BeginImport(t.Context(), p.ID, next); err != nil {
		t.Fatal(err)
	}
}

func TestSource6FreshProofCannotClaimLegacyBasis(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "fresh")
	s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)
	commands[1].Evidence.PropertyPath = new("/id")
	b := sendCommands(t, r, p, s, 1, "fresh", commands...)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err == nil && v.State == "ready" {
		t.Fatal("fresh metadata proof admitted")
	}
	var parsed ImportCommand
	if err := json.Unmarshal([]byte(`{"op":"upsert_evidence","evidence":{"legacyProofBasis":{}}}`), &parsed, json.RejectUnknownMembers(true)); err == nil {
		t.Fatal("client legacy basis admitted")
	}
}

func TestSource6BootstrapPreservesPartialForeignKeyFacet(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "partial-fk")
	in := eventsProfileInput(relationalFixtureInput(t, p, "postgresql", "v1"), false)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := relationalFixture(t, p, s, "postgresql", "v1")
	column := relationalCommand(commands, "column:orders:user_id").Node
	facets, _, err := relationalFacetObject(column.Kind, column.Attributes)
	if err != nil {
		t.Fatal(err)
	}
	delete(facets, "sql")
	column.Attributes = replaceRelationalFacets(column.Kind, column.Attributes, facets)
	relationalCommand(commands, "proof:v1:column:orders:user_id:sql").Evidence.PropertyPath = new("/kind")
	preview, _ := stageRelational(t, r, p, s, commands, "partial")
	if preview.State != "ready" {
		t.Fatalf("valid legacy partial FK rejected: %+v", preview)
	}
	base, err := commitFixture(t, r, p, s, preview, "base")
	if err != nil {
		t.Fatal(err)
	}
	next := source6Input(t, &base.Project)
	next.Manifest.RepositoryName = "additional-repository"
	next.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
	next.IdempotencyKey = "bootstrap"
	_, out := commitSource6Fixture(t, r, &base.Project, next)
	if out.Revision.SchemaVersion != ComposedSchemaVersion {
		t.Fatal("bootstrap failed")
	}
}

func TestSource6LegacyBranchBytesWithOptionalParent(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "optional-parent")
	s, err := r.BeginImport(t.Context(), p.ID, eventsProfileInput(firstImportFixture(p), false))
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)
	service := *commands[0].Node
	service.ExternalKey = "service"
	service.Kind = "service"
	service.EvidenceKeys = []string{"service-proof"}
	proof := *commands[1].Evidence
	proof.ExternalKey = "service-proof"
	proof.SubjectKey = "service"
	edgeProof := proof
	edgeProof.ExternalKey = "contains-proof"
	edgeProof.SubjectType = "edge"
	edgeProof.SubjectKey = "contains"
	commands = append(commands, ImportCommand{Op: "upsert_node", Node: &service}, ImportCommand{Op: "upsert_evidence", Evidence: &proof}, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "contains", Kind: "contains", FromKey: "service", ToKey: "handler", Attributes: service.Attributes, EvidenceKeys: []string{"contains-proof"}}}, ImportCommand{Op: "upsert_evidence", Evidence: &edgeProof})
	b := sendCommands(t, r, p, s, 1, "legacy", commands...)
	base := commitStaged(t, r, p, s, b.AcceptedVersion, "base")
	before := immutableBytes(t, r)
	next := source6Input(t, &base.Project)
	next.Manifest.RepositoryName = "other-repository"
	next.IdempotencyKey = "source6"
	next.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
	commitSource6Fixture(t, r, &base.Project, next)
	after := immutableBytes(t, r)
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("source6 changed legacy bytes: %s", key)
		}
	}
}

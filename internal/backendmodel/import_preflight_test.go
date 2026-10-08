package backendmodel

import (
	"database/sql"
	"testing"
)

func TestImportPreflightSeparatesStorageAndConsumerAdmission(t *testing.T) {
	t.Parallel()
	plan, err := PlanImport(ImportPreflightInput{Profile: EventsProfile, Counts: ImportCardinalities{Nodes: 25228, Edges: 100001, Evidence: 106398}, SemanticBytes: new(int64(140 << 20)), Surfaces: []string{"graph", "events", "data_access", "architecture"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Storage.Status != "within_limits" {
		t.Fatalf("storage admission: %+v", plan.Storage)
	}
	bySurface := map[string]ImportConsumerAvailability{}
	for _, item := range plan.Consumers {
		bySurface[item.Surface] = item
	}
	if bySurface["events"].Status != "blocked" || bySurface["events"].Reasons[0] != "edge_limit" || bySurface["events"].Admission["maxTotalEdges"] != 100000 {
		t.Fatalf("Events preflight missed refusal: %+v", bySurface["events"])
	}
	if bySurface["data_access"].Status != "scope_check_required" || bySurface["data_access"].Traversal["maxVisitedStates"] != 5000 {
		t.Fatal("data access falsely promised completeness")
	}
	if bySurface["architecture"].Concurrency["maxBuilders"] != 1 {
		t.Fatal("missing architecture admission budget")
	}
}

func TestImportPreflightAdmitsRecordedEventsCardinality(t *testing.T) {
	t.Parallel()
	for _, edges := range []int64{32348, 100000} {
		plan, err := PlanImport(ImportPreflightInput{Profile: EventsProfile, Counts: ImportCardinalities{Nodes: 25228, Edges: edges, Evidence: 106398}, SemanticBytes: new(int64(140 << 20)), Surfaces: []string{"events"}})
		if err != nil {
			t.Fatal(err)
		}
		if consumer := plan.Consumers[0]; consumer.Status != "scope_check_required" || consumer.Admission["maxTotalEdges"] != 100000 || len(consumer.Reasons) != 0 {
			t.Fatalf("%d edges refused: %+v", edges, consumer)
		}
	}
}

func TestImportPreflightUnknownBytesAndProfile(t *testing.T) {
	t.Parallel()
	plan, err := PlanImport(ImportPreflightInput{Profile: GraphProfile, Counts: ImportCardinalities{Nodes: 10, Edges: 20, Evidence: 30}, Surfaces: []string{"events"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Storage.Status != "byte_estimate_required" || plan.Consumers[0].Status != "unsupported_profile" {
		t.Fatal("unknown storage/profile presented as admitted")
	}
	_, err = PlanImport(ImportPreflightInput{Profile: EventsProfile, Counts: ImportCardinalities{Edges: -1}, Surfaces: []string{"events"}})
	if err == nil {
		t.Fatal("negative cardinality accepted")
	}
}

func TestImportReadyPreviewIncludesConsumerPreflight(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "preflight")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Preflight == nil || preview.Preflight.Basis != "candidate" || preview.Preflight.Counts.Nodes != preview.Summary.Nodes || preview.Preflight.SemanticBytes == nil {
		t.Fatal("READY preview lacks candidate-bound availability")
	}
}

func TestSource6PreflightUsesRetainedGraphAdmissionBytes(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "preflight-source6")
	_, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	in := source6Input(t, &base.Project)
	in.IdempotencyKey, in.Manifest.RepositoryName = "second-source", "other"
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	batch := putFixture(t, r, &base.Project, s)
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	var expected int64
	err = r.db.Read(t.Context(), func(tx *sql.Tx) error {
		session, err := loadSession(t.Context(), tx, p.ID, s.ID)
		if err != nil {
			return err
		}
		graph, _, err := prepareGraph(t.Context(), tx, session)
		if err != nil {
			return err
		}
		if len(graph.Composed.Source.SourceVector.Partitions) != 2 {
			t.Fatal("fixture lacks retained partition")
		}
		content, err := source6ContextJSON(graph.Composed.Source)
		if err != nil {
			return err
		}
		artifacts, err := source6ArtifactBytes(graph)
		if err != nil {
			return err
		}
		incremental, err := incrementalRevisionBytes(session, graph.Composed)
		if err != nil {
			return err
		}
		expected = int64(len(content) + graph.Composed.Source.legacyBasisBytes + artifacts + incremental)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := preview.Preflight.SemanticBytes; got == nil || *got != expected {
		t.Fatalf("source6 semanticBytes=%v, want retained graph admission bytes %d", got, expected)
	}
}

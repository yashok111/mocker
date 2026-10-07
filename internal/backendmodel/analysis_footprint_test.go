package backendmodel

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/yashok111/mocker/internal/testkit"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"
)

func TestAnalysisFootprintSharedDocumentsAndLease(t *testing.T) {
	r, base, _ := changeFixture(t)
	one, err := r.EffectiveGraphInputFootprint(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{RevisionID: base.Revision.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pair.TotalBytes != one.TotalBytes || pair.TotalBytes <= 0 {
		t.Fatalf("shared documents counted twice: %d %d", pair.TotalBytes, one.TotalBytes)
	}
	ctx, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, pair)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ResolveEffectiveGraph(ctx, base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID}); err != nil {
		t.Fatal(err)
	}
	if r.db.TransientBytes("backend:"+base.Project.ID) == 0 {
		t.Fatal("preparation reservation not retained")
	}
	lease.Release()
	lease.Release()
	if r.db.TransientBytes("backend:"+base.Project.ID) != 0 {
		t.Fatal("reservation leak")
	}
	if _, _, err = r.ReserveAnalysisInput(t.Context(), base.Project.ID, AnalysisInputFootprint{TotalBytes: 1}); err == nil {
		t.Fatal("forged footprint accepted")
	}
}

func TestAnalysisFootprintRejectsCombinedPairBeforeDecode(t *testing.T) {
	r, base, _ := changeFixture(t)
	second := uuid.NewV7().String()
	// The base revision's graph manifest is sealed under Store27 (48dce80,
	// B6.3), so both revisions are seeded in the Store26 fixture shape and
	// published by the production migration. That migration checks the query
	// projection against the payload's id/kind/name, so the padding records
	// carry exactly those identity fields (kind and name empty, matching the
	// column defaults) and nothing else a node needs.
	err := testkit.EditLegacyBackendFixture(t.Context(), r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_revisions(id,project_id,document) SELECT ?,project_id,json_set(document,'$.id',?) FROM backend_revisions WHERE id=?`, second, second, base.Revision.ID); err != nil {
			return err
		}
		for _, rid := range []string{base.Revision.ID, second} {
			// Invalid domain records are deliberately retained: a graph decoder would
			// fail, while admission must reject the combined bytes before reaching it.
			id := uuid.NewV7().String()
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES(?,?,'node',?,json_object('id',?,'kind','','name','','padding',printf('%*s',?,'x')))`, base.Project.ID, rid, id, id, 130<<20); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, rid := range []string{base.Revision.ID, second} {
		f, err := r.EffectiveGraphInputFootprint(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: rid})
		if err != nil || f.TotalBytes >= MaxRevisionBytes {
			t.Fatalf("single side admission: %d %v", f.TotalBytes, err)
		}
	}
	_, err = r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{RevisionID: second}, nil)
	assertFault(t, err, "backend_analysis_input_limit")
	if r.db.TransientBytes("backend:"+base.Project.ID) != 0 {
		t.Fatal("rejected pair retained memory")
	}
}
func TestAnalysisFootprintLeaseRejectsOtherTargetReleasedAndTampering(t *testing.T) {
	r, base, d := changeFixture(t)
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{RevisionID: base.Revision.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := f
	changed.Documents = slices.Clone(f.Documents)
	changed.Documents[0].Bytes--
	changed.TotalBytes--
	if _, _, err = r.ReserveAnalysisInput(t.Context(), base.Project.ID, changed); err == nil {
		t.Fatal("modified server footprint accepted")
	}
	ctx, l, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if _, err = r.ResolveEffectiveGraph(ctx, base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}}); err == nil {
		t.Fatal("unadmitted target loaded")
	}
	l.Release()
	if _, err = r.ResolveEffectiveGraph(ctx, base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID}); err == nil {
		t.Fatal("released reservation reused")
	}
}
func TestAnalysisFootprintReservationConcurrentAndFailureRelease(t *testing.T) {
	r, base, d := changeFixture(t)
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			ctx, l, e := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
			if e != nil {
				t.Error(e)
				return
			}
			defer l.Release()
			_, e = r.ResolveEffectiveGraph(ctx, base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}})
			if e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if r.db.TransientBytes("backend:"+base.Project.ID) != 0 {
		t.Fatal("concurrent preparation leaked reservation")
	}
	in := PreviewChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{changeCommand(t, "rename", fmt.Sprintf(`"recordType":"node","id":%q,"name":"missing"`, uuid.NewV7().String()))}}
	if _, err = r.FreezeChangePreview(t.Context(), base.Project.ID, d.Proposal.ID, in, strings.Repeat("a", 64)); err == nil {
		t.Fatal("invalid freeze accepted")
	}
	if r.db.TransientBytes("backend:"+base.Project.ID) != 0 {
		t.Fatal("failed preparation leaked reservation")
	}
}
func TestAnalysisFootprintOutstandingReservationCannotBeBypassed(t *testing.T) {
	r, base, _ := changeFixture(t)
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{RevisionID: base.Revision.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	held, ok := r.db.ReserveTransient("backend:"+base.Project.ID, MaxProjectStagingBytes, MaxProjectStagingBytes)
	if !ok {
		t.Fatal("fixture reservation failed")
	}
	defer held.Release()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
			if err == nil {
				lease.Release()
				t.Error("concurrent preparation bypassed shared memory cap")
			}
		})
	}
	wg.Wait()
	held.Release()
	_, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if r.db.TransientBytes("backend:"+base.Project.ID) != 0 {
		t.Fatal("reservation release did not restore admission capacity")
	}
}

func TestAnalysisFootprintSavedTargetMatrix(t *testing.T) {
	for _, version := range []string{"1", "2", "3", "4", "5", "6"} {
		t.Run(version, func(t *testing.T) {
			var r *Repo
			var base *ImportCommitResult
			switch version {
			case "1":
				r, _ = testRepo(t)
				p := createProject(t, r, "one")
				s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
				if err != nil {
					t.Fatal(err)
				}
				putFixture(t, r, p, s)
				v := previewFixture(t, r, p, s)
				base, err = commitFixture(t, r, p, s, v, "one")
				if err != nil {
					t.Fatal(err)
				}
			case "2":
				r, _ = testRepo(t)
				base, _ = commitRelationalFixture(t, r, "postgresql", "v1")
			case "3":
				r, _ = testRepo(t)
				base, _ = runtimeRelationalFixture(t, r, "postgresql")
			case "4":
				r, base, _, _ = lineageOrdersCommitted(t)
			case "5":
				r, base, _ = effectiveFiveRelationalFixture(t)
			case "6":
				r, base, _ = changeFixture(t)
			}
			if base.Revision.SchemaVersion != version {
				t.Fatalf("fixture schema %s wanted %s", base.Revision.SchemaVersion, version)
			}
			target := BackendReadTarget{RevisionID: base.Revision.ID}
			f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, target, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, l, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Release()
			graph, err := r.ResolveEffectiveGraph(ctx, base.Project.ID, target)
			if err != nil {
				t.Fatal(err)
			}
			if len(graph.State.Nodes) > 0 {
				if _, err = EffectiveRecordAnalysisProof(graph, ChangeRecordRef{RecordType: "node", ID: graph.State.Nodes[0].ID}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	r, base, ids := effectiveFiveRelationalFixture(t)
	legacy, err := r.CreateProposal(t.Context(), base.Project.ID, proposalCreateInput(base, ids, "legacy-footprint"))
	if err != nil {
		t.Fatal(err)
	}
	full, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Full footprint", BaseRevisionID: base.Revision.ID, IdempotencyKey: "full-footprint"})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []BackendReadTarget{{Proposal: &ProposalReadTarget{ProposalID: legacy.Proposal.ID, ProposalRevisionID: legacy.Revision.ID}}, {ChangeProposal: &ProposalReadTarget{ProposalID: full.Proposal.ID, ProposalRevisionID: full.Revision.ID}}} {
		f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, l, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.ResolveEffectiveGraph(ctx, base.Project.ID, target); err != nil {
			l.Release()
			t.Fatal(err)
		}
		l.Release()
	}
	if _, err = r.EffectiveGraphInputFootprint(t.Context(), base.Project.ID, BackendReadTarget{ImportCandidate: &ImportCandidateReadTarget{}}); err == nil {
		t.Fatal("staging target admitted")
	}
}
func TestAnalysisFootprintPreviewOnlyHistoricalTestSource(t *testing.T) {
	r, base, _ := changeFixture(t)
	source, err := loadSourceGraph(t.Context(), r.db.R, base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Get(t.Context(), base.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := source6Input(t, p)
	secondInput.IdempotencyKey = "historical-second"
	secondInput.Manifest.RepositoryName = "other-source"
	_, next := commitSource6Fixture(t, r, p, secondInput)
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Historical test", BaseRevisionID: next.Revision.ID, IdempotencyKey: "historical-test"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := source.State.Sources[0]
	file := snapshot.Files[0]
	c := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "old-test", "kind": "test_attachment", "required": true, "description": "Exact old source test", "targetIds": []string{source.State.Nodes[0].ID}, "attachment": map[string]any{"kind": "source", "revisionId": base.Revision.ID, "repositoryId": snapshot.RepositoryID, "snapshotId": snapshot.ID, "file": file.Path, "contentHash": file.ContentHash, "startLine": int64(1), "endLine": int64(1)}}}})
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}}
	preview := &AnalysisCommandPreviewInput{ChangeProposal: ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}, Preview: in}
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, next.Revision.ID, BackendReadTarget{}, preview)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(f.Documents, func(v AnalysisDocumentBytes) bool { return v.Key == "source:"+base.Revision.ID }) {
		t.Fatal("preview-only historical source missing before decoding")
	}
}
func TestAnalysisFootprintSource6LegacyBasisRetainsRawSourceBytes(t *testing.T) {
	service, old, _, _ := artifactServiceFixture(t)
	base := source6ArtifactBaseline(t, service.repo, old)
	source, err := service.repo.ResolveSourceGraph(t.Context(), base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := service.repo.EffectiveGraphInputFootprint(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, basis := range source.LegacyProofBases {
		if !slices.ContainsFunc(f.Documents, func(d AnalysisDocumentBytes) bool { return d.Key == "source:"+basis.SourceRevisionID }) {
			t.Fatalf("legacy raw basis source %s missing from footprint", basis.SourceRevisionID)
		}
	}
}
func TestAnalysisFootprintCombinedClaimsBasisAndNewOwnerReachCap(t *testing.T) {
	service, old, ids, scenario := artifactServiceFixture(t)
	r := service.repo
	base := source6ArtifactBaseline(t, r, old)
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Pair inventory", BaseRevisionID: base.Revision.ID, IdempotencyKey: "pair-inventory"})
	if err != nil {
		t.Fatal(err)
	}
	ownerCommand := scenarioSet(base, ids, scenario).Commands[0]
	c := changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": ownerCommand.Artifact, "revisionId": ownerCommand.RevisionID, "editorBindings": ownerCommand.EditorBindings})
	preview := &AnalysisCommandPreviewInput{ChangeProposal: ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}, Preview: PreviewChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}}}
	initial, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, old.Revision.ID, BackendReadTarget{}, preview)
	if err != nil {
		t.Fatal(err)
	}
	var claimBytes, basisBytes, ownerBytes int64
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT (SELECT sum(length(CAST(document AS BLOB))) FROM backend_revision_assertions_documents WHERE revision_id=?),(SELECT sum(length(CAST(document AS BLOB))) FROM backend_revision_legacy_proof_bases_documents WHERE revision_id=?)`, base.Revision.ID, base.Revision.ID).Scan(&claimBytes, &basisBytes); err != nil {
		t.Fatal(err)
	}
	for _, doc := range initial.Documents {
		if doc.Key == "design_scenario:"+ownerCommand.Artifact.ID+":"+ownerCommand.RevisionID {
			ownerBytes = doc.Bytes
		}
	}
	if claimBytes <= 32 || basisBytes <= 32 || ownerBytes <= 32 {
		t.Fatalf("fixture must contribute all inventories: %d %d %d", claimBytes, basisBytes, ownerBytes)
	}
	// The full exact inventory crosses the cap by one byte; omitting claims,
	// bases or owner masks rejection. Store27 (48dce80, B6.3) checks the graph
	// projection against the payload's id/kind/name, so each padding record
	// carries exactly those identity fields (kind/name empty = the column
	// defaults); the wrapper's own bytes are measured, not hard-coded (it was
	// 14 bytes per record while the wrapper held only the padding key).
	padIDs := []string{uuid.NewV7().String(), uuid.NewV7().String()}
	var wrappers int64
	for _, id := range padIDs {
		wrappers += int64(len(`{"id":"` + id + `","kind":"","name":"","padding":""}`))
	}
	padding := int64(MaxRevisionBytes) + 1 - initial.TotalBytes - wrappers
	// Both revisions' graph manifests are sealed, so the records are seeded in
	// the Store26 fixture shape and published by the production migration.
	err = testkit.EditLegacyBackendFixture(t.Context(), r.db, func(tx *sql.Tx) error {
		for i, rid := range []string{old.Revision.ID, base.Revision.ID} {
			size := padding / 2
			if i == 1 {
				size = padding - size
			}
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES(?,?,'node',?,json_object('id',?,'kind','','name','','padding',printf('%*s',?,'x')))`, base.Project.ID, rid, padIDs[i], padIDs[i], size); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, old.Revision.ID, BackendReadTarget{}, preview)
	f, ok := errors.AsType[*FaultError](err)
	if !ok || f.Code != "backend_analysis_input_limit" {
		t.Fatalf("cap must reject before malformed graph decoding: %v", err)
	}
	actual, ok := f.Details["actualBytes"].(int64)
	if !ok || actual != MaxRevisionBytes+1 {
		t.Fatalf("inventory sum=%v wanted cap+1", f.Details)
	}
	if actual-claimBytes > MaxRevisionBytes || actual-basisBytes > MaxRevisionBytes || actual-ownerBytes > MaxRevisionBytes {
		t.Fatal("fixture did not require every inventory")
	}
}

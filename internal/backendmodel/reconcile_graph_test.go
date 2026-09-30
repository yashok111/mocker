package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
)

func TestReconcileIdentityMappedCommitAndRemoval(t *testing.T) {
	r, p, old, first := committedBase(t)
	s := beginRepeat(t, r, p, old)
	id := first.Identities[0].ID
	mapping := ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "handler", ToExternalKey: "renamed", ExpectedID: id, Reason: "same handler", EvidenceKeys: []string{"new-proof"}}}
	b := sendCommands(t, r, p, s, 1, "mapping", mapping)
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "remove-map", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "renamed"}})
	cs := fixtureCommands(s)
	cs[0].Node.ExternalKey = "renamed"
	cs[0].Node.Name = "Renamed"
	cs[0].Node.EvidenceKeys = []string{"new-proof"}
	cs[1].Evidence.ExternalKey = "new-proof"
	cs[1].Evidence.SubjectKey = "renamed"
	cs[1].Evidence.PropertyPath = new("/name")
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "unmapped-target", cs...)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "needs_resolution" {
		t.Fatalf("removed mapping %+v %v", v, err)
	}
	b = sendCommands(t, r, p, s, v.Version, "clear-target", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "renamed"}})
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "restore-map", mapping)
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "target", cs...)
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "commit-renamed")
	graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].ID != id || graph.Nodes[0].ExternalKey != "renamed" {
		t.Fatalf("mapped graph %+v %v", graph, err)
	}
	evidence, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{})
	if err != nil || len(evidence.Items) != 1 || evidence.Items[0].ExternalKey != "new-proof" {
		t.Fatalf("new evidence %+v %v", evidence, err)
	}
	oldEvidence, err := r.Evidence(t.Context(), p.ID, p.CurrentRevisionID, EvidenceQueryInput{EvidenceID: first.Identities[1].ID})
	if err != nil || len(oldEvidence.Items) != 1 {
		t.Fatalf("old evidence %+v %v", oldEvidence, err)
	}
	status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
	if err != nil || status.Preview == nil || status.CommittedRevisionID == nil || *status.CommittedRevisionID != out.Revision.ID {
		t.Fatalf("saved status %+v %v", status, err)
	}
	details, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: status.Preview.Version, RecordType: "identity"})
	if err != nil || len(details.Items) != 1 || !details.Items[0].Identity.Resolved || details.Items[0].Identity.OldSubject.RevisionID != p.CurrentRevisionID || len(details.Items[0].Identity.OldEvidenceRefs) != 1 {
		t.Fatalf("decision %+v %v", details, err)
	}
}
func TestReconcileDeleteSafetyMatrix(t *testing.T) {
	for _, mode := range []string{"complete", "partial scope", "partial files", "unverified", "unsupported locator", "excluded locator", "count mismatch", "unrelated secret"} {
		t.Run(mode, func(t *testing.T) {
			r, p, old, first := committedBase(t)
			in := repeatInput(p, old.RepositoryID)
			in.IdempotencyKey = "delete"
			switch mode {
			case "partial scope":
				in.GraphScope.Status = "partial"
				in.GraphScope.Gaps = []string{"limited scan"}
			case "partial files":
				in.Inventory[0].Status = "partial"
				in.Inventory[0].Gaps = []string{"limited scan"}
			case "unverified":
				in.Manifest.Snapshot.Consistency = "unverified"
			case "unsupported locator":
				in.Manifest.Snapshot.Files[0].AnalysisStatus = "unsupported"
				in.Manifest.Snapshot.Files[0].Reason = "unsupported"
			case "excluded locator":
				in.Manifest.Snapshot.Files[0].AnalysisStatus = "excluded"
				in.Manifest.Snapshot.Files[0].Reason = "excluded"
			case "count mismatch":
				in.Inventory[0].KnownCount = 2
				in.Inventory[0].Denominator = new(int64(2))
			case "unrelated secret":
				in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, ManifestFile{Path: ".env", ContentHash: fixtureHash, FileType: "env", AnalysisStatus: "excluded", Reason: "secret"})
				in.Inventory[0].KnownCount = 2
				in.Inventory[0].Denominator = new(int64(2))
			}
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			b := sendCommands(t, r, p, s, 1, "delete", ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: "handler", ExpectedID: first.Identities[0].ID, Reason: "handler removed"}})
			v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if err != nil {
				t.Fatal(err)
			}
			allowed := mode == "complete" || mode == "unrelated secret"
			if allowed {
				if v.State != "ready" {
					t.Fatalf("safe deletion %+v", v)
				}
				out, err := commitFixture(t, r, p, s, v, "delete-commit")
				if err != nil {
					t.Fatal(err)
				}
				graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
				if err != nil || len(graph.Nodes) != 0 {
					t.Fatalf("deletion %+v %v", graph, err)
				}
				proof, err := r.Evidence(t.Context(), p.ID, p.CurrentRevisionID, EvidenceQueryInput{})
				if err != nil || len(proof.Items) != 1 {
					t.Fatalf("lost history %+v %v", proof, err)
				}
				nextIn := repeatInput(&out.Project, old.RepositoryID)
				nextIn.IdempotencyKey = "reuse"
				next, err := r.BeginImport(t.Context(), p.ID, nextIn)
				if err != nil {
					t.Fatal(err)
				}
				cs := fixtureCommands(next)
				h, _ := ImportBatchHash(cs)
				_, err = r.PutImportBatch(t.Context(), p.ID, next.ID, "reuse", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: h, Commands: cs})
				assertFault(t, err, "backend_identity_deleted")
			} else if v.State != "needs_resolution" || v.CandidateHash != nil {
				t.Fatalf("unsafe deletion ready %+v", v)
			}
		})
	}
}
func TestReconcileSourcePartialGapAndSavedPages(t *testing.T) {
	r, p, old, _ := committedBase(t)
	in := repeatInput(p, old.RepositoryID)
	in.IdempotencyKey = "partial"
	in.GraphScope.Status = "partial"
	in.GraphScope.Gaps = []string{"collector incomplete"}
	in.Inventory[0].Status = "partial"
	in.Inventory[0].Gaps = []string{"missing file"}
	in.Inventory[0].KnownCount = 0
	in.Inventory[0].Denominator = nil
	in.Manifest.Snapshot.Files = []ManifestFile{}
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: 1, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "ready" || v.SourceChangeCount != 1 {
		t.Fatalf("partial preview %+v %v", v, err)
	}
	page, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: v.Version, RecordType: "source", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Source.Kind != "no_longer_observed" || page.Items[0].Source.DeletionConfirmed {
		t.Fatalf("source delta %+v %v", page, err)
	}
	out, err := commitFixture(t, r, p, s, v, "partial-commit")
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.RevisionCoverage(t.Context(), p.ID, out.Revision.ID)
	if err != nil || !slices.Contains(c.ReconciliationGaps, "collector incomplete") {
		t.Fatalf("missing scope gap %+v %v", c, err)
	}
}
func TestReconcileStaleEdgesAndDependency(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	cs := fixtureCommands(s)
	cs = append(cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "operation", Kind: "http_operation", Name: "GET /", Attributes: map[string]jsontext.Value{"method": jsontext.Value(`"GET"`), "path": jsontext.Value(`"/"`)}, EvidenceKeys: []string{"op-proof"}}})
	proof := *cs[1].Evidence
	proof.ExternalKey = "op-proof"
	proof.SubjectKey = "operation"
	cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	edge := ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "handles", Kind: "handles", FromKey: "operation", ToKey: "handler", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"edge-proof"}}}
	cs = append(cs, edge)
	edgeProof := *cs[1].Evidence
	edgeProof.ExternalKey = "edge-proof"
	edgeProof.SubjectType = "edge"
	edgeProof.SubjectKey = "handles"
	cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &edgeProof})
	b := sendCommands(t, r, p, s, 1, "graph", cs...)
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "graph-commit")
	p = &out.Project
	in := repeatInput(p, s.RepositoryID)
	in.IdempotencyKey = "repeat"
	in.GraphScope.Status = "partial"
	in.GraphScope.Gaps = []string{"operation not reobserved"}
	next, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	updated := fixtureCommands(next)
	updated[0].Node.Name = "Changed"
	updated = append(updated, edge)
	edgeProof.Source.SnapshotID = next.SnapshotID
	updated = append(updated, ImportCommand{Op: "upsert_evidence", Evidence: &edgeProof})
	b = sendCommands(t, r, p, next, 1, "updates", updated...)
	result := commitStaged(t, r, p, next, b.AcceptedVersion, "repeat-commit")
	graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: result.Revision.ID, RecordType: "edges"})
	if err != nil || len(graph.Edges) != 1 || !slices.Contains(graph.Edges[0].Freshness.Reasons, "dependency_stale") {
		raw, _ := json.Marshal(graph)
		t.Fatalf("dependency %s %v", raw, err)
	}
}

func TestReconcileDeleteDanglingDecisionIsUnresolved(t *testing.T) {
	r, p, old, first := committedBase(t)
	s := beginRepeat(t, r, p, old)
	commands := fixtureCommands(s)
	commands[0].Node.ExternalKey = "other"
	commands[1].Evidence.ExternalKey = "other-proof"
	commands[1].Evidence.SubjectKey = "other"
	commands[0].Node.EvidenceKeys = []string{"other-proof"}
	edge := ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "calls", Kind: "calls", FromKey: "other", ToKey: "handler", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"calls-proof"}}}
	proof := *commands[1].Evidence
	proof.ExternalKey = "calls-proof"
	proof.SubjectType = "edge"
	proof.SubjectKey = "calls"
	commands = append(commands, edge, ImportCommand{Op: "upsert_evidence", Evidence: &proof}, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: "handler", ExpectedID: first.Identities[0].ID, Reason: "removed"}})
	b := sendCommands(t, r, p, s, 1, "dangling", commands...)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "needs_resolution" {
		t.Fatalf("dangling %+v %v", v, err)
	}
	page, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: v.Version, RecordType: "deletion"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Deletion.Resolved || v.Summary.Nodes != 2 {
		t.Fatalf("unsafe decision must remain unresolved and preserve target %+v %+v %v", page, v, err)
	}
}
func TestIdentityMappingRemoveResubmitAndTargetAllocation(t *testing.T) {
	for _, mode := range []string{"preallocated", "removed mapping"} {
		t.Run(mode, func(t *testing.T) {
			r, p, old, first := committedBase(t)
			s := beginRepeat(t, r, p, old)
			mapping := ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "handler", ToExternalKey: "next", ExpectedID: first.Identities[0].ID, Reason: "rename", EvidenceKeys: []string{"proof"}}}
			version := int64(1)
			if mode == "preallocated" {
				c := fixtureCommands(s)[0]
				c.Node.ExternalKey = "next"
				b := sendCommands(t, r, p, s, version, "allocate", c)
				b = sendCommands(t, r, p, s, b.AcceptedVersion, "remove", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "next"}})
				h, _ := ImportBatchHash([]ImportCommand{mapping})
				_, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "mapping", ImportBatchInput{ExpectedImportVersion: b.AcceptedVersion, PayloadHash: h, Commands: []ImportCommand{mapping}})
				assertFault(t, err, "backend_identity_conflict")
			} else {
				b := sendCommands(t, r, p, s, version, "mapping", mapping)
				b = sendCommands(t, r, p, s, b.AcceptedVersion, "remove", ImportCommand{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "next"}})
				b = sendCommands(t, r, p, s, b.AcceptedVersion, "resubmit", mapping)
				if b.Identities[0].ID != first.Identities[0].ID {
					t.Fatal("acknowledged UUID changed")
				}
			}
		})
	}
}

func TestReconcileCompetingNewKeysRevalidateWithoutReassigningReceipts(t *testing.T) {
	r, p, old, _ := committedBase(t)
	first := beginRepeat(t, r, p, old)
	in := repeatInput(p, old.RepositoryID)
	in.IdempotencyKey = "second"
	second, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	fill := func(s *ImportSession) *BatchReceipt {
		cs := fixtureCommands(s)
		other := *cs[0].Node
		other.ExternalKey = "new"
		other.EvidenceKeys = []string{"new-proof"}
		proof := *cs[1].Evidence
		proof.ExternalKey = "new-proof"
		proof.SubjectKey = "new"
		cs = append(cs, ImportCommand{Op: "upsert_node", Node: &other}, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
		return sendCommands(t, r, p, s, 1, "records", cs...)
	}
	a, b := fill(first), fill(second)
	preview := func(s *ImportSession, version int64, base string) *ImportPreview {
		t.Helper()
		v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: version, BaseRevisionID: base})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	av, bv := preview(first, a.AcceptedVersion, p.CurrentRevisionID), preview(second, b.AcceptedVersion, p.CurrentRevisionID)
	winner, err := commitFixture(t, r, p, first, av, "winner")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.CommitImport(t.Context(), p.ID, second.ID, CommitImportInput{ExpectedVersion: winner.Project.Version, ExpectedImportVersion: bv.Version, CandidateHash: *bv.CandidateHash, IdempotencyKey: "version-only"})
	assertFault(t, err, "backend_import_hash_conflict")
	next := preview(second, bv.Version, winner.Revision.ID)
	if next.State != "needs_resolution" || next.CandidateHash != nil {
		t.Fatalf("conflicting allocation silently adopted %+v", next)
	}
	// Replaying the acknowledged batch after a conflicting base keeps its original IDs.
	replay := fill(second)
	before, _ := json.Marshal(b)
	after, _ := json.Marshal(replay)
	if string(before) != string(after) {
		t.Fatal("receipt remapped after explicit base change")
	}
	_, err = r.CommitImport(t.Context(), p.ID, second.ID, CommitImportInput{ExpectedVersion: winner.Project.Version, ExpectedImportVersion: next.Version, CandidateHash: *bv.CandidateHash, IdempotencyKey: "old-hash"})
	assertFault(t, err, "backend_import_state_conflict")
}
func TestReconcilePreviewDetailsClearedAndPinned(t *testing.T) {
	r, p, old, _ := committedBase(t)
	in := repeatInput(p, old.RepositoryID)
	in.IdempotencyKey = "changes"
	in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, ManifestFile{Path: "new.go", ContentHash: fixtureHash, FileType: "go", AnalysisStatus: "analyzed"}, ManifestFile{Path: "another.go", ContentHash: fixtureHash, FileType: "go", AnalysisStatus: "analyzed"})
	in.Inventory[0].KnownCount = 3
	in.Inventory[0].Denominator = new(int64(3))
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: 1, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: v.Version, RecordType: "source", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page %+v %v", first, err)
	}
	next, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: v.Version, RecordType: "source", Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(next.Items) != 1 || first.Items[0].Source.Path >= next.Items[0].Source.Path {
		t.Fatalf("next page %+v %v", next, err)
	}
	sendCommands(t, r, p, s, v.Version, "change", fixtureCommands(s)...)
	_, err = r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: v.Version, RecordType: "source", Cursor: first.NextCursor})
	assertFault(t, err, "backend_import_preview_conflict")
	status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
	if err != nil || status.Preview != nil || status.Session.CandidateHash != nil {
		t.Fatalf("stale preview %+v %v", status, err)
	}
}

func TestReconcileDeleteEvidenceKeysStayDeleted(t *testing.T) {
	r, p, old, first := committedBase(t)
	s := beginRepeat(t, r, p, old)
	b := sendCommands(t, r, p, s, 1, "delete", ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: "handler", ExpectedID: first.Identities[0].ID, Reason: "removed"}})
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "commit-delete")
	var state string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT state FROM backend_identity_bindings WHERE record_type='evidence' AND external_key='proof'`).Scan(&state); err != nil || state != "deleted" {
		t.Fatalf("attached evidence binding = %s, err=%v", state, err)
	}
	historical, err := r.Evidence(t.Context(), p.ID, p.CurrentRevisionID, EvidenceQueryInput{EvidenceID: first.Identities[1].ID})
	if err != nil || len(historical.Items) != 1 {
		t.Fatalf("historical evidence %+v %v", historical, err)
	}
	current, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{EvidenceID: first.Identities[1].ID})
	if err != nil || len(current.Items) != 0 {
		t.Fatalf("current evidence %+v %v", current, err)
	}
}

func TestReconcileDeleteRejectsForeignOwnership(t *testing.T) {
	r, p, old, first := committedBase(t)
	if _, err := r.db.W.ExecContext(t.Context(), `UPDATE backend_graph_records SET document=json_set(document,'$.ownership.providerNamespace','other-owner') WHERE revision_id=? AND record_type='node'`, p.CurrentRevisionID); err != nil {
		t.Fatal(err)
	}
	s := beginRepeat(t, r, p, old)
	b := sendCommands(t, r, p, s, 1, "delete", ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: "handler", ExpectedID: first.Identities[0].ID, Reason: "removed"}})
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "needs_resolution" {
		t.Fatalf("foreign owner deletion %+v %v", v, err)
	}
}

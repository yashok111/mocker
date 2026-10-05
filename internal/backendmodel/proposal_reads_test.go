package backendmodel

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestProposalReadHistoricalPins(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			r, d, _, ids := proposalEvaluationFixture(t, dialect)
			pid := d.Proposal.ProjectID
			before := proposalSourceBytes(t, r)
			out, err := r.ApplyProposal(t.Context(), pid, d.Proposal.ID, proposalApplyInput(t, r, d, "read", proposalNullable(ids, "required", false), proposalFK(ids)))
			if err != nil {
				t.Fatal(err)
			}
			target := BackendReadTarget{Proposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: out.Revision.ID}}
			node, err := r.ReadNode(t.Context(), pid, target, ids["column:orders:user_id"])
			if err != nil {
				t.Fatal(err)
			}
			projection := node.ProposalProjection
			if projection == nil || projection.SourceRecord == nil || projection.EffectiveFacet.PropertyOrigins["/nullable"].Kind != "intent" || string(projection.EffectiveFacet.Values["nullable"]) != `{"status":"known","value":false}` {
				t.Fatalf("projection: %+v", node)
			}
			source, err := r.Node(t.Context(), pid, d.Proposal.BaseRevisionID, projection.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, source, projection.SourceRecord)
			created := out.Changes[1].GeneratedIDs["constraintId"]
			designed, err := r.ReadNode(t.Context(), pid, target, created)
			if err != nil || designed.ProposalProjection == nil || designed.ProposalProjection.SourceRecord != nil || designed.ProposalProjection.EffectiveFacet.Base != nil {
				t.Fatalf("designed: %+v %v", designed, err)
			}
			if _, err := r.ReadEvidence(t.Context(), pid, target, EvidenceQueryInput{SubjectID: created}); err == nil {
				t.Fatal("designed record acquired source evidence")
			}
			graph, err := r.QueryGraph(t.Context(), pid, GraphQueryInput{Proposal: target.Proposal, RecordType: "nodes", ID: created})
			if err != nil || len(graph.Nodes) != 0 || graph.ProposalProjection == nil || len(graph.ProposalProjection.Nodes) != 1 {
				t.Fatalf("graph: %+v %v", graph, err)
			}
			page, err := r.QueryDatabase(t.Context(), pid, DatabaseQueryInput{Proposal: target.Proposal, DatastoreID: d.Proposal.DatastoreID, FacetKey: "sql", RecordType: "relationships"})
			if err != nil {
				t.Fatal(err)
			}
			if page.ViewSchemaVersion != ProposalDocumentVersion || page.ProposalPins.EffectiveGraphHash != out.CandidateGraphHash {
				t.Fatalf("pins: %+v", page)
			}
			found := false
			for _, rel := range page.RelationshipItems {
				if rel.Status != "proposed" || rel.RuntimeStatus != "unverified" || rel.EffectiveFacet == nil {
					t.Fatalf("false proof: %+v", rel)
				}
				if rel.ConstraintID == created {
					found = true
					if strings.Contains(strings.Join(rel.TargetCardinality.Basis, "\n"), "Current explicit nondeferrable") {
						t.Fatal("designed FK acquired a source enforcement claim")
					}
					if len(rel.EvidenceIDs) != 0 || rel.TargetCardinality.Min == nil || *rel.TargetCardinality.Min != 1 || rel.TargetCardinality.Max == nil || *rel.TargetCardinality.Max != "1" {
						t.Fatalf("designed bounds: %+v", rel)
					}
				}
			}
			if !found {
				t.Fatal("designed relationship absent")
			}
			assertProposalSourceBytes(t, r, before)
			// A later save and source advance leave an old exact proposal view intact.
			current, err := r.GetProposal(t.Context(), pid, d.Proposal.ID, GetProposalInput{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.ApplyProposal(t.Context(), pid, d.Proposal.ID, proposalApplyInput(t, r, current, "later-read", proposalNullable(ids, "optional", true))); err != nil {
				t.Fatal(err)
			}
			proposalAdvanceSource(t, r, d, ids, dialect)
			again, err := r.ReadNode(t.Context(), pid, target, projection.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, node, again)
			coverage, err := r.ReadCoverage(t.Context(), pid, target)
			if err != nil || coverage.ProposalPins.BaseRevisionID != d.Proposal.BaseRevisionID {
				t.Fatalf("coverage: %+v %v", coverage, err)
			}

		})
	}
}

func assertJSONEqual(t *testing.T, a, b any) {
	t.Helper()
	x, err := canonicalJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := canonicalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(x) != string(y) {
		t.Fatalf("JSON differs:\n%s\n%s", x, y)
	}
}

func TestProposalReadCursorExclusiveTarget(t *testing.T) {
	t.Parallel()
	r, d, _, ids := proposalEvaluationFixture(t, "sqlite")
	pid := d.Proposal.ProjectID
	p := &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}
	for _, in := range []GraphQueryInput{
		{RecordType: "nodes"}, {RevisionID: d.Proposal.BaseRevisionID, Proposal: p, RecordType: "nodes"},
		{Proposal: &ProposalReadTarget{ProposalID: p.ProposalID}, RecordType: "nodes"},
		{Proposal: &ProposalReadTarget{ProposalID: p.ProposalRevisionID, ProposalRevisionID: p.ProposalID}, RecordType: "nodes"},
	} {
		if _, err := r.QueryGraph(t.Context(), pid, in); err == nil {
			t.Fatalf("accepted target: %+v", in)
		}
	}
	page, err := r.QueryGraph(t.Context(), pid, GraphQueryInput{Proposal: p, RecordType: "nodes", Kind: "column", Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("page: %+v %v", page, err)
	}
	if _, err := r.QueryGraph(t.Context(), pid, GraphQueryInput{RevisionID: d.Proposal.BaseRevisionID, RecordType: "nodes", Kind: "column", Cursor: page.NextCursor}); err == nil {
		t.Fatal("proposal cursor accepted in source")
	}
	if _, err := r.QueryGraph(t.Context(), pid, GraphQueryInput{Proposal: p, RecordType: "nodes", Kind: "table", Cursor: page.NextCursor}); err == nil {
		t.Fatal("cross-filter cursor accepted")
	}
	if _, err := r.QueryDatabase(t.Context(), pid, DatabaseQueryInput{Proposal: p, DatastoreID: ids["table:orders"], FacetKey: "sql", RecordType: "tables"}); err == nil {
		t.Fatal("selection changed")
	}
	if _, err := r.QueryDatabase(t.Context(), pid, DatabaseQueryInput{Proposal: p, DatastoreID: d.Proposal.DatastoreID, FacetKey: "orm", RecordType: "tables"}); err == nil {
		t.Fatal("facet changed")
	}
	proposalAdvanceSource(t, r, d, ids, "sqlite")
	if _, err := r.QueryGraph(t.Context(), pid, GraphQueryInput{Proposal: p, RecordType: "nodes", Kind: "column", Cursor: page.NextCursor}); err != nil {
		t.Fatal(err)
	}
}

func TestProposalReadStrictTargetWire(t *testing.T) {
	valid := `{"proposal":{"proposalId":"018effb2-731a-7edb-b633-1387b184b21d","proposalRevisionId":"018effb2-731a-7edb-b633-1387b184b21e"},"recordType":"nodes"}`
	for _, raw := range []string{`{"recordType":"nodes"}`, strings.Replace(valid, `"recordType"`, `"revisionId":"018effb2-731a-7edb-b633-1387b184b21d","recordType"`, 1), strings.Replace(valid, `"proposalRevisionId"`, `"unknown"`, 1), `{"proposal":null,"recordType":"nodes"}`, valid[:len(valid)-1] + `,"limit":0}`} {
		var in GraphQueryInput
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatalf("accepted: %s", raw)
		}
	}
	var in GraphQueryInput
	if err := json.Unmarshal([]byte(valid), &in); err != nil {
		t.Fatal(err)
	}
}

func proposalAdvanceSource(t *testing.T, r *Repo, d *ProposalDetail, ids map[string]string, dialect string) {
	t.Helper()
	project, err := r.Get(t.Context(), d.Proposal.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := r.Revision(t.Context(), d.Proposal.ProjectID, d.Proposal.BaseRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var sessionID string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT id FROM backend_import_sessions WHERE project_id=? ORDER BY id LIMIT 1`, project.ID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	databaseCommitV2(t, r, &ImportCommitResult{Project: *project, Revision: *revision, SessionID: sessionID}, ids, dialect)
}

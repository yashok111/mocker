package backendanalysis

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
	"uuid"

	model "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestB43PositiveDeletionSourceFiveAndSix(t *testing.T) {
	for _, schema := range []string{"5", "6"} {
		t.Run(schema, func(t *testing.T) {
			f := newRebaseAnalysisFixture(t, schema)
			base := f.project.CurrentRevisionID
			f.importSource("removed", "", true, nil)
			req := model.AnalysisEvidenceRequest{Targets: []model.BackendReadTarget{{RevisionID: base}, {RevisionID: f.project.CurrentRevisionID}}, BaselineRevisionID: base, ResultRevisionID: f.project.CurrentRevisionID}
			footprint, err := f.graphs.AnalysisEvidenceInputFootprint(t.Context(), f.project.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			ctx, lease, err := f.graphs.ReserveAnalysisInput(t.Context(), f.project.ID, footprint)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			pins, err := f.graphs.ReadAnalysisEvidencePins(ctx, f.project.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := f.graphs.ReadAnalysisDeletionEvidence(ctx, f.project.ID, base, f.project.CurrentRevisionID, pins)
			if err != nil {
				t.Fatal(err)
			}
			if len(evidence.Deletions) != 1 || evidence.Deletions[0].Record.ID != f.node || evidence.Deletions[0].OldSubject.RevisionID != base {
				t.Fatalf("positive deletion missing: %+v", evidence)
			}
			if _, err = f.graphs.ReadAnalysisDeletionEvidence(ctx, f.project.ID, base, f.project.CurrentRevisionID, nil); err == nil {
				t.Fatal("unpinned deletion read accepted")
			}
		})
	}
}

func TestB43ConformanceExactSourceAndRuntime(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	d = setB43Criteria(t, f, d, []model.ChangeCriterion{
		{Key: "exists", Kind: "object_exists", Required: true, Description: "Exact handler", RecordType: "node", ID: f.node, ObjectKind: "handler"},
		{Key: "runtime", Kind: "runtime_check", Required: true, Description: "Execute manually", TargetIDs: []string{f.node}},
	})
	f.importSource("implemented", "Desired", false, nil)
	_, saved, report := runB43Conformance(t, f, d, nil, nil)
	outcomes := map[string]string{}
	for _, chunk := range report.Snapshot.Chunks {
		if chunk.Section != "checks" {
			continue
		}
		var records []ResultRecord
		if err := json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			var result ConformanceCriterionDetail
			if err := json.Unmarshal(record.Detail, &result); err != nil {
				t.Fatal(err)
			}
			outcomes[result.CriterionKey] = result.Outcome
		}
	}
	if outcomes["exists"] != "satisfied" || outcomes["runtime"] != "unverified" {
		t.Fatalf("outcomes %+v", outcomes)
	}
	f.importSource("later", "Later", false, nil)
	again, err := NewEngine(f.graphs, nil).Analyze(t.Context(), saved, nil)
	if err != nil || integrationBytes(t, again) != integrationBytes(t, report) {
		t.Fatalf("historical conformance moved %v", err)
	}
}
func setB43Criteria(t *testing.T, f *rebaseAnalysisFixture, d *model.ChangeProposalDetail, criteria []model.ChangeCriterion) *model.ChangeProposalDetail {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": "set_criteria", "commandId": uuid.NewV7().String(), "reason": "Saved acceptance criteria", "criteria": criteria})
	if err != nil {
		t.Fatal(err)
	}
	var command model.ChangeProposalCommand
	if err = json.Unmarshal(raw, &command); err != nil {
		t.Fatal(err)
	}
	input := model.PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []model.ChangeProposalCommand{command}}
	preview, err := f.graphs.PreviewChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, input)
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("criteria preview %+v %v", preview, err)
	}
	out, err := f.graphs.ApplyChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalInput{ExpectedVersion: input.ExpectedVersion, ProposalRevisionID: input.ProposalRevisionID, Commands: input.Commands, CandidateHash: *preview.CandidateHash, IdempotencyKey: uuid.NewV7().String()})
	if err != nil {
		t.Fatal(err)
	}
	return &model.ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision}
}
func runB43Conformance(t *testing.T, f *rebaseAnalysisFixture, d *model.ChangeProposalDetail, mapping []IdentityMapEntry, attachments []CriterionAttachment) (*Job, *ImmutableInput, *TerminalSnapshot) {
	t.Helper()
	s := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	start := StartInput{Kind: "conformance", Conformance: &StartConformanceInput{StartCommon: StartCommon{Kind: "conformance", Limits: defaultLimits(), ObservationMode: "none", IdempotencyKey: uuid.NewV7().String()}, ChangeProposal: model.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}, ResultRevisionID: f.project.CurrentRevisionID, IdentityMap: mapping, TestAttachments: attachments}}
	job, err := s.Start(t.Context(), f.project.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.jobs.Input(t.Context(), f.project.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.engine.Analyze(t.Context(), saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	return job, saved, report
}

func TestB43OptionalMismatchAndHistoricalAttachment(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	base := f.project.CurrentRevisionID
	g, err := f.graphs.ResolveEffectiveGraph(t.Context(), f.project.ID, model.BackendReadTarget{RevisionID: base})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := g.State.Sources[0]
	file := snapshot.Files[0]
	attachment := model.TestAttachmentRef{Kind: "source", RevisionID: base, RepositoryID: snapshot.RepositoryID, SnapshotID: snapshot.ID, File: file.Path, ContentHash: file.ContentHash, StartLine: 1, EndLine: 1}
	d = setB43Criteria(t, f, d, []model.ChangeCriterion{
		{Key: "optional", Kind: "field_equals", Required: false, Description: "Exact optional name", RecordType: "node", ID: f.node, Selector: []byte(`{"kind":"source","source":{"kind":"name"}}`), Expected: &model.SourcePropertyValue{Present: true, Value: []byte(`"Desired"`)}},
		{Key: "Исторический тест", Kind: "test_attachment", Required: true, Description: "Exact historical source locator", TargetIDs: []string{f.node}, Attachment: &attachment},
	})
	f.importSource("actual", "Other", false, nil)
	for _, tc := range []struct {
		name       string
		attachment *model.TestAttachmentRef
		outcome    string
	}{{"missing", nil, "unverified"}, {"exact", &attachment, "satisfied"}, {"wrong", new(attachment), "violated"}} {
		t.Run(tc.name, func(t *testing.T) {
			var associations []CriterionAttachment
			if tc.attachment != nil {
				if tc.name == "wrong" {
					tc.attachment.StartLine = 2
					tc.attachment.EndLine = 2
				}
				associations = []CriterionAttachment{{CriterionKey: "Исторический тест", Attachment: *tc.attachment}}
			}
			_, _, report := runB43Conformance(t, f, d, nil, associations)
			rows := b43ConformanceRows(t, report)
			if rows["optional"].Outcome != "violated" || rows["Исторический тест"].Outcome != tc.outcome {
				t.Fatalf("rows %+v", rows)
			}
		})
	}
}
func b43ConformanceRows(t *testing.T, report *TerminalSnapshot) map[string]ConformanceCriterionDetail {
	t.Helper()
	out := map[string]ConformanceCriterionDetail{}
	for _, chunk := range report.Snapshot.Chunks {
		if chunk.Section != "checks" {
			continue
		}
		var records []ResultRecord
		if err := json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			var row ConformanceCriterionDetail
			if err := json.Unmarshal(record.Detail, &row); err != nil {
				t.Fatal(err)
			}
			out[row.CriterionKey] = row
		}
	}
	return out
}
func TestB43IdentityMappingAndOutsideIntent(t *testing.T) {
	existing, created, source := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	base := &model.EffectiveGraphSnapshot{State: model.RevisionState{Nodes: []model.Node{{ID: existing, Kind: "handler", Name: "before"}}}}
	draft := &model.EffectiveGraphSnapshot{State: model.RevisionState{Nodes: []model.Node{{ID: existing, Kind: "handler", Name: "desired"}, {ID: created, Kind: "handler", Name: "same"}}}}
	result := &model.EffectiveGraphSnapshot{Pins: model.EffectiveGraphPins{StructuralSchemaVersion: "6"}, State: model.RevisionState{Nodes: []model.Node{{ID: existing, Kind: "handler", Name: "desired"}, {ID: source, Kind: "handler", Name: "same"}}}}
	for _, tc := range []struct {
		name  string
		entry IdentityMapEntry
		valid bool
	}{{"explicit", IdentityMapEntry{created, source, "Exact creation"}, true}, {"retained remap", IdentityMapEntry{existing, source, "Wrong"}, false}, {"collision", IdentityMapEntry{created, existing, "Wrong"}, false}, {"unknown", IdentityMapEntry{uuid.NewV7().String(), source, "Wrong"}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateConformanceAssociations(&ConformancePayload{IdentityMap: []IdentityMapEntry{tc.entry}}, base, draft, result)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	mapping, err := validateConformanceAssociations(&ConformancePayload{}, base, draft, result)
	if err != nil {
		t.Fatal(err)
	}
	if mapping[created] != "" {
		t.Fatal("created node inferred by label")
	}
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	before, err := f.graphs.ResolveEffectiveGraph(t.Context(), f.project.ID, model.BackendReadTarget{RevisionID: d.Revision.BaseRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := f.graphs.ResolveEffectiveGraph(t.Context(), f.project.ID, model.BackendReadTarget{ChangeProposal: &model.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	actual := *desired
	actual.State = desired.State
	actual.State.Nodes = slices.Clone(desired.State.Nodes)
	actual.State.Nodes[0].Attributes = map[string]jsontext.Value{"description": []byte(`"outside intent"`)}
	changes, err := outsideIntentChanges(t.Context(), before, desired, &actual, map[string]string{f.node: f.node})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(changes, func(c DiffChange) bool {
		return c.Object.ID == f.node && slices.Contains(c.Paths, "/attributes/description")
	}) {
		t.Fatalf("outside property omitted %+v", changes)
	}
}
func TestB43DeletionMissingDecision(t *testing.T) {
	for _, schema := range []string{"5", "6"} {
		t.Run(schema, func(t *testing.T) {
			f := newRebaseAnalysisFixture(t, schema)
			baseline := f.project.CurrentRevisionID
			f.importSource("remove", "", true, nil)
			result := f.project.CurrentRevisionID
			// Store27 (48dce80, B6.3) seals the decision payload membership; the
			// missing decision is shaped as a Store26 fixture and migrated.
			if _, err := testkit.EditLegacyBackendPayload(t.Context(), f.db, `DELETE FROM backend_revision_decisions WHERE revision_id=?`, result); err != nil {
				t.Fatal(err)
			}
			req := model.AnalysisEvidenceRequest{Targets: []model.BackendReadTarget{{RevisionID: baseline}, {RevisionID: result}}, BaselineRevisionID: baseline, ResultRevisionID: result}
			footprint, err := f.graphs.AnalysisEvidenceInputFootprint(t.Context(), f.project.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			ctx, lease, err := f.graphs.ReserveAnalysisInput(t.Context(), f.project.ID, footprint)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			pins, err := f.graphs.ReadAnalysisEvidencePins(ctx, f.project.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := f.graphs.ReadAnalysisDeletionEvidence(ctx, f.project.ID, baseline, result, pins)
			if err != nil || len(evidence.Deletions) != 0 {
				t.Fatalf("absence became proof %+v %v", evidence, err)
			}
		})
	}
}

func TestB43DeletionNegativeDecisionsAndScopes(t *testing.T) {
	cases := []struct{ name, schema, table, path, value string }{
		{"unresolved5", "5", "backend_revision_decisions", "$.deletion[0].resolved", "false"},
		{"wrong-old-subject5", "5", "backend_revision_decisions", "$.deletion[0].oldSubject.id", `"11111111-1111-4111-8111-111111111111"`},
		{"wrong-command5", "5", "backend_revision_decisions", "$.deletion[0].command.externalKey", `"another"`},
		{"partial5", "5", "backend_revision_sources", "$.coverage.status", `"partial"`},
		{"migration6", "6", "backend_revision_decisions", "$.sourceScope.kind", `"migrate_provider"`},
		{"wrong-owner6", "6", "backend_revision_decisions", "$.sourceScope.providerNamespace", `"other"`},
		{"wrong-command6", "6", "backend_revision_decisions", "$.legacyDecisions[0].deletion.externalKey", `"another"`},
		{"partial6", "6", "backend_revision_sources", "$.sourceVector.partitions[0].scopeStatus.status", `"partial"`},
		{"retired6", "6", "backend_revision_sources", "$.sourceVector.snapshots[0].role", `"retained_provenance"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRebaseAnalysisFixture(t, tc.schema)
			base := f.project.CurrentRevisionID
			f.importSource("removed", "", true, nil)
			result := f.project.CurrentRevisionID
			// Store27 (48dce80, B6.3) seals the payload; the negative decision is
			// shaped as a Store26 fixture and published by the production migration.
			if _, err := testkit.EditLegacyBackendPayload(t.Context(), f.db, `UPDATE `+tc.table+` SET document=json_set(document,?,json(?)) WHERE revision_id=?`, tc.path, tc.value, result); err != nil {
				t.Fatal(err)
			}
			req := model.AnalysisEvidenceRequest{Targets: []model.BackendReadTarget{{RevisionID: base}, {RevisionID: result}}, BaselineRevisionID: base, ResultRevisionID: result}
			footprint, err := f.graphs.AnalysisEvidenceInputFootprint(t.Context(), f.project.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			ctx, lease, err := f.graphs.ReserveAnalysisInput(t.Context(), f.project.ID, footprint)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			pins, err := f.graphs.ReadAnalysisEvidencePins(ctx, f.project.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := f.graphs.ReadAnalysisDeletionEvidence(ctx, f.project.ID, base, result, pins)
			if err != nil {
				t.Fatal(err)
			}
			if len(evidence.Deletions) != 0 {
				t.Fatalf("unsupported deletion became affirmative %+v", evidence)
			}
		})
	}
}

func TestB43CreatedRelationshipUsesUniqueImportedTuple(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	edgeID := uuid.NewV7().String()
	command := model.ChangeProposalCommand{Type: "upsert_edge", CommandID: uuid.NewV7().String(), Reason: "Add recursive call", ID: edgeID, Kind: "calls", From: f.node, To: f.node, Attributes: map[string]jsontext.Value{}}
	preview, err := f.graphs.PreviewChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []model.ChangeProposalCommand{command}})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("edge preview %+v %v", preview, err)
	}
	applied, err := f.graphs.ApplyChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []model.ChangeProposalCommand{command}, CandidateHash: *preview.CandidateHash, IdempotencyKey: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	d = &model.ChangeProposalDetail{Proposal: applied.Proposal, Revision: applied.Revision}
	d = setB43Criteria(t, f, d, []model.ChangeCriterion{{Key: "relationship", Kind: "edge_exists", Required: true, Description: "Recursive call relationship", ID: edgeID, EdgeKind: "calls", From: f.node, To: f.node}, {Key: "edge-object", Kind: "object_exists", Required: true, Description: "Call exists", RecordType: "edge", ID: edgeID, ObjectKind: "calls"}})
	f.extraEdges = 1
	f.importSource("with-edge", "Desired", false, nil)
	_, _, report := runB43Conformance(t, f, d, nil, nil)
	rows := b43ConformanceRows(t, report)
	if rows["relationship"].Outcome != "satisfied" || rows["edge-object"].Outcome != "satisfied" || rows["relationship"].SourceObject.ID == edgeID {
		t.Fatalf("actual imported edge %+v", rows)
	}
	for _, chunk := range report.Snapshot.Chunks {
		if chunk.Section != "changes" {
			continue
		}
		var records []ResultRecord
		if err = json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Object.RecordType == "edge" && record.Object.ID == rows["relationship"].SourceObject.ID {
				var change OutsideIntentChangeDetail
				if err = json.Unmarshal(record.Detail, &change); err != nil {
					t.Fatal(err)
				}
				for _, path := range change.Change.Paths {
					if path == "/from" || path == "/to" || path == "/kind" {
						t.Fatalf("intended relationship outside intent %+v", change)
					}
				}
			}
		}
	}
	f.extraEdges = 2
	f.importSource("parallel-edges", "Desired", false, nil)
	_, _, report = runB43Conformance(t, f, d, nil, nil)
	rows = b43ConformanceRows(t, report)
	if rows["relationship"].Outcome != "unverified" || rows["edge-object"].Outcome != "unverified" {
		t.Fatalf("ambiguous parallel relationship %+v", rows)
	}
}
func TestB43CreatedNodeAdditionalPropertyIsOutsideIntent(t *testing.T) {
	desiredID, sourceID := uuid.NewV7().String(), uuid.NewV7().String()
	base := &model.EffectiveGraphSnapshot{}
	draft := &model.EffectiveGraphSnapshot{State: model.RevisionState{Nodes: []model.Node{{ID: desiredID, Kind: "handler", Name: "Added", Attributes: map[string]jsontext.Value{}}}}}
	result := &model.EffectiveGraphSnapshot{State: model.RevisionState{Nodes: []model.Node{{ID: sourceID, Kind: "handler", Name: "Added", Attributes: map[string]jsontext.Value{"description": []byte(`"outside"`)}}}}}
	changes, err := outsideIntentChanges(t.Context(), base, draft, result, map[string]string{desiredID: sourceID})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(changes, func(c DiffChange) bool {
		return c.Object.ID == sourceID && slices.Contains(c.Paths, "/attributes/description")
	}) {
		t.Fatalf("added-property hidden %+v", changes)
	}
}

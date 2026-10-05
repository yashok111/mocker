package backendmodel

import (
	"bytes"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/designscenario"
)

func TestFrozenPreviewSurvivesLaterApplyAndLedgerGrowth(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	id := uuid.NewV7().String()
	c := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Desired","parentId":null,"attributes":{}`, id))
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}}
	candidate, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := r.FreezeChangePreview(t.Context(), base.Project.ID, d.Proposal.ID, in, *candidate.CandidateHash)
	if err != nil {
		t.Fatal(err)
	}
	frozen = roundTripAnalysisFrozen(t, frozen)
	before, err := r.ResolveFrozenChangePreview(t.Context(), base.Project.ID, frozen)
	if err != nil {
		t.Fatal(err)
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		return r.ValidateFrozenChangePreviewAdmission(t.Context(), tx, base.Project.ID, frozen)
	})
	if err != nil {
		t.Fatal(err)
	}
	next, _ := saveChange(t, r, d, "accept", c)
	_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: next.Proposal.Version, ProposalRevisionID: next.Revision.ID, Commands: []ChangeProposalCommand{c}})
	assertFault(t, err, "backend_change_command_conflict")
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		return r.ValidateFrozenChangePreviewAdmission(t.Context(), tx, base.Project.ID, frozen)
	})
	if err == nil {
		t.Fatal("stale admission accepted")
	}
	after, err := r.ResolveFrozenChangePreview(t.Context(), base.Project.ID, frozen)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before, json.Deterministic(true))
	b, _ := json.Marshal(after, json.Deterministic(true))
	if string(a) != string(b) {
		t.Fatal("frozen snapshot changed after acceptance")
	}
	if after.Pins.EffectiveSemanticHash != frozen.EffectiveSemanticHash || len(after.Origins) == 0 {
		t.Fatal("missing desired hash/origins")
	}
	frozen.Commands[0].Name = "Tampered"
	if _, err = r.ResolveFrozenChangePreview(t.Context(), base.Project.ID, frozen); err == nil {
		t.Fatal("tampered frozen commands accepted")
	}
}

func TestFrozenPreviewRetainsNewArtifactAndTestOwnerAfterAdvancement(t *testing.T) {
	t.Parallel()
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Frozen artifact", BaseRevisionID: base.Revision.ID, IdempotencyKey: "frozen-artifact"})
	if err != nil {
		t.Fatal(err)
	}
	command := scenarioSet(base, ids, scenario).Commands[0]
	set := changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": command.Artifact, "revisionId": command.RevisionID, "editorBindings": command.EditorBindings})
	owner := service.scenarios.(*designscenario.Repo)
	snapshot, err := owner.ArtifactSnapshot(t.Context(), scenario.Scenario.ID, scenario.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	pin := ArtifactPin{Kind: "design_scenario", ID: command.Artifact.ID, RevisionID: command.RevisionID, ContentHash: snapshot.ContentHash}
	check := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "scenario", "kind": "test_attachment", "required": true, "description": "Exact saved check", "targetIds": []string{ids["http"]}, "attachment": map[string]any{"kind": "artifact", "artifact": pin, "jsonPointer": ""}}}})
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{set, check}}
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{}, &AnalysisCommandPreviewInput{ChangeProposal: ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}, Preview: in})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(f.Documents, func(v AnalysisDocumentBytes) bool { return v.Key == "design_scenario:"+pin.ID+":"+pin.RevisionID }) {
		t.Fatal("new owner missing from predecode footprint")
	}
	preview, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := r.FreezeChangePreview(t.Context(), base.Project.ID, d.Proposal.ID, in, *preview.CandidateHash)
	if err != nil {
		t.Fatal(err)
	}
	frozen = roundTripAnalysisFrozen(t, frozen)
	before, err := r.ResolveFrozenChangePreview(t.Context(), base.Project.ID, frozen)
	if err != nil {
		t.Fatal(err)
	}
	doc := scenario.Draft.Document
	doc.Title = "Later owner revision"
	if _, err = owner.Save(t.Context(), scenario.Scenario.ID, designscenario.SaveInput{ExpectedVersion: scenario.Scenario.Version, Document: doc, FormDrafts: scenario.Draft.FormDrafts, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	after, err := r.ResolveFrozenChangePreview(t.Context(), base.Project.ID, frozen)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before, json.Deterministic(true))
	b, _ := json.Marshal(after, json.Deterministic(true))
	if string(a) != string(b) || !slices.Contains(after.Pins.ArtifactPins, pin) {
		t.Fatal("owner advancement changed frozen replay")
	}
	request := NewEditorArtifactRequest(t.Context(), service.api, service.scenarios)
	page, err := request.ProjectEffective(after, ArtifactQueryInput{RevisionID: after.State.Revision.ID, Artifact: artifactKey(pin), View: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	if page.SelectedPin != pin || len(page.Items) == 0 {
		t.Fatal("desired artifact context was not projected")
	}
}
func TestFrozenPreviewAdmissionClosesIdentityRaceWithoutGraphDecode(t *testing.T) {
	r, base, d := changeFixture(t)
	id := uuid.NewV7().String()
	c := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Race","parentId":null,"attributes":{}`, id))
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}}
	candidate, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := r.FreezeChangePreview(t.Context(), base.Project.ID, d.Proposal.ID, in, *candidate.CandidateHash)
	if err != nil {
		t.Fatal(err)
	}
	identity := ChangeObjectIdentity{ChangeRecordRef: ChangeRecordRef{RecordType: "node", ID: id}, Kind: "service", Origin: EffectiveOrigin{Kind: "intent", CommandID: c.CommandID, Reason: c.Reason}}
	raw, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposal_identities(project_id,proposal_id,id,record_type,kind,first_revision_id,document) VALUES(?,?,?,'node','service',?,?)`, base.Project.ID, d.Proposal.ID, id, d.Revision.ID, string(raw))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		if err := r.ValidateFrozenChangePreviewAdmission(t.Context(), tx, base.Project.ID, frozen); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO backend_analysis_inputs(project_id,input_hash,document,input_bytes) VALUES(?,'must-not-save','{}',2)`, base.Project.ID)
		return err
	})
	assertFault(t, err, "backend_change_identity_conflict")
	for _, table := range []string{"backend_analysis_inputs", "backend_analysis_jobs", "backend_analysis_receipts"} {
		var count int
		if err = r.db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("admission race persisted %s: %d %v", table, count, err)
		}
	}
	c.CommandID = uuid.NewV7().String()
	in.Commands = []ChangeProposalCommand{c}
	_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	assertFault(t, err, "backend_change_identity_conflict")
}

func roundTripAnalysisFrozen(t *testing.T, frozen *FrozenChangePreview) *FrozenChangePreview {
	t.Helper()
	raw, err := json.Marshal(frozen, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"expectedVersion":`)) || !bytes.Contains(raw, []byte(`"commandIds":`)) || bytes.Contains(raw, []byte(`"sourceVector":`)) || bytes.Contains(raw, []byte(`"State":`)) {
		t.Fatal("frozen persistence exposed a graph/manifest or lost lowerCamelCase protocol")
	}
	var out FrozenChangePreview
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestAnalysisLeaseOrdinaryPreviewAndApplyRejectUnadmittedInputs(t *testing.T) {
	t.Parallel()
	r, base, first := changeFixture(t)
	same, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Other draft", BaseRevisionID: base.Revision.ID, IdempotencyKey: "lease-other-draft"})
	if err != nil {
		t.Fatal(err)
	}
	in := source6Input(t, &base.Project)
	in.IdempotencyKey = "lease-other-base"
	in.Manifest.RepositoryName = "lease-other-repository"
	_, next := commitSource6Fixture(t, r, &base.Project, in)
	other, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Other base", BaseRevisionID: next.Revision.ID, IdempotencyKey: "lease-other-base-draft"})
	if err != nil {
		t.Fatal(err)
	}
	footprint, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{RevisionID: base.Revision.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, footprint)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	charged := r.db.TransientBytes("backend:" + base.Project.ID)
	for i, d := range []*ChangeProposalDetail{first, same, other} {
		id := uuid.NewV7().String()
		command := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Unadmitted","parentId":null,"attributes":{}`, id))
		input := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{command}}
		candidate, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		apply := ApplyChangeProposalInput{ExpectedVersion: input.ExpectedVersion, ProposalRevisionID: input.ProposalRevisionID, Commands: input.Commands, CandidateHash: *candidate.CandidateHash, IdempotencyKey: fmt.Sprintf("unadmitted-%d", i)}
		t.Run(fmt.Sprintf("draft%d-preview", i), func(t *testing.T) {
			_, err := r.PreviewChangeProposal(ctx, base.Project.ID, d.Proposal.ID, input)
			assertAnalysisLeaseDenied(t, err)
		})
		t.Run(fmt.Sprintf("draft%d-apply", i), func(t *testing.T) {
			_, err := r.ApplyChangeProposal(ctx, base.Project.ID, d.Proposal.ID, apply)
			assertAnalysisLeaseDenied(t, err)
		})
		current, err := loadChangeProposal(t.Context(), r.db.R, base.Project.ID, d.Proposal.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Version != d.Proposal.Version || current.CurrentDraftRevisionID != d.Revision.ID {
			t.Error("denied Apply mutated aggregate")
		}
		var count int
		if err = r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_command_receipts WHERE scope=? AND key=?`, "change-proposal-apply:"+base.Project.ID+":"+d.Proposal.ID, apply.IdempotencyKey).Scan(&count); err != nil || count != 0 {
			t.Errorf("denied Apply saved receipt: count=%d err=%v", count, err)
		}
	}
	if got := r.db.TransientBytes("backend:" + base.Project.ID); got != charged {
		t.Fatalf("ordinary preparation changed held charge: %d -> %d", charged, got)
	}
}

func assertAnalysisLeaseDenied(t *testing.T, err error) {
	t.Helper()
	fault, ok := errors.AsType[*FaultError](err)
	if !ok || fault.Code != "backend_invalid" || fault.Details["field"] != "analysisInput" {
		t.Fatalf("expected preparation admission rejection before decoding, got %v", err)
	}
}

func TestAnalysisLeaseOrdinaryPreparationRejectsBeforeIdentityDecode(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{RevisionID: base.Revision.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposal_identities(project_id,proposal_id,id,record_type,kind,first_revision_id,document) VALUES(?,?,?,'node','service',?,'{"origin":[]}')`, base.Project.ID, d.Proposal.ID, uuid.NewV7().String(), d.Revision.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	input := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Blocked","parentId":null,"attributes":{}`, uuid.NewV7().String()))}}
	_, err = r.PreviewChangeProposal(ctx, base.Project.ID, d.Proposal.ID, input)
	assertAnalysisLeaseDenied(t, err)
}

func TestAnalysisLeaseOrdinaryPreparationRejectsBeforeOtherGraphDecode(t *testing.T) {
	t.Parallel()
	r, base, _ := changeFixture(t)
	in := source6Input(t, &base.Project)
	in.IdempotencyKey = "poison-other-base"
	in.Manifest.RepositoryName = "poison-other-repository"
	_, next := commitSource6Fixture(t, r, &base.Project, in)
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Other graph", BaseRevisionID: next.Revision.ID, IdempotencyKey: "poison-other-draft"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{RevisionID: base.Revision.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES(?,?,'node',?,'{"attributes":[]}')`, base.Project.ID, next.Revision.ID, uuid.NewV7().String())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	input := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Blocked","parentId":null,"attributes":{}`, uuid.NewV7().String()))}}
	_, err = r.PreviewChangeProposal(ctx, base.Project.ID, d.Proposal.ID, input)
	assertAnalysisLeaseDenied(t, err)
}

func TestAnalysisLeaseCannotBorrowOversizedPreparation(t *testing.T) {
	r, base, _ := changeFixture(t)
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{RevisionID: base.Revision.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	_, err = r.reserveChangeInput(ctx, base.Project.ID, MaxRevisionBytes+1)
	if err == nil {
		t.Fatal("undersized pair lease admitted oversized preparation")
	}
}

func TestAnalysisLeaseAdmittedFreezeRemainsExactAndReceiptsRemainFirst(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	other, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Not admitted", BaseRevisionID: base.Revision.ID, IdempotencyKey: "freeze-other"})
	if err != nil {
		t.Fatal(err)
	}
	c := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Admitted","parentId":null,"attributes":{}`, uuid.NewV7().String()))
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}}
	candidate, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{}, &AnalysisCommandPreviewInput{ChangeProposal: ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}, Preview: in})
	if err != nil {
		t.Fatal(err)
	}
	ctx, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	frozen, err := r.FreezeChangePreview(ctx, base.Project.ID, d.Proposal.ID, in, *candidate.CandidateHash)
	if err != nil {
		t.Fatal(err)
	}
	charged := r.db.TransientBytes("backend:" + base.Project.ID)
	if _, err = r.ResolveFrozenChangePreview(ctx, base.Project.ID, frozen); err != nil {
		t.Fatal(err)
	}
	if got := r.db.TransientBytes("backend:" + base.Project.ID); got != charged || got == 0 {
		t.Fatalf("legal replay lost/doubled reservation: %d -> %d", charged, got)
	}
	changed := in
	changed.Commands = slices.Clone(in.Commands)
	changed.Commands[0].CommandID = uuid.NewV7().String()
	changedCandidate, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.FreezeChangePreview(ctx, base.Project.ID, d.Proposal.ID, changed, *changedCandidate.CandidateHash); err == nil {
		t.Fatal("lease accepted another normalized command input")
	}
	otherInput := in
	otherInput.ProposalRevisionID = other.Revision.ID
	otherCandidate, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, other.Proposal.ID, otherInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.FreezeChangePreview(ctx, base.Project.ID, other.Proposal.ID, otherInput, *otherCandidate.CandidateHash); err == nil {
		t.Fatal("lease accepted another draft")
	}
	_, err = r.PreviewChangeProposal(ctx, base.Project.ID, d.Proposal.ID, in)
	assertAnalysisLeaseDenied(t, err)
	apply := ApplyChangeProposalInput{ExpectedVersion: in.ExpectedVersion, ProposalRevisionID: in.ProposalRevisionID, Commands: in.Commands, CandidateHash: *candidate.CandidateHash, IdempotencyKey: "accepted-before-lease-replay"}
	first, err := r.ApplyChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.ApplyChangeProposal(ctx, base.Project.ID, d.Proposal.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(replay)
	if !bytes.Equal(a, b) {
		t.Fatal("borrow guard changed acknowledged Apply receipt")
	}
	if _, err = r.ResolveFrozenChangePreview(ctx, base.Project.ID, frozen); err != nil {
		t.Fatal(err)
	}
}

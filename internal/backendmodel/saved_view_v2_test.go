package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func savedV2FlowState() SavedViewState {
	return SavedViewState{Flow: &SavedFlowViewState{Kind: "flow", Filters: SavedFlowViewFilters{}, Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}
}

func TestSavedViewV2Source5UsesBoundedNativeRead(t *testing.T) {
	r, base, _ := effectiveFiveRelationalFixture(t)
	reader, writer := diagramCountReads(t, r)
	view, err := r.CreateSavedView(t.Context(), base.Project.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Source5", Target: BackendReadTarget{RevisionID: base.Revision.ID}, State: savedV2FlowState(), IdempotencyKey: "native-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Pins.Effective == nil || view.Pins.Effective.BaseRevisionID != base.Revision.ID {
		t.Fatal("effective pins lost")
	}
	if reader.reads.Load() > 25 || writer.reads.Load() > 40 {
		t.Fatalf("source5 view bootstrapped proof bases: reader=%d writer=%d", reader.reads.Load(), writer.reads.Load())
	}
}
func TestSavedViewV2FullDraftExactHistoryAndReplay(t *testing.T) {
	t.Parallel()
	r, base, initial := changeFixture(t)
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: initial.Proposal.ID, ProposalRevisionID: initial.Revision.ID}}
	input := CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Full flow presentation", Target: target, State: savedV2FlowState(), IdempotencyKey: "saved-v2"}
	first, err := r.CreateSavedView(t.Context(), base.Project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.DocumentVersion != SavedViewV2DocumentVersion || first.Pins.Effective == nil || first.Pins.Effective.BaseRevisionID != base.Revision.ID {
		t.Fatalf("v2 exact pins: %+v", first)
	}
	firstBytes, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	id := lineageQueryID(900996)
	next, _ := saveChange(t, r, initial, "saved-v2-later", changeCommand(t, "create_node", `"id":"`+id+`","kind":"service","name":"Later","parentId":null,"attributes":{}`))
	retry, err := r.CreateSavedView(t.Context(), base.Project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	retryBytes, _ := json.Marshal(retry)
	if !bytes.Equal(firstBytes, retryBytes) {
		t.Fatal("saved receipt followed later draft")
	}
	save := SaveSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Saved layout", State: savedV2FlowState(), ExpectedVersion: first.Version, IdempotencyKey: "save-v2"}
	updated, err := r.SaveSavedView(t.Context(), base.Project.ID, first.ID, save)
	if err != nil {
		t.Fatal(err)
	}
	historical, err := r.GetSavedView(t.Context(), base.Project.ID, first.ID, GetSavedViewInput{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	historicalBytes, _ := json.Marshal(historical)
	if !bytes.Equal(firstBytes, historicalBytes) || historical.Target.ChangeProposal.ProposalRevisionID == next.Revision.ID {
		t.Fatal("historical view target moved")
	}
	if updated.Target.ChangeProposal.ProposalRevisionID != initial.Revision.ID {
		t.Fatal("save replaced immutable target")
	}
	save.ExpectedVersion = updated.Version
	save.DocumentVersion = ""
	save.IdempotencyKey = "wrong-tag"
	if _, err := r.SaveSavedView(t.Context(), base.Project.ID, first.ID, save); err == nil {
		t.Fatal("v2 save implicitly used v1")
	}
}
func TestSavedViewV2StrictTagAndStagingRejection(t *testing.T) {
	t.Parallel()
	r, base, initial := changeFixture(t)
	state := savedV2FlowState()
	full := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: initial.Proposal.ID, ProposalRevisionID: initial.Revision.ID}}
	if _, err := r.CreateSavedView(t.Context(), base.Project.ID, CreateSavedViewInput{Name: "legacy", Target: full, State: state, IdempotencyKey: "legacy-full"}); err == nil {
		t.Fatal("v1 accepted full target")
	}
	for _, version := range []string{"null", `""`, `"unknown"`} {
		raw := `{"documentVersion":` + version + `,"name":"x","target":{"revisionId":"` + base.Revision.ID + `"},"state":{"kind":"flow","scope":{},"filters":{"search":"","accessKind":"","reverseAccessKind":""},"selection":null,"positions":[],"collapsedGroupIds":[]},"idempotencyKey":"x"}`
		var input CreateSavedViewInput
		if err := json.Unmarshal([]byte(raw), &input); err == nil {
			t.Fatalf("invalid version %s admitted", version)
		}
	}
	staged := BackendReadTarget{ImportCandidate: &ImportCandidateReadTarget{ImportID: initial.Proposal.ID, ImportVersion: 1, CandidateHash: base.Revision.SemanticHash}}
	if _, err := r.CreateSavedView(t.Context(), base.Project.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "staging", Target: staged, State: state, IdempotencyKey: "staging"}); err == nil {
		t.Fatal("v2 accepted staging")
	}
}

func TestSavedViewV2CASRollbackRestartAndV1ReceiptParity(t *testing.T) {
	t.Parallel()
	r, base, initial := changeFixture(t)
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: initial.Proposal.ID, ProposalRevisionID: initial.Revision.ID}}
	input := CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Exact saved target", Target: target, State: savedV2FlowState(), IdempotencyKey: "v2-create"}
	first, err := r.CreateSavedView(t.Context(), base.Project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	save := SaveSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Version2", State: savedV2FlowState(), ExpectedVersion: 1, IdempotencyKey: "v2-save"}
	second, err := r.SaveSavedView(t.Context(), base.Project.ID, first.ID, save)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.SaveSavedView(t.Context(), base.Project.ID, first.ID, save)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(second)
	b, _ := json.Marshal(replay)
	if !bytes.Equal(a, b) {
		t.Fatal("v2 stale CAS replay changed original receipt")
	}
	stale := save
	stale.IdempotencyKey = "stale-new-key"
	if _, err := r.SaveSavedView(t.Context(), base.Project.ID, first.ID, stale); err == nil {
		t.Fatal("stale saved CAS succeeded")
	}
	if _, err := r.db.W.ExecContext(t.Context(), `CREATE TRIGGER reject_saved_version BEFORE INSERT ON backend_saved_view_versions BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	fail := save
	fail.ExpectedVersion = 2
	fail.Name = "Must roll back"
	fail.IdempotencyKey = "rollback"
	if _, err := r.SaveSavedView(t.Context(), base.Project.ID, first.ID, fail); err == nil {
		t.Fatal("injected failure succeeded")
	}
	current, err := r.GetSavedView(t.Context(), base.Project.ID, first.ID, GetSavedViewInput{})
	if err != nil || current.Version != 2 || current.Name != "Version2" {
		t.Fatalf("saved rollback changed head: %+v %v", current, err)
	}
	path := r.db.Path()
	if err := r.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reopened := NewRepo(db)
	historical, err := reopened.GetSavedView(t.Context(), base.Project.ID, first.ID, GetSavedViewInput{Version: 1})
	if err != nil || historical.Target.ChangeProposal.ProposalRevisionID != initial.Revision.ID {
		t.Fatalf("saved restart lost target: %+v %v", historical, err)
	}
	actual, err := reopened.CreateSavedView(t.Context(), base.Project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := json.Marshal(first)
	actualBytes, _ := json.Marshal(actual)
	if !bytes.Equal(firstBytes, actualBytes) {
		t.Fatal("v2 create replay changed after restart")
	}
	legacyRepo, legacyBase, _, _ := lineageOrdersCommitted(t)
	legacyInput := CreateSavedViewInput{Name: "V1 pinned source", Target: BackendReadTarget{RevisionID: legacyBase.Revision.ID}, State: savedV2FlowState(), IdempotencyKey: "legacy-preserved"}
	legacy, err := legacyRepo.CreateSavedView(t.Context(), legacyBase.Project.ID, legacyInput)
	if err != nil {
		t.Fatal(err)
	}
	legacyBytes, _ := json.Marshal(legacy)
	legacyRetry, err := legacyRepo.CreateSavedView(t.Context(), legacyBase.Project.ID, legacyInput)
	if err != nil {
		t.Fatal(err)
	}
	retryBytes, _ := json.Marshal(legacyRetry)
	if !bytes.Equal(legacyBytes, retryBytes) || bytes.Contains(legacyBytes, []byte(`"effective"`)) {
		t.Fatal("v1 receipt wire changed")
	}
}

func TestSavedViewV2ConcurrentOneWinner(t *testing.T) {
	r, base, draft := changeFixture(t)
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}}
	first, err := r.CreateSavedView(t.Context(), base.Project.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Race", Target: target, State: savedV2FlowState(), IdempotencyKey: "race-create"})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var work sync.WaitGroup
	for i := range 2 {
		work.Go(func() {
			_, err := r.SaveSavedView(t.Context(), base.Project.ID, first.ID, SaveSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: fmt.Sprint("Layout", i), State: savedV2FlowState(), ExpectedVersion: 1, IdempotencyKey: fmt.Sprint("race", i)})
			results <- err
		})
	}
	work.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("CAS winners=%d", wins)
	}
}

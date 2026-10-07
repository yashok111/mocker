package backendmodel

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"
)

// Review 2026-10-06, cluster C20: change-proposal validation and lifecycle.

// F44: a remove_node with no artifact command in the batch left an editor
// binding pointing at a missing node; Apply accepted it and only a later
// Rebase refused the draft.
func TestChangeProposalRemoveNodeRejectsDanglingArtifactBinding(t *testing.T) {
	t.Parallel()
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "dangling binding", BaseRevisionID: base.Revision.ID, IdempotencyKey: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewV7().String()
	pin := scenarioSet(base, ids, scenario).Commands[0]
	bindings := []EditorBindingInput{{Selector: EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{id}}}
	d, _ = saveChange(t, r, d, "pin", changeCreateNode(t, id, "service", nil, map[string]any{}), changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": pin.Artifact, "revisionId": pin.RevisionID, "editorBindings": bindings}))
	remove := changeMapCommand(t, "remove_node", map[string]any{"id": id})
	preview, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{remove}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash != nil || len(preview.Diagnostics) == 0 {
		t.Fatalf("removed node kept a live artifact binding: %+v", preview)
	}
	// The same batch with the pin removed too is the documented repair.
	repair := changeMapCommand(t, "remove_artifact_pin", map[string]any{"artifact": pin.Artifact})
	repaired, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{remove, repair}})
	if err != nil {
		t.Fatal(err)
	}
	if repaired.CandidateHash == nil {
		t.Fatalf("explicit pin removal did not repair: %+v", repaired.Diagnostics)
	}
}

// F38: the rebase annotation of a draft survived into every later Apply and
// Restore revision, which then claimed to be a rebase output.
func TestChangeProposalLaterRevisionsDropRebaseAnnotation(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: base.Revision.ID}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("rebase preview: %+v %v", preview, err)
	}
	rebased, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: "rebase"})
	if err != nil {
		t.Fatal(err)
	}
	if rebased.Revision.Rebase == nil {
		t.Fatal("rebase result lost its own annotation")
	}
	applied, _ := saveChange(t, r, &ChangeProposalDetail{Proposal: rebased.Proposal, Revision: rebased.Revision}, "after-rebase", changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}}))
	if applied.Revision.Rebase != nil {
		t.Fatalf("apply revision inherited the rebase action: %+v", applied.Revision.Rebase)
	}
	restored, err := r.RestoreChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, RestoreChangeProposalInput{ExpectedVersion: applied.Proposal.Version, ProposalRevisionID: applied.Revision.ID, RestoreRevisionID: rebased.Revision.ID, IdempotencyKey: "restore"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision.Rebase != nil {
		t.Fatalf("restore revision inherited the rebase action: %+v", restored.Revision.Rebase)
	}
	stored, err := loadChangeProposalRevision(t.Context(), r.db.R, base.Project.ID, d.Proposal.ID, restored.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Rebase != nil {
		t.Fatal("persisted restore revision claims a rebase")
	}
}

// F39: names were validated trimmed but stored raw, so padding became part of
// the desired name, the semantic hash and the proposal name.
func TestChangeProposalStoresNormalizedNames(t *testing.T) {
	t.Parallel()
	r, base, _ := changeFixture(t)
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "  Padded proposal  ", BaseRevisionID: base.Revision.ID, IdempotencyKey: "padded"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Proposal.Name != "Padded proposal" {
		t.Fatalf("proposal name stored raw: %q", d.Proposal.Name)
	}
	id := uuid.NewV7().String()
	create := changeMapCommand(t, "create_node", map[string]any{"id": id, "kind": "service", "name": "  Orders  ", "parentId": nil, "attributes": map[string]any{}})
	d, _ = saveChange(t, r, d, "create", create)
	nodeName := func() string {
		for _, n := range changeReadSnapshot(t, r, d).Nodes {
			if n.ID == id {
				return n.Name
			}
		}
		t.Fatal("created node missing")
		return ""
	}
	if got := nodeName(); got != "Orders" {
		t.Fatalf("create stored raw name %q", got)
	}
	rename := changeMapCommand(t, "rename", map[string]any{"recordType": "node", "id": id, "name": "\tOrders "})
	preview, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{rename}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.SemanticHash == nil || *preview.SemanticHash != d.Revision.SemanticHash {
		t.Fatal("a padded rename of the same name changed desired meaning")
	}
}

// F47: a whitespace-only reason passed validation and became the stored "why".
func TestChangeProposalRejectsBlankReasons(t *testing.T) {
	t.Parallel()
	id := uuid.NewV7().String()
	c := changeCreateNode(t, id, "service", nil, map[string]any{})
	for _, reason := range []string{" ", "\t\n", " "} {
		c.Reason = reason
		if err := validateChangeCommands([]ChangeProposalCommand{c}); err == nil {
			t.Fatalf("blank command reason %q admitted", reason)
		}
	}
	criteria := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "runtime", "kind": "runtime_check", "required": false, "description": "  ", "targetIds": []string{}}}})
	if err := validateChangeCommands([]ChangeProposalCommand{criteria}); err == nil {
		t.Fatal("blank criterion description admitted")
	}
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: 1, ProposalRevisionID: uuid.NewV7().String(), NewBaseRevisionID: uuid.NewV7().String(), Resolutions: []ChangeRebaseResolution{{ConflictID: strings.Repeat("a", 64), Choice: "keep_proposal", Reason: " "}}}
	if err := validateChangeRebaseInput(in); err == nil {
		t.Fatal("blank resolution reason admitted")
	}
}

// F48: an edit of an archived proposal answered 422 backend_unsupported_scope
// instead of the 409 status conflict the lifecycle arms use.
func TestChangeProposalArchivedEditIsStatusConflict(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	archived, err := r.ApplyChangeProposalLifecycle(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalLifecycleInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Action: "archive", IdempotencyKey: "archive"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: archived.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}})}})
	if fault := assertFault(t, err, "backend_change_status_conflict"); fault.Status != 409 {
		t.Fatalf("status = %d", fault.Status)
	}
	_, err = r.ListChangeProposals(t.Context(), base.Project.ID, ChangeProposalListInput{Status: "bogus"})
	fault := assertFault(t, err, "backend_unsupported_scope")
	for _, status := range []string{"draft", "ready", "implemented", "archived"} {
		if !strings.Contains(fault.Message, status) {
			t.Fatalf("list error %q omits supported status %s", fault.Message, status)
		}
	}
}

// F43: archived proposals counted against the active-proposal cap forever,
// so archiving - the documented end of life - freed nothing.
func TestChangeProposalArchivedProposalsDoNotHoldActiveSlots(t *testing.T) {
	r, base, d := changeFixture(t)
	err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		for i := 1; i < MaxChangeProposals; i++ {
			p := d.Proposal
			p.ID = uuid.NewV7().String()
			p.CurrentDraftRevisionID = uuid.NewV7().String()
			rev := d.Revision
			rev.ID, rev.ProposalID, rev.AcceptedBatchRevisionID = p.CurrentDraftRevisionID, p.ID, p.CurrentDraftRevisionID
			stamp := p.CreatedAt.Format(time.RFC3339Nano)
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposals (`+changeProposalColumns+`) VALUES(?,?,?,?,?,?,?,?,?,NULL)`, p.ID, p.ProjectID, p.Name, p.Version, "archived", p.CurrentDraftRevisionID, p.CurrentDraftHash, stamp, stamp); err != nil {
				return err
			}
			if err := persistChangeRevision(t.Context(), tx, p, rev, "create", "", []ChangeProposalCommand{}, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "After archive", BaseRevisionID: base.Revision.ID, IdempotencyKey: "after-archive"}); err != nil {
		t.Fatalf("archived proposals still hold active slots: %v", err)
	}
	// Two active proposals out of 101 rows: the count is of active ones, and
	// the refusal names what frees a slot.
	var active int
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_change_proposals WHERE project_id=? AND status<>'archived'`, base.Project.ID).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active = %d %v", active, err)
	}
	fault := &FaultError{}
	if quota := changeQuotaFault(MaxChangeProposals+1, 1, 0); !errors.As(quota, &fault) || !strings.Contains(fault.Message, "archive") {
		t.Fatalf("quota refusal names no remedy: %v", quota)
	}
}

// F42: applying a rebase whose candidate still had unresolved conflicts
// answered 422 backend_change_invalid with an empty diagnostics list.
func TestChangeRebaseApplyWithUnresolvedConflictsNamesThem(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	node := changeReadSnapshot(t, r, d).Nodes[0]
	d, _ = saveChange(t, r, d, "rename", changeMapCommand(t, "rename", map[string]any{"recordType": "node", "id": node.ID, "name": "Retain"}))
	next := rebaseDeletedSource(t, r, base, node.ID)
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: next.Revision.ID}
	unresolved, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved.Conflicts) != 1 || unresolved.CandidateHash != nil {
		t.Fatalf("fixture needs one unresolved conflict: %+v", unresolved)
	}
	_, err = r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: strings.Repeat("0", 64), IdempotencyKey: "unresolved"})
	fault := assertFault(t, err, "backend_change_rebase_conflict")
	ids, _ := fault.Details["conflictIds"].([]string)
	if fault.Status != 409 || !slices.Equal(ids, []string{unresolved.Conflicts[0].ID}) {
		t.Fatalf("conflict refusal does not name the conflicts: %+v", fault)
	}
}

// F41: the historical-reference walk ranged over a map and returned the first
// failure, so the one diagnostic reported changed between identical calls.
func TestChangeProposalHistoricalReferenceDiagnosticIsDeterministic(t *testing.T) {
	t.Parallel()
	r, d, ids := changeRelationalFixture(t)
	migration := func(revisionID string) (string, []ChangeProposalCommand) {
		id := uuid.NewV7().String()
		facet := changeDesiredFacet(map[string]any{"order": known(1), "parentIds": []string{}, "definition": "opaque migration text", "derivationStatus": "complete", "changes": []any{map[string]any{"target": map[string]any{"kind": "historical", "revisionId": revisionID, "objectId": uuid.NewV7().String()}, "operation": "drop", "description": "Removed historical table"}}})
		return id, []ChangeProposalCommand{changeCreateNode(t, id, "migration", new(ids["database"]), changeFacets(facet)), changeContains(t, ids["database"], id)}
	}
	// One unknown revision and one real ancestor with an unknown object fail
	// with different messages.
	_, first := migration(uuid.NewV7().String())
	_, second := migration(d.Revision.BaseRevisionID)
	commands := append(first, second...)
	messages := map[string]bool{}
	for range 24 {
		preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: commands})
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(preview.Diagnostics, func(x ImportDiagnostic) bool { return x.Path == "historicalReferences" })
		if i < 0 {
			t.Fatalf("missing historical diagnostic: %+v", preview.Diagnostics)
		}
		messages[preview.Diagnostics[i].Message] = true
	}
	if len(messages) != 1 {
		t.Fatalf("identical previews reported different diagnostics: %v", messages)
	}
}

// F40: a limit fault while reading a referenced source became a desired-graph
// diagnostic, telling the caller its commands were invalid.
func TestChangeProposalReferenceLimitIsNotADiagnostic(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	nextInput := source6Input(t, &base.Project)
	nextInput.IdempotencyKey = "second-source"
	nextInput.Manifest.RepositoryName = "second"
	_, next := commitSource6Fixture(t, r, &base.Project, nextInput)
	source, err := r.ResolveSourceGraph(t.Context(), base.Project.ID, next.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	baseline := changeReadSnapshot(t, r, d)
	snapshot := source.State.Sources[0]
	file := snapshot.Files[0]
	attachment := map[string]any{"kind": "source", "revisionId": next.Revision.ID, "repositoryId": snapshot.RepositoryID, "snapshotId": snapshot.ID, "file": file.Path, "contentHash": file.ContentHash, "startLine": 1, "endLine": 1}
	command := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "test", "kind": "test_attachment", "required": true, "description": "Attached source", "targetIds": []string{baseline.Nodes[0].ID}, "attachment": attachment}}})
	// Hold exactly the transient room the preview's initial reservation needs,
	// so admitting the referenced revision's bytes is refused.
	inputBytes := func() int64 {
		tx, err := r.db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		var draftBytes int64
		if err = tx.QueryRowContext(t.Context(), `SELECT length(CAST(document AS BLOB)) FROM backend_change_proposal_revisions_documents WHERE id=?`, d.Revision.ID).Scan(&draftBytes); err != nil {
			t.Fatal(err)
		}
		sourceBytes, err := changeSourceInputBytes(t.Context(), tx, base.Project.ID, d.Revision.BaseRevisionID)
		if err != nil {
			t.Fatal(err)
		}
		ledger, err := changeIdentityInputBytes(t.Context(), tx, base.Project.ID, d.Proposal.ID)
		if err != nil {
			t.Fatal(err)
		}
		historical, err := changeHistoricalIdentityBytes(t.Context(), tx, base.Project.ID, d.Proposal.ID, d.Revision.ID)
		if err != nil {
			t.Fatal(err)
		}
		return sourceBytes + draftBytes + ledger + historical
	}()
	var held interface{ Release() }
	if err = r.db.Write(t.Context(), func(*sql.Tx) error {
		reservation, ok := r.db.ReserveTransient("backend:"+base.Project.ID, MaxProjectStagingBytes-(4*inputBytes+MaxChangeProposalCommandBytes), MaxProjectStagingBytes)
		if !ok {
			t.Fatal("reservation fixture failed")
		}
		held = reservation
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	preview, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{command}})
	if err == nil {
		t.Fatalf("budget refusal reported as diagnostics: %+v", preview.Diagnostics)
	}
	// The reader's own refusal, not the later admission of the prepared
	// candidate, which a diagnostic-shaped refusal would also reach.
	if fault := assertFault(t, err, "backend_import_limit"); fault.Status != 413 || fault.Message != "Combined proposal readers exceed project transient budget" {
		t.Fatalf("fault = %d %q", fault.Status, fault.Message)
	}
}

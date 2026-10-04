package backendmodel

import (
	"encoding/json/v2"
	"fmt"
	"testing"
)

func TestChangeRebaseSameBaseReceiptAndHistory(t *testing.T) {
	r, base, d := changeFixture(t)
	before := immutableBytes(t, r)
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: base.Revision.ID}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash == nil || len(preview.Conflicts) != 0 || len(preview.Diagnostics) != 0 {
		t.Fatalf("invalid same-base preview: %+v", preview)
	}
	apply := ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: "rebase-same"}
	out, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	if out.Proposal.Version != 2 || out.Revision.ID == d.Revision.ID || out.Revision.ParentRevisionID == nil || *out.Revision.ParentRevisionID != d.Revision.ID || out.Proposal.Status != "draft" {
		t.Fatalf("not a versioned rebase: %+v", out)
	}
	var action, commands string
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT action,commands FROM backend_change_proposal_batches WHERE revision_id=?`, out.Revision.ID).Scan(&action, &commands); err != nil {
		t.Fatal(err)
	}
	if action != "rebase" || commands != "[]" {
		t.Fatalf("wrong rebase batch %s %s", action, commands)
	}
	for k, v := range before {
		if immutableBytes(t, r)[k] != v {
			t.Fatalf("changed historical source %s", k)
		}
	}
	later := &ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision}
	saveChange(t, r, later, "later", changeCommand(t, "set_criteria", `"criteria":[]`))
	replay, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(out)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("receipt changed after draft advancement")
	}
}

func TestChangeRebaseNewSourcePreservesDesiredName(t *testing.T) {
	r, base, d := changeFixture(t)
	graph := changeReadSnapshot(t, r, d)
	id := graph.Nodes[0].ID
	d, _ = saveChange(t, r, d, "rename", changeCommand(t, "rename", fmt.Sprintf(`"recordType":"node","id":%q,"name":"Desired"`, id)))
	nextInput := source6Input(t, &base.Project)
	nextInput.IdempotencyKey = "second-source"
	nextInput.Manifest.RepositoryName = "second"
	_, next := commitSource6Fixture(t, r, &base.Project, nextInput)
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: next.Revision.ID}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash == nil {
		t.Fatalf("blocked rebase: %+v", preview)
	}
	out, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: "rebase"})
	if err != nil {
		t.Fatal(err)
	}
	merged := changeReadSnapshot(t, r, &ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision})
	if len(merged.Nodes) != 2 {
		t.Fatalf("lost new source: %+v", merged.Nodes)
	}
	for _, n := range merged.Nodes {
		if n.ID == id && n.Name != "Desired" {
			t.Fatalf("lost intent: %+v", n)
		}
	}
	if out.Revision.BaseRevisionID != next.Revision.ID {
		t.Fatal("wrong new baseline")
	}
}

func rebaseDeletedSource(t *testing.T, r *Repo, base *ImportCommitResult, id string) *ImportCommitResult {
	t.Helper()
	graph, err := r.ResolveSourceGraph(t.Context(), base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	identity := graph.Identities[0]
	in := source6Input(t, &base.Project)
	in.IdempotencyKey = "delete-source"
	in.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: identity.RepositoryID, ProviderNamespace: identity.ProviderNamespace}
	s, err := r.BeginImport(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	batch := sendCommands(t, r, &base.Project, s, 1, "delete", ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: identity.ExternalKey, ExpectedID: id, Reason: "source removed"}})
	return commitStaged(t, r, &base.Project, s, batch.AcceptedVersion, "delete")
}
func TestChangeRebaseHistoricalImportedIDsRemainReserved(t *testing.T) {
	r, base, d := changeFixture(t)
	node := changeReadSnapshot(t, r, d).Nodes[0]
	next := rebaseDeletedSource(t, r, base, node.ID)
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: next.Revision.ID}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash == nil {
		t.Fatalf("unexpected conflict: %+v", preview)
	}
	out, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: "rebase"})
	if err != nil {
		t.Fatal(err)
	}
	command := changeCreateNode(t, node.ID, node.Kind, nil, map[string]any{})
	_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: out.Proposal.Version, ProposalRevisionID: out.Revision.ID, Commands: []ChangeProposalCommand{command}})
	assertFault(t, err, "backend_change_identity_conflict")
}
func TestChangeRebasePersistedCarryAndResolutionHash(t *testing.T) {
	r, base, d := changeFixture(t)
	node := changeReadSnapshot(t, r, d).Nodes[0]
	d, _ = saveChange(t, r, d, "rename", changeMapCommand(t, "rename", map[string]any{"recordType": "node", "id": node.ID, "name": "Retain"}))
	next := rebaseDeletedSource(t, r, base, node.ID)
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: next.Revision.ID}
	unresolved, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved.Conflicts) != 1 {
		t.Fatalf("missing delete/edit: %+v", unresolved)
	}
	in.Resolutions = []ChangeRebaseResolution{{ConflictID: unresolved.Conflicts[0].ID, Choice: "keep_proposal", Reason: "Keep for desired design"}}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash == nil {
		t.Fatalf("carry rejected: %+v", preview)
	}
	out, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: "carry"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: out.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Identities) != 1 || snapshot.Identities[0].Target.Kind != "carried_source_identity" {
		t.Fatalf("lost carry: %+v", snapshot.Identities)
	}
	if snapshot.Pins.EffectiveSemanticHash != *preview.SemanticHash {
		t.Fatal("apply stamping changed semantic hash")
	}
	for _, origin := range snapshot.Origins {
		if origin.CommandID != "" {
			continue
		}
		if origin.RebaseResolution == nil || origin.RebaseResolution.ProposalRevisionID != out.Revision.ID {
			t.Fatalf("wrong resolution author: %+v", origin)
		}
	}
	// The unchanged analysis bridge must admit and expose the saved carry as
	// desired, including after selector serialization used by persisted jobs.
	footprint, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, base.Revision.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: out.Revision.ID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	leased, lease, err := r.ReserveAnalysisInput(t.Context(), base.Project.ID, footprint)
	if err != nil {
		t.Fatal(err)
	}
	frozen, resolveErr := r.ResolveEffectiveGraph(leased, base.Project.ID, snapshot.Target)
	lease.Release()
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if frozen.Pins.TargetHash != snapshot.Pins.TargetHash {
		t.Fatal("leased analysis changed saved carry pin")
	}
	selectorRaw, err := json.Marshal(changeIdentitySelector(snapshot.Identities[0].Target))
	if err != nil {
		t.Fatal(err)
	}
	var selector EffectivePropertySelector
	if err = json.Unmarshal(selectorRaw, &selector); err != nil {
		t.Fatal(err)
	}
	proof, err := EffectivePropertyAnalysisProof(frozen, ChangeRecordRef{RecordType: "node", ID: node.ID}, selector)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "desired" || len(proof.Assertions) != 0 || len(proof.EvidenceIDs) != 0 {
		t.Fatalf("analysis promoted carry proof: %+v", proof)
	}
	target := snapshot.Identities[0].Target
	command := changeMapCommand(t, "map_identity", map[string]any{"target": target, "expectedExternalKey": *snapshot.Identities[0].ExternalKey, "newExternalKey": "desired-carried"})
	saveChange(t, r, &ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision}, "carried-map", command)
}

func TestChangeRebaseRepeatedCarryKeepsAuthorshipAndResolvesCurrentClaim(t *testing.T) {
	r, base, d := changeFixture(t)
	id := changeReadSnapshot(t, r, d).Nodes[0].ID
	d, _ = saveChange(t, r, d, "desired", changeMapCommand(t, "rename", map[string]any{"recordType": "node", "id": id, "name": "Desired"}))
	next := rebaseDeletedSource(t, r, base, id)
	apply := func(d *ChangeProposalDetail, baseID, key string, resolve bool) *ChangeProposalDetail {
		t.Helper()
		in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: baseID}
		preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if resolve {
			for _, conflict := range preview.Conflicts {
				in.Resolutions = append(in.Resolutions, ChangeRebaseResolution{ConflictID: conflict.ID, Choice: "keep_proposal", Reason: "Keep desired historical intent"})
			}
		}
		preview, err = r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if preview.CandidateHash == nil {
			t.Fatalf("invalid rebase %+v", preview)
		}
		out, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return &ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision}
	}
	carried := apply(d, next.Revision.ID, "carry", true)
	repeated := apply(carried, next.Revision.ID, "repeat", false)
	for _, origin := range changeReadSnapshot(t, r, repeated).Origins {
		if origin.Origin.CommandID != "" {
			continue
		}
		if origin.Origin.RebaseResolution == nil || origin.Origin.RebaseResolution.ProposalRevisionID != carried.Revision.ID {
			t.Errorf("retained resolution authorship rewritten: %+v", origin)
		}
	}
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: repeated.Proposal.Version, ProposalRevisionID: repeated.Revision.ID, NewBaseRevisionID: base.Revision.ID}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	identityConflict := false
	for _, c := range preview.Conflicts {
		if c.Selector.Kind == "identity" {
			identityConflict = true
		}
	}
	if !identityConflict {
		t.Fatalf("carry silently promoted to returning source: %+v", preview)
	}
	returned := apply(repeated, base.Revision.ID, "return-keep", true)
	identities := changeReadSnapshot(t, r, returned).Identities
	if len(identities) != 1 || identities[0].Target.Kind != "carried_source_identity" {
		t.Fatalf("carry/current duplicated or silently promoted: %+v", identities)
	}
}

func TestChangeRebaseDeletedIdentityHonorsTakeSourceAndCriterionFootprint(t *testing.T) {
	r, base, d := changeFixture(t)
	graph, err := r.ResolveSourceGraph(t.Context(), base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	identity := graph.Identities[0]
	id := identity.ID
	target := ChangeIdentityTarget{Kind: "source_identity", Source: new(identity)}
	criterion := ChangeCriterion{Key: "name", Kind: "field_equals", Required: true, Description: "Desired name", RecordType: "node", ID: id, Selector: mustChangeJSON(EffectivePropertySelector{Kind: "source", Source: &TypedSourcePropertySelector{Kind: "name"}}), Expected: &SourcePropertyValue{Present: true, Value: mustChangeJSON("Desired")}}
	d, _ = saveChange(t, r, d, "intent", changeMapCommand(t, "rename", map[string]any{"recordType": "node", "id": id, "name": "Desired"}), changeMapCommand(t, "map_identity", map[string]any{"target": target, "expectedExternalKey": identity.ExternalKey, "newExternalKey": "desired-key"}), changeMapCommand(t, "set_criteria", map[string]any{"criteria": []ChangeCriterion{criterion}}))
	next := rebaseDeletedSource(t, r, base, id)
	in := PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: next.Revision.ID}
	first, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range first.Conflicts {
		choice := "keep_proposal"
		if conflict.Selector.Kind == "identity" {
			choice = "take_source"
		}
		in.Resolutions = append(in.Resolutions, ChangeRebaseResolution{ConflictID: conflict.ID, Choice: choice, Reason: "Explicit final design"})
	}
	preview, err := r.PreviewChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CandidateHash == nil {
		t.Fatalf("resolved rebase blocked: %+v", preview)
	}
	out, err := r.ApplyChangeProposalRebase(t.Context(), base.Project.ID, d.Proposal.ID, ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *preview.CandidateHash, IdempotencyKey: "rebase"})
	if err != nil {
		t.Fatal(err)
	}
	snap := changeReadSnapshot(t, r, &ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision})
	if len(snap.Identities) != 1 || snap.Identities[0].Target.Kind != "carried_source_identity" || snap.Identities[0].ExternalKey != nil {
		t.Fatalf("take_source ignored or historical identity lost: %+v", snap.Identities)
	}
	if len(snap.Criteria) != 1 || snap.Criteria[0].Key != "name" {
		t.Fatal("criterion lost")
	}
}

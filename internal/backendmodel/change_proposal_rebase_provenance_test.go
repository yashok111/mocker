package backendmodel

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestChangeRebaseCarriedIdentityCodec(t *testing.T) {
	raw := `{"kind":"carried_source_identity","source":{"recordType":"node","id":"01900000-0000-7000-8000-000000000001","repositoryId":"01900000-0000-7000-8000-000000000002","providerNamespace":"go","externalKey":"service","assertionHash":"` + strings.Repeat("a", 64) + `"},"basis":{"revisionId":"01900000-0000-7000-8000-000000000003","semanticHash":"` + strings.Repeat("b", 64) + `"}}`
	var target ChangeIdentityTarget
	if err := json.Unmarshal([]byte(raw), &target); err != nil {
		t.Fatalf("historical source identity rejected: %v", err)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["kind"] != "carried_source_identity" || got["basis"] == nil {
		t.Fatalf("carry promoted to current source: %s", encoded)
	}
	var mixed ChangeIdentityTarget
	if json.Unmarshal([]byte(strings.Replace(raw, `"carried_source_identity"`, `"source_identity"`, 1)), &mixed) == nil {
		t.Fatal("current source target accepted historical basis")
	}
}

func TestChangeRebaseKeepsDeletedSourceAsHistoricalCarry(t *testing.T) {
	t.Parallel()
	r, _, d := changeFixture(t)
	source, err := loadChangeSourceForDraft(t.Context(), r.db.R, d.Proposal.ProjectID, &d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	base, err := newChangeEvaluation(source, d.Revision, map[string]ChangeObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	ours, err := newChangeEvaluation(source, d.Revision, map[string]ChangeObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	id := source.State.Nodes[0].ID
	c := changeMapCommand(t, "rename", map[string]any{"recordType": "node", "id": id, "name": "Keep me"})
	if err = ours.apply(c); err != nil {
		t.Fatal(err)
	}
	next := *source
	next.State = source.State
	next.State.Nodes = nil
	next.Identities = nil
	next.State.Revision.ID = "01900000-0000-7000-8000-000000000099"
	next.State.Revision.SemanticHash = strings.Repeat("d", 64)
	nextDraft := d.Revision
	nextDraft.BaseRevisionID = next.State.Revision.ID
	nextDraft.BaseSemanticHash = next.State.Revision.SemanticHash
	build := func(resolutions map[string]ChangeRebaseResolution) (*changeRebaseMerge, *changeEvaluation) {
		t.Helper()
		result, err := newChangeEvaluation(&next, nextDraft, map[string]ChangeObjectIdentity{})
		if err != nil {
			t.Fatal(err)
		}
		m := &changeRebaseMerge{draft: d.Revision, next: &next, resolutions: resolutions, seen: map[string]bool{}}
		if err = m.records(base, ours, result); err != nil {
			t.Fatal(err)
		}
		if err = m.identities(base, ours, result); err != nil {
			t.Fatal(err)
		}
		return m, result
	}
	m, _ := build(nil)
	if len(m.conflicts) != 1 || m.conflicts[0].Selector.Kind != "record" {
		t.Fatalf("want delete/edit conflict: %+v", m.conflicts)
	}
	resolution := ChangeRebaseResolution{ConflictID: m.conflicts[0].ID, Choice: "keep_proposal", Reason: "Keep deleted imported service"}
	m, result := build(map[string]ChangeRebaseResolution{resolution.ConflictID: resolution})
	if len(m.conflicts) != 0 {
		t.Fatalf("unresolved: %+v", m.conflicts)
	}
	snap, err := result.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Identities) != len(source.Identities) {
		t.Fatalf("historical provider identities lost: %+v", snap.Identities)
	}
	for _, identity := range snap.Identities {
		if identity.Target.Kind != "carried_source_identity" || identity.Target.Basis == nil || identity.Target.Basis.RevisionID != d.Revision.BaseRevisionID {
			t.Fatalf("imported ID promoted: %+v", identity)
		}
	}
	for _, origin := range snap.Origins {
		if origin.Selector.Source != nil && origin.Selector.Source.Kind == "name" {
			if origin.Origin.CommandID != c.CommandID || origin.Origin.RebaseResolution != nil {
				t.Fatalf("retained desired name lost command authorship: %+v", origin)
			}
			continue
		}
		if origin.Origin.Kind != "intent" || origin.Origin.CommandID != "" || origin.Origin.RebaseResolution == nil {
			t.Fatalf("not resolution-authored intent: %+v", origin)
		}
		projected := effectiveEvaluationOrigin(&next, origin)
		if len(projected.SourceClaims) != 0 || len(projected.EvidenceIDs) != 0 || projected.RebaseResolution == nil {
			t.Fatalf("historical carry acquired source proof: %+v", projected)
		}
	}
	if len(result.newIDs) != 1 || result.newIDs[0].AllocationKind != "carried_source" {
		t.Fatalf("missing explicit carry allocation: %+v", result.newIDs)
	}
}

func TestChangeRebaseResolutionOriginRejectsMixedAuthorship(t *testing.T) {
	raw := `{"kind":"intent","commandId":"01900000-0000-7000-8000-000000000001","reason":"command","rebaseResolution":{"proposalRevisionId":"01900000-0000-7000-8000-000000000002","resolutionId":"` + strings.Repeat("a", 64) + `","reason":"resolution"}}`
	var origin EffectiveOrigin
	if json.Unmarshal([]byte(raw), &origin) == nil {
		t.Fatal("mixed command/resolution authorship accepted")
	}
}

func TestChangeRebaseCarryHashIncludesBasisButExcludesAuthorship(t *testing.T) {
	source := QualifiedSourceIdentity{RecordType: "node", ID: "01900000-0000-7000-8000-000000000001", RepositoryID: "01900000-0000-7000-8000-000000000002", ProviderNamespace: "go", ExternalKey: "handler", AssertionHash: strings.Repeat("a", 64)}
	basis := CarriedSourceBasis{RevisionID: "01900000-0000-7000-8000-000000000003", SemanticHash: strings.Repeat("b", 64)}
	snapshot := &ChangeEvaluationSnapshot{Identities: []ChangeEvaluationIdentity{{Target: ChangeIdentityTarget{Kind: "carried_source_identity", Source: new(source), Basis: new(basis)}, ExternalKey: new("desired"), Origin: EffectiveOrigin{Kind: "intent", RebaseResolution: &RebaseResolutionOrigin{ProposalRevisionID: "01900000-0000-7000-8000-000000000004", ResolutionID: strings.Repeat("c", 64), Reason: "choice"}}}}}
	original, err := changeSemanticHash(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Identities[0].Origin.RebaseResolution.Reason = "different reason"
	same, err := changeSemanticHash(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if original != same {
		t.Fatal("semantic hash includes resolution authorship")
	}
	snapshot.Identities[0].Target.Basis.RevisionID = "01900000-0000-7000-8000-000000000005"
	changed, err := changeSemanticHash(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if changed == same {
		t.Fatal("semantic hash ignores historical carry basis")
	}
}

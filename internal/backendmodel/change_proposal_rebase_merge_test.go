package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"
)

func rebaseUnitFixture(t *testing.T, schema string, b, o, n SourceAssertionPayload) (*changeRebaseMerge, *changeEvaluation, *changeEvaluation, *changeEvaluation) {
	t.Helper()
	id := "01900000-0000-7000-8000-000000000001"
	makeSource := func(rid string, p SourceAssertionPayload) *SourceGraphSnapshot {
		return &SourceGraphSnapshot{State: RevisionState{Revision: Revision{ID: rid, SchemaVersion: schema, SemanticHash: strings.Repeat("a", 64)}, Nodes: []Node{{ID: id, Kind: p.Kind, Name: p.Name, ParentID: p.ParentID, Attributes: p.Attributes}}}}
	}
	source := makeSource("01900000-0000-7000-8000-000000000002", b)
	next := makeSource("01900000-0000-7000-8000-000000000003", n)
	rev := ChangeProposalRevision{ID: "01900000-0000-7000-8000-000000000004", BaseRevisionID: source.State.Revision.ID, BaseSemanticHash: source.State.Revision.SemanticHash, BaseSchemaVersion: schema, Delta: emptyChangeDelta()}
	base, err := newChangeEvaluation(source, rev, map[string]ChangeObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	ours, err := newChangeEvaluation(source, rev, map[string]ChangeObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if err = ours.replace(ChangeProposalCommand{CommandID: "01900000-0000-7000-8000-000000000005", Reason: "desired"}, ours.records[id], o); err != nil {
		t.Fatal(err)
	}
	nextRev := rev
	nextRev.BaseRevisionID = next.State.Revision.ID
	result, err := newChangeEvaluation(next, nextRev, map[string]ChangeObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	m := &changeRebaseMerge{draft: rev, next: next, resolutions: map[string]ChangeRebaseResolution{}, seen: map[string]bool{}}
	return m, base, ours, result
}

func TestChangeRebaseTypedMergeMatrix(t *testing.T) {
	payload := func(name, description string) SourceAssertionPayload {
		return SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: name, Attributes: map[string]jsontext.Value{"description": mustChangeJSON(description)}}
	}
	for _, schema := range []string{"5", "6"} {
		for _, tc := range []struct {
			name                      string
			b, o, n                   SourceAssertionPayload
			wantName, wantDescription string
			conflicts                 int
		}{
			{"disjoint", payload("B", "B"), payload("O", "B"), payload("B", "N"), "O", "N", 0},
			{"equal", payload("B", "B"), payload("N", "B"), payload("N", "B"), "N", "B", 0},
			{"source only", payload("B", "B"), payload("B", "B"), payload("N", "B"), "N", "B", 0},
			{"conflicting", payload("B", "B"), payload("O", "B"), payload("N", "B"), "N", "B", 1},
		} {
			t.Run(schema+"/"+tc.name, func(t *testing.T) {
				m, b, o, n := rebaseUnitFixture(t, schema, tc.b, tc.o, tc.n)
				if err := m.records(b, o, n); err != nil {
					t.Fatal(err)
				}
				if len(m.conflicts) != tc.conflicts {
					t.Fatalf("conflicts=%+v", m.conflicts)
				}
				for _, record := range n.records {
					var description string
					if err := json.Unmarshal(record.Payload.Attributes["description"], &description); err != nil {
						t.Fatal(err)
					}
					if record.Payload.Name != tc.wantName || description != tc.wantDescription {
						t.Fatalf("wrong merged values: %+v", record)
					}
				}
				if tc.conflicts > 0 {
					conflict := m.conflicts[0]
					if conflict.Selector.Kind != "property" || conflict.Selector.Property.Source.Kind != "name" {
						t.Fatalf("wrong typed conflict: %+v", conflict)
					}
					m, b, o, n = rebaseUnitFixture(t, schema, tc.b, tc.o, tc.n)
					m.resolutions[conflict.ID] = ChangeRebaseResolution{ConflictID: conflict.ID, Choice: "replace", Reason: "resolved", Value: jsontext.Value(`"Replacement"`)}
					if err := m.records(b, o, n); err != nil {
						t.Fatal(err)
					}
					for _, r := range n.records {
						if r.Payload.Name != "Replacement" {
							t.Fatal("typed replacement missing")
						}
					}
					if len(n.revision.Delta.Properties) != 1 || n.revision.Delta.Properties[0].Origin.RebaseResolution == nil {
						t.Fatal("replacement lacks resolution authorship")
					}
				}
			})
		}
	}
}
func TestChangeRebasePresenceAndOrderedArrayConflicts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		b, o, n ChangeRebaseValue
	}{
		{"absent versus null", ChangeRebaseValue{Presence: "absent_property"}, ChangeRebaseValue{Presence: "null", Value: jsontext.Value(`null`)}, ChangeRebaseValue{Presence: "value", Value: jsontext.Value(`"new"`)}},
		{"ordered arrays atomic", ChangeRebaseValue{Presence: "value", Value: jsontext.Value(`["a","b"]`)}, ChangeRebaseValue{Presence: "value", Value: jsontext.Value(`["b","a"]`)}, ChangeRebaseValue{Presence: "value", Value: jsontext.Value(`["a","b","c"]`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &changeRebaseMerge{next: &SourceGraphSnapshot{}}
			if _, _, err := m.choose(ChangeRecordRef{}, ChangeRebaseSelector{Kind: "property"}, tc.b, tc.o, tc.n, false); err != nil {
				t.Fatal(err)
			}
			if len(m.conflicts) != 1 || m.conflicts[0].Base.Presence != tc.b.Presence || string(m.conflicts[0].Ours.Value) != string(tc.o.Value) {
				t.Fatalf("lost presence/whole array: %+v", m.conflicts)
			}
		})
	}
}
func TestChangeRebaseRetargetsEveryProviderToExactNewAssertion(t *testing.T) {
	p := SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "Handler", Attributes: map[string]jsontext.Value{}}
	m, b, o, n := rebaseUnitFixture(t, "6", p, p, p)
	id := "01900000-0000-7000-8000-000000000001"
	repository := "01900000-0000-7000-8000-000000000010"
	for _, provider := range []string{"a", "b"} {
		old := QualifiedSourceIdentity{RecordType: "node", ID: id, RepositoryID: repository, ProviderNamespace: provider, ExternalKey: provider, AssertionHash: strings.Repeat("b", 64)}
		b.source.Identities = append(b.source.Identities, old)
		next := old
		next.AssertionHash = strings.Repeat("c", 64)
		n.source.Identities = append(n.source.Identities, next)
		if provider == "a" {
			o.revision.Delta.IdentityIntents = append(o.revision.Delta.IdentityIntents, ChangeIdentityIntent{Target: ChangeIdentityTarget{Kind: "source_identity", Source: new(old)}, ExternalKey: new("desired"), Origin: EffectiveOrigin{Kind: "intent", CommandID: "01900000-0000-7000-8000-000000000005", Reason: "keep key"}})
		}
	}
	if err := m.identities(b, o, n); err != nil {
		t.Fatal(err)
	}
	if len(m.conflicts) != 0 {
		t.Fatalf("new assertion hash invented identity conflict: %+v", m.conflicts)
	}
	got, err := n.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Identities) != 2 {
		t.Fatalf("providers lost: %+v", got.Identities)
	}
	for _, identity := range got.Identities {
		if identity.Target.Source.AssertionHash != strings.Repeat("c", 64) {
			t.Fatal("identity kept old assertion")
		}
		if identity.Target.Source.ProviderNamespace == "a" && *identity.ExternalKey != "desired" {
			t.Fatal("desired key lost")
		}
	}
	n.source.Identities[0].ExternalKey = "source-renamed"
	n.revision.Delta.IdentityIntents = nil
	m.conflicts = nil
	if err = m.identities(b, o, n); err != nil {
		t.Fatal(err)
	}
	if len(m.conflicts) != 1 || m.conflicts[0].Selector.Kind != "identity" {
		t.Fatalf("competing external key change not conflicted: %+v", m.conflicts)
	}
}
func TestChangeRebaseTypedCorrespondenceDoesNotRewriteNativeText(t *testing.T) {
	old := "01900000-0000-7000-8000-000000000001"
	next := "01900000-0000-7000-8000-000000000002"
	p := SourceAssertionPayload{RecordType: "node", Kind: "flow_step", Name: "Step", ParentID: new(old), Attributes: runtimeStepAttrs(t, "opaque")}
	p.Attributes["nativeText"] = mustChangeJSON(old)
	remapped, err := rebaseRemapPayload(p, map[string]string{old: next})
	if err != nil {
		t.Fatal(err)
	}
	if remapped.ParentID == nil || *remapped.ParentID != next || !slices.Equal(remapped.Attributes["nativeText"], p.Attributes["nativeText"]) {
		t.Fatalf("typed remap rewrote opaque text: %+v", remapped)
	}
}

func TestChangeRebaseCorrespondencePreservesDesiredQualifiedKey(t *testing.T) {
	p := SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "Handler", Attributes: map[string]jsontext.Value{}}
	m, b, o, n := rebaseUnitFixture(t, "6", p, p, p)
	oldID := "01900000-0000-7000-8000-000000000001"
	newID := "01900000-0000-7000-8000-000000000011"
	identity := QualifiedSourceIdentity{RecordType: "node", ID: oldID, RepositoryID: "01900000-0000-7000-8000-000000000010", ProviderNamespace: "provider", ExternalKey: "old", AssertionHash: strings.Repeat("b", 64)}
	b.source.Identities = []QualifiedSourceIdentity{identity}
	next := identity
	next.ID = newID
	next.AssertionHash = strings.Repeat("c", 64)
	n.source.Identities = []QualifiedSourceIdentity{next}
	n.source.State.Nodes[0].ID = newID
	record := n.records[oldID]
	delete(n.records, oldID)
	record.ID = newID
	n.records[newID] = record
	o.revision.Delta.IdentityIntents = []ChangeIdentityIntent{{Target: ChangeIdentityTarget{Kind: "source_identity", Source: new(identity)}, ExternalKey: new("desired"), Origin: EffectiveOrigin{Kind: "intent", CommandID: "01900000-0000-7000-8000-000000000005", Reason: "desired key"}}}
	m.input.IdentityResolutions = []ChangeRebaseIdentityResolution{{OldSourceID: oldID, NewSourceID: newID, Reason: "Explicit correspondence"}}
	if err := m.correspondence(b, o, n); err != nil {
		t.Fatal(err)
	}
	if err := m.records(b, o, n); err != nil {
		t.Fatal(err)
	}
	if err := m.identities(b, o, n); err != nil {
		t.Fatal(err)
	}
	snap, err := n.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.conflicts) != 0 || len(snap.Identities) != 1 || snap.Identities[0].Target.Source.ID != newID || *snap.Identities[0].ExternalKey != "desired" {
		t.Fatalf("correspondence lost desired qualified key: %+v conflicts=%+v", snap.Identities, m.conflicts)
	}
	if b.source.Identities[0].ID == oldID { // The original source snapshot must still be exact, even if an internal comparison copy is remapped.
		t.Log("source identity kept exact")
	}
}

func TestChangeRebaseArtifactPinAndBindingConflictIsAtomic(t *testing.T) {
	pin := ArtifactPin{Kind: "design_scenario", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}
	binding := EditorBinding{ArtifactKind: pin.Kind, ArtifactID: pin.ID, Selector: EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{"01900000-0000-7000-8000-000000000001"}, SourceLabels: []string{"Handler"}, ObjectHash: strings.Repeat("b", 64), LastKnownLabel: "Participant", Origin: "manual", Reason: "Association"}
	context := ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("c", 64), SourceSemanticHash: strings.Repeat("d", 64), APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{binding}}
	old := ChangeProposalRevision{ArtifactPins: []ArtifactPin{pin}, ArtifactContext: context, Delta: emptyChangeDelta()}
	b := &changeEvaluation{revision: old, source: &SourceGraphSnapshot{State: RevisionState{Revision: Revision{ArtifactPins: []ArtifactPin{pin}}, ArtifactContext: new(context)}}}
	o := &changeEvaluation{revision: old}
	o.revision.ArtifactContext.EditorBindings = slices.Clone(context.EditorBindings)
	o.revision.ArtifactContext.EditorBindings[0].SourceNodeIDs = []string{"01900000-0000-7000-8000-000000000002"}
	n := &changeEvaluation{revision: old}
	n.revision.ArtifactPins = slices.Clone(old.ArtifactPins)
	n.revision.ArtifactPins[0].RevisionID = "2"
	n.revision.ArtifactPins[0].ContentHash = strings.Repeat("e", 64)
	m := &changeRebaseMerge{next: &SourceGraphSnapshot{}, resolutions: map[string]ChangeRebaseResolution{}, seen: map[string]bool{}}
	if err := m.artifacts(b, o, n); err != nil {
		t.Fatal(err)
	}
	if len(m.conflicts) != 1 || m.conflicts[0].Selector.Kind != "artifact" {
		t.Fatalf("split or missing artifact conflict: %+v", m.conflicts)
	}
	var before, desired, current changeRebaseArtifact
	for _, pair := range []struct {
		value ChangeRebaseValue
		out   *changeRebaseArtifact
	}{{m.conflicts[0].Base, &before}, {m.conflicts[0].Ours, &desired}, {m.conflicts[0].NewSource, &current}} {
		if err := rebaseDecodeValue(pair.value, pair.out); err != nil {
			t.Fatal(err)
		}
	}
	if desired.Pin.RevisionID != "1" || desired.Editor[0].SourceNodeIDs[0] != o.revision.ArtifactContext.EditorBindings[0].SourceNodeIDs[0] || current.Pin.RevisionID != "2" || current.Editor[0].SourceNodeIDs[0] != binding.SourceNodeIDs[0] {
		t.Fatal("pin and binding were independently merged")
	}
}
func TestChangeRebaseCorrespondenceRejectsDuplicateAndKindCollision(t *testing.T) {
	p := SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "Handler", Attributes: map[string]jsontext.Value{}}
	for _, test := range []string{"duplicate", "kind"} {
		t.Run(test, func(t *testing.T) {
			m, b, o, n := rebaseUnitFixture(t, "6", p, p, p)
			old := "01900000-0000-7000-8000-000000000001"
			next := "01900000-0000-7000-8000-000000000011"
			record := n.records[old]
			delete(n.records, old)
			record.ID = next
			if test == "kind" {
				record.Payload.Kind = "service"
			}
			n.records[next] = record
			c := ChangeRebaseIdentityResolution{OldSourceID: old, NewSourceID: next, Reason: "explicit"}
			m.input.IdentityResolutions = []ChangeRebaseIdentityResolution{c}
			if test == "duplicate" {
				m.input.IdentityResolutions = append(m.input.IdentityResolutions, c)
			}
			if err := m.correspondence(b, o, n); err == nil {
				t.Fatal("invalid correspondence accepted")
			}
		})
	}
}

func TestChangeRebaseCorrespondenceRetainedReferenceReloadParity(t *testing.T) {
	child := "01900000-0000-7000-8000-000000000001"
	oldParent := "01900000-0000-7000-8000-000000000011"
	newParent := "01900000-0000-7000-8000-000000000012"
	build := func(resolutions map[string]ChangeRebaseResolution) (*changeRebaseMerge, *changeEvaluation) {
		t.Helper()
		payload := SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "Child", Attributes: map[string]jsontext.Value{}}
		m, b, o, n := rebaseUnitFixture(t, "6", payload, payload, payload)
		parent := ChangeCreatedRecord{ChangeRecordRef: ChangeRecordRef{RecordType: "node", ID: oldParent}, Payload: SourceAssertionPayload{RecordType: "node", Kind: "service", Name: "Parent", Attributes: map[string]jsontext.Value{}}, Origin: EffectiveOrigin{Kind: "source"}}
		b.records[oldParent] = parent
		o.records[oldParent] = parent
		desired := o.records[child]
		desired.Payload.ParentID = new(oldParent)
		if err := o.replace(ChangeProposalCommand{CommandID: "01900000-0000-7000-8000-000000000005", Reason: "Desired parent"}, o.records[child], desired.Payload); err != nil {
			t.Fatal(err)
		}
		n.records = map[string]ChangeCreatedRecord{}
		parent.ID = newParent
		n.records[newParent] = parent
		n.source.State.Nodes = []Node{{ID: newParent, Kind: "service", Name: "Parent", Attributes: map[string]jsontext.Value{}}}
		m.input.IdentityResolutions = []ChangeRebaseIdentityResolution{{OldSourceID: oldParent, NewSourceID: newParent, Reason: "Explicit parent correspondence"}}
		m.resolutions = resolutions
		if err := m.correspondence(b, o, n); err != nil {
			t.Fatal(err)
		}
		if err := m.records(b, o, n); err != nil {
			t.Fatal(err)
		}
		return m, n
	}
	unresolved, _ := build(nil)
	if len(unresolved.conflicts) != 1 {
		t.Fatalf("expected child delete/edit conflict: %+v", unresolved.conflicts)
	}
	resolution := ChangeRebaseResolution{ConflictID: unresolved.conflicts[0].ID, Choice: "keep_proposal", Reason: "Keep desired child"}
	_, prepared := build(map[string]ChangeRebaseResolution{resolution.ConflictID: resolution})
	reloaded, err := newChangeEvaluation(prepared.source, prepared.revision, map[string]ChangeObjectIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.records[child].Payload.ParentID; got == nil || *got != newParent {
		t.Fatalf("reload restored old parent reference: %v", got)
	}
	before, err := prepared.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	after, err := reloaded.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	h1, err := changeSemanticHash(before)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := changeSemanticHash(after)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("prepared and persisted semantic hashes differ")
	}
}

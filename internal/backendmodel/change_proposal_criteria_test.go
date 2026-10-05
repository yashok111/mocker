package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"uuid"
)

func TestChangeProposalCriteriaSemanticsAndAttachments(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	source, err := r.ResolveSourceGraph(t.Context(), base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := source.State.Nodes[0].ID
	snapshot := source.State.Sources[0]
	file := snapshot.Files[0]
	attachment := map[string]any{"kind": "source", "revisionId": base.Revision.ID, "repositoryId": snapshot.RepositoryID, "snapshotId": snapshot.ID, "file": file.Path, "contentHash": file.ContentHash, "startLine": 1, "endLine": 2}
	criteria := []any{
		map[string]any{"key": "exists", "kind": "object_exists", "required": true, "description": "Object must remain", "recordType": "node", "id": id, "objectKind": source.State.Nodes[0].Kind},
		map[string]any{"key": "name", "kind": "field_equals", "required": true, "description": "Expected name", "recordType": "node", "id": id, "selector": map[string]any{"kind": "source", "source": map[string]any{"kind": "name"}}, "expected": map[string]any{"present": true, "value": "Desired"}},
		map[string]any{"key": "test", "kind": "test_attachment", "required": true, "description": "Review attached source", "targetIds": []string{id}, "attachment": attachment},
		map[string]any{"key": "runtime", "kind": "runtime_check", "required": false, "description": "Measure response time", "targetIds": []string{id}},
	}
	c := changeMapCommand(t, "set_criteria", map[string]any{"criteria": criteria})
	d, _ = saveChange(t, r, d, "criteria", c)
	if len(d.Revision.Criteria) != 4 {
		t.Fatal("complete criterion list not stored")
	}
	var previous string
	for i, description := range []string{"Measure response time", "Check response correctness"} {
		next := mapsCriterion(criteria[3].(map[string]any))
		next["description"] = description
		command := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{next}})
		p, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{command}})
		if err != nil || p.SemanticHash == nil {
			t.Fatalf("criterion preview %+v %v", p, err)
		}
		if i > 0 && previous == *p.SemanticHash {
			t.Fatal("criterion description omitted from semantics")
		}
		previous = *p.SemanticHash
	}
	attachment["contentHash"] = fmt.Sprintf("%064d", 0)
	bad := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{criteria[2]}})
	p, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{bad}})
	if err != nil {
		t.Fatal(err)
	}
	if p.SemanticHash != nil {
		t.Fatal("foreign source attachment was accepted")
	}
	for _, raw := range []string{
		fmt.Sprintf(`{"key":"x","kind":"field_equals","required":true,"description":"x","recordType":"node","id":%q,"selector":{"kind":"source","source":{"kind":"name"}},"expected":{}}`, id),
		fmt.Sprintf(`{"key":"x","kind":"runtime_check","required":true,"description":"x","targetIds":[%q],"executed":true}`, id),
		fmt.Sprintf(`{"key":"x","kind":"test_attachment","required":true,"description":"x","targetIds":[%q],"attachment":{"kind":"source","revisionId":%q,"repositoryId":%q,"snapshotId":%q,"file":"../escape","contentHash":%q,"startLine":1,"endLine":1}}`, id, base.Revision.ID, snapshot.RepositoryID, snapshot.ID, file.ContentHash),
	} {
		var criterion ChangeCriterion
		if json.Unmarshal([]byte(raw), &criterion) == nil {
			t.Fatalf("invalid criterion accepted: %s", raw)
		}
	}
}
func mapsCriterion(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestChangeProposalSource5VocabularyAndArtifactOwnerIsolation(t *testing.T) {
	t.Parallel()
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "source5 desired", BaseRevisionID: base.Revision.ID, IdempotencyKey: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Revision.BaseSchemaVersion != "5" {
		t.Fatal("source5 baseline silently upgraded")
	}
	projected := changeReadSnapshot(t, r, d)
	if projected.SchemaVersion != ComposedSchemaVersion || projected.BaselineSchemaVersion != EventsSchemaVersion {
		t.Fatal("full structural projection6 must retain the exact source5 baseline")
	}
	before := artifactOwnerRows(t, r)
	sourceBefore := immutableBytes(t, r)
	owner, field, edge := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	representation := changeCreateNode(t, owner, "dto", nil, representationOwnerAttrs())
	v, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{representation}})
	if err != nil {
		t.Fatal(err)
	}
	if v.CandidateHash != nil {
		t.Fatal("source5 accepted representation vocabulary")
	}
	handler := uuid.NewV7().String()
	source6Call := []ChangeProposalCommand{changeCreateNode(t, handler, "handler", nil, map[string]any{}), changeMapCommand(t, "upsert_edge", map[string]any{"id": uuid.NewV7().String(), "kind": "calls", "from": handler, "to": ids["http"], "attributes": map[string]any{}})}
	endpointPreview, err := r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: d.Revision.ID, Commands: source6Call})
	if err != nil {
		t.Fatal(err)
	}
	if endpointPreview.CandidateHash != nil {
		t.Fatal("source5 accepted source6-only call endpoint semantics")
	}
	_ = field
	_ = edge
	command := scenarioSet(base, ids, scenario).Commands[0]
	set := changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": command.Artifact, "revisionId": command.RevisionID, "editorBindings": command.EditorBindings})
	d, _ = saveChange(t, r, d, "pin", set)
	if len(d.Revision.ArtifactPins) != 1 || len(d.Revision.ArtifactContext.EditorBindings) != 2 {
		t.Fatal("desired artifact group was not frozen")
	}
	pin := d.Revision.ArtifactPins[0]
	criterion := map[string]any{"key": "scenario", "kind": "test_attachment", "required": true, "description": "Authored check scenario", "targetIds": []string{ids["http"]}, "attachment": map[string]any{"kind": "artifact", "artifact": pin, "jsonPointer": ""}}
	match := map[string]any{"key": "object", "kind": "artifact_object_matches", "required": false, "description": "Participant remains", "artifact": pin, "selector": map[string]any{"kind": "participant", "participantId": "p"}, "expectedHash": d.Revision.ArtifactContext.EditorBindings[0].ObjectHash}
	d, _ = saveChange(t, r, d, "authored-check", changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{criterion, match}}))
	d, _ = saveChange(t, r, d, "remove-pin", changeMapCommand(t, "remove_artifact_pin", map[string]any{"artifact": command.Artifact}), changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{}}))
	if len(d.Revision.ArtifactPins) != 0 || len(d.Revision.ArtifactContext.EditorBindings) != 0 {
		t.Fatal("desired artifact group not fully removed")
	}
	if !bytes.Equal(before, artifactOwnerRows(t, r)) {
		t.Fatal("proposal edited artifact owner")
	}
	for key, value := range sourceBefore {
		if immutableBytes(t, r)[key] != value {
			t.Fatalf("source bytes changed: %s", key)
		}
	}
	if strconv.FormatInt(scenario.Draft.ID, 10) != pin.RevisionID {
		t.Fatal("artifact latest fallback")
	}
	source, err := loadComposedBase(t.Context(), r.db.R, base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Identities) == 0 || !slices.ContainsFunc(source.Identities, func(i QualifiedSourceIdentity) bool { return i.ID == ids["http"] }) {
		t.Fatal("source5 identity not derived")
	}
}

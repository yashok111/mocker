package backendmaterialize

import (
	"encoding/json/v2"
	"fmt"
	"strconv"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/apidesign"
	bm "github.com/yashok111/mocker/internal/backendmodel"
)

func TestMaterializationCopyPinsRemainInCandidate(t *testing.T) {
	t.Parallel()
	s, pid, in := fixture(t)
	installation, err := s.models.InstallationID(t.Context())
	must(t, err)
	pins := []bm.NamespacedArtifactPin{}
	commands := []bm.ChangeProposalCommand{}
	for range 2 {
		api, err := s.apis.Create(t.Context(), apidesign.CreateInput{Name: "Copy source", Document: apiDoc, Source: "ui"})
		must(t, err)
		pins = append(pins, bm.NamespacedArtifactPin{Namespace: bm.ArtifactNamespace{Scope: "local", InstallationID: installation}, Pin: bm.ArtifactPin{Kind: "api_design", ID: strconv.FormatInt(api.Design.ID, 10), RevisionID: strconv.FormatInt(api.Draft.ID, 10), ContentHash: api.Draft.Hash}})
		var command bm.ChangeProposalCommand
		raw := fmt.Sprintf(`{"type":"set_artifact_pin","commandId":%q,"reason":"copy source","artifact":{"kind":"api_design","id":%q},"revisionId":%q,"apiBindings":[],"editorBindings":[]}`, uuid.NewV7().String(), strconv.FormatInt(api.Design.ID, 10), strconv.FormatInt(api.Draft.ID, 10))
		must(t, json.Unmarshal([]byte(raw), &command))
		commands = append(commands, command)
	}
	proposal := in.Target.ChangeProposal
	preview, err := s.models.PreviewChangeProposal(t.Context(), pid, proposal.ProposalID, bm.PreviewChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: proposal.ProposalRevisionID, Commands: commands})
	must(t, err)
	if preview.CandidateHash == nil {
		t.Fatal(preview.Diagnostics)
	}
	applied, err := s.models.ApplyChangeProposal(t.Context(), pid, proposal.ProposalID, bm.ApplyChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: proposal.ProposalRevisionID, Commands: commands, CandidateHash: *preview.CandidateHash, IdempotencyKey: "pins"})
	must(t, err)
	_ = applied
	detail, err := s.models.GetChangeProposal(t.Context(), pid, proposal.ProposalID, bm.GetChangeProposalInput{})
	must(t, err)
	in.Target.ChangeProposal = &bm.ProposalReadTarget{ProposalID: proposal.ProposalID, ProposalRevisionID: detail.Revision.ID}
	graph, err := s.models.ResolveEffectiveGraph(t.Context(), pid, in.Target)
	must(t, err)
	in.TargetHash = graph.Pins.TargetHash
	in.Targets[0].Commands = []Command{{Type: "copy_api_document", CopyFrom: &pins[0]}}
	first, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	if in.Targets[0].Commands[0].CopyFrom == nil {
		t.Fatal("preview mutated caller")
	}
	in.Targets[0].Commands[0].CopyFrom = &pins[1]
	second, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	if first.CandidateHash == second.CandidateHash {
		t.Fatal("candidate lost exact source pin")
	}
	in.Targets[0].Commands = []Command{{Type: "copy_api_object", CopyFrom: &pins[0], Selector: &bm.APIArtifactSelector{ObjectKey: "orders-get"}, Destination: "/paths/~1orders/get", APIDocument: `{"openapi":"3.1.0","info":{"title":"Copy","version":"1"},"paths":{}}`}}
	object, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	if !hasPointer([]byte(object.Input.Targets[0].Commands[0].APIDocument), "/paths/~1orders/get") {
		t.Fatal("operation not copied")
	}
}

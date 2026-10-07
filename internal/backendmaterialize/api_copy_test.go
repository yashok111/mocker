package backendmaterialize

import (
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/apidesign"
	bm "github.com/yashok111/mocker/internal/backendmodel"
)

// copyFixture pins two API designs into the fixture proposal and points the
// materialization input at the resulting proposal revision.
func copyFixture(t *testing.T) (*Service, string, PreviewInput, []bm.NamespacedArtifactPin) {
	t.Helper()
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
	return s, pid, in, pins
}

func TestMaterializationCopyPinsRemainInCandidate(t *testing.T) {
	t.Parallel()
	s, pid, in, pins := copyFixture(t)
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

// review 2026-10-06, F36: the linked-contract check compares number lexemes
// exactly; RFC 8785 canonicalisation made 9007199254740993 equal ...992, and
// Apply then silently overwrote the scenario's contract document.
func TestLinkedContractEqualityKeepsNumberLexemes(t *testing.T) {
	t.Parallel()
	if equalJSON(`{"a":9007199254740993}`, `{"a":9007199254740992}`) {
		t.Fatal("distinct big integers compared equal")
	}
	if !equalJSON(`{"b":1, "a":[2.5]}`, `{"a":[2.5],"b":1}`) {
		t.Fatal("member order or whitespace made equal documents differ")
	}
}

// review 2026-10-06, F125: splicing one object into the destination must leave
// every other number lexeme exact; a float64 round trip turned
// 9223372036854775807 into 9223372036854775808 and 1.0 into 1.
func TestMaterializationCopyObjectKeepsNumberLexemes(t *testing.T) {
	t.Parallel()
	s, pid, in, pins := copyFixture(t)
	doc := `{"openapi":"3.1.0","info":{"title":"Copy","version":"1"},"paths":{},"components":{"schemas":{"Big":{"type":"integer","format":"int64","maximum":9223372036854775807,"default":1.0,"enum":[9007199254740993]}}}}`
	in.Targets[0].Commands = []Command{{Type: "copy_api_object", CopyFrom: &pins[0], Selector: &bm.APIArtifactSelector{ObjectKey: "orders-get"}, Destination: "/paths/~1orders/get", APIDocument: doc}}
	out, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	got := out.Input.Targets[0].Commands[0].APIDocument
	for _, lexeme := range []string{"9223372036854775807", "9007199254740993", "1.0"} {
		if !strings.Contains(got, lexeme) {
			t.Fatalf("number lexeme %s lost: %s", lexeme, got)
		}
	}
	if !hasPointer([]byte(got), "/paths/~1orders/get") {
		t.Fatal("operation not copied")
	}
}

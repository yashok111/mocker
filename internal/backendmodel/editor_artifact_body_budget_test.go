package backendmodel

import (
	"encoding/json/v2"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

func TestArtifactApplyBodyReservationCompactBoundary(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	lengths := []int{50000, 50000, 30000}
	// Build an actual compact request against an owner-created immutable scenario.
	doc := d.Draft.Document
	doc.Participants = []designscenario.Participant{}
	doc.Messages = []designscenario.Message{}
	doc.Contracts = []designscenario.Contract{}
	doc.Fragments = []designscenario.Fragment{}
	for i, n := range lengths {
		doc.Participants = append(doc.Participants, designscenario.Participant{ID: strings.Repeat(string(rune('a'+i)), n), Name: strconv.Itoa(i), Kind: "service"})
	}
	owner := s.scenarios.(*designscenario.Repo)
	d, err := owner.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: ArtifactKey{"design_scenario", strconv.FormatInt(d.Scenario.ID, 10)}, RevisionID: strconv.FormatInt(d.Draft.ID, 10), EditorBindings: []EditorBindingInput{}, Reason: "x"}}}
	for _, p := range doc.Participants {
		in.Commands[0].EditorBindings = append(in.Commands[0].EditorBindings, EditorBindingInput{Selector: EditorSelector{Kind: "participant", ParticipantID: p.ID}, SourceNodeIDs: []string{ids["http"]}})
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	target := MaxAPIPinBodyBytes - 52
	delta := target - len(raw)
	in.Commands[0].EditorBindings[2].Selector.ParticipantID += strings.Repeat("c", delta)
	raw, err = json.Marshal(in)
	if err != nil || len(raw) != target {
		t.Fatal(len(raw), err)
	}

	doc.Participants[2].ID = in.Commands[0].EditorBindings[2].Selector.ParticipantID
	saved, err := owner.Save(t.Context(), d.Scenario.ID, designscenario.SaveInput{ExpectedVersion: d.Scenario.Version, Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in.Commands[0].RevisionID = strconv.FormatInt(saved.Draft.ID, 10)
	raw, err = json.Marshal(in)
	if err != nil || len(raw) != target {
		t.Fatal("actual compact body", len(raw), err)
	}
	before, err := s.repo.Get(t.Context(), base.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Preview(t.Context(), base.Project.ID, in)
	var fault *FaultError
	if !errors.As(err, &fault) || fault.Status != 413 || fault.Code != "backend_artifact_apply_body_limit" {
		t.Fatalf("compact preview promised inapplicable Apply: %v", err)
	}
	after, err := s.repo.Get(t.Context(), base.Project.ID)
	if err != nil || after.Version != before.Version || after.CurrentRevisionID != before.CurrentRevisionID {
		t.Fatal("budget rejection wrote project", err)
	}
}

func TestArtifactApplyBodyReservationExactLimitsAndKeys(t *testing.T) {
	for _, key := range []string{strings.Repeat(`\`, MaxKeyLength), strings.Repeat(`"`, MaxKeyLength), strings.Repeat("x", MaxKeyLength), "10000000-0000-4000-8000-000000000001"} {
		t.Run(strconv.Itoa(len(key))+key[:1], func(t *testing.T) {
			s, base, ids, d := artifactServiceFixture(t)
			in := scenarioSet(base, ids, d)
			size, err := reservedArtifactApplyBytes(base.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			independent, err := json.Marshal(map[string]any{"projectId": base.Project.ID, "baseRevisionId": in.BaseRevisionID, "expectedVersion": in.ExpectedVersion, "commands": in.Commands, "candidateHash": strings.Repeat("a", 64), "idempotencyKey": strings.Repeat(`\`, MaxKeyLength)})
			if err != nil || int64(len(independent)) != size {
				t.Fatal("serialized reservation", len(independent), size, err)
			}
			at := NewArtifactServiceWithBodyLimit(s.repo, s.api, s.scenarios, size+ArtifactApplyRPCFramingBytes)
			p, err := at.Preview(t.Context(), base.Project.ID, in)
			if err != nil || !p.CanApply {
				t.Fatal("inclusive reserve", size, err)
			}
			normal, err := s.Preview(t.Context(), base.Project.ID, in)
			if err != nil || normal.CandidateHash != p.CandidateHash || normal.SemanticHash != p.SemanticHash {
				t.Fatal("budget changed hash domain", err)
			}
			tooSmall := NewArtifactServiceWithBodyLimit(s.repo, nil, nil, size+ArtifactApplyRPCFramingBytes-1)
			_, err = tooSmall.Preview(t.Context(), base.Project.ID, in)
			assertArtifactReservationFault(t, err, size-1, size)
			apply := ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, key}
			_, err = tooSmall.Apply(t.Context(), base.Project.ID, apply)
			assertArtifactReservationFault(t, err, size-1, size)
			actual, err := json.Marshal(map[string]any{"projectId": base.Project.ID, "baseRevisionId": apply.BaseRevisionID, "expectedVersion": apply.ExpectedVersion, "commands": apply.Commands, "candidateHash": apply.CandidateHash, "idempotencyKey": apply.IdempotencyKey})
			if err != nil || int64(len(actual)) > size {
				t.Fatal("supported canonical key does not fit", len(actual), size, err)
			}
			result, err := at.Apply(t.Context(), base.Project.ID, apply)
			if err != nil {
				t.Fatal("applicable reserved preview", err)
			}
			// New stricter admission and unavailable owners cannot obstruct an old receipt.
			replay, err := tooSmall.Apply(t.Context(), base.Project.ID, apply)
			if err != nil || string(replay.ReceiptBytes()) != string(result.ReceiptBytes()) {
				t.Fatal("reservation preceded receipt lookup", err)
			}
			if _, err = tooSmall.Query(t.Context(), base.Project.ID, ArtifactQueryInput{RevisionID: result.Revision.ID, Artifact: in.Commands[0].Artifact, View: "sequence"}); err != nil {
				t.Fatal("reservation changed Query", err)
			}
		})
	}
}

func assertArtifactReservationFault(t *testing.T, err error, allowed, reserved int64) {
	t.Helper()
	var f *FaultError
	if !errors.As(err, &f) || f.Status != 413 || f.Code != "backend_artifact_apply_body_limit" || f.Details["allowedBytes"] != allowed || f.Details["reservedApplyBytes"] != reserved {
		t.Fatalf("reserve fault: %v; allowed=%d reserved=%d", err, allowed, reserved)
	}
}

func TestArtifactApplyBodyReservationPrivatePrepareUnchanged(t *testing.T) {
	s, base, ids, d := artifactServiceFixture(t)
	in := scenarioSet(base, ids, d)
	limited := NewArtifactServiceWithBodyLimit(s.repo, s.api, s.scenarios, 1)
	out, err := limited.prepareArtifacts(t.Context(), base.Project.ID, in)
	if err != nil || !out.preview.CanApply {
		t.Fatal("shared private legacy engine changed", err)
	}
	if _, err := limited.Preview(t.Context(), base.Project.ID, in); err == nil {
		t.Fatal("public reservation missing")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var padded PreviewArtifactPinsInput
	if err = json.Unmarshal(append([]byte(" \n "), raw...), &padded); err != nil {
		t.Fatal(err)
	}
	a, _ := reservedArtifactApplyBytes(base.Project.ID, in)
	b, _ := reservedArtifactApplyBytes(base.Project.ID, padded)
	if a != b {
		t.Fatal("reservation counted preview whitespace", a, b)
	}
	if got := NewArtifactServiceWithBodyLimit(s.repo, s.api, s.scenarios, 1<<20).maxApplyBodyBytes; got != MaxAPIPinBodyBytes {
		t.Fatal("raised artifact body limit", got)
	}
}

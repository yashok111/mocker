package backendanalysis

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/store"
)

func TestAnalysisPublicContextStoredTargets(t *testing.T) {
	for _, arm := range []string{"revision", "proposal", "changeProposal", "commandPreview"} {
		t.Run(arm, func(t *testing.T) {
			r, _ := testRepo(t)
			p := testPrepared(t, arm)
			var in ImmutableInput
			if err := json.Unmarshal(p.InputJSON, &in); err != nil {
				t.Fatal(err)
			}
			target := backendmodel.ProposalReadTarget{ProposalID: projectID, ProposalRevisionID: revisionID}
			switch arm {
			case "proposal":
				in.To = &backendmodel.BackendReadTarget{Proposal: &target}
			case "changeProposal":
				in.To = &backendmodel.BackendReadTarget{ChangeProposal: &target}
			case "commandPreview":
				in.To = nil
				in.CommandPreview = &backendmodel.FrozenChangePreview{ChangeProposal: target, ExpectedVersion: 9007199254740993, DraftHash: strings.Repeat("a", 64), CandidateHash: strings.Repeat("b", 64), CommandsHash: strings.Repeat("c", 64), ArtifactContext: backendmodel.ArtifactContext{SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64)}}
			}
			p.InputJSON, _ = canonical(in)
			p.InputHash = digest(p.InputJSON)
			job, err := r.Start(t.Context(), p, nil)
			if err != nil {
				t.Fatal(err)
			}
			detail, err := r.Detail(t.Context(), projectID, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Input.FromRevisionID != revisionID || detail.Job.AnalysisInputHash != p.InputHash {
				t.Fatal(string(raw))
			}
			for _, private := range []string{"admission", "reservation", "commands\"", "projectId\":null"} {
				if strings.Contains(string(raw), private) {
					t.Fatal(string(raw))
				}
			}
			if arm == "commandPreview" && !strings.Contains(string(raw), `"expectedVersion":9007199254740993`) {
				t.Fatal(string(raw))
			}
			_, err = r.Detail(t.Context(), revisionID, job.ID)
			requireStatus(t, err, 404)
		})
	}
}

func TestAnalysisPublicContextHistoricalPinsAndRestart(t *testing.T) {
	for _, tagged := range []bool{false, true} {
		t.Run(fmt.Sprint(tagged), func(t *testing.T) {
			r, db := testRepo(t)
			p := testPrepared(t, "pins")
			var in ImmutableInput
			if err := json.Unmarshal(p.InputJSON, &in); err != nil {
				t.Fatal(err)
			}
			c := backendmodel.ArtifactContext{SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64)}
			pins := []backendmodel.ArtifactPin{}
			if tagged {
				c.DocumentVersion = backendmodel.EditorArtifactDocumentVersion
				pins = append(pins, backendmodel.ArtifactPin{Kind: "design_scenario", ID: "9007199254740993", RevisionID: "9223372036854775807", ContentHash: strings.Repeat("c", 64)})
			}
			in.BeforePins = backendmodel.EffectiveGraphPins{ArtifactContext: &c, ArtifactPins: pins}
			in.AfterPins = in.BeforePins
			in.CommandPreview = &backendmodel.FrozenChangePreview{ChangeProposal: backendmodel.ProposalReadTarget{ProposalID: projectID, ProposalRevisionID: revisionID}, ExpectedVersion: 9223372036854775807, DraftHash: strings.Repeat("d", 64), CandidateHash: strings.Repeat("e", 64), CommandsHash: strings.Repeat("f", 64), ArtifactContext: c, ArtifactPins: pins}
			in.To = nil
			p.InputJSON, _ = canonical(in)
			p.InputHash = digest(p.InputJSON)
			job, err := r.Start(t.Context(), p, nil)
			if err != nil {
				t.Fatal(err)
			}
			detail, err := r.Detail(t.Context(), projectID, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			first, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(first), `"expectedVersion":9223372036854775807`) {
				t.Fatal(string(first))
			}
			expected, err := backendmodel.EncodeArtifactContext(c, pins)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(first, expected) {
				t.Fatalf("historical bytes changed: %s, want %s", first, expected)
			}
			var n int
			var name, path string
			if err = db.R.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&n, &name, &path); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close() }()
			next := NewRepo(reopened)
			detail, err = next.Detail(t.Context(), projectID, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			second, err := json.Marshal(detail)
			if err != nil || !bytes.Equal(first, second) {
				t.Fatalf("restart changed context: %s %v", second, err)
			}
		})
	}
}

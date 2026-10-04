package backendanalysis

import (
	_ "embed"
	"encoding/json/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
)

//go:embed testdata/artifact-before.json
var artifactDocument string

func TestAnalysisImpactExactArtifactDeletion(t *testing.T) {
	_, db := testRepo(t)
	owner := apidesign.NewRepo(db, &config.Config{MaxBody: 2 << 20})
	original, err := owner.Create(t.Context(), apidesign.CreateInput{Name: "Exact artifact", Document: artifactDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	revised := strings.Replace(artifactDocument, `,{"id":"b","name":"B","x":1,"y":0,"terminal":true}`, "", 1)
	next, err := owner.Save(t.Context(), original.Design.ID, apidesign.SaveInput{ExpectedVersion: original.Design.Version, Document: revised, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	request := backendmodel.NewEditorArtifactRequest(t.Context(), owner, nil)
	key := backendmodel.ArtifactKey{Kind: "api_design", ID: strconv.FormatInt(original.Design.ID, 10)}
	oldPin, err := request.SnapshotPin(key, strconv.FormatInt(original.Draft.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	newPin, err := request.SnapshotPin(key, strconv.FormatInt(next.Draft.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	before, after := supportedGraph(nil, nil), supportedGraph(nil, nil)
	for i, g := range []*backendmodel.EffectiveGraphSnapshot{before, after} {
		pin := oldPin
		if i == 1 {
			pin = newPin
		}
		g.Target = backendmodel.BackendReadTarget{RevisionID: revisionID}
		g.State.Revision.ID = revisionID
		g.Pins.ArtifactPins = []backendmodel.ArtifactPin{pin}
		g.Pins.ArtifactContext = &backendmodel.ArtifactContext{DocumentVersion: backendmodel.EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64)}
	}
	// A later owner revision is not substituted for either exact analysis side.
	if _, err = owner.Save(t.Context(), next.Design.ID, apidesign.SaveInput{ExpectedVersion: next.Design.Version, Document: strings.Replace(revised, "Historical", "Unrelated latest", 1), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if _, err := request.ProjectEffective(before, backendmodel.ArtifactQueryInput{RevisionID: revisionID, Artifact: key, View: "states", Limit: 100}); err != nil {
		t.Fatalf("exact projection: %v", err)
	}
	terminal, err := analyzeGraphs(t.Context(), engineInput(), before, after, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	removed, paired := false, false
	for _, chunk := range terminal.Snapshot.Chunks {
		if chunk.Section != "changes" {
			continue
		}
		var records []ResultRecord
		if err = json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Kind != "artifact_object" {
				continue
			}
			var detail struct {
				Before    *backendmodel.ArtifactProjectionItem `json:"before"`
				After     *backendmodel.ArtifactProjectionItem `json:"after"`
				BeforePin *backendmodel.ArtifactPin            `json:"beforePin"`
				AfterPin  *backendmodel.ArtifactPin            `json:"afterPin"`
			}
			if err = json.Unmarshal(record.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Before != nil && detail.Before.Locator.Owner.StateID == "b" {
				removed = detail.After == nil && detail.BeforePin.RevisionID == oldPin.RevisionID
			}
			if detail.Before != nil && detail.After != nil && detail.Before.Locator.Owner.StateID == "a" {
				paired = detail.BeforePin.RevisionID == oldPin.RevisionID && detail.AfterPin.RevisionID == newPin.RevisionID
			}
		}
	}
	if !removed || !paired {
		t.Fatalf("exact deletion=%v stable object paired=%v gaps=%+v", removed, paired, terminal.Snapshot.Manifest.Gaps)
	}
	scoped := engineInput()
	scoped.Scope.ChangedIDs = []ObjectAddress{{RecordType: "node", ID: projectID}}
	excluded, err := analyzeGraphs(t.Context(), scoped, before, after, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(excluded.Snapshot.Manifest.CoveredChangedIDs) != 0 {
		t.Fatalf("artifact changes bypassed scope: %+v", excluded.Snapshot.Manifest.CoveredChangedIDs)
	}

}

func TestAnalysisImpactBoundEmbeddedStateDeletion(t *testing.T) {
	_, db := testRepo(t)
	cfg := &config.Config{MaxBody: 2 << 20}
	api := apidesign.NewRepo(db, cfg)
	scenarios := designscenario.NewRepo(db, cfg, api)
	doc := designscenario.Document{FormatVersion: 3, Title: "Bound nested state", Participants: []designscenario.Participant{}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{{ID: "embedded", Name: "States", Mode: "copy", Document: []byte(artifactDocument)}}}
	original, err := scenarios.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Contracts[0].Document = []byte(strings.Replace(artifactDocument, `,{"id":"b","name":"B","x":1,"y":0,"terminal":true}`, "", 1))
	next, err := scenarios.Save(t.Context(), original.Scenario.ID, designscenario.SaveInput{ExpectedVersion: original.Scenario.Version, Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	request := backendmodel.NewEditorArtifactRequest(t.Context(), api, scenarios)
	key := backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(original.Scenario.ID, 10)}
	graphs := []*backendmodel.EffectiveGraphSnapshot{supportedGraph([]backendmodel.Node{{ID: revisionID, Kind: "handler"}}, nil), supportedGraph([]backendmodel.Node{{ID: revisionID, Kind: "handler"}}, nil)}
	for i, rid := range []int64{original.Draft.ID, next.Draft.ID} {
		pin, err := request.SnapshotPin(key, strconv.FormatInt(rid, 10))
		if err != nil {
			t.Fatal(err)
		}
		selector := backendmodel.EditorSelector{Kind: "state_diagram", DiagramID: "d", EmbeddedContractID: "embedded"}
		object, err := request.ResolveObject(pin, selector)
		if err != nil {
			t.Fatal(err)
		}
		g := graphs[i]
		g.Target = backendmodel.BackendReadTarget{RevisionID: revisionID}
		g.State.Revision.ID = revisionID
		g.Pins.ArtifactPins = []backendmodel.ArtifactPin{pin}
		g.Pins.ArtifactContext = &backendmodel.ArtifactContext{DocumentVersion: backendmodel.EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64), EditorBindings: []backendmodel.EditorBinding{{ArtifactKind: key.Kind, ArtifactID: key.ID, Selector: selector, SourceNodeIDs: []string{revisionID}, SourceLabels: []string{"Handler"}, ObjectHash: object.ObjectHash, LastKnownLabel: object.Label, Origin: "manual", Reason: "Exact consumer state contract"}}}
	}
	terminal, err := analyzeGraphs(t.Context(), engineInput(), graphs[0], graphs[1], request, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, chunk := range terminal.Snapshot.Chunks {
		if chunk.Section != "changes" {
			continue
		}
		var records []ResultRecord
		if err = json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Kind != "artifact_object" {
				continue
			}
			var detail struct {
				Before *backendmodel.ArtifactProjectionItem `json:"before"`
				After  *backendmodel.ArtifactProjectionItem `json:"after"`
			}
			if err = json.Unmarshal(record.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Before != nil && detail.Before.Locator.Owner.StateID == "b" && detail.After == nil && detail.Before.Locator.Embedded.ContractID == "embedded" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("bound embedded deletion absent: %+v", terminal.Snapshot.Manifest.Gaps)
	}
}

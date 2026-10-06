package backendanalysis

import (
	"encoding/json/v2"
	"github.com/yashok111/mocker/internal/backendmodel"
	"strings"
	"testing"
)

func TestLifecycleDiagnosticExactMappingGuardAndAmbiguity(t *testing.T) {
	g := supportedGraph([]backendmodel.Node{{ID: "source", Kind: "flow_step"}, {ID: "trigger", Kind: "handler"}}, nil)
	tr := backendmodel.LifecycleTransition{ID: "transition", From: "paid", To: "cancelled", Origin: backendmodel.DiagramOrigin{Kind: "source_assertion", Evidence: []backendmodel.DiagramEvidenceRef{{RevisionID: revisionID, EvidenceID: "proof-source", SubjectID: "source"}}}, Refs: []backendmodel.DiagramRef{{Kind: "record", RecordType: "node", ID: "source"}}, Triggers: []backendmodel.DiagramRef{{Kind: "record", RecordType: "node", ID: "trigger"}}, Guard: backendmodel.LifecycleGuard{Kind: "none"}}
	rule := backendmodel.LifecycleRule{ID: "rule", From: tr.From, To: tr.To, Trigger: tr.Triggers[0], Verdict: "forbidden", Origin: backendmodel.DiagramOrigin{Kind: "authored", Reason: "business restriction"}}
	pin := backendmodel.DiagramPin{ID: projectID, Version: 1, ContentHash: strings.Repeat("a", 64)}
	in := DiagnosticInput{DiagramScope: &backendmodel.DiagramScope{Pin: pin, TargetHash: g.Pins.TargetHash, ScopeHash: strings.Repeat("b", 64), Selectors: []backendmodel.DiagramScopeSelector{{Kind: "semantic", ID: tr.ID}}}, Diagram: &backendmodel.DiagramVersion{Pin: pin, Document: backendmodel.DiagramDocument{Lifecycle: &backendmodel.LifecyclePayload{States: []backendmodel.LifecycleState{{ID: "paid", Value: &backendmodel.LifecycleValue{JSON: `"paid"`}}, {ID: "cancelled", Value: &backendmodel.LifecycleValue{JSON: `"cancelled"`}}}, Transitions: []backendmodel.LifecycleTransition{tr}, Rules: []backendmodel.LifecycleRule{rule}}}}}
	run := func() *backendmodel.Finding {
		t.Helper()
		r, e := EvaluateDiagnostics(t.Context(), g, in)
		if e != nil {
			t.Fatal(e)
		}
		return diagnosticRule(r, "lifecycle_forbidden_transition")
	}
	if f := run(); f == nil || f.Certainty != "confirmed" {
		t.Fatal(f)
	}
	in.Diagram.Document.Lifecycle.Transitions[0].Guard = backendmodel.LifecycleGuard{Kind: "opaque", Text: "unknown condition"}
	if f := run(); f == nil || f.Certainty != "possible" {
		t.Fatal(f)
	}
	in.Diagram.Document.Lifecycle.Transitions[0].Triggers = append(in.Diagram.Document.Lifecycle.Transitions[0].Triggers, backendmodel.DiagramRef{Kind: "record", RecordType: "node", ID: "other-trigger"})
	if f := run(); f == nil || f.Certainty != "unknown" {
		t.Fatal(f)
	}
	in.Diagram.Document.Lifecycle.Rules[0].Trigger.ID = "same-label-different-id"
	if run() != nil {
		t.Fatal("name matching")
	}
	in.Diagram.Document.Lifecycle.Transitions = nil
	if run() != nil {
		t.Fatal("missing transition became positive")
	}
}
func TestDiagnosticLegacyWireBytes(t *testing.T) {
	var in StartInput
	if err := json.Unmarshal([]byte(validStart), &in); err != nil {
		t.Fatal(err)
	}
	raw, err := canonical(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "diagramScope") {
		t.Fatal("nil scope changed legacy bytes")
	}
	var again StartInput
	if err = json.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	raw2, _ := canonical(again)
	if string(raw) != string(raw2) {
		t.Fatal("legacy normalization unstable")
	}
}
func TestDiagramDiagnosticMismatchBeforeAdmission(t *testing.T) {
	repo, db := testRepo(t)
	models := backendmodel.NewRepo(db)
	a, e := models.Create(t.Context(), backendmodel.CreateInput{Name: "A", IdempotencyKey: "a"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := models.Create(t.Context(), backendmodel.CreateInput{Name: "B", IdempotencyKey: "b"})
	if e != nil {
		t.Fatal(e)
	}
	d := backendmodel.DiagramDocument{Format: "backend-diagram-v1", Kind: "architecture", Target: backendmodel.BackendReadTarget{RevisionID: a.CurrentRevisionID}, Payload: backendmodel.ArchitecturePayload{PrimarySystemID: projectID, Elements: []backendmodel.ArchitectureElement{{ID: projectID, Role: "software_system", Label: "A", Origin: backendmodel.DiagramOrigin{Kind: "authored", Reason: "Boundary"}, Refs: []backendmodel.DiagramRef{}}}, Links: []backendmodel.ArchitectureLink{}}}
	v, e := models.CreateDiagram(t.Context(), a.ID, backendmodel.DiagramCreateInput{Document: d, IdempotencyKey: "diagram"})
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(repo, models, nil)
	_, e = s.Start(t.Context(), a.ID, StartInput{Kind: "diagnostics", Target: AnalysisTarget{RevisionID: b.CurrentRevisionID}, DiagramScope: &backendmodel.DiagramScopeInput{Pin: v.Pin, Selectors: []backendmodel.DiagramScopeSelector{{Kind: "semantic", ID: projectID}}}, ObservationMode: "none", IdempotencyKey: "mismatch"})
	if e == nil {
		t.Fatal("foreign target accepted")
	}
	var count int
	_ = db.R.QueryRow(`SELECT count(*) FROM backend_analysis_jobs WHERE project_id=?`, a.ID).Scan(&count)
	if count != 0 {
		t.Fatal("job admitted before scope validation")
	}
}

func TestDiagramDiagnosticBrokenReferenceNeedsCoverage(t *testing.T) {
	g := supportedGraph(nil, nil)
	ref := backendmodel.DiagramRef{Kind: "record", RecordType: "node", ID: revisionID}
	hash, _ := requestHash(ref)
	pin := backendmodel.DiagramPin{ID: projectID, Version: 2, ContentHash: strings.Repeat("a", 64)}
	original := backendmodel.DiagramPin{ID: revisionID, Version: 1, ContentHash: strings.Repeat("b", 64)}
	scope := &backendmodel.DiagramScope{Pin: pin, TargetHash: g.Pins.TargetHash, ScopeHash: strings.Repeat("c", 64), Selectors: []backendmodel.DiagramScopeSelector{{Kind: "semantic", ID: projectID}}, SourceRefs: []backendmodel.DiagramRef{ref}}
	diagram := &backendmodel.DiagramVersion{Pin: pin, Gaps: []backendmodel.DiagramGap{{SubjectID: projectID, Code: "historical_ref:" + hash}}, Provenance: backendmodel.DiagramProvenance{Fork: &backendmodel.DiagramForkProvenance{Source: original}}}
	run := func() *backendmodel.Finding {
		t.Helper()
		r, err := EvaluateDiagnostics(t.Context(), g, DiagnosticInput{DiagramScope: scope, Diagram: diagram})
		if err != nil {
			t.Fatal(err)
		}
		return diagnosticRule(r, "diagram_broken_reference")
	}
	f := run()
	if f == nil || f.Certainty != "confirmed" || f.Witness.MissingRef == nil || f.Witness.MissingRef.ID != revisionID || f.Witness.OriginalDiagramPin == nil || *f.Witness.OriginalDiagramPin != original {
		t.Fatal(f)
	}
	g.State.Revision.Coverage.Status = "partial"
	if f = run(); f == nil || f.Certainty != "unknown" {
		t.Fatal("partial absence confirmed deletion", f)
	}
}

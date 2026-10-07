package apidesign

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestFinalizeImpactRetainsIncompleteDiagnosticsAndEmptyArrays(t *testing.T) {
	report := ImpactReport{DesignID: 7, ImpactAnalysis: ImpactAnalysis{Complete: false,
		Affected:    []ImpactEntity{{ID: "unknown", Kind: "scenario_message", Label: "Unknown"}},
		Diagnostics: []ImpactDiagnostic{{Code: "scenario_unresolved", EntityID: "unknown", Side: "before", Severity: "warning", Message: "Unknown binding"}},
	}}
	if err := FinalizeImpactReport(t.Context(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Complete || len(report.Affected) != 1 || len(report.Diagnostics) != 1 || report.Diagnostics[0].EntityID != "unknown" {
		t.Fatalf("lost unknown join: %+v", report)
	}
	if report.Changes == nil || report.Evidence == nil || report.FieldImpacts == nil || report.Coverage.TruncatedReasons == nil {
		t.Fatal("nil arrays")
	}
}

func TestFinalizeImpactBoundsFieldFindings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                    string
		count, explanationBytes int
		reason                  string
	}{
		{"field cap", MaxImpactFieldFindings + 1, 20, "scenario_fields"},
		{"byte budget", 1000, 10000, "output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := ImpactReport{ImpactAnalysis: ImpactAnalysis{Complete: true, Coverage: ImpactCoverage{FieldUsagesChecked: 9000}}}
			for i := range tc.count {
				report.FieldImpacts = append(report.FieldImpacts, ImpactFieldImpact{ID: fmt.Sprint(i), Explanation: strings.Repeat("x", tc.explanationBytes)})
			}
			if err := FinalizeImpactReport(t.Context(), &report); err != nil {
				t.Fatal(err)
			}
			raw, err := jsonx.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if report.Complete || !slices.Contains(report.Coverage.TruncatedReasons, tc.reason) || len(raw) > impactResponseBytes || len(report.FieldImpacts) > MaxImpactFieldFindings {
				t.Fatalf("unbounded fields: bytes=%d coverage=%+v", len(raw), report.Coverage)
			}
			if len(report.FieldImpacts) == 0 || report.Coverage.FieldImpactsReturned != len(report.FieldImpacts) || report.Coverage.FieldUsagesChecked != 9000 {
				t.Fatal("wrong field counts")
			}
			for i, finding := range report.FieldImpacts {
				if finding.ID != fmt.Sprint(i) {
					t.Fatal("did not retain stable prefix")
				}
			}
		})
	}
}

func TestFinalizeImpactCapsOutputWithoutDanglingReferences(t *testing.T) {
	report := ImpactReport{DesignID: 7, Version: 9, FromRevisionID: 3, FromHash: "base", ProposedHash: "candidate", ImpactAnalysis: ImpactAnalysis{Complete: true}}
	for i := range 500 {
		id := fmt.Sprint(i)
		report.Changes = append(report.Changes, ImpactChange{ID: id, Pointer: "/" + id, Kind: "changed", ChangeClass: "contract", Compatibility: "review", Explanation: strings.Repeat("reason", 1000)})
		report.Affected = append(report.Affected, ImpactEntity{ID: id, Kind: "schema", Label: strings.Repeat("schema", 1000)})
		report.Evidence = append(report.Evidence, ImpactEvidence{ID: id, ChangeID: id, EntityID: id, Side: "before", Direction: "response", Explanation: strings.Repeat("dependency", 1000)})
		report.Diagnostics = append(report.Diagnostics, ImpactDiagnostic{Code: "test", EntityID: id, Side: "before", Severity: "warning", Message: strings.Repeat("warning", 1000)})
	}
	if err := FinalizeImpactReport(t.Context(), &report); err != nil {
		t.Fatal(err)
	}
	b, err := jsonx.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 4<<20 || report.Complete || !slices.Contains(report.Coverage.TruncatedReasons, "output") {
		t.Fatalf("cap: %d %+v", len(b), report.Coverage)
	}
	if report.DesignID != 7 || report.Version != 9 || report.FromHash != "base" {
		t.Fatal("snapshot metadata lost")
	}
	changes := map[string]bool{}
	entities := map[string]bool{}
	for _, c := range report.Changes {
		changes[c.ID] = true
	}
	for _, e := range report.Affected {
		entities[e.ID] = true
	}
	for _, e := range report.Evidence {
		if !changes[e.ChangeID] || !entities[e.EntityID] || e.ReferenceSites == nil {
			t.Fatalf("dangling evidence: %+v", e)
		}
	}
	for _, d := range report.Diagnostics {
		if d.EntityID != "" && !entities[d.EntityID] {
			t.Fatalf("dangling diagnostic: %+v", d)
		}
	}
	if report.Coverage.ChangesReturned != len(report.Changes) || report.Coverage.EntitiesReturned != len(report.Affected) || report.Coverage.EvidenceReturned != len(report.Evidence) {
		t.Fatal("wrong returned counts")
	}
}

func TestFinalizeImpactHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := FinalizeImpactReport(ctx, &ImpactReport{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestImpactFieldSelectorPreservesWholeBodyPointer(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"response", "body"} {
		raw, err := jsonx.Marshal(ImpactFieldSelector{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"pointer":""`) {
			t.Fatalf("whole %s pointer lost: %s", kind, raw)
		}
	}
}

package backendmaterialize

import (
	"encoding/json/v2"
	"strconv"
	"strings"
	"testing"
)

// review 2026-10-06, F184: a new API target name Apply's CreateTx refuses
// (over 200 runes after trimming) must not preview as applicable.
func TestMaterializationPreviewRejectsUncreatableAPIName(t *testing.T) {
	t.Parallel()
	s, pid, in := fixture(t)
	if in.Targets[0].Kind != "api_design" || in.Targets[0].Pin != nil {
		t.Fatal("fixture target is not a new API")
	}
	in.Targets[0].Name = " " + strings.Repeat("я", 200) + " "
	if _, err := s.Preview(t.Context(), pid, in); err != nil {
		t.Fatalf("200 runes after trimming must preview: %v", err)
	}
	in.Targets[0].Name = strings.Repeat("я", 201)
	if p, err := s.Preview(t.Context(), pid, in); err == nil {
		t.Fatalf("uncreatable name previewed: canApply=%v", p.CanApply)
	}
}

// review 2026-10-06, F130: the stored Preview is the plan the candidateHash
// bound. Apply rewrote the scenario's linked contracts in place through a
// shallow copy, so the stored plan carried the new API revision instead.
func TestMaterializationStoredPlanIsThePreviewedPlan(t *testing.T) {
	t.Parallel()
	s, pid, in := fixture(t)
	in, api := linkedInput(t, s, in)
	request := applyInput(t, s, pid, in, "plan")
	receipt, err := s.Apply(t.Context(), pid, request)
	must(t, err)
	var raw string
	must(t, s.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_materializations_documents WHERE id=?`, receipt.ID).Scan(&raw))
	var stored struct{ Preview *Preview }
	must(t, json.Unmarshal([]byte(raw), &stored))
	for _, target := range stored.Preview.Input.Targets {
		if target.Kind != "design_scenario" {
			continue
		}
		source := target.Commands[0].Scenario.Contracts[0].Source
		if strconv.FormatInt(source.RevisionID, 10) != strconv.FormatInt(api.Draft.ID, 10) {
			t.Fatalf("stored plan contract revision %d; previewed %d", source.RevisionID, api.Draft.ID)
		}
		return
	}
	t.Fatal("stored plan lost the scenario target")
}

package backendmodel

import (
	"errors"
	"strings"
	"testing"
)

// TestAnalysisFootprintUnknownTargetsAnswer404 pins review 2026-10-06, F117:
// a well-formed but unknown fromRevisionId, change-proposal revision or legacy
// proposal revision reached the single-row scans in analysisFootprintBuilder
// and escaped as sql.ErrNoRows, a logged 500 at the admin plane.
func TestAnalysisFootprintUnknownTargetsAnswer404(t *testing.T) {
	r, base, change := changeFixture(t)
	unknown := "99999999-9999-4999-8999-999999999999"
	for name, call := range map[string]func() error{
		"from revision": func() error {
			_, err := r.AnalysisPairInputFootprint(t.Context(), base.Project.ID, unknown, BackendReadTarget{RevisionID: base.Revision.ID}, nil)
			return err
		},
		"target revision": func() error {
			_, err := r.EffectiveGraphInputFootprint(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: unknown})
			return err
		},
		"change proposal revision": func() error {
			_, err := r.EffectiveGraphInputFootprint(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: change.Proposal.ID, ProposalRevisionID: unknown}})
			return err
		},
		"legacy proposal revision": func() error {
			_, err := r.EffectiveGraphInputFootprint(t.Context(), base.Project.ID, BackendReadTarget{Proposal: &ProposalReadTarget{ProposalID: unknown, ProposalRevisionID: unknown}})
			return err
		},
		"frozen change preview": func() error {
			_, err := r.FreezeChangePreview(t.Context(), base.Project.ID, change.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: change.Proposal.Version, ProposalRevisionID: unknown, Commands: []ChangeProposalCommand{}}, strings.Repeat("a", 64))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if fault, ok := errors.AsType[*FaultError](err); !ok || fault.Status != 404 {
				t.Fatalf("err = %T %v, want a 404 FaultError", err, err)
			}
		})
	}
}

// TestChangePreviewUnknownArtifactRevisionIsBlocked pins review 2026-10-06,
// F174: set_artifact_pin naming an artifact revision that does not exist
// escaped as sql.ErrNoRows from the read budget (a logged 500), while the
// legacy artifact-pins preview reports the same case as the blocking
// backend_artifact_target_unavailable diagnostic.
func TestChangePreviewUnknownArtifactRevisionIsBlocked(t *testing.T) {
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	r := service.repo
	d, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Missing", BaseRevisionID: base.Revision.ID, IdempotencyKey: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	command := scenarioSet(base, ids, scenario).Commands[0]
	set := changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": command.Artifact, "revisionId": "999999", "editorBindings": []any{}})
	in := PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{set}}
	_, err = r.PreviewChangeProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	fault, ok := errors.AsType[*FaultError](err)
	if !ok || fault.Status != 422 || fault.Code != "backend_artifact_pins_blocked" {
		t.Fatalf("err = %T %v, want 422 backend_artifact_pins_blocked", err, err)
	}
	diagnostics, _ := fault.Details["diagnostics"].([]ArtifactDiagnostic)
	if len(diagnostics) != 1 || diagnostics[0].Code != "backend_artifact_target_unavailable" {
		t.Errorf("diagnostics = %+v, want one backend_artifact_target_unavailable", diagnostics)
	}
}

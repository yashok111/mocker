package backendmaterialize

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/designscenario"
)

func ownerID(v string) int64 { n, _ := strconv.ParseInt(v, 10, 64); return n }

func (s *Service) previewTx(ctx context.Context, tx *sql.Tx, pid string, input PreviewInput) (*Preview, error) {
	in, err := cloneInput(input)
	if err != nil {
		return nil, err
	}
	if err = checkPreviewShape(in); err != nil {
		return nil, err
	}
	g, err := s.models.ResolveEffectiveGraphTx(ctx, tx, pid, in.Target)
	if err != nil {
		return nil, err
	}
	if g.Pins.TargetHash != in.TargetHash {
		return nil, conflict("Exact proposal/source target hash differs")
	}
	installation, err := s.models.InstallationIDTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	equivalence, err := previewEquivalence(in)
	if err != nil {
		return nil, err
	}
	out := &Preview{Input: in, Effects: []Effect{}, Coverage: []Coverage{}, Diagnostics: []string{}, Equivalence: equivalence, CanApply: true}
	if err = s.checkTargets(ctx, tx, out.Input.Targets, installation); err != nil {
		return nil, err
	}
	// Prepare APIs before scenarios so all linked documents have one normalized plan.
	for i := range out.Input.Targets {
		if t := &out.Input.Targets[i]; t.Kind == "api_design" {
			if err = s.prepareAPITarget(ctx, tx, installation, g, t); err != nil {
				return nil, err
			}
		}
	}
	for i := range out.Input.Targets {
		if t := &out.Input.Targets[i]; t.Kind == "design_scenario" {
			//nolint:contextcheck // designscenario.PrepareMaterialization takes no context; threading one is a signature change in that package, and the call is bounded in-memory validation
			if err = s.prepareScenarioTarget(t, out.Input.Targets); err != nil {
				return nil, err
			}
		}
	}
	out.Effects = append(out.Effects, targetEffects(out.Input.Targets)...)
	if err := s.coverageTx(ctx, tx, pid, g, out); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(out.Input)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBytes {
		return nil, quota()
	}
	// Bind the original source-copy pins as well as the normalized result.
	out.CandidateHash, err = digest(struct {
		Domain, Project string
		Request         PreviewInput
		Plan            *Preview
	}{"backend-materialization-v1", pid, input, out})
	return out, err
}

// checkPreviewShape requires the supported profile, an exact full proposal
// target, and bounded target and source-scope lists.
func checkPreviewShape(in PreviewInput) error {
	if in.ProfileVersion != ProfileVersion || in.Target.ChangeProposal == nil || !validHash(in.TargetHash) {
		return invalid("Exact full proposal, targetHash and supported profile are required")
	}
	if len(in.Targets) == 0 || len(in.Targets) > 5 {
		return quota()
	}
	if len(in.SourceScope) == 0 || len(in.SourceScope) > 1000 {
		return invalid("Select 1–1000 explicit source identities")
	}
	return nil
}

// previewEquivalence: excluding identities is a partial simulation, which
// must be consented to with a reason.
func previewEquivalence(in PreviewInput) (string, error) {
	if in.PartialSimulation {
		if len(in.ExcludedIDs) == 0 || strings.TrimSpace(in.Reason) == "" {
			return "", invalid("Partial simulation requires excluded IDs and a reason")
		}
		return "partial_simulation", nil
	}
	if len(in.ExcludedIDs) != 0 {
		return "", invalid("Excluded IDs require explicit partialSimulation consent")
	}
	return "structural_projection", nil
}

// checkTargets requires unique keys, supported owners, one replacement
// command each within the command quota, and for an existing owner its
// exact current draft.
func (s *Service) checkTargets(ctx context.Context, tx *sql.Tx, targets []Target, installation string) error {
	keys := map[string]bool{}
	owners := map[string]bool{}
	commands := 0
	for i := range targets {
		t := &targets[i]
		if strings.TrimSpace(t.Key) == "" || len(t.Key) > 100 || keys[t.Key] {
			return invalid("Target keys must be unique nonempty strings")
		}
		keys[t.Key] = true
		if t.Kind != "api_design" && t.Kind != "design_scenario" {
			return invalid("Unsupported target owner")
		}
		commands += len(t.Commands)
		if commands > 100 {
			return quota()
		}
		if len(t.Commands) != 1 {
			return invalid("Each owner requires one normalized replacement command")
		}
		if t.Pin == nil {
			if err := checkNewTarget(t); err != nil {
				return err
			}
			continue
		}
		pin, err := t.Pin.LocalPin(installation)
		if err != nil {
			return err
		}
		if pin.Kind != t.Kind || owners[pin.Kind+":"+pin.ID] {
			return invalid("Duplicate or mismatched target owner")
		}
		owners[pin.Kind+":"+pin.ID] = true
		if err := s.checkOwner(ctx, tx, pin, t.ExpectedVersion); err != nil {
			return err
		}
	}
	return nil
}

func checkNewTarget(t *Target) error {
	if t.ExpectedVersion != 0 || strings.TrimSpace(t.Name) == "" {
		return invalid("New target requires a name and expectedVersion=0")
	}
	// apidesign.CreateTx refuses a trimmed name over 200 runes, so such a
	// preview was applicable and its Apply always failed (review
	// 2026-10-06, F184).
	if t.Kind == "api_design" && len([]rune(strings.TrimSpace(t.Name))) > 200 {
		return invalid("New API target name must have 1 to 200 characters")
	}
	return nil
}

// prepareAPITarget normalizes an API target's command to an explicit
// replacement document and lets the API owner prepare it.
func (s *Service) prepareAPITarget(ctx context.Context, tx *sql.Tx, installation string, g *backendmodel.EffectiveGraphSnapshot, t *Target) error {
	c := &t.Commands[0]
	if c.Scenario != nil {
		return invalid("API command cannot carry a scenario")
	}
	switch c.Type {
	case "copy_api_object":
		if c.CopyFrom == nil || c.Selector == nil || c.APIDocument == "" || c.Destination == "" {
			return invalid("Object copy requires source pin, selector and destination document/path")
		}
		if err := s.copyAPIObject(ctx, tx, installation, g, c); err != nil {
			return err
		}
	case "copy_api_document":
		if err := s.copyAPIDocument(ctx, tx, installation, g, c); err != nil {
			return err
		}
	case "replace_api_document":
		if c.CopyFrom != nil || c.Selector != nil || c.Destination != "" || c.APIDocument == "" {
			return invalid("API replacement requires an explicit document")
		}
	default:
		return invalid("Unsupported API command")
	}
	id := int64(0)
	if t.Pin != nil {
		id = ownerID(t.Pin.Pin.ID)
	}
	var err error
	c.APIDocument, err = s.apis.PrepareMaterializationTx(ctx, tx, id, t.ExpectedVersion, c.APIDocument)
	return err
}

// copyAPIDocument turns a whole-document copy into a replacement with the
// source revision's document, which the selected proposal must pin exactly.
func (s *Service) copyAPIDocument(ctx context.Context, tx *sql.Tx, installation string, g *backendmodel.EffectiveGraphSnapshot, c *Command) error {
	if c.Selector != nil || c.Destination != "" {
		return invalid("Document copy cannot carry object selectors")
	}
	if c.CopyFrom == nil || c.APIDocument != "" {
		return invalid("Copy requires only an exact source owner pin")
	}
	p, err := c.CopyFrom.LocalPin(installation)
	if err != nil {
		return err
	}
	if p.Kind != "api_design" {
		return invalid("API copy requires API owner")
	}
	if !containsPin(g, p, *c.CopyFrom) {
		return invalid("Copy source is not pinned by the selected proposal")
	}
	rev, err := s.apis.VerifiedRevisionTx(ctx, tx, ownerID(p.ID), ownerID(p.RevisionID))
	if err != nil {
		return err
	}
	if rev.Hash != p.ContentHash {
		return conflict("Copy source digest changed")
	}
	c.APIDocument = rev.Document
	c.CopyFrom = nil
	c.Type = "replace_api_document"
	return nil
}

// prepareScenarioTarget admits only the supported sequential profile, with
// every contract linked to a planned API target's exact pin and document.
func (s *Service) prepareScenarioTarget(t *Target, targets []Target) error {
	c := &t.Commands[0]
	if c.Type != "replace_scenario" || c.Scenario == nil || c.CopyFrom != nil || c.Selector != nil || c.Destination != "" || c.APIDocument != "" {
		return invalid("Scenario requires an explicit typed document")
	}
	if len(c.Scenario.Fragments) > 0 || c.Scenario.EventModel != nil {
		return invalid("Loops, branches, parallelism and events require an unsupported profile")
	}
	for _, m := range c.Scenario.Messages {
		if m.Operation == nil || len(m.EventBindings) > 0 || m.Kind != "request" {
			return invalid("Only sequential explicitly bound synchronous HTTP messages are supported")
		}
	}
	for _, contract := range c.Scenario.Contracts {
		if err := checkLinkedContract(contract, targets); err != nil {
			return err
		}
	}
	document, err := s.scenarios.PrepareMaterialization(*c.Scenario)
	if err != nil {
		return err
	}
	c.Scenario = &document
	return nil
}

// checkLinkedContract requires a scenario contract to link a planned API
// target at exactly its planned pin and document.
func checkLinkedContract(contract designscenario.Contract, targets []Target) error {
	if contract.Mode != "linked" || contract.Source == nil {
		return invalid("Scenario contracts require exact linked API targets")
	}
	api := findAPI(targets, contract.Source.DesignID)
	if api == nil {
		return invalid("Unplanned linked API effect; include its exact target")
	}
	if api.Pin.Pin.RevisionID != strconv.FormatInt(contract.Source.RevisionID, 10) || api.ExpectedVersion != contract.Source.Version {
		return conflict("Linked contract differs from the planned API pin")
	}
	if !equalJSON(string(contract.Document), api.Commands[0].APIDocument) {
		return invalid("Linked contract differs from the planned API document")
	}
	return nil
}

// targetEffects lists one effect per target; an existing API lists the
// scenario targets whose contracts link it.
func targetEffects(targets []Target) []Effect {
	effects := make([]Effect, 0, len(targets))
	for _, t := range targets {
		e := Effect{TargetKey: t.Key, Kind: t.Kind, DraftMock: t.Kind == "api_design", LinkedFrom: []string{}}
		if t.Pin != nil && t.Kind == "api_design" {
			for _, other := range targets {
				if other.Kind != "design_scenario" {
					continue
				}
				for _, c := range other.Commands[0].Scenario.Contracts {
					if c.Source.DesignID == ownerID(t.Pin.Pin.ID) {
						e.LinkedFrom = append(e.LinkedFrom, other.Key)
					}
				}
			}
		}
		effects = append(effects, e)
	}
	return effects
}

func containsPin(g *backendmodel.EffectiveGraphSnapshot, p backendmodel.ArtifactPin, n backendmodel.NamespacedArtifactPin) bool {
	if g.Pins.ArtifactContextV3 != nil {
		for _, group := range g.Pins.ArtifactContextV3.Groups {
			if group.Namespace == n.Namespace && slices.Contains(group.Pins, p) {
				return true
			}
		}
		return false
	}
	return slices.Contains(g.Pins.ArtifactPins, p)
}
func findAPI(targets []Target, id int64) *Target {
	for i := range targets {
		t := &targets[i]
		if t.Kind == "api_design" && t.Pin != nil && ownerID(t.Pin.Pin.ID) == id {
			return t
		}
	}
	return nil
}
func (s *Service) checkOwner(ctx context.Context, tx *sql.Tx, p backendmodel.ArtifactPin, expected int64) error {
	if expected <= 0 {
		return invalid("Existing target requires expectedVersion")
	}
	if p.Kind == "api_design" {
		d, v, err := s.apis.DraftTx(ctx, tx, ownerID(p.ID))
		if err != nil {
			return err
		}
		if d.Version != expected || v.ID != ownerID(p.RevisionID) || v.Hash != p.ContentHash {
			return conflict("API draft/version/hash changed")
		}
	} else {
		d, v, err := s.scenarios.DraftTx(ctx, tx, ownerID(p.ID))
		if err != nil {
			return err
		}
		if d.Version != expected || v.ID != ownerID(p.RevisionID) || v.Hash != p.ContentHash {
			return conflict("Scenario draft/version/hash changed")
		}
	}
	return nil
}

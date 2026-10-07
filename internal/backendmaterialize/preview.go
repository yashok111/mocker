package backendmaterialize

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func ownerID(v string) int64 { n, _ := strconv.ParseInt(v, 10, 64); return n }

func (s *Service) previewTx(ctx context.Context, tx *sql.Tx, pid string, input PreviewInput) (*Preview, error) {
	in, err := cloneInput(input)
	if err != nil {
		return nil, err
	}
	if in.ProfileVersion != ProfileVersion || in.Target.ChangeProposal == nil || !validHash(in.TargetHash) {
		return nil, invalid("Exact full proposal, targetHash and supported profile are required")
	}
	if len(in.Targets) == 0 || len(in.Targets) > 5 {
		return nil, quota()
	}
	if len(in.SourceScope) == 0 || len(in.SourceScope) > 1000 {
		return nil, invalid("Select 1–1000 explicit source identities")
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
	out := &Preview{Input: in, Effects: []Effect{}, Coverage: []Coverage{}, Diagnostics: []string{}, Equivalence: "structural_projection", CanApply: true}
	if in.PartialSimulation {
		if len(in.ExcludedIDs) == 0 || strings.TrimSpace(in.Reason) == "" {
			return nil, invalid("Partial simulation requires excluded IDs and a reason")
		}
		out.Equivalence = "partial_simulation"
	} else if len(in.ExcludedIDs) != 0 {
		return nil, invalid("Excluded IDs require explicit partialSimulation consent")
	}
	keys := map[string]bool{}
	owners := map[string]bool{}
	commands := 0
	for i := range out.Input.Targets {
		t := &out.Input.Targets[i]
		if strings.TrimSpace(t.Key) == "" || len(t.Key) > 100 || keys[t.Key] {
			return nil, invalid("Target keys must be unique nonempty strings")
		}
		keys[t.Key] = true
		if t.Kind != "api_design" && t.Kind != "design_scenario" {
			return nil, invalid("Unsupported target owner")
		}
		commands += len(t.Commands)
		if commands > 100 {
			return nil, quota()
		}
		if len(t.Commands) != 1 {
			return nil, invalid("Each owner requires one normalized replacement command")
		}
		if t.Pin == nil {
			if t.ExpectedVersion != 0 || strings.TrimSpace(t.Name) == "" {
				return nil, invalid("New target requires a name and expectedVersion=0")
			}
		} else {
			pin, err := t.Pin.LocalPin(installation)
			if err != nil {
				return nil, err
			}
			if pin.Kind != t.Kind || owners[pin.Kind+":"+pin.ID] {
				return nil, invalid("Duplicate or mismatched target owner")
			}
			owners[pin.Kind+":"+pin.ID] = true
			if err := s.checkOwner(ctx, tx, pin, t.ExpectedVersion); err != nil {
				return nil, err
			}
		}
	}
	// Prepare APIs before scenarios so all linked documents have one normalized plan.
	for i := range out.Input.Targets {
		t := &out.Input.Targets[i]
		if t.Kind != "api_design" {
			continue
		}
		c := &t.Commands[0]
		if c.Scenario != nil {
			return nil, invalid("API command cannot carry a scenario")
		}
		switch c.Type {
		case "copy_api_object":
			if c.CopyFrom == nil || c.Selector == nil || c.APIDocument == "" || c.Destination == "" {
				return nil, invalid("Object copy requires source pin, selector and destination document/path")
			}
			if err := s.copyAPIObject(ctx, tx, installation, g, c); err != nil {
				return nil, err
			}
		case "copy_api_document":
			if c.Selector != nil || c.Destination != "" {
				return nil, invalid("Document copy cannot carry object selectors")
			}
			if c.CopyFrom == nil || c.APIDocument != "" {
				return nil, invalid("Copy requires only an exact source owner pin")
			}
			p, err := c.CopyFrom.LocalPin(installation)
			if err != nil {
				return nil, err
			}
			if p.Kind != "api_design" {
				return nil, invalid("API copy requires API owner")
			}
			if !containsPin(g, p, *c.CopyFrom) {
				return nil, invalid("Copy source is not pinned by the selected proposal")
			}
			rev, err := s.apis.VerifiedRevisionTx(ctx, tx, ownerID(p.ID), ownerID(p.RevisionID))
			if err != nil {
				return nil, err
			}
			if rev.Hash != p.ContentHash {
				return nil, conflict("Copy source digest changed")
			}
			c.APIDocument = rev.Document
			c.CopyFrom = nil
			c.Type = "replace_api_document"
		case "replace_api_document":
			if c.CopyFrom != nil || c.Selector != nil || c.Destination != "" || c.APIDocument == "" {
				return nil, invalid("API replacement requires an explicit document")
			}
		default:
			return nil, invalid("Unsupported API command")
		}
		id := int64(0)
		if t.Pin != nil {
			id = ownerID(t.Pin.Pin.ID)
		}
		c.APIDocument, err = s.apis.PrepareMaterializationTx(ctx, tx, id, t.ExpectedVersion, c.APIDocument)
		if err != nil {
			return nil, err
		}
	}
	for i := range out.Input.Targets {
		t := &out.Input.Targets[i]
		if t.Kind != "design_scenario" {
			continue
		}
		c := &t.Commands[0]
		if c.Type != "replace_scenario" || c.Scenario == nil || c.CopyFrom != nil || c.Selector != nil || c.Destination != "" || c.APIDocument != "" {
			return nil, invalid("Scenario requires an explicit typed document")
		}
		if len(c.Scenario.Fragments) > 0 || c.Scenario.EventModel != nil {
			return nil, invalid("Loops, branches, parallelism and events require an unsupported profile")
		}
		for _, m := range c.Scenario.Messages {
			if m.Operation == nil || len(m.EventBindings) > 0 || m.Kind != "request" {
				return nil, invalid("Only sequential explicitly bound synchronous HTTP messages are supported")
			}
		}
		for _, contract := range c.Scenario.Contracts {
			if contract.Mode != "linked" || contract.Source == nil {
				return nil, invalid("Scenario contracts require exact linked API targets")
			}
			api := findAPI(out.Input.Targets, contract.Source.DesignID)
			if api == nil {
				return nil, invalid("Unplanned linked API effect; include its exact target")
			}
			if api.Pin.Pin.RevisionID != strconv.FormatInt(contract.Source.RevisionID, 10) || api.ExpectedVersion != contract.Source.Version {
				return nil, conflict("Linked contract differs from the planned API pin")
			}
			if !equalJSON(string(contract.Document), api.Commands[0].APIDocument) {
				return nil, invalid("Linked contract differs from the planned API document")
			}
		}
		document, err := s.scenarios.PrepareMaterialization(*c.Scenario)
		if err != nil {
			return nil, err
		}
		c.Scenario = &document
	}
	for _, t := range out.Input.Targets {
		e := Effect{TargetKey: t.Key, Kind: t.Kind, DraftMock: t.Kind == "api_design", LinkedFrom: []string{}}
		if t.Pin != nil && t.Kind == "api_design" {
			for _, other := range out.Input.Targets {
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
		out.Effects = append(out.Effects, e)
	}
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

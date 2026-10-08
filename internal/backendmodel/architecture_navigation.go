package backendmodel

import (
	"context"
	"fmt"
	"slices"
)

const ArchitectureNavigationVersion = "architecture-navigation-v1"

type ArchitectureNavigation struct {
	Target  *BackendReadTarget `json:"target,omitzero"`
	Format  string             `json:"format"`
	Kind    string             `json:"kind"`
	Label   string             `json:"label"`
	Diagram *DiagramPin        `json:"diagram,omitzero"`
	Level   string             `json:"level,omitempty"`
	RootID  string             `json:"rootId,omitempty"`
	FocusID string             `json:"focusId,omitempty"`
	FlowID  string             `json:"flowId,omitempty"`
}

func (n *ArchitectureNavigation) UnmarshalJSON(raw []byte) error {
	type plain ArchitectureNavigation
	*n = ArchitectureNavigation{}
	if err := strictAPIObject(raw, []string{"format", "kind", "label"}, []string{"diagram", "level", "rootId", "focusId", "flowId", "target"}, (*plain)(n)); err != nil {
		return err
	}
	return n.Validate()
}
func (n ArchitectureNavigation) Validate() error {
	if n.Format != ArchitectureNavigationVersion || !validAPIText(n.Label, 1, 256) {
		return invalid("navigation", "Use architecture-navigation-v1 and a bounded label")
	}
	switch n.Kind {
	case "diagram":
		if n.Target != nil {
			return invalid("navigation/target", "Diagram destination uses its exact diagram pin")
		}
		if n.Diagram == nil || n.FlowID != "" || !ValidID(n.RootID) || !slices.Contains([]string{"context", "containers", "components"}, n.Level) || n.FocusID != "" && !ValidID(n.FocusID) {
			return invalid("navigation", "Diagram navigation requires an exact pin and valid projection root")
		}
		return n.Diagram.Validate()
	case "flow":
		if n.Target == nil {
			return invalid("navigation/target", "Flow navigation requires its exact source target")
		}
		if err := validateDiagramTarget(*n.Target); err != nil {
			return err
		}
		if !ValidID(n.FlowID) || n.Diagram != nil || n.Level != "" || n.RootID != "" || n.FocusID != "" {
			return invalid("navigation", "Flow navigation requires only an exact Flow ID")
		}
	default:
		return invalid("navigation", "Select diagram or flow navigation")
	}
	return nil
}

func validateArchitectureNavigation(elements []ArchitectureElement) error {
	pins := map[DiagramPin]bool{}
	for _, element := range elements {
		if len(element.Navigation) > 8 {
			return invalid("navigation", "At most8 explicit destinations per element")
		}
		seen := map[string]bool{}
		for _, entry := range element.Navigation {
			if err := entry.Validate(); err != nil {
				return err
			}
			hash, err := requestDigest(entry)
			if err != nil {
				return err
			}
			if seen[hash] {
				return invalid("navigation", "Duplicate navigation entry")
			}
			seen[hash] = true
			if entry.Diagram != nil {
				pins[*entry.Diagram] = true
			}
		}
	}
	if len(pins) > 128 {
		return invalid("navigation", "At most128 distinct exact destination pins per document")
	}
	return nil
}

// Navigation is authored correspondence, independent of C4 parentage. An exact
// older destination retained through a fork is a visible historical gap, never a
// permission to resolve its head or to relabel it as current-source evidence.
func resolveArchitectureNavigation(ctx context.Context, q importReader, pid string, g *EffectiveGraphSnapshot, d DiagramDocument, previous *DiagramVersion) ([]DiagramGap, error) {
	gaps := []DiagramGap{}
	if d.Kind != "architecture" {
		return gaps, nil
	}
	old := map[string]bool{}
	if previous != nil {
		for _, element := range previous.Document.Payload.Elements {
			for _, entry := range element.Navigation {
				hash, _ := requestDigest(entry)
				old[element.ID+":"+hash] = true
			}
		}
	}
	flows := map[string]bool{}
	for _, node := range g.State.Nodes {
		if node.Kind == "flow" {
			flows[node.ID] = true
		}
	}
	loaded := map[DiagramPin]*DiagramVersion{}
	for _, element := range d.Payload.Elements {
		for index, entry := range element.Navigation {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			hash, _ := requestDigest(entry)
			code, err := navigationDestination(ctx, q, pid, g.Pins.TargetHash, d.Target, entry, flows, loaded)
			if err != nil {
				return nil, err
			}
			if code == "" {
				continue
			}
			if !old[element.ID+":"+hash] {
				return nil, invalid("navigation", fmt.Sprintf("Element %s destination %d: %s", element.ID, index, code))
			}
			gaps = append(gaps, DiagramGap{ID: diagramIdentity("architecture-navigation-gap-v1", element.ID, hash, code), SubjectID: element.ID, Code: code, Explanation: fmt.Sprintf("Retained destination %d is unavailable or belongs to an older target; its exact pin was not advanced", index)})
		}
	}
	return gaps, nil
}

func navigationDestination(ctx context.Context, q importReader, pid, targetHash string, target BackendReadTarget, entry ArchitectureNavigation, flows map[string]bool, loaded map[DiagramPin]*DiagramVersion) (string, error) {
	if entry.Kind == "flow" {
		if entry.Target == nil {
			return "navigation_flow_unavailable", nil
		}
		want, _ := requestDigest(target)
		actual, _ := requestDigest(*entry.Target)
		if want != actual {
			return "navigation_historical_target", nil
		}
		if flows[entry.FlowID] {
			return "", nil
		}
		return "navigation_flow_unavailable", nil
	}
	pin := *entry.Diagram
	child := loaded[pin]
	if child == nil {
		var err error
		child, err = loadDiagram(ctx, q, pid, pin.ID, pin.Version)
		if err != nil {
			return "", err
		}
		if child.Pin != pin {
			return "", diagramPinMismatch()
		}
		loaded[pin] = child
	}
	if child.Document.Kind != "architecture" {
		return "navigation_wrong_kind", nil
	}
	if child.TargetHash != targetHash {
		return "navigation_historical_target", nil
	}
	if err := validateNavigationRoot(child.Document, entry); err != nil {
		return "", err
	}
	return "", nil
}

func validateNavigationRoot(document DiagramDocument, entry ArchitectureNavigation) error {
	role := "software_system"
	if entry.Level == "components" {
		role = "application"
	}
	if !slices.ContainsFunc(document.Payload.Elements, func(e ArchitectureElement) bool { return e.ID == entry.RootID && e.Role == role }) {
		return invalid("navigation/rootId", "Destination root does not support the selected level")
	}
	if entry.FocusID != "" && (entry.Level != "components" || !slices.ContainsFunc(document.Payload.Elements, func(e ArchitectureElement) bool {
		return e.ID == entry.FocusID && e.Role == "component" && e.ParentID == entry.RootID
	})) {
		return invalid("navigation/focusId", "Focus must belong to the exact destination application")
	}
	return nil
}

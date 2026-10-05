package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"strings"
)

func diagramUnsupported() error {
	return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Only architecture diagrams on exact source/full proposal targets are supported"}
}
func validateDiagramTarget(t BackendReadTarget) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if t.Proposal != nil || t.ImportCandidate != nil {
		return diagramUnsupported()
	}
	return nil
}
func validateDiagramOrigin(o DiagramOrigin) error {
	switch o.Kind {
	case "authored":
		if !validAPIText(o.Reason, 1, 4096) || strings.TrimSpace(o.Reason) == "" || o.Evidence != nil {
			return invalid("origin", "Authored intent needs a reason, without source proof")
		}
	case "source_assertion":
		if o.Reason != "" || len(o.Evidence) == 0 || len(o.Evidence) > 20 {
			return invalid("origin", "Source assertions require 1–20 exact evidence refs")
		}
		seen := map[DiagramEvidenceRef]bool{}
		for _, e := range o.Evidence {
			if !ValidID(e.RevisionID) || !ValidID(e.EvidenceID) || !ValidID(e.SubjectID) || seen[e] {
				return invalid("evidence", "Invalid or duplicate proof")
			}
			seen[e] = true
		}
	default:
		return invalid("origin", "Unknown origin")
	}
	return nil
}
func validateDiagramRef(r DiagramRef) error {
	switch r.Kind {
	case "record":
		if !slices.Contains([]string{"node", "edge"}, r.RecordType) || !ValidID(r.ID) || r.Locator != nil || r.RowID != "" {
			return invalid("ref", "Exact record required")
		}
	case "artifact":
		if r.Locator == nil || r.RowID == "" || len(r.RowID) > 4096 || r.RecordType != "" || r.ID != "" {
			return invalid("ref", "Exact artifact row required")
		}
		raw, err := json.Marshal(r.Locator)
		if err != nil {
			return err
		}
		var locator ArtifactProjectionLocator
		if err = json.Unmarshal(raw, &locator); err != nil {
			return err
		}
	default:
		return invalid("ref", "Unknown reference kind")
	}
	return nil
}
func validateDiagramBase(id, label string, origin DiagramOrigin, refs []DiagramRef) error {
	if !ValidID(id) || !validAPIText(label, 1, 256) || refs == nil || len(refs) > 100 {
		return invalid("element", "Invalid identity, label or reference array")
	}
	if err := validateDiagramOrigin(origin); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if err := validateDiagramRef(ref); err != nil {
			return err
		}
		key, _ := requestDigest(ref)
		if seen[key] {
			return invalid("refs", "Duplicate reference")
		}
		seen[key] = true
	}
	return nil
}
func (d DiagramDocument) Validate() error {
	if d.Format != DiagramDocumentVersion || d.Kind != "architecture" {
		return diagramUnsupported()
	}
	if err := validateDiagramTarget(d.Target); err != nil {
		return err
	}
	p := d.Payload
	if p.Elements == nil || p.Links == nil || len(p.Elements) > 1000 || len(p.Links) > 3000 {
		return invalid("payload", "Required arrays exceed architecture limits")
	}
	elements := map[string]ArchitectureElement{}
	ids := map[string]bool{}
	for _, e := range p.Elements {
		if err := validateDiagramBase(e.ID, e.Label, e.Origin, e.Refs); err != nil {
			return err
		}
		if ids[e.ID] {
			return invalid("id", "Duplicate semantic identity")
		}
		ids[e.ID] = true
		elements[e.ID] = e
		if !validAPIText(e.Responsibility, 0, 4096) || !validAPIText(e.Technology, 0, 4096) {
			return invalid("element", "Architecture text exceeds limits")
		}
	}
	if err := validateArchitectureParents(p, elements); err != nil {
		return err
	}
	if err := validateArchitectureLinks(p, elements, ids); err != nil {
		return err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return &FaultError{Status: 413, Code: "backend_diagram_limit", Message: "Diagram exceeds 1 MiB"}
	}
	return nil
}

func validateArchitectureParents(p ArchitecturePayload, elements map[string]ArchitectureElement) error {
	if primary, ok := elements[p.PrimarySystemID]; !ok || primary.Role != "software_system" {
		return invalid("primarySystemId", "Select an explicit software system")
	}
	for _, e := range p.Elements {
		parent, ok := elements[e.ParentID]
		switch e.Role {
		case "person", "software_system":
			if e.ParentID != "" {
				return invalid("parentId", "Top-level elements cannot have parents")
			}
		case "application", "data_store":
			if !ok || parent.Role != "software_system" {
				return invalid("parentId", "Containers require a software system parent")
			}
		case "component":
			if !ok || parent.Role != "application" {
				return invalid("parentId", "Components require an application parent")
			}
		default:
			return invalid("role", "Unknown architecture role")
		}
	}

	return nil
}

func validateArchitectureLinks(p ArchitecturePayload, elements map[string]ArchitectureElement, ids map[string]bool) error {
	for _, l := range p.Links {
		if err := validateDiagramBase(l.ID, l.Label, l.Origin, l.Refs); err != nil {
			return err
		}
		if ids[l.ID] {
			return invalid("id", "Duplicate semantic identity")
		}
		ids[l.ID] = true
		if _, ok := elements[l.From]; !ok {
			return invalid("from", "Missing source element")
		}
		if _, ok := elements[l.To]; !ok {
			return invalid("to", "Missing destination element")
		}
		if !validAPIText(l.Relation, 1, 256) {
			return invalid("relation", "Bounded relation required")
		}
	}

	return nil
}

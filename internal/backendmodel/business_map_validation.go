package backendmodel

import (
	"encoding/json/v2"
	"slices"
)

func validateBusinessMap(d DiagramDocument) error {
	p := d.BusinessMap
	if p == nil || d.Interactions != nil || d.Lifecycle != nil || d.Payload.Elements != nil || d.Payload.Links != nil || d.Payload.PrimarySystemID != "" {
		return invalid("payload", "Exactly one business map payload required")
	}
	if p.Elements == nil || p.Links == nil || len(p.Elements) > 1000 || len(p.Links) > 3000 {
		return invalid("payload", "Required arrays exceed business map limits")
	}
	if p.Architecture != nil {
		if err := p.Architecture.Validate(); err != nil {
			return err
		}
	}
	roles, err := validateBusinessElements(p)
	if err != nil {
		return err
	}
	if err = validateBusinessLinks(p, roles); err != nil {
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
func businessRolePair(from, to, relation string) bool {
	switch relation {
	case "initiates":
		return from == "actor" && to == "command"
	case "produces":
		return from == "command" && to == "business_event"
	case "reacts_to":
		return from == "business_event" && to == "policy"
	case "issues":
		return from == "policy" && to == "command"
	case "updates":
		return from == "business_event" && to == "read_model"
	case "reads":
		return slices.Contains([]string{"actor", "command", "policy"}, from) && to == "read_model"
	case "questions":
		return from == "question"
	}
	return false
}

func validateBusinessElements(p *BusinessMapPayload) (map[string]string, error) {
	ids := map[string]bool{}
	roles := map[string]string{}
	for _, e := range p.Elements {
		if err := validateDiagramBase(e.ID, e.Label, e.Origin, e.Refs); err != nil {
			return nil, err
		}
		if ids[e.ID] {
			return nil, invalid("id", "Duplicate semantic identity")
		}
		ids[e.ID] = true
		roles[e.ID] = e.Role
		if !slices.Contains([]string{"actor", "command", "business_event", "policy", "read_model", "question"}, e.Role) || !validAPIText(e.Responsibility, 0, 4096) {
			return nil, invalid("element", "Unknown role or oversized responsibility")
		}
		if e.ArchitectureElementID != "" && (p.Architecture == nil || !ValidID(e.ArchitectureElementID)) {
			return nil, invalid("architectureElementId", "Exact architecture pin required for membership")
		}
	}

	return roles, nil
}

func validateBusinessLinks(p *BusinessMapPayload, roles map[string]string) error {
	ids := map[string]bool{}
	for id := range roles {
		ids[id] = true
	}
	for _, l := range p.Links {
		if err := validateDiagramBase(l.ID, l.Label, l.Origin, l.Refs); err != nil {
			return err
		}
		if ids[l.ID] {
			return invalid("id", "Duplicate semantic identity")
		}
		ids[l.ID] = true
		from, to := roles[l.From], roles[l.To]
		if from == "" || to == "" || !businessRolePair(from, to, l.Relation) || l.Relation == "questions" && l.From == l.To {
			return invalid("relation", "Business relation is outside the closed role-pair matrix")
		}
	}

	return nil
}

package backendmodel

import (
	"encoding/json/v2"
	"slices"
)

type interactionValidator struct {
	ids          map[string]bool
	participants map[string]bool
	branches     map[string]InteractionBranch
	steps        map[string]InteractionStep
}

func (v *interactionValidator) take(id string) error {
	if !ValidID(id) || v.ids[id] {
		return invalid("id", "Invalid or duplicate semantic identity")
	}
	v.ids[id] = true
	return nil
}
func validateInteractions(d DiagramDocument) error {
	if err := validateInteractionShape(d); err != nil {
		return err
	}
	v := interactionValidator{ids: map[string]bool{}, participants: map[string]bool{}, branches: map[string]InteractionBranch{}, steps: map[string]InteractionStep{}}
	if err := v.validateParticipants(d.Interactions); err != nil {
		return err
	}
	if err := v.validateBranches(d.Interactions.Branches); err != nil {
		return err
	}
	if err := v.validateSteps(d.Interactions.Steps); err != nil {
		return err
	}
	if err := v.validateReplies(); err != nil {
		return err
	}
	if err := v.validateOrder(d.Interactions.Order); err != nil {
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
func validateInteractionShape(d DiagramDocument) error {
	p := d.Interactions
	if p == nil || d.Payload.Elements != nil || d.Payload.Links != nil || d.Payload.PrimarySystemID != "" {
		return invalid("payload", "Exactly one interactions payload required")
	}
	if p.Participants == nil || p.Steps == nil || p.Order == nil || p.Branches == nil || p.ScopeRefs == nil {
		return invalid("payload", "Nonnull interaction arrays required")
	}
	if len(p.Participants)+len(p.Steps)+len(p.Branches) > 1000 || len(p.Order) > 3000 || len(p.ScopeRefs) > 100 {
		return invalid("payload", "Interaction limits exceeded")
	}
	if p.Architecture != nil {
		if err := p.Architecture.Validate(); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, ref := range p.ScopeRefs {
		if err := validateDiagramRef(ref); err != nil {
			return err
		}
		key, _ := requestDigest(ref)
		if seen[key] {
			return invalid("scopeRefs", "Duplicate scope reference")
		}
		seen[key] = true
	}
	return nil
}
func (v *interactionValidator) validateParticipants(p *InteractionPayload) error {
	for _, row := range p.Participants {
		if err := v.take(row.ID); err != nil {
			return err
		}
		if err := validateDiagramBase(row.ID, row.Label, row.Origin, row.Refs); err != nil {
			return err
		}
		if row.ArchitectureElementID != "" && (p.Architecture == nil || !ValidID(row.ArchitectureElementID)) {
			return invalid("architectureElementId", "Exact architecture pin and member required")
		}
		v.participants[row.ID] = true
	}
	return nil
}
func (v *interactionValidator) validateBranches(rows []InteractionBranch) error {
	groups := map[string]InteractionBranch{}
	for _, row := range rows {
		if err := v.take(row.ID); err != nil {
			return err
		}
		if !ValidID(row.GroupID) || !validAPIText(row.Label, 1, 256) || !validAPIText(row.GuardText, 0, 4096) || !slices.Contains([]string{"alternative", "parallel", "loop"}, row.Kind) {
			return invalid("branch", "Invalid branch declaration")
		}
		if err := validateDiagramOrigin(row.Origin); err != nil {
			return err
		}
		if prior, ok := groups[row.GroupID]; ok && (prior.ParentID != row.ParentID || prior.Kind != row.Kind) {
			return invalid("branch", "Group must share parent and fragment kind")
		}
		groups[row.GroupID] = row
		v.branches[row.ID] = row
	}
	return v.validateBranchParents()
}
func (v *interactionValidator) validateBranchParents() error {
	for _, row := range v.branches {
		seen := map[string]bool{row.ID: true}
		for parent := row.ParentID; parent != ""; {
			b, ok := v.branches[parent]
			if !ok || seen[parent] {
				return invalid("branch", "Missing or cyclic parent chain")
			}
			seen[parent] = true
			parent = b.ParentID
		}
	}
	return nil
}
func (v *interactionValidator) validateSteps(rows []InteractionStep) error {
	for _, row := range rows {
		if err := v.take(row.ID); err != nil {
			return err
		}
		if err := validateDiagramBase(row.ID, row.Label, row.Origin, row.Refs); err != nil {
			return err
		}
		if !v.participants[row.From] || row.To != "" && !v.participants[row.To] {
			return invalid("participant", "Step endpoint is not an owned participant")
		}
		if !slices.Contains([]string{"request", "response", "send", "receive", "error", "boundary"}, row.Kind) || row.To == "" && row.Kind != "send" && row.Kind != "boundary" {
			return invalid("step", "Invalid kind or missing receiver")
		}
		if err := v.validateBranchPath(row.BranchPath); err != nil {
			return err
		}
		v.steps[row.ID] = row
	}
	return nil
}
func (v *interactionValidator) validateBranchPath(path []string) error {
	if path == nil || len(path) > len(v.branches) {
		return invalid("branchPath", "Required bounded parent chain")
	}
	parent := ""
	for _, id := range path {
		b, ok := v.branches[id]
		if !ok || b.ParentID != parent {
			return invalid("branchPath", "Path must follow complete parent chain")
		}
		parent = id
	}
	return nil
}
func (v *interactionValidator) validateReplies() error {
	for _, row := range v.steps {
		if row.ReplyTo == "" {
			continue
		}
		request, ok := v.steps[row.ReplyTo]
		if !ok || request.Kind != "request" || row.Kind != "response" || row.From != request.To || row.To != request.From {
			return invalid("replyTo", "Response must address its owned request endpoints")
		}
	}
	return nil
}
func (v *interactionValidator) alternatives(a, b InteractionStep) bool {
	for _, left := range a.BranchPath {
		for _, right := range b.BranchPath {
			if left != right && v.branches[left].GroupID == v.branches[right].GroupID && v.branches[left].Kind == "alternative" {
				return true
			}
		}
	}
	return false
}
func (v *interactionValidator) validateOrder(rows []InteractionOrder) error {
	outgoing := map[string][]string{}
	degree := map[string]int{}
	pairs := map[[2]string]bool{}
	for _, row := range rows {
		if err := v.take(row.ID); err != nil {
			return err
		}
		if err := validateDiagramOrigin(row.Origin); err != nil {
			return err
		}
		_, fromOK := v.steps[row.From]
		_, toOK := v.steps[row.To]
		pair := [2]string{row.From, row.To}
		if !fromOK || !toOK || row.From == row.To || pairs[pair] {
			return invalid("order", "Missing endpoints, self edge or duplicate pair")
		}
		pairs[pair] = true
		outgoing[row.From] = append(outgoing[row.From], row.To)
		degree[row.To]++
	}
	queue := []string{}
	for id := range v.steps {
		if degree[id] == 0 {
			queue = append(queue, id)
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, id := range outgoing[queue[i]] {
			degree[id]--
			if degree[id] == 0 {
				queue = append(queue, id)
			}
		}
	}
	if len(queue) != len(v.steps) {
		return invalid("order", "Order must be an acyclic partial order")
	}
	return v.validateAlternativeReachability(outgoing)
}
func (v *interactionValidator) validateAlternativeReachability(outgoing map[string][]string) error {
	// Transitive paths through a common unbranched row must not join alternatives either.
	for id, step := range v.steps {
		if len(step.BranchPath) == 0 {
			continue
		}
		queue := slices.Clone(outgoing[id])
		seen := map[string]bool{}
		for i := 0; i < len(queue); i++ {
			next := queue[i]
			if seen[next] {
				continue
			}
			seen[next] = true
			if v.alternatives(step, v.steps[next]) {
				return invalid("order", "Alternative branches cannot form one execution path")
			}
			queue = append(queue, outgoing[next]...)
		}
	}
	return nil
}

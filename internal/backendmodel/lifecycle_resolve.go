package backendmodel

import (
	"context"
	"slices"
)

func lifecycleRowBasis(v any) (DiagramOrigin, []DiagramRef) {
	switch v := v.(type) {
	case LifecycleState:
		return v.Origin, v.Refs
	case LifecycleTransition:
		refs := slices.Clone(v.Refs)
		refs = append(refs, v.Triggers...)
		refs = append(refs, v.Writes...)
		refs = append(refs, v.Events...)
		unique := []DiagramRef{}
		seen := map[string]bool{}
		for _, r := range refs {
			k, _ := requestDigest(r)
			if !seen[k] {
				unique = append(unique, r)
				seen[k] = true
			}
		}
		return v.Origin, unique
	case LifecycleRule:
		return v.Origin, []DiagramRef{v.Trigger}
	}
	return DiagramOrigin{}, nil
}
func resolveLifecycleGaps(ctx context.Context, resolver *diagramArtifactResolver, d DiagramDocument, previous *DiagramVersion, nodes, edges map[string]bool) ([]DiagramGap, error) {
	p := d.Lifecycle
	if p == nil {
		return nil, nil
	}
	g := resolver.graph
	gaps := []DiagramGap{}
	var old *LifecyclePayload
	if previous != nil {
		old = previous.Document.Lifecycle
	}
	meta := diagramIdentity("lifecycle-scope-v1")
	oldRefs := []DiagramRef{}
	if old != nil {
		oldRefs = append(oldRefs, old.Entity)
		oldRefs = append(oldRefs, old.StateFields...)
	}
	refs := append([]DiagramRef{p.Entity}, p.StateFields...)
	extra, err := diagramReferenceGaps(resolver, meta, refs, oldRefs, nodes, edges)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, extra...)
	byID := map[string]Node{}
	for _, n := range g.State.Nodes {
		byID[n.ID] = n
	}
	edgeKinds := map[string]string{}
	for _, e := range g.State.Edges {
		edgeKinds[e.ID] = e.Kind
	}
	historical := lifecycleRoleRefs(old)
	currentEntity, _ := requestDigest(p.Entity)
	if old != nil {
		oldEntity, _ := requestDigest(old.Entity)
		if currentEntity != oldEntity {
			delete(historical, "field:")
		}
	}
	check := func(ref DiagramRef, role, subject string) error {
		retained := func() error {
			hash, _ := requestDigest(ref)
			for _, oldRef := range historical[role+":"+subject] {
				oldHash, _ := requestDigest(oldRef)
				if hash == oldHash {
					return nil
				}
			}
			return invalid("refs", "Historical lifecycle reference cannot change semantic role or entity association")
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		if ref.Kind == "namespaced_artifact" && ref.NamespacedLocator != nil && ref.NamespacedLocator.Namespace.Scope == "foreign" {
			if _, err := namespacedDiagramGroup(g, ref); err != nil {
				return err
			}
			gaps = append(gaps, diagramGap(subject, "foreign_artifact_unresolved", "Foreign artifact role remains unverified"))
			return nil
		}
		if ref.Kind == "artifact" || ref.Kind == "namespaced_artifact" {
			locator := ref.Locator
			if ref.NamespacedLocator != nil {
				locator = &ref.NamespacedLocator.Locator
			}
			exists, err := resolver.resolve(ref)
			if err != nil {
				return err
			}
			if !exists {
				return retained()
			}

			// Owner projections currently expose operations/events, not entity fields.
			if role == "entity" || role == "field" {
				return lifecycleSemanticRefError(role)
			}
			if role == "trigger" && locator.View == "api_operations" && locator.Owner.OperationKey != "" {
				return nil
			}
			if role == "trigger" && locator.View == "event_model" && locator.Owner.OperationID != "" {
				return nil
			}
			if role == "event" && locator.View == "event_model" && locator.Owner.MessageID != "" {
				return nil
			}
			return lifecycleSemanticRefError(role)
		}
		if ref.RecordType == "edge" {
			kind, ok := edgeKinds[ref.ID]
			if !ok {
				return retained()
			}
			if role == "write" && slices.Contains([]string{"writes", "deletes"}, kind) || role == "event" && kind == "emits" {
				return nil
			}
			return lifecycleSemanticRefError(role)
		}
		n, ok := byID[ref.ID]
		if !ok {
			return retained()
		} // Only server-authorized inherited absence reaches here.
		allowed := map[string][]string{"entity": {"domain_entity", "dto", "api_schema", "table", "view"}, "field": {"representation_field", "column", "api_field", "event_field"}, "trigger": {"http_operation", "handler", "symbol", "consumer", "job"}, "write": {"query", "flow_step", "column", "table"}, "event": {"message", "channel"}}
		if !slices.Contains(allowed[role], n.Kind) {
			return lifecycleSemanticRefError(role)
		}
		if role == "field" && p.Entity.Kind == "record" && (n.ParentID == nil || *n.ParentID != p.Entity.ID) {
			return invalid("stateFields", "Field does not belong to selected entity")
		}
		return nil
	}
	if err = check(p.Entity, "entity", ""); err != nil {
		return nil, err
	}
	for _, ref := range p.StateFields {
		if err = check(ref, "field", ""); err != nil {
			return nil, err
		}
	}
	states := map[string]LifecycleState{}
	for _, s := range p.States {
		states[s.ID] = s
	}
	for _, tr := range p.Transitions {
		for role, refs := range map[string][]DiagramRef{"trigger": tr.Triggers, "write": tr.Writes, "event": tr.Events} {
			for _, ref := range refs {
				if err = check(ref, role, tr.ID); err != nil {
					return nil, err
				}
			}
		}
		if states[tr.From].Terminal {
			gaps = append(gaps, diagramGap(tr.ID, "terminal_outgoing", "Declared terminal state has an explicit outgoing transition"))
		}
		if tr.Guard.Kind == "opaque" {
			gaps = append(gaps, diagramGap(tr.ID, "opaque_guard", "Guard is opaque; runtime outcome is unverified"))
		}
		if len(tr.Triggers) == 0 {
			gaps = append(gaps, diagramGap(tr.ID, "unresolved_trigger", "No exact operation binding is established"))
		}
	}
	for _, r := range p.Rules {
		if err = check(r.Trigger, "trigger", r.ID); err != nil {
			return nil, err
		}
	}
	if p.Coverage == "partial" {
		gaps = append(gaps, diagramGap(meta, "partial_lifecycle", "Partial coverage cannot prove missing transitions forbidden or unreachable"))
	}
	evidence := map[DiagramEvidenceRef]bool{}
	for _, e := range g.State.Evidence {
		evidence[DiagramEvidenceRef{g.Pins.BaseRevisionID, e.ID, e.SubjectID}] = true
	}
	for _, e := range g.BaselineEvidence {
		evidence[DiagramEvidenceRef{e.RevisionID, e.EvidenceID, e.SubjectID}] = true
	}
	oldProof := []DiagramEvidenceRef{}
	moved := false
	if old != nil {
		oldProof = old.CoverageOrigin.Evidence
		a, _ := requestDigest(previous.Document.Target)
		b, _ := requestDigest(d.Target)
		moved = a != b
	}
	proofGaps, err := diagramEvidenceGaps(meta, p.CoverageOrigin.Evidence, oldProof, evidence, previous, moved)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, proofGaps...)
	return gaps, nil
}
func lifecycleSemanticRefError(role string) error {
	return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Unsupported lifecycle " + role + " reference kind"}
}

// A disappeared ref retains only the role approved in its immutable predecessor.
// Membership in another row/ref bag never authorizes a new semantic claim.
func lifecycleRoleRefs(p *LifecyclePayload) map[string][]DiagramRef {
	out := map[string][]DiagramRef{}
	if p == nil {
		return out
	}
	out["entity:"] = []DiagramRef{p.Entity}
	out["field:"] = p.StateFields
	for _, tr := range p.Transitions {
		out["trigger:"+tr.ID] = tr.Triggers
		out["write:"+tr.ID] = tr.Writes
		out["event:"+tr.ID] = tr.Events
	}
	for _, r := range p.Rules {
		out["trigger:"+r.ID] = []DiagramRef{r.Trigger}
	}
	return out
}

package backendmodel

import (
	"cmp"
	"context"
	"maps"
	"slices"
)

type DiagramInteractionBuildInput struct {
	Target       BackendReadTarget `json:"target"`
	EntrypointID string            `json:"entrypointId"`
	Architecture *DiagramPin       `json:"architecture,omitzero"`
	MaxSteps     int               `json:"maxSteps"`
}
type DiagramInteractionCandidate struct {
	Document       DiagramDocument `json:"document"`
	TargetHash     string          `json:"targetHash"`
	Gaps           []DiagramGap    `json:"gaps"`
	VisitedObjects int             `json:"visitedObjects"`
	Truncated      bool            `json:"truncated"`
}

func (v *DiagramInteractionBuildInput) UnmarshalJSON(b []byte) error {
	type plain DiagramInteractionBuildInput
	*v = DiagramInteractionBuildInput{MaxSteps: 200}
	if err := strictAPIObject(b, []string{"target", "entrypointId"}, []string{"architecture", "maxSteps"}, (*plain)(v)); err != nil {
		return err
	}
	return v.Validate()
}
func (v DiagramInteractionBuildInput) Validate() error {
	if err := validateDiagramTarget(v.Target); err != nil {
		return err
	}
	if !ValidID(v.EntrypointID) || v.MaxSteps < 1 || v.MaxSteps > 1000 {
		return invalid("scope", "Exact entrypoint and maxSteps 1–1000 required")
	}
	if v.Architecture != nil {
		return v.Architecture.Validate()
	}
	return nil
}
func (r *Repo) BuildInteractions(ctx context.Context, pid string, in DiagramInteractionBuildInput) (*DiagramInteractionCandidate, error) {
	if in.MaxSteps == 0 {
		in.MaxSteps = 200
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	g, err := r.ResolveEffectiveGraph(ctx, pid, in.Target)
	if err != nil {
		return nil, err
	}
	var architecture *DiagramVersion
	if in.Architecture != nil {
		architecture, err = r.GetDiagram(ctx, pid, *in.Architecture)
		if err != nil {
			return nil, err
		}
		if architecture.Document.Kind != "architecture" || architecture.TargetHash != g.Pins.TargetHash {
			return nil, invalid("architecture", "Architecture must share exact target")
		}
	}
	candidate, err := buildInteractions(ctx, g, in, architecture)
	if err != nil {
		return nil, err
	}
	if interactionInputObjects(g) > 250000 {
		return candidate, nil
	}
	if err = resolveInteractionCandidateGaps(ctx, g, candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

// resolveInteractionCandidateGaps adds the evidence gaps of the candidate
// document. Both the builder and evidence resolution report an
// unresolved_receiver for the same step, so the merged list is sorted and
// de-duplicated by gap ID (review 2026-10-06, F104).
func resolveInteractionCandidateGaps(ctx context.Context, g *EffectiveGraphSnapshot, candidate *DiagramInteractionCandidate) error {
	gaps, err := resolveDiagramEvidence(ctx, g, candidate.Document, nil)
	if err != nil {
		return err
	}
	candidate.Gaps = sortDiagramGaps(append(candidate.Gaps, gaps...))
	return nil
}

type interactionFrontier struct {
	node, last string
	branches   []string
	control    []DiagramEvidenceRef
}

func buildInteractions(ctx context.Context, g *EffectiveGraphSnapshot, in DiagramInteractionBuildInput, architecture *DiagramVersion) (*DiagramInteractionCandidate, error) {
	payload := &InteractionPayload{Architecture: in.Architecture, ScopeRefs: []DiagramRef{{Kind: "record", RecordType: "node", ID: in.EntrypointID}}, Participants: []InteractionParticipant{}, Steps: []InteractionStep{}, Branches: []InteractionBranch{}, Order: []InteractionOrder{}}
	result := &DiagramInteractionCandidate{Document: DiagramDocument{Format: DiagramDocumentVersion, Kind: "interactions", Target: in.Target, Interactions: payload}, TargetHash: g.Pins.TargetHash, Gaps: []DiagramGap{}}
	// Check the materialized input before allocating indexes. Graph resolution has its own budget.
	if interactionInputObjects(g) > 250000 {
		return oversizedInteractionCandidate(g, in)
	}
	nodes, out, proofs := interactionBuildIndex(g, result)
	entry, ok := nodes[in.EntrypointID]
	if !ok {
		return nil, notFound()
	}
	schema := g.Pins.StructuralSchemaVersion
	if schema == "" {
		schema = g.State.Revision.SchemaVersion
	}
	if !interactionEntrypointSupported(schema, entry.Kind) {
		return nil, diagramUnsupported()
	}
	relevant := &runtimeFlowProjection{schema: schema, nodes: nodes}
	origin := interactionOriginReader(g, proofs)
	membership := map[string][]string{}
	if architecture != nil {
		for _, e := range architecture.Document.Payload.Elements {
			for _, ref := range e.Refs {
				if ref.Kind == "record" && ref.RecordType == "node" {
					membership[ref.ID] = append(membership[ref.ID], e.ID)
				}
			}
		}
	}
	participantIDs := map[string]string{}
	participant := interactionParticipantReader(nodes, membership, participantIDs, payload, result, architecture, origin)
	if err := walkInteractions(ctx, g, in, payload, result, nodes, out, relevant, origin, participant, entry); err != nil {
		return nil, err
	}
	if interactionHasBoundary(result) {
		retainInteractionFrontier(result, in, participant(entry.ID), g)
	}
	normalized, err := normalizeDiagram(result.Document)
	if err != nil {
		return nil, err
	}
	result.Document = normalized
	return result, nil
}

// Reuse the event projection's positive-witness admission. A message/channel
// match alone is insufficient: unknown dispatch and multiple routes stay boundaries.
func interactionEventReceiver(ctx context.Context, g *EffectiveGraphSnapshot, emit Edge, result *DiagramInteractionCandidate) (string, error) {
	cost := len(g.State.Nodes) + len(g.State.Edges) + len(g.State.Evidence)
	if result.VisitedObjects+cost > 250000 {
		result.Truncated = true
		result.Gaps = append(result.Gaps, diagramGap(emit.ID, "event_frontier", "Event route inspection exceeds visited-object budget"))
		return "", nil
	}
	result.VisitedObjects += cost
	in := EventsQueryInput{RevisionID: g.State.Revision.ID, View: "routes", SeedNodeID: emit.From, Limit: 100}
	var effective *EffectiveGraphSnapshot
	if g.Source != nil {
		effective = g
	}
	page, err := projectEventsWithEffective(ctx, &g.State, g.Source, effective, in)
	if err != nil {
		return "", err
	}
	if page.Truncated || page.NextCursor != "" {
		result.Gaps = append(result.Gaps, diagramGap(emit.ID, "event_frontier", "Configured route projection is incomplete"))
		result.Truncated = true
		return "", nil
	}
	receiver := ""
	count := 0
	for _, item := range page.Items {
		if item.Route == nil || item.Route.References.EmitsEdgeID != emit.ID {
			continue
		}
		if item.Route.Witness.Status != "explicit" || item.Route.Witness.Provenance != "source" {
			continue
		}
		count++
		receiver = item.Route.References.ConsumerID
	}
	if count != 1 {
		return "", nil
	}
	return receiver, nil
}

// The accepted document must retain its expansion boundary after the transient
// candidate response is gone. This uses an authored boundary, never a fake call.
func retainInteractionFrontier(result *DiagramInteractionCandidate, in DiagramInteractionBuildInput, from string, g *EffectiveGraphSnapshot) {
	p := result.Document.Interactions
	id := diagramIdentity("interaction-frontier-v1", in.EntrypointID)
	boundary := InteractionStep{ID: id, Label: "Unexpanded interaction frontier", Kind: "boundary", From: from, BranchPath: []string{}, Refs: []DiagramRef{}, Origin: DiagramOrigin{Kind: "authored", Reason: "Static expansion stopped at a budget, merge, recursion or missing-proof boundary; omitted behavior remains unverified"}}
	if len(p.Steps) >= in.MaxSteps {
		removed := p.Steps[len(p.Steps)-1]
		p.Steps = p.Steps[:len(p.Steps)-1]
		boundary.Refs = slices.Clone(removed.Refs)
		p.Order = slices.DeleteFunc(p.Order, func(e InteractionOrder) bool { return e.From == removed.ID || e.To == removed.ID })
	}
	refs := map[string]DiagramRef{}
	for _, ref := range boundary.Refs {
		refs[ref.ID] = ref
	}
	subjects := map[string]bool{}
	for _, gap := range result.Gaps {
		subjects[gap.SubjectID] = true
	}
	for _, n := range g.State.Nodes {
		if subjects[n.ID] {
			refs[n.ID] = DiagramRef{Kind: "record", RecordType: "node", ID: n.ID}
		}
	}
	for _, e := range g.State.Edges {
		if subjects[e.ID] {
			refs[e.ID] = DiagramRef{Kind: "record", RecordType: "edge", ID: e.ID}
		}
	}
	boundary.Refs = []DiagramRef{}
	for _, id := range slices.Sorted(maps.Keys(refs)) {
		if len(boundary.Refs) == 100 {
			break
		}
		boundary.Refs = append(boundary.Refs, refs[id])
	}
	p.Steps = append(p.Steps, boundary)
}

func interactionBuildIndex(g *EffectiveGraphSnapshot, result *DiagramInteractionCandidate) (map[string]Node, map[string][]Edge, map[string][]DiagramEvidenceRef) {
	nodes := map[string]Node{}
	out := map[string][]Edge{}
	proofs := map[string][]DiagramEvidenceRef{}
	for _, n := range g.State.Nodes {
		nodes[n.ID] = n
		result.VisitedObjects++
	}
	for _, e := range g.State.Edges {
		out[e.From] = append(out[e.From], e)
		result.VisitedObjects++
	}
	for _, e := range g.State.Evidence {
		if e.Status != "explicit" || (e.Freshness != nil && e.Freshness.Status != "current") {
			result.VisitedObjects++
			continue
		}
		proofs[e.SubjectID] = append(proofs[e.SubjectID], DiagramEvidenceRef{RevisionID: g.Pins.BaseRevisionID, EvidenceID: e.ID, SubjectID: e.SubjectID})
		result.VisitedObjects++
	}
	for _, e := range g.BaselineEvidence {
		result.VisitedObjects++
		proofs[e.SubjectID] = append(proofs[e.SubjectID], DiagramEvidenceRef{RevisionID: e.RevisionID, EvidenceID: e.EvidenceID, SubjectID: e.SubjectID})
	}
	for id, rows := range proofs {
		proofs[id] = uniqueInteractionProofs(rows)
	}
	for id := range out {
		slices.SortFunc(out[id], func(a, b Edge) int {
			aMessage := a.Kind == "calls" || a.Kind == "emits"
			bMessage := b.Kind == "calls" || b.Kind == "emits"
			if aMessage != bMessage {
				if aMessage {
					return -1
				}
				return 1
			}
			return cmp.Compare(a.ID, b.ID)
		})
	}
	result.VisitedObjects += len(g.Origins)
	return nodes, out, proofs
}
func interactionOriginReader(g *EffectiveGraphSnapshot, proofs map[string][]DiagramEvidenceRef) func(string) DiagramOrigin {
	desired := map[string]bool{}
	for _, origin := range g.Origins {
		if origin.Kind == "intent" {
			desired[origin.SubjectID] = true
		}
	}
	return func(id string) DiagramOrigin {
		if desired[id] {
			return DiagramOrigin{Kind: "authored", Reason: "Desired structure requires an authored interaction declaration"}
		}
		if len(proofs[id]) > 0 && len(proofs[id]) <= 20 {
			return DiagramOrigin{Kind: "source_assertion", Evidence: slices.Clone(proofs[id])}
		}
		return DiagramOrigin{Kind: "authored", Reason: "Derived boundary; source proof unavailable"}
	}
}
func interactionParticipantReader(nodes map[string]Node, membership map[string][]string, participantIDs map[string]string, payload *InteractionPayload, result *DiagramInteractionCandidate, architecture *DiagramVersion, origin func(string) DiagramOrigin) func(string) string {
	return func(node string) string {
		n := nodes[node]
		seen := map[string]bool{}
		for (n.Kind == "flow" || n.Kind == "flow_step") && n.ParentID != nil && !seen[n.ID] {
			seen[n.ID] = true
			n = nodes[*n.ParentID]
		}
		owner := n.ID
		if owner == "" {
			owner = node
		}
		if id := participantIDs[owner]; id != "" {
			return id
		}
		id := diagramIdentity("interaction-participant-v1", owner)
		label := n.Name
		if label == "" {
			label = "Unknown source owner"
		}
		if len(label) > 256 {
			label = "Source owner " + owner
		}
		v := InteractionParticipant{ID: id, Label: label, Origin: origin(owner), Refs: []DiagramRef{{Kind: "record", RecordType: "node", ID: owner}}}
		if len(membership[owner]) == 1 {
			v.ArchitectureElementID = membership[owner][0]
		} else if architecture != nil {
			result.Gaps = append(result.Gaps, diagramGap(id, "unresolved_membership", "No unique explicit architecture membership"))
		}
		payload.Participants = append(payload.Participants, v)
		participantIDs[owner] = id
		return id
	}
}
func appendInteractionBranch(payload *InteractionPayload, in DiagramInteractionBuildInput, n Node, edge Edge, evidence DiagramOrigin, next *interactionFrontier) {
	id := diagramIdentity("interaction-branch-v1", in.EntrypointID, edge.ID)
	parent := ""
	if len(next.branches) > 0 {
		parent = next.branches[len(next.branches)-1]
	}
	label := runtimeAttributeString(edge.Attributes, "label")
	if label == "" {
		label = "Source alternative"
	}
	if len(label) > 256 {
		label = "Source alternative"
	}
	payload.Branches = append(payload.Branches, InteractionBranch{ID: id, ParentID: parent, GroupID: diagramIdentity("interaction-branch-group-v1", in.EntrypointID, n.ID), Label: label, Kind: "alternative", GuardText: runtimeAttributeString(edge.Attributes, "label"), Origin: evidence})
	next.branches = append(next.branches, id)

}
func appendInteractionMessage(ctx context.Context, g *EffectiveGraphSnapshot, in DiagramInteractionBuildInput, nodes map[string]Node, payload *InteractionPayload, result *DiagramInteractionCandidate, participant func(string) string, n Node, edge Edge, evidence DiagramOrigin, next *interactionFrontier) (string, bool, error) {
	target := nodes[edge.To]
	kind := "request"
	to := ""
	if edge.Kind == "emits" {
		kind = "send"
		receiver, err := interactionEventReceiver(ctx, g, edge, result)
		if err != nil {
			return "", false, err
		}
		if receiver != "" {
			to = participant(receiver)
		}
	}
	if edge.Kind != "emits" && target.ID != "" && target.Kind != "unresolved_target" {
		to = participant(target.ID)
	} else if kind != "send" {
		kind = "boundary"
	}
	id := diagramIdentity("interaction-step-v1", in.EntrypointID, edge.ID)
	label := target.Name
	if label == "" {
		label = "Unresolved dispatch"
	}
	if len(label) > 256 {
		label = "Source interaction " + edge.ID
	}
	step := InteractionStep{ID: id, Label: label, Kind: kind, From: participant(n.ID), To: to, BranchPath: slices.Clone(next.branches), Origin: evidence, Refs: []DiagramRef{{Kind: "record", RecordType: "edge", ID: edge.ID}}}
	payload.Steps = append(payload.Steps, step)
	if next.last != "" && len(next.control) > 0 {
		payload.Order = append(payload.Order, InteractionOrder{ID: diagramIdentity("interaction-order-v1", next.last, id), From: next.last, To: id, Origin: DiagramOrigin{Kind: "source_assertion", Evidence: next.control}})
	}
	next.last = id
	next.control = []DiagramEvidenceRef{}
	if to == "" {
		result.Gaps = append(result.Gaps, diagramGap(id, "unresolved_receiver", "Receiver is not established; no receive or reply was inferred"))
		return id, false, nil
	}

	return id, true, nil

}
func linkInteractionControl(result *DiagramInteractionCandidate, edge Edge, emitted []string, next *interactionFrontier) {
	if edge.Kind == "next" || edge.Kind == "branch" || edge.Kind == "error" || edge.Kind == "returns" {
		if len(emitted) == 1 {
			next.last = emitted[0]
			next.control = []DiagramEvidenceRef{}
		}
		if len(emitted) > 1 {
			next.last = ""
			next.control = nil
			result.Gaps = append(result.Gaps, diagramGap(edge.ID, "control_boundary", "Multiple calls at one source step have unknown relative order"))
		}
	}
}
func walkInteractions(ctx context.Context, g *EffectiveGraphSnapshot, in DiagramInteractionBuildInput, payload *InteractionPayload, result *DiagramInteractionCandidate, nodes map[string]Node, out map[string][]Edge, relevant *runtimeFlowProjection, origin func(string) DiagramOrigin, participant func(string) string, entry Node) error {
	incoming := interactionIncoming(nodes, out, relevant)
	queue := []interactionFrontier{{node: entry.ID, branches: []string{}, control: []DiagramEvidenceRef{}}}
	seen := map[string]bool{}
	cut := func(subject, code, message string) {
		result.Truncated = true
		result.Gaps = append(result.Gaps, diagramGap(subject, code, message))
	}
	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		w := queue[head]
		if result.VisitedObjects >= 250000 {
			cut(in.EntrypointID, "visited_frontier", "Visited-object budget exhausted; remaining scope uninspected")
			break
		}
		result.VisitedObjects++
		if seen[w.node] || incoming[w.node] > 1 {
			result.Gaps = append(result.Gaps, diagramGap(w.node, "control_boundary", "Recursive back edge or unsupported merge; expansion stopped"))
			continue
		}
		seen[w.node] = true
		n := nodes[w.node]
		emitted := []string{}
		for _, edge := range out[w.node] {
			if !relevant.relevant(n, edge) && (n.Kind != "flow_step" || edge.Kind != "emits") {
				continue
			}
			if result.VisitedObjects >= 250000 {
				cut(edge.ID, "visited_frontier", "Remaining outgoing edge was not inspected")
				break
			}
			result.VisitedObjects++
			if interactionStepBudgetReached(payload, in.MaxSteps) {
				cut(edge.ID, "step_frontier", "Unexpanded source edge at interaction limit")
				continue
			}
			next := interactionFrontier{node: edge.To, last: w.last, branches: slices.Clone(w.branches), control: slices.Clone(w.control)}
			linkInteractionControl(result, edge, emitted, &next)
			evidence := origin(edge.ID)
			if evidence.Kind != "source_assertion" {
				result.Gaps = append(result.Gaps, diagramGap(edge.ID, "missing_positive_witness", "Source edge lacks bounded exact proof; no causal claim inferred"))
				continue
			}
			if edge.Kind == "branch" {
				appendInteractionBranch(payload, in, n, edge, evidence, &next)
			}
			next.control = append(next.control, evidence.Evidence...)
			if len(next.control) > 20 {
				result.Gaps = append(result.Gaps, diagramGap(edge.ID, "control_boundary", "Control witness exceeds evidence bound"))
				next.control = nil
				next.last = ""
			}
			if edge.Kind == "calls" || edge.Kind == "emits" {
				id, follow, err := appendInteractionMessage(ctx, g, in, nodes, payload, result, participant, n, edge, evidence, &next)
				if err != nil {
					return err
				}
				emitted = append(emitted, id)
				if !follow {
					continue
				}
			}
			if !runtimeAccessKind(edge.Kind) {
				queue = append(queue, next)
			}
		}
	}

	return nil

}

func interactionHasBoundary(result *DiagramInteractionCandidate) bool {
	if result.Truncated {
		return true
	}
	return slices.ContainsFunc(result.Gaps, func(gap DiagramGap) bool {
		return gap.Code == "control_boundary" || gap.Code == "missing_positive_witness"
	})
}

// Stop before any shared traversal target, including shared callees. Assigning
// its body to the first queue arrival would invent alternative ownership.
func interactionIncoming(nodes map[string]Node, out map[string][]Edge, relevant *runtimeFlowProjection) map[string]int {
	incoming := map[string]int{}
	for _, edges := range out {
		for _, edge := range edges {
			if relevant.relevant(nodes[edge.From], edge) && !runtimeAccessKind(edge.Kind) {
				incoming[edge.To]++
			}
		}
	}
	return incoming
}

func interactionStepBudgetReached(p *InteractionPayload, maxSteps int) bool {
	return len(p.Steps) >= maxSteps || len(p.Steps)+len(p.Participants)+len(p.Branches) >= 996
}
func oversizedInteractionCandidate(g *EffectiveGraphSnapshot, in DiagramInteractionBuildInput) (*DiagramInteractionCandidate, error) {
	schema := g.Pins.StructuralSchemaVersion
	if schema == "" {
		schema = g.State.Revision.SchemaVersion
	}
	for i, node := range g.State.Nodes {
		if i == 250000 {
			break
		}
		if node.ID != in.EntrypointID {
			continue
		}
		if !interactionEntrypointSupported(schema, node.Kind) {
			return nil, diagramUnsupported()
		}
		owner := diagramIdentity("interaction-participant-v1", node.ID)
		id := diagramIdentity("interaction-frontier-v1", node.ID)
		origin := DiagramOrigin{Kind: "authored", Reason: "Input exceeds visited-object budget; scope is unexpanded and behavior unverified"}
		refs := []DiagramRef{{Kind: "record", RecordType: "node", ID: node.ID}}
		payload := &InteractionPayload{Architecture: in.Architecture, ScopeRefs: refs, Participants: []InteractionParticipant{{ID: owner, Label: "Unexpanded source owner", Origin: origin, Refs: refs}}, Steps: []InteractionStep{{ID: id, Label: "Visited-object frontier", Kind: "boundary", From: owner, BranchPath: []string{}, Origin: origin, Refs: refs}}, Order: []InteractionOrder{}, Branches: []InteractionBranch{}}
		doc, err := normalizeDiagram(DiagramDocument{Format: DiagramDocumentVersion, Kind: "interactions", Target: in.Target, Interactions: payload})
		if err != nil {
			return nil, err
		}
		return &DiagramInteractionCandidate{Document: doc, TargetHash: g.Pins.TargetHash, VisitedObjects: i + 1, Truncated: true, Gaps: []DiagramGap{diagramGap(id, "visited_frontier", "Input exceeds visited-object budget; no downstream expansion performed")}}, nil
	}
	return nil, invalid("entrypointId", "Entrypoint is outside the inspected object budget; select a bounded supported scope")
}

func interactionInputObjects(g *EffectiveGraphSnapshot) int {
	return len(g.State.Nodes) + len(g.State.Edges) + len(g.State.Evidence) + len(g.BaselineEvidence) + len(g.Origins)
}

func interactionEntrypointSupported(schema, kind string) bool {
	return (isRuntimeSchema(schema) || schema == ComposedSchemaVersion) && sourceRuntimeEntrypoint(schema, kind)
}

func uniqueInteractionProofs(rows []DiagramEvidenceRef) []DiagramEvidenceRef {
	seen := map[DiagramEvidenceRef]bool{}
	out := make([]DiagramEvidenceRef, 0, len(rows))
	for _, proof := range rows {
		if !seen[proof] {
			seen[proof] = true
			out = append(out, proof)
		}
	}
	return out
}

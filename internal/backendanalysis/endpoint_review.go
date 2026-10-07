package backendanalysis

import (
	"context"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type EndpointItemDetail struct {
	DocumentVersion string           `json:"documentVersion"`
	Type            string           `json:"type"`
	Category        string           `json:"category"`
	Side            string           `json:"side"`
	Object          ObjectAddress    `json:"object"`
	Witness         Witness          `json:"witness"`
	Proof           []ProofReference `json:"proof"`
	BehaviorStatus  string           `json:"behaviorStatus"`
}
type EndpointChangeDetail struct {
	DocumentVersion string     `json:"documentVersion"`
	Type            string     `json:"type"`
	Change          DiffChange `json:"change"`
}
type EndpointCheckDetail struct {
	DocumentVersion string     `json:"documentVersion"`
	Type            string     `json:"type"`
	Check           RuleResult `json:"check"`
	Required        bool       `json:"required"`
	Origin          string     `json:"origin"`
	CriterionKey    *string    `json:"criterionKey"`
}

func validateEndpointRoots(p *EndpointReviewPayload, before, after *backendmodel.EffectiveGraphSnapshot) error {
	for _, g := range []*backendmodel.EffectiveGraphSnapshot{before, after} {
		if g.Pins.StructuralSchemaVersion != "5" && g.Pins.StructuralSchemaVersion != "6" {
			return fault(422, "unsupported", "Endpoint review requires source5/source6")
		}
	}
	exists := func(g *backendmodel.EffectiveGraphSnapshot, id string) bool {
		return slices.ContainsFunc(g.State.Nodes, func(n backendmodel.Node) bool { return n.ID == id && n.Kind == "http_operation" })
	}
	if !exists(before, p.BeforeEndpointID) || p.AfterEndpointID != nil && !exists(after, *p.AfterEndpointID) {
		return malformed("Explicit endpoint ID must exist on its selected source side")
	}
	return nil
}
func endpointIndex(ctx context.Context, g *backendmodel.EffectiveGraphSnapshot) (*graphIndex, error) {
	// Reuse contextual mapping/proof transitions, replacing impact's reverse
	// caller propagation with declared forward endpoint execution structure.
	idx, err := indexGraph(ctx, g)
	if err != nil {
		return nil, err
	}
	for object, transitions := range idx.adj {
		idx.adj[object] = slices.DeleteFunc(transitions, func(t transition) bool { return t.kind != "mapping" })
	}
	add := func(from, to, kind, id string, proof backendmodel.EffectiveAnalysisProof, boundary string) {
		a, b := ObjectAddress{RecordType: "node", ID: from}, ObjectAddress{RecordType: "node", ID: to}
		idx.adj[a] = append(idx.adj[a], transition{from: a, to: b, kind: kind, id: id, proof: proof, boundary: boundary})
	}
	for _, edge := range g.State.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !slices.Contains([]string{"handles", "contains", "calls", "next", "branch", "error", "returns", "reads", "writes", "deletes", "emits", "delivered_to"}, edge.Kind) {
			continue
		}
		if edge.Kind == "contains" && idx.nodes[edge.From].Kind != "handler" && idx.nodes[edge.From].Kind != "flow" {
			continue
		}
		proof := recordProof(g, ObjectAddress{RecordType: "edge", ID: edge.ID})
		add(edge.From, edge.To, edge.Kind, edge.ID, proof, edgeBoundary(edge, idx.nodes))
		if edge.Kind == "delivered_to" {
			if message := attr(edge.Attributes, "messageId"); message != "" && message != edge.From {
				add(message, edge.To, edge.Kind, edge.ID, proof, edgeBoundary(edge, idx.nodes))
			}
		}
	}
	for _, node := range g.State.Nodes {
		if node.ParentID == nil || !slices.Contains([]string{"flow", "flow_step"}, node.Kind) {
			continue
		}
		parent := idx.nodes[*node.ParentID]
		if !slices.Contains([]string{"handler", "flow"}, parent.Kind) {
			continue
		}
		proof, err := backendmodel.EffectivePropertyAnalysisProof(g, backendmodel.ChangeRecordRef{RecordType: "node", ID: node.ID}, backendmodel.EffectivePropertySelector{Source: &backendmodel.TypedSourcePropertySelector{Kind: "parent"}})
		if err != nil {
			return nil, err
		}
		add(parent.ID, node.ID, "flow_contains", "parent/"+node.ID, proof, "")
	}
	for object, transitions := range idx.adj {
		slices.SortFunc(transitions, func(a, b transition) int {
			if n := strings.Compare(a.id, b.id); n != 0 {
				return n
			}
			return strings.Compare(a.to.ID, b.to.ID)
		})
		idx.adj[object] = transitions
	}
	return idx, nil
}
func (r *reportBuilder) addEndpointItem(idx *graphIndex, t transition, witness Witness, proof []ProofReference) {
	category := ""
	switch t.kind {
	case "writes", "deletes":
		category = "write"
	case "error", "branch":
		category = "error_branch"
	case "emits":
		category = "emitted_event"
	}
	if kind := idx.nodes[t.to.ID].Kind; kind == "consumer" || kind == "job" {
		category = "affected_consumer"
	}
	if category == "" {
		return
	}
	r.add("findings", t.to, idx.nodes[t.to.ID].Kind, witness.Status, len(witness.Steps), EndpointItemDetail{b43ResultVersion, "endpoint_item", category, witness.Side, t.to, witness, proof, "unverified"})
}
func analyzeEndpointReview(ctx context.Context, in *ImmutableInput, p *EndpointReviewPayload, before, after, intentBase, intent *backendmodel.EffectiveGraphSnapshot) (*TerminalSnapshot, error) {
	r := newReport(in)
	r.before, r.after = before, after
	r.endpoint = true
	r.endpointObjects = map[ObjectAddress]bool{}
	r.services = objectServices(before, after)
	indexes := map[string]*graphIndex{}
	var err error
	indexes["before"], err = endpointIndex(ctx, before)
	if err != nil {
		return nil, err
	}
	indexes["after"], err = endpointIndex(ctx, after)
	if err != nil {
		return nil, err
	}
	if p.AfterEndpointID == nil {
		r.gap("endpoint_removal_unverified", ObjectAddress{RecordType: "node", ID: p.BeforeEndpointID})
	}
	root := ObjectAddress{RecordType: "node", ID: p.BeforeEndpointID}
	r.endpointObjects[root] = true
	queue := []traversalState{{side: "before", seed: root, object: root, status: "confirmed"}}
	if p.AfterEndpointID != nil {
		root = ObjectAddress{RecordType: "node", ID: *p.AfterEndpointID}
		r.endpointObjects[root] = true
		queue = append(queue, traversalState{side: "after", seed: root, object: root, status: "confirmed"})
	}
	if err = r.traverseIndexes(ctx, indexes, queue); err != nil {
		return nil, err
	}
	changes, err := structuralChanges(ctx, before, after)
	if err != nil {
		return nil, err
	}
	for _, change := range changes {
		if !r.endpointObjects[change.Object] {
			continue
		}
		if !r.trackChange(change.Object) {
			continue
		}
		r.addEndpointChange(change, before, after)
	}
	if intent != nil && intentBase != nil {
		if err = r.addEndpointIntent(ctx, intentBase, intent, after); err != nil {
			return nil, err
		}
	}
	r.potential = true
	return r.finish(before, after, nil)
}

// addEndpointChange records one structural change on the endpoint's objects
// and the analysis rule each of its paths triggers.
func (r *reportBuilder) addEndpointChange(change DiffChange, before, after *backendmodel.EffectiveGraphSnapshot) {
	r.covered[change.Object] = true
	r.add("changes", change.Object, change.Kind, "confirmed", 0, EndpointChangeDetail{b43ResultVersion, "endpoint_change", change})
	// One rule per changed path, deduplicated by rule, exactly like
	// diff/impact's addChangeRule: a single ruleFor over the joined paths
	// took the first match, so a nativeType+nullable change hid the
	// not-null data check (review 2026-10-06, F140).
	paths := change.Paths
	if len(paths) == 0 {
		paths = []string{""}
	}
	seen := map[string]bool{}
	for _, path := range paths {
		part := change
		part.Paths = []string{path}
		check := ruleFor(part)
		if seen[check.RuleID] {
			continue
		}
		seen[check.RuleID] = true
		for _, side := range []string{"before", "after"} {
			g := before
			if side == "after" {
				g = after
			}
			if side == "before" && change.Operation == "added" || side == "after" && change.Operation == "removed" {
				continue
			}
			check.Evidence = append(check.Evidence, proofReference(side, g, recordProof(g, change.Object)))
		}
		// An unknown rule (e.g. native-type-change) makes the verdict
		// unknown, as recordRule does; it was left "potential"
		// (review 2026-10-06, F146).
		if check.Status == "unknown" {
			r.unknown = true
		}
		r.add("checks", change.Object, change.Kind, check.Certainty, 0, EndpointCheckDetail{b43ResultVersion, "endpoint_check", check, false, "analysis_rule", nil})
	}
}

func (r *reportBuilder) addEndpointIntent(ctx context.Context, intentBase, intent, after *backendmodel.EffectiveGraphSnapshot) error {

	mapping := map[string]string{}
	for _, n := range intentBase.State.Nodes {
		mapping[n.ID] = n.ID
	}
	for _, e := range intentBase.State.Edges {
		mapping[e.ID] = e.ID
	}
	addCreatedEdgeCorrespondence(intentBase, intent, after, mapping)
	outside, err := outsideIntentChanges(ctx, intentBase, intent, after, mapping)
	if err != nil {
		return err
	}
	// Endpoint review has no identity map, so an object the proposal creates
	// keeps its draft UUID and can never correspond to the implementation's
	// new UUID. An added source object of the same record type and kind as an
	// unmatched created draft object may be that creation: it is an unverified
	// correspondence (a gap), not an outside-intent change
	// (review 2026-10-06, F145).
	type createdKind struct{ recordType, kind string }
	unmatched := map[createdKind]bool{}
	for _, n := range intent.State.Nodes {
		if _, retained := mapping[n.ID]; !retained {
			unmatched[createdKind{"node", n.Kind}] = true
		}
	}
	for _, e := range intent.State.Edges {
		if mapping[e.ID] == "" {
			unmatched[createdKind{"edge", e.Kind}] = true
		}
	}
	for _, change := range outside {
		if !r.endpointObjects[change.Object] {
			continue
		}
		if change.Operation == "added" && unmatched[createdKind{change.Object.RecordType, change.Kind}] {
			r.gap("unverified_created_correspondence", change.Object)
			continue
		}
		r.add("changes", change.Object, change.Kind, "confirmed", 0, OutsideIntentChangeDetail{b43ResultVersion, "outside_intent_change", change})
	}
	for _, criterion := range intent.Criteria {
		relevant := criterion.ID != "" && r.endpointObjects[ObjectAddress{RecordType: criterion.RecordType, ID: criterion.ID}]
		if criterion.Kind == "edge_exists" {
			relevant = r.endpointObjects[ObjectAddress{RecordType: "edge", ID: criterion.ID}]
		}
		for _, id := range criterion.TargetIDs {
			relevant = relevant || r.endpointObjects[ObjectAddress{RecordType: "node", ID: id}]
		}
		// Only a criterion with neither an object nor targets is global. An
		// object/field/edge criterion carries an ID, not TargetIDs, so testing
		// TargetIDs alone added every unrelated criterion as a required
		// endpoint check (review 2026-10-06, F139).
		if !relevant && (criterion.ID != "" || len(criterion.TargetIDs) != 0) {
			continue
		}
		check := RuleResult{RuleID: "criterion/" + criterion.Kind, Version: "1", Prerequisites: []string{"exact_saved_intent_criterion"}, Object: ObjectAddress{RecordType: criterion.RecordType, ID: criterion.ID}, Status: "check_required", Severity: "review", Certainty: "unknown", Message: criterion.Description, Evidence: []ProofReference{}}
		r.add("checks", check.Object, "criterion", "unknown", 0, EndpointCheckDetail{b43ResultVersion, "endpoint_check", check, criterion.Required, "criterion", new(criterion.Key)})
	}
	return nil
}

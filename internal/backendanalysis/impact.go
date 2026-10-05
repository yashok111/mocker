package backendanalysis

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type DependencyStep struct {
	Artifact   *backendmodel.ArtifactProjectionLocator `json:"artifact,omitzero"`
	ObjectHash string                                  `json:"objectHash,omitempty"`
	Kind       string                                  `json:"kind"`
	ID         string                                  `json:"id"`
	From       ObjectAddress                           `json:"from"`
	To         ObjectAddress                           `json:"to"`
	Context    *backendmodel.LineageValueRef           `json:"context,omitzero"`
	Evidence   []ProofReference                        `json:"evidence"`
}
type Witness struct {
	Side     string           `json:"side"`
	Seed     ObjectAddress    `json:"seed"`
	Affected ObjectAddress    `json:"affected"`
	Steps    []DependencyStep `json:"steps"`
	Status   string           `json:"status"`
}
type transition struct {
	from, to            ObjectAddress
	kind, id            string
	source, destination *backendmodel.LineageValueRef
	proof               backendmodel.EffectiveAnalysisProof
	boundary            string
}
type traversalState struct {
	side, kind   string
	seed, object ObjectAddress
	value        backendmodel.LineageValueRef
	steps        []DependencyStep
	status       string
}
type traversalKey struct {
	side, kind   string
	seed, object ObjectAddress
	value        backendmodel.LineageValueRef
}
type graphIndex struct {
	graph *backendmodel.EffectiveGraphSnapshot
	nodes map[string]backendmodel.Node
	adj   map[ObjectAddress][]transition
}

func indexGraph(ctx context.Context, g *backendmodel.EffectiveGraphSnapshot) (*graphIndex, error) {
	idx := &graphIndex{graph: g, nodes: map[string]backendmodel.Node{}, adj: map[ObjectAddress][]transition{}}
	for _, n := range g.State.Nodes {
		idx.nodes[n.ID] = n
	}
	for _, e := range g.State.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		idx.addEdge(e)
	}

	for _, n := range g.State.Nodes {
		idx.addRelational(ObjectAddress{RecordType: "node", ID: n.ID}, n.Kind, n.Attributes)
		if n.Kind == "flow_step" && n.ParentID != nil && idx.nodes[*n.ParentID].Kind == "flow" {
			p, err := backendmodel.EffectivePropertyAnalysisProof(g, backendmodel.ChangeRecordRef{RecordType: "node", ID: n.ID}, backendmodel.EffectivePropertySelector{Source: &backendmodel.TypedSourcePropertySelector{Kind: "parent"}})
			if err != nil {
				return nil, err
			}
			idx.adj[ObjectAddress{RecordType: "node", ID: n.ID}] = append(idx.adj[ObjectAddress{RecordType: "node", ID: n.ID}], transition{from: ObjectAddress{RecordType: "node", ID: n.ID}, to: ObjectAddress{RecordType: "node", ID: *n.ParentID}, kind: "flow_owner", id: "parent/" + n.ID, proof: p})
		}
		if n.Kind == "field_mapping" {
			if err := idx.addMapping(n); err != nil {
				return nil, err
			}
		}
	}
	for key, edges := range idx.adj {
		slices.SortFunc(edges, func(a, b transition) int {
			ak, _ := requestHash(struct {
				ID    string
				To    ObjectAddress
				Value *backendmodel.LineageValueRef
			}{a.id, a.to, a.destination})
			bk, _ := requestHash(struct {
				ID    string
				To    ObjectAddress
				Value *backendmodel.LineageValueRef
			}{b.id, b.to, b.destination})
			if order := strings.Compare(a.id, b.id); order != 0 {
				return order
			}
			return strings.Compare(ak, bk)
		})
		idx.adj[key] = edges
	}
	return idx, nil
}
func attr(attrs map[string]jsontext.Value, key string) string { return stringValue(attrs[key]) }
func (idx *graphIndex) addMapping(n backendmodel.Node) error {
	raw, err := canonical(n.Attributes)
	if err != nil {
		return err
	}
	var mapping backendmodel.LineageMappingAttributes
	if err = json.Unmarshal(raw, &mapping); err != nil {
		return err
	}
	p := recordProof(idx.graph, ObjectAddress{RecordType: "node", ID: n.ID})
	boundary := ""
	if mapping.Transform.Kind == "unknown_transform" || mapping.AnalysisStatus != "complete" {
		boundary = "unknown_transform"
	}
	// The mapping is a hyperedge: every input's selected value proof applies
	// even when only one input (or the mapping definition itself) changed.
	for _, ref := range append(slices.Clone(mapping.Sources), mapping.Destination) {
		value, err := backendmodel.EffectiveValueAnalysisProof(idx.graph, ref)
		if err != nil {
			value = backendmodel.EffectiveAnalysisProof{Status: "unresolved", Boundary: true, Reasons: []string{err.Error()}}
		}
		p = combineProof(p, value)
	}
	own := ObjectAddress{RecordType: "node", ID: n.ID}
	idx.adj[own] = append(idx.adj[own], transition{from: own, to: ObjectAddress{RecordType: "node", ID: mapping.Destination.NodeID}, kind: "mapping", id: n.ID, destination: new(mapping.Destination), proof: p, boundary: boundary})
	for _, source := range mapping.Sources {
		from := ObjectAddress{RecordType: "node", ID: source.NodeID}
		to := ObjectAddress{RecordType: "node", ID: mapping.Destination.NodeID}
		idx.adj[from] = append(idx.adj[from], transition{from: from, to: to, kind: "mapping", id: n.ID, source: new(source), destination: new(mapping.Destination), proof: p, boundary: boundary})
	}
	return nil
}
func selectedProof(idx *graphIndex, state traversalState, t transition) backendmodel.EffectiveAnalysisProof {
	p := t.proof
	if t.source != nil {
		value, err := backendmodel.EffectiveValueAnalysisProof(idx.graph, *t.source)
		if err != nil {
			value = backendmodel.EffectiveAnalysisProof{Status: "unresolved", Boundary: true, Reasons: []string{err.Error()}}
		}
		p = combineProof(p, value)
	}
	if t.destination != nil {
		value, err := backendmodel.EffectiveValueAnalysisProof(idx.graph, *t.destination)
		if err != nil {
			value = backendmodel.EffectiveAnalysisProof{Status: "unresolved", Boundary: true, Reasons: []string{err.Error()}}
		}
		p = combineProof(p, value)
	}
	if t.source == nil {
		if state.value.Kind != "" && state.value.NodeID == state.object.ID {
			value, err := backendmodel.EffectiveValueAnalysisProof(idx.graph, state.value)
			if err != nil {
				value = backendmodel.EffectiveAnalysisProof{Status: "unresolved", Boundary: true, Reasons: []string{err.Error()}}
			}
			p = combineProof(p, value)
		} else {
			p = combineProof(p, recordProof(idx.graph, state.object))
		}
	}
	if t.destination == nil {
		p = combineProof(p, recordProof(idx.graph, t.to))
	}

	return p
}
func combineProof(a, b backendmodel.EffectiveAnalysisProof) backendmodel.EffectiveAnalysisProof {
	a.Boundary = a.Boundary || b.Boundary
	a.Reasons = append(slices.Clone(a.Reasons), b.Reasons...)
	a.EvidenceIDs = append(slices.Clone(a.EvidenceIDs), b.EvidenceIDs...)
	a.Assertions = append(slices.Clone(a.Assertions), b.Assertions...)
	if proofRank(b.Status) > proofRank(a.Status) || proofRank(b.Status) == proofRank(a.Status) && b.Status > a.Status {
		a.Status = b.Status
	}
	return a
}
func supported(p backendmodel.EffectiveAnalysisProof) bool {
	return !p.Boundary && (p.Status == "explicit" || p.Status == "desired")
}
func (r *reportBuilder) traverse(ctx context.Context, before, after *backendmodel.EffectiveGraphSnapshot, seeds []ObjectAddress) error {
	indexes := map[string]*graphIndex{}
	for _, side := range []string{"before", "after"} {
		g := before
		if side == "after" {
			g = after
		}
		idx, err := indexGraph(ctx, g)
		if err != nil {
			return err
		}
		idx.direction(r.input.Scope.Direction)
		indexes[side] = idx
	}
	queue := initialQueue(indexes, seeds)
	return r.traverseIndexes(ctx, indexes, queue)
}
func (r *reportBuilder) traverseIndexes(ctx context.Context, indexes map[string]*graphIndex, queue []traversalState) error {
	visited := map[traversalKey]bool{}
	scheduled := map[traversalKey]bool{}
	for _, s := range queue {
		scheduled[traversalKey{s.side, s.kind, s.seed, s.object, s.value}] = true
	}
	witnesses := map[ObjectAddress]int{}
	emitted := map[witnessKey]bool{}
	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.progress.Records >= r.input.Limits.Records {
			r.truncate("record_limit")
			break
		}
		s := queue[head]
		key := traversalKey{side: s.side, kind: s.kind, seed: s.seed, object: s.object, value: s.value}
		if visited[key] {
			continue
		}
		if r.progress.States >= r.input.Limits.States {
			r.truncate("state_limit")
			break
		}
		visited[key] = true
		r.progress.States++
		idx := indexes[s.side]
		for _, t := range idx.adj[s.object] {
			if t.source != nil && s.value.Kind != "" && s.value != *t.source {
				continue
			}
			if r.progress.DependencyVisits >= r.input.Limits.DependencyVisits {
				r.truncate("dependency_visit_limit")
				return nil
			}
			r.progress.DependencyVisits++
			if len(s.steps) >= min(r.input.Limits.Depth, r.input.Scope.Depth) {
				r.truncate("depth_limit")
				continue
			}
			next, ok := r.followTransition(idx, s, t, witnesses, emitted)
			if ok {
				nk := traversalKey{next.side, next.kind, next.seed, next.object, next.value}
				if scheduled[nk] {
					continue
				}
				if len(scheduled) >= r.input.Limits.States {
					r.truncate("state_limit")
					continue
				}
				scheduled[nk] = true
				queue = append(queue, next)
			}
		}
	}
	return nil
}
func sortedAddresses(set map[ObjectAddress]bool) []ObjectAddress {
	out := slices.Collect(maps.Keys(set))
	slices.SortFunc(out, func(a, b ObjectAddress) int {
		if n := strings.Compare(a.RecordType, b.RecordType); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// Both graph sides share one owner request/cache/budget. Exact pin failure is a
// visible boundary; no latest-owner lookup or independent second cache is used.
func (r *reportBuilder) artifactChanges(ctx context.Context, before, after *backendmodel.EffectiveGraphSnapshot, request *backendmodel.EditorArtifactRequest) error {
	if len(before.Pins.ArtifactPins)+len(after.Pins.ArtifactPins) == 0 {
		return nil
	}
	if request == nil {
		r.gap("artifact_reader_missing", ObjectAddress{RecordType: "artifact", ID: "owners"})
		return nil
	}
	left, err := r.projectArtifacts(ctx, before, request)
	if err != nil {
		return err
	}
	right, err := r.projectArtifacts(ctx, after, request)
	if err != nil {
		return err
	}
	sides := map[string]map[string]artifactObject{"before": left, "after": right}
	if r.input.Kind == "impact" {
		r.artifactAssociations("before", before, left)
		r.artifactAssociations("after", after, right)
	}

	keys := map[string]bool{}
	for key := range sides["before"] {
		keys[key] = true
	}
	for key := range sides["after"] {
		keys[key] = true
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		a, aok := sides["before"][key]
		b, bok := sides["after"][key]
		if aok && bok && a.item.ObjectHash == b.item.ObjectHash && a.pin == b.pin {
			continue
		}
		address := ObjectAddress{RecordType: "artifact_object", ID: key}
		if !r.trackChange(address) {
			continue
		}
		if !r.selectedArtifact(address, a, b, before, after) {
			r.gap("scope_omitted_change", address)
			continue
		}
		r.covered[address] = true
		r.potential = true
		detail := struct {
			Before    *backendmodel.ArtifactProjectionItem `json:"before,omitzero"`
			After     *backendmodel.ArtifactProjectionItem `json:"after,omitzero"`
			BeforePin *backendmodel.ArtifactPin            `json:"beforePin,omitzero"`
			AfterPin  *backendmodel.ArtifactPin            `json:"afterPin,omitzero"`
		}{}
		if aok {
			detail.Before = &a.item
			detail.BeforePin = &a.pin
		}
		if bok {
			detail.After = &b.item
			detail.AfterPin = &b.pin
		}
		r.add("changes", address, "artifact_object", "confirmed", 0, detail)
	}
	return nil
}

func (idx *graphIndex) addEdge(e backendmodel.Edge) {
	idx.addRelational(ObjectAddress{RecordType: "edge", ID: e.ID}, e.Kind, e.Attributes)
	g := idx.graph
	add := func(from, to, kind, id string, p backendmodel.EffectiveAnalysisProof, boundary string) {
		a := ObjectAddress{RecordType: "node", ID: from}
		idx.adj[a] = append(idx.adj[a], transition{from: a, to: ObjectAddress{RecordType: "node", ID: to}, kind: kind, id: id, proof: p, boundary: boundary})
	}
	p := recordProof(g, ObjectAddress{RecordType: "edge", ID: e.ID})
	switch e.Kind {
	case "reads", "writes", "deletes", "calls":
		add(e.To, e.From, e.Kind, e.ID, p, "")
	case "handles":
		boundary := ""
		if (idx.nodes[e.From].Kind == "consumer" || idx.nodes[e.From].Kind == "job") && attr(idx.nodes[e.From].Attributes, "dispatchStatus") != "complete" {
			boundary = "unknown_dispatch"
		}
		add(e.To, e.From, e.Kind, e.ID, p, boundary)
		add(e.From, e.To, e.Kind, e.ID, p, boundary)
	case "contains":
		if idx.nodes[e.From].Kind == "handler" && idx.nodes[e.To].Kind == "flow" {
			add(e.To, e.From, "flow_owner", e.ID, p, "")
		}
	case "next", "branch", "error", "returns":
		add(e.From, e.To, e.Kind, e.ID, p, "")
	case "emits":
		boundary := ""
		if attr(e.Attributes, "deliveryStatus") != "declared" {
			boundary = "unknown_delivery"
		}
		add(e.From, e.To, e.Kind, e.ID, p, boundary)
	case "delivered_to":
		boundary := ""
		if attr(e.Attributes, "deliveryStatus") != "declared" {
			boundary = "unknown_delivery"
		}
		add(e.From, e.To, e.Kind, e.ID, p, boundary)
		if message := attr(e.Attributes, "messageId"); message != "" {
			add(message, e.To, e.Kind, e.ID, p, boundary)
		}
	}
	// A removed route/access is itself a changed seed, retaining its old endpoints.
	if e.Kind != "contains" && e.Kind != "derived_from" {
		key := ObjectAddress{RecordType: "edge", ID: e.ID}
		for _, node := range []string{e.From, e.To} {
			idx.adj[key] = append(idx.adj[key], transition{from: key, to: ObjectAddress{RecordType: "node", ID: node}, kind: e.Kind, id: e.ID, proof: p, boundary: edgeBoundary(e, idx.nodes)})
		}
	}
}

type artifactObject struct {
	item backendmodel.ArtifactProjectionItem
	pin  backendmodel.ArtifactPin
}

func (r *reportBuilder) projectArtifacts(ctx context.Context, g *backendmodel.EffectiveGraphSnapshot, request *backendmodel.EditorArtifactRequest) (map[string]artifactObject, error) {
	out := map[string]artifactObject{}
	for _, pin := range g.Pins.ArtifactPins {
		for _, query := range artifactQueries(g, pin) {
			for {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				page, err := request.ProjectEffective(g, query)
				if err != nil {
					r.gap("artifact_resolution", ObjectAddress{RecordType: "artifact", ID: pin.Kind + "/" + pin.ID})
					break
				}
				if !page.Complete {
					r.gap("artifact_projection_incomplete", ObjectAddress{RecordType: "artifact", ID: pin.Kind + "/" + pin.ID})
				}
				for _, item := range page.Items {
					if len(out) >= r.input.Limits.Records {
						r.truncate("artifact_record_limit")
						break
					}
					key := artifactObjectKey(pin, query.View, item)
					out[key] = artifactObject{item, pin}
				}
				if page.NextCursor == "" || len(out) >= r.input.Limits.Records {
					break
				}
				query.Cursor = page.NextCursor
			}
		}
	}
	return out, nil
}

type witnessKey struct {
	side, status string
	seed, object ObjectAddress
	value        backendmodel.LineageValueRef
}

func (r *reportBuilder) followTransition(idx *graphIndex, s traversalState, t transition, witnesses map[ObjectAddress]int, emitted map[witnessKey]bool) (traversalState, bool) {
	p := selectedProof(idx, s, t)
	if r.endpoint {
		r.endpointObjects[t.from] = true
		r.endpointObjects[t.to] = true
		r.endpointObjects[ObjectAddress{RecordType: "edge", ID: t.id}] = true
	}
	status := s.status
	if p.Status == "desired" {
		status = "desired"
	}
	if !supported(p) || t.boundary != "" {
		status = "unknown"
	}
	value := s.value
	if t.destination != nil {
		value = *t.destination
	}
	step := DependencyStep{Kind: t.kind, ID: t.id, From: t.from, To: t.to, Evidence: []ProofReference{proofReference(s.side, idx.graph, p)}}
	if value.Kind != "" {
		step.Context = new(value)
	}
	path := append(slices.Clone(s.steps), step)
	cyclic := cyclicWitness(s, t.to, value)
	if cyclic && !r.endpoint {
		return traversalState{}, false
	}
	witness := Witness{Side: s.side, Seed: s.seed, Affected: t.to, Steps: path, Status: status}
	if status != "unknown" {
		key := witnessPathKey(s.side, t.to)
		if old, ok := r.paths[key]; !ok || len(path) < len(old.Steps) {
			r.paths[key] = witness
		}
	}
	wk := witnessKey{s.side, status, s.seed, t.to, value}
	if r.endpoint {
		wk.value = backendmodel.LineageValueRef{Kind: "endpoint_transition", NodeID: t.id}
	}
	if emitted[wk] {
		return traversalState{}, false
	}
	emitted[wk] = true
	if witnesses[t.to] >= r.input.Limits.WitnessesPerObject {
		r.truncate("witness_limit")
	} else {
		witnesses[t.to]++
		if r.endpoint {
			r.addEndpointItem(idx, t, witness, step.Evidence)
		} else {
			r.add("witnesses", t.to, idx.nodes[t.to.ID].Kind, status, len(path), witness)
		}
	}
	if status == "unknown" {
		reason := t.boundary
		if reason == "" {
			reason = "unsupported_dependency_proof"
		}
		r.gap(reason, t.to)
		return traversalState{}, false
	}
	if cyclic {
		return traversalState{}, false
	}
	if !r.endpoint {
		finding := RuleResult{RuleID: "declared-dependency", Version: "1", Object: t.to, Prerequisites: []string{"declared_typed_transition", "current_or_desired_proof"}, Status: "potential", Severity: "review", Certainty: status, Message: "Declared consumer may be affected", Evidence: step.Evidence}
		r.add("findings", t.to, idx.nodes[t.to.ID].Kind, status, len(path), finding)
		r.potential = true
	}
	return traversalState{side: s.side, kind: t.kind, seed: s.seed, object: t.to, value: value, steps: path, status: status}, true
}

// Projection row IDs bind an entire revision/page scope; they cannot identify
// the same authored object across revisions. Use exact typed owner identity.
func artifactObjectKey(pin backendmodel.ArtifactPin, view string, item backendmodel.ArtifactProjectionItem) string {
	owner := item.Locator.Owner
	var identity any
	if item.BindingSelector != nil {
		identity = item.BindingSelector
	} else {
		raw, _ := canonical(owner)
		var fields map[string]jsontext.Value
		_ = json.Unmarshal(raw, &fields)
		if len(fields) > 1 {
			delete(fields, "pointer")
		}
		identity = fields
	}
	key, _ := requestHash(struct {
		Kind     string
		Identity any
		Embedded string
	}{item.Kind, identity, func() string {
		if item.Locator.Embedded != nil {
			return item.Locator.Embedded.ContractID
		}
		return ""
	}()})
	return pin.Kind + "/" + pin.ID + "/" + view + "/" + key
}

func (idx *graphIndex) direction(direction string) {
	if direction != "upstream" && direction != "both" {
		return
	}
	reverse := map[ObjectAddress][]transition{}
	for _, edges := range idx.adj {
		for _, edge := range edges {
			next := edge
			next.from, next.to = edge.to, edge.from
			next.source, next.destination = edge.destination, edge.source
			next.kind = "upstream/" + edge.kind
			reverse[next.from] = append(reverse[next.from], next)
		}
	}
	if direction == "upstream" {
		idx.adj = reverse
	} else {
		for key, edges := range reverse {
			idx.adj[key] = append(idx.adj[key], edges...)
		}
	}
	for key, edges := range idx.adj {
		slices.SortFunc(edges, func(a, b transition) int {
			if n := strings.Compare(a.id, b.id); n != 0 {
				return n
			}
			if n := strings.Compare(a.kind, b.kind); n != 0 {
				return n
			}
			return strings.Compare(a.to.ID, b.to.ID)
		})
		idx.adj[key] = edges
	}
}

func edgeBoundary(e backendmodel.Edge, nodes map[string]backendmodel.Node) string {
	if (e.Kind == "emits" || e.Kind == "delivered_to") && attr(e.Attributes, "deliveryStatus") != "declared" {
		return "unknown_delivery"
	}
	if e.Kind == "handles" && (nodes[e.From].Kind == "consumer" || nodes[e.From].Kind == "job") && attr(nodes[e.From].Attributes, "dispatchStatus") != "complete" {
		return "unknown_dispatch"
	}
	return ""
}

func initialQueue(indexes map[string]*graphIndex, seeds []ObjectAddress) []traversalState {
	queue := []traversalState{}
	for _, seed := range seeds {
		for _, side := range []string{"before", "after"} {
			idx := indexes[side]
			if _, exists := idx.nodes[seed.ID]; seed.RecordType == "node" && !exists {
				continue
			}
			queue = append(queue, traversalState{side: side, seed: seed, object: seed, status: "confirmed"})
		}
	}
	return queue
}

func witnessPathKey(side string, object ObjectAddress) string {
	return side + "\x00" + object.RecordType + "\x00" + object.ID
}
func (r *reportBuilder) artifactAssociations(side string, g *backendmodel.EffectiveGraphSnapshot, objects map[string]artifactObject) {
	for _, key := range slices.Sorted(maps.Keys(objects)) {
		object := objects[key]
		address := ObjectAddress{RecordType: "artifact_object", ID: key}
		count := 0
		for _, id := range object.item.SourceNodeIDs {
			source := ObjectAddress{RecordType: "node", ID: id}
			path, ok := r.paths[witnessPathKey(side, source)]
			if !ok && !r.seeds[source] {
				continue
			}
			if !ok {
				path = Witness{Side: side, Seed: source, Affected: source, Status: "confirmed"}
			}
			if count >= r.input.Limits.WitnessesPerObject {
				r.truncate("witness_limit")
				break
			}
			count++
			p := recordProof(g, source)
			status := path.Status
			if !supported(p) {
				status = "unknown"
				r.gap("artifact_association_proof", address)
			}
			step := DependencyStep{Kind: "artifact_association", ID: object.item.ID, From: source, To: address, Artifact: &object.item.Locator, ObjectHash: object.item.ObjectHash, Evidence: []ProofReference{proofReference(side, g, p)}}
			path.Steps = append(slices.Clone(path.Steps), step)
			path.Affected = address
			path.Status = status
			r.add("witnesses", address, object.item.Kind, status, len(path.Steps), path)
			r.add("findings", address, object.item.Kind, "possible", len(path.Steps), RuleResult{RuleID: "exact-artifact-association", Version: "1", Object: address, Status: "potential", Certainty: "possible", Severity: "review", Message: "Review the exact associated authored object", Prerequisites: []string{"exact_owner_pin", "frozen_effective_artifact_context"}, Evidence: step.Evidence})
			r.potential = true
		}
	}
}

func (idx *graphIndex) addRelational(object ObjectAddress, kind string, attrs map[string]jsontext.Value) {
	if !slices.Contains([]string{"constraint", "index", "references", "view", "symbol"}, kind) {
		return
	}
	if kind == "symbol" {
		var nested map[string]jsontext.Value
		if json.Unmarshal(attrs["databaseRoutine"], &nested) != nil {
			return
		}
		attrs = nested
	}
	var facets map[string]jsontext.Value
	if json.Unmarshal(attrs["facets"], &facets) != nil {
		return
	}
	for _, key := range slices.Sorted(maps.Keys(facets)) {
		var facet struct {
			Columns     []string `json:"columnIds"`
			ColumnPairs []struct {
				From string `json:"fromColumnId"`
				To   string `json:"toColumnId"`
			} `json:"columnPairs"`
			Dependencies       []string `json:"dependencyIds"`
			DependenciesStatus string   `json:"dependenciesStatus"`
		}
		if json.Unmarshal(facets[key], &facet) != nil {
			continue
		}
		proof, err := backendmodel.EffectivePropertyAnalysisProof(idx.graph, backendmodel.ChangeRecordRef{RecordType: object.RecordType, ID: object.ID}, backendmodel.EffectivePropertySelector{Source: &backendmodel.TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: key}})
		if err != nil {
			proof = backendmodel.EffectiveAnalysisProof{Status: "unresolved", Boundary: true}
		}
		ids := slices.Clone(facet.Columns)
		for _, pair := range facet.ColumnPairs {
			ids = append(ids, pair.From, pair.To)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		for _, id := range ids {
			if id == "" {
				continue
			}
			to := ObjectAddress{RecordType: "node", ID: id}
			idx.adj[object] = append(idx.adj[object], transition{from: object, to: to, kind: "relational_columns", id: object.ID + "/" + key, proof: proof, destination: &backendmodel.LineageValueRef{Kind: "column", NodeID: id, FacetKey: key}})
		}
		for _, id := range facet.Dependencies {
			from := ObjectAddress{RecordType: "node", ID: id}
			boundary := ""
			if facet.DependenciesStatus != "complete" {
				boundary = "incomplete_relational_dependencies"
			}
			idx.adj[from] = append(idx.adj[from], transition{from: from, to: object, kind: "relational_dependency", id: object.ID + "/" + key, proof: proof, boundary: boundary})
		}
	}
}

func cyclicWitness(s traversalState, to ObjectAddress, value backendmodel.LineageValueRef) bool {
	if to == s.seed && value.Kind == "" {
		return true
	}
	for _, old := range s.steps {
		if old.To == to && (old.Context == nil && value.Kind == "" || old.Context != nil && *old.Context == value) {
			return true
		}
	}
	return false
}

func (r *reportBuilder) selectedArtifact(address ObjectAddress, a, b artifactObject, before, after *backendmodel.EffectiveGraphSnapshot) bool {
	if !scopeObjectSelected(r.input.Scope, address, "artifact_object") {
		return false
	}
	service := r.input.Scope.Service
	if service == "" {
		return true
	}
	for _, id := range a.item.SourceNodeIDs {
		if inService(before, id, service) {
			return true
		}
	}
	for _, id := range b.item.SourceNodeIDs {
		if inService(after, id, service) {
			return true
		}
	}
	return false
}

// Desired authorship never upgrades missing source support. The join is
// order-independent, including source inputs preceding a desired destination.
func proofRank(status string) int {
	switch status {
	case "explicit":
		return 0
	case "desired":
		return 1
	case "inferred":
		return 2
	default:
		return 3
	}
}

// A scenario's state/rule views require an exact embedded owner selector.
// Bound nested objects cannot be covered by its top-level sequence/event views.
func artifactQueries(g *backendmodel.EffectiveGraphSnapshot, pin backendmodel.ArtifactPin) []backendmodel.ArtifactQueryInput {
	base := backendmodel.ArtifactQueryInput{RevisionID: g.Pins.BaseRevisionID, Artifact: backendmodel.ArtifactKey{Kind: pin.Kind, ID: pin.ID}, Limit: 100}
	views := []string{"states", "response_rules"}
	if pin.Kind == "design_scenario" {
		views = []string{"sequence", "event_model"}
	}
	queries := make([]backendmodel.ArtifactQueryInput, 0, 2)
	for _, view := range views {
		q := base
		q.View = view
		queries = append(queries, q)
	}
	if pin.Kind != "design_scenario" || g.Pins.ArtifactContext == nil {
		return queries
	}
	embedded := map[string]bool{}
	for _, binding := range g.Pins.ArtifactContext.EditorBindings {
		if binding.ArtifactKind == pin.Kind && binding.ArtifactID == pin.ID && binding.Selector.EmbeddedContractID != "" {
			embedded[binding.Selector.EmbeddedContractID] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(embedded)) {
		for _, view := range []string{"states", "response_rules"} {
			q := base
			q.View = view
			q.EmbeddedContractID = id
			queries = append(queries, q)
		}
	}
	return queries
}

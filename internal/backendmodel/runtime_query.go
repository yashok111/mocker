package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	runtimeTraversalPolicy  = "runtime-flow-reachability-v1"
	runtimeMaxCallHops      = 32
	runtimeMaxStates        = 5000
	runtimeMaxExaminedEdges = 20000
	runtimeMaxAccessPairs   = 5000
	runtimeMaxWitnessEdges  = 256
)

// FlowQueryInput selects one immutable source-flow projection.
type FlowQueryInput struct {
	RevisionID   string `json:"revisionId"`
	View         string `json:"view"`
	Search       string `json:"search,omitempty"`
	FlowID       string `json:"flowId,omitempty"`
	EntrypointID string `json:"entrypointId,omitempty"`
	DataNodeID   string `json:"dataNodeId,omitempty"`
	AccessKind   string `json:"accessKind,omitempty"`
	Limit        int    `json:"limit,omitzero"`
	Cursor       string `json:"cursor,omitempty"`
	present      map[string]bool
}

type FlowPage struct {
	ProjectID         string               `json:"projectId"`
	RevisionID        string               `json:"revisionId"`
	SemanticHash      string               `json:"semanticHash"`
	View              string               `json:"view"`
	Coverage          RevisionCoverage     `json:"coverage"`
	Limitations       []string             `json:"limitations"`
	Truncated         bool                 `json:"truncated"`
	TruncationReasons []string             `json:"truncationReasons"`
	NextCursor        string               `json:"nextCursor"`
	EntryPointItems   []FlowEntrypointItem `json:"entrypointItems,omitzero"`
	StepItems         []Node               `json:"stepItems,omitzero"`
	TransitionItems   []Edge               `json:"transitionItems,omitzero"`
	AccessItems       []FlowAccessItem     `json:"accessItems,omitzero"`
}

type FlowEntrypointItem struct {
	Operation         Node     `json:"operation"`
	HandlerIDs        []string `json:"handlerIds"`
	FlowIDs           []string `json:"flowIds"`
	UnresolvedHandles []Edge   `json:"unresolvedHandles"`
	EvidenceIDs       []string `json:"evidenceIds"`
	Limitations       []string `json:"limitations"`
}

type FlowAccessItem struct {
	AccessEdgeID string   `json:"accessEdgeId"`
	QueryID      string   `json:"queryId"`
	TargetID     string   `json:"targetId"`
	DatastoreID  string   `json:"datastoreId"`
	FacetKey     string   `json:"facetKey"`
	AccessKind   string   `json:"accessKind"`
	AccessMode   string   `json:"accessMode"`
	Relation     string   `json:"relation"`
	EntrypointID *string  `json:"entrypointId"`
	FlowID       *string  `json:"flowId"`
	PathNodeIDs  []string `json:"pathNodeIds"`
	PathEdgeIDs  []string `json:"pathEdgeIds"`
	Status       string   `json:"status"`
	EvidenceIDs  []string `json:"evidenceIds"`
	Limitations  []string `json:"limitations"`
}

func (r *Repo) QueryFlow(ctx context.Context, projectID string, in FlowQueryInput) (*FlowPage, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	state, err := loadRevisionState(ctx, r.db.R, projectID, in.RevisionID)
	if err != nil {
		return nil, err
	}
	page, err := projectRuntimeFlow(ctx, state, in)
	if err != nil {
		return nil, err
	}
	coverage, err := r.RevisionCoverage(ctx, projectID, in.RevisionID)
	if err != nil {
		return nil, err
	}
	page.Coverage = *coverage
	return page, nil
}

func (in *FlowQueryInput) UnmarshalJSON(b []byte) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("body", "Expected a strict flow query object")
	}
	if err := relationalFields(m, []string{"revisionId", "view"}, []string{"search", "flowId", "entrypointId", "dataNodeId", "accessKind", "limit", "cursor"}); err != nil {
		return invalid("body", err.Error())
	}
	for key, raw := range m {
		if key == "limit" {
			var limit int
			if json.Unmarshal(raw, &limit) != nil || limit < 1 || limit > MaxGraphPageSize {
				return invalid(key, "limit must be between 1 and 500")
			}
		} else if len(raw) == 0 || raw[0] != '"' {
			return invalid(key, "Expected a string")
		}
	}
	type input FlowQueryInput
	var decoded input
	if err := json.Unmarshal(b, &decoded, json.RejectUnknownMembers(true)); err != nil {
		return invalid("body", "Invalid flow query")
	}
	*in = FlowQueryInput(decoded)
	in.present = map[string]bool{}
	for key := range m {
		in.present[key] = true
	}
	return in.validate()
}

func (in FlowQueryInput) validate() error {
	has := func(key, value string) bool { return in.present[key] || value != "" }
	if !slices.Contains([]string{"entrypoints", "steps", "transitions", "accesses"}, in.View) {
		return invalid("view", "Select entrypoints, steps, transitions or accesses")
	}
	if in.Limit < 0 || in.Limit > MaxGraphPageSize {
		return invalid("limit", "limit must be between 1 and 500")
	}
	if !utf8.ValidString(in.Search) || len(in.Search) > 1024 || strings.ContainsRune(in.Search, 0) {
		return invalid("search", "Invalid search string")
	}
	if in.View != "entrypoints" && has("search", in.Search) || in.View != "accesses" && (has("entrypointId", in.EntrypointID) || has("dataNodeId", in.DataNodeID) || has("accessKind", in.AccessKind)) || in.View != "steps" && in.View != "transitions" && has("flowId", in.FlowID) {
		return invalid("selectors", "Selectors are not valid for this view")
	}
	if in.View == "steps" || in.View == "transitions" {
		if !ValidID(in.FlowID) {
			return invalid("flowId", "A canonical flow UUID is required")
		}
	}
	if in.View == "accesses" {
		if has("entrypointId", in.EntrypointID) == has("dataNodeId", in.DataNodeID) {
			return invalid("selectors", "Select exactly one entrypointId or dataNodeId")
		}
		if has("entrypointId", in.EntrypointID) && !ValidID(in.EntrypointID) || has("dataNodeId", in.DataNodeID) && !ValidID(in.DataNodeID) {
			return invalid("selectors", "Use a canonical UUID selector")
		}
		if has("accessKind", in.AccessKind) && !slices.Contains([]string{"reads", "writes", "deletes"}, in.AccessKind) {
			return invalid("accessKind", "Select reads, writes or deletes")
		}
	}
	return nil
}

type runtimeFlowProjection struct {
	state            *RevisionState
	in               FlowQueryInput
	page             *FlowPage
	nodes            map[string]Node
	edges            map[string]Edge
	out              map[string][]Edge
	children         map[string][]Node
	evidence         map[string]Evidence
	limitations      map[string]bool
	truncations      map[string]bool
	accesses         map[string]FlowAccessItem
	selectedAccess   map[string]string
	discoveredAccess map[string]bool
}

func projectRuntimeFlow(ctx context.Context, state *RevisionState, in FlowQueryInput) (*FlowPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if state == nil || !ValidID(in.RevisionID) || in.RevisionID != state.Revision.ID {
		return nil, notFound()
	}
	source := primarySource(*state)
	if state.Revision.SchemaVersion != RuntimeSchemaVersion || source == nil || len(source.Provider.Profiles) != 3 || !slices.Contains(source.Provider.Profiles, GraphProfile) || !slices.Contains(source.Provider.Profiles, RelationalProfile) || !slices.Contains(source.Provider.Profiles, RuntimeProfile) {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Flow reads require a pinned runtime-flow source revision"}
	}
	page := &FlowPage{ProjectID: state.Revision.ProjectID, RevisionID: state.Revision.ID, SemanticHash: state.Revision.SemanticHash, View: in.View, Coverage: RevisionCoverage{Coverage: state.Revision.Coverage, Snapshots: slices.Clone(state.Sources), Inventory: slices.Clone(state.Inventory), ReconciliationGaps: []string{}}, Limitations: []string{}, TruncationReasons: []string{}}
	p := &runtimeFlowProjection{state: state, in: in, page: page, nodes: map[string]Node{}, edges: map[string]Edge{}, out: map[string][]Edge{}, children: map[string][]Node{}, evidence: map[string]Evidence{}, limitations: map[string]bool{}, truncations: map[string]bool{}, accesses: map[string]FlowAccessItem{}, selectedAccess: map[string]string{}, discoveredAccess: map[string]bool{}}
	for _, n := range state.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.nodes[n.ID] = n
		if n.ParentID != nil {
			p.children[*n.ParentID] = append(p.children[*n.ParentID], n)
		}
		if n.Freshness != nil && n.Freshness.Status == "stale" {
			page.Coverage.StaleCounts.Nodes++
		}
	}
	for _, e := range state.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.edges[e.ID] = e
		p.out[e.From] = append(p.out[e.From], e)
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			page.Coverage.StaleCounts.Edges++
		}
	}
	for _, e := range state.Evidence {
		p.evidence[e.ID] = e
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			page.Coverage.StaleCounts.Evidence++
		}
	}
	for _, edges := range p.out {
		slices.SortFunc(edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	}
	for _, children := range p.children {
		slices.SortFunc(children, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	}
	for _, gap := range state.Revision.Coverage.Gaps {
		p.limitations[gap] = true
	}
	for _, limitation := range source.Provider.Limitations {
		p.limitations[limitation] = true
	}
	p.limitations["Source reachability does not verify runtime execution, branch feasibility or transaction atomicity"] = true
	// The page size is intentionally excluded: continuation recomputes the same
	// bounded projection, then selects a different-sized window over that result.
	scope, err := requestDigest(struct {
		ProjectID, RevisionID, SemanticHash, Policy, View, Search, FlowID, EntrypointID, DataNodeID, AccessKind string
	}{page.ProjectID, page.RevisionID, page.SemanticHash, runtimeTraversalPolicy, in.View, strings.ToLower(in.Search), in.FlowID, in.EntrypointID, in.DataNodeID, in.AccessKind})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "flow", page.ProjectID, scope, in.View != "accesses")
	if err != nil {
		return nil, err
	}
	if in.View == "accesses" && after != "" {
		entry, access, ok := strings.Cut(after, "/")
		if !ok || entry != "" && !ValidID(entry) || !ValidID(access) {
			return nil, invalid("cursor", "Invalid access ordering key")
		}
	}
	switch in.View {
	case "entrypoints":
		page.EntryPointItems = []FlowEntrypointItem{}
		for _, id := range slices.Sorted(maps.Keys(p.nodes)) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			n := p.nodes[id]
			if n.Kind != "http_operation" || !strings.Contains(strings.ToLower(n.Name+" "+runtimeAttributeString(n.Attributes, "method")+" "+runtimeAttributeString(n.Attributes, "path")), strings.ToLower(in.Search)) {
				continue
			}
			page.EntryPointItems = append(page.EntryPointItems, p.entrypoint(n))
		}
		page.EntryPointItems = slices.DeleteFunc(page.EntryPointItems, func(i FlowEntrypointItem) bool { return i.Operation.ID <= after })
		if len(page.EntryPointItems) > limit {
			page.NextCursor = encodeGraphPage("flow", page.ProjectID, scope, page.EntryPointItems[limit-1].Operation.ID)
			page.EntryPointItems = page.EntryPointItems[:limit]
		}
	case "steps", "transitions":
		flow, ok := p.nodes[in.FlowID]
		if !ok {
			return nil, notFound()
		}
		if flow.Kind != "flow" {
			return nil, invalid("flowId", "Selector must identify a flow")
		}
		p.observeNode(flow)
		if in.View == "steps" {
			page.StepItems = []Node{}
			for _, n := range p.children[flow.ID] {
				if n.Kind == "flow_step" {
					p.observeNode(n)
					page.StepItems = append(page.StepItems, n)
				}
			}
			page.StepItems = slices.DeleteFunc(page.StepItems, func(n Node) bool { return n.ID <= after })
			if len(page.StepItems) > limit {
				page.NextCursor = encodeGraphPage("flow", page.ProjectID, scope, page.StepItems[limit-1].ID)
				page.StepItems = page.StepItems[:limit]
			}
		} else {
			page.TransitionItems = []Edge{}
			for _, n := range p.children[flow.ID] {
				if n.Kind != "flow_step" {
					continue
				}
				p.observeNode(n)
				for _, e := range p.out[n.ID] {
					if slices.Contains([]string{"next", "branch", "error", "returns", "calls", "begins", "commits", "rolls_back"}, e.Kind) {
						p.observeEdge(e)
						page.TransitionItems = append(page.TransitionItems, e)
					}
				}
			}
			slices.SortFunc(page.TransitionItems, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
			page.TransitionItems = slices.DeleteFunc(page.TransitionItems, func(e Edge) bool { return e.ID <= after })
			if len(page.TransitionItems) > limit {
				page.NextCursor = encodeGraphPage("flow", page.ProjectID, scope, page.TransitionItems[limit-1].ID)
				page.TransitionItems = page.TransitionItems[:limit]
			}
		}
	case "accesses":
		if err := p.selectAccesses(); err != nil {
			return nil, err
		}
		if err := p.traverse(ctx); err != nil {
			return nil, err
		}
		page.AccessItems = []FlowAccessItem{}
		for _, key := range slices.Sorted(maps.Keys(p.accesses)) {
			page.AccessItems = append(page.AccessItems, p.accesses[key])
		}
		page.AccessItems = slices.DeleteFunc(page.AccessItems, func(i FlowAccessItem) bool { return runtimeAccessKey(i) <= after })
		if len(page.AccessItems) > limit {
			page.NextCursor = encodeGraphPage("flow", page.ProjectID, scope, runtimeAccessKey(page.AccessItems[limit-1]))
			page.AccessItems = page.AccessItems[:limit]
		}
	}
	page.Limitations = runtimeSortedKeys(p.limitations)
	page.TruncationReasons = runtimeSortedKeys(p.truncations)
	page.Truncated = len(page.TruncationReasons) != 0
	return page, nil
}

func runtimeSortedKeys(values map[string]bool) []string {
	return append([]string{}, slices.Sorted(maps.Keys(values))...)
}

func runtimeAttributeString(attrs map[string]jsontext.Value, key string) string {
	var value string
	_ = json.Unmarshal(attrs[key], &value)
	return value
}
func runtimeAttributeStrings(attrs map[string]jsontext.Value, key string) []string {
	var value []string
	_ = json.Unmarshal(attrs[key], &value)
	return value
}

func (p *runtimeFlowProjection) recordStatus(ids []string, fresh *AssertionFreshness, limitations map[string]bool) string {
	status := "explicit"
	if fresh != nil && fresh.Status == "stale" {
		status = "stale"
		limitations["Stale source assertion retained from an earlier snapshot"] = true
		for _, reason := range fresh.Reasons {
			limitations[reason] = true
		}
	}
	if len(ids) == 0 {
		status = runtimeWorseStatus(status, "inferred")
		limitations["No explicit source evidence for a witness record"] = true
	}
	for _, id := range ids {
		e, ok := p.evidence[id]
		if !ok {
			status = runtimeWorseStatus(status, "unresolved")
			limitations["Missing evidence "+id] = true
			continue
		}
		status = runtimeWorseStatus(status, e.Status)
		if e.Status != "explicit" {
			limitations["Source evidence "+id+" is "+e.Status] = true
		}
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			status = runtimeWorseStatus(status, "stale")
			limitations["Stale source evidence "+id] = true
		}
	}
	return status
}

func runtimeWorseStatus(a, b string) string {
	weight := func(s string) int {
		switch s {
		case "explicit":
			return 0
		case "inferred":
			return 1
		case "stale":
			return 2
		case "unresolved":
			return 3
		default:
			return 1
		}
	}
	if weight(b) > weight(a) {
		if slices.Contains([]string{"explicit", "inferred", "stale", "unresolved"}, b) {
			return b
		}
		return "inferred"
	}
	return a
}

func (p *runtimeFlowProjection) nodeLimitations(n Node, limitations map[string]bool) {
	for _, gap := range runtimeAttributeStrings(n.Attributes, "gaps") {
		limitations[n.ID+": "+gap] = true
	}
	for _, key := range []string{"analysisStatus", "exitStatus", "dispatchStatus", "columnScope", "boundaryStatus"} {
		if v := runtimeAttributeString(n.Attributes, key); v != "" && v != "complete" {
			limitations[n.ID+": "+key+"="+v] = true
		}
	}
	for _, key := range []string{"reason", "dispatchReason", "definitionReason", "nativeReason"} {
		if reason := runtimeAttributeString(n.Attributes, key); reason != "" {
			limitations[n.ID+": "+reason] = true
		}
	}
	if n.Kind == "handler" || n.Kind == "symbol" {
		found := false
		for _, child := range p.children[n.ID] {
			if child.Kind == "flow" {
				found = true
				break
			}
		}
		if !found {
			limitations["Missing source flow for "+n.ID] = true
		}
	}
}

func (p *runtimeFlowProjection) observeNode(n Node) {
	p.nodeLimitations(n, p.limitations)
	p.recordStatus(n.EvidenceIDs, n.Freshness, p.limitations)
}
func (p *runtimeFlowProjection) observeEdge(e Edge) {
	p.recordStatus(e.EvidenceIDs, e.Freshness, p.limitations)
}

func (p *runtimeFlowProjection) entrypoint(n Node) FlowEntrypointItem {
	i := FlowEntrypointItem{Operation: n, HandlerIDs: []string{}, FlowIDs: []string{}, UnresolvedHandles: []Edge{}, EvidenceIDs: []string{}, Limitations: []string{}}
	limitations := map[string]bool{}
	proof := map[string]bool{}
	observe := func(n Node) {
		p.nodeLimitations(n, limitations)
		p.recordStatus(n.EvidenceIDs, n.Freshness, limitations)
		for _, id := range n.EvidenceIDs {
			proof[id] = true
		}
	}
	observe(n)
	handled := false
	for _, e := range p.out[n.ID] {
		if e.Kind != "handles" {
			continue
		}
		handled = true
		p.recordStatus(e.EvidenceIDs, e.Freshness, limitations)
		for _, id := range e.EvidenceIDs {
			proof[id] = true
		}
		h, ok := p.nodes[e.To]
		if !ok || h.Kind == "unresolved_target" {
			i.UnresolvedHandles = append(i.UnresolvedHandles, e)
			limitations["Unresolved HTTP handle "+e.ID] = true
			if ok {
				observe(h)
			}
			continue
		}
		i.HandlerIDs = append(i.HandlerIDs, h.ID)
		observe(h)
		for _, flow := range p.children[h.ID] {
			if flow.Kind == "flow" {
				i.FlowIDs = append(i.FlowIDs, flow.ID)
				observe(flow)
			}
		}
	}
	if !handled {
		limitations["No imported HTTP handle for "+n.ID] = true
	}
	slices.Sort(i.HandlerIDs)
	i.HandlerIDs = slices.Compact(i.HandlerIDs)
	slices.Sort(i.FlowIDs)
	i.FlowIDs = slices.Compact(i.FlowIDs)
	i.EvidenceIDs = runtimeSortedKeys(proof)
	i.Limitations = runtimeSortedKeys(limitations)
	for key := range limitations {
		p.limitations[key] = true
	}
	return i
}

func runtimeAccessKind(kind string) bool {
	return slices.Contains([]string{"reads", "writes", "deletes"}, kind)
}

func (p *runtimeFlowProjection) selectAccesses() error {
	if p.in.EntrypointID != "" {
		n, ok := p.nodes[p.in.EntrypointID]
		if !ok {
			return notFound()
		}
		if n.Kind != "http_operation" {
			return invalid("entrypointId", "Selector must identify an HTTP operation")
		}
	}
	var data Node
	if p.in.DataNodeID != "" {
		var ok bool
		data, ok = p.nodes[p.in.DataNodeID]
		if !ok {
			return notFound()
		}
		if !slices.Contains([]string{"table", "column", "view"}, data.Kind) {
			return invalid("dataNodeId", "Select a table, column or view")
		}
	}
	for _, e := range p.state.Edges {
		if !runtimeAccessKind(e.Kind) || p.in.AccessKind != "" && e.Kind != p.in.AccessKind {
			continue
		}
		relation := "direct"
		if data.ID != "" {
			target, ok := p.nodes[e.To]
			if !ok {
				continue
			}
			switch data.Kind {
			case "column":
				if target.ID != data.ID {
					if data.ParentID == nil || target.ID != *data.ParentID || runtimeAttributeString(e.Attributes, "columnScope") != "unknown" {
						continue
					}
					relation = "possible"
				}
			case "table":
				if target.ID != data.ID && (target.Kind != "column" || target.ParentID == nil || *target.ParentID != data.ID) {
					continue
				}
			case "view":
				if target.ID != data.ID {
					continue
				}
			}
		}
		p.selectedAccess[e.ID] = relation
	}
	return nil
}

type runtimeReachState struct {
	entrypointID, nodeID string
	callHops             int
}
type runtimeWitness struct {
	runtimeReachState
	nodes, edges []string
}

func runtimeCompareWitness(a, b runtimeWitness) int {
	if len(a.edges) != len(b.edges) {
		return len(a.edges) - len(b.edges)
	}
	if order := slices.Compare(a.edges, b.edges); order != 0 {
		return order
	}
	return slices.Compare(a.nodes, b.nodes)
}

func (p *runtimeFlowProjection) relevant(n Node, e Edge) bool {
	switch e.Kind {
	case "handles":
		return n.Kind == "http_operation"
	case "contains":
		target := p.nodes[e.To]
		return (n.Kind == "handler" || n.Kind == "symbol") && target.Kind == "flow" || n.Kind == "flow" && e.To == runtimeAttributeString(n.Attributes, "entryStepId")
	case "next", "branch", "error", "returns":
		return n.Kind == "flow_step"
	case "calls":
		return n.Kind == "flow_step" || n.Kind == "handler" || n.Kind == "symbol"
	case "reads", "writes", "deletes":
		return n.Kind == "query"
	}
	return false
}

func (p *runtimeFlowProjection) traverse(ctx context.Context) error {
	queue := []runtimeWitness{}
	seen := map[runtimeReachState]runtimeWitness{}
	for _, id := range slices.Sorted(maps.Keys(p.nodes)) {
		n := p.nodes[id]
		if n.Kind != "http_operation" || p.in.EntrypointID != "" && id != p.in.EntrypointID {
			continue
		}
		w := runtimeWitness{runtimeReachState: runtimeReachState{entrypointID: id, nodeID: id}, nodes: []string{id}, edges: []string{}}
		if len(seen) == runtimeMaxStates {
			p.truncations["state_limit"] = true
			break
		}
		seen[w.runtimeReachState] = w
		queue = append(queue, w)
	}
	examined := 0
	stop := false
	for head := 0; head < len(queue) && !stop; head++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		w := queue[head]
		if best := seen[w.runtimeReachState]; runtimeCompareWitness(w, best) > 0 {
			continue
		}
		n := p.nodes[w.nodeID]
		p.observeNode(n)
		if n.Kind == "http_operation" {
			p.entrypoint(n)
		}
		for _, e := range p.out[n.ID] {
			if !p.relevant(n, e) {
				continue
			}
			if examined == runtimeMaxExaminedEdges {
				p.truncations["edge_limit"] = true
				stop = true
				break
			}
			examined++
			p.observeEdge(e)
			target, ok := p.nodes[e.To]
			if !ok || target.Kind == "unresolved_target" || e.Kind == "calls" && target.Kind == "external_system" {
				p.limitations["Unknown source reachability boundary "+e.ID] = true
				if ok {
					p.observeNode(target)
				}
				if !runtimeAccessKind(e.Kind) {
					continue
				}
			}
			hops := w.callHops
			if e.Kind == "calls" && ok && target.Kind != "unresolved_target" {
				hops++
			}
			if hops > runtimeMaxCallHops {
				p.truncations["call_hops"] = true
			}
			if len(w.edges) == runtimeMaxWitnessEdges {
				p.truncations["witness_length"] = true
			}
			if hops > runtimeMaxCallHops || len(w.edges) == runtimeMaxWitnessEdges {
				continue
			}
			next := runtimeWitness{runtimeReachState: runtimeReachState{entrypointID: w.entrypointID, nodeID: e.To, callHops: hops}, nodes: append(slices.Clone(w.nodes), e.To), edges: append(slices.Clone(w.edges), e.ID)}
			best, exists := seen[next.runtimeReachState]
			if !exists && len(seen) == runtimeMaxStates {
				p.truncations["state_limit"] = true
				continue
			}
			if !exists || runtimeCompareWitness(next, best) < 0 {
				seen[next.runtimeReachState] = next
				if !runtimeAccessKind(e.Kind) {
					queue = append(queue, next)
				}
			}
			if _, selected := p.selectedAccess[e.ID]; runtimeAccessKind(e.Kind) && selected {
				p.discoveredAccess[e.ID] = true
				key := w.entrypointID + "/" + e.ID
				if previous, found := p.accesses[key]; found {
					old := runtimeWitness{nodes: previous.PathNodeIDs, edges: previous.PathEdgeIDs}
					if runtimeCompareWitness(next, old) >= 0 {
						continue
					}
				} else if len(p.accesses) == runtimeMaxAccessPairs {
					p.truncations["result_limit"] = true
					stop = true
					break
				}
				p.accesses[key] = p.access(e, &next, p.selectedAccess[e.ID])
			}
		}
	}
	// Reverse inspection preserves imported access records even if no caller was
	// found. With truncation this is discovery absence, never proven absence.
	if p.in.DataNodeID != "" {
		for _, id := range slices.Sorted(maps.Keys(p.selectedAccess)) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if p.discoveredAccess[id] {
				continue
			}
			if len(p.accesses) == runtimeMaxAccessPairs {
				p.truncations["result_limit"] = true
				break
			}
			p.accesses["/"+id] = p.access(p.edges[id], nil, p.selectedAccess[id])
		}
	}
	return nil
}

func runtimeAccessKey(i FlowAccessItem) string {
	entry := ""
	if i.EntrypointID != nil {
		entry = *i.EntrypointID
	}
	return entry + "/" + i.AccessEdgeID
}

func (p *runtimeFlowProjection) access(e Edge, w *runtimeWitness, relation string) FlowAccessItem {
	i := FlowAccessItem{AccessEdgeID: e.ID, QueryID: e.From, TargetID: e.To, DatastoreID: runtimeAttributeString(e.Attributes, "datastoreId"), FacetKey: runtimeAttributeString(e.Attributes, "facetKey"), AccessKind: e.Kind, AccessMode: runtimeAttributeString(e.Attributes, "accessMode"), Relation: relation, PathNodeIDs: []string{}, PathEdgeIDs: []string{}, Status: "explicit", EvidenceIDs: []string{}, Limitations: []string{}}
	limitations := map[string]bool{}
	evidence := map[string]bool{}
	observeNode := func(n Node) {
		p.nodeLimitations(n, limitations)
		ids := n.EvidenceIDs
		if n.ID == e.To {
			ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
				proof, ok := p.evidence[id]
				if !ok || proof.PropertyPath == nil {
					return false
				}
				facet, scoped := strings.CutPrefix(*proof.PropertyPath, "/attributes/facets/")
				if !scoped {
					return false
				}
				key, _, _ := strings.Cut(facet, "/")
				return key != escapeRelationalPointer(i.FacetKey)
			})
		}
		i.Status = runtimeWorseStatus(i.Status, p.recordStatus(ids, n.Freshness, limitations))
		for _, id := range ids {
			evidence[id] = true
		}
		if n.Kind == "unresolved_target" {
			i.Status = "unresolved"
		}
	}
	observeEdge := func(e Edge) {
		i.Status = runtimeWorseStatus(i.Status, p.recordStatus(e.EvidenceIDs, e.Freshness, limitations))
		for _, id := range e.EvidenceIDs {
			evidence[id] = true
		}
	}
	if w != nil {
		i.EntrypointID = new(w.entrypointID)
		i.PathNodeIDs = slices.Clone(w.nodes)
		i.PathEdgeIDs = slices.Clone(w.edges)
		for _, id := range w.nodes {
			n := p.nodes[id]
			observeNode(n)
			if n.Kind == "flow" {
				i.FlowID = new(id)
			}
		}
		for _, id := range w.edges {
			observeEdge(p.edges[id])
		}
	} else {
		observeNode(p.nodes[e.From])
		observeNode(p.nodes[e.To])
		observeEdge(e)
		limitations["No discovered HTTP caller in the bounded pinned scope (unattached)"] = true
		if len(p.truncations) != 0 {
			limitations["Caller reachability is incomplete because traversal was truncated"] = true
		}
	}
	if relation == "possible" {
		limitations["Unknown table column scope; possible access to the selected column is not confirmed"] = true
	}
	if reason := runtimeAttributeString(e.Attributes, "scopeReason"); reason != "" {
		limitations[reason] = true
	}
	// Facet proof belongs to the selected source descriptor; other facets cannot
	// provide an explicit/stale conclusion for this access.
	if target, ok := p.nodes[e.To]; ok {
		var facets map[string]jsontext.Value
		if json.Unmarshal(target.Attributes["facets"], &facets) == nil {
			var facet struct {
				AnalysisStatus string              `json:"analysisStatus"`
				Gaps           []string            `json:"gaps"`
				EvidenceIDs    []string            `json:"evidenceIds"`
				Freshness      *AssertionFreshness `json:"freshness"`
			}
			if raw, found := facets[i.FacetKey]; found && json.Unmarshal(raw, &facet) == nil {
				i.Status = runtimeWorseStatus(i.Status, p.recordStatus(facet.EvidenceIDs, facet.Freshness, limitations))
				for _, id := range facet.EvidenceIDs {
					evidence[id] = true
				}
				for _, gap := range facet.Gaps {
					limitations[target.ID+": "+gap] = true
				}
				if facet.AnalysisStatus != "complete" {
					limitations[fmt.Sprintf("%s: selected facet analysisStatus=%s", target.ID, facet.AnalysisStatus)] = true
				}
			} else {
				i.Status = runtimeWorseStatus(i.Status, "unresolved")
				limitations["Selected target facet is unavailable"] = true
			}
		}
	}
	i.EvidenceIDs = runtimeSortedKeys(evidence)
	i.Limitations = runtimeSortedKeys(limitations)
	for key := range limitations {
		p.limitations[key] = true
	}
	return i
}

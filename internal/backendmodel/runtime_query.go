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
	Proposal        *ProposalReadTarget        `json:"proposal,omitzero"`
	ChangeProposal  *ProposalReadTarget        `json:"changeProposal,omitzero"`
	ImportCandidate *ImportCandidateReadTarget `json:"importCandidate,omitzero"`
	RevisionID      string                     `json:"revisionId,omitempty"`
	View            string                     `json:"view"`
	Search          string                     `json:"search,omitempty"`
	FlowID          string                     `json:"flowId,omitempty"`
	EntrypointID    string                     `json:"entrypointId,omitempty"`
	DataNodeID      string                     `json:"dataNodeId,omitempty"`
	AccessKind      string                     `json:"accessKind,omitempty"`
	Limit           int                        `json:"limit,omitzero"`
	Cursor          string                     `json:"cursor,omitempty"`
	present         map[string]bool
}

type FlowPage struct {
	Target            *BackendReadTarget   `json:"target,omitzero"`
	Pins              *EffectiveGraphPins  `json:"pins,omitzero"`
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
	if in.Proposal != nil || in.ChangeProposal != nil || in.ImportCandidate != nil {
		return r.queryEffectiveFlow(ctx, projectID, in)
	}
	if in.RevisionID != "" && in.Proposal == nil && in.ChangeProposal == nil {
		revision, err := r.Revision(ctx, projectID, in.RevisionID)
		if err != nil {
			return nil, err
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			return r.queryEffectiveFlow(ctx, projectID, in)
		}
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	state, err := loadRevisionState(ctx, r.db.R, projectID, in.RevisionID)
	if err != nil {
		return nil, err
	}
	var graph *SourceGraphSnapshot
	if state.Revision.SchemaVersion == ComposedSchemaVersion {
		graph, err = r.ResolveSourceGraph(ctx, projectID, in.RevisionID)
		if err != nil {
			return nil, err
		}
	}
	page, err := projectRuntimeFlowWithSource(ctx, state, graph, in)
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
	if advancedReadQuery(b) {
		return decodeAdvancedReadQuery(b, in)
	}
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
	if in.Proposal != nil || in.ChangeProposal != nil || in.ImportCandidate != nil {
		target := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
		if err := target.Validate(); err != nil {
			return err
		}
		if err := rejectStagedView(target); err != nil {
			return err
		}
	}
	if !slices.Contains([]string{"entrypoints", "steps", "transitions", "accesses"}, in.View) {
		return invalid("view", "Select entrypoints, steps, transitions or accesses")
	}
	if in.Limit < 0 || in.Limit > MaxGraphPageSize {
		return invalid("limit", "limit must be between 1 and 500")
	}
	if !utf8.ValidString(in.Search) || len(in.Search) > 1024 || strings.ContainsRune(in.Search, 0) {
		return invalid("search", "Invalid search string")
	}
	return in.validateSelectors()
}

// has reports a selector as given when it was present in the body, even
// empty, so an explicit "" still counts against a view that rejects it.
func (in FlowQueryInput) has(key, value string) bool { return in.present[key] || value != "" }

func (in FlowQueryInput) validateSelectors() error {
	has := in.has
	if in.View != "entrypoints" && has("search", in.Search) || in.View != "accesses" && (has("entrypointId", in.EntrypointID) || has("dataNodeId", in.DataNodeID) || has("accessKind", in.AccessKind)) || in.View != "steps" && in.View != "transitions" && has("flowId", in.FlowID) {
		return invalid("selectors", "Selectors are not valid for this view")
	}
	if in.View == "steps" || in.View == "transitions" {
		if !ValidID(in.FlowID) {
			return invalid("flowId", "A canonical flow UUID is required")
		}
	}
	if in.View == "accesses" {
		return in.validateAccessSelectors()
	}
	return nil
}

func (in FlowQueryInput) validateAccessSelectors() error {
	has := in.has
	if has("entrypointId", in.EntrypointID) == has("dataNodeId", in.DataNodeID) {
		return invalid("selectors", "Select exactly one entrypointId or dataNodeId")
	}
	if has("entrypointId", in.EntrypointID) && !ValidID(in.EntrypointID) || has("dataNodeId", in.DataNodeID) && !ValidID(in.DataNodeID) {
		return invalid("selectors", "Use a canonical UUID selector")
	}
	if has("accessKind", in.AccessKind) && !slices.Contains([]string{"reads", "writes", "deletes"}, in.AccessKind) {
		return invalid("accessKind", "Select reads, writes or deletes")
	}
	return nil
}

type runtimeFlowProjection struct {
	effective        *EffectiveGraphSnapshot
	schema           string
	source           *SourceGraphSnapshot
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
	return projectRuntimeFlowWithSource(ctx, state, nil, in)
}
func projectRuntimeFlowWithSource(ctx context.Context, state *RevisionState, sourceGraph *SourceGraphSnapshot, in FlowQueryInput) (*FlowPage, error) {
	return projectRuntimeFlowWithEffective(ctx, state, sourceGraph, nil, in)
}
func projectRuntimeFlowWithEffective(ctx context.Context, state *RevisionState, sourceGraph *SourceGraphSnapshot, effective *EffectiveGraphSnapshot, in FlowQueryInput) (*FlowPage, error) {
	schema, hash := state.Revision.SchemaVersion, state.Revision.SemanticHash
	if effective != nil {
		schema = effective.Pins.StructuralSchemaVersion
		hash = effective.Pins.EffectiveSemanticHash
	}
	source, err := checkRuntimeFlowRequest(ctx, state, sourceGraph, effective, in, schema)
	if err != nil {
		return nil, err
	}
	page := &FlowPage{ProjectID: state.Revision.ProjectID, RevisionID: state.Revision.ID, SemanticHash: hash, View: in.View, Coverage: RevisionCoverage{Coverage: state.Revision.Coverage, Snapshots: slices.Clone(state.Sources), Inventory: slices.Clone(state.Inventory), ReconciliationGaps: []string{}}, Limitations: []string{}, TruncationReasons: []string{}}
	if sourceGraph != nil {
		page.Coverage.Source = sourceVectorReadContext(sourceGraph)
	}
	p := &runtimeFlowProjection{effective: effective, schema: schema, source: sourceGraph, state: state, in: in, page: page, nodes: map[string]Node{}, edges: map[string]Edge{}, out: map[string][]Edge{}, children: map[string][]Node{}, evidence: map[string]Evidence{}, limitations: map[string]bool{}, truncations: map[string]bool{}, accesses: map[string]FlowAccessItem{}, selectedAccess: map[string]string{}, discoveredAccess: map[string]bool{}}
	if err := p.indexNodes(ctx); err != nil {
		return nil, err
	}
	if err := p.indexEdges(ctx); err != nil {
		return nil, err
	}
	p.collectLimitations(source)
	scope, err := p.pageScope()
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "flow", page.ProjectID, scope, in.View != "accesses")
	if err != nil {
		return nil, err
	}
	switch in.View {
	case "entrypoints":
		err = p.entrypointPage(ctx, limit, after, scope)
	case "steps", "transitions":
		err = p.flowPage(limit, after, scope)
	case "accesses":
		err = p.accessPage(ctx, limit, after, scope)
	}
	if err != nil {
		return nil, err
	}
	page.Limitations = runtimeSortedKeys(p.limitations)
	page.TruncationReasons = runtimeSortedKeys(p.truncations)
	page.Truncated = len(page.TruncationReasons) != 0
	return page, nil
}

// checkRuntimeFlowRequest rejects a request this revision cannot answer and
// returns the revision's primary source, whose limitations the page carries.
func checkRuntimeFlowRequest(ctx context.Context, state *RevisionState, sourceGraph *SourceGraphSnapshot, effective *EffectiveGraphSnapshot, in FlowQueryInput, schema string) (*SourceSnapshot, error) {
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
	if effective == nil && sourceGraph == nil && (!isRuntimeSchema(schema) || source == nil || !sourceProfilesMatch(schema, source.Provider.Profiles)) {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Flow reads require a pinned runtime-flow source revision"}
	}
	return source, nil
}

func (p *runtimeFlowProjection) indexNodes(ctx context.Context) error {
	for _, n := range p.state.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.effective != nil {
			n = effectiveNodeRecord(p.effective, n)
		} else if p.source != nil {
			n.Source = sourceRecordReadContext(p.source, "node", n.ID)
		}
		p.nodes[n.ID] = n
		if n.ParentID != nil {
			p.children[*n.ParentID] = append(p.children[*n.ParentID], n)
		}
		if n.Freshness != nil && n.Freshness.Status == "stale" {
			p.page.Coverage.StaleCounts.Nodes++
		}
	}
	return nil
}

// indexEdges indexes edges and evidence, then fixes the ID order every
// adjacency list is walked in.
func (p *runtimeFlowProjection) indexEdges(ctx context.Context) error {
	for _, e := range p.state.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.effective != nil {
			e = effectiveEdgeRecord(p.effective, e)
		} else if p.source != nil {
			e.Source = sourceRecordReadContext(p.source, "edge", e.ID)
		}
		p.edges[e.ID] = e
		p.out[e.From] = append(p.out[e.From], e)
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			p.page.Coverage.StaleCounts.Edges++
		}
	}
	for _, e := range p.state.Evidence {
		p.evidence[e.ID] = e
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			p.page.Coverage.StaleCounts.Evidence++
		}
	}
	for _, edges := range p.out {
		slices.SortFunc(edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	}
	for _, children := range p.children {
		slices.SortFunc(children, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	}
	return nil
}

func (p *runtimeFlowProjection) collectLimitations(source *SourceSnapshot) {
	for _, gap := range p.state.Revision.Coverage.Gaps {
		p.limitations[gap] = true
	}
	if p.source == nil {
		for _, limitation := range source.Provider.Limitations {
			p.limitations[limitation] = true
		}
	} else {
		for _, part := range p.source.SourceVector.Partitions {
			for _, limitation := range part.Provider.Limitations {
				p.limitations[limitation] = true
			}
		}
	}
	p.limitations["Source reachability does not verify runtime execution, branch feasibility or transaction atomicity"] = true
}

// pageScope is the cursor scope digest. The page size is intentionally
// excluded: continuation recomputes the same bounded projection, then
// selects a different-sized window over that result.
func (p *runtimeFlowProjection) pageScope() (string, error) {
	page, in := p.page, p.in
	scope, err := requestDigest(struct {
		ProjectID, RevisionID, SemanticHash, Policy, View, Search, FlowID, EntrypointID, DataNodeID, AccessKind string
	}{page.ProjectID, page.RevisionID, page.SemanticHash, runtimePolicyForSchema(p.schema), in.View, strings.ToLower(in.Search), in.FlowID, in.EntrypointID, in.DataNodeID, in.AccessKind})
	if err != nil {
		return "", err
	}
	if p.effective != nil {
		page.Target = new(p.effective.Target)
		page.Pins = new(p.effective.Pins)
		scope, err = requestDigest(struct {
			Scope string
			Pins  EffectiveGraphPins
		}{scope, p.effective.Pins})
		if err != nil {
			return "", err
		}
	}
	return scope, nil
}

// entrypointPage cuts the cursor window BEFORE building items: entrypoint()
// merges each item's limitations into the page-wide list, and building every
// match made page 1 report the gaps of operations shown only on later pages
// (review 2026-10-06, F116). The window is the same one the old
// build-then-trim produced, because Operation.ID is the node ID.
func (p *runtimeFlowProjection) entrypointPage(ctx context.Context, limit int, after, scope string) error {
	page := p.page
	window := []Node{}
	for _, id := range slices.Sorted(maps.Keys(p.nodes)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := p.nodes[id]
		if id <= after || !sourceRuntimeEntrypoint(p.schema, n.Kind) || !strings.Contains(strings.ToLower(n.Name+" "+runtimeAttributeString(n.Attributes, "method")+" "+runtimeAttributeString(n.Attributes, "path")), strings.ToLower(p.in.Search)) {
			continue
		}
		window = append(window, n)
	}
	if len(window) > limit {
		page.NextCursor = encodeGraphPage("flow", page.ProjectID, scope, window[limit-1].ID)
		window = window[:limit]
	}
	page.EntryPointItems = make([]FlowEntrypointItem, 0, len(window))
	for _, n := range window {
		page.EntryPointItems = append(page.EntryPointItems, p.entrypoint(n))
	}
	return nil
}

func (p *runtimeFlowProjection) flowPage(limit int, after, scope string) error {
	flow, ok := p.nodes[p.in.FlowID]
	if !ok {
		return notFound()
	}
	if flow.Kind != "flow" {
		return invalid("flowId", "Selector must identify a flow")
	}
	p.observeNode(flow)
	if p.in.View == "steps" {
		p.stepPage(flow, limit, after, scope)
	} else {
		p.transitionPage(flow, limit, after, scope)
	}
	return nil
}

func (p *runtimeFlowProjection) stepPage(flow Node, limit int, after, scope string) {
	page := p.page
	page.StepItems = []Node{}
	for _, n := range p.children[flow.ID] {
		if n.Kind == "flow_step" {
			page.StepItems = append(page.StepItems, n)
		}
	}
	page.StepItems = slices.DeleteFunc(page.StepItems, func(n Node) bool { return n.ID <= after })
	if len(page.StepItems) > limit {
		page.NextCursor = encodeGraphPage("flow", page.ProjectID, scope, page.StepItems[limit-1].ID)
		page.StepItems = page.StepItems[:limit]
	}
	// Observe only the returned window (review 2026-10-06, F116).
	for _, n := range page.StepItems {
		p.observeNode(n)
	}
}

func (p *runtimeFlowProjection) transitionPage(flow Node, limit int, after, scope string) {
	page := p.page
	page.TransitionItems = []Edge{}
	silent := []Node{}
	// Every effective read and every composed schema carries emits:
	// a full change proposal over a source5 baseline reads with
	// schema "6" and no native source graph, and the older gate
	// dropped its emits edges (review 2026-10-06, F110).
	emits := p.effective != nil || p.schema == EventsSchemaVersion || p.schema == ComposedSchemaVersion || p.source != nil
	for _, n := range p.children[flow.ID] {
		if n.Kind != "flow_step" {
			continue
		}
		before := len(page.TransitionItems)
		for _, e := range p.out[n.ID] {
			if slices.Contains([]string{"next", "branch", "error", "returns", "calls", "begins", "commits", "rolls_back"}, e.Kind) || emits && e.Kind == "emits" {
				page.TransitionItems = append(page.TransitionItems, e)
			}
		}
		if len(page.TransitionItems) == before {
			silent = append(silent, n)
		}
	}
	slices.SortFunc(page.TransitionItems, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	page.TransitionItems = slices.DeleteFunc(page.TransitionItems, func(e Edge) bool { return e.ID <= after })
	if len(page.TransitionItems) > limit {
		page.NextCursor = encodeGraphPage("flow", page.ProjectID, scope, page.TransitionItems[limit-1].ID)
		page.TransitionItems = page.TransitionItems[:limit]
	}
	// Observe the window's edges and the steps they leave, not every
	// step of the flow (review 2026-10-06, F116). A step that leaves no
	// transition is on no page, so its limitations ride on the last one
	// instead of vanishing from the view.
	observed := map[string]bool{}
	for _, e := range page.TransitionItems {
		if step, ok := p.nodes[e.From]; ok && !observed[step.ID] {
			observed[step.ID] = true
			p.observeNode(step)
		}
		p.observeEdge(e)
	}
	if page.NextCursor == "" {
		for _, n := range silent {
			p.observeNode(n)
		}
	}
}

func (p *runtimeFlowProjection) accessPage(ctx context.Context, limit int, after, scope string) error {
	page := p.page
	if after != "" {
		entry, access, ok := strings.Cut(after, "/")
		if !ok || entry != "" && !ValidID(entry) || !ValidID(access) {
			return invalid("cursor", "Invalid access ordering key")
		}
	}
	if err := p.selectAccesses(); err != nil {
		return err
	}
	if err := p.traverse(ctx); err != nil {
		return err
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
	return nil
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
		case "desired", "inferred":
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
		if slices.Contains([]string{"explicit", "desired", "inferred", "stale", "unresolved"}, b) {
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
	p.sourceRecordStatus("node", n.ID, nil, n.EvidenceIDs, n.Freshness, p.limitations)
}
func (p *runtimeFlowProjection) observeEdge(e Edge) {
	p.sourceRecordStatus("edge", e.ID, nil, e.EvidenceIDs, e.Freshness, p.limitations)
}

func (p *runtimeFlowProjection) entrypoint(n Node) FlowEntrypointItem {
	i := FlowEntrypointItem{Operation: n, HandlerIDs: []string{}, FlowIDs: []string{}, UnresolvedHandles: []Edge{}, EvidenceIDs: []string{}, Limitations: []string{}}
	limitations := map[string]bool{}
	proof := map[string]bool{}
	observe := func(n Node) {
		p.nodeLimitations(n, limitations)
		p.sourceRecordStatus("node", n.ID, nil, n.EvidenceIDs, n.Freshness, limitations)
		for _, id := range p.sourceRecordEvidence("node", n.ID, nil, n.EvidenceIDs) {
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
		p.sourceRecordStatus("edge", e.ID, nil, e.EvidenceIDs, e.Freshness, limitations)
		for _, id := range p.sourceRecordEvidence("edge", e.ID, nil, e.EvidenceIDs) {
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
		if !sourceRuntimeEntrypoint(p.readSchema(), n.Kind) {
			return invalid("entrypointId", "Selector must identify a supported pinned entrypoint")
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
		if relation, ok := p.accessRelation(e, data); ok {
			p.selectedAccess[e.ID] = relation
		}
	}
	return nil
}

// accessRelation decides whether access edge e touches the selected data
// node (none selected: every access is direct). A table access with unknown
// column scope only possibly touches a selected column.
func (p *runtimeFlowProjection) accessRelation(e Edge, data Node) (string, bool) {
	if data.ID == "" {
		return "direct", true
	}
	target, ok := p.nodes[e.To]
	if !ok {
		return "", false
	}
	switch data.Kind {
	case "column":
		if target.ID != data.ID {
			if data.ParentID == nil || target.ID != *data.ParentID || runtimeAttributeString(e.Attributes, "columnScope") != "unknown" {
				return "", false
			}
			return "possible", true
		}
	case "table":
		if target.ID != data.ID && (target.Kind != "column" || target.ParentID == nil || *target.ParentID != data.ID) {
			return "", false
		}
	case "view":
		if target.ID != data.ID {
			return "", false
		}
	}
	return "direct", true
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
		return sourceRuntimeEntrypoint(p.readSchema(), n.Kind)
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
	t := &runtimeTraversal{p: p, queue: []runtimeWitness{}, seen: map[runtimeReachState]runtimeWitness{}}
	t.seedEntrypoints()
	for head := 0; head < len(t.queue) && !t.stop; head++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		w := t.queue[head]
		if best := t.seen[w.runtimeReachState]; runtimeCompareWitness(w, best) > 0 {
			continue
		}
		n := p.nodes[w.nodeID]
		p.observeNode(n)
		if sourceRuntimeEntrypoint(p.readSchema(), n.Kind) {
			p.entrypoint(n)
		}
		t.expand(w, n)
	}
	return p.unattachedAccesses(ctx)
}

// runtimeTraversal is the breadth-first witness search from the selected
// entrypoints, with the budgets that bound it.
type runtimeTraversal struct {
	p        *runtimeFlowProjection
	queue    []runtimeWitness
	seen     map[runtimeReachState]runtimeWitness
	examined int
	stop     bool
}

func (t *runtimeTraversal) seedEntrypoints() {
	p := t.p
	for _, id := range slices.Sorted(maps.Keys(p.nodes)) {
		n := p.nodes[id]
		if !sourceRuntimeEntrypoint(p.readSchema(), n.Kind) || p.in.EntrypointID != "" && id != p.in.EntrypointID {
			continue
		}
		w := runtimeWitness{runtimeReachState: runtimeReachState{entrypointID: id, nodeID: id}, nodes: []string{id}, edges: []string{}}
		if len(t.seen) == runtimeMaxStates {
			p.truncations["state_limit"] = true
			break
		}
		t.seen[w.runtimeReachState] = w
		t.queue = append(t.queue, w)
	}
}

func (t *runtimeTraversal) expand(w runtimeWitness, n Node) {
	p := t.p
	for _, e := range p.out[n.ID] {
		if !p.relevant(n, e) {
			continue
		}
		if t.examined == runtimeMaxExaminedEdges {
			p.truncations["edge_limit"] = true
			t.stop = true
			break
		}
		t.examined++
		t.follow(w, e)
		if t.stop {
			break
		}
	}
}

// follow extends witness w along e: it queues the extended witness when it
// is the best one for its state, and records e when it is a selected access.
func (t *runtimeTraversal) follow(w runtimeWitness, e Edge) {
	p := t.p
	p.observeEdge(e)
	target, ok := p.nodes[e.To]
	if p.boundaryEdge(e, target, ok) && !runtimeAccessKind(e.Kind) {
		return
	}
	hops, within := p.witnessBudget(w, e, target, ok)
	if !within {
		return
	}
	next := runtimeWitness{runtimeReachState: runtimeReachState{entrypointID: w.entrypointID, nodeID: e.To, callHops: hops}, nodes: append(slices.Clone(w.nodes), e.To), edges: append(slices.Clone(w.edges), e.ID)}
	best, exists := t.seen[next.runtimeReachState]
	if !exists && len(t.seen) == runtimeMaxStates {
		p.truncations["state_limit"] = true
		return
	}
	if !exists || runtimeCompareWitness(next, best) < 0 {
		t.seen[next.runtimeReachState] = next
		if !runtimeAccessKind(e.Kind) {
			t.queue = append(t.queue, next)
		}
	}
	if _, selected := p.selectedAccess[e.ID]; runtimeAccessKind(e.Kind) && selected {
		t.recordAccess(w, e, next)
	}
}

// boundaryEdge reports an edge whose target the pinned scope cannot follow,
// recording it as an unknown reachability boundary.
func (p *runtimeFlowProjection) boundaryEdge(e Edge, target Node, ok bool) bool {
	if !ok || target.Kind == "unresolved_target" || e.Kind == "calls" && target.Kind == "external_system" {
		p.limitations["Unknown source reachability boundary "+e.ID] = true
		if ok {
			p.observeNode(target)
		}
		return true
	}
	return false
}

// witnessBudget returns the call-hop count after e and whether the extended
// witness stays within the hop and length budgets, recording each it breaks.
func (p *runtimeFlowProjection) witnessBudget(w runtimeWitness, e Edge, target Node, ok bool) (int, bool) {
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
	return hops, hops <= runtimeMaxCallHops && len(w.edges) != runtimeMaxWitnessEdges
}

// recordAccess keeps the best witness per entrypoint/access pair; a new pair
// past the result budget stops the traversal.
func (t *runtimeTraversal) recordAccess(w runtimeWitness, e Edge, next runtimeWitness) {
	p := t.p
	p.discoveredAccess[e.ID] = true
	key := w.entrypointID + "/" + e.ID
	if previous, found := p.accesses[key]; found {
		old := runtimeWitness{nodes: previous.PathNodeIDs, edges: previous.PathEdgeIDs}
		if runtimeCompareWitness(next, old) >= 0 {
			return
		}
	} else if len(p.accesses) == runtimeMaxAccessPairs {
		p.truncations["result_limit"] = true
		t.stop = true
		return
	}
	p.accesses[key] = p.access(e, &next, p.selectedAccess[e.ID])
}

// unattachedAccesses is the reverse inspection: it preserves imported access
// records even if no caller was found. With truncation this is discovery
// absence, never proven absence.
func (p *runtimeFlowProjection) unattachedAccesses(ctx context.Context) error {
	if p.in.DataNodeID == "" {
		return nil
	}
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
		i.Status = runtimeWorseStatus(i.Status, p.sourceRecordStatus("node", n.ID, &LineageValueRef{Kind: "column", NodeID: n.ID, FacetKey: i.FacetKey}, ids, n.Freshness, limitations))
		for _, id := range p.sourceRecordEvidence("node", n.ID, &LineageValueRef{Kind: "column", NodeID: n.ID, FacetKey: i.FacetKey}, ids) {
			evidence[id] = true
		}
		if n.Kind == "unresolved_target" {
			i.Status = "unresolved"
		}
	}
	observeEdge := func(e Edge) {
		i.Status = runtimeWorseStatus(i.Status, p.sourceRecordStatus("edge", e.ID, nil, e.EvidenceIDs, e.Freshness, limitations))
		for _, id := range p.sourceRecordEvidence("edge", e.ID, nil, e.EvidenceIDs) {
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
	if target, ok := p.nodes[e.To]; ok {
		p.accessFacetProof(&i, target, limitations, evidence)
	}
	i.EvidenceIDs = runtimeSortedKeys(evidence)
	i.Limitations = runtimeSortedKeys(limitations)
	for key := range limitations {
		p.limitations[key] = true
	}
	return i
}

// accessFacetProof folds the selected target facet into the access. Facet
// proof belongs to the selected source descriptor; other facets cannot
// provide an explicit/stale conclusion for this access.
func (p *runtimeFlowProjection) accessFacetProof(i *FlowAccessItem, target Node, limitations, evidence map[string]bool) {
	var facets map[string]jsontext.Value
	if json.Unmarshal(target.Attributes["facets"], &facets) != nil {
		return
	}
	var facet struct {
		AnalysisStatus string              `json:"analysisStatus"`
		Gaps           []string            `json:"gaps"`
		EvidenceIDs    []string            `json:"evidenceIds"`
		Freshness      *AssertionFreshness `json:"freshness"`
	}
	if raw, found := facets[i.FacetKey]; !found || json.Unmarshal(raw, &facet) != nil {
		i.Status = runtimeWorseStatus(i.Status, "unresolved")
		limitations["Selected target facet is unavailable"] = true
		return
	}
	if p.source == nil {
		i.Status = runtimeWorseStatus(i.Status, p.recordStatus(facet.EvidenceIDs, facet.Freshness, limitations))
	}
	for _, id := range p.sourceRecordEvidence("node", target.ID, &LineageValueRef{Kind: "column", NodeID: target.ID, FacetKey: i.FacetKey}, facet.EvidenceIDs) {
		evidence[id] = true
	}
	for _, gap := range facet.Gaps {
		limitations[target.ID+": "+gap] = true
	}
	if facet.AnalysisStatus != "complete" {
		limitations[fmt.Sprintf("%s: selected facet analysisStatus=%s", target.ID, facet.AnalysisStatus)] = true
	}
}

func runtimePolicyForSchema(schema string) string {
	if schema == ComposedSchemaVersion {
		return "runtime-flow-reachability-source6-v1"
	}
	if schema == EventsSchemaVersion {
		return EventsFlowTraversalPolicy
	}
	return runtimeTraversalPolicy
}

func sourceRuntimeEntrypoint(schema, kind string) bool {
	return runtimeEntrypointForSchema(schema, kind) || schema == ComposedSchemaVersion && (kind == "consumer" || kind == "job")
}
func (p *runtimeFlowProjection) sourceRecordStatus(typ, id string, ref *LineageValueRef, ids []string, fresh *AssertionFreshness, limitations map[string]bool) string {
	if p.effective != nil {
		proof, err := effectiveRecordProof(p.effective, typ, id, ref)
		if err != nil {
			limitations[err.Error()] = true
			return "unresolved"
		}
		for reason := range proof.reasons {
			limitations[reason] = true
		}
		return proof.status
	}
	if p.source == nil {
		return p.recordStatus(ids, fresh, limitations)
	}
	proof, err := sourceRecordProof(p.source, typ, id, ref)
	if err != nil {
		limitations[err.Error()] = true
		return "unresolved"
	}
	for reason := range proof.reasons {
		limitations[reason] = true
	}
	return proof.status
}

func (p *runtimeFlowProjection) sourceRecordEvidence(typ, id string, ref *LineageValueRef, legacy []string) []string {
	if p.effective != nil {
		proof, err := effectiveRecordProof(p.effective, typ, id, ref)
		if err != nil {
			return nil
		}
		return proof.evidenceIDs
	}
	if p.source == nil {
		return legacy
	}
	proof, err := sourceRecordProof(p.source, typ, id, ref)
	if err != nil {
		return nil
	}
	return proof.evidenceIDs
}

func (p *runtimeFlowProjection) readSchema() string {
	if p.schema != "" {
		return p.schema
	}
	return p.state.Revision.SchemaVersion
}

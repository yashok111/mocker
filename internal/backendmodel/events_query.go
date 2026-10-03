package backendmodel

import (
	"context"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
)

func (in *EventsQueryInput) UnmarshalJSON(raw []byte) error {
	m, err := relationalObject(raw)
	if err != nil {
		return invalid("body", "Expected a strict events query object")
	}
	if err = relationalFields(m, []string{"revisionId", "view"}, []string{"seedNodeId", "serviceId", "limit", "cursor"}); err != nil {
		return invalid("body", err.Error())
	}
	for key, v := range m {
		if key == "limit" {
			var n int
			if json.Unmarshal(v, &n) != nil || n < 1 || n > MaxPageSize {
				return invalid(key, "limit must be between 1 and 100")
			}
		} else {
			var value string
			if len(v) == 0 || v[0] != '"' || json.Unmarshal(v, &value) != nil {
				return invalid(key, "Expected a string")
			}
		}
	}
	type plain EventsQueryInput
	var value plain
	if json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)) != nil {
		return invalid("body", "Invalid events query")
	}
	*in = EventsQueryInput(value)
	in.present = map[string]bool{}
	for key := range m {
		in.present[key] = true
	}
	return in.validate()
}

func (in EventsQueryInput) validate() error {
	if !ValidID(in.RevisionID) {
		return invalid("revisionId", "A pinned revision UUID is required")
	}
	if !slices.Contains([]string{"routes", "jobs", "service_calls"}, in.View) {
		return invalid("view", "Select routes, jobs or service_calls")
	}
	if in.Limit < 0 || in.Limit > MaxPageSize {
		return invalid("limit", "limit must be between 1 and 100")
	}
	has := func(key, value string) bool { return in.present[key] || value != "" }
	if in.View == "routes" && has("serviceId", in.ServiceID) || in.View != "routes" && has("seedNodeId", in.SeedNodeID) {
		return invalid("selectors", "Selectors are not valid for this view")
	}
	if has("seedNodeId", in.SeedNodeID) && !ValidID(in.SeedNodeID) {
		return invalid("seedNodeId", "Use a canonical UUID selector")
	}
	if has("serviceId", in.ServiceID) && !ValidID(in.ServiceID) {
		return invalid("serviceId", "Use a canonical UUID selector")
	}
	return nil
}

func (r *Repo) QueryEvents(ctx context.Context, pid string, in EventsQueryInput) (*EventsPage, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	state, err := loadRevisionState(ctx, r.db.R, pid, in.RevisionID)
	if err != nil {
		return nil, err
	}
	page, err := projectEvents(ctx, state, in)
	if err != nil {
		return nil, err
	}
	coverage, err := r.RevisionCoverage(ctx, pid, in.RevisionID)
	if err != nil {
		return nil, err
	}
	page.Coverage = *coverage
	return page, nil
}

type eventsProjection struct {
	ctx           context.Context
	state         *RevisionState
	in            EventsQueryInput
	page          *EventsPage
	nodes         map[string]Node
	edges         map[string]Edge
	out           map[string][]Edge
	evidence      map[string]Evidence
	truncations   map[string]bool
	dispatchCache map[string][]EventsDispatch
	relatedCache  map[string][]EventsRelatedRoute
	emitCache     map[string]*EventsEmitContext
	items         []eventsOrderedItem
}
type eventsOrderedItem struct {
	key  string
	item EventsItem
}

func projectEvents(ctx context.Context, state *RevisionState, in EventsQueryInput) (*EventsPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if state == nil || state.Revision.ID != in.RevisionID {
		return nil, notFound()
	}
	source := primarySource(*state)
	if state.Revision.SchemaVersion != EventsSchemaVersion || source == nil || !sourceProfilesMatch(EventsSchemaVersion, source.Provider.Profiles) {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Event reads require a pinned events-service source5 revision"}
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultPageSize
	}
	page := &EventsPage{ProjectID: state.Revision.ProjectID, RevisionID: state.Revision.ID, SemanticHash: state.Revision.SemanticHash, Policy: EventsQueryPolicy, View: in.View, SeedNodeID: in.SeedNodeID, ServiceID: in.ServiceID, Items: []EventsItem{}, Complete: true, TotalEdgeCount: len(state.Edges), Coverage: RevisionCoverage{Coverage: state.Revision.Coverage, Snapshots: slices.Clone(state.Sources), Inventory: slices.Clone(state.Inventory), ReconciliationGaps: []string{}}, Limits: EventsQueryLimits{MaxExaminedEdges: EventsMaxExaminedEdges, MaxItems: EventsMaxItems, MaxAuxiliaryRecords: EventsMaxAuxiliaryRecords, MaxWitnessRecords: EventsMaxWitnessRecords, DefaultPageSize: DefaultPageSize, MaxPageSize: MaxPageSize, ScanPolicy: "complete-scan-admission"}, TruncationReasons: []string{}, Limitations: []string{"Configured source relationships do not verify broker delivery, deployment, job execution or transaction atomicity"}}
	page.Limitations = append(page.Limitations, state.Revision.Coverage.Gaps...)
	page.Limitations = append(page.Limitations, source.Provider.Limitations...)
	scope, after, err := eventsQueryCursor(page, in, limit)
	if err != nil {
		return nil, err
	}
	p := &eventsProjection{ctx: ctx, state: state, in: in, page: page, nodes: map[string]Node{}, edges: map[string]Edge{}, out: map[string][]Edge{}, evidence: map[string]Evidence{}, truncations: map[string]bool{}, dispatchCache: map[string][]EventsDispatch{}, relatedCache: map[string][]EventsRelatedRoute{}, emitCache: map[string]*EventsEmitContext{}}
	if err := p.indexNodes(); err != nil {
		return nil, err
	}
	if err := p.validateSelectors(); err != nil {
		return nil, err
	}
	// Do not construct adjacency or pairs for a scan that cannot be completed.
	// The empty diagnostic page says nothing about absence of configured routes.
	if len(state.Edges) > EventsMaxExaminedEdges {
		p.truncations["edge_limit"] = true
		return p.finish(limit, after, scope), nil
	}
	if err := p.indexRecords(); err != nil {
		return nil, err
	}
	switch in.View {
	case "routes":
		err = p.routes()
	case "jobs":
		err = p.jobs()
	case "service_calls":
		err = p.serviceCalls()
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.finish(limit, after, scope), nil
}
func (p *eventsProjection) indexNodes() error {
	for _, n := range p.state.Nodes {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		p.nodes[n.ID] = n
		if n.Freshness != nil && n.Freshness.Status == "stale" {
			p.page.Coverage.StaleCounts.Nodes++
		}
	}
	return nil
}
func (p *eventsProjection) validateSelectors() error {
	if p.in.SeedNodeID != "" {
		n, ok := p.nodes[p.in.SeedNodeID]
		if !ok {
			return notFound()
		}
		if !slices.Contains([]string{"message", "channel", "consumer"}, n.Kind) && (n.Kind != "flow_step" || runtimeAttributeString(n.Attributes, "stepKind") != "emit") {
			return invalid("seedNodeId", "Select an emit step, message, channel or consumer")
		}
	}
	if p.in.ServiceID != "" {
		n, ok := p.nodes[p.in.ServiceID]
		if !ok {
			return notFound()
		}
		if n.Kind != "service" {
			return invalid("serviceId", "Select a service")
		}
	}
	return nil
}
func (p *eventsProjection) indexRecords() error {
	for _, e := range p.state.Edges {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		p.page.ExaminedEdgeCount++
		p.edges[e.ID] = e
		p.out[e.From] = append(p.out[e.From], e)
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			p.page.Coverage.StaleCounts.Edges++
		}
	}
	for _, e := range p.state.Evidence {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		p.evidence[e.ID] = e
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			p.page.Coverage.StaleCounts.Evidence++
		}
	}
	for _, edges := range p.out {
		slices.SortFunc(edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	}
	return nil
}
func (p *eventsProjection) finish(limit int, after, scope string) *EventsPage {
	p.page.TruncationReasons = runtimeSortedKeys(p.truncations)
	p.page.Truncated = len(p.page.TruncationReasons) > 0
	p.page.Complete = !p.page.Truncated
	if p.page.Truncated {
		p.page.Limitations = append(p.page.Limitations, "Projection budget exhausted; omitted paths and dependencies have not been proven absent")
	}
	slices.Sort(p.page.Limitations)
	p.page.Limitations = slices.Compact(p.page.Limitations)
	slices.SortFunc(p.items, func(a, b eventsOrderedItem) int { return strings.Compare(a.key, b.key) })
	lastKey := ""
	for _, i := range p.items {
		if i.key <= after {
			continue
		}
		if len(p.page.Items) == limit {
			p.page.NextCursor = encodeGraphPage("events", p.page.ProjectID, scope, lastKey)
			break
		}
		p.page.Items = append(p.page.Items, i.item)
		lastKey = i.key
	}
	return p.page
}
func (p *eventsProjection) admit() bool {
	if p.page.ConstructedItemCount == EventsMaxItems {
		p.truncations["item_limit"] = true
		return false
	}
	p.page.ConstructedItemCount++
	return true
}
func (p *eventsProjection) auxiliary() bool {
	if p.page.AuxiliaryRecordCount == EventsMaxAuxiliaryRecords {
		p.truncations["auxiliary_limit"] = true
		return false
	}
	p.page.AuxiliaryRecordCount++
	return true
}
func (p *eventsProjection) service(id string) string {
	for range EventsMaxWitnessRecords {
		n, ok := p.nodes[id]
		if !ok {
			return ""
		}
		if n.Kind == "service" {
			return id
		}
		if n.ParentID == nil {
			return ""
		}
		id = *n.ParentID
	}
	p.truncations["ownership_depth"] = true
	return ""
}
func (p *eventsProjection) nodeIDs() []string { return slices.Sorted(maps.Keys(p.nodes)) }
func (p *eventsProjection) edgeIDs() []string { return slices.Sorted(maps.Keys(p.edges)) }

func eventsOrderingKey(view, key string) bool {
	if ValidID(key) {
		return view != "routes"
	}
	left, right, ok := strings.Cut(key, "/")
	if !ok {
		return false
	}
	if left == "node" {
		return view != "jobs" && ValidID(right)
	}
	return view == "routes" && (left == "" || ValidID(left)) && (right == "" || ValidID(right)) && (left != "" || right != "")
}

func eventsQueryCursor(page *EventsPage, in EventsQueryInput, limit int) (string, string, error) {
	scope, err := requestDigest(struct {
		ProjectID, RevisionID, SemanticHash, Policy, View, SeedNodeID, ServiceID string
		Limit                                                                    int
	}{page.ProjectID, page.RevisionID, page.SemanticHash, page.Policy, in.View, in.SeedNodeID, in.ServiceID, limit})
	if err != nil {
		return "", "", err
	}
	_, after, err := decodeGraphPage(limit, in.Cursor, "events", page.ProjectID, scope, false)
	if err != nil {
		return "", "", err
	}
	if after != "" && !eventsOrderingKey(in.View, after) {
		return "", "", invalid("cursor", "Invalid events ordering key")
	}
	return scope, after, nil
}

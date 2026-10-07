package backendmodel

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	lineageMaxVisitedValues       = 5000
	lineageMaxExaminedMappings    = 5000
	lineageMaxReferenceIncidences = 20000
)

// LineageQueryInput always selects an immutable source4 revision.
type LineageQueryInput struct {
	Proposal        *ProposalReadTarget        `json:"proposal,omitzero"`
	ChangeProposal  *ProposalReadTarget        `json:"changeProposal,omitzero"`
	ImportCandidate *ImportCandidateReadTarget `json:"importCandidate,omitzero"`
	RevisionID      string                     `json:"revisionId,omitempty"`
	Seed            LineageValueRef            `json:"seed"`
	Direction       string                     `json:"direction"`
	MaxDepth        int                        `json:"maxDepth,omitzero"`
	Limit           int                        `json:"limit,omitzero"`
	Cursor          string                     `json:"cursor,omitempty"`
}
type LineageItem struct {
	Mapping           Node              `json:"mapping"`
	Via               LineageValueRef   `json:"via"`
	Depth             int               `json:"depth"`
	WitnessMappingIDs []string          `json:"witnessMappingIds"`
	Status            string            `json:"status"`
	Expansion         string            `json:"expansion"`
	ExpandedValues    []LineageValueRef `json:"expandedValues"`
	Reasons           []string          `json:"reasons"`
	RequiresReview    bool              `json:"requiresReview"`
}
type LineagePage struct {
	Target               *BackendReadTarget  `json:"target,omitzero"`
	Pins                 *EffectiveGraphPins `json:"pins,omitzero"`
	ProjectID            string              `json:"projectId"`
	RevisionID           string              `json:"revisionId"`
	SemanticHash         string              `json:"semanticHash"`
	Policy               string              `json:"policy"`
	Seed                 LineageValueRef     `json:"seed"`
	Direction            string              `json:"direction"`
	Items                []LineageItem       `json:"items"`
	NextCursor           string              `json:"nextCursor"`
	Coverage             RevisionCoverage    `json:"coverage"`
	Truncated            bool                `json:"truncated"`
	TruncationReasons    []string            `json:"truncationReasons"`
	Limitations          []string            `json:"limitations"`
	VisitedValueCount    int                 `json:"visitedValueCount"`
	ExaminedMappingCount int                 `json:"examinedMappingCount"`
}

func (in *LineageQueryInput) UnmarshalJSON(raw []byte) error {
	if advancedReadQuery(raw) {
		return decodeAdvancedReadQuery(raw, in)
	}
	m, err := relationalObject(raw)
	if err != nil {
		return invalid("body", "Expected a strict lineage query object")
	}
	if err = relationalFields(m, []string{"revisionId", "seed", "direction"}, []string{"maxDepth", "limit", "cursor"}); err != nil {
		return invalid("body", err.Error())
	}
	for key, v := range m {
		switch key {
		case "seed":
			if err := validateRepresentationLineageRef(v, true); err != nil {
				return invalid("seed", err.Error())
			}
		case "maxDepth", "limit":
			var n int
			upper := 32
			if key == "limit" {
				upper = 100
			}
			if json.Unmarshal(v, &n) != nil || n < 1 || n > upper {
				return invalid(key, "Integer outside lineage query range")
			}
		default:
			var s string
			if len(v) == 0 || v[0] != '"' || json.Unmarshal(v, &s) != nil {
				return invalid(key, "Expected a string")
			}
		}
	}
	type plain LineageQueryInput
	var value plain
	if err = json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return invalid("body", "Invalid lineage query")
	}
	*in = LineageQueryInput(value)
	return in.validate()
}
func (in LineageQueryInput) validate() error {
	if in.Proposal != nil || in.ChangeProposal != nil || in.ImportCandidate != nil {
		target := graphTarget(in.RevisionID, in.Proposal, in.ChangeProposal, in.ImportCandidate)
		if err := target.Validate(); err != nil {
			return err
		}
		if err := rejectStagedView(target); err != nil {
			return err
		}
	}
	if in.Proposal == nil && in.ChangeProposal == nil && !ValidID(in.RevisionID) {
		return invalid("revisionId", "A pinned revision UUID is required")
	}
	if in.Direction != "forward" && in.Direction != "reverse" {
		return invalid("direction", "Select forward or reverse")
	}
	if in.MaxDepth < 0 || in.MaxDepth > 32 {
		return invalid("maxDepth", "maxDepth must be between 1 and 32")
	}
	if in.Limit < 0 || in.Limit > 100 {
		return invalid("limit", "limit must be between 1 and 100")
	}
	raw, err := json.Marshal(in.Seed)
	if err != nil {
		return invalid("seed", "Invalid value reference")
	}
	if err = validateRepresentationLineageRef(raw, true); err != nil {
		return invalid("seed", err.Error())
	}
	return nil
}
func (r *Repo) QueryLineage(ctx context.Context, projectID string, in LineageQueryInput) (*LineagePage, error) {
	if in.Proposal != nil || in.ChangeProposal != nil || in.ImportCandidate != nil {
		return r.queryEffectiveLineage(ctx, projectID, in)
	}
	if in.RevisionID != "" && in.Proposal == nil && in.ChangeProposal == nil {
		revision, err := r.Revision(ctx, projectID, in.RevisionID)
		if err != nil {
			return nil, err
		}
		if revision.SchemaVersion == ComposedSchemaVersion {
			return r.queryEffectiveLineage(ctx, projectID, in)
		}
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	state, err := loadRevisionState(ctx, r.db.R, projectID, in.RevisionID)
	if err != nil {
		return nil, err
	}
	var page *LineagePage
	if state.Revision.SchemaVersion == ComposedSchemaVersion {
		graph, e := r.ResolveSourceGraph(ctx, projectID, in.RevisionID)
		if e != nil {
			return nil, e
		}
		page, err = projectSourceLineage(ctx, graph, in)
	} else {
		page, err = projectLineage(ctx, state, in)
	}
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

type lineageIndexedMapping struct {
	node  Node
	attrs LineageMappingAttributes
}
type lineageReach struct {
	value   LineageValueRef
	witness []string
	status  string
	reasons map[string]bool
}
type lineageProof struct {
	evidenceIDs []string
	claims      []BaseAssertionRef
	status      string
	reasons     map[string]bool
	boundary    bool
}

func (p *lineageProof) add(reason string, boundary bool) {
	p.reasons[reason] = true
	p.boundary = p.boundary || boundary
}

// projectLineage never resolves a current head and never executes imported text.
func projectLineage(ctx context.Context, state *RevisionState, in LineageQueryInput) (*LineagePage, error) {
	return projectLineageWithSource(ctx, state, nil, in)
}
func projectSourceLineage(ctx context.Context, source *SourceGraphSnapshot, in LineageQueryInput) (*LineagePage, error) {
	if source == nil || source.SourceVector == nil {
		return nil, semantic("sourceVector", "Composed lineage requires full source snapshot")
	}
	return projectLineageWithSource(ctx, &source.State, source, in)
}
func projectLineageWithSource(ctx context.Context, state *RevisionState, sourceGraph *SourceGraphSnapshot, in LineageQueryInput) (*LineagePage, error) {
	return projectLineageWithEffective(ctx, state, sourceGraph, nil, in)
}
func projectLineageWithEffective(ctx context.Context, state *RevisionState, sourceGraph *SourceGraphSnapshot, effective *EffectiveGraphSnapshot, in LineageQueryInput) (*LineagePage, error) {
	schema, hash := state.Revision.SchemaVersion, state.Revision.SemanticHash
	if effective != nil {
		schema = effective.Pins.StructuralSchemaVersion
		hash = effective.Pins.EffectiveSemanticHash
	}
	if err := checkLineageRequest(ctx, state, sourceGraph, effective, &in, schema); err != nil {
		return nil, err
	}
	page, scope, err := newLineagePage(state, sourceGraph, effective, in, schema, hash)
	if err != nil {
		return nil, err
	}
	_, after, err := decodeGraphPage(in.Limit, in.Cursor, "lineage", page.ProjectID, scope, false)
	if err != nil {
		return nil, err
	}
	limitations := lineageBaseLimitations(state)
	truncations := map[string]bool{}
	idx, err := buildLineageIndex(ctx, state, sourceGraph, in, schema, page, limitations, truncations)
	if err != nil {
		return nil, err
	}
	walk := &lineageWalk{
		ctx: ctx, in: in, page: page, idx: idx, effective: effective, sourceGraph: sourceGraph, truncations: truncations,
		proofReader: lineageProofReader{effective: effective, source: sourceGraph, ctx: ctx, nodes: idx.nodes, evidence: idx.evidence, contains: idx.contains, edges: idx.edges, handles: idx.handles, schema: schema},
		visited:     map[LineageValueRef]bool{in.Seed: true},
		emitted:     map[string]bool{},
		queue:       []lineageReach{{value: in.Seed, status: "explicit", reasons: map[string]bool{}}},
	}
	if err := walk.run(); err != nil {
		return nil, err
	}
	return finishLineagePage(page, len(walk.visited), truncations, limitations, after, scope, in.Limit)
}

// checkLineageRequest rejects a request this revision cannot answer and
// fills the depth and page-size defaults into in.
func checkLineageRequest(ctx context.Context, state *RevisionState, sourceGraph *SourceGraphSnapshot, effective *EffectiveGraphSnapshot, in *LineageQueryInput, schema string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	if state == nil || state.Revision.ID != in.RevisionID {
		return notFound()
	}
	source := primarySource(*state)
	if effective == nil && sourceGraph == nil && (!isLineageSchema(schema) || source == nil || !sourceProfilesMatch(schema, source.Provider.Profiles)) {
		return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Lineage reads require a pinned source4 or source5 revision"}
	}
	rawSeed, err := json.Marshal(in.Seed)
	if err != nil {
		return err
	}
	if err := lineageRefValidator(schema, sourceGraph, effective)(rawSeed, true); err != nil {
		return invalid("seed", err.Error())
	}
	if in.MaxDepth == 0 {
		in.MaxDepth = 8
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	return nil
}

// lineageRefValidator picks the seed grammar: a representation read accepts
// every profile's refs, a pinned revision only its own schema's.
func lineageRefValidator(schema string, sourceGraph *SourceGraphSnapshot, effective *EffectiveGraphSnapshot) func(jsontext.Value, bool) error {
	if effective != nil || sourceGraph != nil {
		return validateRepresentationLineageRef
	}
	if schema == EventsSchemaVersion {
		return validateEventsLineageRef
	}
	return validateLineageRef
}

// newLineagePage builds the empty page and the cursor scope digest, which
// binds every input that changes the bounded result.
func newLineagePage(state *RevisionState, sourceGraph *SourceGraphSnapshot, effective *EffectiveGraphSnapshot, in LineageQueryInput, schema, hash string) (*LineagePage, string, error) {
	page := &LineagePage{ProjectID: state.Revision.ProjectID, RevisionID: in.RevisionID, SemanticHash: hash, Policy: lineagePolicyForSchema(schema), Seed: in.Seed, Direction: in.Direction, Items: []LineageItem{}, TruncationReasons: []string{}, Limitations: []string{}, Coverage: RevisionCoverage{Coverage: state.Revision.Coverage, Snapshots: slices.Clone(state.Sources), Inventory: slices.Clone(state.Inventory), ReconciliationGaps: []string{}}}
	if sourceGraph != nil {
		page.Coverage.Source = sourceVectorReadContext(sourceGraph)
		page.Policy = "field-lineage-traversal-source6-v1"
	}
	scope, err := requestDigest(struct {
		Project, Revision, Hash, Policy string
		Seed                            LineageValueRef
		Direction                       string
		Depth, Limit                    int
	}{page.ProjectID, page.RevisionID, page.SemanticHash, page.Policy, in.Seed, in.Direction, in.MaxDepth, in.Limit})
	if err != nil {
		return nil, "", err
	}
	if effective != nil {
		page.Target = new(effective.Target)
		page.Pins = new(effective.Pins)
		page.SemanticHash = effective.Pins.EffectiveSemanticHash
		if effective.Target.ChangeProposal != nil {
			page.Policy = "effective-field-lineage-v1"
		}
		scope, err = requestDigest(struct {
			Scope string
			Pins  EffectiveGraphPins
		}{scope, effective.Pins})
		if err != nil {
			return nil, "", err
		}
	}
	return page, scope, nil
}

// lineageBaseLimitations collects every limitation the revision itself
// declares, before the walk adds its own.
func lineageBaseLimitations(state *RevisionState) map[string]bool {
	limitations := map[string]bool{}
	for _, gap := range state.Revision.Coverage.Gaps {
		limitations[gap] = true
	}
	for _, src := range state.Sources {
		for _, l := range src.Provider.Limitations {
			limitations[l] = true
		}
	}
	for _, inv := range state.Inventory {
		for _, gap := range inv.Gaps {
			limitations[gap] = true
		}
		if inv.Reason != "" {
			limitations[inv.Reason] = true
		}
	}
	limitations["Imported mappings describe possible structural dependencies; witnesses do not prove execution or branch feasibility"] = true
	return limitations
}

// lineageIndex is the revision's graph keyed the ways the walk and its proof
// reader look records up.
type lineageIndex struct {
	nodes    map[string]Node
	evidence map[string]Evidence
	index    map[LineageValueRef][]lineageIndexedMapping
	contains map[string][]Edge
	edges    map[string]Edge
	handles  map[string][]Edge
}

func buildLineageIndex(ctx context.Context, state *RevisionState, sourceGraph *SourceGraphSnapshot, in LineageQueryInput, schema string, page *LineagePage, limitations, truncations map[string]bool) (*lineageIndex, error) {
	idx := &lineageIndex{nodes: map[string]Node{}, evidence: map[string]Evidence{}, index: map[LineageValueRef][]lineageIndexedMapping{}, contains: map[string][]Edge{}, edges: map[string]Edge{}, handles: map[string][]Edge{}}
	for _, e := range state.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Kind == "handles" {
			idx.handles[e.From] = append(idx.handles[e.From], e)
		}
		idx.edges[e.ID] = e
	}
	for _, n := range state.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		idx.nodes[n.ID] = n
		if n.Freshness != nil && n.Freshness.Status == "stale" {
			page.Coverage.StaleCounts.Nodes++
		}
	}
	if err := validateLineageValueTargetForSchema(in.Seed, idx.nodes, idx.edges, schema); err != nil {
		return nil, err
	}
	if err := idx.indexEvidenceAndContainment(ctx, state, page, limitations); err != nil {
		return nil, err
	}
	if err := idx.indexMappings(ctx, state, sourceGraph, in, schema, truncations); err != nil {
		return nil, err
	}
	for _, mappings := range idx.index {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		slices.SortFunc(mappings, func(a, b lineageIndexedMapping) int { return strings.Compare(a.node.ID, b.node.ID) })
	}
	return idx, nil
}

func (idx *lineageIndex) indexEvidenceAndContainment(ctx context.Context, state *RevisionState, page *LineagePage, limitations map[string]bool) error {
	for _, e := range state.Evidence {
		if err := ctx.Err(); err != nil {
			return err
		}
		idx.evidence[e.ID] = e
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			page.Coverage.StaleCounts.Evidence++
		}
	}
	for _, e := range state.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Kind == "contains" {
			idx.contains[e.To] = append(idx.contains[e.To], e)
		}
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			page.Coverage.StaleCounts.Edges++
		}
		if runtimeAttributeString(e.Attributes, "columnScope") == "unknown" {
			limitations["Unknown table column access "+e.ID+" cannot be expanded into field references"] = true
		}
	}
	return nil
}

// indexMappings keys every field mapping by the values the walk reaches it
// from. Events and source6 reads bound the index itself; the others are
// bounded only by the walk.
func (idx *lineageIndex) indexMappings(ctx context.Context, state *RevisionState, sourceGraph *SourceGraphSnapshot, in LineageQueryInput, schema string, truncations map[string]bool) error {
	indexedMappings, indexedIncidences := 0, 0
	for _, n := range state.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n.Kind != "field_mapping" {
			continue
		}
		if schema == EventsSchemaVersion || sourceGraph != nil {
			if indexedMappings == lineageMaxExaminedMappings {
				truncations["mapping_limit"] = true
				break
			}
			refs, err := relationalArray(n.Attributes["sources"], MaxLineageSources)
			if err != nil {
				return err
			}
			if indexedIncidences+len(refs)+1 > lineageMaxReferenceIncidences {
				truncations["reference_limit"] = true
				break
			}
			indexedMappings++
			indexedIncidences += len(refs) + 1
		}
		attrs, err := decodeLineageMappingForSchema(n.Attributes, schema)
		if err != nil {
			return err
		}
		m := lineageIndexedMapping{n, attrs}
		refs := attrs.Sources
		if in.Direction == "reverse" {
			refs = []LineageValueRef{attrs.Destination}
		}
		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return err
			}
			idx.index[ref] = append(idx.index[ref], m)
		}
	}
	return nil
}

// lineageWalk is the breadth-first expansion from the seed, with the
// budgets that stop it.
type lineageWalk struct {
	ctx         context.Context
	in          LineageQueryInput
	page        *LineagePage
	idx         *lineageIndex
	effective   *EffectiveGraphSnapshot
	sourceGraph *SourceGraphSnapshot
	proofReader lineageProofReader
	truncations map[string]bool
	visited     map[LineageValueRef]bool
	emitted     map[string]bool
	queue       []lineageReach
	incidences  int
	stop        bool
}

func (t *lineageWalk) run() error {
	for head := 0; head < len(t.queue) && !t.stop; head++ {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		w := t.queue[head]
		for _, m := range t.idx.index[w.value] {
			if err := t.ctx.Err(); err != nil {
				return err
			}
			if t.emitted[m.node.ID] {
				continue
			}
			if !t.admit(m) {
				break
			}
			if err := t.expand(w, m); err != nil {
				return err
			}
			if t.stop {
				break
			}
		}
	}
	return nil
}

// admit charges one mapping against the examined and incidence budgets;
// false stops the walk, the exhausted budget recorded as a truncation.
func (t *lineageWalk) admit(m lineageIndexedMapping) bool {
	if t.page.ExaminedMappingCount == lineageMaxExaminedMappings {
		t.truncations["mapping_limit"] = true
		t.stop = true
		return false
	}
	t.page.ExaminedMappingCount++
	count := len(m.attrs.Sources) + 1
	if t.incidences+count > lineageMaxReferenceIncidences {
		t.truncations["reference_limit"] = true
		t.stop = true
		return false
	}
	t.incidences += count
	return true
}

// expand emits mapping m reached through w and queues the values it leads to.
func (t *lineageWalk) expand(w lineageReach, m lineageIndexedMapping) error {
	proof, err := t.proofReader.mapping(m)
	if err != nil {
		return err
	}
	proof.status = runtimeWorseStatus(w.status, proof.status)
	for reason := range w.reasons {
		proof.reasons[reason] = true
	}
	depth := len(w.witness) + 1
	item := LineageItem{Mapping: m.node, Via: w.value, Depth: depth, WitnessMappingIDs: append(slices.Clone(w.witness), m.node.ID), Status: proof.status, Expansion: "expanded", ExpandedValues: []LineageValueRef{}, Reasons: []string{}}
	if err := t.readMapping(&item); err != nil {
		return err
	}
	t.emitted[m.node.ID] = true
	next := []LineageValueRef{m.attrs.Destination}
	if t.in.Direction == "reverse" {
		next = slices.Clone(m.attrs.Sources)
	}
	t.applyLimits(&proof, next, depth)
	if proof.boundary {
		item.Expansion = "boundary"
	} else {
		item.ExpandedValues = slices.Clone(next)
		if err := t.enqueue(next, item, proof); err != nil {
			return err
		}
	}
	item.Reasons = runtimeSortedKeys(proof.reasons)
	item.RequiresReview = proof.boundary || item.Status != "explicit" || len(item.Reasons) > 0
	t.page.Items = append(t.page.Items, item)
	return nil
}

// readMapping replaces the mapping record with the representation the read
// is pinned to: the effective overlay, or the source6 claim and its proof.
func (t *lineageWalk) readMapping(item *LineageItem) error {
	if t.effective != nil {
		item.Mapping = effectiveNodeRecord(t.effective, item.Mapping)
	} else if t.sourceGraph != nil {
		item.Mapping.Source = sourceRecordReadContext(t.sourceGraph, "node", item.Mapping.ID)
		mappingProof, err := sourceRecordProof(t.sourceGraph, "node", item.Mapping.ID, nil)
		if err != nil {
			return err
		}
		item.Mapping.EvidenceIDs = slices.Clone(mappingProof.evidenceIDs)
	}
	return nil
}

// applyLimits turns the mapping into a boundary when the depth or the
// visited-value budget would be exceeded by expanding it.
func (t *lineageWalk) applyLimits(proof *lineageProof, next []LineageValueRef, depth int) {
	if !proof.boundary && depth == t.in.MaxDepth && t.reachesUnemitted(next) {
		proof.add("depth_limit", true)
		t.truncations["depth_limit"] = true
	}
	if !proof.boundary {
		additional := 0
		for _, ref := range next {
			if !t.visited[ref] {
				additional++
			}
		}
		if len(t.visited)+additional > lineageMaxVisitedValues {
			proof.add("value_limit", true)
			t.truncations["value_limit"] = true
			t.stop = true
		}
	}
}

// reachesUnemitted reports whether an unvisited next value leads to a
// mapping not yet emitted, i.e. whether stopping here hides something.
func (t *lineageWalk) reachesUnemitted(next []LineageValueRef) bool {
	for _, ref := range next {
		if t.visited[ref] {
			continue
		}
		for _, further := range t.idx.index[ref] {
			if !t.emitted[further.node.ID] {
				return true
			}
		}
	}
	return false
}

func (t *lineageWalk) enqueue(next []LineageValueRef, item LineageItem, proof lineageProof) error {
	for _, ref := range next {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		if t.visited[ref] {
			continue
		}
		t.visited[ref] = true
		t.queue = append(t.queue, lineageReach{ref, item.WitnessMappingIDs, item.Status, maps.Clone(proof.reasons)})
	}
	return nil
}

// finishLineagePage records the walk's outcome, orders the bounded result
// and cuts the requested page out of it.
func finishLineagePage(page *LineagePage, visited int, truncations, limitations map[string]bool, after, scope string, limit int) (*LineagePage, error) {
	page.VisitedValueCount = visited
	page.TruncationReasons = runtimeSortedKeys(truncations)
	page.Truncated = len(truncations) > 0
	if len(page.Items) == 0 {
		limitations["No imported mapping in this scope; absence of dependencies is not established"] = true
	}
	page.Limitations = runtimeSortedKeys(limitations)
	slices.SortFunc(page.Items, func(a, b LineageItem) int {
		return cmp.Or(cmp.Compare(a.Depth, b.Depth), strings.Compare(a.Mapping.ID, b.Mapping.ID))
	})
	start := 0
	if after != "" {
		at := slices.IndexFunc(page.Items, func(i LineageItem) bool { return lineageItemKey(i) == after })
		if at < 0 {
			return nil, invalid("cursor", "Cursor ordering key is not in the bounded result")
		}
		start = at + 1
	}
	end := min(start+limit, len(page.Items))
	if end < len(page.Items) {
		page.NextCursor = encodeGraphPage("lineage", page.ProjectID, scope, lineageItemKey(page.Items[end-1]))
	}
	page.Items = page.Items[start:end]
	return page, nil
}
func lineageItemKey(i LineageItem) string { return fmt.Sprintf("%02d/%s", i.Depth, i.Mapping.ID) }

type lineageProofReader struct {
	effective *EffectiveGraphSnapshot
	source    *SourceGraphSnapshot
	ctx       context.Context
	nodes     map[string]Node
	evidence  map[string]Evidence
	contains  map[string][]Edge
	edges     map[string]Edge
	schema    string
	handles   map[string][]Edge
}

func (r lineageProofReader) record(p *lineageProof, ids []string, fresh *AssertionFreshness) error {
	if fresh != nil && fresh.Status == "stale" {
		p.status = runtimeWorseStatus(p.status, "stale")
		p.add("stale_assertion", true)
		for _, reason := range fresh.Reasons {
			p.add(reason, true)
		}
	}
	if len(ids) == 0 {
		p.status = runtimeWorseStatus(p.status, "inferred")
		p.add("missing_explicit_proof", false)
	}
	for _, id := range ids {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		e, ok := r.evidence[id]
		if !ok {
			p.status = runtimeWorseStatus(p.status, "unresolved")
			p.add("unresolved_evidence", true)
			continue
		}
		p.status = runtimeWorseStatus(p.status, e.Status)
		switch e.Status {
		case "unresolved":
			p.add("unresolved_evidence", true)
		case "stale":
			p.add("stale_evidence", true)
		case "explicit":
		default:
			p.add("inferred_evidence", false)
		}
		if e.Freshness != nil && e.Freshness.Status == "stale" {
			p.status = runtimeWorseStatus(p.status, "stale")
			p.add("stale_evidence", true)
		}
	}
	return nil
}
func lineageAnalysis(p *lineageProof, attrs map[string]jsontext.Value) {
	for _, gap := range runtimeAttributeStrings(attrs, "gaps") {
		p.add(gap, false)
	}
	switch runtimeAttributeString(attrs, "analysisStatus") {
	case "unsupported":
		p.add("unsupported_analysis", true)
	case "partial":
		p.add("partial_analysis", false)
	}
}
func (r lineageProofReader) node(p *lineageProof, n Node, ref *LineageValueRef) error {
	if r.effective != nil {
		proof, err := effectiveRecordProof(r.effective, "node", n.ID, ref)
		if err != nil {
			return err
		}
		mergeSourceProof(p, proof)
		lineageAnalysis(p, n.Attributes)
		return nil
	}
	if r.source != nil {
		proof, err := sourceRecordProof(r.source, "node", n.ID, ref)
		if err != nil {
			return err
		}
		mergeSourceProof(p, proof)
		lineageAnalysis(p, n.Attributes)
		return nil
	}
	ids := n.EvidenceIDs
	if ref != nil && ref.Kind == "column" {
		ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
			e, ok := r.evidence[id]
			if !ok || e.PropertyPath == nil {
				return false
			}
			suffix, scoped := strings.CutPrefix(*e.PropertyPath, "/attributes/facets/")
			if !scoped {
				return false
			}
			key, _, _ := strings.Cut(suffix, "/")
			return key != escapeRelationalPointer(ref.FacetKey)
		})
	}
	if err := r.record(p, ids, n.Freshness); err != nil {
		return err
	}
	lineageAnalysis(p, n.Attributes)
	if ref != nil && ref.Kind == "column" {
		raw, f, ok := lineageColumnFacet(n, ref.FacetKey)
		if !ok {
			p.status = runtimeWorseStatus(p.status, "unresolved")
			p.add("unresolved_facet", true)
			return nil
		}
		if err := r.record(p, f.EvidenceIDs, f.Freshness); err != nil {
			return err
		}
		var attrs map[string]jsontext.Value
		if json.Unmarshal(raw, &attrs) == nil {
			lineageAnalysis(p, attrs)
		}
	}
	return nil
}

// lineageFacetProof is the provenance a column facet carries for itself.
type lineageFacetProof struct {
	EvidenceIDs []string            `json:"evidenceIds"`
	Freshness   *AssertionFreshness `json:"freshness"`
}

// lineageColumnFacet reports a column's facet body and its provenance, and
// false when either cannot be read. An unreadable facet degrades the proof to
// "unresolved_facet"; it is a finding about the path, never a query failure,
// which is why the decode errors are folded into a bool here.
func lineageColumnFacet(n Node, key string) (jsontext.Value, lineageFacetProof, bool) {
	var f lineageFacetProof
	facets, _, err := relationalFacetObject(n.Kind, n.Attributes)
	if err != nil {
		return nil, f, false
	}
	if json.Unmarshal(facets[key], &f) != nil {
		return nil, f, false
	}
	return facets[key], f, true
}
func (r lineageProofReader) owner(p *lineageProof, n Node) error {
	if n.ParentID == nil {
		return nil
	}
	owner, ok := r.nodes[*n.ParentID]
	if !ok {
		p.status = runtimeWorseStatus(p.status, "unresolved")
		p.add("unresolved_owner", true)
		return nil
	}
	if err := r.node(p, owner, nil); err != nil {
		return err
	}
	for _, edge := range r.contains[n.ID] {
		if err := r.edge(p, edge); err != nil {
			return err
		}
	}
	return nil
}
func (r lineageProofReader) mapping(m lineageIndexedMapping) (lineageProof, error) {
	p := lineageProof{status: "explicit", reasons: map[string]bool{}}
	if err := r.node(&p, m.node, nil); err != nil {
		return p, err
	}
	if err := r.owner(&p, m.node); err != nil {
		return p, err
	}
	if m.attrs.Transform.Kind == "unknown_transform" {
		p.add("unknown_transform", true)
	}
	for _, ref := range append(slices.Clone(m.attrs.Sources), m.attrs.Destination) {
		if err := r.ctx.Err(); err != nil {
			return p, err
		}
		if err := validateLineageValueTargetForSchema(ref, r.nodes, r.edges, r.schema); err != nil {
			p.status = runtimeWorseStatus(p.status, "unresolved")
			p.add("unresolved_endpoint", true)
			continue
		}
		if ref.Kind == "event_field" {
			if err := r.eventRef(&p, ref); err != nil {
				return p, err
			}
		}
		n := r.nodes[ref.NodeID]
		if err := r.node(&p, n, &ref); err != nil {
			return p, err
		}
		if err := r.owner(&p, n); err != nil {
			return p, err
		}
	}
	if (r.schema == EventsSchemaVersion || r.source != nil) && contextualLineageMapping(m.node.Kind, m.node.Attributes, false) {
		if err := validateEventsLineageMapping(m.node, m.attrs, r.nodes, r.edges, r.handles); err != nil {
			p.status = runtimeWorseStatus(p.status, "unresolved")
			p.add("invalid_event_tuple", true)
		}
		if err := r.eventMapping(&p, m); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (r lineageProofReader) edge(p *lineageProof, e Edge) error {
	if r.effective != nil {
		proof, err := effectiveRecordProof(r.effective, "edge", e.ID, nil)
		if err != nil {
			return err
		}
		mergeSourceProof(p, proof)
		return nil
	}
	if r.source == nil {
		return r.record(p, e.EvidenceIDs, e.Freshness)
	}
	proof, err := sourceRecordProof(r.source, "edge", e.ID, nil)
	if err != nil {
		return err
	}
	mergeSourceProof(p, proof)
	return nil
}

package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
)

func lineageReferences(kind string, a map[string]jsontext.Value, edge, persisted bool) ([]relationalReference, error) {
	refs := []relationalReference{}
	if !lineageSubject(kind, edge) {
		return refs, nil
	}
	if err := validateLineageAttributes(kind, a, edge, persisted); err != nil {
		return nil, err
	}
	if kind != "field_mapping" {
		return refs, nil
	}
	sources, _ := relationalArray(a["sources"], MaxLineageSources)
	key := "nodeKey"
	if persisted {
		key = "nodeId"
	}
	for i, raw := range append(sources, a["destination"]) {
		m, _ := relationalObject(raw)
		path := "/attributes/destination/" + key
		if i < len(sources) {
			path = fmt.Sprintf("/attributes/sources/%d/%s", i, key)
		}
		ref := relationalReference{Path: path, Kind: runtimeString(m["kind"]), Key: runtimeString(m[key])}
		if persisted {
			ref.ID, ref.Key = ref.Key, ""
		}
		refs = append(refs, ref)
	}
	return refs, nil
}
func resolveLineageAttributes(kind string, a map[string]jsontext.Value, resolve func(string, string, string) string) (map[string]jsontext.Value, error) {
	if kind != "field_mapping" {
		return a, nil
	}
	if err := validateLineageAttributes(kind, a, false, false); err != nil {
		return nil, err
	}
	out := maps.Clone(a)
	sources, _ := relationalArray(a["sources"], MaxLineageSources)
	convert := func(raw jsontext.Value, path string) (jsontext.Value, error) {
		m, err := relationalObject(raw)
		if err != nil {
			return nil, err
		}
		m["nodeId"], err = json.Marshal(resolve("node", runtimeString(m["nodeKey"]), path+"/nodeId"))
		if err != nil {
			return nil, err
		}
		delete(m, "nodeKey")
		return json.Marshal(m)
	}
	for i := range sources {
		var err error
		sources[i], err = convert(sources[i], fmt.Sprintf("/attributes/sources/%d", i))
		if err != nil {
			return nil, err
		}
	}
	var err error
	out["sources"], err = json.Marshal(sources)
	if err != nil {
		return nil, err
	}
	out["destination"], err = convert(a["destination"], "/attributes/destination")
	return out, err
}
func lineageContains(from, to Node) (bool, bool) {
	switch to.Kind {
	case "api_field":
		return from.Kind == "http_operation", true
	case "field_mapping":
		return from.Kind == "flow_step" || from.Kind == "query", true
	}
	return false, false
}

// validateLineageValueTarget checks the full value address, not only node
// existence. The query path can reuse this after validating its strict input.
func validateLineageValueTarget(ref LineageValueRef, nodes map[string]Node) error {
	n, ok := nodes[ref.NodeID]
	if !ok {
		return notFound()
	}
	switch ref.Kind {
	case "api_field":
		if n.Kind == "api_field" {
			return nil
		}
	case "column":
		if n.Kind == "column" {
			facets, _, err := relationalFacetObject(n.Kind, n.Attributes)
			if err == nil && facets[ref.FacetKey] != nil {
				return nil
			}
		}
	case "port":
		valid := n.Kind == "flow_step" && slices.Contains([]string{"inputs", "outputs"}, ref.Collection) || n.Kind == "query" && slices.Contains([]string{"parameters", "results"}, ref.Collection)
		if valid {
			var ports []runtimePort
			if json.Unmarshal(n.Attributes[ref.Collection], &ports) == nil && slices.ContainsFunc(ports, func(p runtimePort) bool { return p.Key == ref.PortKey }) {
				return nil
			}
		}
	}
	return invalid("seed", "Value reference kind, facet or local port does not exist")
}
func validateLineageGraph(ctx context.Context, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) error {
	return validateLineageGraphRules(ctx, selectedProfile(s.Profile), s, g, diagnostics)
}

func validateLineageGraphRules(ctx context.Context, profile string, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) error {
	c, err := newLineageGraphCheck(ctx, profile, s, g, diagnostics)
	if err != nil {
		return err
	}
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !lineageSubject(n.Kind, false) {
			continue
		}
		if err := c.node(n); err != nil {
			return err
		}
	}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if lineageSubject(c.nodes[e.From].Kind, false) || lineageSubject(c.nodes[e.To].Kind, false) {
			c.proof(e.ID, e.EvidenceIDs, e.Freshness)
		}
	}
	return nil
}

// lineageGraphCheck holds the indexes and the running limits one pass of
// the lineage rules shares across nodes; the profile picks which attribute
// validator and mapping schema apply (lineage, events or composed).
type lineageGraphCheck struct {
	profile     string
	s           *ImportSession
	events      bool
	validator   func(kind string, a map[string]jsontext.Value, edge, persisted bool) error
	schema      string
	diagnostics *[]ImportDiagnostic

	edges    map[string]Edge
	handles  map[string][]Edge
	nodes    map[string]Node
	proofs   map[string]Evidence
	contains map[string][]Edge
	files    map[string]bool

	fields    map[string]int
	selectors map[string]bool
	count     int
}

func newLineageGraphCheck(ctx context.Context, profile string, s *ImportSession, g *graphCandidate, diagnostics *[]ImportDiagnostic) (*lineageGraphCheck, error) {
	c := &lineageGraphCheck{profile: profile, s: s, diagnostics: diagnostics, validator: validateLineageAttributes, schema: LineageSchemaVersion}
	c.events = profile == EventsProfile || profile == ComposedProfile
	if c.events {
		c.validator = validateEventsLineageAttributes
		c.schema = EventsSchemaVersion
	}
	if profile == ComposedProfile {
		c.validator = validateRepresentationAttributes
		c.schema = ComposedSchemaVersion
	}
	c.edges = map[string]Edge{}
	c.handles = map[string][]Edge{}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Kind == "handles" {
			c.handles[e.From] = append(c.handles[e.From], e)
		}
		c.edges[e.ID] = e
	}
	c.nodes = map[string]Node{}
	c.proofs = map[string]Evidence{}
	c.contains = map[string][]Edge{}
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.nodes[n.ID] = n
	}
	for _, p := range g.Evidence {
		c.proofs[p.ID] = p
	}
	for _, e := range g.Edges {
		if e.Kind == "contains" {
			c.contains[e.To] = append(c.contains[e.To], e)
		}
	}
	// Index analyzed manifest members once; every lineage assertion needs member
	// evidence with physical line bounds, including retained historical evidence.
	c.files = map[string]bool{}
	for _, src := range g.Sources {
		for _, f := range src.Files {
			if f.AnalysisStatus == "analyzed" {
				c.files[src.RepositoryID+"\x00"+src.ID+"\x00"+f.Path+"\x00"+f.ContentHash] = true
			}
		}
	}
	c.fields = map[string]int{}
	c.selectors = map[string]bool{}
	return c, nil
}

func (c *lineageGraphCheck) add(path, message string) {
	*c.diagnostics = append(*c.diagnostics, ImportDiagnostic{Code: "backend_graph_invalid", Path: path, Message: message})
}

// proof requires one member evidence record with physical line bounds in an
// analyzed manifest file; a current assertion must cite this snapshot.
func (c *lineageGraphCheck) proof(id string, ids []string, fresh *AssertionFreshness) {
	if c.s == nil {
		return
	}
	for _, eid := range ids {
		e, ok := c.proofs[eid]
		if !ok || !c.memberEvidence(id, e) {
			continue
		}
		if fresh != nil && fresh.Status == "current" && e.Source.SnapshotID != c.s.SnapshotID {
			continue
		}
		if c.files[e.Source.RepositoryID+"\x00"+e.Source.SnapshotID+"\x00"+e.Source.File+"\x00"+e.Source.ContentHash] {
			return
		}
	}
	c.add("subjects/"+id+"/evidenceIds", "Lineage assertions require member evidence in an analyzed manifest file with physical line bounds")
}

// memberEvidence reports whether e is the subject's own evidence from this
// repository with a well-formed line range.
func (c *lineageGraphCheck) memberEvidence(id string, e Evidence) bool {
	return e.SubjectID == id && e.Source.RepositoryID == c.s.RepositoryID && e.Source.StartLine != nil && e.Source.EndLine != nil && *e.Source.StartLine >= 1 && *e.Source.EndLine >= *e.Source.StartLine
}

func (c *lineageGraphCheck) node(n Node) error {
	path := "nodes/" + n.ID
	if err := c.validator(n.Kind, n.Attributes, false, true); err != nil {
		c.add(path, err.Error())
		return nil
	}
	c.proof(n.ID, n.EvidenceIDs, n.Freshness)
	parent := Node{}
	if n.ParentID != nil {
		parent = c.nodes[*n.ParentID]
	}
	if !c.parentValid(n, parent) {
		c.add(path+"/parentId", "Lineage nodes require one agreeing contains edge and a valid parent in the same repository")
	}
	if n.Kind == "api_field" {
		return c.apiField(n, parent, path)
	}
	return c.mapping(n, path)
}

// parentValid applies the profile's containment rule and requires exactly
// one agreeing contains edge from a parent in the session's repository.
func (c *lineageGraphCheck) parentValid(n, parent Node) bool {
	valid, _ := lineageContains(parent, n)
	if c.events {
		if v, ok := eventsContains(parent, n); ok {
			valid = v
		}
	}
	if c.profile == ComposedProfile {
		if v, ok := representationContains(parent, n); ok {
			valid = v
		}
	}
	if !valid || len(c.contains[n.ID]) != 1 || c.contains[n.ID][0].From != parent.ID {
		return false
	}
	return c.s == nil || parent.Ownership != nil && parent.Ownership.RepositoryID == c.s.RepositoryID
}

// apiField counts fields per operation and rejects two fields with the
// same selector identity (the attributes minus their descriptive members).
func (c *lineageGraphCheck) apiField(n, parent Node, path string) error {
	c.fields[parent.ID]++
	if c.fields[parent.ID] > MaxAPIFieldsPerOperation {
		return limitFault("API field limit per operation exceeded")
	}
	identity := maps.Clone(n.Attributes)
	for _, k := range []string{"nativeType", "analysisStatus", "gaps", "description"} {
		delete(identity, k)
	}
	raw, err := canonicalJSON(identity)
	if err != nil {
		return err
	}
	key := parent.ID + "\x00" + string(raw)
	if c.selectors[key] {
		c.add(path+"/attributes/selector", "Duplicate API selector identity within operation")
	}
	c.selectors[key] = true
	return nil
}

func (c *lineageGraphCheck) mapping(n Node, path string) error {
	a, err := decodeLineageMappingForSchema(n.Attributes, c.schema)
	if err != nil {
		c.add(path, err.Error())
		return nil
	}
	if c.events {
		mappingValidator := validateEventsLineageMapping
		if c.profile == ComposedProfile {
			mappingValidator = validateRepresentationLineageMapping
		}
		if err := mappingValidator(n, a, c.nodes, c.edges, c.handles); err != nil {
			c.add(path+"/attributes", err.Error())
		}
	}
	c.count += len(a.Sources) + 1
	if c.count > MaxLineageReferences {
		return limitFault("Lineage reference limit exceeded")
	}
	for i, ref := range append(slices.Clone(a.Sources), a.Destination) {
		refPath := path + "/attributes/destination"
		if i < len(a.Sources) {
			refPath = fmt.Sprintf("%s/attributes/sources/%d", path, i)
		}
		if err := validateLineageValueTargetForSchema(ref, c.nodes, c.edges, c.schema); err != nil {
			c.add(refPath, "Every lineage value must survive with its exact kind, facet and local port")
			continue
		}
		target := c.nodes[ref.NodeID]
		if c.s != nil && (target.Ownership == nil || target.Ownership.RepositoryID != c.s.RepositoryID) {
			c.add(refPath, "Lineage endpoints must belong to the same repository")
		}
	}
	return nil
}

// Selected facet proof can be stale even when another facet of the column was
// reobserved. Seed that staleness before the ordinary transitive propagation.
func markLineageEndpointStaleness(g *graphCandidate) {
	nodes := map[string]Node{}
	proofs := map[string]Evidence{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	for _, e := range g.Evidence {
		proofs[e.ID] = e
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind != "field_mapping" || n.Freshness == nil || n.Freshness.Status == "stale" {
			continue
		}
		a, err := decodeLineageMappingForSchema(n.Attributes, EventsSchemaVersion)
		if err != nil {
			continue
		}
		stale := false
		for _, ref := range append(slices.Clone(a.Sources), a.Destination) {
			target := nodes[ref.NodeID]
			ids := target.EvidenceIDs
			if ref.Kind == "column" {
				facets, _, err := relationalFacetObject("column", target.Attributes)
				if err != nil {
					continue
				}
				var f struct {
					Freshness   *AssertionFreshness `json:"freshness"`
					EvidenceIDs []string            `json:"evidenceIds"`
				}
				if json.Unmarshal(facets[ref.FacetKey], &f) == nil {
					stale = stale || f.Freshness != nil && f.Freshness.Status == "stale"
					ids = f.EvidenceIDs
				}
			}
			for _, id := range ids {
				e := proofs[id]
				stale = stale || e.Freshness != nil && e.Freshness.Status == "stale"
			}
		}
		if stale {
			n.Freshness.Status = "stale"
			n.Freshness.Reasons = []string{"dependency_stale"}
		}
	}
}

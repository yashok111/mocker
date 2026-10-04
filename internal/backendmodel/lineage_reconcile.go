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
	events := profile == EventsProfile || profile == ComposedProfile
	validator := validateLineageAttributes
	schema := LineageSchemaVersion
	if events {
		validator = validateEventsLineageAttributes
		schema = EventsSchemaVersion
	}
	if profile == ComposedProfile {
		validator = validateRepresentationAttributes
		schema = ComposedSchemaVersion
	}
	edges := map[string]Edge{}
	handles := map[string][]Edge{}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Kind == "handles" {
			handles[e.From] = append(handles[e.From], e)
		}
		edges[e.ID] = e
	}
	add := func(path, message string) {
		*diagnostics = append(*diagnostics, ImportDiagnostic{Code: "backend_graph_invalid", Path: path, Message: message})
	}
	nodes := map[string]Node{}
	proofs := map[string]Evidence{}
	contains := map[string][]Edge{}
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes[n.ID] = n
	}
	for _, p := range g.Evidence {
		proofs[p.ID] = p
	}
	for _, e := range g.Edges {
		if e.Kind == "contains" {
			contains[e.To] = append(contains[e.To], e)
		}
	}
	// Index analyzed manifest members once; every lineage assertion needs member
	// evidence with physical line bounds, including retained historical evidence.
	files := map[string]bool{}
	for _, src := range g.Sources {
		for _, f := range src.SnapshotManifest.Files {
			if f.AnalysisStatus == "analyzed" {
				files[src.RepositoryID+"\x00"+src.ID+"\x00"+f.Path+"\x00"+f.ContentHash] = true
			}
		}
	}
	proof := func(id string, ids []string, fresh *AssertionFreshness) {
		if s == nil {
			return
		}
		for _, eid := range ids {
			e, ok := proofs[eid]
			if !ok || e.SubjectID != id || e.Source.RepositoryID != s.RepositoryID || e.Source.StartLine == nil || e.Source.EndLine == nil || *e.Source.StartLine < 1 || *e.Source.EndLine < *e.Source.StartLine {
				continue
			}
			if fresh != nil && fresh.Status == "current" && e.Source.SnapshotID != s.SnapshotID {
				continue
			}
			if files[e.Source.RepositoryID+"\x00"+e.Source.SnapshotID+"\x00"+e.Source.File+"\x00"+e.Source.ContentHash] {
				return
			}
		}
		add("subjects/"+id+"/evidenceIds", "Lineage assertions require member evidence in an analyzed manifest file with physical line bounds")
	}
	fields := map[string]int{}
	selectors := map[string]bool{}
	count := 0
	for _, n := range g.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !lineageSubject(n.Kind, false) {
			continue
		}
		path := "nodes/" + n.ID
		if err := validator(n.Kind, n.Attributes, false, true); err != nil {
			add(path, err.Error())
			continue
		}
		proof(n.ID, n.EvidenceIDs, n.Freshness)
		parent := Node{}
		if n.ParentID != nil {
			parent = nodes[*n.ParentID]
		}
		valid, _ := lineageContains(parent, n)
		if events {
			if v, ok := eventsContains(parent, n); ok {
				valid = v
			}
		}
		if profile == ComposedProfile {
			if v, ok := representationContains(parent, n); ok {
				valid = v
			}
		}
		if !valid || len(contains[n.ID]) != 1 || contains[n.ID][0].From != parent.ID || s != nil && (parent.Ownership == nil || parent.Ownership.RepositoryID != s.RepositoryID) {
			add(path+"/parentId", "Lineage nodes require one agreeing contains edge and a valid parent in the same repository")
		}
		if n.Kind == "api_field" {
			fields[parent.ID]++
			if fields[parent.ID] > MaxAPIFieldsPerOperation {
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
			if selectors[key] {
				add(path+"/attributes/selector", "Duplicate API selector identity within operation")
			}
			selectors[key] = true
			continue
		}
		a, err := decodeLineageMappingForSchema(n.Attributes, schema)
		if err != nil {
			add(path, err.Error())
			continue
		}
		if events {
			mappingValidator := validateEventsLineageMapping
			if profile == ComposedProfile {
				mappingValidator = validateRepresentationLineageMapping
			}
			if err := mappingValidator(n, a, nodes, edges, handles); err != nil {
				add(path+"/attributes", err.Error())
			}
		}
		count += len(a.Sources) + 1
		if count > MaxLineageReferences {
			return limitFault("Lineage reference limit exceeded")
		}
		for i, ref := range append(slices.Clone(a.Sources), a.Destination) {
			refPath := path + "/attributes/destination"
			if i < len(a.Sources) {
				refPath = fmt.Sprintf("%s/attributes/sources/%d", path, i)
			}
			if err := validateLineageValueTargetForSchema(ref, nodes, edges, schema); err != nil {
				add(refPath, "Every lineage value must survive with its exact kind, facet and local port")
				continue
			}
			target := nodes[ref.NodeID]
			if s != nil && (target.Ownership == nil || target.Ownership.RepositoryID != s.RepositoryID) {
				add(refPath, "Lineage endpoints must belong to the same repository")
			}
		}
	}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if lineageSubject(nodes[e.From].Kind, false) || lineageSubject(nodes[e.To].Kind, false) {
			proof(e.ID, e.EvidenceIDs, e.Freshness)
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

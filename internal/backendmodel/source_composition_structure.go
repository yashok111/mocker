package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"slices"
)

// ValidateSourceStructure checks a complete materialized graph, including
// desired records without source provenance. It never reads source history or
// admits evidence, provider ownership, freshness or snapshot membership.
// Historical migration pins require separate project/ancestor admission.
func ValidateSourceStructure(ctx context.Context, graph SourceStructuralGraph) ([]ImportDiagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v := sourceStructureValidator{schema: graph.SchemaVersion, profile: structuralProfile(graph.SchemaVersion), nodes: map[string]Node{}, ids: map[string]bool{}, parents: map[string]string{}, diagnostics: []ImportDiagnostic{}}
	if v.profile == "" {
		v.add("schemaVersion", "Unsupported structural schema version")
		return v.diagnostics, nil
	}
	for _, n := range graph.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v.node(n)
	}
	for _, e := range graph.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v.edge(e)
	}
	if err := v.hierarchy(ctx, graph.Nodes); err != nil {
		return nil, err
	}
	if len(v.diagnostics) != 0 {
		return v.diagnostics, nil
	}
	if err := validateSourceStructureGroups(ctx, v.profile, graph, &v.diagnostics); err != nil {
		return nil, err
	}
	return v.diagnostics, ctx.Err()
}

type sourceStructureValidator struct {
	schema, profile string
	nodes           map[string]Node
	ids             map[string]bool
	parents         map[string]string
	diagnostics     []ImportDiagnostic
}

func (v *sourceStructureValidator) add(path, message string) {
	v.diagnostics = append(v.diagnostics, ImportDiagnostic{Code: "backend_graph_invalid", Path: path, Message: message})
}

func (v *sourceStructureValidator) node(n Node) {
	path := "nodes/" + n.ID
	if !ValidID(n.ID) || v.ids[n.ID] {
		v.add(path+"/id", "Node UUID must be valid and unique")
	}
	v.ids[n.ID] = true
	v.nodes[n.ID] = n
	if !slices.Contains(SupportedNodeKindsForProfile(v.profile), n.Kind) {
		v.add(path+"/kind", "Unsupported node kind")
	}
	if !nonblank(n.Name) {
		v.add(path+"/name", "Node name is required")
	}
	if err := validateSourceStructuralAttributes(v.profile, n.Kind, n.Attributes, false); err != nil {
		v.add(path+"/attributes", err.Error())
	}
}

func (v *sourceStructureValidator) edge(e Edge) {
	path := "edges/" + e.ID
	if !ValidID(e.ID) || v.ids[e.ID] {
		v.add(path+"/id", "Edge UUID must be valid and unique")
	}
	v.ids[e.ID] = true
	if !slices.Contains(SupportedEdgeKindsForProfile(v.profile), e.Kind) {
		v.add(path+"/kind", "Unsupported edge kind")
	}
	if err := validateSourceStructuralAttributes(v.profile, e.Kind, e.Attributes, true); err != nil {
		v.add(path+"/attributes", err.Error())
	}
	from, fok := v.nodes[e.From]
	to, tok := v.nodes[e.To]
	if !fok || !tok {
		v.add(path, "Edge endpoints must survive in the final graph")
		return
	}
	if !sourceStructuralEndpoints(v.profile, e, from, to) && !source6HTTPCall(v.schema, e, from, to) {
		v.add(path, "Edge kind does not support these endpoint kinds")
	}
	if e.Kind == "contains" {
		if _, ok := v.parents[e.To]; ok {
			v.add(path, "A node may have only one incoming contains edge")
		}
		v.parents[e.To] = e.From
	}
}

func (v *sourceStructureValidator) hierarchy(ctx context.Context, nodes []Node) error {
	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n.ParentID != nil && (!ValidID(*n.ParentID) || v.parents[n.ID] != *n.ParentID) {
			v.add("nodes/"+n.ID+"/parentId", "Structural parent must agree with its sole incoming contains edge")
		}
	}
	colors := map[string]uint8{}
	for _, n := range nodes {
		path := []string{}
		id := n.ID
		for id != "" && colors[id] == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			colors[id] = 1
			path = append(path, id)
			id = v.parents[id]
		}
		if id != "" && colors[id] == 1 {
			v.add("nodes/"+id, "Contains hierarchy must be acyclic")
		}
		for _, id := range path {
			colors[id] = 2
		}
	}
	return nil
}

func validateSourceStructureGroups(ctx context.Context, profile string, graph SourceStructuralGraph, d *[]ImportDiagnostic) error {
	g := &graphCandidate{Nodes: graph.Nodes, Edges: graph.Edges}
	if hasRelationalProfile(profile) {
		if err := validateRelationalGraphRules(ctx, nil, nil, g, d); err != nil {
			return err
		}
	}
	if hasRuntimeProfile(profile) {
		runtimeProfile := profile
		if profile == ComposedProfile {
			runtimeProfile = EventsProfile
		}
		if err := validateRuntimeGraphRules(ctx, runtimeProfile, nil, g, d); err != nil {
			return err
		}
	}
	if hasLineageProfile(profile) {
		if err := validateLineageGraphRules(ctx, profile, nil, g, d); err != nil {
			return err
		}
	}
	if profile == EventsProfile || profile == ComposedProfile {
		if err := validateEventsGraphRules(ctx, nil, g, d); err != nil {
			return err
		}
	}
	if profile == ComposedProfile {
		return validateRepresentationGraph(ctx, graph, d)
	}
	return nil
}

func source6HTTPCall(schema string, e Edge, from, to Node) bool {
	if schema != ComposedSchemaVersion || e.Kind != "calls" || to.Kind != "http_operation" {
		return false
	}
	if from.Kind == "handler" || from.Kind == "symbol" {
		return true
	}
	if from.Kind != "flow_step" {
		return false
	}
	step := runtimeString(from.Attributes["stepKind"])
	return step == "call" || step == "query"
}

func structuralProfile(schema string) string {
	switch schema {
	case SchemaVersion:
		return GraphProfile
	case RelationalSchemaVersion:
		return RelationalProfile
	case RuntimeSchemaVersion:
		return RuntimeProfile
	case LineageSchemaVersion:
		return LineageProfile
	case EventsSchemaVersion:
		return EventsProfile
	case ComposedSchemaVersion:
		return ComposedProfile
	default:
		return ""
	}
}

func validateSourceStructuralAttributes(profile, kind string, attrs map[string]jsontext.Value, edge bool) error {
	if attrs == nil {
		return semantic("attributes", "Attributes must be an object")
	}
	if hasRelationalProfile(profile) && relationalSubject(kind, attrs, edge) {
		return validateRelationalStructureAttributes(kind, attrs, edge)
	}
	switch profile {
	case ComposedProfile:
		return validateRepresentationAttributes(kind, attrs, edge, true)
	case EventsProfile:
		return validateEventsAttributes(kind, attrs, edge, true)
	case LineageProfile:
		return validateLineageAttributes(kind, attrs, edge, true)
	case RuntimeProfile:
		return validateRuntimeAttributes(kind, attrs, edge, true)
	default:
		return validateAttributes(kind, attrs, edge)
	}
}

func sourceStructuralEndpoints(profile string, e Edge, from, to Node) bool {
	valid := sourceFoundationEndpoints(profile, e, from, to)
	if hasRuntimeProfile(profile) {
		if value, applies := runtimeEndpoints(e, from, to); applies {
			valid = value
		}
	}
	if hasLineageProfile(profile) && e.Kind == "contains" {
		if value, applies := lineageContains(from, to); applies {
			valid = value
		}
	}
	if profile == EventsProfile || profile == ComposedProfile {
		if value, applies := eventsEndpoints(e, from, to); applies {
			valid = value
		}
	}
	if profile == ComposedProfile && e.Kind == "contains" {
		if value, applies := representationContains(from, to); applies {
			valid = value
		}
	}
	return valid
}

func sourceFoundationEndpoints(profile string, e Edge, from, to Node) bool {
	valid := false
	switch e.Kind {
	case "contains":
		valid = slices.Contains([]string{"system", "service", "module"}, from.Kind) || slices.Contains([]string{"external_system", "datastore"}, from.Kind) && slices.Contains([]string{"module", "symbol", "handler"}, to.Kind)
		if hasRelationalProfile(profile) {
			if value, applies := relationalContains(from, to); applies {
				valid = value
			}
		}
	case "handles":
		valid = from.Kind == "http_operation" && slices.Contains([]string{"handler", "symbol", "unresolved_target"}, to.Kind)
	case "calls":
		valid = slices.Contains([]string{"symbol", "handler"}, from.Kind) && slices.Contains([]string{"symbol", "handler", "external_system", "unresolved_target"}, to.Kind)
	case "references":
		valid = hasRelationalProfile(profile) && from.Kind == "constraint" && (to.Kind == "table" || to.Kind == "unresolved_target")
	case "derived_from":
		valid = slices.Contains([]string{"symbol", "module", "unresolved_target"}, to.Kind)
	}
	return valid
}

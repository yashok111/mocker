package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestValidateSourceStructureDesiredWithoutProof(t *testing.T) {
	for _, kind := range []string{"relational", "runtime", "events"} {
		t.Run(kind, func(t *testing.T) {
			g := structuralTestGraph(t, kind)
			d, err := ValidateSourceStructure(t.Context(), g)
			if err != nil || len(d) != 0 {
				t.Fatalf("desired graph requires no source admission: %v %+v", err, d)
			}
		})
	}
}

func TestValidateSourceStructureInvalidFinalGraph(t *testing.T) {
	for _, tc := range []struct {
		name, fixture, message string
		change                 func(*testing.T, *SourceStructuralGraph)
	}{
		{"wrong parent", "runtime", "parent", func(t *testing.T, g *SourceStructuralGraph) {
			structuralNode(g, "step").ParentID = new(structuralID("handler"))
		}},
		{"containment cycle", "runtime", "acyclic", func(t *testing.T, g *SourceStructuralGraph) {
			g.Nodes = append(g.Nodes, Node{ID: structuralID("module"), Kind: "module", Name: "module", ParentID: new(structuralID("module")), Attributes: map[string]jsontext.Value{}})
			g.Edges = append(g.Edges, structuralEdge("cycle", "contains", "module", "module", nil))
		}},
		{"wrong endpoint", "runtime", "endpoint", func(t *testing.T, g *SourceStructuralGraph) {
			g.Edges = append(g.Edges, structuralEdge("wrong", "handles", "handler", "flow", nil))
		}},
		{"dangling endpoint", "runtime", "survive", func(t *testing.T, g *SourceStructuralGraph) {
			g.Edges = append(g.Edges, structuralEdge("wrong", "calls", "handler", "missing", nil))
		}},
		{"unknown attribute", "runtime", "Unknown", func(t *testing.T, g *SourceStructuralGraph) {
			structuralNode(g, "step").Attributes["surprise"] = jsontext.Value(`true`)
		}},
		{"wrong entry membership", "runtime", "Entry", func(t *testing.T, g *SourceStructuralGraph) {
			structuralNode(g, "flow").Attributes["entryStepId"] = relationalRaw(t, structuralID("handler"))
		}},
		{"missing port", "events", "local port", func(t *testing.T, g *SourceStructuralGraph) {
			structuralNode(g, "step").Attributes["outputs"] = jsontext.Value(`[]`)
		}},
		{"wrong contextual endpoint", "events", "exact producer", func(t *testing.T, g *SourceStructuralGraph) {
			structuralNode(g, "serialize").Attributes["destination"] = relationalRaw(t, LineageValueRef{Kind: "event_field", NodeID: structuralID("field"), EndpointID: structuralID("consumer"), RouteID: structuralID("emission")})
		}},
		{"missing transport route", "events", "Transport", func(t *testing.T, g *SourceStructuralGraph) {
			structuralNode(g, "transport").Attributes["transport"] = relationalRaw(t, map[string]string{"emitsEdgeId": structuralID("emission"), "deliveryEdgeId": structuralID("missing")})
		}},
		{"missing relational facet", "relational", "facet", func(t *testing.T, g *SourceStructuralGraph) {
			structuralNode(g, "table").Attributes["facets"] = jsontext.Value(`{}`)
		}},
		{"wrong typed scalar", "relational", "boolean", func(t *testing.T, g *SourceStructuralGraph) {
			structuralMutateFacet(t, structuralNode(g, "column"), "nullable", known("false"))
		}},
		{"missing index column", "relational", "survive", func(t *testing.T, g *SourceStructuralGraph) {
			structuralMutateFacet(t, structuralNode(g, "index"), "terms", []any{map[string]any{"columnId": structuralID("missing"), "direction": "asc", "nulls": "last"}})
		}},
		{"foreign key order", "relational", "ordered", func(t *testing.T, g *SourceStructuralGraph) {
			structuralMutateFacet(t, structuralNode(g, "fk"), "columnIds", []string{structuralID("column2"), structuralID("column")})
		}},
		{"migration cycle", "relational", "acyclic", func(t *testing.T, g *SourceStructuralGraph) {
			structuralMutateFacet(t, structuralNode(g, "migration"), "parentIds", []string{structuralID("migration")})
		}},
		{"unknown kind", "runtime", "Unsupported", func(t *testing.T, g *SourceStructuralGraph) { structuralNode(g, "handler").Kind = "invented" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := structuralTestGraph(t, tc.fixture)
			tc.change(t, &g)
			d, err := ValidateSourceStructure(t.Context(), g)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(d, func(d ImportDiagnostic) bool { return strings.Contains(d.Message, tc.message) }) {
				t.Fatalf("wanted %q diagnostic, got %+v", tc.message, d)
			}
		})
	}
}

func TestValidateSourceStructureFinalReferenceRepair(t *testing.T) {
	g := structuralTestGraph(t, "events")
	step := structuralNode(&g, "step")
	step.Attributes["outputs"] = relationalRaw(t, []any{map[string]any{"key": "new", "name": "new", "nativeType": known("string")}})
	if d, err := ValidateSourceStructure(t.Context(), g); err != nil || len(d) == 0 {
		t.Fatalf("dangling old port accepted: %v %+v", err, d)
	}
	structuralNode(&g, "serialize").Attributes["sources"] = relationalRaw(t, []LineageValueRef{{Kind: "port", NodeID: step.ID, Collection: "outputs", PortKey: "new"}})
	if d, err := ValidateSourceStructure(t.Context(), g); err != nil || len(d) != 0 {
		t.Fatalf("final repaired graph rejected: %v %+v", err, d)
	}
}

func TestValidateSourceStructureAllowsRuntimeRecursion(t *testing.T) {
	g := structuralTestGraph(t, "runtime")
	g.Edges = append(g.Edges, structuralEdge("recursive", "calls", "handler", "handler", nil))
	a := runtimeStepAttrs(t, "loop")
	a["expression"] = relationalRaw(t, known("hasNext"))
	g.Nodes = append(g.Nodes, Node{ID: structuralID("loop"), Kind: "flow_step", Name: "loop", ParentID: new(structuralID("flow")), Attributes: a})
	g.Edges = append(g.Edges, structuralEdge("contains:loop", "contains", "flow", "loop", nil), structuralEdge("next:loop", "next", "loop", "loop", nil), structuralEdge("next:exit", "next", "loop", "step", nil))
	structuralNode(&g, "flow").Attributes["entryStepId"] = relationalRaw(t, structuralID("loop"))
	if d, err := ValidateSourceStructure(t.Context(), g); err != nil || len(d) != 0 {
		t.Fatalf("legal recursion rejected: %v %+v", err, d)
	}
}

func TestValidateSourceStructureRuntimeTransactionAndFacet(t *testing.T) {
	for _, mode := range []string{"valid", "missing access facet", "missing boundary", "wrong transaction context"} {
		t.Run(mode, func(t *testing.T) {
			g := structuralTestGraph(t, "runtime")
			relational := structuralTestGraph(t, "relational")
			g.Nodes = append(g.Nodes, relational.Nodes...)
			g.Edges = append(g.Edges, relational.Edges...)
			g.Nodes = append(g.Nodes, Node{ID: structuralID("tx"), Kind: "transaction", Name: "tx", ParentID: new(structuralID("flow")), Attributes: runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "datastoreId": structuralID("db"), "connectionScope": known("tx"), "isolationLevel": known("serializable"), "boundaryStatus": "complete"})})
			begin := runtimeStepAttrs(t, "transaction_begin")
			begin["transactionContext"] = relationalRaw(t, map[string]any{"status": "known", "transactionId": structuralID("tx")})
			g.Nodes = append(g.Nodes, Node{ID: structuralID("begin"), Kind: "flow_step", Name: "begin", ParentID: new(structuralID("flow")), Attributes: begin})
			step := structuralNode(&g, "step")
			step.Attributes["stepKind"], step.Attributes["transactionContext"] = relationalRaw(t, "transaction_commit"), begin["transactionContext"]
			structuralNode(&g, "flow").Attributes["entryStepId"] = relationalRaw(t, structuralID("begin"))
			g.Nodes = append(g.Nodes, Node{ID: structuralID("query"), Kind: "query", Name: "query", ParentID: new(structuralID("handler")), Attributes: runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "dialect": "sqlite", "nativeDefinition": "select value", "parameters": []any{}, "results": []any{}, "columnScope": "complete"})})
			g.Edges = append(g.Edges, structuralEdge("contains:tx", "contains", "flow", "tx", nil), structuralEdge("contains:begin", "contains", "flow", "begin", nil), structuralEdge("contains:query", "contains", "handler", "query", nil), structuralEdge("begins", "begins", "begin", "tx", nil), structuralEdge("commits", "commits", "step", "tx", nil), structuralEdge("next:begin", "next", "begin", "step", nil), structuralEdge("reads", "reads", "query", "column", runtimeAttrs(t, map[string]any{"accessMode": "read", "datastoreId": structuralID("db"), "facetKey": "sql", "columnScope": "listed"})))
			switch mode {
			case "missing access facet":
				g.Edges[len(g.Edges)-1].Attributes["facetKey"] = relationalRaw(t, "missing")
			case "missing boundary":
				g.Edges = slices.DeleteFunc(g.Edges, func(e Edge) bool { return e.Kind == "begins" })
			case "wrong transaction context":
				structuralNode(&g, "begin").Attributes["transactionContext"] = relationalRaw(t, map[string]any{"status": "known", "transactionId": structuralID("db")})
			}
			d, err := ValidateSourceStructure(t.Context(), g)
			if err != nil || (len(d) == 0) != (mode == "valid") {
				t.Fatalf("%s: %v %+v", mode, err, d)
			}
		})
	}
}

func TestValidateSourceStructureCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ValidateSourceStructure(ctx, structuralTestGraph(t, "events"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
}

func TestValidateSourceStructureRetainsLegacyAdmission(t *testing.T) {
	g := structuralTestGraph(t, "relational")
	if err := validateRelationalAttributes("column", structuralNode(&g, "column").Attributes, false, true); err == nil {
		t.Fatal("legacy facet accepted without proof")
	}
	g = structuralTestGraph(t, "runtime")
	d := []ImportDiagnostic{}
	err := validateRuntimeGraph(t.Context(), nil, &ImportSession{Profile: RuntimeProfile}, &graphCandidate{Nodes: g.Nodes, Edges: g.Edges}, &d)
	if err != nil || !slices.ContainsFunc(d, func(d ImportDiagnostic) bool { return strings.Contains(d.Message, "proof") }) {
		t.Fatalf("legacy runtime proof admission lost: %v %+v", err, d)
	}
}

func structuralID(key string) string {
	// Stable IDs keep the fixture independent of source import allocation.
	return fmt.Sprintf("11111111-1111-4111-8111-%012x", structuralKeyNumber(key))
}

func structuralKeyNumber(key string) uint64 {
	var n uint64
	for _, c := range key {
		n = (n*31 + uint64(c)) & 0xffffffffffff
	}
	return n
}

func structuralNode(g *SourceStructuralGraph, key string) *Node {
	i := slices.IndexFunc(g.Nodes, func(n Node) bool { return n.ID == structuralID(key) })
	return &g.Nodes[i]
}

func structuralEdge(key, kind, from, to string, attrs map[string]jsontext.Value) Edge {
	if attrs == nil {
		attrs = map[string]jsontext.Value{}
	}
	return Edge{ID: structuralID(key), Kind: kind, From: structuralID(from), To: structuralID(to), Attributes: attrs}
}

func structuralMutateFacet(t *testing.T, n *Node, field string, value any) {
	t.Helper()
	fs, _, err := relationalFacetObject(n.Kind, n.Attributes)
	if err != nil {
		t.Fatal(err)
	}
	f, err := relationalObject(fs["sql"])
	if err != nil {
		t.Fatal(err)
	}
	f[field] = relationalRaw(t, value)
	fs["sql"] = relationalRaw(t, f)
	n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, fs)
}

func structuralTestGraph(t *testing.T, fixture string) SourceStructuralGraph {
	t.Helper()
	g := SourceStructuralGraph{SchemaVersion: ComposedSchemaVersion, Nodes: []Node{}, Edges: []Edge{}}
	node := func(key, kind, parent string, attrs map[string]jsontext.Value) {
		if attrs == nil {
			attrs = map[string]jsontext.Value{}
		}
		n := Node{ID: structuralID(key), Kind: kind, Name: key, Attributes: attrs}
		if parent != "" {
			n.ParentID = new(structuralID(parent))
			g.Edges = append(g.Edges, structuralEdge("contains:"+key, "contains", parent, key, nil))
		}
		g.Nodes = append(g.Nodes, n)
	}
	if fixture == "relational" {
		facet := func(kind string, fields map[string]any) map[string]jsontext.Value {
			fields["dialect"], fields["analysisStatus"], fields["gaps"] = "sqlite", "complete", []string{}
			a := runtimeAttrs(t, map[string]any{"facets": map[string]any{"sql": fields}})
			if kind == "datastore" {
				return runtimeAttrs(t, map[string]any{"relational": a})
			}
			return a
		}
		node("db", "datastore", "", facet("datastore", map[string]any{"databaseName": "app", "qualifiedName": "app", "nativeDefinition": nil}))
		node("schema", "db_schema", "db", facet("db_schema", map[string]any{"qualifiedName": "main", "nativeDefinition": nil}))
		node("table", "table", "schema", facet("table", map[string]any{"qualifiedName": "main.records", "nativeDefinition": nil, "columnsStatus": "complete", "constraintsStatus": "complete"}))
		node("column", "column", "table", facet("column", map[string]any{"nativeType": known("integer"), "typeFamily": known("integer"), "nullable": known(false), "defaultExpression": known(nil), "generatedExpression": known(nil), "identity": known(nil), "ordinal": known(1)}))
		node("column2", "column", "table", facet("column", map[string]any{"nativeType": known("integer"), "typeFamily": known("integer"), "nullable": known(false), "defaultExpression": known(nil), "generatedExpression": known(nil), "identity": known(nil), "ordinal": known(2)}))
		node("index", "index", "table", facet("index", map[string]any{"terms": []any{map[string]any{"columnId": structuralID("column"), "direction": "asc", "nulls": "last"}}, "unique": known(true), "predicate": known(nil), "method": known(nil), "nativeDefinition": nil}))
		node("fk", "constraint", "table", facet("constraint", map[string]any{"constraintKind": "foreign_key", "columnIds": []string{structuralID("column"), structuralID("column2")}, "expression": known(nil), "nativeDefinition": nil, "deferrable": known(false), "initiallyDeferred": known(false)}))
		g.Edges = append(g.Edges, structuralEdge("references", "references", "fk", "table", facet("references", map[string]any{"columnPairs": []any{map[string]string{"fromColumnId": structuralID("column"), "toColumnId": structuralID("column")}, map[string]string{"fromColumnId": structuralID("column2"), "toColumnId": structuralID("column2")}}, "updateAction": known("no_action"), "deleteAction": known("no_action"), "matchType": known("simple")})))
		node("migration", "migration", "db", facet("migration", map[string]any{"order": known(1), "parentIds": []string{}, "definition": "CREATE TABLE records", "changes": []any{map[string]any{"target": map[string]string{"kind": "candidate", "objectId": structuralID("table")}, "operation": "create", "description": "create records"}}, "derivationStatus": "complete"}))
		return g
	}
	node("handler", "handler", "", nil)
	node("flow", "flow", "handler", runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "entryStepId": structuralID("step"), "exitStepIds": []string{structuralID("step")}, "exitStatus": "complete"}))
	a := runtimeStepAttrs(t, "return")
	a["outputs"] = relationalRaw(t, []any{map[string]any{"key": "value", "name": "value", "nativeType": known("string")}})
	if fixture == "events" {
		a["stepKind"] = relationalRaw(t, "emit")
	}
	node("step", "flow_step", "flow", a)
	if fixture != "events" {
		return g
	}
	node("service", "service", "", nil)
	for _, kind := range []string{"channel", "message", "consumer"} {
		node(kind, kind, "service", eventNodeAttrs(t, kind))
	}
	node("field", "event_field", "message", eventNodeAttrs(t, "event_field"))
	g.Edges = append(g.Edges, structuralEdge("emission", "emits", "step", "message", runtimeAttrs(t, map[string]any{"channelId": structuralID("channel"), "deliveryStatus": "declared"})), structuralEdge("delivery", "delivered_to", "channel", "consumer", runtimeAttrs(t, map[string]any{"messageId": structuralID("message"), "condition": known("always"), "group": known("workers"), "deliveryStatus": "declared"})), structuralEdge("handles", "handles", "consumer", "handler", nil))
	producer := LineageValueRef{Kind: "event_field", NodeID: structuralID("field"), EndpointID: structuralID("step"), RouteID: structuralID("emission")}
	consumer := LineageValueRef{Kind: "event_field", NodeID: structuralID("field"), EndpointID: structuralID("consumer"), RouteID: structuralID("delivery")}
	for _, m := range []struct {
		key, parent string
		from, to    LineageValueRef
	}{{"serialize", "step", LineageValueRef{Kind: "port", NodeID: structuralID("step"), Collection: "outputs", PortKey: "value"}, producer}, {"transport", "consumer", producer, consumer}} {
		a := runtimeAttrs(t, map[string]any{"sources": []LineageValueRef{m.from}, "destination": m.to, "transform": LineageTransform{Kind: "copy", Description: "explicit mapping"}, "analysisStatus": "complete", "gaps": []string{}})
		if m.key == "transport" {
			a["transport"] = relationalRaw(t, map[string]string{"emitsEdgeId": structuralID("emission"), "deliveryEdgeId": structuralID("delivery")})
		}
		node(m.key, "field_mapping", m.parent, a)
	}
	return g
}

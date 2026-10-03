package backendmodel

import (
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

// A source5 import that drops contextual edge references, a static entrypoint or
// an explicit unresolved handler must fail this real persisted-graph check.
func TestEventsOrdersFixtureImport(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		t.Run(map[bool]string{false: "baseline", true: "resolved"}[resolved], func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, eventsOrdersInput(t, p, resolved))
			if err != nil {
				t.Fatal(err)
			}
			commands := eventsOrdersCommands(t, s, resolved)
			v, ids := stageRelational(t, r, p, s, commands, "orders")
			if v.State != "ready" {
				t.Fatalf("fixture preview: %+v", v.Diagnostics)
			}
			out, err := commitFixture(t, r, p, s, v, "commit")
			if err != nil {
				t.Fatal(err)
			}
			state, err := loadRevisionState(t.Context(), r.db.R, p.ID, out.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("fixture commands=%d nodes=%d edges=%d evidence=%d analyzedFiles=%d", len(commands), len(state.Nodes), len(state.Edges), len(state.Evidence), len(s.Manifest.Snapshot.Files))
			if state.Revision.SchemaVersion != "5" {
				t.Fatal("fixture did not persist source5")
			}
			for _, key := range []string{"emits.primary", "emits.secondary", "emits.returns", "delivery.primary", "delivery.secondary", "delivery.returns", "delivery.orphan", "delivery.fraud", "delivery.audit", "delivery.legacy", "job.cron", "job.manual", "call.billing", "map.reason", "map.transport.primary.status", "map.transport.secondary.status"} {
				if ids[key] == "" {
					t.Fatalf("missing source assertion %s", key)
				}
			}
			for _, key := range []string{"unknown.audit", "unknown.legacy"} {
				if !slices.ContainsFunc(state.Nodes, func(n Node) bool { return n.ExternalKey == key && n.Kind == "unresolved_target" }) {
					t.Fatalf("unknown omitted: %s", key)
				}
			}
			fraud := "unknown.fraud"
			if resolved {
				fraud = "handler.fraud"
			}
			if !slices.ContainsFunc(state.Nodes, func(n Node) bool { return n.ExternalKey == fraud }) {
				t.Fatalf("wrong fraud evidence outcome: %s", fraud)
			}
		})
	}
}

// Import is tested against an independently authored expectation document, never
// by generating expected routes from QueryEvents or the adapter command builder.
func TestEventsOrdersFixtureIndependentOracle(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		t.Run(map[bool]string{false: "baseline", true: "resolved"}[resolved], func(t *testing.T) {
			r, out, session, ids := eventsOrdersCommitted(t, resolved)
			state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(eventsOrdersSourceDir + "/expected.json")
			if err != nil {
				t.Fatal(err)
			}
			var want struct {
				SourceManifest []struct {
					Path   string `json:"path"`
					SHA256 string `json:"sha256"`
					Scope  string `json:"scope"`
				} `json:"sourceManifest"`
				Nodes []struct {
					Key   string `json:"key"`
					Kind  string `json:"kind"`
					Phase string `json:"phase"`
				} `json:"nodes"`
				Mappings []struct {
					Key         string                  `json:"key"`
					Semantics   string                  `json:"semantics"`
					ParentKey   string                  `json:"parentKey"`
					Sources     []eventsOrdersOracleRef `json:"sources"`
					Destination eventsOrdersOracleRef   `json:"destination"`
				} `json:"mappings"`
				ExpectedViews struct {
					Routes []struct {
						Key             string `json:"key"`
						EmitsEdgeKey    string `json:"emitsEdgeKey"`
						DeliveryEdgeKey string `json:"deliveryEdgeKey"`
					} `json:"routes"`
					Jobs         []string `json:"jobs"`
					ServiceCalls []struct {
						CallEdgeKey  string `json:"callEdgeKey"`
						OperationKey string `json:"operationKey"`
						HandlerKey   string `json:"handlerKey"`
						FlowKey      string `json:"flowKey"`
					} `json:"serviceCalls"`
				} `json:"expectedViews"`
			}
			if err = json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if len(want.Nodes) != 59 || len(want.Mappings) != 12 || len(want.ExpectedViews.Routes) != 6 {
				t.Fatal("independent oracle was not decoded completely")
			}
			nodes := map[string]Node{}
			edges := map[string]Edge{}
			for _, n := range state.Nodes {
				nodes[n.ExternalKey] = n
			}
			for _, e := range state.Edges {
				edges[e.ExternalKey] = e
			}
			for _, n := range want.Nodes {
				if n.Phase == "resolved_only" && !resolved || n.Key == "unknown.fraud" && resolved {
					continue
				}
				got, ok := nodes[n.Key]
				if !ok {
					t.Fatalf("oracle node missing: %s", n.Key)
				}
				kind := n.Kind
				if kind == "database" {
					kind = "datastore"
				}
				if got.Kind != kind {
					t.Fatalf("oracle kind %s = %s, want %s", n.Key, got.Kind, kind)
				}
			}
			for _, m := range want.Mappings {
				if nodes[m.Key].Kind != "field_mapping" {
					t.Fatalf("semantic mapping omitted: %s", m.Key)
				}
			}
			// Each semantic source address is explicitly represented in the
			// strict wire model. Serialization alone adds one producer-port hop.
			addresses := map[string]LineageValueRef{
				"value.row.status":     {Kind: "port", NodeID: ids["query.cancel"], Collection: "results", PortKey: "status"},
				"value.operatorReason": {Kind: "port", NodeID: ids["step.input"], Collection: "inputs", PortKey: "operatorReason"},
				"value.reason":         {Kind: "port", NodeID: ids["step.reason"], Collection: "outputs", PortKey: "reason"},
				"value.decoded.status": {Kind: "port", NodeID: ids["step.decode.cancel"], Collection: "outputs", PortKey: "status"},
			}
			address := func(ref eventsOrdersOracleRef) LineageValueRef {
				if ref.ValueKey != "" {
					got, ok := addresses[ref.ValueKey]
					if !ok {
						t.Fatalf("unknown oracle value %s", ref.ValueKey)
					}
					return got
				}
				got := LineageValueRef{Kind: ref.Kind, NodeID: ids[ref.NodeKey], EndpointID: ids[ref.EndpointKey], RouteID: ids[ref.RouteKey]}
				if ref.Kind == "column" {
					got.FacetKey = "sql"
				}
				return got
			}
			for _, expected := range want.Mappings {
				node := nodes[expected.Key]
				actual, err := decodeLineageMappingForSchema(node.Attributes, state.Revision.SchemaVersion)
				if err != nil {
					t.Fatalf("mapping %s: %v", expected.Key, err)
				}
				parent := expected.ParentKey
				if expected.Semantics == "database_result" {
					parent = "query.cancel"
				}
				if node.ParentID == nil || *node.ParentID != ids[parent] {
					t.Fatalf("semantic mapping %s lost its parent", expected.Key)
				}
				if actual.Destination != address(expected.Destination) {
					t.Fatalf("semantic destination %s: %+v", expected.Key, actual.Destination)
				}
				sources := actual.Sources
				if expected.Semantics == "serialization" {
					bridgeKey := strings.Replace(expected.Key, "map.serialize.", "map.emit_input.", 1)
					bridgeNode := nodes[bridgeKey]
					bridge, err := decodeLineageMappingForSchema(bridgeNode.Attributes, state.Revision.SchemaVersion)
					if err != nil {
						t.Fatal(err)
					}
					if len(sources) != 1 || sources[0] != bridge.Destination || bridge.Destination.Kind != "port" || bridge.Destination.NodeID != ids[parent] || bridgeNode.ParentID == nil || *bridgeNode.ParentID != ids[parent] || len(bridgeNode.EvidenceIDs) == 0 {
						t.Fatalf("serialization input hop %s has no exact parent/port/witness", expected.Key)
					}
					sources = bridge.Sources
				}
				if len(sources) != len(expected.Sources) {
					t.Fatalf("semantic source count %s=%d want%d", expected.Key, len(sources), len(expected.Sources))
				}
				for i, ref := range expected.Sources {
					if sources[i] != address(ref) {
						t.Fatalf("semantic source %s[%d]=%+v want%+v", expected.Key, i, sources[i], address(ref))
					}
				}
			}

			mappingCount := 0
			for _, n := range state.Nodes {
				if n.Kind == "field_mapping" {
					mappingCount++
				}
			}
			if mappingCount != 16 {
				t.Fatalf("wire mappings=%d; want12semantic plus4explicit input copies", mappingCount)
			}
			if len(session.Manifest.Snapshot.Files) != map[bool]int{false: 4, true: 5}[resolved] {
				t.Fatal("wrong analyzed scope")
			}
			for _, f := range want.SourceManifest {
				if f.Scope == "resolved_only" && !resolved {
					continue
				}
				if !slices.ContainsFunc(session.Manifest.Snapshot.Files, func(got ManifestFile) bool {
					return got.Path == f.Path && got.ContentHash == f.SHA256 && got.AnalysisStatus == "analyzed"
				}) {
					t.Fatalf("source identity differs from oracle: %s", f.Path)
				}
			}
			actualPairs := []string{}
			for _, emit := range state.Edges {
				if emit.Kind != "emits" {
					continue
				}
				channel := runtimeString(emit.Attributes["channelId"])
				for _, delivery := range state.Edges {
					if delivery.Kind == "delivered_to" && delivery.From == channel && runtimeString(delivery.Attributes["messageId"]) == emit.To {
						actualPairs = append(actualPairs, emit.ExternalKey+"/"+delivery.ExternalKey)
					}
				}
			}
			wantPairs := make([]string, 0, len(want.ExpectedViews.Routes))
			for _, pair := range want.ExpectedViews.Routes {
				wantPairs = append(wantPairs, pair.EmitsEdgeKey+"/"+pair.DeliveryEdgeKey)
			}
			slices.Sort(actualPairs)
			slices.Sort(wantPairs)
			if !slices.Equal(actualPairs, wantPairs) {
				t.Fatalf("configured source pairs=%v want independent oracle %v", actualPairs, wantPairs)
			}
			for _, key := range want.ExpectedViews.Jobs {
				if nodes[key].Kind != "job" {
					t.Fatalf("static job omitted: %s", key)
				}
			}
			for _, call := range want.ExpectedViews.ServiceCalls {
				e := edges[call.CallEdgeKey]
				if e.To != ids[call.OperationKey] || edges["handles.http"].From != e.To || edges["handles.http"].To != ids[call.HandlerKey] || nodes[call.FlowKey].ParentID == nil || *nodes[call.FlowKey].ParentID != ids[call.HandlerKey] {
					t.Fatal("explicit HTTP operation/handler/flow chain changed")
				}
			}
			for _, route := range []string{"primary", "secondary"} {
				m := nodes["map.transport."+route+".status"]
				var sources []LineageValueRef
				var destination LineageValueRef
				var transport LineageTransport
				if err = json.Unmarshal(m.Attributes["sources"], &sources); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(m.Attributes["destination"], &destination); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(m.Attributes["transport"], &transport); err != nil {
					t.Fatal(err)
				}
				if len(sources) != 1 || sources[0].NodeID != ids["field.orders.status"] || sources[0].EndpointID != ids["step.emit."+route] || sources[0].RouteID != ids["emits."+route] || destination.NodeID != ids["field.orders.status"] || destination.EndpointID != ids["consumer.cancel"] || destination.RouteID != ids["delivery."+route] || transport.EmitsEdgeID != ids["emits."+route] || transport.DeliveryEdgeID != ids["delivery."+route] {
					t.Fatalf("lost exact contextual tuple %s: %+v %+v %+v", route, sources, destination, transport)
				}
			}
			var reasonSources []LineageValueRef
			if err = json.Unmarshal(nodes["map.reason"].Attributes["sources"], &reasonSources); err != nil {
				t.Fatal(err)
			}
			if len(reasonSources) != 2 || reasonSources[0].NodeID != ids["query.cancel"] || reasonSources[0].PortKey != "status" || reasonSources[1].NodeID != ids["step.input"] || reasonSources[1].PortKey != "operatorReason" {
				t.Fatalf("multi-input transform collapsed: %+v", reasonSources)
			}
			for _, key := range []string{"step.emit.primary", "step.emit.secondary"} {
				var tx runtimeTransactionContext
				if err = json.Unmarshal(nodes[key].Attributes["transactionContext"], &tx); err != nil {
					t.Fatal(err)
				}
				if tx.Status != "known" || tx.TransactionID != ids["tx.cancel"] {
					t.Fatal("emit local transaction witness missing")
				}
			}
			if edges["control.commit"].From != ids["step.emit.secondary"] || edges["control.commit"].To != ids["step.commit"] {
				t.Fatal("pre-commit source order lost")
			}
			for _, pair := range [][2]string{{"msg.orders", "msg.returns"}, {"consumer.cancel", "consumer.return"}, {"ch.primary", "ch.returns"}} {
				if nodes[pair[0]].Name != nodes[pair[1]].Name || ids[pair[0]] == ids[pair[1]] {
					t.Fatal("display label collision erased distinct source identity", pair)
				}
			}
			if resolved && nodes["unknown.fraud"].ID != "" {
				t.Fatal("resolved scope retains replaced fraud unknown")
			}
			if edges["delivery.orphan"].To != ids["consumer.orphan"] {
				t.Fatal("reverse orphan subscription lost")
			}
		})
	}
}

// Oracle address vocabulary intentionally remains independent of import DTOs.
type eventsOrdersOracleRef struct {
	Kind        string `json:"kind"`
	ValueKey    string `json:"valueKey"`
	NodeKey     string `json:"nodeKey"`
	EndpointKey string `json:"endpointKey"`
	RouteKey    string `json:"routeKey"`
}

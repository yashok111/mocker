package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type sourceContractReference struct {
	path, recordType, kind, relation, key string
	property                              TypedSourcePropertySelector
	context                               *LineageValueRef
}

func TestSource6ScopedReferenceInventory(t *testing.T) {
	t.Parallel()
	attr := func(group string) TypedSourcePropertySelector {
		return TypedSourcePropertySelector{Kind: "attributes", Group: group}
	}
	facet := func(group string) TypedSourcePropertySelector {
		return TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql/orm~1", Group: group}
	}
	const root = "/attributes/facets/sql~1orm~01"
	const nodeA = `{"localKey":"node-a"}`
	const nodeB = `{"localKey":"node-b"}`
	ref := func(path, kind, key string, property TypedSourcePropertySelector) sourceContractReference {
		return sourceContractReference{path, "node", kind, "same_repository", key, property, nil}
	}
	for _, tc := range []struct {
		name, kind, attrs string
		edge, parent      bool
		want              []sourceContractReference
	}{
		{"parent", "handler", `{}`, false, true, []sourceContractReference{ref("/parentId", "", "node-a", TypedSourcePropertySelector{Kind: "parent"})}},
		{"flow entry and ordered exits", "flow", `{"analysisStatus":"complete","gaps":[],"entryStepRef":` + nodeA + `,"exitStepRefs":[` + nodeB + `,` + nodeA + `],"exitStatus":"complete"}`, false, false, []sourceContractReference{ref("/attributes/entryStepId", "flow_step", "node-a", attr("entryStepId")), ref("/attributes/exitStepIds/0", "flow_step", "node-b", attr("exitStepIds")), ref("/attributes/exitStepIds/1", "flow_step", "node-a", attr("exitStepIds"))}},
		{"known transaction context", "flow_step", `{"analysisStatus":"complete","gaps":[],"stepKind":"transform","inputs":[],"outputs":[],"transactionContext":{"status":"known","transactionRef":` + nodeA + `},"nativeText":"keep nodeKey, facetKey and portKey"}`, false, false, []sourceContractReference{ref("/attributes/transactionContext/transactionId", "transaction", "node-a", attr("transactionContext"))}},
		{"transaction datastore", "transaction", `{"analysisStatus":"complete","gaps":[],"datastoreRef":` + nodeA + `,"connectionScope":{"status":"known","value":"shared"},"isolationLevel":{"status":"known","value":"serializable"},"boundaryStatus":"complete"}`, false, false, []sourceContractReference{ref("/attributes/datastoreId", "datastore", "node-a", attr("datastoreId"))}},
		{"read datastore", "reads", `{"datastoreRef":` + nodeA + `,"accessMode":"read","facetKey":"node-b","columnScope":"listed"}`, true, false, []sourceContractReference{ref("/attributes/datastoreId", "datastore", "node-a", attr("datastoreId"))}},
		{"write datastore", "writes", `{"datastoreRef":` + nodeA + `,"accessMode":"update","facetKey":"node-b","columnScope":"listed"}`, true, false, []sourceContractReference{ref("/attributes/datastoreId", "datastore", "node-a", attr("datastoreId"))}},
		{"delete datastore", "deletes", `{"datastoreRef":` + nodeA + `,"accessMode":"delete","facetKey":"node-b","columnScope":"listed"}`, true, false, []sourceContractReference{ref("/attributes/datastoreId", "datastore", "node-a", attr("datastoreId"))}},
		{"facet ordered columns", "constraint", sourceContractFacet(`"constraintKind":"unique","columnRefs":[` + nodeB + `,` + nodeA + `],"expression":{"status":"known","value":null},"nativeDefinition":"UNIQUE(b,a)","deferrable":{"status":"known","value":false},"initiallyDeferred":{"status":"known","value":false}`), false, false, []sourceContractReference{ref(root+"/columnIds/0", "column", "node-b", facet("columnIds")), ref(root+"/columnIds/1", "column", "node-a", facet("columnIds"))}},
		{"view dependencies", "view", sourceContractFacet(`"qualifiedName":"v","materialized":false,"definition":"SELECT sourceKey FROM nodeKey","dependencyRefs":[` + nodeA + `,` + nodeB + `],"dependenciesStatus":"complete"`), false, false, []sourceContractReference{ref(root+"/dependencyIds/0", "dependency", "node-a", facet("dependencyIds")), ref(root+"/dependencyIds/1", "dependency", "node-b", facet("dependencyIds"))}},
		{"index term", "index", sourceContractFacet(`"terms":[{"columnRef":` + nodeA + `,"direction":"asc","nulls":"last"},{"expression":"lower(nodeKey)","direction":"desc","nulls":"first"}],"unique":{"status":"known","value":true},"predicate":{"status":"known","value":null},"method":{"status":"known","value":"btree"},"nativeDefinition":"CREATE INDEX nodeKey"`), false, false, []sourceContractReference{ref(root+"/terms/0/columnId", "column", "node-a", facet("terms"))}},
		{"foreign key pairs", "references", sourceContractFacet(`"columnPairs":[{"fromColumnRef":` + nodeA + `,"toColumnRef":` + nodeB + `}],"updateAction":{"status":"known","value":"cascade"},"deleteAction":{"status":"known","value":"restrict"},"matchType":{"status":"known","value":"simple"}`), true, false, []sourceContractReference{ref(root+"/columnPairs/0/fromColumnId", "column", "node-a", facet("columnPairs")), ref(root+"/columnPairs/0/toColumnId", "column", "node-b", facet("columnPairs"))}},
		{"migration parent candidate and historical", "migration", sourceContractFacet(`"order":{"status":"known","value":2},"parentRefs":[` + nodeA + `],"definition":"ALTER TABLE nodeKey","derivationStatus":"complete","changes":[{"target":{"kind":"candidate","objectRef":` + nodeB + `},"operation":"alter","description":"candidate"},{"target":{"kind":"historical","revisionId":"66666666-6666-4666-8666-666666666666","objectId":"33333333-3333-4333-8333-333333333333"},"operation":"drop","description":"historical"},{"target":{"kind":"source_only","externalKey":"node-a","expectedKind":"table","qualifiedName":"node-a","reason":"not observed"},"operation":"alter","description":"descriptor"}]`), false, false, []sourceContractReference{ref(root+"/parentIds/0", "migration", "node-a", facet("parentIds")), ref(root+"/changes/0/target/objectId", "relational", "node-b", facet("changes"))}},
		{"routine descriptor root", "symbol", `{"databaseRoutine":` + sourceContractFacet(`"routineKind":"function","qualifiedName":"f","definition":"SELECT nodeKey","bodyStatus":"complete","dependencyRefs":[`+nodeB+`]`) + `}`, false, false, []sourceContractReference{ref("/attributes/databaseRoutine/facets/sql~1orm~01/dependencyIds/0", "dependency", "node-b", facet("dependencyIds"))}},
		{"datastore descriptor proof is local", "datastore", `{"relational":` + sourceContractFacet(`"databaseName":"main","qualifiedName":"main","nativeDefinition":"CREATE DATABASE main"`) + `}`, false, false, nil},
		{"emit channel", "emits", `{"channelRef":` + nodeA + `,"deliveryStatus":"declared"}`, true, false, []sourceContractReference{ref("/attributes/channelId", "channel", "node-a", attr("channelId"))}},
		{"delivery message", "delivered_to", `{"messageRef":` + nodeB + `,"condition":{"status":"known","value":"always"},"group":{"status":"known","value":"g"},"deliveryStatus":"declared"}`, true, false, []sourceContractReference{ref("/attributes/messageId", "message", "node-b", attr("messageId"))}},
		{"retry message", "retries", `{"messageRef":` + nodeB + `,"reason":"retry","delay":{"status":"known","value":"1s"},"maxAttempts":{"status":"known","value":"3"}}`, true, false, []sourceContractReference{ref("/attributes/messageId", "message", "node-b", attr("messageId"))}},
		{"dead letter message", "dead_letters", `{"messageRef":` + nodeB + `,"reason":"exhausted"}`, true, false, []sourceContractReference{ref("/attributes/messageId", "message", "node-b", attr("messageId"))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "subject", Kind: tc.kind, Name: "subject", Attributes: sourceContractAttrs(t, tc.attrs)}}
			want := append([]sourceContractReference{}, tc.want...)
			if tc.parent {
				c.Node.ParentRef = &ImportRecordRef{LocalKey: "node-a"}
			}
			if tc.edge {
				c = ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "subject", Kind: tc.kind, FromRef: &ImportRecordRef{LocalKey: "node-a"}, ToRef: &ImportRecordRef{LocalKey: "node-b"}, Attributes: sourceContractAttrs(t, tc.attrs)}}
				relation := "same_repository"
				if tc.kind == "emits" || tc.kind == "delivered_to" || tc.kind == "retries" || tc.kind == "dead_letters" {
					relation = "explicit"
					for i := range want {
						want[i].relation = "explicit"
					}
				}
				want = append([]sourceContractReference{{"/from", "node", "", relation, "node-a", TypedSourcePropertySelector{Kind: "edge_endpoints"}, nil}, {"/to", "node", "", relation, "node-b", TypedSourcePropertySelector{Kind: "edge_endpoints"}, nil}}, want...)
			}
			sourceContractNormalize(t, c, want)
		})
	}
}

func sourceContractFacet(members string) string {
	return `{"facets":{"sql/orm~1":{"sourceKind":"sql","dialect":"postgresql","analysisStatus":"complete","gaps":[],"evidenceKeys":["proof-local"],` + members + `}}}`
}

func TestSource6ScopedMappingContexts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, destination string
		contexts                  []LineageValueRef
	}{
		{"column facet", `{"kind":"column","nodeRef":{"localKey":"node-a"},"facetKey":"node-b"}`, `{"kind":"api_field","nodeRef":{"localKey":"node-b"}}`, []LineageValueRef{{Kind: "column", NodeID: sourceContractNode, FacetKey: "node-b"}, {Kind: "api_field", NodeID: sourceContractOther}}},
		{"port subaddress", `{"kind":"port","nodeRef":{"localKey":"node-a"},"collection":"outputs","portKey":"node-b"}`, `{"kind":"port","nodeRef":{"localKey":"node-b"},"collection":"parameters","portKey":"arg/1"}`, []LineageValueRef{{Kind: "port", NodeID: sourceContractNode, Collection: "outputs", PortKey: "node-b"}, {Kind: "port", NodeID: sourceContractOther, Collection: "parameters", PortKey: "arg/1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attrs := `{"analysisStatus":"complete","gaps":[],"sources":[` + tc.source + `],"destination":` + tc.destination + `,"transform":{"kind":"copy","description":"exact","redacted":false}}`
			want := make([]sourceContractReference, 0, 2)
			for i, path := range []string{"/attributes/sources/0/nodeId", "/attributes/destination/nodeId"} {
				kind := tc.contexts[i].Kind
				if kind == "port" {
					kind = "port_owner"
				}
				property := TypedSourcePropertySelector{Kind: "mapping_sources"}
				key := "node-a"
				if i == 1 {
					property.Kind = "mapping_destination"
					key = "node-b"
				}
				want = append(want, sourceContractReference{path, "node", kind, "explicit", key, property, &tc.contexts[i]})
			}
			sourceContractNormalize(t, ImportCommand{Op: "upsert_node", Node: &ImportNode{Kind: "field_mapping", Name: "map", Attributes: sourceContractAttrs(t, attrs)}}, want)
		})
	}
	t.Run("event endpoints routes and transport", func(t *testing.T) {
		attrs := `{"analysisStatus":"complete","gaps":[],"sources":[{"kind":"event_field","nodeRef":{"localKey":"node-a"},"endpointRef":{"localKey":"node-b"},"routeRef":{"localKey":"edge-a"}}],"destination":{"kind":"event_field","nodeRef":{"localKey":"node-b"},"endpointRef":{"localKey":"node-a"},"routeRef":{"localKey":"edge-b"}},"transform":{"kind":"copy","description":"exact","redacted":false},"transport":{"emitsEdgeRef":{"localKey":"edge-a"},"deliveryEdgeRef":{"localKey":"edge-b"}}}`
		source := &LineageValueRef{Kind: "event_field", NodeID: sourceContractNode, EndpointID: sourceContractOther, RouteID: sourceContractSnapshot}
		dest := &LineageValueRef{Kind: "event_field", NodeID: sourceContractOther, EndpointID: sourceContractNode, RouteID: sourceContractBase}
		want := []sourceContractReference{
			{"/attributes/sources/0/nodeId", "node", "event_field", "explicit", "node-a", TypedSourcePropertySelector{Kind: "mapping_sources"}, source},
			{"/attributes/sources/0/endpointId", "node", "event_endpoint", "explicit", "node-b", TypedSourcePropertySelector{Kind: "mapping_sources"}, source},
			{"/attributes/sources/0/routeId", "edge", "event_route", "explicit", "edge-a", TypedSourcePropertySelector{Kind: "mapping_sources"}, source},
			{"/attributes/destination/nodeId", "node", "event_field", "explicit", "node-b", TypedSourcePropertySelector{Kind: "mapping_destination"}, dest},
			{"/attributes/destination/endpointId", "node", "event_endpoint", "explicit", "node-a", TypedSourcePropertySelector{Kind: "mapping_destination"}, dest},
			{"/attributes/destination/routeId", "edge", "event_route", "explicit", "edge-b", TypedSourcePropertySelector{Kind: "mapping_destination"}, dest},
			{"/attributes/transport/emitsEdgeId", "edge", "emits", "explicit", "edge-a", TypedSourcePropertySelector{Kind: "attributes", Group: "transport"}, nil},
			{"/attributes/transport/deliveryEdgeId", "edge", "delivered_to", "explicit", "edge-b", TypedSourcePropertySelector{Kind: "attributes", Group: "transport"}, nil},
		}
		sourceContractNormalize(t, ImportCommand{Op: "upsert_node", Node: &ImportNode{Kind: "field_mapping", Name: "map", Attributes: sourceContractAttrs(t, attrs)}}, want)
	})
}

func sourceContractNormalize(t *testing.T, c ImportCommand, want []sourceContractReference) {
	t.Helper()
	for _, basis := range []string{"candidate", "base"} {
		t.Run(basis, func(t *testing.T) { sourceContractNormalizeBasis(t, c, want, basis) })
	}
}

func sourceContractNormalizeBasis(t *testing.T, c ImportCommand, want []sourceContractReference, basis string) {
	t.Helper()
	targets := map[string]BaseAssertionRef{
		"node-a": {RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "node-a", ExpectedID: sourceContractNode, AssertionHash: strings.Repeat("a", 64)},
		"node-b": {RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "node-b", ExpectedID: sourceContractOther, AssertionHash: strings.Repeat("b", 64)},
		"edge-a": {RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "edge", ExternalKey: "edge-a", ExpectedID: sourceContractSnapshot, AssertionHash: strings.Repeat("c", 64)},
		"edge-b": {RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "edge", ExternalKey: "edge-b", ExpectedID: sourceContractBase, AssertionHash: strings.Repeat("d", 64)},
	}
	if basis == "base" {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		for key, target := range targets {
			replacement, err := json.Marshal(ImportRecordRef{Base: &target})
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(strings.ReplaceAll(string(raw), `{"localKey":"`+key+`"}`, string(replacement)))
		}
		var scoped ImportCommand
		if err := json.Unmarshal(raw, &scoped); err != nil {
			t.Fatal(err)
		}
		c = scoped
	}
	var seen []SourceReferenceSite
	before, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	p, bindings, err := normalizeSourcePayload(c, &ImportSession{SnapshotID: sourceContractSnapshot, BaseRevisionID: sourceContractBase}, func(site SourceReferenceSite) (BaseAssertionRef, error) {
		seen = append(seen, site)
		key := site.Ref.LocalKey
		if site.Ref.Base != nil {
			key = site.Ref.Base.ExternalKey
		}
		target, ok := targets[key]
		if !ok {
			return BaseAssertionRef{}, fmt.Errorf("unexpected lookup %q", key)
		}
		if basis == "base" && (site.Ref.Base == nil || *site.Ref.Base != target || site.Ref.LocalKey != "") {
			return BaseAssertionRef{}, fmt.Errorf("base tag changed: %+v", site.Ref)
		}
		return target, nil
	}, func(key string) (string, error) {
		if key != "proof-local" {
			return "", fmt.Errorf("foreign evidence %q", key)
		}
		return sourceContractProof, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !sourceContractJSONEqual(t, before, after) {
		t.Error("normalizing mutated the input command")
	}
	if len(seen) != len(want) || len(bindings) != len(want) {
		t.Fatalf("got %d sites and %d bindings, want %d: %+v", len(seen), len(bindings), len(want), seen)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	seenByPath := make(map[string]SourceReferenceSite, len(seen))
	bindingsByPath := make(map[string]SourceDependencyBinding, len(bindings))
	for _, site := range seen {
		if _, exists := seenByPath[site.Path]; exists {
			t.Errorf("duplicate reference site %s", site.Path)
		}
		seenByPath[site.Path] = site
	}
	for _, binding := range bindings {
		if _, exists := bindingsByPath[binding.Site]; exists {
			t.Errorf("duplicate binding site %s", binding.Site)
		}
		bindingsByPath[binding.Site] = binding
	}
	for i, expected := range want {
		got := seenByPath[expected.path]
		key := got.Ref.LocalKey
		if got.Ref.Base != nil {
			key = got.Ref.Base.ExternalKey
		}
		if got.Path != expected.path || got.RecordType != expected.recordType || got.ExpectedKind != expected.kind || got.Relation != expected.relation || key != expected.key || got.Property != expected.property {
			t.Errorf("site[%d] = %+v, want %+v", i, got, expected)
		}
		binding := bindingsByPath[expected.path]
		revision := ""
		if basis == "base" {
			revision = sourceContractBase
		}
		if binding.Basis != basis || binding.BaseRevisionID != revision || binding.Site != expected.path || binding.Target != targets[expected.key] || binding.Property != expected.property || !reflect.DeepEqual(binding.ValueContext, expected.context) {
			t.Errorf("binding[%d] = %+v, want site=%s context=%+v", i, binding, expected.path, expected.context)
		}
		value := sourceContractJSONPointer(t, raw, expected.path)
		var id string
		if err := json.Unmarshal(value, &id); err != nil {
			t.Fatal(err)
		}
		if id != targets[expected.key].ExpectedID {
			t.Errorf("normalized %s = %s", expected.path, id)
		}
	}
}

func sourceContractJSONPointer(t *testing.T, raw []byte, path string) jsontext.Value {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			value = current[part]
		case []any:
			var index int
			if _, err := fmt.Sscanf(part, "%d", &index); err != nil || index < 0 || index >= len(current) {
				t.Fatalf("invalid pointer %s", path)
			}
			value = current[index]
		default:
			t.Fatalf("missing pointer %s", path)
		}
	}
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSource6ExactAssertionRefDoesNotInferNames(t *testing.T) {
	t.Parallel()
	a := sourceContractHashGraph(t).Assertions[0]
	ref := BaseAssertionRef{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "provider:save", ExpectedID: sourceContractNode, AssertionHash: a.AssertionHash}
	if got, err := exactSourceAssertion([]ProviderAssertion{a}, ref); err != nil || got.RecordID != sourceContractNode {
		t.Fatalf("exact reference: %+v %v", got, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*BaseAssertionRef)
	}{
		{"repository", func(r *BaseAssertionRef) { r.RepositoryID = sourceContractOther }}, {"provider", func(r *BaseAssertionRef) { r.ProviderNamespace = "provider-b" }},
		{"record type", func(r *BaseAssertionRef) { r.RecordType = "edge" }}, {"key equals name", func(r *BaseAssertionRef) { r.ExternalKey = "save" }},
		{"UUID", func(r *BaseAssertionRef) { r.ExpectedID = sourceContractOther }}, {"hash", func(r *BaseAssertionRef) { r.AssertionHash = strings.Repeat("b", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := ref
			tc.change(&bad)
			if _, err := exactSourceAssertion([]ProviderAssertion{a}, bad); err == nil {
				t.Error("resolved inexact claim using equal name or partial identity")
			}
		})
	}
}

func TestSource6ReferenceExpectedKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expected, kind string
		allowed              bool
	}{
		{"column", "column", "column", true}, {"table is not column", "column", "table", false},
		{"dependency table", "dependency", "table", true}, {"dependency view", "dependency", "view", true},
		{"dependency column", "dependency", "column", true}, {"dependency symbol", "dependency", "symbol", true},
		{"dependency handler", "dependency", "handler", false},
		{"step port", "port_owner", "flow_step", true}, {"query port", "port_owner", "query", true},
		{"handler port", "port_owner", "handler", false},
		{"consumer endpoint", "event_endpoint", "consumer", true}, {"emit endpoint", "event_endpoint", "flow_step", true},
		{"message is not endpoint", "event_endpoint", "message", false},
		{"emit route", "event_route", "emits", true}, {"delivery route", "event_route", "delivered_to", true},
		{"retry is not contextual route", "event_route", "retries", false},
		{"migration parent", "migration", "migration", true}, {"table is not migration", "migration", "table", false},
		{"datastore", "datastore", "datastore", true}, {"schema is not datastore", "datastore", "db_schema", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sourceExpectedKind(tc.expected, ProviderAssertion{RecordType: "node", Payload: SourceAssertionPayload{Kind: tc.kind}})
			if got != tc.allowed {
				t.Errorf("target kind %s for %s = %v, want %v", tc.kind, tc.expected, got, tc.allowed)
			}
		})
	}
}

func TestSource6ScopedReferenceRejectsMalformedUnion(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{}`, `null`, `{"localKey":null}`, `{"localKey":"node-a","base":null}`,
		`{"localKey":"node-a","site":"/parentId"}`, `{"localKey":"node-a","property":{"kind":"parent"}}`,
		`{"localKey":"node-a","valueContext":{"kind":"column"}}`, `{"localKey":"node-a","basis":"candidate"}`,
		`{"base":{"repositoryId":"11111111-1111-4111-8111-111111111111","providerNamespace":"provider-a","recordType":"node","externalKey":"node-a","expectedId":"22222222-2222-4222-8222-222222222222","assertionHash":"bad"}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var ref ImportRecordRef
			if err := json.Unmarshal([]byte(raw), &ref); err == nil {
				t.Error("accepted malformed or server-only binding members")
			}
		})
	}
}

func TestSource6NestedRefsRejectPersistedUUIDBypass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, kind, attrs string }{
		{"flow UUID", "flow", `{"analysisStatus":"complete","gaps":[],"entryStepId":"22222222-2222-4222-8222-222222222222","exitStepRefs":[],"exitStatus":"complete"}`},
		{"known transaction UUID", "flow_step", `{"analysisStatus":"complete","gaps":[],"stepKind":"transform","inputs":[],"outputs":[],"transactionContext":{"status":"known","transactionId":"22222222-2222-4222-8222-222222222222"},"nativeText":"noop"}`},
		{"mapping UUID", "field_mapping", `{"analysisStatus":"complete","gaps":[],"sources":[],"destination":{"kind":"column","nodeId":"22222222-2222-4222-8222-222222222222","facetKey":"sql"},"transform":{"kind":"constant","description":"1","redacted":false}}`},
		{"mapping mixed local key", "field_mapping", `{"analysisStatus":"complete","gaps":[],"sources":[],"destination":{"kind":"column","nodeRef":{"localKey":"node-a"},"nodeKey":"node-a","facetKey":"sql"},"transform":{"kind":"constant","description":"1","redacted":false}}`},
		{"facet UUID", "constraint", sourceContractFacet(`"constraintKind":"unique","columnIds":["22222222-2222-4222-8222-222222222222"],"expression":{"status":"known","value":null},"nativeDefinition":"UNIQUE(id)","deferrable":{"status":"known","value":false},"initiallyDeferred":{"status":"known","value":false}`)},
		{"index term UUID", "index", sourceContractFacet(`"terms":[{"columnId":"22222222-2222-4222-8222-222222222222","direction":"asc","nulls":"last"}],"unique":{"status":"known","value":true},"predicate":{"status":"known","value":null},"method":{"status":"known","value":"btree"},"nativeDefinition":"CREATE INDEX id"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := ImportCommand{Op: "upsert_node", Node: &ImportNode{Kind: tc.kind, Name: "subject", Attributes: sourceContractAttrs(t, tc.attrs)}}
			_, _, err := normalizeSourcePayload(command, &ImportSession{SnapshotID: sourceContractSnapshot, BaseRevisionID: sourceContractBase}, func(SourceReferenceSite) (BaseAssertionRef, error) {
				return BaseAssertionRef{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "node-a", ExpectedID: sourceContractNode, AssertionHash: strings.Repeat("a", 64)}, nil
			}, func(string) (string, error) { return sourceContractProof, nil })
			if err == nil {
				t.Error("fresh input bypassed scoped reference with a persisted UUID or old key")
			}
		})
	}
}

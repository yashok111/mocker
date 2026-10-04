package backendmodel

import "testing"

func TestSource6ExactEventClaimContexts(t *testing.T) {
	for _, mode := range []string{"valid", "wrong_endpoint_claim", "wrong_route_claim", "wrong_field_claim"} {
		t.Run(mode, func(t *testing.T) {
			message, field, endpoint, route, channel := structuralID("message"), structuralID("field"), structuralID("endpoint"), structuralID("route"), structuralID("channel")
			owner := AssertionOwnership{RepositoryID: sourceContractRepo, ProviderNamespace: "bound-owner", Profile: EventsProfile}
			fieldClaim := ProviderAssertion{RecordType: "node", RecordID: field, Owner: owner, ExternalKey: "field", AssertionHash: fixtureHash, Payload: SourceAssertionPayload{RecordType: "node", Kind: "event_field", ParentID: &message}}
			endpointClaim := ProviderAssertion{RecordType: "node", RecordID: endpoint, Owner: owner, ExternalKey: "endpoint", AssertionHash: fixtureHash, Payload: SourceAssertionPayload{RecordType: "node", Kind: "flow_step", Attributes: runtimeAttrs(t, map[string]any{"stepKind": "emit"})}}
			routeClaim := ProviderAssertion{RecordType: "edge", RecordID: route, Owner: owner, ExternalKey: "route", AssertionHash: fixtureHash, Payload: SourceAssertionPayload{RecordType: "edge", Kind: "emits", From: endpoint, To: message, Attributes: runtimeAttrs(t, map[string]any{"channelId": channel})}}
			switch mode {
			case "wrong_endpoint_claim":
				endpointClaim.Payload.Attributes = runtimeAttrs(t, map[string]any{"stepKind": "transform"})
			case "wrong_route_claim":
				routeClaim.Payload.To = structuralID("other-message")
			case "wrong_field_claim":
				fieldClaim.Payload.ParentID = new(structuralID("other-message"))
			}
			destination := ProviderAssertion{RecordType: "node", RecordID: sourceContractNode, Owner: owner, ExternalKey: "destination", AssertionHash: fixtureHash, Payload: SourceAssertionPayload{RecordType: "node", Kind: "api_field"}}
			ref := LineageValueRef{Kind: "event_field", NodeID: field, EndpointID: endpoint, RouteID: route}
			a := ProviderAssertion{RecordType: "node", RecordID: sourceContractOther, Owner: owner, Payload: SourceAssertionPayload{RecordType: "node", Kind: "field_mapping", Name: "mapping", Attributes: runtimeAttrs(t, map[string]any{"sources": []LineageValueRef{ref}, "destination": LineageValueRef{Kind: "api_field", NodeID: destination.RecordID}, "transform": LineageTransform{Kind: "copy", Description: "declared mapping"}, "analysisStatus": "complete", "gaps": []string{}})}}
			targets := map[string]ProviderAssertion{"/attributes/sources/0/nodeId": fieldClaim, "/attributes/sources/0/endpointId": endpointClaim, "/attributes/sources/0/routeId": routeClaim, "/attributes/destination/nodeId": destination}
			claims := map[string]ProviderAssertion{}
			for site, target := range targets {
				property := TypedSourcePropertySelector{Kind: "mapping_sources"}
				context := new(ref)
				if site == "/attributes/destination/nodeId" {
					property.Kind = "mapping_destination"
					context = new(LineageValueRef{Kind: "api_field", NodeID: destination.RecordID})
				}
				a.DependencyClaims = append(a.DependencyClaims, SourceDependencyBinding{Basis: "base", BaseRevisionID: sourceContractBase, Target: sourceAssertionRef(target), Site: site, Property: property, ValueContext: context})
				claims[sourceAssertionKey(target)] = target
			}
			err := validateSourceValueContexts(a, func(binding SourceDependencyBinding) (ProviderAssertion, error) {
				return exactSourceAssertion([]ProviderAssertion{claims[sourceClaimKey(binding.Target.RecordType, binding.Target.ExpectedID, binding.Target.RepositoryID, binding.Target.ProviderNamespace)]}, binding.Target)
			})
			if mode == "valid" && err != nil {
				t.Fatal(err)
			}
			if mode != "valid" && err == nil {
				t.Fatal("event tuple borrowed the merged provider's endpoint or route payload")
			}
		})
	}
}

func TestSource6MissingSelectedCurrentnessIsUnsafe(t *testing.T) {
	f := sourceReviewSharedValue(t, "column", "borrow")
	graph, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, f.project.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]ProviderAssertion{}
	current := map[string]SourceClaimCurrentness{}
	for _, a := range graph.Assertions {
		claims[sourceAssertionKey(a)] = a
		fresh := sourceCurrentness(a)
		fresh.Fields = nil
		current[sourceAssertionKey(a)] = fresh
	}
	binding := SourceDependencyBinding{Basis: "base", BaseRevisionID: f.project.CurrentRevisionID, Target: f.b, ValueContext: &LineageValueRef{Kind: "column", NodeID: f.b.ExpectedID, FacetKey: "sql"}}
	if !sourceDependencyUnsafe(binding, claims, current) {
		t.Fatal("missing selected field currentness implied current proof")
	}
}

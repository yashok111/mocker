package backendmodel

import (
	"bytes"
	"maps"
	"slices"
	"strings"
)

type sourceBindingLookup func(SourceDependencyBinding) (ProviderAssertion, error)

// Only freshly normalized assertions are admitted here. Retained assertions keep
// their original pins and are classified by currentness if targets changed.
func (p *composedGraphPreparation) validateFreshReferenceContexts() error {
	lookup := func(binding SourceDependencyBinding) (ProviderAssertion, error) {
		if binding.Basis == "base" {
			if binding.BaseRevisionID != p.session.BaseRevisionID {
				return ProviderAssertion{}, semantic(binding.Site, "Fresh base reference must use the pinned import base")
			}
			return exactSourceAssertion(p.base.Assertions, binding.Target)
		}
		key := sourceClaimKey(binding.Target.RecordType, binding.Target.ExpectedID, binding.Target.RepositoryID, binding.Target.ProviderNamespace)
		target, ok := p.claims[key]
		if !ok || sourceAssertionRef(target) != binding.Target {
			return ProviderAssertion{}, semantic(binding.Site, "Candidate reference must match its final exact claim")
		}
		return target, nil
	}
	for _, a := range p.candidate.Source.Assertions {
		if !p.fresh[sourceAssertionKey(a)] {
			continue
		}
		retained := sourceRetainedContextFacets(a, p.originalClaims[sourceAssertionKey(a)])
		if err := validateSourceReferenceContexts(a, lookup, retained); err != nil {
			return err
		}
	}
	return nil
}

func validateSourceValueContexts(a ProviderAssertion, lookup sourceBindingLookup) error {
	return validateSourceReferenceContexts(a, lookup, nil)
}

func sourceRetainedContextFacets(a, old ProviderAssertion) map[string]bool {
	retained := map[string]bool{}
	if !relationalSubject(a.Payload.Kind, a.Payload.Attributes, a.RecordType == "edge") {
		return retained
	}
	current, _, err := relationalFacetObject(a.Payload.Kind, a.Payload.Attributes)
	if err != nil {
		return retained
	}
	prior, _, err := relationalFacetObject(old.Payload.Kind, old.Payload.Attributes)
	if err != nil {
		return retained
	}
	for key, raw := range current {
		if bytes.Equal(raw, prior[key]) {
			retained[key] = true
		}
	}
	return retained
}

func validateSourceReferenceContexts(a ProviderAssertion, lookup sourceBindingLookup, retained map[string]bool) error {
	if err := validateSourceBindings(a); err != nil {
		return err
	}
	targets := map[string]ProviderAssertion{}
	contexts := map[string]LineageValueRef{}
	for _, binding := range a.DependencyClaims {
		if binding.Property.Kind == "relational_facet" && retained[binding.Property.FacetKey] {
			continue
		}
		target, err := lookup(binding)
		if err != nil {
			return err
		}
		targets[binding.Site] = target
		if binding.ValueContext != nil {
			root, _, ok := strings.CutLast(binding.Site, "/")
			if !ok {
				return semantic(binding.Site, "Value reference site is invalid")
			}
			contexts[root] = *binding.ValueContext
		}
	}
	if err := validateSourceBoundRecordContexts(a, targets, retained); err != nil {
		return err
	}
	for _, root := range slices.Sorted(maps.Keys(contexts)) {
		if err := validateSourceValueContext(contexts[root], root, targets); err != nil {
			return err
		}
	}
	if a.Payload.Kind == "field_mapping" {
		return validateSourceTransportContext(a, contexts, targets)
	}
	return nil
}

func sourceClaimNode(a ProviderAssertion) Node {
	return Node{ID: a.RecordID, Kind: a.Payload.Kind, Name: a.Payload.Name, ParentID: a.Payload.ParentID, Attributes: a.Payload.Attributes}
}

func validateSourceValueContext(value LineageValueRef, root string, targets map[string]ProviderAssertion) error {
	target, exists := targets[root+"/nodeId"]
	if !exists || target.RecordType != "node" || target.RecordID != value.NodeID {
		return semantic(root, "Value must identify its exact bound node claim")
	}
	if value.Kind != "event_field" {
		if err := validateLineageValueTargetForSchema(value, map[string]Node{target.RecordID: sourceClaimNode(target)}, nil, ComposedSchemaVersion); err != nil {
			return semantic(root, "Requested value kind, facet or port is absent from its exact owner claim")
		}
		return nil
	}
	endpoint, epExists := targets[root+"/endpointId"]
	route, routeExists := targets[root+"/routeId"]
	if !epExists || !routeExists || endpoint.RecordType != "node" || route.RecordType != "edge" || endpoint.RecordID != value.EndpointID || route.RecordID != value.RouteID {
		return semantic(root, "Event value requires exact endpoint and route claims")
	}
	return validateSourceEventTuple(target, endpoint, route, root)
}

func validateSourceEventTuple(field, endpoint, route ProviderAssertion, path string) error {
	if field.Payload.Kind != "event_field" || field.Payload.ParentID == nil {
		return semantic(path, "Exact event field claim must identify its message")
	}
	message := *field.Payload.ParentID
	valid := false
	switch route.Payload.Kind {
	case "emits":
		valid = eventsEmit(endpoint.Payload.Kind, endpoint.Payload.Attributes, false) && route.Payload.From == endpoint.RecordID && route.Payload.To == message && ValidID(runtimeString(route.Payload.Attributes["channelId"]))
	case "delivered_to":
		valid = endpoint.Payload.Kind == "consumer" && route.Payload.To == endpoint.RecordID && runtimeString(route.Payload.Attributes["messageId"]) == message && ValidID(route.Payload.From)
	}
	if !valid {
		return semantic(path, "Exact field, endpoint and route claims do not establish this event value")
	}
	return nil
}

func validateSourceTransportContext(a ProviderAssertion, contexts map[string]LineageValueRef, targets map[string]ProviderAssertion) error {
	if _, hasTransport := a.Payload.Attributes["transport"]; !hasTransport {
		return nil
	}
	emits, ok := targets["/attributes/transport/emitsEdgeId"]
	if !ok {
		return semantic("transport", "Missing exact emission claim")
	}
	delivery, ok := targets["/attributes/transport/deliveryEdgeId"]
	if !ok {
		return semantic("transport", "Missing exact delivery claim")
	}
	destination := contexts["/attributes/destination"]
	if emits.Payload.Kind != "emits" || delivery.Payload.Kind != "delivered_to" || destination.Kind != "event_field" || destination.RouteID != delivery.RecordID || destination.EndpointID != delivery.Payload.To || runtimeString(emits.Payload.Attributes["channelId"]) != delivery.Payload.From || emits.Payload.To != runtimeString(delivery.Payload.Attributes["messageId"]) {
		return semantic("transport", "Exact transport claims must establish the same message/channel/consumer tuple")
	}
	for root, ref := range contexts {
		if root == "/attributes/destination" {
			continue
		}
		field := targets[root+"/nodeId"]
		if ref.Kind != "event_field" || ref.EndpointID != emits.Payload.From || ref.RouteID != emits.RecordID || field.Payload.ParentID == nil || *field.Payload.ParentID != emits.Payload.To {
			return semantic("transport", "Exact transport sources must belong to their bound emission")
		}
	}
	return nil
}

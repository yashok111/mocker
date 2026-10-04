package backendmodel

import (
	"encoding/json/v2"
	"slices"
)

func sourceStaleReason(f AssertionFreshness, reason string) AssertionFreshness {
	f.Status = "stale"
	f.Reasons = append(slices.Clone(f.Reasons), reason)
	slices.Sort(f.Reasons)
	f.Reasons = slices.Compact(f.Reasons)
	return f
}

func validateSourceOwnProof(s *ImportSession, a ProviderAssertion, graph *SourceGraphSnapshot, fresh bool, retained map[string]bool) error {
	if len(a.EvidenceIDs) == 0 && a.Payload.Kind != "unresolved_target" {
		return semantic("evidenceIds", "Known assertion requires its own source evidence")
	}
	for _, id := range a.EvidenceIDs {
		e, err := sourceOwnedProof(a, graph, id)
		if err != nil {
			return err
		}
		if err := validateSourceLocator(e, a.Owner, graph); err != nil {
			return err
		}
		index, err := sourceProofs(graph)
		if err != nil {
			return err
		}
		_, legacy := index.legacy[id]
		if fresh && !retained[id] {
			if e.Source.SnapshotID != s.SnapshotID {
				return semantic("evidence.source", "Fresh assertion requires reobserved own proof in selected snapshot")
			}
			if legacy {
				return semantic("evidence", "Fresh proof cannot inherit legacy admission")
			}
		}
		if err := validateSourceProofProperty(a, e, legacy); err != nil {
			return err
		}
	}
	return validateSourceFacetProof(a, graph)
}

func sourceOwnedProof(a ProviderAssertion, graph *SourceGraphSnapshot, id string) (Evidence, error) {
	index, err := sourceProofs(graph)
	if err != nil {
		return Evidence{}, err
	}
	e, ok := index.evidence[id]
	if !ok {
		return e, semantic("evidenceIds", "Assertion evidence is absent")
	}
	if e.SubjectID != a.RecordID || e.Source.RepositoryID != a.Owner.RepositoryID {
		return e, semantic("evidenceIds", "Evidence must belong to the assertion subject and repository")
	}
	if e.Ownership != nil && (e.Ownership.RepositoryID != a.Owner.RepositoryID || e.Ownership.ProviderNamespace != a.Owner.ProviderNamespace) {
		return e, semantic("evidenceIds", "Proof cannot be borrowed from another provider")
	}
	return e, nil
}

func validateSourceLocator(e Evidence, owner AssertionOwnership, graph *SourceGraphSnapshot) error {
	index, err := sourceProofs(graph)
	if err != nil {
		return err
	}
	snapshot, exists := index.owners[e.Source.SnapshotID]
	if !exists || snapshot.RepositoryID != owner.RepositoryID || snapshot.ProviderNamespace != owner.ProviderNamespace {
		return semantic("evidence.source", "Proof snapshot is absent from exact owner vector")
	}
	if !index.files[sourceProofFileKey(e.Source.SnapshotID, e.Source.File, e.Source.ContentHash)] {
		return semantic("evidence.source", "Proof must address an analyzed manifest member")
	}
	return nil
}

func validateSourceProofProperty(a ProviderAssertion, e Evidence, legacy bool) error {
	if legacy {
		return nil
	}
	if e.PropertyPath != nil {
		property := sourcePropertyForPointer(a.Payload, *e.PropertyPath)
		if property == nil || !pointerExists(a.Payload, *e.PropertyPath) {
			return semantic("evidence.propertyPath", "Fresh proof must address an own semantic payload property")
		}
	}
	if slices.Contains([]string{RuntimeProfile, LineageProfile, EventsProfile, ComposedProfile}, a.Owner.Profile) {
		if e.Source.StartLine == nil || e.Source.EndLine == nil || *e.Source.StartLine <= 0 || *e.Source.EndLine < *e.Source.StartLine {
			return semantic("evidence.source", "Runtime, lineage and event proof requires physical bounds")
		}
	}
	return nil
}

func validateSourceFacetProof(a ProviderAssertion, graph *SourceGraphSnapshot) error {
	if !relationalSubject(a.Payload.Kind, a.Payload.Attributes, a.RecordType == "edge") {
		return nil
	}
	facets, _, err := relationalFacetObject(a.Payload.Kind, a.Payload.Attributes)
	if err != nil {
		return err
	}
	for _, raw := range facets {
		facet, err := decodeRelationalFacet(a.Payload.Kind, raw, true)
		if err != nil {
			return err
		}
		for _, eid := range facet.EvidenceIDs {
			if !slices.Contains(a.EvidenceIDs, eid) {
				return semantic("facets.evidenceIds", "Facet proof must belong to its own assertion")
			}
			var e Evidence
			if err := json.Unmarshal(graph.RawEvidence[eid], &e); err != nil {
				return err
			}
			if e.Source.SnapshotID != facet.SourceSnapshotID {
				return semantic("facets.sourceSnapshotId", "Facet snapshot must match its own proof")
			}
		}
	}
	return nil
}

func sourceDependencyCurrentness(graph *SourceGraphSnapshot, own map[string]SourceClaimCurrentness) []SourceClaimCurrentness {
	claims := map[string]ProviderAssertion{}
	reverse := map[string][]string{}
	queue := []string{}
	queued := map[string]bool{}
	mark := func(key string) {
		if !queued[key] {
			queued[key] = true
			queue = append(queue, key)
		}
	}
	for _, a := range graph.Assertions {
		claims[sourceAssertionKey(a)] = a
	}
	for _, a := range graph.Assertions {
		key := sourceAssertionKey(a)
		f := own[key]
		f.Dependency.Reasons = slices.Clone(f.Dependency.Reasons)
		if f.Own.Status != "current" || f.Dependency.Status != "current" {
			mark(key)
		}
		for _, dependency := range a.DependencyClaims {
			targetKey := sourceClaimKey(dependency.Target.RecordType, dependency.Target.ExpectedID, dependency.Target.RepositoryID, dependency.Target.ProviderNamespace)
			reverse[targetKey] = append(reverse[targetKey], key)
			unsafe := sourceConsumerDependencyUnsafe(a, dependency, claims, own)
			if unsafe {
				f.Dependency.Status = "stale"
				f.Dependency.Reasons = append(f.Dependency.Reasons, "dependency_changed")
				mark(key)
			}
		}
		own[key] = f
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, consumer := range reverse[key] {
			f := own[consumer]
			f.Dependency.Status = "stale"
			f.Dependency.Reasons = append(f.Dependency.Reasons, "dependency_changed")
			own[consumer] = f
			mark(consumer)
		}
	}
	out := make([]SourceClaimCurrentness, 0, len(graph.Assertions))
	for _, a := range graph.Assertions {
		f := own[sourceAssertionKey(a)]
		slices.Sort(f.Dependency.Reasons)
		f.Dependency.Reasons = slices.Compact(f.Dependency.Reasons)
		if f.Dependency.Status != "current" {
			for i := range f.Fields {
				f.Fields[i].Dependency = f.Dependency
			}
		}
		out = append(out, f)
	}
	return out
}

func sourceDependencyUnsafe(binding SourceDependencyBinding, claims map[string]ProviderAssertion, current map[string]SourceClaimCurrentness) bool {
	key := sourceClaimKey(binding.Target.RecordType, binding.Target.ExpectedID, binding.Target.RepositoryID, binding.Target.ProviderNamespace)
	target, ok := claims[key]
	if !ok || target.AssertionHash != binding.Target.AssertionHash {
		return true
	}
	value := binding.ValueContext
	if value == nil {
		return false
	}
	freshness, exists := current[key]
	if !exists {
		return true
	}
	if _, err := sourceDependencyValue(binding, target.Payload); err != nil {
		return true
	}
	requiresField := binding.Target.ExpectedID == value.NodeID && (value.Kind == "column" || value.Kind == "port")
	found := false
	for _, field := range freshness.Fields {
		selected := value.Kind == "column" && field.Property.Kind == "relational_facet" && field.Property.FacetKey == value.FacetKey || value.Kind == "port" && field.Property.Kind == "flow_ports" && field.Property.Collection == value.Collection
		if selected {
			found = true
		}
		if selected && (field.Own.Status != "current" || field.Dependency.Status != "current") {
			return true
		}
	}
	return requiresField && !found
}

func sourceConsumerDependencyUnsafe(consumer ProviderAssertion, binding SourceDependencyBinding, claims map[string]ProviderAssertion, current map[string]SourceClaimCurrentness) bool {
	if sourceDependencyUnsafe(binding, claims, current) {
		return true
	}
	keys := sourceDependencyFacetKeys(consumer, binding)
	if len(keys) == 0 {
		return false
	}
	key := sourceClaimKey(binding.Target.RecordType, binding.Target.ExpectedID, binding.Target.RepositoryID, binding.Target.ProviderNamespace)
	target := claims[key]
	if target.Payload.Kind == "unresolved_target" {
		return false
	}
	freshness, exists := current[key]
	if !exists {
		return true
	}
	if _, err := sourceConsumerDependencyValue(consumer, binding, target.Payload); err != nil {
		return true
	}
	for _, facet := range keys {
		found := false
		for _, field := range freshness.Fields {
			if field.Property.Kind != "relational_facet" || field.Property.FacetKey != facet {
				continue
			}
			found = true
			if field.Own.Status != "current" || field.Dependency.Status != "current" {
				return true
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func classifySourceEdgeClaims(claims map[string]ProviderAssertion, base []ProviderAssertion, fresh map[string]bool) {
	for key, a := range claims {
		if a.RecordType != "edge" || !fresh[key] {
			continue
		}
		endpoints := map[string]Node{}
		for _, binding := range a.DependencyClaims {
			if binding.Site != "/from" && binding.Site != "/to" {
				continue
			}
			target := sourceBoundClaim(binding, claims, base)
			endpoints[binding.Site] = Node{ID: target.RecordID, Kind: target.Payload.Kind, Attributes: target.Payload.Attributes}
		}
		from, to := endpoints["/from"], endpoints["/to"]
		profile := sourcePayloadProfile(a.Payload)
		if runtimeSubject(from.Kind, false) || runtimeSubject(to.Kind, false) {
			profile = RuntimeProfile
		}
		if lineageSubject(from.Kind, false) || lineageSubject(to.Kind, false) {
			profile = LineageProfile
		}
		if eventsRelation(a.Payload.Kind, from, to) {
			profile = EventsProfile
		}
		if representationSubject(a.Payload.Kind, true) || representationContainsEndpoint(from) || representationContainsEndpoint(to) {
			profile = ComposedProfile
		}
		a.Owner.Profile = profile
		claims[key] = a
	}
}

func representationContainsEndpoint(n Node) bool {
	return representationSubject(n.Kind, false) || representationMapping(n.Kind, n.Attributes, false)
}

func sourceBoundClaim(binding SourceDependencyBinding, claims map[string]ProviderAssertion, base []ProviderAssertion) ProviderAssertion {
	if binding.Basis == "base" {
		target, _ := exactSourceAssertion(base, binding.Target)
		return target
	}
	return claims[sourceClaimKey(binding.Target.RecordType, binding.Target.ExpectedID, binding.Target.RepositoryID, binding.Target.ProviderNamespace)]
}

func classifySourceClaimOwnership(claims map[string]ProviderAssertion, base []ProviderAssertion, fresh map[string]bool) {
	for key, a := range claims {
		if !fresh[key] || a.RecordType != "node" || a.Payload.Kind != "field_mapping" {
			continue
		}
		for _, binding := range a.DependencyClaims {
			if binding.Site != "/parentId" {
				continue
			}
			parent := sourceBoundClaim(binding, claims, base)
			if representationOwner(parent.Payload.Kind) {
				a.Owner.Profile = ComposedProfile
				claims[key] = a
			}
		}
	}
	classifySourceEdgeClaims(claims, base, fresh)
}

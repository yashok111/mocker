package backendmodel

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// Context checks use the independently bound payload at each site. A UUID in
// another provider's projection cannot supply a missing facet or parent fact.
func validateSourceBoundRecordContexts(a ProviderAssertion, targets map[string]ProviderAssertion, retained map[string]bool) error {
	if a.RecordType == "edge" {
		if err := validateSourceEndpointContexts(a, targets); err != nil {
			return err
		}
		if a.Payload.Kind == "references" {
			return validateSourceFKContexts(a, targets, retained)
		}
		return nil
	}
	if a.Payload.ParentID != nil {
		parent := sourceClaimNode(targets["/parentId"])
		child := sourceClaimNode(a)
		edge := Edge{Kind: "contains", From: parent.ID, To: child.ID}
		if !sourceStructuralEndpoints(ComposedProfile, edge, parent, child) {
			return semantic("/parentId", "Exact parent claim has an incompatible container kind")
		}
	}
	for _, binding := range a.DependencyClaims {
		target, exists := targets[binding.Site]
		if !exists {
			continue // A retained facet keeps its historical dependencies.
		}
		if err := validateSourceNodeReferenceContext(a, binding, target); err != nil {
			return err
		}
	}
	return nil
}

func sourceClaimHasParent(a ProviderAssertion, parent *string) bool {
	return parent != nil && a.Payload.ParentID != nil && *a.Payload.ParentID == *parent
}

func validateSourceNodeReferenceContext(a ProviderAssertion, binding SourceDependencyBinding, target ProviderAssertion) error {
	p := a.Payload
	switch {
	case p.Kind == "flow" && (binding.Property.Group == "entryStepId" || binding.Property.Group == "exitStepIds"):
		if target.Payload.Kind != "flow_step" || !sourceClaimHasParent(target, new(a.RecordID)) {
			return semantic(binding.Site, "Exact step claim must belong to this flow")
		}
	case p.Kind == "flow_step" && binding.Property.Group == "transactionContext":
		if target.Payload.Kind != "transaction" || !sourceClaimHasParent(target, p.ParentID) {
			return semantic(binding.Site, "Exact transaction claim must belong to the step's flow")
		}
	case binding.Property.Kind == "relational_facet":
		switch binding.Property.Group {
		case "columnIds", "terms":
			if target.Payload.Kind != "column" || !sourceClaimHasParent(target, p.ParentID) {
				return semantic(binding.Site, "Exact column claim must have the subject's table/view parent")
			}
		case "parentIds":
			return validateSourceMigrationParent(a, target, binding)
		}
	}
	return nil
}

func sourceClaimFacet(a ProviderAssertion, key, path string) (*relationalFacet, error) {
	fs, _, err := relationalFacetObject(a.Payload.Kind, a.Payload.Attributes)
	if err != nil || fs[key] == nil {
		return nil, semantic(path, "Selected facet is absent from the exact bound claim")
	}
	f, err := decodeRelationalFacetMode(a.Payload.Kind, fs[key], true, false)
	if err != nil {
		return nil, semantic(path, "Selected facet is invalid in the exact bound claim")
	}
	return f, nil
}

func validateSourceEndpointContexts(a ProviderAssertion, targets map[string]ProviderAssertion) error {
	from, to := targets["/from"], targets["/to"]
	edge := Edge{Kind: a.Payload.Kind, From: a.Payload.From, To: a.Payload.To, Attributes: a.Payload.Attributes}
	nf, nt := sourceClaimNode(from), sourceClaimNode(to)
	if !sourceStructuralEndpoints(ComposedProfile, edge, nf, nt) && !source6HTTPCall(ComposedSchemaVersion, edge, nf, nt) {
		return semantic("edge", "Exact endpoint claims do not support this edge kind")
	}
	if edge.Kind == "contains" && to.Payload.ParentID != nil && *to.Payload.ParentID != from.RecordID {
		return semantic("/to", "Exact child claim disagrees with its contains parent")
	}
	if runtimeControlKind(edge.Kind) || runtimeBoundaryKind(edge.Kind) {
		if !sourceClaimHasParent(to, from.Payload.ParentID) {
			return semantic("edge", "Exact control or transaction endpoints must belong to one flow")
		}
	}
	if runtimeBoundaryKind(edge.Kind) {
		attrs := decodeRuntimeAttributes(from.Payload.Attributes)
		expected := map[string]string{"begins": "transaction_begin", "commits": "transaction_commit", "rolls_back": "transaction_rollback"}[edge.Kind]
		if attrs.StepKind != expected || attrs.TransactionContext.Status != "known" || attrs.TransactionContext.TransactionID != to.RecordID {
			return semantic("/from", "Exact boundary step must declare the bound transaction and matching step kind")
		}
	}
	if runtimeAccessEdgeKind(edge.Kind) {
		key := runtimeString(a.Payload.Attributes["facetKey"])
		if _, err := sourceClaimFacet(targets["/attributes/datastoreId"], key, "/attributes/datastoreId"); err != nil {
			return err
		}
		if to.Payload.Kind != "unresolved_target" {
			_, err := sourceClaimFacet(to, key, "/to")
			return err
		}
	}
	return nil
}

func validateSourceFKContexts(a ProviderAssertion, targets map[string]ProviderAssertion, retained map[string]bool) error {
	fs, root, err := relationalFacetObject(a.Payload.Kind, a.Payload.Attributes)
	if err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(fs)) {
		if retained[key] {
			continue
		}
		path := root + "/" + escapeRelationalPointer(key)
		facet, err := sourceClaimFacet(a, key, path)
		if err != nil {
			return err
		}
		constraint := targets["/from"]
		definition, err := sourceClaimFacet(constraint, key, "/from")
		if err != nil {
			return err
		}
		if definition.ConstraintKind != "foreign_key" || constraint.Payload.ParentID == nil {
			return semantic("/from", "Exact constraint facet must declare a foreign key and its source table")
		}
		if targets["/to"].Payload.Kind == "unresolved_target" {
			continue // Existing final-graph validation checks empty pairs and reason.
		}
		columns := make([]string, 0, len(facet.ColumnPairs))
		for i, pair := range facet.ColumnPairs {
			pairPath := fmt.Sprintf("%s/columnPairs/%d", path, i)
			from, to := targets[pairPath+"/fromColumnId"], targets[pairPath+"/toColumnId"]
			if !sourceClaimHasParent(from, constraint.Payload.ParentID) || !sourceClaimHasParent(to, new(a.Payload.To)) {
				return semantic(pairPath, "Exact FK column claims must belong to the bound source and target tables")
			}
			columns = append(columns, pair.FromColumnID)
		}
		if !slices.Equal(columns, definition.ColumnIDs) {
			return semantic(path, "Ordered FK pair columns differ from the exact bound constraint facet")
		}
	}
	return nil
}

func validateSourceMigrationParent(a, parent ProviderAssertion, binding SourceDependencyBinding) error {
	f, err := sourceClaimFacet(a, binding.Property.FacetKey, binding.Site)
	if err != nil || f.DerivationStatus != "complete" {
		return err
	}
	p, err := sourceClaimFacet(parent, binding.Property.FacetKey, binding.Site)
	if err != nil {
		return err
	}
	order, ownErr := strconv.ParseInt(string(f.Order.Value), 10, 64)
	prior, parentErr := strconv.ParseInt(string(p.Order.Value), 10, 64)
	if p.DerivationStatus != "complete" || p.Order.Status != "known" || ownErr != nil || parentErr != nil || order <= prior {
		return semantic(binding.Site, "Complete migration requires an earlier complete exact parent facet")
	}
	return nil
}

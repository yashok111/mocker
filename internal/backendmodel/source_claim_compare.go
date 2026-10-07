package backendmodel

import (
	"context"
	"maps"
	"slices"
	"strings"
)

func sourceClaimComparisonSide(graph *SourceGraphSnapshot, a ProviderAssertion, property *TypedSourcePropertySelector) *SourceClaimComparisonSide {
	current := sourceReadCurrentness(graph, a)
	out := &SourceClaimComparisonSide{Assertion: sourceAssertionRef(a), Property: property, Currentness: current, Selections: []SourceAssertionResolution{}, DependencyClaims: []SourceDependencyBinding{}}
	if property != nil {
		value, err := SelectSourceProperty(a.Payload, *property)
		if err == nil {
			out.Value = new(value)
		}
		out.Currentness.Fields = slices.DeleteFunc(slices.Clone(current.Fields), func(f TypedFieldCurrentness) bool { return f.Property != *property })
	}
	for _, selection := range graph.recordSelections(a.RecordType, a.RecordID) {
		if property == nil || selection.Property == *property {
			out.Selections = append(out.Selections, selection)
		}
	}
	for _, binding := range a.DependencyClaims {
		if property == nil || binding.Property == *property {
			out.DependencyClaims = append(out.DependencyClaims, binding)
		}
	}
	return out
}
func sourceClaimComparisonRecord(a ProviderAssertion, side *SourceClaimComparisonSide) *RecordSide {
	return &RecordSide{RecordType: a.RecordType, ID: a.RecordID, Name: new(a.Payload.Name), Key: new(a.ExternalKey), SourceClaim: side}
}
func sourceCompareEqual(a, b any) bool {
	left, e1 := requestDigest(a)
	right, e2 := requestDigest(b)
	return e1 == nil && e2 == nil && left == right
}

// appendSourceClaimDeltas augments structural deltas before hash/filter/paging.
// The qualified address never chooses one provider to represent a shared UUID.
func appendSourceClaimDeltas(ctx context.Context, delta *RevisionDelta, before, after *SourceGraphSnapshot) error {
	left, right := sourceComparisonClaims(before), sourceComparisonClaims(after)
	keys := map[string]bool{}
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}

	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		a, aok := left[key]
		b, bok := right[key]
		claim := b
		if !bok {
			claim = a
		}
		address := "assertion/" + claim.RecordID + "/" + claim.Owner.RepositoryID + "/" + claim.Owner.ProviderNamespace
		change := RecordDelta{RecordType: claim.RecordType, ID: address, ChangeKinds: []string{}, ChangedPaths: []string{}}
		var old, next *SourceClaimComparisonSide
		if aok {
			old = sourceClaimComparisonSide(before, a, nil)
			change.Before = sourceClaimComparisonRecord(a, old)
		}
		if bok {
			next = sourceClaimComparisonSide(after, b, nil)
			change.After = sourceClaimComparisonRecord(b, next)
		}
		switch {
		case !aok:
			change.ChangeKinds = []string{"added"}
			change.ChangedPaths = []string{"/source/assertion"}
		case !bok:
			change.ChangeKinds = []string{"removed"}
			change.ChangedPaths = []string{"/source/assertion"}
		default:
			if !sourceCompareEqual(old.Currentness.Own, next.Currentness.Own) || !sourceCompareEqual(old.Currentness.Dependency, next.Currentness.Dependency) {
				change.ChangeKinds = append(change.ChangeKinds, "freshness_changed")
				change.ChangedPaths = append(change.ChangedPaths, "/source/currentness")
			}
			if a.ExternalKey != b.ExternalKey {
				change.ChangeKinds = append(change.ChangeKinds, "identity_mapped")
				change.ChangedPaths = append(change.ChangedPaths, "/source/externalKey")
				delta.Summary.IdentityMappings++
			}
			if a.AssertionHash != b.AssertionHash || !sourceCompareEqual(old.DependencyClaims, next.DependencyClaims) {
				change.ChangeKinds = append(change.ChangeKinds, "modified")
				change.ChangedPaths = append(change.ChangedPaths, "/source/assertion")
			}
		}
		sourceAppendDelta(delta, change)
		if !aok || !bok {
			continue
		}
		if err := sourceClaimPropertyDeltas(delta, before, after, a, b, address); err != nil {
			return err
		}

	}
	slices.SortFunc(delta.Changes, func(a, b RecordDelta) int {
		if order := strings.Compare(a.RecordType, b.RecordType); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return nil
}
func sourceAppendDelta(delta *RevisionDelta, change RecordDelta) {
	if len(change.ChangeKinds) == 0 {
		return
	}
	slices.Sort(change.ChangeKinds)
	change.ChangeKinds = slices.Compact(change.ChangeKinds)
	slices.Sort(change.ChangedPaths)
	change.ChangedPaths = slices.Compact(change.ChangedPaths)
	delta.Changes = append(delta.Changes, change)
	delta.Summary.SourceChanges++
	if slices.Contains(change.ChangeKinds, "freshness_changed") {
		delta.Summary.FreshnessChanges++
	}
}

func sourceClaimPropertyDeltas(delta *RevisionDelta, before, after *SourceGraphSnapshot, a, b ProviderAssertion, address string) error {
	claim := b
	payloads := []SourceAssertionPayload{a.Payload, b.Payload}
	properties, err := sourceSelectors(payloads)
	if err != nil {
		return err
	}
	for _, property := range properties {
		old, next := sourceClaimComparisonSide(before, a, new(property)), sourceClaimComparisonSide(after, b, new(property))
		change := RecordDelta{RecordType: claim.RecordType, ID: address + "/property/" + sourcePropertyKey(property), Before: sourceClaimComparisonRecord(a, old), After: sourceClaimComparisonRecord(b, next), ChangeKinds: []string{}, ChangedPaths: []string{}}
		if !sourceCompareEqual(old.Currentness.Fields, next.Currentness.Fields) {
			change.ChangeKinds = append(change.ChangeKinds, "freshness_changed")
			change.ChangedPaths = append(change.ChangedPaths, "/source/currentness/field")
		}
		if !sourceCompareEqual(old.Value, next.Value) || !sourceCompareEqual(old.Selections, next.Selections) || !sourceCompareEqual(old.DependencyClaims, next.DependencyClaims) {
			change.ChangeKinds = append(change.ChangeKinds, "modified")
			change.ChangedPaths = append(change.ChangedPaths, "/source/property")
		}
		sourceAppendDelta(delta, change)
	}
	return nil
}

func sourceComparisonClaims(graph *SourceGraphSnapshot) map[string]ProviderAssertion {
	out := map[string]ProviderAssertion{}
	if graph == nil {
		return out
	}
	for _, a := range graph.Assertions {
		out[sourceAssertionKey(a)] = a
	}
	return out
}

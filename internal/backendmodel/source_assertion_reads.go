package backendmodel

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"slices"
	"strings"
)

type sourceAssertionCursor struct {
	Scope string `json:"scope"`
	After string `json:"after"`
}

// QuerySourceAssertions reads only the supplied immutable snapshot. Registry heads
// are deliberately absent from both filtering and cursor validation.
func QuerySourceAssertions(ctx context.Context, graph *SourceGraphSnapshot, pin SourceReadPin, in SourceAssertionsQuery) (*SourceAssertionsPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil || graph.SourceVector == nil {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Assertions require a composed source vector"}
	}
	limit, scope, after, err := sourceAssertionPageInput(graph, pin, in)
	if err != nil {
		return nil, err
	}
	claims := slices.Clone(graph.Assertions)
	slices.SortFunc(claims, func(a, b ProviderAssertion) int { return strings.Compare(sourceAssertionKey(a), sourceAssertionKey(b)) })
	items := []SourceAssertionItem{}
	found := after == ""
	for _, a := range claims {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !sourceAssertionMatches(a, in) {
			continue
		}
		key := sourceAssertionKey(a)
		if !found {
			if key == after {
				found = true
			}
			continue
		}
		if len(items) == limit {
			raw, _ := json.Marshal(sourceAssertionCursor{scope, sourceAssertionKey(items[len(items)-1].Assertion)})
			page := &SourceAssertionsPage{DocumentVersion: "source-assertions-v1", Pins: pin, Items: items, NextCursor: base64.RawURLEncoding.EncodeToString(raw)}
			return detachSourcePage(page)
		}
		item, err := sourceAssertionReadItem(graph, a)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if !found {
		return nil, invalid("cursor", "Cursor ordering key is absent from source snapshot")
	}
	return detachSourcePage(&SourceAssertionsPage{DocumentVersion: "source-assertions-v1", Pins: pin, Items: items})
}
func detachSourcePage(page *SourceAssertionsPage) (*SourceAssertionsPage, error) {
	raw, err := json.Marshal(page)
	if err != nil {
		return nil, err
	}
	var out SourceAssertionsPage
	// Assertions may contain normalized UUID attributes: avoid importing the
	// source wire command codec here; ProviderAssertion has no command decoder.
	err = json.Unmarshal(raw, &out)
	return &out, err
}
func sourceAssertionReadItem(graph *SourceGraphSnapshot, a ProviderAssertion) (SourceAssertionItem, error) {
	item := SourceAssertionItem{Assertion: a, Currentness: sourceReadCurrentness(graph, a), Selections: []SourceAssertionResolution{}, Conflicts: []SourceAssertionConflict{}}
	claims := []ProviderAssertion{}
	current := map[string]SourceClaimCurrentness{}
	for _, claim := range graph.recordClaims(a.RecordType, a.RecordID) {
		claims = append(claims, claim)
		current[sourceAssertionKey(claim)] = sourceReadCurrentness(graph, claim)
	}
	slices.SortFunc(claims, func(a, b ProviderAssertion) int { return strings.Compare(sourceAssertionKey(a), sourceAssertionKey(b)) })
	payloads := make([]SourceAssertionPayload, 0, len(claims))
	for _, claim := range claims {
		payloads = append(payloads, claim.Payload)
	}
	selectors, err := sourceSelectors(payloads)
	if err != nil {
		return item, err
	}
	merger := sourceAssertionMerger{base: graph, current: current}
	for _, selector := range selectors {
		conflict, err := merger.conflict(claims, selector)
		if err != nil {
			return item, err
		}
		if conflict == nil {
			continue
		}
		for _, selection := range graph.recordSelections(a.RecordType, a.RecordID) {
			if selection.Property == selector {
				item.Selections = append(item.Selections, selection)
				conflict.ConflictHash = selection.ConflictHash
			}
		}
		item.Conflicts = append(item.Conflicts, *conflict)
	}
	return item, nil
}
func sourceReadCurrentness(graph *SourceGraphSnapshot, a ProviderAssertion) SourceClaimCurrentness {
	// An indexed lookup: this runs per claim and per selector in the claim
	// diff, where the scan made one diff job O(claims²) (review 2026-10-06, F53).
	if f, ok := graph.claimCurrentness(a); ok {
		return f
	}
	missing := sourceCurrentness(a)
	missing.Dependency = AssertionFreshness{Status: "stale", Reasons: []string{"currentness_missing"}}
	return missing
}

func sourceAssertionMatches(a ProviderAssertion, in SourceAssertionsQuery) bool {
	return (in.RecordType == "" || a.RecordType == in.RecordType) && (in.ID == "" || a.RecordID == in.ID) && (in.RepositoryID == "" || a.Owner.RepositoryID == in.RepositoryID) && (in.ProviderNamespace == "" || a.Owner.ProviderNamespace == in.ProviderNamespace)
}
func sourceAssertionPageInput(graph *SourceGraphSnapshot, pin SourceReadPin, in SourceAssertionsQuery) (int, string, string, error) {
	vectorHash, err := requestDigest(graph.SourceVector)
	if err != nil {
		return 0, "", "", err
	}
	if pin.ProjectID != graph.State.Revision.ProjectID || pin.BaseRevisionID != graph.State.Revision.ID || pin.SourceVectorHash != vectorHash || pin.TargetHash == "" || pin.EffectiveSemanticHash == "" {
		return 0, "", "", invalid("pins", "Source read pins do not match the supplied snapshot")
	}
	if in.RecordType != "" && in.RecordType != "node" && in.RecordType != "edge" {
		return 0, "", "", semantic("recordType", "Select node or edge assertions")
	}
	if in.Limit < 0 || in.Limit > MaxGraphPageSize {
		return 0, "", "", invalid("limit", "limit must be between 1 and 500")
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultGraphPageSize
	}
	filter := in
	filter.Cursor = ""
	filter.Limit = 0
	scope, err := requestDigest(struct {
		Pins    SourceReadPin
		Filters SourceAssertionsQuery
	}{pin, filter})
	if err != nil {
		return 0, "", "", err
	}
	after, err := sourceAssertionAfter(in.Cursor, scope)
	if err != nil {
		return 0, "", "", err
	}

	return limit, scope, after, nil
}

func sourceAssertionAfter(cursor, scope string) (string, error) {
	after := ""
	if cursor != "" {
		if len(cursor) > 2048 {
			return "", invalid("cursor", "Invalid cursor")
		}
		raw, e := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if e != nil {
			return "", invalid("cursor", "Invalid cursor")
		}
		var c sourceAssertionCursor
		if json.Unmarshal(raw, &c, json.RejectUnknownMembers(true)) != nil || c.Scope != scope || c.After == "" {
			return "", invalid("cursor", "Cursor does not match source pins or filters")
		}
		after = c.After
	}

	return after, nil
}

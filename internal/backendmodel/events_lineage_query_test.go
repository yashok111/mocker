package backendmodel

import (
	"slices"
	"testing"
)

func TestEventsLineageIndexBudgetBeforeTraversal(t *testing.T) {
	s := lineageQueryState(t, 2)
	s.Revision.SchemaVersion = EventsSchemaVersion
	s.Sources[0].Provider.Profiles = append(s.Sources[0].Provider.Profiles, EventsProfile)
	a, b := lineageQueryRef(s, 0), lineageQueryRef(s, 1)
	for i := range lineageMaxExaminedMappings + 1 {
		lineageQueryMapping(t, s, i, []LineageValueRef{b}, b, "copy")
	}
	p, err := projectLineage(t.Context(), s, LineageQueryInput{RevisionID: s.Revision.ID, Seed: a, Direction: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Truncated || !slices.Contains(p.TruncationReasons, "mapping_limit") {
		t.Fatalf("unreachable mappings escaped construction admission: %+v", p)
	}
}

func TestEventsLineageCursorPolicyAndContextIdentity(t *testing.T) {
	s := lineageQueryState(t, 4)
	a, b, c, d := lineageQueryRef(s, 0), lineageQueryRef(s, 1), lineageQueryRef(s, 2), lineageQueryRef(s, 3)
	lineageQueryMapping(t, s, 1, []LineageValueRef{a}, b, "copy")
	lineageQueryMapping(t, s, 2, []LineageValueRef{a}, c, "copy")
	lineageQueryMapping(t, s, 3, []LineageValueRef{a}, d, "copy")
	in := LineageQueryInput{RevisionID: s.Revision.ID, Seed: a, Direction: "forward", Limit: 1}
	old, err := projectLineage(t.Context(), s, in)
	if err != nil || old.NextCursor == "" {
		t.Fatal(old, err)
	}
	s.Revision.SchemaVersion = EventsSchemaVersion
	s.Sources[0].Provider.Profiles = append(s.Sources[0].Provider.Profiles, EventsProfile)
	in.Cursor = old.NextCursor
	if _, err := projectLineage(t.Context(), s, in); err == nil {
		t.Fatal("source4 cursor replayed under source5 policy")
	}
	in.Cursor = ""
	first, err := projectLineage(t.Context(), s, in)
	if err != nil || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	in.Cursor = first.NextCursor
	in.Seed = b
	if _, err := projectLineage(t.Context(), s, in); err == nil {
		t.Fatal("source5 cursor replayed for a different seed")
	}
	in.Seed = a
	second, err := projectLineage(t.Context(), s, in)
	if err != nil || len(second.Items) != 1 || second.Items[0].Mapping.ID == first.Items[0].Mapping.ID {
		t.Fatal(second, err)
	}
}

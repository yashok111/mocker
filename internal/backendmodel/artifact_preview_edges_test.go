package backendmodel

import (
	"encoding/json/jsontext"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
)

// review 2026-10-06, F63: every row whose embedded contract cannot be resolved
// carries embedded_contract_unavailable, not only the first one; the cache
// stored a nil entry and later rows looked clean.
func TestEditorProjectionEmbeddedUnavailableOnEveryRow(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	// The owner refuses a dangling contractId on save, so only a stored
	// document from before that rule can carry one: edit the snapshot bytes.
	raw := strings.ReplaceAll(f.scenario.snapshot.DocumentJSON, `"contractId":"linked"`, `"contractId":"gone"`)
	raw = strings.ReplaceAll(raw, `"contractId":"copy"`, `"contractId":"gone"`)
	if raw == f.scenario.snapshot.DocumentJSON {
		t.Fatal("fixture has no linked operation")
	}
	f.scenario.snapshot.DocumentJSON = raw
	page, err := f.request.Project(&f.state, f.context, f.query("sequence", ""))
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, item := range page.Items {
		if item.Kind != "sequence_message" || item.Locator.Embedded != nil {
			continue
		}
		rows++
		if !slices.ContainsFunc(item.Diagnostics, func(d ArtifactDiagnostic) bool { return d.Code == "embedded_contract_unavailable" }) {
			t.Fatalf("row %s looks clean: %+v", item.ID, item.Diagnostics)
		}
	}
	if rows < 2 {
		t.Fatalf("fixture has %d rows with an unresolved embedded contract", rows)
	}
}

// review 2026-10-06, F62: a group whose new target is unavailable keeps its
// old bindings; when another command of the same request binds one of those
// sources, the preview answered 400 "Duplicate API source binding" instead of
// the blocking backend_artifact_target_unavailable diagnostic.
func TestArtifactPreviewUnavailableTargetWithReboundSourceIsBlocked(t *testing.T) {
	t.Parallel()
	s, base, ids, _ := artifactServiceFixture(t)
	owner := s.api.(*apidesign.Repo)
	pin := func(name string) (ArtifactKey, string) {
		d, err := owner.Create(t.Context(), apidesign.CreateInput{Name: name, Document: artifactTestDocument, Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		return ArtifactKey{"api_design", strconv.FormatInt(d.Design.ID, 10)}, strconv.FormatInt(d.Draft.ID, 10)
	}
	set := func(key ArtifactKey, revision string) ArtifactPinCommand {
		return ArtifactPinCommand{Type: "set_artifact_pin", Artifact: key, RevisionID: revision, APIBindings: []APIPinBindingInput{{SourceNodeID: ids["http"], Selector: APIArtifactSelector{ObjectKey: "orders-read"}}}, EditorBindings: []EditorBindingInput{}, Reason: "Manual"}
	}
	a, aRevision := pin("A")
	b, bRevision := pin("B")
	pinned, _ := applyArtifactTest(t, s, base.Project.ID, PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []ArtifactPinCommand{set(a, aRevision)}}, "a")
	p, err := s.Preview(t.Context(), base.Project.ID, PreviewArtifactPinsInput{BaseRevisionID: pinned.Revision.ID, ExpectedVersion: pinned.Project.Version, Commands: []ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: a, RevisionID: "999999", APIBindings: []APIPinBindingInput{}, EditorBindings: []EditorBindingInput{}, Reason: "Retarget"}, set(b, bRevision)}})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if p.CanApply || !slices.ContainsFunc(p.Diagnostics, func(d ArtifactDiagnostic) bool { return d.Code == "backend_artifact_target_unavailable" }) {
		t.Fatalf("unavailable target not blocking: %+v", p.Diagnostics)
	}
}

// review 2026-10-06, F96 (uncertain, confirmed): a retained stale
// representation mapping over an event route that is no longer declared keeps
// the stale exemption the events validators already give; without it the
// whole source6 import was blocked by backend_graph_invalid.
func TestRepresentationStaleMappingExemptFromUnknownRouteRule(t *testing.T) {
	t.Parallel()
	ids := map[string]string{}
	for i, name := range []string{"message", "field", "emit", "channel", "route", "mapping", "parent"} {
		ids[name] = "00000000-0000-4000-8000-00000000000" + string(rune('1'+i))
	}
	nodes := map[string]Node{
		ids["message"]: {ID: ids["message"], Kind: "message"},
		ids["field"]:   {ID: ids["field"], Kind: "event_field", ParentID: new(ids["message"])},
		ids["emit"]:    {ID: ids["emit"], Kind: "flow_step", Attributes: map[string]jsontext.Value{"stepKind": jsontext.Value(`"emit"`)}},
		ids["channel"]: {ID: ids["channel"], Kind: "channel"},
	}
	edges := map[string]Edge{ids["route"]: {ID: ids["route"], Kind: "emits", From: ids["emit"], To: ids["message"], Attributes: map[string]jsontext.Value{"channelId": jsontext.Value(`"` + ids["channel"] + `"`), "deliveryStatus": jsontext.Value(`"unknown"`)}}}
	n := Node{ID: ids["mapping"], Kind: "field_mapping", ParentID: new(ids["parent"]), Attributes: map[string]jsontext.Value{"destination": jsontext.Value(`{"kind":"representation_field"}`)}}
	a := LineageMappingAttributes{Sources: []LineageValueRef{{Kind: "event_field", NodeID: ids["field"], EndpointID: ids["emit"], RouteID: ids["route"]}}, Destination: LineageValueRef{Kind: "representation_field", NodeID: ids["parent"]}, Transform: LineageTransform{Kind: "copy"}, AnalysisStatus: "complete"}
	if err := validateRepresentationLineageMapping(n, a, nodes, edges, nil); err == nil {
		t.Fatal("current mapping over an unknown route accepted")
	}
	n.Freshness = &AssertionFreshness{Status: "stale"}
	if err := validateRepresentationLineageMapping(n, a, nodes, edges, nil); err != nil {
		t.Fatalf("stale mapping blocked: %v", err)
	}
}

// review 2026-10-06, F93: a representation_field seed whose node is absent
// from the revision answers 404 like every other seed kind; a wrong kind or
// schema stays 400.
func TestLineageRepresentationSeedMissingNodeIsNotFound(t *testing.T) {
	t.Parallel()
	present, absent := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	nodes := map[string]Node{present: {ID: present, Kind: "representation_field"}}
	seed := func(id string) LineageValueRef { return LineageValueRef{Kind: "representation_field", NodeID: id} }
	if err := validateLineageValueTargetForSchema(seed(present), nodes, nil, ComposedSchemaVersion); err != nil {
		t.Fatal(err)
	}
	assertFault(t, validateLineageValueTargetForSchema(seed(absent), nodes, nil, ComposedSchemaVersion), "backend_not_found")
	assertFault(t, validateLineageValueTargetForSchema(seed(present), nodes, nil, EventsSchemaVersion), "backend_invalid")
	nodes[present] = Node{ID: present, Kind: "api_field"}
	assertFault(t, validateLineageValueTargetForSchema(seed(present), nodes, nil, ComposedSchemaVersion), "backend_invalid")
}

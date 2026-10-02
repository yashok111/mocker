package backendmodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testkit"
)

// Start with a real Repo.Create+ArtifactSnapshot. Controlled historical variants
// exercise the read seam without owner writes repairing malformed legacy data.
func editorHistoricalVariant(t *testing.T, f *editorProjectionFixture, change func(map[string]any)) {
	t.Helper()
	tree, err := newEditorRawTree(f.scenario.snapshot.DocumentJSON)
	if err != nil {
		t.Fatal(err)
	}
	change(tree.root)
	raw, err := jsonx.Marshal(tree.root)
	if err != nil {
		t.Fatal(err)
	}
	snapshotCopy := *f.scenario.snapshot
	snapshotCopy.DocumentJSON = string(raw)
	snapshotCopy.DocumentHash = fmt.Sprintf("%x", sha256.Sum256(raw))
	// The owner reader is the trusted envelope verifier; this test double returns
	// a matching synthetic digest for a deliberately historical saved document.
	snapshotCopy.ContentHash = snapshotCopy.DocumentHash
	f.scenario.snapshot = &snapshotCopy
	f.pin.ContentHash = snapshotCopy.ContentHash
	f.state.Revision.ArtifactPins[0] = f.pin
	f.request = NewEditorArtifactRequest(t.Context(), f.api, f.scenario)
}
func editorFixtureBinding(f *editorProjectionFixture) EditorBinding {
	return EditorBinding{ArtifactKind: f.pin.Kind, ArtifactID: f.pin.ID, Selector: EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{editorSourceID}, SourceLabels: []string{"Frozen source"}, ObjectHash: strings.Repeat("d", 64), LastKnownLabel: "Service", Origin: "manual", Reason: "Explicit link"}
}
func TestEditorEventConstructionAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"shared label", func(root map[string]any) {
			m := editorObject(root["eventModel"])
			editorObject(editorArray(m["messages"])[0])["name"] = strings.Repeat("<", 20000)
			c := editorObject(editorArray(m["contracts"])[0])
			ops := []any{}
			for i := range 80 {
				ops = append(ops, map[string]any{"id": fmt.Sprint("o", i), "action": "send", "channelId": "ch", "messageId": "em"})
			}
			c["operations"] = ops
		}},
		{"duplicate candidates", func(root map[string]any) {
			c := editorObject(editorArray(editorObject(root["eventModel"])["contracts"])[0])
			ops := []any{}
			links := []any{}
			for range 100 {
				links = append(links, map[string]any{"contractId": "copy", "operationKey": "op"})
			}
			for i := range 50 {
				ops = append(ops, map[string]any{"id": fmt.Sprint("o", i), "action": "send", "channelId": "ch", "messageId": "em", "apiLinks": links})
			}
			c["operations"] = ops
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEditorProjectionFixture(t)
			editorHistoricalVariant(t, f, tc.change)
			f.context.EditorBindings = []EditorBinding{editorFixtureBinding(f)}
			page, err := f.request.Project(&f.state, f.context, f.query("event_model", ""))
			if err != nil {
				t.Fatal(err)
			}
			if page.Complete || len(page.Items) != 0 || len(page.Diagnostics) != 1 || page.Diagnostics[0].Code != "event_model_construction_budget" || len(page.Coverage.TruncatedReasons) != 1 || !page.BindingsComplete || len(page.EditorBindings) != 1 || page.SelectedPin != f.pin {
				t.Fatalf("admission page: %+v", page)
			}
			if page.Coverage.DiagnosticsReturned != 1 || page.Coverage.ItemsReturned != 0 {
				t.Fatal(page.Coverage)
			}
			// Whole-map admission is not part of selector resolution or other views.
			if _, err := f.request.ResolveObject(f.pin, EditorSelector{Kind: "event_message", MessageID: "em"}); err != nil {
				t.Fatal(err)
			}
			sequence, err := f.request.Project(&f.state, f.context, f.query("sequence", ""))
			if err != nil || len(sequence.Items) != 5 {
				t.Fatalf("unrelated view: %v", err)
			}
		})
	}
	// Inclusive boundary is meaningful to admission; a one-byte crossing rejects.
	b := editorEventConstruction{}
	if !b.add(0, 0, 0, MaxEditorEventConstructionBytes) || b.add(0, 0, 0, 1) || b.exceeded != "construction_bytes" {
		t.Fatal(b)
	}
	if got := editorEscapedBytes("<\n\"\\é:%", true); got != 24 {
		t.Fatalf("escaped charge=%d want24", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f := newEditorProjectionFixture(t)
	s, err := f.request.pinnedSnapshot(f.pin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = editorEventPreflight(ctx, s); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestEditorProjectionUnsupportedAndEmpty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, view string
		change     func(map[string]any)
	}{
		{"participant", "sequence", func(root map[string]any) { editorObject(editorArray(root["participants"])[0])["futureField"] = true }},
		{"event extension", "event_model", func(root map[string]any) { editorObject(root["eventModel"])["futureField"] = true }},
		{"states", "states", func(root map[string]any) {
			editorObject(editorObject(editorArray(root["contracts"])[1])["document"])["x-mocker-state-diagrams"] = map[string]any{"formatVersion": 999}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEditorProjectionFixture(t)
			editorHistoricalVariant(t, f, tc.change)
			embedded := ""
			if tc.view == "states" {
				embedded = "copy"
			}
			page, err := f.request.Project(&f.state, f.context, f.query(tc.view, embedded))
			if err != nil {
				t.Fatal(err)
			}
			unsupported := false
			for _, item := range page.Items {
				unsupported = unsupported || item.Data.Unsupported != nil
			}
			if page.Complete || !unsupported {
				t.Fatalf("unsupported owner claimed complete: %+v", page)
			}
		})
	}
	f := newEditorProjectionFixture(t)
	editorHistoricalVariant(t, f, func(root map[string]any) {
		root["participants"] = []any{}
		root["messages"] = []any{}
		root["fragments"] = []any{}
	})
	page, err := f.request.Project(&f.state, f.context, f.query("sequence", ""))
	if err != nil || !page.Complete || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("empty: %+v %v", page, err)
	}
}
func TestEditorLinkedOriginQualification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		change     func(map[string]any)
		missing    bool
	}{
		{name: "exact", want: "verified"},
		{name: "missing snapshot", want: "unavailable", missing: true},
		{name: "missing source", want: "unavailable", change: func(c map[string]any) { delete(c, "source") }},
		{name: "version mismatch", want: "version_mismatch", change: func(c map[string]any) { editorObject(c["source"])["version"] = 9007199254740993 }},
		{name: "content mismatch", want: "content_mismatch", change: func(c map[string]any) { editorObject(editorObject(c["document"])["info"])["title"] = "Changed" }},
		{name: "legacy copy", want: "copy", change: func(c map[string]any) { delete(c, "mode") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEditorProjectionFixture(t)
			if tc.change != nil {
				editorHistoricalVariant(t, f, func(root map[string]any) { tc.change(editorObject(editorArray(root["contracts"])[0])) })
			}
			if tc.missing {
				f.api.snapshots = nil
			}
			o, err := f.request.ResolveObject(f.pin, EditorSelector{Kind: "state_transition", EmbeddedContractID: "linked", DiagramID: "d", TransitionID: "tr"})
			if err != nil {
				t.Fatal(err)
			}
			if o.Locator.Embedded.OriginStatus != tc.want || o.Data.StateTransition.Name != "Finish" {
				t.Fatalf("origin %+v", o)
			}
			if tc.want != "verified" && tc.want != "copy" && len(o.Diagnostics) == 0 {
				t.Fatal("missing qualification")
			}
			if tc.name == "version mismatch" && o.Locator.Embedded.Origin.Version != "9007199254740993" {
				t.Fatal(o.Locator.Embedded.Origin)
			}
			if tc.want == "copy" && len(f.api.calls) != 0 {
				t.Fatal("copy origin was consulted")
			}
		})
	}
}
func TestEditorManyOriginsShareRequestBudget(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	editorHistoricalVariant(t, f, func(root map[string]any) {
		base := editorObject(editorArray(root["contracts"])[0])
		contracts := make([]any, 0, 25)
		messages := make([]any, 0, 25)
		for i := range 25 {
			contracts = append(contracts, map[string]any{"id": fmt.Sprint("c", i), "mode": "linked", "document": base["document"], "source": map[string]any{"designId": i + 100, "revisionId": 1, "version": 1}})
			messages = append(messages, map[string]any{"id": fmt.Sprint("m", i), "fromId": "client", "toId": "p", "kind": "request", "label": "Saved", "operation": map[string]any{"contractId": fmt.Sprint("c", i), "operationKey": "op"}})
		}
		root["contracts"] = contracts
		root["messages"] = messages
		root["fragments"] = []any{}
	})
	if _, err := f.request.SnapshotPin(ArtifactKey{f.pin.Kind, f.pin.ID}, f.pin.RevisionID); err != nil {
		t.Fatal(err)
	}
	if len(f.api.calls) != 0 {
		t.Fatal("projection-only pin expanded origins")
	}
	page, err := f.request.Project(&f.state, f.context, f.query("sequence", ""))
	if err != nil {
		t.Fatal(err)
	}
	unverified := 0
	for _, item := range page.Items {
		if item.Locator.Embedded != nil && item.Locator.Embedded.OriginStatus == "unverified" {
			unverified++
		}
	}
	if len(f.api.calls) != 19 || unverified != 6 || len(page.Items) != 27 || !page.Complete {
		t.Fatalf("reads=%d unverified=%d items=%d complete=%v", len(f.api.calls), unverified, len(page.Items), page.Complete)
	}
	_, err = f.request.ResolveObject(ArtifactPin{Kind: "api_design", ID: "999", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}, EditorSelector{Kind: "state_diagram", DiagramID: "d"})
	if fault, ok := errors.AsType[*FaultError](err); !ok || fault.Status != 413 {
		t.Fatalf("required new owner: %v", err)
	}
}
func TestEditorLinkedNavigationFullIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                   string
		wrongRevision, wrongHash, explicitCopy bool
	}{
		{name: "exact"}, {name: "different top revision", wrongRevision: true}, {name: "wrong object hash", wrongHash: true}, {name: "explicit copy", explicitCopy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEditorProjectionFixture(t)
			object, err := f.request.ResolveAPIObject(f.apiPin, APIArtifactSelector{ObjectKey: "op"})
			if err != nil {
				t.Fatal(err)
			}
			ref := ArtifactRef{Kind: "api_design", ArtifactID: f.apiPin.ID, RevisionID: f.apiPin.RevisionID, ContentHash: f.apiPin.ContentHash, Selector: APIArtifactSelector{ObjectKey: "op"}, ObjectHash: object.ObjectHash, LastKnownLabel: "GET /x", ResolvedPointer: object.Pointer}
			top := f.apiPin
			if tc.wrongRevision {
				top.RevisionID = "999"
				ref.RevisionID = "999"
			}
			if tc.wrongHash {
				ref.ObjectHash = strings.Repeat("d", 64)
			}
			f.state.Revision.ArtifactPins = append(f.state.Revision.ArtifactPins, top)
			f.context.APIBindings = []APIArtifactBinding{{SourceNodeID: editorSourceID, SourceKind: "http_operation", SourceLastKnownLabel: "Frozen", Ref: ref, Origin: "manual", Reason: "Exact API association"}}
			if tc.explicitCopy {
				b := editorFixtureBinding(f)
				b.Selector = EditorSelector{Kind: "sequence_message", MessageID: "m1"}
				f.context.EditorBindings = []EditorBinding{b}
			}
			page, err := f.request.Project(&f.state, f.context, f.query("sequence", ""))
			if err != nil {
				t.Fatal(err)
			}
			linked, copied := page.Items[2], page.Items[3]
			wantLinked := !tc.wrongRevision && !tc.wrongHash
			if (len(linked.SourceNodeIDs) > 0) != wantLinked || (len(copied.SourceNodeIDs) > 0) != tc.explicitCopy {
				t.Fatalf("link=%v copy=%v", linked.SourceNodeIDs, copied.SourceNodeIDs)
			}
			if len(page.APIBindings) != 0 || len(page.EditorBindings) != len(f.context.EditorBindings) {
				t.Fatal("selected-group roster leaked other artifact")
			}
		})
	}
}
func TestEditorWideIDsAndCursorScope(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	wide := strings.Repeat("界", 50000)
	editorHistoricalVariant(t, f, func(root map[string]any) { editorObject(editorArray(root["contracts"])[1])["id"] = wide })
	q := f.query("states", wide)
	q.Limit = 1
	page, err := f.request.Project(&f.state, f.context, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.NextCursor) > 256 || page.Items[0].Locator.Embedded.ContractID != wide {
		t.Fatal("lost wide owner ID or unbounded cursor")
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wide selected owner 150000 UTF8 bytes: page=%d cursor=%d", len(raw), len(page.NextCursor))
	q.Cursor = page.NextCursor
	q.EmbeddedContractID = "linked"
	if _, err := f.request.Project(&f.state, f.context, q); err == nil {
		t.Fatal("cross-embedded cursor accepted")
	}
	// Exact numeric event origin retains all digits at the new wire boundary.
	loc := editorEventLocator(designscenario.EventMapLocator{PinnedRevisionID: 9007199254740993})
	if loc.PinnedRevisionID != "9007199254740993" {
		t.Fatal(loc)
	}
	// Ambiguous exact selector is rejected, never first-match lookup.
	editorHistoricalVariant(t, f, func(root map[string]any) {
		root["participants"] = append(editorArray(root["participants"]), editorArray(root["participants"])[0])
	})
	if _, err := f.request.ResolveObject(f.pin, EditorSelector{Kind: "participant", ParticipantID: "p"}); err == nil {
		t.Fatal("ambiguous ID accepted")
	}
}

type editorLateAPI struct {
	*editorTestAPI
	cancel context.CancelFunc
}

func (f *editorLateAPI) ArtifactSnapshot(ctx context.Context, id, rev int64) (*apidesign.ArtifactSnapshot, error) {
	s, e := f.editorTestAPI.ArtifactSnapshot(ctx, id, rev)
	f.cancel()
	return s, e
}
func TestEditorLateReaderCancellation(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := NewEditorArtifactRequest(ctx, &editorLateAPI{f.api, cancel}, f.scenario)
	if _, err := r.SnapshotPin(ArtifactKey{f.apiPin.Kind, f.apiPin.ID}, f.apiPin.RevisionID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := r.Project(&f.state, f.context, f.query("sequence", "")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !slices.Equal(f.api.calls, []string{f.apiPin.ID + "/" + f.apiPin.RevisionID}) || len(f.scenario.calls) != 0 {
		t.Fatal("late reader used after cancellation")
	}
}

func TestEditorEventConstructionAdmittedEscapedKey(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	repo := designscenario.NewRepo(db, cfg, apidesign.NewRepo(db, cfg))
	key := strings.Repeat("%:", 2000)
	doc := f.scenario.snapshot.Document
	doc.Messages = []designscenario.Message{}
	doc.Fragments = []designscenario.Fragment{}
	doc.Contracts = []designscenario.Contract{{ID: "copy", Mode: "copy", Document: []byte(strings.ReplaceAll(editorAPIDocument, `"op"`, fmt.Sprintf("%q", key)))}}
	doc.EventModel.Channels = []designscenario.EventChannel{}
	doc.EventModel.Contracts[0].Operations = []designscenario.EventOperation{}
	for i := range 60 {
		ch := fmt.Sprint("ch", i)
		doc.EventModel.Channels = append(doc.EventModel.Channels, designscenario.EventChannel{ID: ch, Address: ch, ServerIDs: []string{}, MessageIDs: []string{"em"}})
		doc.EventModel.Contracts[0].Operations = append(doc.EventModel.Contracts[0].Operations, designscenario.EventOperation{ID: fmt.Sprint("o", i), Action: "send", MessageID: "em", ChannelID: ch, APILinks: []designscenario.EventAPILink{{ContractID: "copy", OperationKey: key}}})
	}
	detail, err := repo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		if invalid, ok := errors.AsType[*designscenario.InvalidError](err); ok {
			t.Fatalf("real owner admission: %+v", invalid.Diagnostics)
		}
		t.Fatal(err)
	}
	snapshot, err := repo.ArtifactSnapshot(t.Context(), detail.Scenario.ID, detail.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := newEditorRawTree(snapshot.DocumentJSON)
	if err != nil {
		t.Fatal(err)
	}
	b, err := editorEventPreflight(t.Context(), &editorOwnerSnapshot{scenario: snapshot, tree: tree})
	if err != nil {
		t.Fatal(err)
	}
	if b.exceeded != "construction_bytes" {
		t.Fatalf("escaped admitted key bypassed byte admission: %+v", b)
	}
	t.Logf("real owner admitted escaped key=%dB input=%dB; preflight stopped at nodes=%d edges=%d diagnostics=%d accounted=%dB; no expanded owner map allocated", len(key), len(snapshot.DocumentJSON), b.Nodes, b.Edges, b.Diagnostics, b.Bytes)
	// A safe single reference exercises the unchanged owner's two escaping stages.
	doc.EventModel.Contracts[0].Operations = doc.EventModel.Contracts[0].Operations[:1]
	doc.EventModel.Channels = doc.EventModel.Channels[:1]
	analysis, err := designscenario.AnalyzeEventMap(t.Context(), doc)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range analysis.Edges {
		if edge.Kind == "api_link" {
			found = true
			if !strings.Contains(edge.Target, "%25%3A") || !strings.Contains(edge.ID, "%2525%253A") {
				t.Fatal("unexpected owner ID escaping")
			}
		}
	}
	if !found {
		t.Fatal("owner did not resolve long saved key")
	}
}

func TestEditorProjectionRosterPageAccounting(t *testing.T) {
	f := newEditorProjectionFixture(t)
	binding := editorFixtureBinding(f)
	binding.SourceNodeIDs = nil
	binding.SourceLabels = nil
	label := strings.Repeat("L", 4096)
	for i := range 100 {
		binding.SourceNodeIDs = append(binding.SourceNodeIDs, fmt.Sprintf("20000000-0000-4000-8000-%012d", i))
		binding.SourceLabels = append(binding.SourceLabels, label)
	}
	f.context.EditorBindings = []EditorBinding{binding}
	f.state.Nodes = nil
	q := f.query("sequence", "")
	q.Limit = 1
	page, err := f.request.Project(&f.state, f.context, q)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.EditorBindings) != 1 || len(page.EditorBindings[0].SourceLabels) != 100 || len(page.Items) != 1 || !page.BindingsComplete {
		t.Fatal("incomplete selected roster")
	}
	if bytes.Count(raw, []byte(label)) != 100 {
		t.Fatal("frozen labels duplicated per item or shortened")
	}
	if page.Coverage.DiagnosticsReturned != len(page.Items[0].Diagnostics)+len(page.Diagnostics)+len(page.Resolution.Diagnostics) {
		t.Fatal("diagnostic count disagrees")
	}
	t.Logf("100×4096-byte frozen source labels: serialized page=%dB items=%d labels=%d; roster occurs once", len(raw), len(page.Items), bytes.Count(raw, []byte(label)))
	q.Cursor = page.NextCursor
	next, err := f.request.Project(&f.state, f.context, q)
	if err != nil || len(next.EditorBindings[0].SourceLabels) != 100 {
		t.Fatal("second page lost roster", err)
	}
	f.scenario.snapshot = nil
	f.request = NewEditorArtifactRequest(t.Context(), f.api, f.scenario)
	q.Cursor = ""
	broken, err := f.request.Project(&f.state, f.context, q)
	if err != nil {
		t.Fatal(err)
	}
	if broken.Coverage.DiagnosticsReturned != 1 {
		t.Fatalf("unavailable diagnostic is uncounted: %+v", broken.Coverage)
	}
}

func TestEditorSeparateEmbeddedAndScenarioScopes(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	editorHistoricalVariant(t, f, func(root map[string]any) { editorObject(editorArray(root["contracts"])[1])["mode"] = "linked" })
	verified, err := f.request.ResolveObject(f.pin, EditorSelector{Kind: "state_diagram", EmbeddedContractID: "linked", DiagramID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	divergent, err := f.request.ResolveObject(f.pin, EditorSelector{Kind: "state_diagram", EmbeddedContractID: "copy", DiagramID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Locator.Embedded.OriginStatus != "verified" || divergent.Locator.Embedded.OriginStatus != "content_mismatch" || len(f.api.calls) != 1 {
		t.Fatal("origin verification leaked between saved scopes")
	}
	q := f.query("states", "linked")
	q.Limit = 1
	one, err := f.request.Project(&f.state, f.context, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Items[0].Data.StateDiagram.States) != 0 || len(one.Items[0].Data.StateDiagram.Transitions) != 0 {
		t.Fatal("ancestor repeats child subtree")
	}
	clone := *f.scenario.snapshot
	clone.ScenarioID = 999
	f.scenario.snapshot = &clone
	f.pin.ID = "999"
	f.state.Revision.ArtifactPins[0] = f.pin
	f.request = NewEditorArtifactRequest(t.Context(), f.api, f.scenario)
	q2 := f.query("states", "linked")
	q2.Limit = 1
	two, err := f.request.Project(&f.state, f.context, q2)
	if err != nil {
		t.Fatal(err)
	}
	if one.Items[0].ID == two.Items[0].ID {
		t.Fatal("distinct scenario identity collapsed")
	}
	q2.Cursor = one.NextCursor
	if _, err := f.request.Project(&f.state, f.context, q2); err == nil {
		t.Fatal("cross-scenario cursor accepted")
	}
}
func TestEditorAuthoredHashLossless(t *testing.T) {
	t.Parallel()
	hashes := map[string]bool{}
	for _, raw := range []string{`{"n":1}`, `{"n":1.0}`, `{"n":9007199254740992}`, `{"n":9007199254740993}`} {
		tree, err := newEditorRawTree(raw)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := editorAuthoredHash(tree.root)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"domain":"backend-editor-object-v1","object":`+raw+`}`)))
		if hash != want || hashes[hash] {
			t.Fatal("numeric spelling or precision lost", raw, hash)
		}
		hashes[hash] = true
	}
}

func TestEditorUnsupportedCompletenessAcrossPages(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	editorHistoricalVariant(t, f, func(root map[string]any) { editorObject(editorArray(root["participants"])[1])["futureField"] = true })
	q := f.query("sequence", "")
	q.Limit = 1
	page, err := f.request.Project(&f.state, f.context, q)
	if err != nil {
		t.Fatal(err)
	}
	if page.Complete || len(page.Diagnostics) == 0 {
		t.Fatal("first page hides unsupported owner on later page")
	}
}

type editorCancelScanContext struct {
	context.Context
	checks int
}

func (c *editorCancelScanContext) Err() error {
	c.checks++
	if c.checks >= 3 {
		return context.Canceled
	}
	return nil
}
func TestEditorSequenceCancellationDuringScan(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	s, err := f.request.pinnedSnapshot(f.pin)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &editorCancelScanContext{Context: t.Context()}
	r := NewEditorArtifactRequest(ctx, f.api, f.scenario)
	_, _, _, _, err = r.projectionRows(s, f.query("sequence", ""))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("sequence scan did not observe cancellation during traversal", err)
	}
}

func TestEditorEventConstructionAdmittedParticipantID(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	repo := designscenario.NewRepo(db, cfg, apidesign.NewRepo(db, cfg))
	participantID := strings.Repeat("%", 50000)
	doc := designscenario.Document{FormatVersion: 3, Title: "Long participant", Participants: []designscenario.Participant{{ID: participantID, Name: "Service", Kind: "service"}}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}, EventModel: &designscenario.EventModel{Servers: []designscenario.EventServer{}, Schemas: []designscenario.EventSchema{}, Messages: []designscenario.EventMessage{{ID: "m", Name: "Message", Examples: []designscenario.EventExample{}}}, Channels: []designscenario.EventChannel{}, Contracts: []designscenario.EventContract{{ID: "c", ParticipantID: participantID, Operations: []designscenario.EventOperation{}}}}}
	for i := range 12 {
		ch := fmt.Sprint("ch", i)
		doc.EventModel.Channels = append(doc.EventModel.Channels, designscenario.EventChannel{ID: ch, Address: ch, MessageIDs: []string{"m"}, ServerIDs: []string{}})
		doc.EventModel.Contracts[0].Operations = append(doc.EventModel.Contracts[0].Operations, designscenario.EventOperation{ID: fmt.Sprint("o", i), Action: "send", ChannelID: ch, MessageID: "m"})
	}
	detail, err := repo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.ArtifactSnapshot(t.Context(), detail.Scenario.ID, detail.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := newEditorRawTree(snapshot.DocumentJSON)
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := editorEventPreflight(t.Context(), &editorOwnerSnapshot{scenario: snapshot, tree: tree})
	if err != nil {
		t.Fatal(err)
	}
	if estimate.exceeded != "construction_bytes" {
		t.Fatalf("admitted long participant bypasses preflight: %+v", estimate)
	}
	t.Logf("real owner participant50000 percent bytes; input=%dB; preflight stops after nodes=%d edges=%d accounted=%dB without AnalyzeEventMap", len(snapshot.DocumentJSON), estimate.Nodes, estimate.Edges, estimate.Bytes)
}

func TestEditorAuxiliaryAuthoredContent(t *testing.T) {
	t.Parallel()
	project := func(edit func(*designscenario.Document)) *ArtifactProjectionPage {
		t.Helper()
		f := newEditorProjectionFixture(t, edit)
		q := f.query("event_model", "")
		q.Limit = 100
		page, err := f.request.Project(&f.state, f.context, q)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	graph := func(page *ArtifactProjectionPage, kind, embedded string) ArtifactProjectionItem {
		t.Helper()
		for _, item := range page.Items {
			if n := item.Data.EventNode; n != nil && n.Kind == kind && n.Locator.HTTPContractID == embedded {
				return item
			}
		}
		t.Fatalf("missing graph node %s/%s", kind, embedded)
		return ArtifactProjectionItem{}
	}
	baseline := project(func(*designscenario.Document) {})
	unrelated := project(func(d *designscenario.Document) {
		d.Title = "Other title"
		d.Messages[0].Label = "Other sequence annotation"
	})
	for _, tc := range []struct {
		name, kind, embedded string
		edit                 func(*designscenario.Document)
	}{
		{"schema", "schema", "", func(d *designscenario.Document) { d.EventModel.Schemas[0].SchemaJSON = `{"type":"string"}` }},
		{"host", "server", "", func(d *designscenario.Document) { d.EventModel.Servers[0].Host = "other:9092" }},
		{"auth", "server", "", func(d *designscenario.Document) { d.EventModel.Servers[0].Auth = "plain" }},
		{"api response", "api_operation", "copy", func(d *designscenario.Document) {
			editorMutateEmbeddedAPI(t, d, func(op map[string]any) {
				op["responses"] = map[string]any{"200": map[string]any{"description": "Other response"}}
			})
		}},
		{"api summary", "api_operation", "copy", func(d *designscenario.Document) {
			editorMutateEmbeddedAPI(t, d, func(op map[string]any) { op["summary"] = "Changed authored summary" })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := graph(baseline, tc.kind, tc.embedded)
			after := graph(project(tc.edit), tc.kind, tc.embedded)
			stable := graph(unrelated, tc.kind, tc.embedded)
			if before.ObjectHash == after.ObjectHash {
				t.Fatal("authored auxiliary content absent from object hash")
			}
			if before.ObjectHash != stable.ObjectHash {
				t.Fatal("unrelated owner field changed selected object hash")
			}
			if after.BindingSelector != nil || after.Locator.Owner.EventMap == nil || after.Data.EventNode == nil {
				t.Fatal("graph locator lost or binding union widened")
			}
		})
	}
	// Independent literal schema object, including the owner's normal empty fields.
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"domain":"backend-editor-object-v1","object":{"description":"","id":"schema","name":"","schemaJSON":"{}"}}`)))
	if graph(baseline, "schema", "").ObjectHash != expected {
		t.Fatal("graph hash is not authored schema hash")
	}
	found := map[string]bool{}
	for _, item := range baseline.Items {
		switch item.Data.Kind {
		case "event_server":
			found["server"] = true
			if item.Data.EventServer.Protocol != "kafka" || item.Data.EventServer.Auth != "none" {
				t.Fatal("server authored fields missing")
			}
		case "event_schema":
			found["schema"] = true
			if item.Data.EventSchema.SchemaJSON != "{}" {
				t.Fatal("schema authored fields missing")
			}
		case "api_operation":
			found["api"] = true
			if !strings.Contains(item.Data.APIOperation.DocumentJSON, `"responses"`) {
				t.Fatal("API authored document missing")
			}
		default:
			continue
		}
		if item.BindingSelector != nil || item.Locator.Owner.EventMap == nil {
			t.Fatal("authored auxiliary row locator/bindability changed")
		}
	}
	if len(found) != 3 {
		t.Fatalf("missing authored variants %v", found)
	}
}

func editorMutateEmbeddedAPI(t *testing.T, d *designscenario.Document, edit func(map[string]any)) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(d.Contracts[1].Document, &root); err != nil {
		t.Fatal(err)
	}
	edit(root["paths"].(map[string]any)["/x"].(map[string]any)["get"].(map[string]any))
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	d.Contracts[1].Document = raw
}

type editorCountingInspection struct {
	*designscenario.Repo
	calls int
}

func (r *editorCountingInspection) ArtifactInspectionSnapshot(ctx context.Context, id, rev int64) (*designscenario.ArtifactInspectionSnapshot, error) {
	r.calls++
	return r.Repo.ArtifactInspectionSnapshot(ctx, id, rev)
}
func TestEditorRealUnsupportedInspection(t *testing.T) {
	t.Parallel()
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	repo := designscenario.NewRepo(db, cfg, apidesign.NewRepo(db, cfg))
	doc := designscenario.Document{FormatVersion: 1, Title: "Stored raw", Participants: []designscenario.Participant{{ID: "p", Name: "Readable", Kind: "service"}}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}
	created, err := repo.Create(t.Context(), designscenario.CreateInput{Document: doc, FormDrafts: map[string]string{"panel": "buffer"}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := repo.ArtifactSnapshot(t.Context(), created.Scenario.ID, created.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Replace(old.DocumentJSON, `{"formatVersion"`, `{"future":{"precise":9007199254740993},"formatVersion"`, 1)
	storedHash := strings.Repeat("b", 64)
	var revision int64
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(t.Context(), `INSERT INTO design_scenario_revisions(scenario_id,version,hash,document,form_drafts,source,summary,created_at) VALUES (?,2,?,?,?,'ui','synthetic future revision',2)`, created.Scenario.ID, storedHash, raw, old.FormDraftsJSON)
		if err != nil {
			return err
		}
		revision, err = result.LastInsertId()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ArtifactSnapshot(t.Context(), created.Scenario.ID, revision); err == nil {
		t.Fatal("strict owner unexpectedly supports future schema")
	}
	inspection, err := repo.ArtifactInspectionSnapshot(t.Context(), created.Scenario.ID, revision)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.DocumentJSON != raw || inspection.FormDraftsJSON != old.FormDraftsJSON || inspection.ContentHash != "" || inspection.StoredContentHash != storedHash || inspection.TypedStatus != "unsupported" || inspection.EnvelopeVerification != "unavailable" || inspection.DocumentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) {
		t.Fatal("real raw inspection loses bytes or invents verification")
	}
	reader := &editorCountingInspection{Repo: repo}
	request := NewEditorArtifactRequest(t.Context(), nil, reader)
	pin := ArtifactPin{Kind: "design_scenario", ID: fmt.Sprint(created.Scenario.ID), RevisionID: fmt.Sprint(revision), ContentHash: storedHash}
	state := RevisionState{Revision: Revision{ID: editorBackendRevision, SemanticHash: strings.Repeat("a", 64), SourceSnapshotIDs: []string{editorSourceID}, ArtifactPins: []ArtifactPin{pin}}}
	binding := EditorBinding{ArtifactKind: pin.Kind, ArtifactID: pin.ID, Selector: EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{editorSourceID}, SourceLabels: []string{"Stored label"}, ObjectHash: strings.Repeat("c", 64), LastKnownLabel: "Readable", Origin: "manual", Reason: "Frozen association"}
	context := ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("d", 64), SourceSemanticHash: strings.Repeat("e", 64), APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{binding}}
	q := ArtifactQueryInput{RevisionID: editorBackendRevision, Artifact: ArtifactKey{pin.Kind, pin.ID}, View: "sequence"}
	page, err := request.Project(&state, context, q)
	if err != nil {
		t.Fatal(err)
	}
	if page.Complete || page.Resolution.Status != "unverified" || !page.BindingsComplete || len(page.EditorBindings) != 1 || len(page.Items) != 2 || len(page.Resolution.Diagnostics) == 0 {
		t.Fatalf("real unverified projection %+v", page)
	}
	if page.Items[0].Data.Participant == nil || page.Items[0].Data.Participant.Name != "Readable" || page.Items[0].Locator.Owner.Pointer != "/participants/0" || page.Items[1].Data.Unsupported == nil || !strings.Contains(page.Items[1].Data.Unsupported.DocumentJSON, "9007199254740993") {
		t.Fatal("understood/raw rows unavailable")
	}
	for _, item := range page.Items {
		if item.BindingSelector != nil || len(item.SourceNodeIDs) != 0 {
			t.Fatal("unverified snapshot gained binding/navigation")
		}
	}
	if _, err := request.SnapshotPin(q.Artifact, pin.RevisionID); err == nil {
		t.Fatal("unverified snapshot pinned")
	}
	if _, err := request.ResolveObject(pin, binding.Selector); err == nil {
		t.Fatal("unverified object bound")
	}
	if reader.calls != 1 {
		t.Fatalf("shared exact inspection read %d times", reader.calls)
	}
	var afterDocument, afterDrafts, afterHash string
	if err := db.R.QueryRowContext(t.Context(), `SELECT document,form_drafts,hash FROM design_scenario_revisions WHERE id=?`, revision).Scan(&afterDocument, &afterDrafts, &afterHash); err != nil {
		t.Fatal(err)
	}
	if afterDocument != raw || afterDrafts != old.FormDraftsJSON || afterHash != storedHash {
		t.Fatal("projection mutated stored owner")
	}
}

func TestEditorExactRawDeepPointerAllocation(t *testing.T) {
	// Deliberately small: the old ancestor-copy algorithm handles under1MiB of
	// cumulative raw payload here, enough to distinguish depth amplification.
	depth := 48
	payload := `{"n":9007199254740993,"payload":"` + strings.Repeat("x", 16384) + `"}`
	raw := strings.Repeat(`{"nested":`, depth) + payload + strings.Repeat("}", depth)
	pointer := strings.Repeat("/nested", depth)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	selected, err := editorExactRaw(raw, pointer)
	runtime.ReadMemStats(&after)
	if err != nil || selected != payload {
		t.Fatal("deep exact bytes lost", err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("raw=%dB depth=%d extraction allocated=%dB", len(raw), depth, allocated)
	if allocated > uint64(len(raw))*12 {
		t.Fatal("raw extraction copies ancestor payload per depth")
	}
}
func TestEditorExactRawPointerAndLargeSibling(t *testing.T) {
	t.Parallel()
	raw := `{"skip":"` + strings.Repeat("s", 16384) + `","a/b":{"~key":[null, { "n" : 9007199254740993, "s":"a\\b" }]},"":false}`
	for _, tc := range []struct{ pointer, want string }{{"/a~1b/~0key/0", "null"}, {"/a~1b/~0key/1", `{ "n" : 9007199254740993, "s":"a\\b" }`}, {"/", "false"}} {
		got, err := editorExactRaw(raw, tc.pointer)
		if err != nil || got != tc.want {
			t.Fatalf("%s=%s %v", tc.pointer, got, err)
		}
	}
	for _, pointer := range []string{"/absent", "/a~1b/~0key/4", "/a~1b/~0key/0/nope"} {
		if _, err := editorExactRaw(raw, pointer); err == nil {
			t.Fatal("missing raw selection accepted", pointer)
		}
	}
}

func TestEditorAuxiliaryInheritedDeepRaw(t *testing.T) {
	t.Parallel()
	const operation = `{"responses":{"200":{"description":"kept exact"}},"summary":"Authored","x-mocker-canvas-operation-id":"op"}`
	depth := 32
	ref := "#/x-tree" + strings.Repeat("/nested", depth)
	document := `{"openapi":"3.1.0","info":{"title":"Deep copy","version":"1"},"paths":{"/x":{"$ref":"` + ref + `"}},"largeSibling":"` + strings.Repeat("s", 16384) + `","x-tree":` + strings.Repeat(`{"nested":`, depth) + `{"get":` + operation + `}` + strings.Repeat("}", depth) + `}`
	f := newEditorProjectionFixture(t, func(d *designscenario.Document) { d.Contracts[1].Document = []byte(document) })
	q := f.query("event_model", "")
	q.Limit = 100
	page, err := f.request.Project(&f.state, f.context, q)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"domain":"backend-editor-object-v1","object":`+operation+`}`)))
	for _, item := range page.Items {
		if item.Data.APIOperation != nil && item.Locator.Embedded.ContractID == "copy" {
			if item.Data.APIOperation.DocumentJSON != operation || item.ObjectHash != wantHash || item.Locator.Owner.Pointer != "/contracts/1/document"+ref[1:]+"/get" || item.Data.APIOperation.Path != "/x" {
				t.Fatalf("deep inherited authored operation %+v", item)
			}
			return
		}
	}
	t.Fatal("inherited API authored row missing")
}

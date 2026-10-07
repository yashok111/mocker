package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/yashok111/mocker/internal/testkit"
	"slices"
	"strconv"
	"strings"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/apidesign"
)

type noArtifactHead struct{ APIArtifactReader }

type unavailableArtifact struct {
	APIArtifactReader
	id int64
}

func (r unavailableArtifact) ArtifactSnapshot(ctx context.Context, id, rid int64) (*apidesign.ArtifactSnapshot, error) {
	if id == r.id {
		return nil, apidesign.ErrNotFound
	}
	return r.APIArtifactReader.ArtifactSnapshot(ctx, id, rid)
}
func (r unavailableArtifact) ArtifactDigestTx(ctx context.Context, tx *sql.Tx, id, rid int64) (string, error) {
	if id == r.id {
		return "", apidesign.ErrNotFound
	}
	return r.APIArtifactReader.ArtifactDigestTx(ctx, tx, id, rid)
}

func TestAPIArtifactRetainedUnavailableAndExplicitRemove(t *testing.T) {
	s, base, ids, api := apiPinFixture(t)
	owner := s.artifacts.(*apidesign.Repo)
	other, err := owner.Create(t.Context(), apidesign.CreateInput{Name: "Field API", Document: artifactTestDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := pinTestInput(base, ids, api)
	in.Commands = append(in.Commands, APIPinCommand{Type: "set_api_pin", ArtifactID: strconv.FormatInt(other.Design.ID, 10), RevisionID: strconv.FormatInt(other.Draft.ID, 10), Reason: "Explicit field", Bindings: []APIPinBindingInput{{SourceNodeID: ids["request"], Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order"}}}})
	pinned, _ := applyPinTest(t, s, base.Project.ID, in, "both")
	s.artifacts = unavailableArtifact{owner, other.Design.ID}
	next := pinTestInput(base, ids, api)
	next.BaseRevisionID = pinned.Revision.ID
	next.ExpectedVersion = pinned.Project.Version
	preview, err := s.Preview(t.Context(), base.Project.ID, next)
	if err != nil || !preview.CanApply || len(preview.Pins) != 2 || !slices.ContainsFunc(preview.Diagnostics, func(d APIArtifactDiagnostic) bool { return d.Code == "backend_api_retained_unavailable" }) {
		t.Fatalf("unrelated missing group blocked %+v %v", preview, err)
	}
	modified, _ := applyPinTest(t, s, base.Project.ID, next, "unrelated-change")
	bad := PreviewAPIPinsInput{BaseRevisionID: modified.Revision.ID, ExpectedVersion: modified.Project.Version, Commands: in.Commands[1:]}
	blocked, err := s.Preview(t.Context(), base.Project.ID, bad)
	if err != nil || blocked.CanApply || !slices.ContainsFunc(blocked.Diagnostics, func(d APIArtifactDiagnostic) bool { return d.Code == "backend_api_unavailable_previous" }) {
		t.Fatalf("unavailable old replacement %+v %v", blocked, err)
	}
	bad.Commands = []APIPinCommand{{Type: "remove_api_pin", ArtifactID: strconv.FormatInt(other.Design.ID, 10), Reason: "Remove unavailable reference"}}
	detach, err := s.Preview(t.Context(), base.Project.ID, bad)
	if err != nil || !detach.CanApply || !slices.ContainsFunc(detach.Diagnostics, func(d APIArtifactDiagnostic) bool { return d.Code == "backend_api_unavailable_previous" }) {
		t.Fatalf("explicit unavailable removal %+v %v", detach, err)
	}
	removed, _ := applyPinTest(t, s, base.Project.ID, bad, "detach")
	if len(removed.Revision.ArtifactPins) != 1 {
		t.Fatal("unavailable removal lost unrelated group")
	}
}

func TestAPIArtifactGoCallBodyLimitBeforeDependencies(t *testing.T) {
	s, base, _, api := apiPinFixture(t)
	bindings := []APIPinBindingInput{}
	for range 200 {
		bindings = append(bindings, APIPinBindingInput{SourceNodeID: uuid.NewV7().String(), Selector: APIArtifactSelector{JSONPointer: "/components/schemas/" + strings.Repeat("x", 2000)}})
	}
	in := PreviewAPIPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []APIPinCommand{{Type: "set_api_pin", ArtifactID: strconv.FormatInt(api.Design.ID, 10), RevisionID: strconv.FormatInt(api.Draft.ID, 10), Reason: "Manual", Bindings: bindings}}}
	_, err := NewAPIArtifactService(s.repo, nil).Preview(t.Context(), base.Project.ID, in)
	assertFault(t, err, "backend_api_pins_limit")
}

func (r noArtifactHead) ArtifactHead(context.Context, int64) (int64, error) {
	return 0, errors.New("unavailable optional head")
}

func TestAPIArtifactQueryCursorOrphansAndOptionalHead(t *testing.T) {
	s, base, ids, api := apiPinFixture(t)
	in := pinTestInput(base, ids, api)
	in.Commands[0].Bindings = append(in.Commands[0].Bindings, APIPinBindingInput{SourceNodeID: ids["request"], Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order"}}, APIPinBindingInput{SourceNodeID: ids["response"], Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order/properties/total"}})
	pinned, _ := applyPinTest(t, s, base.Project.ID, in, "pin")
	s.artifacts = noArtifactHead{s.artifacts}
	first, err := s.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: pinned.Revision.ID, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" || first.Items[0].Resolution.Status != "resolved" || first.Items[0].Resolution.CurrentDraftRevisionID != "" {
		t.Fatalf("optional head %+v %v", first, err)
	}
	second, err := s.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: pinned.Revision.ID, Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].Binding.SourceNodeID <= first.Items[0].Binding.SourceNodeID {
		t.Fatalf("pagination %+v %v", second, err)
	}
	for _, q := range []APIArtifactQueryInput{{RevisionID: pinned.Revision.ID, Limit: 2, Cursor: first.NextCursor}, {RevisionID: base.Revision.ID, Limit: 1, Cursor: first.NextCursor}, {RevisionID: pinned.Revision.ID, SourceNodeID: ids["request"], Limit: 1, Cursor: first.NextCursor}} {
		_, err = s.Query(t.Context(), base.Project.ID, q)
		assertFault(t, err, "backend_invalid")
	}
	// Store27 (48dce80, B6.3) seals graph payload membership; the orphaning
	// delete goes through the Store26 fixture rebuild + production migration.
	if _, err = testkit.EditLegacyBackendPayload(t.Context(), s.repo.db, `DELETE FROM backend_graph_records WHERE revision_id=? AND record_type='node' AND id=?`, pinned.Revision.ID, ids["request"]); err != nil {
		t.Fatal(err)
	}
	orphan, err := s.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: pinned.Revision.ID, SourceNodeID: ids["request"]})
	if err != nil || len(orphan.Items) != 1 || orphan.Items[0].Resolution.Status != "orphaned" || orphan.Items[0].Binding.SourceLastKnownLabel == "" {
		t.Fatalf("orphan hidden %+v %v", orphan, err)
	}
}

func TestAPIArtifactMissingContextMustNotHidePins(t *testing.T) {
	s, base, ids, api := apiPinFixture(t)
	pinned, _ := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "pin")
	// A historical corrupt fixture must not silently become an empty pin page.
	// Store27 (48dce80, B6.3) guards the row with an immutable-owner trigger and
	// a sealed manifest, so the corrupt shape is built as a Store26 fixture and
	// published by the production migration.
	if err := testkit.EditLegacyBackendFixture(t.Context(), s.repo.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `DROP TRIGGER backend_revision_api_artifacts_immutable_delete`); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `DELETE FROM backend_revision_api_artifacts WHERE revision_id=?`, pinned.Revision.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: pinned.Revision.ID})
	if err == nil {
		t.Fatal("API pins without frozen context returned a successful empty page")
	}
}

func TestAPIArtifactDeletedSelectorRemapAndMovedOperation(t *testing.T) {
	s, base, ids, api := apiPinFixture(t)
	in := pinTestInput(base, ids, api)
	in.Commands[0].Bindings = append(in.Commands[0].Bindings, APIPinBindingInput{SourceNodeID: ids["request"], Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order/properties/total"}})
	pinned, _ := applyPinTest(t, s, base.Project.ID, in, "initial")
	owner := s.artifacts.(*apidesign.Repo)
	document := strings.Replace(strings.Replace(artifactTestDocument, `"/orders"`, `"/moved"`, 1), `"total":{"type":"number"},`, "", 1)
	target, err := owner.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: api.Design.Version, Document: document, Summary: "Move and delete", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in.BaseRevisionID = pinned.Revision.ID
	in.ExpectedVersion = pinned.Project.Version
	in.Commands[0].RevisionID = strconv.FormatInt(target.Draft.ID, 10)
	in.Commands[0].Bindings[1].Selector = APIArtifactSelector{JSONPointer: "/components/schemas/Order/properties/secret"}
	preview, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil || !preview.CanApply {
		t.Fatalf("remap %+v %v", preview, err)
	}
	if !slices.ContainsFunc(preview.Diff, func(d APIArtifactDiff) bool {
		return d.SourceNodeID == ids["request"] && d.Status == "missing" && d.Before.Selector.JSONPointer == "/components/schemas/Order/properties/total"
	}) || !slices.ContainsFunc(preview.Diff, func(d APIArtifactDiff) bool {
		return d.SourceNodeID == ids["request"] && d.Status == "changed" && d.After.Selector.JSONPointer == "/components/schemas/Order/properties/secret"
	}) || !slices.ContainsFunc(preview.Diff, func(d APIArtifactDiff) bool { return d.SourceNodeID == ids["http"] && d.Status == "moved" }) {
		t.Fatalf("old checks or move lost %+v", preview.Diff)
	}
	applyPinTest(t, s, base.Project.ID, in, "remap")
}

func TestAPIArtifactDiffBoundAndNoRawDiagnosticValues(t *testing.T) {
	for _, tc := range []struct {
		name          string
		changes       int
		wantEntries   int
		wantTruncated bool
	}{
		{name: "999", changes: 999, wantEntries: 999},
		{name: "1000", changes: 1000, wantEntries: 1000},
		{name: "1001", changes: 1001, wantEntries: 1000, wantTruncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, base, ids, api := apiPinFixture(t)
			in := pinTestInput(base, ids, api)
			in.Commands[0].Bindings = []APIPinBindingInput{{SourceNodeID: ids["request"], Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order"}}}
			pinned, _ := applyPinTest(t, s, base.Project.ID, in, "initial")
			owner := s.artifacts.(*apidesign.Repo)
			target, err := owner.Save(t.Context(), api.Design.ID, apidesign.SaveInput{
				ExpectedVersion: api.Design.Version,
				Document:        apiArtifactDiffBoundaryDocument(tc.changes),
				Summary:         "Selected diff boundary",
				Source:          "ui",
			})
			if err != nil {
				t.Fatal(err)
			}
			in.BaseRevisionID = pinned.Revision.ID
			in.ExpectedVersion = pinned.Project.Version
			in.Commands[0].RevisionID = strconv.FormatInt(target.Draft.ID, 10)
			preview, err := s.Preview(t.Context(), base.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			if preview.DiffTruncated != tc.wantTruncated || preview.CanApply == tc.wantTruncated {
				t.Fatalf("%d changes: truncated=%v canApply=%v", tc.changes, preview.DiffTruncated, preview.CanApply)
			}
			if len(preview.Diff) != 1 || len(preview.Diff[0].Changes) != tc.wantEntries {
				t.Fatalf("%d changes: incomplete diff %+v", tc.changes, preview.Diff)
			}
			for i, change := range preview.Diff[0].Changes {
				if change.Pointer != fmt.Sprintf("/properties/field%04d", i) || change.Kind != "added" {
					t.Fatalf("change %d: unexpected order or kind %+v", i, change)
				}
			}
			if tc.wantTruncated {
				if len(preview.Diagnostics) != 1 || preview.Diagnostics[0].Code != "backend_api_diff_truncated" || preview.Diagnostics[0].Pointer != "" {
					t.Fatalf("expected one static truncation diagnostic: %+v", preview.Diagnostics)
				}
			} else if len(preview.Diagnostics) != 0 {
				t.Fatalf("complete diff produced diagnostics: %+v", preview.Diagnostics)
			}
			raw, err := json.Marshal(preview)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "PRIVATE_SENTINEL") {
				t.Fatal("preview retained raw values")
			}
			result, err := s.Apply(t.Context(), base.Project.ID, ApplyAPIPinsInput{
				BaseRevisionID:  in.BaseRevisionID,
				ExpectedVersion: in.ExpectedVersion,
				Commands:        in.Commands,
				CandidateHash:   preview.CandidateHash,
				IdempotencyKey:  "boundary-apply",
			})
			if tc.wantTruncated {
				assertFault(t, err, "backend_api_pins_blocked")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Project.Version != pinned.Project.Version+1 || len(result.Revision.ArtifactPins) != 1 || result.Revision.ArtifactPins[0].RevisionID != in.Commands[0].RevisionID {
				t.Fatalf("complete boundary update was not persisted: %+v", result)
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := apiArtifactObjectDiff(canceled, `{"old":true}`, `{"new":true}`, 1000)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("uncancelable diff %v", err)
	}
}

func apiArtifactDiffBoundaryDocument(changes int) string {
	entries := make([]string, 0, changes)
	for i := range changes {
		entries = append(entries, fmt.Sprintf(`"field%04d":{"description":"PRIVATE_SENTINEL","type":"string"}`, i))
	}
	return strings.Replace(artifactTestDocument, `"properties":{`, `"properties":{`+strings.Join(entries, ",")+",", 1)
}

func TestAPIArtifactDiffSharedBudgetAllowsLaterUnchangedObject(t *testing.T) {
	for _, tc := range []struct {
		name          string
		changeLast    bool
		wantTruncated bool
	}{
		{name: "1000_then_unchanged"},
		{name: "1000_then_changed", changeLast: true, wantTruncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, base, ids, api := apiPinFixture(t)
			owner := s.artifacts.(*apidesign.Repo)
			artifacts := []*apidesign.Detail{api}
			for range 2 {
				other, err := owner.Create(t.Context(), apidesign.CreateInput{Name: "Another API", Document: artifactTestDocument, Source: "ui"})
				if err != nil {
					t.Fatal(err)
				}
				artifacts = append(artifacts, other)
			}
			// Artifact groups are processed by their decimal-string ID. Keep the
			// zero-budget object last independently of the source-node ID order.
			slices.SortFunc(artifacts, func(a, b *apidesign.Detail) int {
				return strings.Compare(strconv.FormatInt(a.Design.ID, 10), strconv.FormatInt(b.Design.ID, 10))
			})
			in := PreviewAPIPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []APIPinCommand{}}
			for i, source := range []string{ids["request"], ids["response"], ids["http"]} {
				selector := APIArtifactSelector{JSONPointer: "/components/schemas/Order"}
				if i == 2 {
					selector = APIArtifactSelector{ObjectKey: "orders-read"}
				}
				in.Commands = append(in.Commands, APIPinCommand{
					Type:       "set_api_pin",
					ArtifactID: strconv.FormatInt(artifacts[i].Design.ID, 10),
					RevisionID: strconv.FormatInt(artifacts[i].Draft.ID, 10),
					Reason:     "Shared structural diff budget",
					Bindings:   []APIPinBindingInput{{SourceNodeID: source, Selector: selector}},
				})
			}
			pinned, _ := applyPinTest(t, s, base.Project.ID, in, "initial")
			in.BaseRevisionID = pinned.Revision.ID
			in.ExpectedVersion = pinned.Project.Version
			for i, artifact := range artifacts {
				document := apiArtifactDiffBoundaryDocument(500)
				if i == 2 {
					document = artifactTestDocument
					if tc.changeLast {
						document = strings.Replace(document, `"description":"OK"`, `"description":"PRIVATE_SENTINEL"`, 1)
					}
				}
				if i == 2 && !tc.changeLast {
					continue
				}
				target, err := owner.Save(t.Context(), artifact.Design.ID, apidesign.SaveInput{
					ExpectedVersion: artifact.Design.Version,
					Document:        document,
					Summary:         "Shared diff boundary",
					Source:          "ui",
				})
				if err != nil {
					t.Fatal(err)
				}
				in.Commands[i].RevisionID = strconv.FormatInt(target.Draft.ID, 10)
			}
			preview, err := s.Preview(t.Context(), base.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			if preview.DiffTruncated != tc.wantTruncated || preview.CanApply == tc.wantTruncated || len(preview.Diff) != 3 {
				t.Fatalf("shared boundary: truncated=%v canApply=%v rows=%d", preview.DiffTruncated, preview.CanApply, len(preview.Diff))
			}
			total := 0
			for i, row := range preview.Diff {
				if i > 0 && preview.Diff[i-1].SourceNodeID >= row.SourceNodeID {
					t.Fatal("diff rows are not deterministically ordered by source")
				}
				if row.SourceNodeID == ids["http"] {
					wantStatus := "unchanged"
					if tc.changeLast {
						wantStatus = "changed"
					}
					if len(row.Changes) != 0 || row.Status != wantStatus {
						t.Fatalf("later zero-budget object: %+v", row)
					}
				} else {
					if len(row.Changes) != 500 || row.Status != "changed" {
						t.Fatalf("shared budget lost a selected object's changes: %+v", row)
					}
					for j, change := range row.Changes {
						if change.Pointer != fmt.Sprintf("/properties/field%04d", j) || change.Kind != "added" {
							t.Fatalf("change %d: unexpected order or kind %+v", j, change)
						}
					}
				}
				total += len(row.Changes)
			}
			if total != 1000 {
				t.Fatalf("shared structural diff count=%d, want 1000", total)
			}
			if tc.wantTruncated {
				if len(preview.Diagnostics) != 1 || preview.Diagnostics[0].Code != "backend_api_diff_truncated" || preview.Diagnostics[0].SourceNodeID != ids["http"] || preview.Diagnostics[0].Pointer != "" {
					t.Fatalf("truncation must identify the actual omitted change: %+v", preview.Diagnostics)
				}
			} else if len(preview.Diagnostics) != 0 {
				t.Fatalf("unchanged zero-budget object produced diagnostics: %+v", preview.Diagnostics)
			}
			raw, err := json.Marshal(preview)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "PRIVATE_SENTINEL") {
				t.Fatal("shared diff retained raw values")
			}
			result, err := s.Apply(t.Context(), base.Project.ID, ApplyAPIPinsInput{
				BaseRevisionID:  in.BaseRevisionID,
				ExpectedVersion: in.ExpectedVersion,
				Commands:        in.Commands,
				CandidateHash:   preview.CandidateHash,
				IdempotencyKey:  "shared-boundary-apply",
			})
			if tc.wantTruncated {
				assertFault(t, err, "backend_api_pins_blocked")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Project.Version != pinned.Project.Version+1 || len(result.Revision.ArtifactPins) != 3 {
				t.Fatalf("complete shared-boundary update was not persisted: %+v", result)
			}
			for i, pin := range result.Revision.ArtifactPins {
				if pin.ID != in.Commands[i].ArtifactID || pin.RevisionID != in.Commands[i].RevisionID {
					t.Fatalf("shared-boundary pin %d differs from selection: %+v", i, pin)
				}
			}
		})
	}
}

func TestAPIArtifactSnapshotBudgetAndRetainedUnavailableGroup(t *testing.T) {
	s, base, ids, api := apiPinFixture(t)
	initial, _ := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "initial")
	// Model an immutable legacy history with twenty distinct exact API snapshots.
	var revision Revision
	doc, err := json.Marshal(initial.Revision)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(doc, &revision)
	revision.ID = uuid.NewV7().String()
	revision.ArtifactPins = []ArtifactPin{}
	context := APIArtifactContext{SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: base.Revision.SemanticHash, Bindings: []APIArtifactBinding{}}
	commands := []APIPinCommand{}
	for i := range 20 {
		id := strconv.Itoa(i + 1)
		source := uuid.NewV7().String()
		revision.ArtifactPins = append(revision.ArtifactPins, ArtifactPin{Kind: "api_design", ID: id, RevisionID: "1", ContentHash: strings.Repeat("b", 64)})
		context.Bindings = append(context.Bindings, APIArtifactBinding{SourceNodeID: source, SourceKind: "api_field", Ref: ArtifactRef{Kind: "api_design", ArtifactID: id, RevisionID: "1", ContentHash: strings.Repeat("b", 64), ObjectHash: strings.Repeat("c", 64), Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order"}, ResolvedPointer: "/components/schemas/Order"}, Origin: "manual", Reason: "Manual"})
		commands = append(commands, APIPinCommand{Type: "set_api_pin", ArtifactID: id, RevisionID: "2", Reason: "New exact snapshot", Bindings: []APIPinBindingInput{{SourceNodeID: source, Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order"}}}})
	}
	document, _ := json.Marshal(revision)
	err = s.repo.db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := testkit.ExecBackendOwner(t.Context(), tx, `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,?)`, revision.ID, base.Project.ID, string(document)); err != nil {
			return err
		}
		if _, err := testkit.ExecBackendOwner(t.Context(), tx, `INSERT INTO backend_revision_sources(revision_id,document) SELECT ?,document FROM backend_revision_sources WHERE revision_id=?`, revision.ID, base.Revision.ID); err != nil {
			return err
		}
		if err := saveAPIArtifactContext(t.Context(), tx, revision.ID, context); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `UPDATE backend_projects SET current_revision_id=? WHERE id=?`, revision.ID, base.Project.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewAPIArtifactService(s.repo, nil).Preview(t.Context(), base.Project.ID, PreviewAPIPinsInput{BaseRevisionID: revision.ID, ExpectedVersion: initial.Project.Version, Commands: commands})
	assertFault(t, err, "backend_api_pins_limit")
}

package apidesign

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/statediagram"
)

func TestManagedStateProjectionOnlyAppliedAndUnionsRuleAncestors(t *testing.T) {
	t.Parallel()
	for _, extension := range []string{statediagram.Extension, statediagram.ExecutionExtension} {
		t.Run(extension, func(t *testing.T) {
			t.Parallel()
			r, db := testRepo(t)
			root, err := decodeDocument(stateExecutionDocument(t, extension, "/orders/{}/items", "status"))
			if err != nil {
				t.Fatal(err)
			}
			if extension == statediagram.ExecutionExtension {
				rules, err := decodeDocument(managedEntityDocument(t, "/orders", responserules.ExecutionExtension))
				if err != nil {
					t.Fatal(err)
				}
				root[responserules.ExecutionExtension] = rules[responserules.ExecutionExtension]
			}
			raw, err := jsonx.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			detail, err := r.Create(t.Context(), CreateInput{Name: "State projection", Document: string(raw), Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			repo := resources.NewRepo(db, r.specs, 1<<20, 1<<20, 1000)
			roster, err := repo.ForWorkspace(t.Context(), detail.Design.DraftWorkspaceID)
			want := 0
			if extension == statediagram.ExecutionExtension {
				want = 2
			}
			if err != nil || len(roster) != want {
				t.Fatalf("union roster=%+v err=%v; want %d", roster, err, want)
			}
			for _, resource := range roster {
				if resource.Seq != 0 || resource.SeedCount != 0 {
					t.Fatalf("projected seed=%+v", resource)
				}
				rows, err := repo.List(t.Context(), resource.ID, "", "")
				if err != nil || len(rows) != 0 {
					t.Fatalf("generated entity rows: %+v %v", rows, err)
				}
				if resource.RouteFamily == "/orders/{}/items" && len(resource.ScopeParams) != 1 {
					t.Fatalf("lost ancestor metadata: %+v", resource)
				}
			}
			published, err := repo.ForWorkspace(t.Context(), detail.Design.PublishedWorkspaceID)
			if err != nil || len(published) != 0 {
				t.Fatalf("draft projected into publication: %+v %v", published, err)
			}
		})
	}
}

func TestManagedStateProjectionKeepsDatasetsAndFrozenReview(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	ctx := t.Context()
	detail, err := r.Create(ctx, CreateInput{Name: "State projection", Document: stateExecutionDocument(t, statediagram.Extension, "/orders", "status"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.EditStateDiagramExecution(ctx, detail.Design.ID, detail.Design.Version, "ui", "lifecycle", true)
	if err != nil {
		t.Fatal(err)
	}
	firstAppliedRevision := detail.Draft.ID
	repo := resources.NewRepo(db, r.specs, 1<<20, 1<<20, 1000)
	roster, err := repo.ForWorkspace(ctx, detail.Design.DraftWorkspaceID)
	if err != nil || len(roster) != 1 {
		t.Fatalf("draft roster=%+v err=%v", roster, err)
	}
	resource := roster[0]
	draftRow, err := repo.Create(ctx, resource.ID, "", "", resource.IDField, resource.Wrapper.IDType, map[string]any{"name": "draft", "exact": jsonx.Number("9007199254740993")})
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.RequestReview(ctx, detail.Design.ID, detail.Design.Version, "ready", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(ctx, detail.Design.ID, review.ID, detail.Design.Version, "ui"); err != nil {
		t.Fatal(err)
	}
	authoring, err := r.StateDiagrams(ctx, detail.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	diagram := authoring.Diagrams[0]
	diagram.Name = "Newer lifecycle"
	detail, err = r.EditStateDiagram(ctx, detail.Design.ID, detail.Design.Version, "ui", diagram.ID, "save", &diagram, nil)
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.EditStateDiagramExecution(ctx, detail.Design.ID, detail.Design.Version, "ui", diagram.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(ctx, detail.Design.ID, review.ID, detail.Design.Version, "ui"); err != nil {
		t.Fatalf("idempotent publication: %v", err)
	}
	publishedDetail, err := r.Detail(ctx, detail.Design.ID)
	if err != nil || publishedDetail.Published == nil {
		t.Fatalf("published detail=%+v err=%v", publishedDetail, err)
	}
	publishedRoot, err := decodeDocument(publishedDetail.Published.Document)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := statediagram.DecodeExecution(publishedRoot)
	if err != nil || len(applied.Diagrams) != 1 || applied.Diagrams[0].Name != "Lifecycle" {
		t.Fatalf("new source leaked into reviewed publication: %+v %v", applied, err)
	}
	published, err := repo.ForWorkspace(ctx, detail.Design.PublishedWorkspaceID)
	if err != nil || len(published) != 1 || published[0].ID == resource.ID {
		t.Fatalf("published roster=%+v err=%v", published, err)
	}
	rows, err := repo.List(ctx, published[0].ID, "", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("draft dataset leaked: %+v %v", rows, err)
	}
	publicationRow, err := repo.Create(ctx, published[0].ID, "", "", published[0].IDField, published[0].Wrapper.IDType, map[string]any{"name": "published"})
	if err != nil {
		t.Fatal(err)
	}
	// Dormant resources keep their identity and data, and restore/reactivation
	// reuses the same projected row and sequence rather than reseeding it.
	detail, err = r.EditStateDiagramExecution(ctx, detail.Design.ID, detail.Design.Version, "ui", diagram.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	roster, err = repo.ForWorkspace(ctx, detail.Design.DraftWorkspaceID)
	if err != nil || len(roster) != 1 || roster[0].ID != resource.ID || roster[0].Seq != 1 {
		t.Fatalf("dormant resource lost: %+v %v", roster, err)
	}
	detail, err = r.Restore(ctx, detail.Design.ID, firstAppliedRevision, detail.Design.Version, "restore lifecycle", "ui")
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.EditStateDiagramExecution(ctx, detail.Design.ID, detail.Design.Version, "ui", diagram.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	row, found, err := repo.Get(ctx, resource.ID, "", "", "1")
	if err != nil || !found || string(row.Data) != string(draftRow.Data) || !strings.Contains(string(row.Data), "9007199254740993") {
		t.Fatalf("reapply changed draft data: %+v %v %v", row, found, err)
	}
	review, err = r.RequestReview(ctx, detail.Design.ID, detail.Design.Version, "ready again", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(ctx, detail.Design.ID, review.ID, detail.Design.Version, "ui"); err != nil {
		t.Fatal(err)
	}
	row, found, err = repo.Get(ctx, published[0].ID, "", "", "1")
	if err != nil || !found || string(row.Data) != string(publicationRow.Data) {
		t.Fatalf("republish changed release data: %+v %v %v", row, found, err)
	}
	published, err = repo.ForWorkspace(ctx, detail.Design.PublishedWorkspaceID)
	if err != nil || len(published) != 1 || published[0].Seq != 1 {
		t.Fatalf("republish reset sequence: %+v %v", published, err)
	}
}

func TestManagedStateProjectionIDFieldRefusalRollsBack(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"create", "apply", "save"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			r, db := testRepo(t)
			var before *Detail
			var err error
			if action != "create" {
				before, err = r.Create(t.Context(), CreateInput{Name: "ID refusal", Document: stateExecutionDocument(t, statediagram.Extension, "/orders", "id"), Source: "ui"})
				if err != nil {
					t.Fatal(err)
				}
			}
			switch action {
			case "create":
				_, err = r.Create(t.Context(), CreateInput{Name: "ID refusal", Document: stateExecutionDocument(t, statediagram.ExecutionExtension, "/orders", "id"), Source: "ui"})
			case "apply":
				_, err = r.EditStateDiagramExecution(t.Context(), before.Design.ID, before.Design.Version, "ui", "lifecycle", true)
			case "save":
				_, err = r.Save(t.Context(), before.Design.ID, SaveInput{ExpectedVersion: before.Design.Version, Document: stateExecutionDocument(t, statediagram.ExecutionExtension, "/orders", "id"), Source: "ui"})
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("state ID accepted: %v", err)
			}
			if before != nil {
				after, err := r.Detail(t.Context(), before.Design.ID)
				if err != nil || after.Design.Version != before.Design.Version || after.Draft.ID != before.Draft.ID || len(after.Revisions) != len(before.Revisions) {
					t.Fatalf("refused operation mutated draft: %+v %v", after, err)
				}
			}
			for _, table := range []string{"resources", "resource_decisions", "entities"} {
				var count int
				if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s survived refusal: %d %v", table, count, err)
				}
			}
			if action == "create" {
				for _, table := range []string{"api_designs", "api_design_revisions", "specs", "workspaces"} {
					var count int
					if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
						t.Fatalf("%s survived failed create: %d %v", table, count, err)
					}
				}
			}
		})
	}
}

func TestManagedStateProjectionPublicationFailureKeepsReviewPending(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "State projection", Document: stateExecutionDocument(t, statediagram.ExecutionExtension, "/orders", "status"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.RequestReview(t.Context(), detail.Design.ID, detail.Design.Version, "ready", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER reject_state_publication BEFORE INSERT ON resources BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(t.Context(), detail.Design.ID, review.ID, detail.Design.Version, "ui"); err == nil {
		t.Fatal("projection failure allowed publication")
	}
	after, err := r.Detail(t.Context(), detail.Design.ID)
	if err != nil || after.Published != nil || len(after.Releases) != 0 || after.Reviews[0].Status != "pending" {
		t.Fatalf("failed publish changed review: %+v %v", after, err)
	}
	var specID *int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT spec_id FROM workspaces WHERE id=?", detail.Design.PublishedWorkspaceID).Scan(&specID); err != nil || specID != nil {
		t.Fatalf("bound failed publication: %v %v", specID, err)
	}
}

func TestManagedStateProjectionRestoreFailureKeepsRevisionAndRows(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Restore state", Document: stateExecutionDocument(t, statediagram.ExecutionExtension, "/orders", "status"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	firstRevision := detail.Draft.ID
	repo := resources.NewRepo(db, r.specs, 1<<20, 1<<20, 1000)
	roster, err := repo.ForWorkspace(t.Context(), detail.Design.DraftWorkspaceID)
	if err != nil || len(roster) != 1 {
		t.Fatalf("roster=%+v err=%v", roster, err)
	}
	beforeRow, err := repo.Create(t.Context(), roster[0].ID, "", "", "id", "integer", map[string]any{"status": "paid"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.Save(t.Context(), detail.Design.ID, SaveInput{ExpectedVersion: detail.Design.Version, Document: strings.Replace(detail.Draft.Document, `"title": "Entities"`, `"title": "Changed"`, 1), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER reject_state_restore BEFORE UPDATE ON resources BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restore(t.Context(), detail.Design.ID, firstRevision, detail.Design.Version, "restore", "ui"); err == nil {
		t.Fatal("projection failure allowed restore")
	}
	after, err := r.Detail(t.Context(), detail.Design.ID)
	if err != nil || after.Design.Version != detail.Design.Version || after.Draft.ID != detail.Draft.ID || len(after.Revisions) != len(detail.Revisions) {
		t.Fatalf("failed restore changed revision: %+v %v", after, err)
	}
	row, found, err := repo.Get(t.Context(), roster[0].ID, "", "", "1")
	if err != nil || !found || string(row.Data) != string(beforeRow.Data) {
		t.Fatalf("failed restore changed data: %+v %v %v", row, found, err)
	}
	roster, err = repo.ForWorkspace(t.Context(), detail.Design.DraftWorkspaceID)
	if err != nil || len(roster) != 1 || roster[0].Seq != 1 {
		t.Fatalf("failed restore changed sequence: %+v %v", roster, err)
	}
}

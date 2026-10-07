package apidesign

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
)

const managedEntityContract = `{"openapi":"3.1.0","info":{"title":"Entities","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"array","items":{"$ref":"#/components/schemas/Order"}}}}}}},"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}},"responses":{"201":{"description":"Created","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}},"/orders/{orderId}":{"parameters":[{"in":"path","name":"orderId","required":true,"schema":{"type":"integer"}}],"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}},"/orders/{orderId}/items":{"parameters":[{"in":"path","name":"orderId","required":true,"schema":{"type":"integer"}}],"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"array","items":{"$ref":"#/components/schemas/Order"}}}}}}}},"/orders/{orderId}/items/{itemId}":{"parameters":[{"in":"path","name":"orderId","required":true,"schema":{"type":"integer"}},{"in":"path","name":"itemId","required":true,"schema":{"type":"integer"}}],"get":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}}},"components":{"schemas":{"Order":{"type":"object","required":["id"],"properties":{"id":{"type":"integer"},"name":{"type":"string"}}}}}}`

func managedEntityDocument(t *testing.T, family, extension string) string {
	t.Helper()
	root, err := decodeDocument(managedEntityContract)
	if err != nil {
		t.Fatal(err)
	}
	if extension != "" {
		operation := &responserules.EntityOperation{Family: family, Data: &responserules.ValueRef{Source: "body"}}
		if family != "/orders" {
			operation.Scope = new([]responserules.ValueRef{{Source: "literal", ValueJSON: new(`"1"`)}})
		}
		root[extension] = responserules.Envelope{FormatVersion: 1, Rules: []responserules.Rule{{
			ID: "create", Name: "Create", Binding: &responserules.Binding{Method: "POST", Path: "/orders"},
			Nodes: []responserules.Node{
				{ID: "start", Type: "start", Name: "Start"},
				{ID: "create", Type: "entity_create", Name: "Create", Entity: operation},
				{ID: "reply", Type: "response", Name: "Reply", Response: &responserules.Response{Status: 201, MediaType: "application/json", Headers: []responserules.Field{}, BodyFrom: &responserules.ValueRef{Source: "result", NodeID: "create"}}},
			},
			Edges: []responserules.Edge{{ID: "a", From: "start", Port: "next", To: "create"}, {ID: "b", From: "create", Port: "next", To: "reply"}},
		}}}
	}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestManagedEntityProjectionCreateOnlyAppliedFamilies(t *testing.T) {
	r, db := testRepo(t)
	ctx := t.Context()
	for _, extension := range []string{responserules.Extension, responserules.ExecutionExtension} {
		t.Run(extension, func(t *testing.T) {
			detail, err := r.Create(ctx, CreateInput{Name: "Entities", Document: managedEntityDocument(t, "/orders", extension), Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			var draftCount, publishedCount, entityCount int
			if err := db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM resources WHERE workspace_id=?", detail.Design.DraftWorkspaceID).Scan(&draftCount); err != nil {
				t.Fatal(err)
			}
			if err := db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM resources WHERE workspace_id=?", detail.Design.PublishedWorkspaceID).Scan(&publishedCount); err != nil {
				t.Fatal(err)
			}
			if err := db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities").Scan(&entityCount); err != nil {
				t.Fatal(err)
			}
			want := 0
			if extension == responserules.ExecutionExtension {
				want = 1
			}
			if draftCount != want || publishedCount != 0 || entityCount != 0 {
				t.Fatalf("projection draft=%d published=%d entities=%d; want %d, 0, 0", draftCount, publishedCount, entityCount, want)
			}
		})
	}
}

func TestManagedEntityProjectionPreservesDataAndReviewedIsolation(t *testing.T) {
	r, db := testRepo(t)
	ctx := t.Context()
	detail, err := r.Create(ctx, CreateInput{Name: "Entities", Document: managedEntityDocument(t, "/orders", responserules.Extension), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.EditResponseRuleExecution(ctx, detail.Design.ID, detail.Design.Version, "ui", "create", true)
	if err != nil {
		t.Fatal(err)
	}
	repo := resources.NewRepo(db, r.specs, 1<<20, 1<<20, 1000)
	roster, err := repo.ForWorkspace(ctx, detail.Design.DraftWorkspaceID)
	if err != nil || len(roster) != 1 {
		t.Fatalf("applied draft roster=%+v err=%v", roster, err)
	}
	res := roster[0]
	if res.IDField != "id" || res.Wrapper.IDType != "integer" || res.Seq != 0 || res.SeedCount != 0 || res.WriteForm == nil || *res.WriteForm != "bare" {
		t.Fatalf("projected metadata: %+v", res)
	}
	created, err := repo.Create(ctx, res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "draft", "exact": jsonx.Number("9007199254740993")})
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
	published, err := repo.ForWorkspace(ctx, detail.Design.PublishedWorkspaceID)
	if err != nil || len(published) != 1 || published[0].ID == res.ID {
		t.Fatalf("published projection=%+v err=%v", published, err)
	}
	rows, err := repo.List(ctx, published[0].ID, "", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("draft entity leaked into release: %+v %v", rows, err)
	}
	publishedRow, err := repo.Create(ctx, published[0].ID, "", "", "id", "integer", map[string]any{"name": "published"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.EditResponseRuleExecution(ctx, detail.Design.ID, detail.Design.Version, "ui", "create", false)
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.EditResponseRuleExecution(ctx, detail.Design.ID, detail.Design.Version, "ui", "create", true)
	if err != nil {
		t.Fatal(err)
	}
	roster, err = repo.ForWorkspace(ctx, detail.Design.DraftWorkspaceID)
	if err != nil || len(roster) != 1 || roster[0].ID != res.ID || roster[0].Seq != 1 {
		t.Fatalf("reapply replaced resource or sequence: %+v %v", roster, err)
	}
	row, found, err := repo.Get(ctx, res.ID, "", "", "1")
	if err != nil || !found || string(row.Data) != string(created.Data) || !strings.Contains(string(row.Data), "9007199254740993") {
		t.Fatalf("draft data changed: %+v %v %v", row, found, err)
	}
	review, err = r.RequestReview(ctx, detail.Design.ID, detail.Design.Version, "ready again", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(ctx, detail.Design.ID, review.ID, detail.Design.Version, "ui"); err != nil {
		t.Fatal(err)
	}
	row, found, err = repo.Get(ctx, published[0].ID, "", "", "1")
	if err != nil || !found || string(row.Data) != string(publishedRow.Data) {
		t.Fatalf("release changed existing published data: %+v %v %v", row, found, err)
	}
}

func TestManagedEntityProjectionIncludesAncestorsWithoutPopulation(t *testing.T) {
	r, db := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Nested", Document: managedEntityDocument(t, "/orders/{}/items", responserules.ExecutionExtension), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	repo := resources.NewRepo(db, r.specs, 1<<20, 1<<20, 1000)
	roster, err := repo.ForWorkspace(t.Context(), detail.Design.DraftWorkspaceID)
	if err != nil || len(roster) != 2 {
		t.Fatalf("ancestor projection=%+v err=%v", roster, err)
	}
	for _, res := range roster {
		if res.Seq != 0 || res.SeedCount != 0 {
			t.Fatalf("projection generated rows: %+v", res)
		}
		if res.RouteFamily == "/orders/{}/items" && len(res.ScopeParams) != 1 {
			t.Fatalf("nested scope metadata=%+v", res)
		}
	}
}

func TestManagedEntityProjectionIdentityChangeRefusesSaveAtomically(t *testing.T) {
	r, db := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Entities", Document: managedEntityDocument(t, "/orders", responserules.ExecutionExtension), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(detail.Draft.Document, `"type": "integer"`, `"type": "string"`)
	if _, err = r.Save(t.Context(), detail.Design.ID, SaveInput{ExpectedVersion: detail.Design.Version, Document: changed, Source: "ui"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("incompatible identity accepted: %v", err)
	}
	after, err := r.Detail(t.Context(), detail.Design.ID)
	if err != nil || after.Design.Version != detail.Design.Version || after.Draft.ID != detail.Draft.ID {
		t.Fatalf("failed projection left revision: %+v %v", after, err)
	}
	var count int
	if err = db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resources WHERE workspace_id=?", detail.Design.DraftWorkspaceID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed projection changed resources: %d %v", count, err)
	}
}

func TestManagedEntityProjectionFailureRollsBackCreate(t *testing.T) {
	r, db := testRepo(t)
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER reject_rule_resource BEFORE INSERT ON resources BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(t.Context(), CreateInput{Name: "Entities", Document: managedEntityDocument(t, "/orders", responserules.ExecutionExtension), Source: "ui"}); err == nil {
		t.Fatal("resource projection failure allowed create")
	}
	for _, table := range []string{"specs", "api_designs", "workspaces", "resources", "resource_decisions"} {
		var count int
		if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived failed create: count=%d err=%v", table, count, err)
		}
	}
}

func TestManagedEntityProjectionFailureKeepsReviewPending(t *testing.T) {
	r, db := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Entities", Document: managedEntityDocument(t, "/orders", responserules.ExecutionExtension), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.RequestReview(t.Context(), detail.Design.ID, detail.Design.Version, "ready", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `CREATE TRIGGER reject_published_rule_resource BEFORE INSERT ON resources BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Publish(t.Context(), detail.Design.ID, review.ID, detail.Design.Version, "ui"); err == nil {
		t.Fatal("resource projection failure allowed publication")
	}
	after, err := r.Detail(t.Context(), detail.Design.ID)
	if err != nil || after.Published != nil || len(after.Releases) != 0 || after.Reviews[0].Status != "pending" {
		t.Fatalf("failed publication changed review: %+v %v", after, err)
	}
	var specID *int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT spec_id FROM workspaces WHERE id=?", detail.Design.PublishedWorkspaceID).Scan(&specID); err != nil || specID != nil {
		t.Fatalf("failed publication bound spec: %v %v", specID, err)
	}
}

func TestManagedEntityProjectionCompatibleWrapperKeepsRows(t *testing.T) {
	r, db := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Entities", Document: managedEntityDocument(t, "/orders", responserules.ExecutionExtension), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	repo := resources.NewRepo(db, r.specs, 1<<20, 1<<20, 1000)
	roster, err := repo.ForWorkspace(t.Context(), detail.Design.DraftWorkspaceID)
	if err != nil || len(roster) != 1 {
		t.Fatalf("initial roster=%+v err=%v", roster, err)
	}
	row, err := repo.Create(t.Context(), roster[0].ID, "", "", "id", "integer", map[string]any{"name": "existing"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := decodeDocument(detail.Draft.Document)
	if err != nil {
		t.Fatal(err)
	}
	paths := root["paths"].(map[string]any)
	get := paths["/orders"].(map[string]any)["get"].(map[string]any)
	response := get["responses"].(map[string]any)["200"].(map[string]any)
	media := response["content"].(map[string]any)["application/json"].(map[string]any)
	media["schema"] = map[string]any{"type": "object", "properties": map[string]any{"results": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Order"}}}}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Save(t.Context(), detail.Design.ID, SaveInput{ExpectedVersion: detail.Design.Version, Document: string(raw), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.ForWorkspace(t.Context(), detail.Design.DraftWorkspaceID)
	if err != nil || len(updated) != 1 || updated[0].ID != roster[0].ID || updated[0].Seq != 1 || updated[0].Wrapper.ArrayKey == nil || *updated[0].Wrapper.ArrayKey != "results" {
		t.Fatalf("compatible metadata update=%+v err=%v", updated, err)
	}
	stored, found, err := repo.Get(t.Context(), updated[0].ID, "", "", "1")
	if err != nil || !found || string(stored.Data) != string(row.Data) {
		t.Fatalf("compatible edit changed data: %+v %v %v", stored, found, err)
	}
}

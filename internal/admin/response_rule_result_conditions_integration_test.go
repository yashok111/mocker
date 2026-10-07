package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
)

const resultConditionLifecycleDocument = `{
  "openapi":"3.0.3","info":{"title":"Stored result conditions","version":"1"},
  "paths":{
    "/widgets":{"get":{"responses":{"200":{"description":"List","content":{"application/json":{"schema":{"type":"array","items":{"$ref":"#/components/schemas/Widget"}}}}}}}},
    "/widgets/{id}":{"get":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}}],"responses":{"200":{"description":"Widget","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Widget"}}}}}}}
  },
  "components":{"schemas":{"Widget":{"type":"object","properties":{"id":{"type":"integer"},"status":{"type":"string"}}}}},
  "x-mocker-response-rules":{"formatVersion":1,"rules":[{
    "id":"stored","name":"Stored status","binding":{"method":"GET","path":"/widgets/{id}"},
    "nodes":[
      {"id":"start","type":"start","name":"Request","x":0,"y":0},
      {"id":"read","type":"entity_read","name":"Read","x":200,"y":0,"entity":{"family":"/widgets","operation":"get","key":{"source":"path","name":"id"}}},
      {"id":"check","type":"condition","name":"Paid","x":400,"y":0,"resultCondition":{"source":{"source":"result","nodeId":"read","pointer":"/status"},"op":"equals","valueJSON":"\"paid\""}},
      {"id":"yes","type":"response","name":"Allowed","x":600,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[],"bodyJSON":"{\"allowed\":true}"}},
      {"id":"no","type":"response","name":"Denied","x":600,"y":200,"response":{"status":409,"mediaType":"application/json","headers":[],"bodyJSON":"{\"allowed\":false}"}},
      {"id":"missing","type":"response","name":"Missing","x":400,"y":400,"response":{"status":404,"mediaType":"application/json","headers":[],"bodyJSON":"{\"missing\":true}"}}
    ],
    "edges":[
      {"id":"begin","from":"start","port":"next","to":"read"},
      {"id":"found","from":"read","port":"found","to":"check"},
      {"id":"absent","from":"read","port":"missing","to":"missing"},
      {"id":"match","from":"check","port":"true","to":"yes"},
      {"id":"mismatch","from":"check","port":"false","to":"no"}
    ]
  }]}
}`

func resultConditionWorkspaceRequest(t *testing.T, s *Server, plane *mockplane.Plane, workspaceID int64) *httptest.ResponseRecorder {
	t.Helper()
	workspace, err := s.ws.ByID(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), "GET", "http://"+workspace.Slug+".mock.local/widgets/1?status=draft", nil)
	request.Header.Set("Accept", "application/json")
	reply := httptest.NewRecorder()
	plane.ServeWorkspace(reply, request, workspace)
	return reply
}

func resultConditionWorkspaceResource(ctx context.Context, t *testing.T, s *Server, workspaceID int64) *resources.Resource {
	t.Helper()
	rows, err := s.resourcesRepo.ForWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.RouteFamily == "/widgets" {
			return row
		}
	}
	t.Fatalf("no widgets resource in workspace %d", workspaceID)
	return nil
}

func TestResultConditionsManagedLifecycle(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	detail, err := s.designsRepo.Create(ctx, apidesign.CreateInput{
		Name: "Result conditions", Document: resultConditionLifecycleDocument, Source: "ui",
	})
	if err != nil {
		t.Fatal(err)
	}
	plane := executionTestPlane(s)
	plane.SetResources(s.resourcesRepo)
	plane.SetEntities(s.resourcesRepo)
	draftID, publishedID := detail.Design.DraftWorkspaceID, detail.Design.PublishedWorkspaceID
	applied, err := s.designsRepo.EditResponseRuleExecution(ctx, detail.Design.ID, detail.Design.Version, "ui", "stored", true)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus := func(workspaceID int64, want int) {
		t.Helper()
		reply := resultConditionWorkspaceRequest(t, s, plane, workspaceID)
		if reply.Code != want {
			t.Fatalf("workspace %d: %d %s; want %d", workspaceID, reply.Code, reply.Body, want)
		}
	}
	seed := func(workspaceID int64, status string) {
		t.Helper()
		resource := resultConditionWorkspaceResource(ctx, t, s, workspaceID)
		_, _, err := s.resourcesRepo.Set(ctx, resource.ID, "", "", "1", resource.IDField, resource.Wrapper.IDType, map[string]any{"status": status})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertStatus(draftID, 404)
	seed(draftID, "paid")
	assertStatus(draftID, 200)
	assertStatus(publishedID, 404)
	review, err := s.designsRepo.RequestReview(ctx, detail.Design.ID, applied.Design.Version, "Result conditions", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.designsRepo.Publish(ctx, detail.Design.ID, review.ID, applied.Design.Version, "ui"); err != nil {
		t.Fatal(err)
	}
	// Publication copies configuration, while each workspace retains its own rows.
	assertStatus(publishedID, 404)
	seed(publishedID, "draft")
	assertStatus(publishedID, 409)
	assertStatus(draftID, 200)
	list, err := s.designsRepo.ResponseRules(ctx, detail.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	rule := list.Rules[0]
	for i := range rule.Nodes {
		if rule.Nodes[i].ID == "check" {
			rule.Nodes[i].ResultCondition.ValueJSON = new(`"draft"`)
		}
	}
	edit, err := s.designsRepo.EditResponseRule(ctx, detail.Design.ID, list.Version, "ui", "stored", "save", &rule, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(draftID, 200)
	assertStatus(publishedID, 409)
	if _, err := s.designsRepo.EditResponseRuleExecution(ctx, detail.Design.ID, list.Version, "ui", "stored", true); !errors.Is(err, apidesign.ErrConflict) {
		t.Fatalf("stale apply bypassed version fence: %v", err)
	}
	if _, err := s.designsRepo.EditResponseRuleExecution(ctx, detail.Design.ID, edit.Design.Version, "ui", "stored", true); err != nil {
		t.Fatal(err)
	}
	assertStatus(draftID, 409)
	assertStatus(publishedID, 409)
	seed(draftID, "draft")
	assertStatus(draftID, 200)
	assertStatus(publishedID, 409)
	// The authored predicate survives the read route with its raw JSON text.
	status, body, err := s.CallAsMCP(ctx, loopbackTestSrc(), "GET", fmt.Sprintf("/api/designs/%d/response-rules/stored", detail.Design.ID), nil)
	if err != nil || status != 200 {
		t.Fatalf("read saved result condition: %d %s %v", status, body, err)
	}
	var stored struct {
		Rule responserules.Rule `json:"rule"`
	}
	if err := jsonx.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}
	if got := stored.Rule.Nodes[2].ResultCondition; got == nil || got.ValueJSON == nil || *got.ValueJSON != `"draft"` {
		t.Fatalf("saved condition lost: %#v", got)
	}
}

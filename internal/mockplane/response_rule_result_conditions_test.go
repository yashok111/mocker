package mockplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/responserules"
)

func liveResultConditionRule(t *testing.T, op, pointer, expected string) responserules.Rule {
	t.Helper()
	condition := map[string]any{
		"source": map[string]any{"source": "result", "nodeId": "entity", "pointer": pointer},
		"op":     op,
	}
	if op == "equals" || op == "not_equals" {
		condition["valueJSON"] = expected
	}
	raw, err := json.Marshal(map[string]any{
		"id": "check", "type": "condition", "name": "Stored value", "x": 0, "y": 0,
		"resultCondition": condition,
	})
	if err != nil {
		t.Fatal(err)
	}
	var node responserules.Node
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("decode result condition: %v", err)
	}
	rule := liveEntityRule("entity_read", "GET", "/widgets/{id}")
	rule.Nodes = append(rule.Nodes, node, responserules.Node{
		ID: "denied", Type: "response", Name: "Denied",
		Response: &responserules.Response{
			Status: 409, MediaType: "application/json", Headers: []responserules.Field{},
			BodyJSON: new(`{"allowed":false}`),
		},
	})
	rule.Edges[1].To = "check"
	rule.Edges = append(rule.Edges,
		responserules.Edge{ID: "yes", From: "check", Port: "true", To: "response"},
		responserules.Edge{ID: "no", From: "check", Port: "false", To: "denied"},
	)
	return rule
}

func TestResultConditionsLiveHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, op, pointer, expected, row string
		status                           int
	}{
		{"paid", "equals", "/status", `"paid"`, `{"status":"paid"}`, 200},
		{"unpaid", "equals", "/status", `"paid"`, `{"status":"draft"}`, 409},
		{"not equal", "not_equals", "/status", `"paid"`, `{"status":"draft"}`, 200},
		{"large exact", "equals", "/n", "9007199254740993", `{"n":9007199254740993}`, 200},
		{"large unequal", "equals", "/n", "9007199254740992", `{"n":9007199254740993}`, 409},
		{"numeric spelling", "equals", "/n", "1e0", `{"n":1.0}`, 200},
		{"close fractions", "equals", "/n", "0.1", `{"n":0.10000000000000001}`, 409},
		{"huge exponent", "equals", "/n", "10e999999999999999999999999999999", `{"n":1e1000000000000000000000000000000}`, 200},
		{"negative zero", "equals", "/n", "0", `{"n":-0.0e999}`, 200},
		{"null exists", "exists", "/note", "", `{"note":null}`, 200},
		{"absent exists", "exists", "/note", "", `{}`, 409},
		{"absent not exists", "not_exists", "/note", "", `{}`, 200},
		{"null not exists", "not_exists", "/note", "", `{"note":null}`, 409},
		{"null equal", "equals", "/note", "null", `{"note":null}`, 200},
		{"null versus scalar", "not_equals", "/note", `"paid"`, `{"note":null}`, 200},
		{"escaped pointer", "equals", "/a~1b/~0v", "true", `{"a/b":{"~v":true}}`, 200},
		{"array pointer", "equals", "/tags/0", `"ready"`, `{"tags":["ready"]}`, 200},
		{"container exists", "exists", "/tags", "", `{"tags":[]}`, 200},
		{"missing comparison", "equals", "/note", "null", `{}`, 500},
		{"type mismatch", "equals", "/n", `"1"`, `{"n":1}`, 500},
		{"container comparison", "equals", "/tags", "null", `{"tags":[]}`, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := liveResultConditionRule(t, tc.op, tc.pointer, tc.expected)
			p, repo, resource, _ := liveEntityPlane(t, []responserules.Rule{rule}, domain.Settings{Seed: 1, ListSize: 1})
			var data map[string]any
			decoder := json.NewDecoder(strings.NewReader(tc.row))
			decoder.UseNumber()
			if err := decoder.Decode(&data); err != nil {
				t.Fatal(err)
			}
			entity, err := repo.Create(t.Context(), resource.ID, "", "", resource.IDField, resource.Wrapper.IDType, data)
			if err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/widgets/%s?status=unpaid&n=wrong", entity.EntityKey)
			reply := entityRequest(p, t.Context(), "GET", path, `{"status":"unpaid","n":"wrong"}`, "application/json")
			if reply.Code != tc.status {
				t.Fatalf("got %d %s; want %d", reply.Code, reply.Body, tc.status)
			}
			if tc.status == 500 && !strings.Contains(reply.Body.String(), "response_rule_failed") {
				t.Fatalf("predicate error silently fell back: %s", reply.Body)
			}
			missing := entityRequest(p, t.Context(), "GET", "/widgets/999", "", "")
			if missing.Code != 404 || missing.Body.String() != `{"missing":true}` {
				t.Fatalf("missing row = %d %s", missing.Code, missing.Body)
			}
			stored, found, err := repo.Get(t.Context(), resource.ID, "", "", entity.EntityKey)
			if err != nil || !found || !bytes.Equal(stored.Data, entity.Data) {
				t.Fatalf("condition changed stored row: found=%v err=%v data=%s", found, err, stored.Data)
			}
		})
	}
}

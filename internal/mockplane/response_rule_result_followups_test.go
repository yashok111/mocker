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

func liveFollowupRule(t *testing.T, predicate string) responserules.Rule {
	t.Helper()
	rule := liveResultConditionRule(t, "exists", "/n", "")
	var condition responserules.ResultCondition
	if err := json.Unmarshal([]byte(predicate), &condition); err != nil {
		t.Fatal(err)
	}
	for i, node := range rule.Nodes {
		if node.ID == "check" {
			rule.Nodes[i].ResultCondition = &condition
		}
	}
	// Both operands refer to distinct prior captures, using the same real SQLite
	// dataset. The found branch of each read must dominate the condition.
	for _, node := range rule.Nodes {
		if node.ID == "entity" {
			node.ID = "other"
			rule.Nodes = append(rule.Nodes, node)
			break
		}
	}
	for i, edge := range rule.Edges {
		if edge.From == "entity" && edge.Port == "found" {
			rule.Edges[i].To = "other"
		}
	}
	rule.Edges = append(rule.Edges,
		responserules.Edge{ID: "other-found", From: "other", Port: "found", To: "check"},
		responserules.Edge{ID: "other-missing", From: "other", Port: "missing", To: "missing"},
	)
	return rule
}

func TestResultConditionFollowupsLiveHTTP(t *testing.T) {
	t.Parallel()
	const left = `{"source":"result","nodeId":"entity","pointer":"/n"}`
	const right = `{"source":"result","nodeId":"other","pointer":"/limit"}`
	presence := `{"source":` + left + `,"op":"exists"}`
	numeric := `{"source":` + left + `,"op":"greater_than","valueFrom":` + right + `}`
	for _, tc := range []struct {
		name, predicate, row string
		status               int
	}{
		{"large numeric literal", `{"source":` + left + `,"op":"greater_than","valueJSON":"9007199254740992"}`, `{"n":9007199254740993}`, 200},
		{"fraction ordering", `{"source":` + left + `,"op":"less_than","valueJSON":"0.1000000000000000002"}`, `{"n":0.1000000000000000001}`, 200},
		{"equivalent greater or equal", `{"source":` + left + `,"op":"greater_or_equal","valueJSON":"1.000e0"}`, `{"n":1}`, 200},
		{"negative less or equal", `{"source":` + left + `,"op":"less_or_equal","valueJSON":"-1.20"}`, `{"n":-1.2}`, 200},
		{"prior result right", numeric, `{"n":9007199254740993,"limit":9007199254740992}`, 200},
		{"prior result denied", numeric, `{"n":10,"limit":11}`, 409},
		{"AND guard missing", `{"all":[` + presence + `,` + numeric + `]}`, `{}`, 409},
		{"AND true", `{"all":[` + presence + `,` + numeric + `]}`, `{"n":10,"limit":9}`, 200},
		{"nested OR", `{"all":[` + presence + `,{"any":[{"source":` + left + `,"op":"equals","valueJSON":"10"},` + numeric + `]}]}`, `{"n":10}`, 200},
		{"OR skipped RHS error", `{"any":[{"source":` + left + `,"op":"not_exists"},` + numeric + `]}`, `{}`, 200},
		{"evaluated RHS missing", numeric, `{"n":10}`, 500},
		{"evaluated RHS wrong kind", numeric, `{"n":10,"limit":"9"}`, 500},
		{"evaluated RHS null", numeric, `{"n":10,"limit":null}`, 500},
		{"ordering actual null", numeric, `{"n":null,"limit":9}`, 500},
		{"evaluated group error", `{"any":[` + presence + `,` + numeric + `]}`, `{}`, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rule := liveFollowupRule(t, tc.predicate)
			plane, repo, resource, _ := liveEntityPlane(t, []responserules.Rule{rule}, domain.Settings{Seed: 1, ListSize: 1})
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
			response := entityRequest(plane, t.Context(), "GET", fmt.Sprintf("/widgets/%s?n=incorrect&limit=incorrect", entity.EntityKey), `{"n":"incorrect"}`, "application/json")
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s; want %d", response.Code, response.Body, tc.status)
			}
			if tc.status == 500 && !strings.Contains(response.Body.String(), "response_rule_failed") {
				t.Fatalf("predicate error fell back: %s", response.Body)
			}
			missing := entityRequest(plane, t.Context(), "GET", "/widgets/999", "", "")
			if missing.Code != 404 {
				t.Fatalf("missing entity=%d %s", missing.Code, missing.Body)
			}
			stored, found, err := repo.Get(t.Context(), resource.ID, "", "", entity.EntityKey)
			if err != nil || !found || !bytes.Equal(stored.Data, entity.Data) {
				t.Fatalf("predicate mutated entity: found=%v err=%v", found, err)
			}
		})
	}
}

package admin

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestResponseRuleSimulationSelectsExampleFromExactSource(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, func(cfg *config.Config) { cfg.CheckpointDebounce = 300 })
	example := `{"id":"one","name":"Example","request":{"query":[],"headers":[],"bodyJSON":" {\"n\":9007199254740993} "}}`
	document := strings.Replace(responseRuleTestDocument, `"edges":[`, `"examples":[`+example+`],"edges":[`, 1)
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Examples", Document: document, Source: "ui"})
	if err != nil {
		t.Fatalf("saved example rejected: %v", err)
	}
	base := fmt.Sprintf("/api/designs/%d/response-rules/r/simulate", d.Design.ID)
	call := func(body string, want int) []byte {
		t.Helper()
		status, out, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", base, []byte(body))
		if err != nil || status != want {
			t.Fatalf("simulate: %d %s %v", status, out, err)
		}
		return out
	}
	before := responseRuleRows(t, s)
	selected := call(`{"exampleId":"one"}`, 200)
	explicit := call(`{"request":{"query":[],"headers":[],"bodyJSON":" {\"n\":9007199254740993} "}}`, 200)
	if string(selected) != string(explicit) {
		t.Fatalf("selected and explicit fixture differ:\n%s\n%s", selected, explicit)
	}
	var saved struct {
		InputHash string                       `json:"inputHash"`
		Source    apidesign.ResponseRuleSource `json:"source"`
	}
	if err := jsonx.Unmarshal(selected, &saved); err != nil || saved.Source.Kind != "saved" || saved.Source.RevisionID == nil || *saved.Source.RevisionID != d.Draft.ID {
		t.Fatalf("saved example source wrong: %+v %v", saved, err)
	}
	proposal := strings.Replace(document, `9007199254740993`, `1e10000`, 1)
	payload, _ := jsonx.Marshal(map[string]any{"document": proposal, "exampleId": "one"})
	var proposed struct {
		InputHash string                       `json:"inputHash"`
		Source    apidesign.ResponseRuleSource `json:"source"`
	}
	if err := jsonx.Unmarshal(call(string(payload), 200), &proposed); err != nil || proposed.Source.Kind != "proposal" || proposed.Source.RevisionID != nil || proposed.InputHash == saved.InputHash {
		t.Fatalf("proposal case resolved against saved source: %+v %v", proposed, err)
	}
	call(`{"exampleId":"missing"}`, 404)
	for _, body := range []string{
		`{}`, `{"exampleId":null}`, `{"exampleId":""}`, `{"exampleId":123}`, `{"exampleId":"bad/id"}`,
		`{"exampleId":"one","request":{"query":[],"headers":[]}}`,
	} {
		call(body, 400)
	}
	if !reflect.DeepEqual(before, responseRuleRows(t, s)) {
		t.Fatal("example simulation changed runtime or saved rows")
	}
}

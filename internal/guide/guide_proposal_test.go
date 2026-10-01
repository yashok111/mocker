package guide

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
)

func TestDatabaseProposalWorkflowNegotiation(t *testing.T) {
	workflows := BackendWorkflows()
	i := slices.IndexFunc(workflows, func(w Workflow) bool { return w.WorkflowID == "mocker-backend-database" })
	if i < 0 || workflows[i].WorkflowVersion != "3" {
		t.Fatal("proposal writes require a separately selectable database workflow3")
	}
	w := workflows[i]
	for _, capability := range []string{"backend-db-proposals", "backend-db-typed-edits"} {
		if !slices.Contains(w.RequiredCapabilities, capability) {
			t.Fatalf("workflow can write without %s", capability)
		}
	}
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var views []string
	if err := json.Unmarshal(fields["requiredViewSchemaVersions"], &views); err != nil || !slices.Equal(views, []string{"proposal-relational-v1"}) {
		t.Fatalf("view negotiation missing: %s %v", raw, err)
	}
	// Model/recovery/examples keep their import4 owner in this exact set.
	for _, topic := range []string{"backend-model", "backend-recovery", "backend-examples"} {
		owner, ok := WorkflowForTopic(topic)
		if !ok || owner.WorkflowID != "mocker-backend-import" || owner.WorkflowVersion != "4" || owner.GuideSetID != w.GuideSetID {
			t.Fatalf("shared owner changed for %s", topic)
		}
	}
}

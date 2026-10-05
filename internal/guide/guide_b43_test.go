package guide

import (
	"slices"
	"strings"
	"testing"
)

func TestB43GuideHandoffOwners(t *testing.T) {
	for _, topic := range []string{"backend-change-handoff", "backend-endpoint-review"} {
		owner, ok := WorkflowForTopic(topic)
		if !ok || owner.WorkflowID != "mocker-backend-change" || owner.WorkflowVersion != "4" || !slices.Contains(Topics(), topic) {
			t.Errorf("new topic %s owner: %+v", topic, owner)
			continue
		}
		body, ok := Topic(topic)
		if !ok || !strings.Contains(body, "start_backend_analysis") || !strings.Contains(body, "unverified") {
			t.Errorf("missing executable workflow contract: %s", topic)
		}
		for _, capability := range []string{"backend-change-package", "backend-conformance", "backend-endpoint-review", "backend-change-implemented", "backend-change-archive", "backend-change-unarchive"} {
			if !slices.Contains(owner.RequiredCapabilities, capability) {
				t.Errorf("%s omits %s", topic, capability)
			}
		}
	}
	inspect, _ := WorkflowForTopic("backend-inspect")
	recovery, _ := WorkflowForTopic("backend-recovery")
	if inspect.WorkflowVersion != "9" || recovery.WorkflowID != "mocker-backend-import" || recovery.WorkflowVersion != "8" {
		t.Fatal("incorrect inspection/recovery owners", inspect, recovery)
	}
}

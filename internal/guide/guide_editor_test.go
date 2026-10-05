package guide

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"testing"
)

func TestEditorGuideHasQualifiedPinnedOwner(t *testing.T) {
	owner, ok := WorkflowForTopic("backend-editor-projections")
	if !ok || owner.WorkflowID != "mocker-backend-inspect" || owner.WorkflowVersion != "9" {
		t.Fatalf("editor guide lacks inspect9 owner: %+v", owner)
	}
	if !slices.Contains(owner.RequiredCapabilities, "backend-editor-projections") || !slices.Contains(owner.RequiredViewSchemaVersions, "backend-editor-artifacts-v1") {
		t.Fatalf("editor guide can qualify without its complete contract: %+v", owner)
	}
	body, ok := Topic("backend-editor-projections")
	i := slices.IndexFunc(owner.Topics, func(item TopicMetadata) bool { return item.Topic == "backend-editor-projections" })
	if !ok || i < 0 || owner.Topics[i].ContentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) {
		t.Fatal("editor guide cannot be verified at its advertised immutable set")
	}
	changed, _ := WorkflowForTopic("backend-editor-projections")
	changed.RequiredViewSchemaVersions[0] = "mutated"
	changed.Topics[i].ContentHash = "mutated"
	after, _ := WorkflowForTopic("backend-editor-projections")
	if !slices.Equal(owner.RequiredViewSchemaVersions, after.RequiredViewSchemaVersions) || !slices.Equal(owner.Topics, after.Topics) {
		t.Fatal("caller mutation changed editor negotiation")
	}
}

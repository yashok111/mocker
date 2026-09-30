package guide

import (
	"encoding/json/v2"
	"slices"
)

// TopicMetadata binds one topic to its served markdown content.
type TopicMetadata struct {
	Topic       string `json:"topic"`
	ContentHash string `json:"contentHash"`
}

// Workflow advertises the exact guide and capability contract for an agent.
type Workflow struct {
	WorkflowID                  string          `json:"workflowId"`
	WorkflowVersion             string          `json:"workflowVersion"`
	GuideSetID                  string          `json:"guideSetId"`
	ManifestHash                string          `json:"manifestHash"`
	Entrypoint                  string          `json:"entrypoint"`
	RequiredModelSchemaVersions []string        `json:"requiredModelSchemaVersions"`
	RequiredCapabilities        []string        `json:"requiredCapabilities"`
	Topics                      []TopicMetadata `json:"topics"`
}

type guideManifest struct {
	GuideSetID   string     `json:"guideSetId"`
	ManifestHash string     `json:"manifestHash"`
	Workflows    []Workflow `json:"workflows"`
}

var currentManifest = loadManifest()

func loadManifest() guideManifest {
	var manifest guideManifest
	if err := json.Unmarshal([]byte(mustRead("manifest.json")), &manifest); err != nil {
		panic("guide: invalid embedded manifest: " + err.Error())
	}
	return manifest
}

// CurrentGuideSetID identifies the immutable guides embedded in this binary.
func CurrentGuideSetID() string { return currentManifest.GuideSetID }

func cloneWorkflow(w Workflow) Workflow {
	w.RequiredModelSchemaVersions = slices.Clone(w.RequiredModelSchemaVersions)
	w.RequiredCapabilities = slices.Clone(w.RequiredCapabilities)
	w.Topics = slices.Clone(w.Topics)
	return w
}

// BackendWorkflows returns defensive copies of implemented backend workflows.
func BackendWorkflows() []Workflow {
	var result []Workflow
	for _, w := range currentManifest.Workflows {
		if w.WorkflowID == "mocker-backend-project" || w.WorkflowID == "mocker-backend-import" {
			result = append(result, cloneWorkflow(w))
		}
	}
	return result
}

// WorkflowForTopic returns a defensive copy of a topic's workflow identity.
func WorkflowForTopic(topic string) (Workflow, bool) {
	for _, w := range currentManifest.Workflows {
		for _, entry := range w.Topics {
			if entry.Topic == topic {
				return cloneWorkflow(w), true
			}
		}
	}
	return Workflow{}, false
}

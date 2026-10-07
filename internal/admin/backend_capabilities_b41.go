package admin

import "github.com/yashok111/mocker/internal/backendmodel"

type backendReadSupport struct {
	Read                   string   `json:"read"`
	Targets                []string `json:"targets"`
	SourceSchemaVersions   []string `json:"sourceSchemaVersions"`
	FullBaseSchemaVersions []string `json:"fullBaseSchemaVersions"`
}

func backendCapabilityFeatures() []string {
	// One list for the endpoint and every project resource (review
	// 2026-10-06, F90); see backendmodel.Features.
	return backendmodel.Features()
}

func backendChangeProposalCommands() []string {
	return []string{
		"alter_column", "alter_constraint", "alter_index", "create_node", "edit_branch",
		"edit_flow_step", "map_identity", "remove_artifact_pin", "remove_edge", "remove_node",
		"rename", "set_artifact_pin", "set_criteria", "set_field_mapping", "update_node", "upsert_edge",
	}
}

func backendReadTargetSupport() []backendReadSupport {
	allSources := []string{"1", "2", "3", "4", "5", "6"}
	fullBases := []string{"5", "6"}
	allTargets := []string{"revisionId", "proposal", "changeProposal", "importCandidate"}
	sourceAndFull := []string{"revisionId", "changeProposal"}
	sourceLegacyFull := []string{"revisionId", "proposal", "changeProposal"}
	return []backendReadSupport{
		{Read: "namespaced_artifacts", Targets: sourceLegacyFull, SourceSchemaVersions: []string{"5", "6"}, FullBaseSchemaVersions: fullBases},
		{
			Read: "graph", Targets: allTargets,
			SourceSchemaVersions: allSources, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "node", Targets: allTargets,
			SourceSchemaVersions: allSources, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "evidence", Targets: allTargets,
			SourceSchemaVersions: allSources, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "coverage", Targets: allTargets,
			SourceSchemaVersions: allSources, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "assertions", Targets: []string{"revisionId", "changeProposal", "importCandidate"},
			SourceSchemaVersions: []string{"6"}, FullBaseSchemaVersions: []string{"6"},
		},
		{
			Read: "database", Targets: sourceLegacyFull,
			SourceSchemaVersions: []string{"2", "3", "4", "5", "6"}, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "flow", Targets: sourceAndFull,
			SourceSchemaVersions: []string{"3", "4", "5", "6"}, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "lineage", Targets: sourceAndFull,
			SourceSchemaVersions: []string{"4", "5", "6"}, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "events", Targets: sourceAndFull,
			SourceSchemaVersions: []string{"5", "6"}, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "api_artifacts", Targets: sourceAndFull,
			SourceSchemaVersions: allSources, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "editor_artifacts", Targets: sourceAndFull,
			SourceSchemaVersions: allSources, FullBaseSchemaVersions: fullBases,
		},
		{
			Read: "saved_view_v2", Targets: sourceLegacyFull,
			SourceSchemaVersions: []string{"2", "3", "4", "5", "6"}, FullBaseSchemaVersions: fullBases,
		},
	}
}

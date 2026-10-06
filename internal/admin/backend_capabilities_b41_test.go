package admin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type capabilityReadSupport struct {
	Read    string   `json:"read"`
	Targets []string `json:"targets"`
	Sources []string `json:"sourceSchemaVersions"`
	Full    []string `json:"fullBaseSchemaVersions"`
}

type capabilityB41Response struct {
	Models            []string                `json:"modelSchemaVersions"`
	Features          []string                `json:"features"`
	Profiles          []string                `json:"providerProfiles"`
	SourceScopes      []string                `json:"sourceScopes"`
	Policies          []string                `json:"syncPolicies"`
	Commands          []string                `json:"changeProposalCommands"`
	LegacyCommands    []map[string]string     `json:"proposalCommands"`
	ImportModes       []string                `json:"importModes"`
	ImportCommands    []string                `json:"importCommands"`
	NodeKinds         []string                `json:"supportedNodeKinds"`
	Views             []string                `json:"viewSchemaVersions"`
	ProposalDocuments []string                `json:"proposalDocumentVersions"`
	Targets           []capabilityReadSupport `json:"readTargetSupport"`
	Limits            map[string]int64        `json:"limits"`
}

func readB41Capabilities(t *testing.T, server *Server) capabilityB41Response {
	t.Helper()
	raw := b41Call(t, server, "GET", "/api/backend-projects/capabilities", nil, 200, nil)
	var caps capabilityB41Response
	if err := json.Unmarshal(raw, &caps); err != nil {
		t.Fatal(err)
	}
	var lossless map[string]jsontext.Value
	if err := json.Unmarshal(raw, &lossless); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(lossless)
	if err != nil {
		t.Fatal(err)
	}
	validateBackendImportResponse(t, "GET", "/api/backend-projects/capabilities", encoded)
	return caps
}

func TestBackendB41CapabilitiesDescribeExactSupportedScopes(t *testing.T) {
	server := loopbackTestServer(t, nil)
	caps := readB41Capabilities(t, server)
	if !slices.Equal(caps.Models, []string{"1", "2", "3", "4", "5", "6"}) {
		t.Fatalf("model inventory: %v", caps.Models)
	}
	if !slices.Contains(caps.Profiles, backendmodel.ComposedProfile) {
		t.Fatalf("missing composed provider profile: %v", caps.Profiles)
	}
	features := []string{
		"backend-annotations", "backend-source-sync", "backend-source-incremental-sync",
		"backend-source-assertions", "backend-import-candidate", "backend-change-proposals",
		"backend-change-typed-edits", "backend-representations", "backend-saved-views-v2",
		"backend-db-proposals", "backend-db-typed-edits", "backend-events-query",
	}
	for _, feature := range features {
		if !slices.Contains(caps.Features, feature) {
			t.Errorf("missing feature %s", feature)
		}
	}
	if !slices.Equal(caps.SourceScopes, []string{"add_repository", "reconcile", "add_provider", "migrate_provider"}) {
		t.Errorf("source scopes: %v", caps.SourceScopes)
	}
	if !slices.Equal(caps.Policies, []string{backendmodel.WholeSourcePolicy, backendmodel.IncrementalSourcePolicy}) {
		t.Errorf("sync policies: %v", caps.Policies)
	}
	wantCommands := []string{
		"alter_column", "alter_constraint", "alter_index", "create_node", "edit_branch",
		"edit_flow_step", "map_identity", "remove_artifact_pin", "remove_edge", "remove_node",
		"rename", "set_artifact_pin", "set_criteria", "set_field_mapping", "update_node", "upsert_edge",
	}
	if !slices.Equal(caps.Commands, wantCommands) {
		t.Errorf("full commands: %v", caps.Commands)
	}
	wantLegacy := []map[string]string{
		{"type": "alter_column", "property": "nullable"},
		{"type": "alter_constraint", "action": "create", "constraintKind": "foreign_key"},
		{"type": "alter_constraint", "action": "update", "constraintKind": "foreign_key"},
		{"type": "set_criteria"},
	}
	old, _ := json.Marshal(wantLegacy, json.Deterministic(true))
	actual, _ := json.Marshal(caps.LegacyCommands, json.Deterministic(true))
	if string(old) != string(actual) {
		t.Errorf("legacy proposalCommands changed meaning: %s", actual)
	}
	for _, mode := range []string{"initial", "reconcile", "composed"} {
		if !slices.Contains(caps.ImportModes, mode) {
			t.Errorf("missing mode %s", mode)
		}
	}
	for _, command := range []string{"claim_identity", "resolve_assertion"} {
		if !slices.Contains(caps.ImportCommands, command) {
			t.Errorf("missing import command %s", command)
		}
	}
	for _, kind := range []string{"domain_entity", "dto", "api_schema", "representation_field"} {
		if !slices.Contains(caps.NodeKinds, kind) {
			t.Errorf("missing representation kind %s", kind)
		}
	}
	for _, view := range []string{"proposal-graph-v1", "import-candidate-v1", "saved-view-v2"} {
		if !slices.Contains(caps.Views, view) {
			t.Errorf("missing view schema %s", view)
		}
	}
	if !slices.Equal(caps.ProposalDocuments, []string{"proposal-relational-v1", "proposal-graph-v1"}) {
		t.Errorf("proposal documents: %v", caps.ProposalDocuments)
	}
	checkB41CapabilityReads(t, caps.Targets)
	checkB41CapabilityLimits(t, caps.Limits, server.cfg.MaxBody)
}

func checkB41CapabilityReads(t *testing.T, rows []capabilityReadSupport) {
	t.Helper()
	want := map[string]capabilityReadSupport{}
	for _, read := range []string{"graph", "node", "evidence", "coverage"} {
		want[read] = capabilityReadSupport{Read: read,
			Targets: []string{"revisionId", "proposal", "changeProposal", "importCandidate"},
			Sources: []string{"1", "2", "3", "4", "5", "6"}, Full: []string{"5", "6"}}
	}
	for _, spec := range []struct {
		read    string
		sources []string
		legacy  bool
	}{
		{read: "database", sources: []string{"2", "3", "4", "5", "6"}, legacy: true},
		{read: "flow", sources: []string{"3", "4", "5", "6"}},
		{read: "lineage", sources: []string{"4", "5", "6"}},
		{read: "events", sources: []string{"5", "6"}},
		{read: "api_artifacts", sources: []string{"1", "2", "3", "4", "5", "6"}},
		{read: "editor_artifacts", sources: []string{"1", "2", "3", "4", "5", "6"}},
		{read: "namespaced_artifacts", sources: []string{"5", "6"}, legacy: true},
		{read: "saved_view_v2", sources: []string{"2", "3", "4", "5", "6"}, legacy: true},
	} {
		targets := []string{"revisionId", "changeProposal"}
		if spec.legacy {
			targets = []string{"revisionId", "proposal", "changeProposal"}
		}
		want[spec.read] = capabilityReadSupport{Read: spec.read, Targets: targets, Sources: spec.sources, Full: []string{"5", "6"}}
	}
	want["assertions"] = capabilityReadSupport{Read: "assertions",
		Targets: []string{"revisionId", "changeProposal", "importCandidate"}, Sources: []string{"6"}, Full: []string{"6"}}
	if len(rows) != len(want) {
		t.Fatalf("read groups: got %d, want %d", len(rows), len(want))
	}
	for _, row := range rows {
		expected, found := want[row.Read]
		if !found {
			t.Fatalf("unknown or duplicate group %q", row.Read)
		}
		correctTargets := slices.Equal(row.Targets, expected.Targets)
		correctSources := slices.Equal(row.Sources, expected.Sources)
		correctFull := slices.Equal(row.Full, expected.Full)
		if !correctTargets || !correctSources || !correctFull {
			t.Errorf("%s support: %+v; want %+v", row.Read, row, expected)
		}
		delete(want, row.Read)
	}
}

func checkB41CapabilityLimits(t *testing.T, limits map[string]int64, body int64) {
	t.Helper()
	want := map[string]int64{
		"maxBodyBytes":                  body,
		"maxCommands":                   backendmodel.MaxProjectCommands,
		"maxProjectCommandBytes":        min(body, int64(backendmodel.MaxProjectCommandBytes)),
		"maxAnnotationBodyBytes":        min(body, int64(backendmodel.MaxAnnotationBodyBytes)),
		"maxAnnotationIdentities":       backendmodel.MaxAnnotationIdentities,
		"maxAnnotationTextBytes":        backendmodel.MaxAnnotationTextBytes,
		"defaultAnnotationPageSize":     backendmodel.DefaultAnnotationPageSize,
		"maxAnnotationPageSize":         backendmodel.MaxAnnotationPageSize,
		"maxSourceRepositories":         backendmodel.MaxSourceRepositories,
		"maxSourceProviders":            backendmodel.MaxSourceProviders,
		"maxIncrementalSubjects":        backendmodel.MaxIncrementalSubjects,
		"maxChangeProposalCommands":     backendmodel.MaxChangeProposalCommands,
		"maxChangeProposalCommandBytes": min(body, int64(backendmodel.MaxChangeProposalCommandBytes)),
		"maxChangeProposalCriteria":     backendmodel.MaxChangeProposalCriteria,
		"maxChangeProposals":            backendmodel.MaxChangeProposals,
		"maxChangeProposalRevisions":    backendmodel.MaxChangeProposalRevisions,
		"maxChangeProposalBytes":        backendmodel.MaxChangeProposalBytes,
	}
	for name, expected := range want {
		if limits[name] != expected {
			t.Errorf("%s = %d, want %d", name, limits[name], expected)
		}
	}
}

func TestBackendB41CapabilityWriteCeilingsRespectServerBody(t *testing.T) {
	for _, limit := range []int64{1024, 9007199254740993} {
		server := loopbackTestServer(t, nil)
		server.cfg.MaxBody = limit
		caps := readB41Capabilities(t, server)
		checkB41CapabilityLimits(t, caps.Limits, server.cfg.MaxBody)
	}
}

func TestBackendB41CapabilityAnnotationHundredCommands(t *testing.T) {
	server := loopbackTestServer(t, nil)
	caps := readB41Capabilities(t, server)
	if caps.Limits["maxCommands"] != 100 {
		t.Fatalf("annotation batch falsely limited to %d", caps.Limits["maxCommands"])
	}
	project, target := annotationTransportSource(t, server)
	commands := make([]backendmodel.Command, 0, 100)
	for range 100 {
		commands = append(commands, backendmodel.Command{
			Type: "create_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: "Static review note",
		})
	}
	b41Call(t, server, "POST", "/api/backend-projects/"+project.ID+"/commands",
		backendmodel.CommandsInput{ExpectedVersion: project.Version, IdempotencyKey: "hundred-notes", Commands: commands}, 200, nil)
	var page backendmodel.AnnotationPage
	b41Call(t, server, "GET", "/api/backend-projects/"+project.ID+"/annotations?limit=100", nil, 200, &page)
	if len(page.Items) != 100 {
		t.Fatalf("accepted annotation count %d", len(page.Items))
	}
}

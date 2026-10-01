package mcp

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// Runs through the real SDK caller used by the relational source example.
// All identities come from its actual authored fixture import. No fixture DDL
// or runtime database verification is executed.
func proposalSDKFixtureExample(t *testing.T, dialect string, project backendmodel.Project, ids map[string]string, call func(string, map[string]any, any) []byte, restart func()) {
	t.Helper()
	create := map[string]any{"projectId": project.ID, "name": "Require users", "baseRevisionId": project.CurrentRevisionID, "repositoryId": project.Repositories[0].ID, "datastoreId": ids["database:orders"], "facetKey": "sql", "idempotencyKey": "proposal-create"}
	var detail backendmodel.ProposalDetail
	creationReceipt := call("create_backend_proposal", create, &detail)
	call("list_backend_proposals", map[string]any{"projectId": project.ID, "status": "draft"}, nil)
	columnID := ids["column:orders:legacy_note"]
	source := map[string]any{"projectId": project.ID, "revisionId": project.CurrentRevisionID, "nodeId": columnID}
	sourceBefore := call("get_backend_node", source, nil)
	raw, err := os.ReadFile(filepath.Join("../backendmodel/testdata/relational/proposals", dialect, "commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	replacements := []string{}
	for key, id := range ids {
		replacements = append(replacements, "@"+key+"@", id)
	}
	var commands []backendmodel.ProposalCommand
	if err := json.Unmarshal([]byte(strings.NewReplacer(replacements...).Replace(string(raw))), &commands); err != nil {
		t.Fatal(err)
	}
	commands[0].ColumnID = columnID
	commands[0].Reason = "Require legacy notes after backfill"
	var originalRequest map[string]any
	var originalReceipt []byte
	var firstDraft string
	for index, command := range commands {
		var preview backendmodel.ProposalPreview
		call("preview_backend_proposal_commands", map[string]any{"projectId": project.ID, "proposalId": detail.Proposal.ID, "expectedVersion": detail.Proposal.Version, "draftRevisionId": detail.Proposal.DraftRevisionID, "commands": []backendmodel.ProposalCommand{command}}, &preview)
		if preview.CandidateHash == nil || len(preview.Diagnostics) != 0 {
			t.Fatalf("proposal preview: %+v", preview)
		}
		request := map[string]any{"projectId": project.ID, "proposalId": detail.Proposal.ID, "expectedVersion": detail.Proposal.Version, "draftRevisionId": detail.Proposal.DraftRevisionID, "candidateHash": *preview.CandidateHash, "commands": []backendmodel.ProposalCommand{command}, "idempotencyKey": "save-" + command.CommandID}
		var applied backendmodel.ProposalApplyResult
		receipt := call("apply_backend_proposal_commands", request, &applied)
		if preview.CandidateGraphHash == nil || applied.CandidateGraphHash != *preview.CandidateGraphHash {
			t.Fatal("preview/apply graph hashes differ")
		}
		if index == 0 {
			originalRequest, originalReceipt, firstDraft = request, receipt, applied.Revision.ID
		}
		call("get_backend_proposal", map[string]any{"projectId": project.ID, "proposalId": detail.Proposal.ID}, &detail)
		pins := map[string]any{"proposalId": detail.Proposal.ID, "proposalRevisionId": applied.Revision.ID}
		var node backendmodel.BackendNodeRead
		call("get_backend_node", map[string]any{"projectId": project.ID, "proposal": pins, "nodeId": columnID}, &node)
		if node.ProposalProjection == nil || node.ProposalProjection.SourceRecord == nil || node.ProposalProjection.EffectiveFacet.PropertyOrigins["/nullable"].Kind != "intent" {
			t.Fatalf("lost source/intent origin: %+v", node)
		}
		var sourceFacets map[string]map[string]jsontext.Value
		if err := json.Unmarshal(node.ProposalProjection.SourceRecord.Attributes["facets"], &sourceFacets); err != nil {
			t.Fatal(err)
		}
		var sourceNullable, desiredNullable struct {
			Status string `json:"status"`
			Value  bool   `json:"value"`
		}
		if err := json.Unmarshal(sourceFacets["sql"]["nullable"], &sourceNullable); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(node.ProposalProjection.EffectiveFacet.Values["nullable"], &desiredNullable); err != nil {
			t.Fatal(err)
		}
		if !sourceNullable.Value || desiredNullable.Value {
			t.Fatal("source NULL and desired NOT NULL were not distinct")
		}
		call("get_backend_evidence", map[string]any{"projectId": project.ID, "proposal": pins, "subjectId": columnID}, nil)
		call("get_backend_coverage", map[string]any{"projectId": project.ID, "proposal": pins}, nil)
		var graph backendmodel.GraphPage
		call("query_backend_graph", map[string]any{"projectId": project.ID, "proposal": pins, "recordType": "nodes"}, &graph)
		var relationships backendmodel.DatabasePage
		call("query_backend_database", map[string]any{"projectId": project.ID, "proposal": pins, "datastoreId": detail.Proposal.DatastoreID, "facetKey": "sql", "recordType": "relationships"}, &relationships)
		for _, relationship := range relationships.RelationshipItems {
			if relationship.Status != "proposed" || relationship.RuntimeStatus != "unverified" {
				t.Fatal("proposal claimed runtime enforcement")
			}
		}
		if command.Type == "alter_constraint" {
			edgeID := applied.Changes[0].GeneratedIDs["edgeId"]
			found := false
			for _, relationship := range relationships.RelationshipItems {
				if relationship.EdgeID == edgeID {
					found = true
					if !slices.Equal(relationship.ColumnPairs, command.ColumnPairs) {
						t.Fatal("ordered FK pairs changed")
					}
				}
			}
			if !found {
				t.Fatal("designed FK absent from ER")
			}
		}
		if command.Type == "set_criteria" && !slices.ContainsFunc(applied.Criteria, func(c backendmodel.ProposalCriterion) bool {
			return c.Key == "rollback" && c.Origin == "authored" && c.Status == "unverified"
		}) {
			t.Fatal("authored criterion missing")
		}
	}
	if after := call("get_backend_node", source, nil); !bytes.Equal(sourceBefore, after) {
		t.Fatal("proposal mutated source node")
	}
	var unchanged backendmodel.Project
	call("get_backend_project", map[string]any{"projectId": project.ID}, &unchanged)
	if unchanged.Version != project.Version || unchanged.CurrentRevisionID != project.CurrentRevisionID {
		t.Fatal("proposal mutated project/source pointer")
	}
	restart()
	if replay := call("apply_backend_proposal_commands", originalRequest, nil); !bytes.Equal(originalReceipt, replay) {
		t.Fatal("restart/later-save replay changed original receipt")
	}
	if replay := call("create_backend_proposal", create, nil); !bytes.Equal(creationReceipt, replay) {
		t.Fatal("restart creation replay changed receipt")
	}
	var historical backendmodel.ProposalDetail
	call("get_backend_proposal", map[string]any{"projectId": project.ID, "proposalId": detail.Proposal.ID, "proposalRevisionId": firstDraft}, &historical)
	if historical.Revision.ID != firstDraft || historical.Proposal.Version != 4 {
		t.Fatal("historical pin or current CAS version lost")
	}
}

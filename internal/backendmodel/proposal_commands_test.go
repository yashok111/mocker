package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func proposalEvaluationFixture(t *testing.T, dialect string) (*Repo, *ProposalDetail, *graphCandidate, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, dialect, "v1")
	detail, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "eval"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r, detail, &graphCandidate{Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, Sources: state.Sources, Coverage: state.Revision.Coverage}, ids
}

func proposalNullable(ids map[string]string, key string, nullable bool) ProposalCommand {
	return ProposalCommand{Type: "alter_column", CommandID: key, Reason: "Customer is required", ColumnID: ids["column:orders:user_id"], Nullable: new(nullable)}
}

func proposalFK(ids map[string]string) ProposalCommand {
	return ProposalCommand{Type: "alter_constraint", CommandID: "new-user-fk", Reason: "Retain user membership", Action: "create", Name: "designed_user_fk", TableID: ids["table:orders"], TargetTableID: ids["table:users"], ColumnPairs: []DatabaseColumnPair{{FromColumnID: ids["column:orders:tenant_id"], ToColumnID: ids["column:users:tenant_id"]}, {FromColumnID: ids["column:orders:user_id"], ToColumnID: ids["column:users:id"]}}, UpdateAction: "no_action", DeleteAction: "restrict", MatchType: "simple", Deferrable: new(false), InitiallyDeferred: new(false)}
}

func TestProposalCommandStrictVariants(t *testing.T) {
	valid := `{"type":"alter_column","commandId":"c","reason":"Required","columnId":"018effb2-731a-7edb-b633-1387b184b21d","nullable":false}`
	for _, raw := range []string{
		`null`, `{}`, `{"type":"delete_node","commandId":"c","reason":"Delete"}`,
		strings.Replace(valid, `,"nullable":false`, "", 1), strings.Replace(valid, `false`, `null`, 1), strings.Replace(valid, `false`, `"false"`, 1),
		strings.Replace(valid, `"c"`, `"with space"`, 1), strings.Replace(valid, `"Required"`, `" "`, 1),
		valid[:len(valid)-1] + `,"columnId":"018effb2-731a-7edb-b633-1387b184b21d"}`, valid[:len(valid)-1] + `,"nativeType":"text"}`,
		`{"type":"set_criteria","commandId":"c","reason":"Check","criteria":[{"key":"k","kind":"writers","targetIds":["018effb2-731a-7edb-b633-1387b184b21d"],"description":"Check all writers","status":"verified"}]}`,
		`{"type":"set_criteria","commandId":"c","reason":"Check","criteria":null}`,
	} {
		var command ProposalCommand
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("accepted invalid command: %s", raw)
		}
	}
	var got ProposalCommand
	if err := json.Unmarshal([]byte(valid), &got); err != nil || got.Nullable == nil || *got.Nullable {
		t.Fatalf("false discarded: %+v %v", got, err)
	}
	if err := json.Unmarshal([]byte(`{"type":"set_criteria","commandId":"c","reason":"Check","criteria":[]}`), &got); err != nil {
		t.Fatal(err)
	}
}

func TestProposalNullableIntent(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			r, detail, base, ids := proposalEvaluationFixture(t, dialect)
			before := proposalSourceBytes(t, r)
			baseCopy, err := canonicalJSON(base)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "required", false)})
			if err != nil || candidate.CandidateHash == nil || candidate.GraphHash == nil || len(candidate.Diagnostics) != 0 || len(candidate.Overlays) != 1 {
				t.Fatalf("candidate: %+v %v", candidate, err)
			}
			overlay := candidate.Overlays[0]
			if overlay.SubjectID != ids["column:orders:user_id"] || string(overlay.Values["nullable"]) != `{"status":"known","value":false}` || overlay.PropertyOrigins["/nullable"].Kind != "intent" || overlay.PropertyOrigins["/nullable"].CommandID != "required" || overlay.PropertyOrigins["/nativeType"].Kind != "source" || overlay.Base == nil || overlay.Base.RevisionID != detail.Proposal.BaseRevisionID {
				t.Fatalf("intent or inheritance lost: %+v", overlay)
			}
			kinds := map[string]bool{}
			for _, criterion := range candidate.Criteria {
				if criterion.Status != "unverified" || criterion.Origin != "required" {
					t.Fatalf("false verification: %+v", criterion)
				}
				kinds[criterion.Kind] = true
			}
			if !kinds["existing_data"] || !kinds["writers"] || !kinds["migration_plan"] {
				t.Fatalf("missing mandatory checks: %+v", candidate.Criteria)
			}
			after, err := canonicalJSON(base)
			if err != nil || string(baseCopy) != string(after) {
				t.Fatal("evaluation mutated input source graph")
			}
			assertProposalSourceBytes(t, r, before)
		})
	}
}

func TestProposalFKOrderedPairs(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			_, detail, base, ids := proposalEvaluationFixture(t, dialect)
			command := proposalFK(ids)
			candidate, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{command})
			if err != nil || candidate.CandidateHash == nil || len(candidate.Overlays) != 2 || len(candidate.Changes) != 1 || len(candidate.Changes[0].GeneratedIDs) != 2 {
				t.Fatalf("FK candidate: %+v %v", candidate, err)
			}
			for _, o := range candidate.Overlays {
				if !ValidID(o.SubjectID) || o.Base != nil || len(o.Values["nativeDefinition"]) > 0 && string(o.Values["nativeDefinition"]) != "null" {
					t.Fatalf("fabricated source/native definition: %+v", o)
				}
			}
			repeated, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{command})
			if err != nil || *candidate.CandidateHash != *repeated.CandidateHash || *candidate.GraphHash != *repeated.GraphHash || !reflect.DeepEqual(candidate.Changes[0].GeneratedIDs, repeated.Changes[0].GeneratedIDs) {
				t.Fatalf("nondeterministic FK: %+v %v", repeated, err)
			}
			// The FK column order must match the selected key order.
			slices.Reverse(command.ColumnPairs)
			bad, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{command})
			if err != nil || bad.CandidateHash != nil || len(bad.Diagnostics) == 0 {
				t.Fatalf("known nonmatching ordered key allowed: %+v %v", bad, err)
			}
		})
	}
}

func TestProposalFKFinalGraphValidation(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	fk := proposalFK(ids)
	fk.DeleteAction = "set_null"
	// Both source columns must be nullable for SET NULL to be feasible.
	tenant := proposalNullable(ids, "optional-tenant", true)
	tenant.ColumnID = ids["column:orders:tenant_id"]
	user := proposalNullable(ids, "optional-user", true)
	good, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{fk, tenant, user})
	if err != nil || good.CandidateHash == nil {
		t.Fatalf("same-batch correction blocked: %+v %v", good, err)
	}
	bad, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{tenant, user, fk, proposalNullable(ids, "required-again", false)})
	if err != nil || bad.CandidateHash != nil || len(bad.Diagnostics) == 0 {
		t.Fatalf("final NOT NULL conflict accepted: %+v %v", bad, err)
	}
	found := false
	for _, diagnostic := range bad.Diagnostics {
		if diagnostic.CommandID == "new-user-fk" && diagnostic.Property == "/deleteAction" {
			found = true
		}
	}
	if !found {
		t.Fatalf("imprecise FK diagnostic: %+v", bad.Diagnostics)
	}
}

func TestProposalDialectValidation(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			_, detail, base, ids := proposalEvaluationFixture(t, dialect)
			for _, match := range []string{"simple", "full", "partial"} {
				c := proposalFK(ids)
				c.MatchType = match
				got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{c})
				wantValid := match == "simple" || dialect == "postgresql" && match == "full"
				if err != nil || (got.CandidateHash != nil) != wantValid {
					t.Fatalf("%s/%s: %+v %v", dialect, match, got, err)
				}
			}
			c := proposalFK(ids)
			c.InitiallyDeferred = new(true)
			got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{c})
			if err != nil || got.CandidateHash != nil {
				t.Fatalf("deferred without deferrable: %+v %v", got, err)
			}
		})
	}
}

func TestProposalCriteriaCannotClaimVerified(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "sqlite")
	set := ProposalCommand{Type: "set_criteria", CommandID: "supplement", Reason: "Review rollout", Criteria: []ProposalCriterionInput{{Key: "rollout", Kind: "migration_plan", TargetIDs: []string{ids["table:orders"]}, Description: "Review rollout and rollback"}}}
	first, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "required", false), set})
	if err != nil || first.CandidateHash == nil {
		t.Fatalf("criteria: %+v %v", first, err)
	}
	draft := detail.Revision
	draft.Overlays = first.Overlays
	draft.Criteria = first.Criteria
	clearCriteria := set
	clearCriteria.CommandID = "clear"
	clearCriteria.Criteria = []ProposalCriterionInput{}
	next, err := evaluateProposal(base, detail.Proposal, draft, []ProposalCommand{clearCriteria})
	if err != nil || next.CandidateHash == nil || len(next.Criteria) != 3 || *next.GraphHash != *first.GraphHash {
		t.Fatalf("mandatory criteria cleared or graph changed: %+v %v", next, err)
	}
	for _, c := range next.Criteria {
		if c.Origin != "required" || c.Status != "unverified" {
			t.Fatalf("criteria invalid: %+v", c)
		}
	}
}

func TestProposalCanonicalHash(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	command := proposalNullable(ids, "required", false)
	first, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{command})
	if err != nil {
		t.Fatal(err)
	}
	raw := relationalRaw(t, command)
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var reordered ProposalCommand
	// Explicit reversed object-member order, rather than computing an expected hash.
	b := `{"nullable":false,"columnId":` + string(fields["columnId"]) + `,"reason":"Customer is required","commandId":"required","type":"alter_column"}`
	if err := json.Unmarshal([]byte(b), &reordered); err != nil {
		t.Fatal(err)
	}
	second, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{reordered})
	if err != nil || *second.CandidateHash != *first.CandidateHash {
		t.Fatalf("JSON order changed semantics: %+v %v", second, err)
	}
	command.Reason = "Different reason"
	third, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{command})
	if err != nil || *third.CandidateHash == *first.CandidateHash || *third.GraphHash == *first.GraphHash {
		t.Fatalf("reason lost from hash: %+v %v", third, err)
	}
}

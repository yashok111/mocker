package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

func TestProposalProvenanceDoesNotRewriteSource(t *testing.T) {
	t.Parallel()
	r, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	before := proposalSourceBytes(t, r)
	c := proposalFK(ids)
	c.Action = "update"
	c.Name = ""
	c.TableID = ""
	c.ConstraintID = ids["constraint:orders:user_fk"]
	got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{c})
	if err != nil || got.CandidateHash == nil || len(got.Overlays) != 2 {
		t.Fatalf("update: %+v %v", got, err)
	}
	for _, o := range got.Overlays {
		if o.Base == nil || o.Base.RevisionID != detail.Proposal.BaseRevisionID {
			t.Fatalf("lost baseline: %+v", o)
		}
		for _, key := range []string{"sourceKind", "sourceSnapshotId", "freshness", "evidenceIds"} {
			if _, ok := o.Values[key]; ok {
				t.Fatalf("source assertion in designed values: %s", key)
			}
		}
		if o.Kind == "constraint" && string(o.Values["nativeDefinition"]) != "null" {
			t.Fatalf("source definition asserted for changed FK: %+v", o)
		}
		if len(got.Changes[0].GeneratedIDs) != 0 {
			t.Fatal("update allocated a replacement identity")
		}
	}
	assertProposalSourceBytes(t, r, before)
}

func TestProposalNullableUnknownStaleAndSameValue(t *testing.T) {
	for _, state := range []string{"known", "unknown", "stale"} {
		t.Run(state, func(t *testing.T) {
			_, detail, base, ids := proposalEvaluationFixture(t, "sqlite")
			for i, n := range base.Nodes {
				if n.ID != ids["column:orders:user_id"] {
					continue
				}
				facets, _, err := relationalFacetObject(n.Kind, n.Attributes)
				if err != nil {
					t.Fatal(err)
				}
				values, err := relationalObject(facets["sql"])
				if err != nil {
					t.Fatal(err)
				}
				if state == "unknown" {
					values["nullable"] = relationalRaw(t, unknown("Source value unavailable"))
				}
				if state == "known" {
					values["nullable"] = relationalRaw(t, known(false))
				}
				if state == "stale" {
					values["freshness"] = relationalRaw(t, AssertionFreshness{Status: "stale", ConfirmedSnapshotID: detail.Revision.SourceSnapshotIDs[0], Reasons: []string{"Source unavailable"}})
				}
				facets["sql"] = relationalRaw(t, values)
				base.Nodes[i].Attributes["facets"] = relationalRaw(t, facets)
			}
			got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "same-or-unknown", false)})
			if err != nil || got.CandidateHash == nil || len(got.Criteria) != 3 || len(got.Changes) != 1 || got.Overlays[0].PropertyOrigins["/nullable"].Kind != "intent" {
				t.Fatalf("lost desired intent: %+v %v", got, err)
			}
			if state != "known" && !strings.Contains(strings.Join(got.Limitations, " "), "unknown or stale") {
				t.Fatalf("lost source limitation: %+v", got)
			}
		})
	}
}

func TestProposalFKUnknownUniqueness(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	for i, n := range base.Nodes {
		if n.ID != ids["table:users"] && n.ID != ids["constraint:users:pk"] {
			continue
		}
		facets, _, err := relationalFacetObject(n.Kind, n.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		values, err := relationalObject(facets["sql"])
		if err != nil {
			t.Fatal(err)
		}
		values["freshness"] = relationalRaw(t, AssertionFreshness{Status: "stale", ConfirmedSnapshotID: detail.Revision.SourceSnapshotIDs[0], Reasons: []string{"Source unavailable"}})
		facets["sql"] = relationalRaw(t, values)
		base.Nodes[i].Attributes["facets"] = relationalRaw(t, facets)
	}
	got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalFK(ids)})
	if err != nil || got.CandidateHash == nil || !strings.Contains(strings.Join(got.Limitations, " "), "Target uniqueness is unknown") {
		t.Fatalf("partial uniqueness misrepresented: %+v %v", got, err)
	}
}

func TestProposalFKReferencesAndDuplicateNames(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	for _, tc := range []struct {
		name   string
		modify func(*ProposalCommand)
	}{
		{"wrong-source-table", func(c *ProposalCommand) { c.ColumnPairs[0].FromColumnID = ids["column:payments:tenant_id"] }},
		{"wrong-target-table", func(c *ProposalCommand) { c.ColumnPairs[0].ToColumnID = ids["column:orders:tenant_id"] }},
		{"missing-target", func(c *ProposalCommand) { c.TargetTableID = uuid.NewV7().String() }},
		{"wrong-kind", func(c *ProposalCommand) { c.TargetTableID = ids["column:users:id"] }},
		{"duplicate-pairs", func(c *ProposalCommand) { c.ColumnPairs[1] = c.ColumnPairs[0] }},
		{"duplicate-name", func(c *ProposalCommand) { c.Name = "orders_user_fk" }},
		{"update-other-kind", func(c *ProposalCommand) {
			c.Action = "update"
			c.Name = ""
			c.TableID = ""
			c.ConstraintID = ids["constraint:orders:pk"]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := proposalFK(ids)
			tc.modify(&c)
			got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{c})
			if err != nil || got.CandidateHash != nil || len(got.Diagnostics) == 0 {
				t.Fatalf("invalid FK accepted: %+v %v", got, err)
			}
		})
	}
}

func TestProposalFKSelfAndCycles(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	self := proposalFK(ids)
	self.CommandID = "self"
	self.Name = "orders_self_fk"
	self.TargetTableID = ids["table:orders"]
	self.ColumnPairs[1].ToColumnID = ids["column:orders:id"]
	self.ColumnPairs[0].ToColumnID = ids["column:orders:tenant_id"]
	cycle := proposalFK(ids)
	cycle.CommandID = "cycle"
	cycle.Name = "users_order_fk"
	cycle.TableID = ids["table:users"]
	cycle.TargetTableID = ids["table:orders"]
	cycle.ColumnPairs = []DatabaseColumnPair{{FromColumnID: ids["column:users:tenant_id"], ToColumnID: ids["column:orders:tenant_id"]}, {FromColumnID: ids["column:users:id"], ToColumnID: ids["column:orders:id"]}}
	got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{self, proposalFK(ids), cycle})
	if err != nil || got.CandidateHash == nil || len(got.Overlays) != 6 {
		t.Fatalf("legal topology rejected: %+v %v", got, err)
	}
}

func TestProposalCommandLimits(t *testing.T) {
	_, detail, base, ids := proposalEvaluationFixture(t, "sqlite")
	commands := []ProposalCommand{}
	for i := range 100 {
		c := proposalNullable(ids, uuid.NewV7().String(), false)
		if i == 0 {
			c.Reason = strings.Repeat("я", 2048)
		}
		commands = append(commands, c)
	}
	got, err := evaluateProposal(base, detail.Proposal, detail.Revision, commands)
	if err != nil || got.CandidateHash == nil {
		t.Fatalf("valid batch boundary: %+v %v", got, err)
	}
	_, err = evaluateProposal(base, detail.Proposal, detail.Revision, append(commands, proposalNullable(ids, "over", false)))
	assertFault(t, err, "backend_limit")
	commands[0].Reason += "я"
	_, err = evaluateProposal(base, detail.Proposal, detail.Revision, commands[:1])
	assertFault(t, err, "backend_invalid")
	_, err = evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "duplicate", false), proposalNullable(ids, "duplicate", true)})
	assertFault(t, err, "backend_invalid")
}

func TestProposalCriteriaKeyCollision(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "sqlite")
	first, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "require", false)})
	if err != nil {
		t.Fatal(err)
	}
	set := ProposalCommand{Type: "set_criteria", CommandID: "collision", Reason: "Extra", Criteria: []ProposalCriterionInput{{Key: first.Criteria[0].Key, Kind: "writers", TargetIDs: []string{ids["column:orders:user_id"]}, Description: "Check"}}}
	draft := detail.Revision
	draft.Overlays, draft.Criteria = first.Overlays, first.Criteria
	got, err := evaluateProposal(base, detail.Proposal, draft, []ProposalCommand{set})
	if err != nil || got.CandidateHash != nil || len(got.Diagnostics) == 0 {
		t.Fatalf("criteria key collision allowed: %+v %v", got, err)
	}
}

func TestProposalUpdatesDesignedFK(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "sqlite")
	first, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalFK(ids)})
	if err != nil {
		t.Fatal(err)
	}
	id := first.Changes[0].GeneratedIDs["constraintId"]
	draft := detail.Revision
	draft.Overlays, draft.Criteria = first.Overlays, first.Criteria
	c := proposalFK(ids)
	c.Action = "update"
	c.CommandID = "update-designed"
	c.Name = ""
	c.TableID = ""
	c.ConstraintID = id
	c.DeleteAction = "cascade"
	got, err := evaluateProposal(base, detail.Proposal, draft, []ProposalCommand{c})
	if err != nil || got.CandidateHash == nil || len(got.Overlays) != 2 || len(got.Changes[0].GeneratedIDs) != 0 {
		t.Fatalf("designed FK update: %+v %v", got, err)
	}
	if !reflect.DeepEqual(first.Overlays[0].Base, got.Overlays[0].Base) {
		t.Fatal("designed record acquired source basis")
	}
}

func TestProposalPreservesAllInheritedNativeFields(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "require", false)})
	if err != nil {
		t.Fatal(err)
	}
	node := base.Nodes[slices.IndexFunc(base.Nodes, func(n Node) bool { return n.ID == ids["column:orders:user_id"] })]
	fs, _, err := relationalFacetObject(node.Kind, node.Attributes)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]jsontext.Value
	if err := json.Unmarshal(fs["sql"], &values); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"nativeType", "defaultExpression", "generatedExpression", "identity", "ordinal"} {
		// Exact raw inherited values, independently read from the source record.
		if string(values[key]) != string(got.Overlays[0].Values[key]) {
			t.Fatalf("native field normalized: %s %s / %s", key, values[key], got.Overlays[0].Values[key])
		}
	}
}

func TestProposalAuthoredDialectFixtures(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			_, detail, base, ids := proposalEvaluationFixture(t, dialect)
			b, err := os.ReadFile(filepath.Join("testdata/relational/proposals", dialect, "commands.json"))
			if err != nil {
				t.Fatal(err)
			}
			for key, id := range ids {
				b = []byte(strings.ReplaceAll(string(b), "@"+key+"@", id))
			}
			var commands []ProposalCommand
			if err := json.Unmarshal(b, &commands); err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(filepath.Join("testdata/relational/proposals", dialect, "expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			var expected struct {
				NullableColumnKey    string      `json:"nullableColumnKey"`
				Nullable             bool        `json:"nullable"`
				NativeType           string      `json:"nativeType"`
				RequiredCriteria     int         `json:"requiredCriteria"`
				SupplementalCriteria int         `json:"supplementalCriteria"`
				CommandIDs           []string    `json:"commandIds"`
				PairKeys             [][2]string `json:"pairKeys"`
			}
			if err := json.Unmarshal(b, &expected, json.RejectUnknownMembers(true)); err != nil {
				t.Fatal(err)
			}
			got, err := evaluateProposal(base, detail.Proposal, detail.Revision, commands)
			if err != nil || got.CandidateHash == nil || len(got.Overlays) != 3 {
				t.Fatalf("fixture: %+v %v", got, err)
			}
			required, supplemental := 0, 0
			for _, c := range got.Criteria {
				if c.Status != "unverified" {
					t.Fatal("fixture claims verification")
				}
				if c.Origin == "required" {
					required++
				} else {
					supplemental++
				}
			}
			if required != expected.RequiredCriteria || supplemental != expected.SupplementalCriteria {
				t.Fatalf("criteria: %d/%d", required, supplemental)
			}
			for i, change := range got.Changes {
				if change.CommandID != expected.CommandIDs[i] {
					t.Fatalf("command order: %+v", got.Changes)
				}
			}
			for _, o := range got.Overlays {
				if o.SubjectID == ids[expected.NullableColumnKey] {
					var nullable struct {
						Value bool `json:"value"`
					}
					if err := json.Unmarshal(o.Values["nullable"], &nullable); err != nil || nullable.Value != expected.Nullable {
						t.Fatalf("nullable: %s %v", o.Values["nullable"], err)
					}
					var native struct {
						Value string `json:"value"`
					}
					if err := json.Unmarshal(o.Values["nativeType"], &native); err != nil || native.Value != expected.NativeType {
						t.Fatalf("native: %s %v", o.Values["nativeType"], err)
					}
				}
				if o.Kind == "references" {
					var pairs []DatabaseColumnPair
					if err := json.Unmarshal(o.Values["columnPairs"], &pairs); err != nil {
						t.Fatal(err)
					}
					if len(pairs) != len(expected.PairKeys) {
						t.Fatal("pair count")
					}
					for i, keys := range expected.PairKeys {
						if pairs[i].FromColumnID != ids[keys[0]] || pairs[i].ToColumnID != ids[keys[1]] {
							t.Fatalf("ordered pairs: %+v", pairs)
						}
					}
				}
			}
		})
	}
}

func TestProposalDesignedUUIDOracle(t *testing.T) {
	if id := proposalDesignedID("018effb2-731a-7edb-b633-1387b184b21d", "new-fk", "constraint"); id != "8738be67-2269-5809-9992-0d024fe69074" {
		t.Fatalf("UUIDv5 namespace/name contract changed: %s", id)
	}
}

func TestProposalCriteriaCollisionInReverseOrder(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "sqlite")
	first, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "require", false)})
	if err != nil {
		t.Fatal(err)
	}
	set := ProposalCommand{Type: "set_criteria", CommandID: "authored", Reason: "Check", Criteria: []ProposalCriterionInput{{Key: first.Criteria[0].Key, Kind: "writers", TargetIDs: []string{ids["table:orders"]}, Description: "Check"}}}
	got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{set, proposalNullable(ids, "require", false)})
	if err != nil || got.CandidateHash != nil || len(got.Diagnostics) == 0 {
		t.Fatalf("reverse-order duplicate criteria: %+v %v", got, err)
	}
}

func TestProposalCriteriaCanTargetSelectedRelationship(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	edgeID := ""
	for _, edge := range base.Edges {
		if edge.Kind == "references" && edge.From == ids["constraint:orders:user_fk"] {
			edgeID = edge.ID
			break
		}
	}
	if edgeID == "" {
		t.Fatal("fixture relationship absent")
	}
	set := ProposalCommand{Type: "set_criteria", CommandID: "review-edge", Reason: "Check", Criteria: []ProposalCriterionInput{{Key: "relationship", Kind: "referential_integrity", TargetIDs: []string{edgeID}, Description: "Check selected relationship"}}}
	got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{set})
	if err != nil || got.CandidateHash == nil {
		t.Fatalf("selected relationship target rejected: %+v %v", got, err)
	}
}

func TestProposalCriteriaPairAndPayloadLimits(t *testing.T) {
	_, detail, base, ids := proposalEvaluationFixture(t, "sqlite")
	set := ProposalCommand{Type: "set_criteria", CommandID: "criteria-boundary", Reason: "Check", Criteria: []ProposalCriterionInput{}}
	for range 100 {
		set.Criteria = append(set.Criteria, ProposalCriterionInput{Key: uuid.NewV7().String(), Kind: "writers", TargetIDs: []string{ids["table:orders"]}, Description: strings.Repeat("x", 4096)})
	}
	got, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{set})
	if err != nil || got.CandidateHash == nil || len(got.Criteria) != 100 {
		t.Fatalf("criteria boundary: %+v %v", got, err)
	}
	set.Criteria = append(set.Criteria, set.Criteria[0])
	set.Criteria[100].Key = "over-limit"
	_, err = evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{set})
	assertFault(t, err, "backend_limit")
	set.Criteria = set.Criteria[:100]
	one, two, three := set, set, set
	one.CommandID, two.CommandID, three.CommandID = "one", "two", "three"
	_, err = evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{one, two, three})
	assertFault(t, err, "backend_limit")
	fk := proposalFK(ids)
	fk.ColumnPairs = nil
	for range 64 {
		fk.ColumnPairs = append(fk.ColumnPairs, DatabaseColumnPair{FromColumnID: uuid.NewV7().String(), ToColumnID: uuid.NewV7().String()})
	}
	var decoded ProposalCommand
	if err := json.Unmarshal(relationalRaw(t, fk), &decoded); err != nil || len(decoded.ColumnPairs) != 64 {
		t.Fatalf("pair boundary: %+v %v", decoded, err)
	}
	fk.ColumnPairs = append(fk.ColumnPairs, DatabaseColumnPair{FromColumnID: uuid.NewV7().String(), ToColumnID: uuid.NewV7().String()})
	err = json.Unmarshal(relationalRaw(t, fk), &decoded)
	assertFault(t, err, "backend_limit")
}

func TestProposalProjectionSeparatesSourceAndIntent(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "postgresql")
	candidate, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "require", false), proposalFK(ids)})
	if err != nil {
		t.Fatal(err)
	}
	for _, overlay := range candidate.Overlays {
		if overlay.Kind == "column" {
			n := base.Nodes[slices.IndexFunc(base.Nodes, func(n Node) bool { return n.ID == overlay.SubjectID })]
			projected, err := projectProposalNode(detail.Proposal, nil, &n, &overlay)
			if err != nil || projected.SourceRecord == nil || !reflect.DeepEqual(*projected.SourceRecord, n) || projected.EffectiveFacet.Origin != "proposal" || projected.EffectiveFacet.ProposalRevisionID != nil || projected.EffectiveFacet.PropertyOrigins["/nullable"].Kind != "intent" || projected.EffectiveFacet.PropertyOrigins["/nativeType"].Kind != "source" || len(projected.EffectiveFacet.BasisEvidenceIDs) == 0 {
				t.Fatalf("source/intent projection: %+v %v", projected, err)
			}
		} else if overlay.Kind == "constraint" {
			projected, err := projectProposalNode(detail.Proposal, new(detail.Revision.ID), nil, &overlay)
			if err != nil || projected.SourceRecord != nil || projected.EffectiveFacet.Base != nil || len(projected.EffectiveFacet.BasisEvidenceIDs) != 0 || projected.EffectiveFacet.ProposalRevisionID == nil || string(projected.EffectiveFacet.Values["nativeDefinition"]) != "null" {
				t.Fatalf("fabricated source for designed FK: %+v %v", projected, err)
			}
		} else {
			projected, err := projectProposalEdge(detail.Proposal, nil, nil, &overlay)
			if err != nil || projected.SourceRecord != nil || projected.From != candidate.Changes[1].GeneratedIDs["constraintId"] || projected.To != ids["table:users"] || len(projected.EffectiveFacet.BasisEvidenceIDs) != 0 {
				t.Fatalf("designed reference projection: %+v %v", projected, err)
			}
		}
	}
}

func TestProposalProjectionRejectsMixedSubjects(t *testing.T) {
	t.Parallel()
	_, detail, base, ids := proposalEvaluationFixture(t, "sqlite")
	candidate, err := evaluateProposal(base, detail.Proposal, detail.Revision, []ProposalCommand{proposalNullable(ids, "require", false)})
	if err != nil {
		t.Fatal(err)
	}
	n := base.Nodes[slices.IndexFunc(base.Nodes, func(n Node) bool { return n.ID == ids["column:orders:tenant_id"] })]
	_, err = projectProposalNode(detail.Proposal, nil, &n, &candidate.Overlays[0])
	if err == nil {
		t.Fatal("projection mixed two subjects")
	}
	n = base.Nodes[slices.IndexFunc(base.Nodes, func(n Node) bool { return n.ID == ids["column:orders:user_id"] })]
	candidate.Overlays[0].FacetKey = "orm"
	_, err = projectProposalNode(detail.Proposal, nil, &n, &candidate.Overlays[0])
	if err == nil {
		t.Fatal("projection mixed two facets")
	}
}

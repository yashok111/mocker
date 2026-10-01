//go:build integration

package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
)

type relationalDescriptorCase struct {
	name, key, descriptor string
}

var relationalDescriptorCases = []relationalDescriptorCase{
	{name: "datastore", key: "database:orders", descriptor: "relational"},
	{name: "routine", key: "symbol:touch_order", descriptor: "databaseRoutine"},
}

func descriptorCanonical(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := canonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func descriptorFacetValues(t *testing.T, n *Node, descriptor string) map[string]jsontext.Value {
	t.Helper()
	var value struct {
		Facets map[string]jsontext.Value `json:"facets"`
	}
	if err := json.Unmarshal(n.Attributes[descriptor], &value); err != nil || len(value.Facets) == 0 {
		t.Fatalf("descriptor %s disappeared from %s: attrs=%s, err=%v", descriptor, n.ExternalKey, relationalRaw(t, n.Attributes), err)
	}
	return value.Facets
}

func commitDescriptorFixture(t *testing.T, r *Repo, tc relationalDescriptorCase) (*ImportCommitResult, map[string]string) {
	t.Helper()
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, relationalFixtureInput(t, p, "postgresql", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	commands := relationalFixture(t, p, s, "postgresql", "v1")
	n := relationalCommand(commands, tc.key).Node
	var descriptor map[string]jsontext.Value
	if err := json.Unmarshal(n.Attributes[tc.descriptor], &descriptor); err != nil {
		t.Fatal(err)
	}
	var facets map[string]jsontext.Value
	if err := json.Unmarshal(descriptor["facets"], &facets); err != nil {
		t.Fatal(err)
	}
	// Two assertions share the same complete proof record; keys are provider-owned.
	facets["sql:shared-proof"] = bytes.Clone(facets["sql"])
	descriptor["facets"] = relationalRaw(t, facets)
	n.Attributes[tc.descriptor] = relationalRaw(t, descriptor)
	v, ids := stageRelational(t, r, p, s, commands, "cold-source")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "cold-source-commit")
	if err != nil {
		t.Fatal(err)
	}
	return out, ids
}

func descriptorMetadataCommands(t *testing.T, p *Project, s *ImportSession, tc relationalDescriptorCase, freshKey bool) []ImportCommand {
	t.Helper()
	proofKey := "proof:v1:" + tc.key + ":sql"
	commands := relationalSelect(relationalFixture(t, p, s, "postgresql", "v1"), tc.key, proofKey)
	n := relationalCommand(commands, tc.key).Node
	n.Attributes = map[string]jsontext.Value{"description": jsontext.Value(`"metadata-only update"`)}
	proof := relationalCommand(commands, proofKey).Evidence
	proof.PropertyPath = nil
	proof.Explanation = "Only ordinary subject metadata reobserved"
	if freshKey {
		proof.ExternalKey = "proof:metadata:" + tc.key
	}
	n.EvidenceKeys = []string{proof.ExternalKey}
	return commands
}

func descriptorReconcileInput(t *testing.T, p *Project, repository string, tc relationalDescriptorCase, key string) BeginImportInput {
	t.Helper()
	in := relationalReconcileInput(t, p, repository, "postgresql", "v1", key)
	if tc.descriptor == "databaseRoutine" {
		for i := range in.Inventory {
			if in.Inventory[i].Category == "datastores" {
				in.Inventory[i].Status = "partial"
				in.Inventory[i].KnownCount = 0
				in.Inventory[i].Denominator = nil
				in.Inventory[i].Gaps = []string{"Only routine metadata reobserved"}
			}
		}
	}
	return in
}

func TestRelationalMetadataOnlyUpsertRetainsOptionalDescriptors(t *testing.T) {
	for _, tc := range relationalDescriptorCases {
		for _, scope := range []string{"complete", "partial"} {
			t.Run(tc.name+"/"+scope, func(t *testing.T) {
				r, _ := testRepo(t)
				first, ids := commitDescriptorFixture(t, r, tc)
				p := &first.Project
				old, err := r.Node(t.Context(), p.ID, first.Revision.ID, ids[tc.key])
				if err != nil {
					t.Fatal(err)
				}
				oldBytes := descriptorCanonical(t, old)
				oldFacets := descriptorFacetValues(t, old, tc.descriptor)
				proofID := ids["proof:v1:"+tc.key+":sql"]
				oldProofs, err := r.Evidence(t.Context(), p.ID, first.Revision.ID, EvidenceQueryInput{EvidenceID: proofID})
				if err != nil || len(oldProofs.Items) != 1 {
					t.Fatalf("old proof unavailable: %+v, %v", oldProofs, err)
				}
				proofBefore := oldProofs.Items[0]
				oldProofInventory, err := r.Evidence(t.Context(), p.ID, first.Revision.ID, EvidenceQueryInput{Limit: 500})
				if err != nil || oldProofInventory.NextCursor != "" {
					t.Fatal("old proof inventory unavailable", err)
				}
				coverage, err := r.RevisionCoverage(t.Context(), p.ID, first.Revision.ID)
				if err != nil {
					t.Fatal(err)
				}
				in := descriptorReconcileInput(t, p, coverage.Snapshots[0].RepositoryID, tc, "metadata-only")
				in.GraphScope.Status = scope
				if scope == "partial" {
					in.GraphScope.Gaps = []string{"Only ordinary metadata reobserved"}
				}
				s, err := r.BeginImport(t.Context(), p.ID, in)
				if err != nil {
					t.Fatal(err)
				}
				v, currentIDs := stageRelational(t, r, p, s, descriptorMetadataCommands(t, p, s, tc, true), "metadata")
				if v.State != "ready" || v.CandidateHash == nil || currentIDs[tc.key] != ids[tc.key] {
					t.Fatalf("metadata preview: %+v", v)
				}
				if v.Summary.Nodes != first.Revision.Coverage.KnownObjects {
					t.Fatalf("metadata upsert changed node inventory: %+v", v.Summary)
				}
				out, err := commitFixture(t, r, p, s, v, "metadata-commit")
				if err != nil {
					t.Fatal(err)
				}
				current, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids[tc.key])
				if err != nil {
					t.Fatal(err)
				}
				newFacets := descriptorFacetValues(t, current, tc.descriptor)
				if len(newFacets) != len(oldFacets) || string(current.Attributes["description"]) != `"metadata-only update"` {
					t.Fatalf("metadata/facet replacement lost data: %s", relationalRaw(t, current))
				}
				for key, before := range oldFacets {
					var previous, retained map[string]jsontext.Value
					if err := json.Unmarshal(before, &previous); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(newFacets[key], &retained); err != nil {
						t.Fatal(err)
					}
					var freshness AssertionFreshness
					if err := json.Unmarshal(retained["freshness"], &freshness); err != nil || freshness.Status != "stale" || freshness.ConfirmedSnapshotID != proofBefore.Source.SnapshotID || !slices.Contains(freshness.Reasons, "not_reobserved") {
						t.Fatalf("omitted facet %s not retained stale: %s, %v", key, newFacets[key], err)
					}
					delete(previous, "freshness")
					delete(retained, "freshness")
					if !bytes.Equal(descriptorCanonical(t, previous), descriptorCanonical(t, retained)) {
						t.Fatalf("omitted facet %s changed its definition/proofs/source: before=%s after=%s", key, before, newFacets[key])
					}
				}
				metadataProofID := currentIDs["proof:metadata:"+tc.key]
				if metadataProofID == proofID || !slices.Contains(current.EvidenceIDs, proofID) || !slices.Contains(current.EvidenceIDs, metadataProofID) || len(current.EvidenceIDs) != 2 {
					t.Fatalf("shared retained/new proof union wrong: %v", current.EvidenceIDs)
				}
				retainedProofs, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{EvidenceID: proofID})
				if err != nil || len(retainedProofs.Items) != 1 {
					t.Fatalf("retained proof unavailable: %+v, %v", retainedProofs, err)
				}
				proofAfter := retainedProofs.Items[0]
				if proofAfter.Freshness == nil || proofAfter.Freshness.Status != "stale" || proofAfter.Freshness.ConfirmedSnapshotID != proofBefore.Source.SnapshotID {
					t.Fatalf("old proof falsely current: %+v", proofAfter)
				}
				proofBefore.Freshness, proofAfter.Freshness = nil, nil
				if !bytes.Equal(descriptorCanonical(t, proofBefore), descriptorCanonical(t, proofAfter)) {
					t.Fatal("retained source/proof body changed")
				}
				page, err := r.QueryDatabase(t.Context(), p.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "tables"})
				if err != nil || len(page.TableItems) != 4 || page.RevisionID != out.Revision.ID || page.SemanticHash != out.Revision.SemanticHash || page.Coverage.Coverage.Status != "partial" {
					t.Fatalf("database became unavailable or falsely complete: %+v, %v", page, err)
				}
				if len(page.Coverage.Snapshots) != 2 || !slices.ContainsFunc(page.Coverage.Snapshots, func(source SourceSnapshot) bool { return source.ID == s.SnapshotID && source.Role == "primary" }) || !slices.ContainsFunc(page.Coverage.Snapshots, func(source SourceSnapshot) bool {
					return source.ID == proofBefore.Source.SnapshotID && source.Role == "retained_provenance" && source.ManifestHash == coverage.Snapshots[0].ManifestHash
				}) {
					t.Fatalf("old/new source vector lost: %+v", page.Coverage.Snapshots)
				}
				metadataProofs, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{EvidenceID: metadataProofID})
				if err != nil || len(metadataProofs.Items) != 1 || metadataProofs.Items[0].Source.SnapshotID != s.SnapshotID {
					t.Fatalf("new metadata proof source lost: %+v, %v", metadataProofs, err)
				}
				if current.Freshness.Status == "current" && (metadataProofs.Items[0].Freshness == nil || metadataProofs.Items[0].Freshness.Status != "current") {
					t.Fatal("current metadata proof incorrectly made stale")
				}
				again, err := r.Node(t.Context(), p.ID, first.Revision.ID, ids[tc.key])
				if err != nil || !bytes.Equal(oldBytes, descriptorCanonical(t, again)) {
					t.Fatal("old immutable node changed", err)
				}
				oldRevision, err := r.Revision(t.Context(), p.ID, first.Revision.ID)
				if err != nil || oldRevision.SemanticHash != first.Revision.SemanticHash || out.Revision.SemanticHash == oldRevision.SemanticHash {
					t.Fatal("old/new immutable semantic hash wrong", err)
				}
				allProofs, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{Limit: 500})
				if err != nil || allProofs.NextCursor != "" || v.Summary.Evidence != int64(len(allProofs.Items)) || len(allProofs.Items) != len(oldProofInventory.Items)+1 {
					t.Fatalf("retained proofs absent from candidate quota counts: %+v, %v", v.Summary, err)
				}
			})
		}
	}
}

func TestRelationalDescriptorOmissionWithReobservedDependencies(t *testing.T) {
	for _, tc := range relationalDescriptorCases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			first, ids := commitDescriptorFixture(t, r, tc)
			p := &first.Project
			coverage, err := r.RevisionCoverage(t.Context(), p.ID, first.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			s, err := r.BeginImport(t.Context(), p.ID, relationalReconcileInput(t, p, coverage.Snapshots[0].RepositoryID, "postgresql", "v1", "reobserved-dependencies"))
			if err != nil {
				t.Fatal(err)
			}
			commands := slices.DeleteFunc(relationalFixture(t, p, s, "postgresql", "v1"), func(command ImportCommand) bool {
				_, key, _ := commandAddress(command)
				return key == tc.key || key == "proof:v1:"+tc.key+":sql"
			})
			commands = append(commands, descriptorMetadataCommands(t, p, s, tc, true)...)
			v, newIDs := stageRelational(t, r, p, s, commands, "all-dependencies")
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			out, err := commitFixture(t, r, p, s, v, "all-dependencies-commit")
			if err != nil {
				t.Fatal(err)
			}
			current, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids[tc.key])
			if err != nil || len(descriptorFacetValues(t, current, tc.descriptor)) != 2 || current.Freshness == nil || current.Freshness.Status != "current" {
				t.Fatal("ordinary metadata subject falsely stale", err)
			}
			for _, check := range []struct{ id, status, snapshot string }{
				{id: ids["proof:v1:"+tc.key+":sql"], status: "stale", snapshot: coverage.Snapshots[0].ID},
				{id: newIDs["proof:metadata:"+tc.key], status: "current", snapshot: s.SnapshotID},
			} {
				proof, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{EvidenceID: check.id})
				if err != nil || len(proof.Items) != 1 || proof.Items[0].Freshness == nil || proof.Items[0].Freshness.Status != check.status || proof.Items[0].Freshness.ConfirmedSnapshotID != check.snapshot || proof.Items[0].Source.SnapshotID != check.snapshot {
					t.Fatalf("independent proof freshness lost: want=%+v, got=%+v, %v", check, proof, err)
				}
			}
		})
	}
}

func TestRelationalDescriptorOmissionProtectsRetainedProof(t *testing.T) {
	for _, tc := range relationalDescriptorCases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			first, ids := commitDescriptorFixture(t, r, tc)
			p := &first.Project
			coverage, err := r.RevisionCoverage(t.Context(), p.ID, first.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			in := descriptorReconcileInput(t, p, coverage.Snapshots[0].RepositoryID, tc, "omitted-proof")
			in.GraphScope.Status = "partial"
			in.GraphScope.Gaps = []string{"Descriptor not reobserved"}
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			v, _ := stageRelational(t, r, p, s, descriptorMetadataCommands(t, p, s, tc, false), "changed-retained-proof")
			if v.State != "needs_resolution" || v.CandidateHash != nil || !slices.ContainsFunc(v.Diagnostics, func(d ImportDiagnostic) bool { return d.Code == "backend_facet_evidence_conflict" }) {
				t.Fatalf("omission allowed old shared proof overwrite: %+v", v)
			}
			head, err := r.Get(t.Context(), p.ID)
			if err != nil || head.CurrentRevisionID != first.Revision.ID || head.Version != p.Version {
				t.Fatal("proof conflict published a change", err)
			}
			old, err := r.Node(t.Context(), p.ID, first.Revision.ID, ids[tc.key])
			if err != nil || len(descriptorFacetValues(t, old, tc.descriptor)) != 2 {
				t.Fatal("proof conflict lost old descriptor", err)
			}
			status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
			if err != nil {
				t.Fatal(err)
			}
			s.Version = status.Session.Version
			repair := append([]ImportCommand{{Op: "remove", Remove: &ImportRemove{RecordType: "evidence", ExternalKey: "proof:v1:" + tc.key + ":sql"}}}, descriptorMetadataCommands(t, p, s, tc, true)...)
			good, newIDs := stageRelational(t, r, p, s, repair, "repair")
			if good.State != "ready" || good.CandidateHash == nil {
				t.Fatalf("fresh proof could not repair omission conflict: %+v", good)
			}
			out, err := commitFixture(t, r, p, s, good, "repair-commit")
			if err != nil {
				t.Fatal(err)
			}
			current, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids[tc.key])
			if err != nil || len(descriptorFacetValues(t, current, tc.descriptor)) != 2 || !slices.Contains(current.EvidenceIDs, newIDs["proof:metadata:"+tc.key]) || !slices.Contains(current.EvidenceIDs, ids["proof:v1:"+tc.key+":sql"]) {
				t.Fatal("repair failed to retain descriptor/proof union", err)
			}
		})
	}
}

func TestRelationalDescriptorOmissionDoesNotAcceptMalformedDescriptors(t *testing.T) {
	for _, tc := range relationalDescriptorCases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			first, ids := commitDescriptorFixture(t, r, tc)
			p := &first.Project
			coverage, err := r.RevisionCoverage(t.Context(), p.ID, first.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, malformed := range []struct{ name, raw string }{
				{name: "null", raw: `null`},
				{name: "string", raw: `"not an object"`},
				{name: "empty", raw: `{}`},
				{name: "empty-facets", raw: `{"facets":{}}`},
			} {
				t.Run(malformed.name, func(t *testing.T) {
					in := descriptorReconcileInput(t, p, coverage.Snapshots[0].RepositoryID, tc, "malformed-"+malformed.name)
					s, err := r.BeginImport(t.Context(), p.ID, in)
					if err != nil {
						t.Fatal(err)
					}
					commands := descriptorMetadataCommands(t, p, s, tc, true)
					relationalCommand(commands, tc.key).Node.Attributes[tc.descriptor] = jsontext.Value(malformed.raw)
					hash, err := ImportBatchHash(commands)
					if err != nil {
						t.Fatal(err)
					}
					_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "invalid-descriptor", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands})
					assertFault(t, err, "backend_import_invalid")
					status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
					if err != nil || status.Session.Version != s.Version || status.Session.AcceptedBatchCount != 0 {
						t.Fatalf("malformed descriptor published a batch: %+v, %v", status, err)
					}
					head, err := r.Get(t.Context(), p.ID)
					if err != nil || head.CurrentRevisionID != first.Revision.ID || head.Version != p.Version {
						t.Fatal("malformed descriptor changed head", err)
					}
					old, err := r.Node(t.Context(), p.ID, first.Revision.ID, ids[tc.key])
					if err != nil || len(descriptorFacetValues(t, old, tc.descriptor)) != 2 {
						t.Fatal("malformed descriptor lost committed facets", err)
					}
				})
			}
		})
	}
}

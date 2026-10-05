package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestDatabaseIndependentFixtureProjection(t *testing.T) {
	t.Parallel()
	for _, dialect := range []string{"postgresql", "sqlite"} {
		for _, version := range []string{"v1", "v2"} {
			t.Run(dialect+"/"+version, func(t *testing.T) {
				r, _ := testRepo(t)
				out, ids := commitRelationalFixture(t, r, dialect, "v1")
				if version == "v2" {
					out, ids = databaseCommitV2(t, r, out, ids, dialect)
				}
				var oracle struct {
					Nodes map[string]struct{ SelectedColumnKeys map[string][]string }
					Edges map[string]struct {
						FromKey, ToKey string
						Facets         map[string]struct {
							ColumnPairs []struct{ FromColumnKey, ToColumnKey string }
						}
					}
					Cardinality map[string]map[string]struct {
						SourceCardinality, TargetCardinality Cardinality
						BasisKeys                            []string
					}
				}
				b, err := os.ReadFile(filepath.Join("testdata/relational/orders", dialect, version, "expected.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(b, &oracle, json.MatchCaseInsensitiveNames(true)); err != nil {
					t.Fatal(err)
				}
				for _, fk := range []string{"sql", "orm"} {
					in := DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: fk, RecordType: "tables"}
					page, err := r.QueryDatabase(t.Context(), out.Project.ID, in)
					if err != nil {
						t.Fatal(err)
					}
					if len(page.TableItems) != 4 || len(page.RelationshipItems) != 0 || page.SemanticHash != out.Revision.SemanticHash {
						t.Fatalf("bad table projection %+v", page)
					}
					for _, item := range page.TableItems {
						for key, want := range oracle.Nodes {
							if ids[key] == item.TableID && item.ColumnCount != int64(len(want.SelectedColumnKeys[fk])) {
								t.Fatalf("%s/%s count %d", key, fk, item.ColumnCount)
							}
						}
					}
					in.RecordType = "relationships"
					page, err = r.QueryDatabase(t.Context(), out.Project.ID, in)
					if err != nil {
						t.Fatal(err)
					}
					if len(page.RelationshipItems) != 3 || len(page.TableItems) != 0 {
						t.Fatalf("bad FK projection %+v", page)
					}
					for _, item := range page.RelationshipItems {
						if len(item.ColumnPairs) != 2 || len(item.EvidenceIDs) == 0 || len(item.SourceCardinality.Basis) == 0 || len(item.TargetCardinality.Basis) == 0 {
							t.Fatalf("missing pair/proof/basis %+v", item)
						}
						for key, facets := range oracle.Cardinality {
							if ids[key] != item.EdgeID {
								continue
							}
							want := facets[fk]
							claim := oracle.Edges[key]
							if item.ConstraintID != ids[claim.FromKey] || item.TargetTableID == nil || *item.TargetTableID != ids[claim.ToKey] {
								t.Fatalf("%s/%s endpoint identity lost %+v", key, fk, item)
							}
							for i, pair := range claim.Facets[fk].ColumnPairs {
								if item.ColumnPairs[i].FromColumnID != ids[pair.FromColumnKey] || item.ColumnPairs[i].ToColumnID != ids[pair.ToColumnKey] {
									t.Fatalf("%s/%s ordered pair %d lost %+v", key, fk, i, item.ColumnPairs)
								}
							}
							if !slices.Equal(item.EvidenceIDs, []string{ids["proof:"+version+":"+key+":"+fk]}) {
								t.Fatalf("selected proof lost for %s/%s: %v", key, fk, item.EvidenceIDs)
							}
							basis := strings.Join(append(slices.Clone(item.SourceCardinality.Basis), item.TargetCardinality.Basis...), " ")
							for _, key := range want.BasisKeys {
								if !strings.Contains(basis, ids[key]) {
									t.Fatalf("required oracle basis %s absent: %s", key, basis)
								}
							}
							if !reflect.DeepEqual(item.SourceCardinality.Min, want.SourceCardinality.Min) || !reflect.DeepEqual(item.SourceCardinality.Max, want.SourceCardinality.Max) || !reflect.DeepEqual(item.TargetCardinality.Min, want.TargetCardinality.Min) || !reflect.DeepEqual(item.TargetCardinality.Max, want.TargetCardinality.Max) {
								t.Fatalf("%s/%s cardinality %+v %+v; want %+v", key, fk, item.SourceCardinality, item.TargetCardinality, want)
							}
						}
					}
				}
			})
		}
	}
}

func TestDatabaseStrictPresenceAndPinnedCursor(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	base := DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "tables", Limit: 1}
	first, err := r.QueryDatabase(t.Context(), out.Project.ID, base)
	if err != nil || len(first.TableItems) != 1 || first.NextCursor == "" {
		t.Fatalf("first page %+v %v", first, err)
	}
	var emptySearch map[string]jsontext.Value
	_ = json.Unmarshal(relationalRaw(t, base), &emptySearch)
	emptySearch["search"] = relationalRaw(t, "")
	var explicitEmpty DatabaseQueryInput
	if err := json.Unmarshal(relationalRaw(t, emptySearch), &explicitEmpty); err != nil {
		t.Fatal(err)
	}
	withEmpty, err := r.QueryDatabase(t.Context(), out.Project.ID, explicitEmpty)
	if err != nil {
		t.Fatal(err)
	}
	omitted := base
	omitted.Cursor = withEmpty.NextCursor
	if _, err := r.QueryDatabase(t.Context(), out.Project.ID, omitted); err != nil {
		t.Fatal("equivalent empty/omitted search filter invalidated cursor", err)
	}
	for _, tc := range []struct{ name, key, value string }{
		{"zero limit", "limit", `0`}, {"null limit", "limit", `null`}, {"string limit", "limit", `"1"`}, {"exponent limit", "limit", `1e2`}, {"large limit", "limit", `501`},
		{"unknown member", "extra", `true`}, {"null facet", "facetKey", `null`}, {"null search", "search", `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]jsontext.Value
			_ = json.Unmarshal(relationalRaw(t, base), &m)
			m[tc.key] = jsontext.Value(tc.value)
			var in DatabaseQueryInput
			if err := json.Unmarshal(relationalRaw(t, m), &in); err == nil {
				t.Fatal("invalid wire query accepted")
			}
		})
	}
	for _, raw := range []string{
		`{"revisionId":"` + base.RevisionID + `","datastoreId":"` + base.DatastoreID + `","facetKey":"sql","recordType":"relationships","search":""}`,
		`{"revisionId":"` + base.RevisionID + `","datastoreId":"` + base.DatastoreID + `","facetKey":"sql","recordType":"tables","tableId":""}`,
	} {
		var in DatabaseQueryInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		// Presence survives an SDK's JSON roundtrip.
		var roundtrip DatabaseQueryInput
		if err := json.Unmarshal(relationalRaw(t, in), &roundtrip); err != nil {
			t.Fatal(err)
		}
		_, err := r.QueryDatabase(t.Context(), out.Project.ID, roundtrip)
		assertFault(t, err, "backend_invalid")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*DatabaseQueryInput)
	}{
		{"facet pin", func(in *DatabaseQueryInput) { in.FacetKey = "orm" }},
		{"record pin", func(in *DatabaseQueryInput) { in.RecordType = "relationships" }},
		{"search pin", func(in *DatabaseQueryInput) { in.Search = "orders" }},
		{"malformed cursor", func(in *DatabaseQueryInput) { in.Cursor = "!bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.Cursor = first.NextCursor
			tc.mutate(&in)
			_, err := r.QueryDatabase(t.Context(), out.Project.ID, in)
			assertFault(t, err, "backend_invalid")
		})
	}
	status, err := r.Import(t.Context(), out.Project.ID, out.SessionID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	p := &out.Project
	s, err := r.BeginImport(t.Context(), p.ID, partialRelationalInput(t, p, status.Session.RepositoryID, "v1", "head-change"))
	if err != nil {
		t.Fatal(err)
	}
	cs := relationalSelect(relationalFixture(t, p, s, "postgresql", "v1"), "table:orders", "proof:v1:table:orders:sql")
	mutateRelationalFacet(t, cs, "table:orders", "sql", func(m map[string]jsontext.Value) { m["qualifiedName"] = relationalRaw(t, "public.orders_new_head") })
	n := relationalCommand(cs, "table:orders").Node
	fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
	delete(fs, "orm")
	delete(fs, "migration")
	n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, fs)
	n.EvidenceKeys = []string{"proof:v1:table:orders:sql"}
	v, _ := stageRelational(t, r, p, s, cs, "advance")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	newHead, err := commitFixture(t, r, p, s, v, "advance-commit")
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{first.TableItems[0].TableID}
	in := base
	in.Limit = 2
	in.Cursor = first.NextCursor
	for in.Cursor != "" {
		page, err := r.QueryDatabase(t.Context(), p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if page.RevisionID != out.Revision.ID || page.SemanticHash != out.Revision.SemanticHash {
			t.Fatal("head substituted for pinned revision")
		}
		for _, item := range page.TableItems {
			if strings.Contains(item.QualifiedName, "new_head") {
				t.Fatal("pinned page read new head")
			}
			seen = append(seen, item.TableID)
		}
		in.Cursor = page.NextCursor
	}
	if len(seen) != 4 || len(slices.Compact(slices.Sorted(slices.Values(seen)))) != 4 {
		t.Fatalf("lost/duplicate table: %v", seen)
	}
	staleIn := base
	staleIn.RevisionID = newHead.Revision.ID
	staleIn.Limit = 0
	stalePage, err := r.QueryDatabase(t.Context(), p.ID, staleIn)
	if err != nil || stalePage.FacetStatus == "current" || len(stalePage.Limitations) == 0 || len(stalePage.TableItems) != 4 {
		t.Fatalf("stale projection hidden %+v %v", stalePage, err)
	}
	staleIn.RecordType = "relationships"
	staleFKs, err := r.QueryDatabase(t.Context(), p.ID, staleIn)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range staleFKs.RelationshipItems {
		if item.Status != "stale" || item.TargetCardinality.Max != nil || item.SourceCardinality.Max != nil {
			t.Fatalf("stale FK cardinality claimed %+v", item)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.QueryDatabase(ctx, p.ID, base); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func databaseVariant(t *testing.T, mutate func([]ImportCommand)) (*Repo, *ImportCommitResult, map[string]string) {
	t.Helper()
	return databaseCommandsVariant(t, func(cs []ImportCommand) []ImportCommand { mutate(cs); return cs })
}
func databaseCommandsVariant(t *testing.T, mutate func([]ImportCommand) []ImportCommand) (*Repo, *ImportCommitResult, map[string]string) {
	t.Helper()
	r, p, s, cs := relationalTestSession(t)
	cs = mutate(cs)
	v, ids := stageRelational(t, r, p, s, cs, "variant")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "variant-commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, out, ids
}

func databaseCommitV2(t *testing.T, r *Repo, first *ImportCommitResult, ids map[string]string, dialect string) (*ImportCommitResult, map[string]string) {
	t.Helper()
	status, err := r.Import(t.Context(), first.Project.ID, first.SessionID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	p := &first.Project
	s, err := r.BeginImport(t.Context(), p.ID, relationalReconcileInput(t, p, status.Session.RepositoryID, dialect, "v2", "v2-database"))
	if err != nil {
		t.Fatal(err)
	}
	mapping := []ImportCommand{{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "column:orders:status", ToExternalKey: "column:orders:state", ExpectedID: ids["column:orders:status"], Reason: "Source explicitly renames column", EvidenceKeys: []string{"proof:v2:column:orders:state:sql"}}}}
	h, err := ImportBatchHash(mapping)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "mapping", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: h, Commands: mapping})
	if err != nil {
		t.Fatal(err)
	}
	s.Version = b.AcceptedVersion
	cs := relationalFixtureWithHistory(t, p, s, dialect, "v2", ids)
	for _, key := range []string{"column:orders:legacy_note", "index:orders:legacy_note_idx"} {
		cs = append(cs, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: key, ExpectedID: ids[key], Reason: "Source proves deletion"}}, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "edge", ExternalKey: "contains:" + key, ExpectedID: ids["contains:"+key], Reason: "Source proves containment deletion"}})
	}
	v, nextIDs := stageRelational(t, r, p, s, cs, "v2")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "v2-commit")
	if err != nil {
		t.Fatal(err)
	}
	for key, id := range nextIDs {
		ids[key] = id
	}
	return out, ids
}

func TestDatabaseSelfCycleAndJoinPreservePhysicalEdges(t *testing.T) {
	for _, mode := range []string{"self", "cycle", "join"} {
		t.Run(mode, func(t *testing.T) {
			r, out, ids := databaseCommandsVariant(t, func(cs []ImportCommand) []ImportCommand {
				if mode == "join" {
					keys := []string{"constraint:order_items:order_fk", "contains:constraint:order_items:order_fk", "references:order_items:order_fk"}
					for _, key := range slices.Clone(keys) {
						for _, fk := range []string{"sql", "orm"} {
							keys = append(keys, "proof:v1:"+key+":"+fk)
						}
					}
					clones := relationalSelect(cs, keys...)
					raw := strings.NewReplacer("constraint:order_items:order_fk", "constraint:order_items:user_fk", "references:order_items:order_fk", "references:order_items:user_fk").Replace(string(relationalRaw(t, clones)))
					if err := json.Unmarshal([]byte(raw), &clones); err != nil {
						t.Fatal(err)
					}
					relationalCommand(clones, "constraint:order_items:user_fk").Node.Name = "order_items_user_fk"
					relationalCommand(clones, "references:order_items:user_fk").Edge.ToKey = "table:users"
					for _, fk := range []string{"sql", "orm"} {
						mutateRelationalFacet(t, clones, "references:order_items:user_fk", fk, func(m map[string]jsontext.Value) {
							m["columnPairs"] = jsontext.Value(`[{"fromColumnKey":"column:order_items:tenant_id","toColumnKey":"column:users:tenant_id"},{"fromColumnKey":"column:order_items:order_id","toColumnKey":"column:users:id"}]`)
						})
					}
					return append(cs, clones...)
				}
				edge := relationalCommand(cs, "references:orders:user_fk").Edge
				target, second := "orders", "id"
				if mode == "cycle" {
					target, second = "order_items", "order_id"
				}
				edge.ToKey = "table:" + target
				for _, fk := range []string{"sql", "orm"} {
					mutateRelationalFacet(t, cs, edge.ExternalKey, fk, func(m map[string]jsontext.Value) {
						m["columnPairs"] = relationalRaw(t, []map[string]string{{"fromColumnKey": "column:orders:tenant_id", "toColumnKey": "column:" + target + ":tenant_id"}, {"fromColumnKey": "column:orders:user_id", "toColumnKey": "column:" + target + ":" + second}})
					})
				}
				return cs
			})
			page, err := r.QueryDatabase(t.Context(), out.Project.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
			if err != nil {
				t.Fatal(err)
			}
			want := 3
			if mode == "join" {
				want = 4
			}
			if len(page.RelationshipItems) != want {
				t.Fatalf("invented/lost physical FK %+v", page)
			}
			item := databaseRelationship(t, r, out, ids, "references:orders:user_fk")
			if mode == "self" && (item.TargetTableID == nil || *item.TargetTableID != item.SourceTableID) {
				t.Fatal("self direction lost")
			}
			if mode == "cycle" && (item.TargetTableID == nil || *item.TargetTableID != ids["table:order_items"] || item.SourceTableID != ids["table:orders"]) {
				t.Fatal("cycle direction lost")
			}
			if mode == "join" {
				count := 0
				for _, item := range page.RelationshipItems {
					if item.SourceTableID == ids["table:order_items"] {
						count++
					}
				}
				if count != 2 {
					t.Fatal("join collapsed into synthetic N:M")
				}
			}
		})
	}
}

func TestDatabaseMissingTargetProof(t *testing.T) {
	for _, key := range []string{"table:users"} {
		t.Run(key, func(t *testing.T) {
			r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
				n := relationalCommand(cs, key).Node
				fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
				delete(fs, "sql")
				n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, fs)
				relationalCommand(cs, "proof:v1:"+key+":sql").Evidence.PropertyPath = new("/kind")
			})
			page, err := r.QueryDatabase(t.Context(), out.Project.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
			if err != nil || page.FacetStatus != "unknown" || len(page.Limitations) == 0 {
				t.Fatalf("missing selected proof hidden %+v %v", page, err)
			}
			found := false
			for _, item := range page.RelationshipItems {
				if item.EdgeID != ids["references:orders:user_fk"] {
					continue
				}
				found = true
				if key == "table:users" && (item.TargetTableID == nil || *item.TargetTableID != ids[key] || item.Status != "unresolved" || item.TargetCardinality.Max != nil || len(item.ColumnPairs) != 2) {
					t.Fatalf("missing target claim erased/confirmed %+v", item)
				}
			}
			if !found {
				t.Fatal("incorrect selected FK membership")
			}
		})
	}
}
func databaseRelationship(t *testing.T, r *Repo, out *ImportCommitResult, ids map[string]string, key string) RelationshipItem {
	t.Helper()
	page, err := r.QueryDatabase(t.Context(), out.Project.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.RelationshipItems {
		if item.EdgeID == ids[key] {
			return item
		}
	}
	t.Fatalf("relationship absent %s: %+v", key, page)
	return RelationshipItem{}
}

func TestDatabaseCardinalityIndependentBounds(t *testing.T) {
	for _, tc := range []struct {
		name, field, raw string
		inferred         bool
	}{
		{"deferred", "deferrable", `{"status":"known","value":true}`, false},
		{"unknown deferral", "deferrable", `{"status":"unknown","reason":"unavailable"}`, false},
		{"unknown nullable", "nullable", `{"status":"unknown","reason":"unavailable"}`, false},
		{"inferred FK", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
				if tc.inferred {
					relationalCommand(cs, "proof:v1:references:orders:user_fk:sql").Evidence.Status = "inferred"
					return
				}
				key := "constraint:orders:user_fk"
				if tc.field == "nullable" {
					key = "column:orders:user_id"
				}
				mutateRelationalFacet(t, cs, key, "sql", func(m map[string]jsontext.Value) { m[tc.field] = jsontext.Value(tc.raw) })
			})
			item := databaseRelationship(t, r, out, ids, "references:orders:user_fk")
			if item.TargetCardinality.Min != nil {
				t.Fatalf("unsupported minimum %+v", item)
			}
			if tc.inferred {
				if item.Status != "inferred" || item.SourceCardinality.Min != nil || item.SourceCardinality.Max != nil || item.TargetCardinality.Max != nil {
					t.Fatalf("inferred confirmation %+v", item)
				}
			} else if item.TargetCardinality.Max == nil || *item.TargetCardinality.Max != "1" || item.SourceCardinality.Max == nil || *item.SourceCardinality.Max != "many" || item.SourceCardinality.Min == nil || *item.SourceCardinality.Min != 0 {
				t.Fatalf("independent key bounds erased %+v", item)
			}
		})
	}
}

func TestDatabaseUniqueIndexPredicateAndExpression(t *testing.T) {
	for _, tc := range []struct {
		name, predicate string
		expression      bool
		want            *string
	}{
		{"global columns", `{"status":"known","value":null}`, false, new("1")},
		{"partial predicate", `{"status":"known","value":"order_id > 0"}`, false, new("many")},
		{"unknown predicate", `{"status":"unknown","reason":"dynamic"}`, false, nil},
		{"expression key", `{"status":"known","value":null}`, true, new("many")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
				n := relationalCommand(cs, "constraint:payments:order_unique").Node
				fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
				for fk, raw := range fs {
					m, _ := relationalObject(raw)
					var cols []string
					_ = json.Unmarshal(m["columnKeys"], &cols)
					for _, field := range []string{"constraintKind", "columnKeys", "expression", "deferrable", "initiallyDeferred"} {
						delete(m, field)
					}
					terms := []map[string]any{}
					for _, key := range cols {
						terms = append(terms, map[string]any{"columnKey": key, "direction": "asc", "nulls": "unknown"})
					}
					if tc.expression {
						delete(terms[0], "columnKey")
						terms[0]["expression"] = "tenant_id + 1"
					}
					m["terms"] = relationalRaw(t, terms)
					m["unique"] = relationalRaw(t, known(true))
					m["predicate"] = jsontext.Value(tc.predicate)
					m["method"] = relationalRaw(t, known(nil))
					fs[fk] = relationalRaw(t, m)
				}
				n.Kind = "index"
				n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, fs)
			})
			item := databaseRelationship(t, r, out, ids, "references:payments:order_fk")
			if !reflect.DeepEqual(item.SourceCardinality.Max, tc.want) || item.TargetCardinality.Max == nil || *item.TargetCardinality.Max != "1" {
				t.Fatalf("index asserted incorrect uniqueness %+v want %v", item, tc.want)
			}
		})
	}
}

func TestDatabaseSelectedMembershipMissingProofAndEndpoints(t *testing.T) {
	r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
		n := relationalCommand(cs, "column:orders:user_id").Node
		fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
		delete(fs, "sql")
		n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, fs)
		relationalCommand(cs, "proof:v1:column:orders:user_id:sql").Evidence.PropertyPath = new("/kind")
	})
	in := DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "tables"}
	page, err := r.QueryDatabase(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range page.TableItems {
		if table.TableID == ids["table:orders"] && table.ColumnCount != 6 {
			t.Fatalf("counted another facet's column %+v", table)
		}
	}
	if page.FacetStatus != "unknown" || len(page.Limitations) == 0 {
		t.Fatal("missing selected proof presented complete")
	}
	item := databaseRelationship(t, r, out, ids, "references:orders:user_fk")
	if item.Status != "unresolved" || item.TargetTableID == nil || *item.TargetTableID != ids["table:users"] || len(item.ColumnPairs) != 2 || item.TargetCardinality.Max != nil {
		t.Fatalf("missing selected column lost claim or invented cardinality %+v", item)
	}
	in.FacetKey = "missing:valid"
	empty, err := r.QueryDatabase(t.Context(), out.Project.ID, in)
	if err != nil || len(empty.TableItems) != 0 || empty.FacetStatus != "unknown" || len(empty.Limitations) == 0 {
		t.Fatalf("missing selector should be limited empty %+v %v", empty, err)
	}
	in.FacetKey = "orm"
	in.RecordType = "relationships"
	in.TableID = ids["table:users"]
	filtered, err := r.QueryDatabase(t.Context(), out.Project.ID, in)
	if err != nil || len(filtered.RelationshipItems) != 1 || filtered.RelationshipItems[0].TargetTableID == nil {
		t.Fatalf("endpoint filter lost off-page target %+v %v", filtered, err)
	}
	in.TableID = ids["column:orders:user_id"]
	_, err = r.QueryDatabase(t.Context(), out.Project.ID, in)
	assertFault(t, err, "backend_invalid")
	in.TableID = "00000000-0000-4000-8000-000000000001"
	_, err = r.QueryDatabase(t.Context(), out.Project.ID, in)
	assertFault(t, err, "backend_not_found")
	_, err = r.Node(t.Context(), out.Project.ID, out.Revision.ID, *item.TargetTableID)
	if err != nil {
		t.Fatal("off-page endpoint not inspectable", err)
	}
}

func TestDatabaseKnownNullableCompositeMinimumSurvivesOtherUnknown(t *testing.T) {
	r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
		mutateRelationalFacet(t, cs, "column:payments:tenant_id", "sql", func(m map[string]jsontext.Value) {
			m["nullable"] = relationalRaw(t, unknown("Source cannot establish this column's nullability"))
		})
	})
	item := databaseRelationship(t, r, out, ids, "references:payments:order_fk")
	if item.TargetCardinality.Min == nil || *item.TargetCardinality.Min != 0 || item.TargetCardinality.Max == nil || *item.TargetCardinality.Max != "1" {
		t.Fatalf("known nullable MATCH SIMPLE bound erased by unrelated unknown %+v", item)
	}
}

func TestDatabaseInferredProofLimitsWholeSelectedProjection(t *testing.T) {
	r, out, ids := databaseVariant(t, func(cs []ImportCommand) {
		relationalCommand(cs, "proof:v1:references:orders:user_fk:sql").Evidence.Status = "inferred"
	})
	page, err := r.QueryDatabase(t.Context(), out.Project.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
	if err != nil || page.FacetStatus != "unknown" || len(page.Limitations) == 0 {
		t.Fatalf("inferred proof advertised complete selected facet %+v %v", page, err)
	}
}

// Matching FK constraint facets are mandatory on legal imports. This test
// deliberately damages only an isolated test store to exercise defensive reads.
func TestDatabaseDefensiveStoredConstraintProofMissing(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range state.Nodes {
		if n.ID != ids["constraint:orders:user_fk"] {
			continue
		}
		fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
		delete(fs, "sql")
		n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, fs)
		if _, err := r.db.W.ExecContext(t.Context(), `UPDATE backend_graph_records SET document=? WHERE revision_id=? AND record_type='node' AND id=?`, string(relationalRaw(t, n)), out.Revision.ID, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	page, err := r.QueryDatabase(t.Context(), out.Project.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "relationships"})
	if err != nil || page.FacetStatus != "unknown" || len(page.RelationshipItems) != 2 || len(page.Limitations) == 0 {
		t.Fatalf("defensive missing constraint proof hidden %+v %v", page, err)
	}
}

func TestDatabaseSchema1AndMissingDescriptorReadContracts(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	in := relationalFixtureInput(t, p, "postgresql", "v1")
	in.Profile = GraphProfile
	in.Manifest.Provider.Profiles = []string{GraphProfile}
	in.IdempotencyKey = "foundation-only"
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	cs := relationalFixture(t, p, s, "postgresql", "v1")
	n := *relationalCommand(cs, "database:orders").Node
	n.Attributes = map[string]jsontext.Value{"technology": jsontext.Value(`"postgresql"`)}
	proof := *relationalCommand(cs, "proof:v1:database:orders:sql").Evidence
	proof.PropertyPath = new("/kind")
	v, ids := stageRelational(t, r, p, s, []ImportCommand{{Op: "upsert_node", Node: &n}, {Op: "upsert_evidence", Evidence: &proof}}, "foundation")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "foundation-commit")
	if err != nil {
		t.Fatal(err)
	}
	read, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["database:orders"])
	if err != nil {
		t.Fatal(err)
	}
	if read.FacetComparison != nil || strings.Contains(string(relationalRaw(t, read)), "facetComparison") {
		t.Fatal("schema1 node shape changed")
	}
	page, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
	if err != nil || strings.Contains(string(relationalRaw(t, page)), "facetComparison") {
		t.Fatal("schema1 graph shape changed", err)
	}
	_, err = r.QueryDatabase(t.Context(), p.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: read.ID, FacetKey: "sql", RecordType: "tables"})
	assertFault(t, err, "backend_relational_unavailable")
	r, out, ids = databaseVariant(t, func(cs []ImportCommand) {
		relationalCommand(cs, "database:orders").Node.Attributes = map[string]jsontext.Value{"technology": jsontext.Value(`"postgresql"`)}
		relationalCommand(cs, "proof:v1:database:orders:sql").Evidence.PropertyPath = new("/kind")
	})
	_, err = r.QueryDatabase(t.Context(), out.Project.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "tables"})
	assertFault(t, err, "backend_relational_unavailable")
}

func TestDatabaseDependencyStaleExplicitProofIsNotInferred(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	first, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	status, err := r.Import(t.Context(), first.Project.ID, first.SessionID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	p := &first.Project
	s, err := r.BeginImport(t.Context(), p.ID, relationalReconcileInput(t, p, status.Session.RepositoryID, "postgresql", "v1", "stale-containment"))
	if err != nil {
		t.Fatal(err)
	}
	cs := relationalFixture(t, p, s, "postgresql", "v1")
	cs = slices.DeleteFunc(cs, func(c ImportCommand) bool {
		return c.Edge != nil && c.Edge.ExternalKey == "contains:constraint:orders:user_fk" || c.Evidence != nil && c.Evidence.SubjectKey == "contains:constraint:orders:user_fk"
	})
	v, _ := stageRelational(t, r, p, s, cs, "stale-containment")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "stale-containment-commit")
	if err != nil {
		t.Fatal(err)
	}
	item := databaseRelationship(t, r, out, ids, "references:orders:user_fk")
	if item.Status != "stale" || item.TargetCardinality.Max != nil || item.SourceCardinality.Max != nil {
		t.Fatalf("explicit stale dependency relabelled inferred/confirmed %+v", item)
	}
}

package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func relationalCommand(cs []ImportCommand, key string) *ImportCommand {
	for i := range cs {
		_, k, _ := commandAddress(cs[i])
		if k == key {
			return &cs[i]
		}
	}
	panic("fixture key missing: " + key)
}
func mutateRelationalFacet(t *testing.T, cs []ImportCommand, key, fk string, fn func(map[string]jsontext.Value)) {
	t.Helper()
	c := relationalCommand(cs, key)
	kind := ""
	var attrs map[string]jsontext.Value
	if c.Node != nil {
		kind, attrs = c.Node.Kind, c.Node.Attributes
	} else {
		kind, attrs = c.Edge.Kind, c.Edge.Attributes
	}
	fs, _, err := relationalFacetObject(kind, attrs)
	if err != nil {
		t.Fatal(err)
	}
	m, err := relationalObject(fs[fk])
	if err != nil {
		t.Fatal(err)
	}
	fn(m)
	fs[fk] = relationalRaw(t, m)
	updated := replaceRelationalFacets(kind, attrs, fs)
	if c.Node != nil {
		c.Node.Attributes = updated
	} else {
		c.Edge.Attributes = updated
	}
}
func relationalTestSession(t *testing.T) (*Repo, *Project, *ImportSession, []ImportCommand) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, relationalFixtureInput(t, p, "postgresql", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	return r, p, s, relationalFixture(t, p, s, "postgresql", "v1")
}
func TestRelationalStrictFacetsAtomic(t *testing.T) {
	cases := []struct {
		name, key, facet, field, raw string
		limit                        bool
	}{
		{name: "unknown nested member", key: "column:orders:total", field: "surprise", raw: `true`},
		{name: "unknown scalar has value", key: "column:orders:total", field: "nullable", raw: `{"status":"unknown","reason":"missing","value":false}`},
		{name: "known nullable wrong type", key: "column:orders:total", field: "nullable", raw: `{"status":"known","value":"false"}`},
		{name: "nullable cannot null", key: "column:orders:total", field: "nullable", raw: `{"status":"known","value":null}`},
		{name: "ordinal zero", key: "column:orders:total", field: "ordinal", raw: `{"status":"known","value":0}`},
		{name: "ordinal overflow", key: "column:orders:total", field: "ordinal", raw: `{"status":"known","value":9223372036854775808}`},
		{name: "ordinal exponent", key: "column:orders:total", field: "ordinal", raw: `{"status":"known","value":1e3}`},
		{name: "facet proof missing", key: "column:orders:total", field: "evidenceKeys", raw: `[]`},
		{name: "null proof array", key: "column:orders:total", field: "evidenceKeys", raw: `null`},
		{name: "server freshness", key: "column:orders:total", field: "freshness", raw: `{"status":"current"}`},
		{name: "missing nullable", key: "column:orders:total", field: "nullable"},
		{name: "duplicate ordered key", key: "constraint:orders:user_fk", field: "columnKeys", raw: `["column:orders:tenant_id","column:orders:tenant_id"]`},
		{name: "unknown type family", key: "column:orders:total", field: "typeFamily", raw: `{"status":"known","value":"money"}`},
		{name: "unsupported migration lacks gaps", key: "migration:002_unsupported", facet: "migration", field: "gaps", raw: `[]`},
		{name: "migration negative order", key: "migration:001_initial", facet: "migration", field: "order", raw: `{"status":"known","value":-1}`},
		{name: "empty source-only reason", key: "migration:001_initial", facet: "migration", field: "changes", raw: `[{"operation":"create","description":"scratch","target":{"kind":"source_only","externalKey":"scratch","expectedKind":"column","qualifiedName":"orders.scratch","reason":""}}]`},
		{name: "native byte bound", key: "column:orders:total", field: "nativeType", raw: fmt.Sprintf(`{"status":"known","value":%q}`, strings.Repeat("雪", MaxRelationalNativeBytes/3+1)), limit: true},
	}
	r, p, s, original := relationalTestSession(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := relationalRaw(t, original)
			var cs []ImportCommand
			_ = json.Unmarshal(raw, &cs)
			fk := tc.facet
			if fk == "" {
				fk = "sql"
			}
			mutateRelationalFacet(t, cs, tc.key, fk, func(m map[string]jsontext.Value) {
				if tc.raw == "" {
					delete(m, tc.field)
				} else {
					m[tc.field] = jsontext.Value(tc.raw)
				}
			})
			h, err := ImportBatchHash(cs)
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "bad", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: h, Commands: cs})
			code := "backend_import_invalid"
			if tc.limit {
				code = "backend_import_limit"
			}
			assertFault(t, err, code)
			status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
			if err != nil || status.Session.Version != 1 || status.Session.AcceptedBatchCount != 0 {
				t.Fatalf("invalid batch published: %+v %v", status, err)
			}
		})
	}
}
func TestRelationalNestedGraphReferences(t *testing.T) {
	for _, name := range []string{"missing column", "wrong kind", "wrong parent", "fk ordering", "pair wrong parent", "dialect disagreement", "duplicate column name", "duplicate ordinal", "duplicate index name", "migration cycle", "historical foreign", "hierarchy bypass", "persisted property path"} {
		t.Run(name, func(t *testing.T) {
			r, p, s, cs := relationalTestSession(t)
			switch name {
			case "missing column":
				mutateRelationalFacet(t, cs, "constraint:orders:user_fk", "sql", func(m map[string]jsontext.Value) {
					m["columnKeys"] = jsontext.Value(`["column:missing","column:orders:user_id"]`)
				})
			case "wrong kind":
				mutateRelationalFacet(t, cs, "constraint:orders:user_fk", "sql", func(m map[string]jsontext.Value) {
					m["columnKeys"] = jsontext.Value(`["table:orders","column:orders:user_id"]`)
				})
			case "wrong parent":
				mutateRelationalFacet(t, cs, "constraint:orders:user_fk", "sql", func(m map[string]jsontext.Value) {
					m["columnKeys"] = jsontext.Value(`["column:users:tenant_id","column:orders:user_id"]`)
				})
			case "fk ordering":
				mutateRelationalFacet(t, cs, "constraint:orders:user_fk", "sql", func(m map[string]jsontext.Value) {
					m["columnKeys"] = jsontext.Value(`["column:orders:user_id","column:orders:tenant_id"]`)
				})
			case "pair wrong parent":
				mutateRelationalFacet(t, cs, "references:orders:user_fk", "sql", func(m map[string]jsontext.Value) {
					m["columnPairs"] = jsontext.Value(`[{"fromColumnKey":"column:orders:tenant_id","toColumnKey":"column:payments:tenant_id"},{"fromColumnKey":"column:orders:user_id","toColumnKey":"column:users:id"}]`)
				})
			case "dialect disagreement":
				mutateRelationalFacet(t, cs, "column:orders:total", "sql", func(m map[string]jsontext.Value) { m["dialect"] = jsontext.Value(`"sqlite"`) })
			case "duplicate column name":
				relationalCommand(cs, "column:orders:total").Node.Name = "id"
			case "duplicate ordinal":
				mutateRelationalFacet(t, cs, "column:orders:total", "sql", func(m map[string]jsontext.Value) { m["ordinal"] = jsontext.Value(`{"status":"known","value":2}`) })
			case "duplicate index name":
				relationalCommand(cs, "index:orders:legacy_note_idx").Node.Name = relationalCommand(cs, "index:orders:state_idx").Node.Name
			case "migration cycle":
				mutateRelationalFacet(t, cs, "migration:001_initial", "migration", func(m map[string]jsontext.Value) { m["parentKeys"] = jsontext.Value(`["migration:002_unsupported"]`) })
			case "historical foreign":
				other := createProject(t, r, "other")
				mutateRelationalFacet(t, cs, "migration:001_initial", "migration", func(m map[string]jsontext.Value) {
					m["changes"] = relationalRaw(t, []any{map[string]any{"operation": "create", "description": "old", "target": map[string]any{"kind": "historical", "revisionId": other.CurrentRevisionID, "objectId": other.ID}}})
				})
			case "hierarchy bypass":
				relationalCommand(cs, "table:orders").Node.ParentKey = new("database:orders")
				relationalCommand(cs, "contains:table:orders").Edge.FromKey = "database:orders"
			case "persisted property path":
				relationalCommand(cs, "proof:v1:constraint:orders:user_fk:sql").Evidence.PropertyPath = new("/attributes/facets/sql/columnKeys/0")
			}
			v, _ := stageRelational(t, r, p, s, cs, "bad-graph")
			if v.State != "needs_resolution" || v.CandidateHash != nil || len(v.Diagnostics) == 0 {
				t.Fatalf("bad graph committed: %+v", v)
			}
		})
	}
}
func TestRelationalArrayAndFacetBounds(t *testing.T) {
	r, p, s, cs := relationalTestSession(t)
	base := relationalCommand(cs, "column:orders:total").Node
	fs, _, _ := relationalFacetObject(base.Kind, base.Attributes)
	for i := 0; i < MaxRelationalFacets; i++ {
		fs[fmt.Sprintf("extra%d", i)] = fs["sql"]
	}
	base.Attributes = replaceRelationalFacets(base.Kind, base.Attributes, fs)
	h, _ := ImportBatchHash(cs)
	_, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "many-facets", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: h, Commands: cs})
	assertFault(t, err, "backend_import_limit")
}
func TestRelationalStrictDuplicateJSON(t *testing.T) {
	_, _, _, cs := relationalTestSession(t)
	a := relationalCommand(cs, "column:orders:total").Node.Attributes
	fs, _, _ := relationalFacetObject("column", a)
	raw := fs["sql"]
	raw = append(jsontext.Value(`{"dialect":"postgresql",`), raw[1:]...)
	fs["sql"] = raw
	a["facets"] = jsontext.Value(`{"sql":` + string(raw) + `}`)
	if err := validateRelationalAttributes("column", a, false, false); err == nil {
		t.Fatal("duplicate JSON object members accepted")
	}
}

func relationalReconcileInput(t *testing.T, p *Project, repositoryID, dialect, version, key string) BeginImportInput {
	in := relationalFixtureInput(t, p, dialect, version)
	in.Mode = "reconcile"
	in.RepositoryID = new(repositoryID)
	in.GraphScope = &GraphScope{Profile: RelationalProfile, Status: "complete", Gaps: []string{}}
	in.IdempotencyKey = key
	return in
}
func relationalSelect(cs []ImportCommand, keys ...string) []ImportCommand {
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	out := []ImportCommand{}
	for _, c := range cs {
		_, key, _ := commandAddress(c)
		if wanted[key] {
			out = append(out, c)
		}
	}
	return out
}
func TestRelationalRetainedFacetProofCannotBeOverwritten(t *testing.T) {
	r, p, s, cs := relationalTestSession(t)
	key := "column:orders:status"
	sqlProof := "proof:v1:" + key + ":sql"
	ormProof := "proof:v1:" + key + ":orm"
	mutateRelationalFacet(t, cs, key, "orm", func(m map[string]jsontext.Value) { m["evidenceKeys"] = relationalRaw(t, []string{sqlProof}) })
	relationalCommand(cs, key).Node.EvidenceKeys = []string{sqlProof}
	cs = slices.DeleteFunc(cs, func(c ImportCommand) bool { return c.Evidence != nil && c.Evidence.ExternalKey == ormProof })
	relationalCommand(cs, sqlProof).Evidence.PropertyPath = new("/attributes/facets")
	v, ids := stageRelational(t, r, p, s, cs, "base")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	first, err := commitFixture(t, r, p, s, v, "base-commit")
	if err != nil {
		t.Fatal(err)
	}
	p = &first.Project
	in := relationalReconcileInput(t, p, s.RepositoryID, "postgresql", "v2", "repeat")
	in.GraphScope.Status = "partial"
	in.GraphScope.Gaps = []string{"SQL facet only reobserved"}
	for i := range in.Inventory {
		if in.Inventory[i].Category == "datastores" {
			in.Inventory[i].Status = "partial"
			in.Inventory[i].KnownCount = 0
			in.Inventory[i].Denominator = nil
			in.Inventory[i].Gaps = []string{"Only one column was reobserved"}
		}
	}
	next, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	// Reassert only SQL with a different snapshot/body under the acknowledged proof key.
	n := *relationalCommand(cs, key).Node
	fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
	delete(fs, "orm")
	n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, fs)
	proof := *relationalCommand(cs, sqlProof).Evidence
	proof.Source.SnapshotID = next.SnapshotID
	proof.Source.File = "postgresql/v2/schema.sql"
	proof.Source.ContentHash = in.Manifest.Snapshot.Files[0].ContentHash
	proof.Snippet = new("state TEXT NOT NULL DEFAULT 'draft'")
	// Ordinal remains the same; the scalar is unchanged while its proof changes.
	partial := []ImportCommand{{Op: "upsert_node", Node: &n}, {Op: "upsert_evidence", Evidence: &proof}}
	bad, _ := stageRelational(t, r, p, next, partial, "collision")
	if bad.CandidateHash != nil || !slices.ContainsFunc(bad.Diagnostics, func(d ImportDiagnostic) bool { return d.Code == "backend_facet_evidence_conflict" }) {
		t.Fatalf("proof collision not blocked: %+v", bad)
	}
	current, err := r.Import(t.Context(), p.ID, next.ID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	next.Version = current.Session.Version
	proof.ExternalKey = "proof:refreshed-sql"
	n.EvidenceKeys = []string{proof.ExternalKey}
	fs, _, _ = relationalFacetObject(n.Kind, n.Attributes)
	m, _ := relationalObject(fs["sql"])
	m["evidenceKeys"] = relationalRaw(t, []string{proof.ExternalKey})
	fs["sql"] = relationalRaw(t, m)
	n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, fs)
	repair := []ImportCommand{{Op: "remove", Remove: &ImportRemove{RecordType: "evidence", ExternalKey: sqlProof}}, {Op: "upsert_node", Node: &n}, {Op: "upsert_evidence", Evidence: &proof}}
	good, newIDs := stageRelational(t, r, p, next, repair, "repair")
	if good.State != "ready" {
		t.Fatal(good.Diagnostics)
	}
	out, err := commitFixture(t, r, p, next, good, "repair-commit")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, snapshot, hash string }{{ids[sqlProof], s.SnapshotID, s.Manifest.Snapshot.Files[0].ContentHash}, {newIDs[proof.ExternalKey], next.SnapshotID, proof.Source.ContentHash}} {
		page, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{SubjectID: ids[key]})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range page.Items {
			if e.ID == tc.id {
				found = true
				if e.Freshness == nil || e.Freshness.ConfirmedSnapshotID != e.Source.SnapshotID {
					t.Fatalf("proof freshness rebased without source observation: %+v", e)
				}
				if e.Source.SnapshotID != tc.snapshot || e.Source.ContentHash != tc.hash {
					t.Fatalf("proof changed: %+v", e)
				}
			}
		}
		if !found {
			t.Fatalf("proof %s missing", tc.id)
		}
	}
	currentNode, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids[key])
	if err != nil {
		t.Fatal(err)
	}
	currentFacets, _, _ := relationalFacetObject(currentNode.Kind, currentNode.Attributes)
	sqlFacet, _ := decodeRelationalFacet(currentNode.Kind, currentFacets["sql"], true)
	ormFacet, _ := decodeRelationalFacet(currentNode.Kind, currentFacets["orm"], true)
	if sqlFacet.Freshness.Status != "current" || ormFacet.Freshness.Status != "stale" || ormFacet.SourceSnapshotID != s.SnapshotID {
		t.Fatal("facet current/stale provenance lost")
	}
	old, err := r.Node(t.Context(), p.ID, first.Revision.ID, ids[key])
	if err != nil || old.ID != ids[key] {
		t.Fatalf("old object gone %v", err)
	}
}
func partialRelationalInput(t *testing.T, p *Project, repository, version, key string) BeginImportInput {
	in := relationalReconcileInput(t, p, repository, "postgresql", version, key)
	in.GraphScope.Status = "partial"
	in.GraphScope.Gaps = []string{"Only selected subjects reobserved"}
	for i := range in.Inventory {
		if in.Inventory[i].Category == "datastores" {
			in.Inventory[i].Status = "partial"
			in.Inventory[i].KnownCount = 0
			in.Inventory[i].Denominator = nil
			in.Inventory[i].Gaps = []string{"Datastore not reobserved"}
		}
	}
	return in
}
func TestRelationalFacetEndpointConflict(t *testing.T) {
	r, p, s, cs := relationalTestSession(t)
	v, ids := stageRelational(t, r, p, s, cs, "base")
	out, err := commitFixture(t, r, p, s, v, "base-commit")
	if err != nil {
		t.Fatal(err)
	}
	p = &out.Project
	next, err := r.BeginImport(t.Context(), p.ID, partialRelationalInput(t, p, s.RepositoryID, "v1", "retarget"))
	if err != nil {
		t.Fatal(err)
	}
	refreshed := relationalFixture(t, p, next, "postgresql", "v1")
	edgeKey := "references:orders:user_fk"
	proofKey := "proof:v1:" + edgeKey + ":sql"
	partial := relationalSelect(refreshed, edgeKey, proofKey)
	edge := relationalCommand(partial, edgeKey).Edge
	fs, _, _ := relationalFacetObject(edge.Kind, edge.Attributes)
	delete(fs, "orm")
	edge.Attributes = replaceRelationalFacets(edge.Kind, edge.Attributes, fs)
	edge.EvidenceKeys = []string{proofKey}
	edge.ToKey = "table:payments"
	mutateRelationalFacet(t, partial, edgeKey, "sql", func(m map[string]jsontext.Value) {
		m["columnPairs"] = jsontext.Value(`[{"fromColumnKey":"column:orders:tenant_id","toColumnKey":"column:payments:tenant_id"},{"fromColumnKey":"column:orders:user_id","toColumnKey":"column:payments:id"}]`)
	})
	bad, ack := stageRelational(t, r, p, next, partial, "retarget-one")
	if ack[edgeKey] != ids[edgeKey] || bad.CandidateHash != nil || !slices.ContainsFunc(bad.Diagnostics, func(d ImportDiagnostic) bool { return d.Code == "backend_facet_endpoint_conflict" }) {
		t.Fatalf("retarget changed ack or lacked conflict %+v", bad)
	}
	current, _ := r.Import(t.Context(), p.ID, next.ID, ListInput{})
	next.Version = current.Session.Version
	refreshed = relationalFixture(t, p, next, "postgresql", "v1")
	all := relationalSelect(refreshed, edgeKey, proofKey, "proof:v1:"+edgeKey+":orm")
	allEdge := relationalCommand(all, edgeKey).Edge
	allEdge.ToKey = "table:payments"
	for _, fk := range []string{"sql", "orm"} {
		mutateRelationalFacet(t, all, edgeKey, fk, func(m map[string]jsontext.Value) {
			m["columnPairs"] = jsontext.Value(`[{"fromColumnKey":"column:orders:tenant_id","toColumnKey":"column:payments:tenant_id"},{"fromColumnKey":"column:orders:user_id","toColumnKey":"column:payments:id"}]`)
		})
	}
	good, ack := stageRelational(t, r, p, next, all, "retarget-all")
	if good.State != "ready" || ack[edgeKey] != ids[edgeKey] {
		t.Fatalf("all-facet retarget %+v", good.Diagnostics)
	}
	_, err = commitFixture(t, r, p, next, good, "retarget-commit")
	if err != nil {
		t.Fatal(err)
	}
}
func TestRelationalMappedRenameNestedDeletionHistory(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, relationalFixtureInput(t, p, dialect, "v1"))
			if err != nil {
				t.Fatal(err)
			}
			v, ids := stageRelational(t, r, p, s, relationalFixture(t, p, s, dialect, "v1"), "v1")
			first, err := commitFixture(t, r, p, s, v, "v1-commit")
			if err != nil {
				t.Fatal(err)
			}
			p = &first.Project
			// A real intermediate partial snapshot refreshes SQL only and retains ORM,
			// migration facets, omitted columns and their immutable old proofs.
			partialIn := relationalReconcileInput(t, p, s.RepositoryID, dialect, "v1", "partial")
			partialIn.GraphScope.Status = "partial"
			partialIn.GraphScope.Gaps = []string{"Only orders SQL facet reobserved"}
			for i := range partialIn.Inventory {
				if partialIn.Inventory[i].Category == "datastores" {
					partialIn.Inventory[i].Status = "partial"
					partialIn.Inventory[i].KnownCount = 0
					partialIn.Inventory[i].Denominator = nil
					partialIn.Inventory[i].Gaps = []string{"Datastore not reobserved"}
				}
			}
			partialSession, err := r.BeginImport(t.Context(), p.ID, partialIn)
			if err != nil {
				t.Fatal(err)
			}
			partialCommands := relationalSelect(relationalFixture(t, p, partialSession, dialect, "v1"), "table:orders", "proof:v1:table:orders:sql")
			pn := relationalCommand(partialCommands, "table:orders").Node
			pfs, _, _ := relationalFacetObject(pn.Kind, pn.Attributes)
			delete(pfs, "orm")
			delete(pfs, "migration")
			pn.Attributes = replaceRelationalFacets(pn.Kind, pn.Attributes, pfs)
			pn.EvidenceKeys = []string{"proof:v1:table:orders:sql"}
			partialPreview, _ := stageRelational(t, r, p, partialSession, partialCommands, "partial")
			if partialPreview.State != "ready" {
				t.Fatal(partialPreview.Diagnostics)
			}
			partialOut, err := commitFixture(t, r, p, partialSession, partialPreview, "partial-commit")
			if err != nil {
				t.Fatal(err)
			}
			if partialOut.Revision.Coverage.Status != "partial" {
				t.Fatal("partial import falsely complete")
			}
			omitted, err := r.Node(t.Context(), p.ID, partialOut.Revision.ID, ids["column:orders:status"])
			if err != nil || omitted.Freshness.Status != "stale" {
				t.Fatal("omitted column did not survive stale", err)
			}
			retained, err := r.Node(t.Context(), p.ID, partialOut.Revision.ID, ids["table:orders"])
			if err != nil {
				t.Fatal(err)
			}
			retainedFacets, _, _ := relationalFacetObject(retained.Kind, retained.Attributes)
			orm, _ := decodeRelationalFacet(retained.Kind, retainedFacets["orm"], true)
			if orm.Freshness.Status != "stale" || orm.SourceSnapshotID != s.SnapshotID {
				t.Fatal("ORM omission changed provenance")
			}
			p = &partialOut.Project
			in := relationalReconcileInput(t, p, s.RepositoryID, dialect, "v2", "v2")
			next, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			historyProject := *p
			historyProject.CurrentRevisionID = first.Revision.ID
			v2 := relationalFixtureWithHistory(t, &historyProject, next, dialect, "v2", ids)
			mapping := ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "column:orders:status", ToExternalKey: "column:orders:state", ExpectedID: ids["column:orders:status"], Reason: "Source declares explicit column rename", EvidenceKeys: []string{"proof:v2:column:orders:state:sql"}}}
			mapHash, _ := ImportBatchHash([]ImportCommand{mapping})
			mapReceipt, err := r.PutImportBatch(t.Context(), p.ID, next.ID, "map-before-upsert", ImportBatchInput{ExpectedImportVersion: next.Version, PayloadHash: mapHash, Commands: []ImportCommand{mapping}})
			if err != nil {
				t.Fatal(err)
			}
			next.Version = mapReceipt.AcceptedVersion
			commands := slices.Clone(v2)
			for _, key := range []string{"column:orders:legacy_note", "index:orders:legacy_note_idx"} {
				commands = append(commands, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: key, ExpectedID: ids[key], Reason: "Reviewed source removes subject"}}, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "edge", ExternalKey: "contains:" + key, ExpectedID: ids["contains:"+key], Reason: "Reviewed source removes containment"}})
			}
			// An unrepaired active view dependency blocks the otherwise proved deletion.
			mutateRelationalFacet(t, commands, "view:order_summaries", "sql", func(m map[string]jsontext.Value) {
				var deps []string
				_ = json.Unmarshal(m["dependencyKeys"], &deps)
				deps = append(deps, "column:orders:legacy_note")
				m["dependencyKeys"] = relationalRaw(t, deps)
			})
			bad, ack := stageRelational(t, r, p, next, commands, "v2-dangling")
			if ack["column:orders:state"] != ids["column:orders:status"] || bad.CandidateHash != nil || !slices.ContainsFunc(bad.Diagnostics, func(d ImportDiagnostic) bool { return d.Code == "backend_unsafe_deletion" }) {
				t.Fatalf("rename/deletion safety %+v", bad)
			}
			current, _ := r.Import(t.Context(), p.ID, next.ID, ListInput{})
			next.Version = current.Session.Version
			v2 = relationalFixtureWithHistory(t, &historyProject, next, dialect, "v2", ids)
			good, ack := stageRelational(t, r, p, next, v2, "v2-repaired")
			if good.State != "ready" {
				t.Fatalf("v2 repaired %+v", good.Diagnostics)
			}
			second, err := commitFixture(t, r, p, next, good, "v2-commit")
			if err != nil {
				t.Fatal(err)
			}
			if ack["contains:column:orders:status"] != ids["contains:column:orders:status"] {
				t.Fatal("mapped column containment edge ID changed")
			}
			if ack["column:orders:state"] != ids["column:orders:status"] {
				t.Fatal("mapped ID changed")
			}
			for _, key := range []string{"column:orders:legacy_note", "index:orders:legacy_note_idx"} {
				if _, err = r.Node(t.Context(), p.ID, second.Revision.ID, ids[key]); err == nil {
					t.Fatal("deleted subject active")
				}
				if _, err = r.Node(t.Context(), p.ID, first.Revision.ID, ids[key]); err != nil {
					t.Fatal("historical subject inaccessible", err)
				}
			}
			migration, err := r.Node(t.Context(), p.ID, second.Revision.ID, ids["migration:001_initial"])
			if err != nil {
				t.Fatal(err)
			}
			fs, _, _ := relationalFacetObject("migration", migration.Attributes)
			facet, err := decodeRelationalFacet("migration", fs["migration"], true)
			if err != nil {
				t.Fatal(err)
			}
			historical := 0
			for _, c := range facet.Changes {
				if c.Target.Kind == "historical" {
					historical++
					if c.Operation != "create" || c.Target.RevisionID != first.Revision.ID {
						t.Fatalf("historical CREATE lost %+v", c)
					}
				}
			}
			if historical != 2 {
				t.Fatalf("historical pins %d", historical)
			}
			for key, id := range ack {
				ids[key] = id
			}
			assertRelationalOracle(t, r, second, ids, dialect, "v2", first.Revision.ID)
		})
	}
}
func TestRelationalSelfAndCyclicFKWithoutPK(t *testing.T) {
	for _, mode := range []string{"self", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			r, p, s, cs := relationalTestSession(t)
			// No PK is required, and reference loops do not become containment loops.
			cs = slices.DeleteFunc(cs, func(c ImportCommand) bool {
				_, key, _ := commandAddress(c)
				return strings.Contains(key, ":pk") || c.Node != nil && c.Node.Kind == "migration" || c.Edge != nil && strings.HasPrefix(c.Edge.ToKey, "migration:") || c.Evidence != nil && (strings.HasPrefix(c.Evidence.SubjectKey, "migration:") || strings.HasPrefix(c.Evidence.SubjectKey, "contains:migration:"))
			})
			target := "table:orders"
			if mode == "cycle" {
				target = "table:users"
			}
			edge := relationalCommand(cs, "references:orders:user_fk").Edge
			edge.ToKey = target
			if mode == "self" {
				for _, fk := range []string{"sql", "orm"} {
					mutateRelationalFacet(t, cs, edge.ExternalKey, fk, func(m map[string]jsontext.Value) {
						m["columnPairs"] = jsontext.Value(`[{"fromColumnKey":"column:orders:tenant_id","toColumnKey":"column:orders:tenant_id"},{"fromColumnKey":"column:orders:user_id","toColumnKey":"column:orders:id"}]`)
					})
				}
			} else {
				cloneNode := *relationalCommand(cs, "constraint:orders:user_fk").Node
				cloneNode.ExternalKey = "constraint:users:orders_fk"
				cloneNode.Name = "users_orders_fk"
				cloneNode.ParentKey = new("table:users")
				cloneNode.Attributes = mapsCloneRaw(cloneNode.Attributes)
				cs = append(cs, ImportCommand{Op: "upsert_node", Node: &cloneNode})
				for _, fk := range []string{"sql", "orm"} {
					key := "proof:cycle:" + fk
					proof := *relationalCommand(cs, "proof:v1:constraint:orders:user_fk:"+fk).Evidence
					proof.ExternalKey = key
					proof.SubjectKey = cloneNode.ExternalKey
					proof.PropertyPath = new("/attributes/facets/" + fk)
					cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
					mutateRelationalFacet(t, cs, cloneNode.ExternalKey, fk, func(m map[string]jsontext.Value) {
						m["columnKeys"] = jsontext.Value(`["column:users:tenant_id","column:users:id"]`)
						m["evidenceKeys"] = relationalRaw(t, []string{key})
					})
				}
				cloneNode.EvidenceKeys = []string{"proof:cycle:sql", "proof:cycle:orm"}
				contains := *relationalCommand(cs, "contains:constraint:orders:user_fk").Edge
				contains.ExternalKey = "contains:" + cloneNode.ExternalKey
				contains.FromKey = "table:users"
				contains.ToKey = cloneNode.ExternalKey
				proof := *relationalCommand(cs, "proof:v1:contains:constraint:orders:user_fk:sql").Evidence
				proof.ExternalKey = "proof:cycle:contains"
				proof.SubjectKey = contains.ExternalKey
				contains.EvidenceKeys = []string{proof.ExternalKey}
				cs = append(cs, ImportCommand{Op: "upsert_edge", Edge: &contains}, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
				cloneEdge := *edge
				cloneEdge.ExternalKey = "references:users:orders_fk"
				cloneEdge.FromKey = cloneNode.ExternalKey
				cloneEdge.ToKey = "table:orders"
				cloneEdge.Attributes = mapsCloneRaw(cloneEdge.Attributes)
				cs = append(cs, ImportCommand{Op: "upsert_edge", Edge: &cloneEdge})
				cloneEdge.EvidenceKeys = []string{}
				for _, fk := range []string{"sql", "orm"} {
					key := "proof:cycle:edge:" + fk
					proof := *relationalCommand(cs, "proof:v1:references:orders:user_fk:"+fk).Evidence
					proof.ExternalKey = key
					proof.SubjectKey = cloneEdge.ExternalKey
					cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
					cloneEdge.EvidenceKeys = append(cloneEdge.EvidenceKeys, key)
					mutateRelationalFacet(t, cs, cloneEdge.ExternalKey, fk, func(m map[string]jsontext.Value) {
						m["columnPairs"] = jsontext.Value(`[{"fromColumnKey":"column:users:tenant_id","toColumnKey":"column:orders:tenant_id"},{"fromColumnKey":"column:users:id","toColumnKey":"column:orders:id"}]`)
						m["evidenceKeys"] = relationalRaw(t, []string{key})
					})
				}
			}
			v, _ := stageRelational(t, r, p, s, cs, "fk-"+mode)
			if v.State != "ready" {
				t.Fatalf("valid FK rejected %+v", v.Diagnostics)
			}
			_, err := commitFixture(t, r, p, s, v, "fk-commit")
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func mapsCloneRaw(in map[string]jsontext.Value) map[string]jsontext.Value {
	out := map[string]jsontext.Value{}
	for k, v := range in {
		out[k] = slices.Clone(v)
	}
	return out
}
func TestRelationalReferenceAndNativeBounds(t *testing.T) {
	for _, name := range []string{"constraint65", "index65", "view501", "migration501", "native64KiB", "exactint64"} {
		t.Run(name, func(t *testing.T) {
			r, p, s, cs := relationalTestSession(t)
			limit := true
			switch name {
			case "constraint65":
				keys := []string{}
				for i := range 65 {
					keys = append(keys, fmt.Sprintf("column:extra%d", i))
				}
				mutateRelationalFacet(t, cs, "constraint:orders:user_fk", "sql", func(m map[string]jsontext.Value) { m["columnKeys"] = relationalRaw(t, keys) })
			case "index65":
				terms := []any{}
				for i := range 65 {
					terms = append(terms, map[string]any{"columnKey": fmt.Sprintf("column:extra%d", i), "direction": "asc", "nulls": "unknown"})
				}
				mutateRelationalFacet(t, cs, "index:orders:state_idx", "sql", func(m map[string]jsontext.Value) { m["terms"] = relationalRaw(t, terms) })
			case "view501":
				keys := []string{}
				for i := range 501 {
					keys = append(keys, fmt.Sprintf("table:extra%d", i))
				}
				mutateRelationalFacet(t, cs, "view:order_summaries", "sql", func(m map[string]jsontext.Value) { m["dependencyKeys"] = relationalRaw(t, keys) })
			case "migration501":
				changes := []any{}
				for range 501 {
					changes = append(changes, map[string]any{"target": map[string]any{"kind": "candidate", "objectKey": "table:orders"}, "operation": "alter", "description": "change"})
				}
				mutateRelationalFacet(t, cs, "migration:001_initial", "migration", func(m map[string]jsontext.Value) { m["changes"] = relationalRaw(t, changes) })
			case "native64KiB":
				limit = false
				mutateRelationalFacet(t, cs, "column:orders:total", "sql", func(m map[string]jsontext.Value) {
					m["nativeType"] = relationalRaw(t, known(strings.Repeat("x", MaxRelationalNativeBytes)))
				})
			case "exactint64":
				limit = false
				mutateRelationalFacet(t, cs, "column:orders:total", "sql", func(m map[string]jsontext.Value) {
					m["ordinal"] = jsontext.Value(`{"status":"known","value":9223372036854775807}`)
				})
			}
			h, err := ImportBatchHash(cs)
			if err != nil {
				t.Fatal(err)
			}
			b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, name, ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: h, Commands: cs})
			if limit {
				assertFault(t, err, "backend_import_limit")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if err != nil || v.State != "ready" {
				t.Fatalf("valid bound %+v %v", v, err)
			}
			out, err := commitFixture(t, r, p, s, v, "commit")
			if err != nil {
				t.Fatal(err)
			}
			var id string
			for _, x := range b.Identities {
				if x.ExternalKey == "column:orders:total" {
					id = x.ID
				}
			}
			n, err := r.Node(t.Context(), p.ID, out.Revision.ID, id)
			if err != nil {
				t.Fatal(err)
			}
			if name == "exactint64" && !strings.Contains(string(n.Attributes["facets"]), `9223372036854775807`) {
				t.Fatal("integer token rounded")
			}
		})
	}
}
func TestRelationalUnresolvedReferenceTarget(t *testing.T) {
	r, p, s, cs := relationalTestSession(t)
	key := "references:orders:user_fk"
	edge := relationalCommand(cs, key).Edge
	edge.ToKey = "unresolved:table"
	for _, fk := range []string{"sql", "orm"} {
		mutateRelationalFacet(t, cs, key, fk, func(m map[string]jsontext.Value) {
			m["columnPairs"] = jsontext.Value(`[]`)
			m["targetReason"] = jsontext.Value(`"Target schema source is unavailable"`)
		})
	}
	cs = append(cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "unresolved:table", Kind: "unresolved_target", Name: "unknown table", Attributes: map[string]jsontext.Value{"expectedKind": jsontext.Value(`"table"`), "reason": jsontext.Value(`"Unavailable source"`), "searchScope": jsontext.Value(`"orders repository"`)}, EvidenceKeys: []string{}}})
	v, _ := stageRelational(t, r, p, s, cs, "unresolved")
	if v.State != "ready" || v.Summary.Unresolved != 1 {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision.Coverage.Status != "partial" {
		t.Fatal("unresolved schema falsely complete")
	}
}
func TestRelationalFoundationDatastoreOwnershipPreserved(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	in := relationalFixtureInput(t, p, "postgresql", "v1")
	in.Profile = GraphProfile
	in.Manifest.Provider.Profiles = []string{GraphProfile}
	firstSession, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	full := relationalFixture(t, p, firstSession, "postgresql", "v1")
	node := *relationalCommand(full, "database:orders").Node
	node.Attributes = map[string]jsontext.Value{"technology": jsontext.Value(`"postgresql"`)}
	proof := *relationalCommand(full, "proof:v1:database:orders:sql").Evidence
	proof.PropertyPath = new("/kind")
	v, ids := stageRelational(t, r, p, firstSession, []ImportCommand{{Op: "upsert_node", Node: &node}, {Op: "upsert_evidence", Evidence: &proof}}, "foundation")
	first, err := commitFixture(t, r, p, firstSession, v, "commit-foundation")
	if err != nil {
		t.Fatal(err)
	}
	p = &first.Project
	nextIn := relationalReconcileInput(t, p, firstSession.RepositoryID, "postgresql", "v1", "extension")
	nextIn.ProfileExtension = &ImportProfileExtension{FromProfile: GraphProfile, ToProfile: RelationalProfile}
	next, err := r.BeginImport(t.Context(), p.ID, nextIn)
	if err != nil {
		t.Fatal(err)
	}
	v, secondIDs := stageRelational(t, r, p, next, relationalFixture(t, p, next, "postgresql", "v1"), "relational")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, next, v, "commit-extension")
	if err != nil {
		t.Fatal(err)
	}
	if secondIDs["database:orders"] != ids["database:orders"] {
		t.Fatal("foundation datastore ID changed")
	}
	current, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["database:orders"])
	if err != nil || current.Ownership.Profile != GraphProfile {
		t.Fatal("existing foundation ownership rewritten", err)
	}
	page, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{SubjectID: current.ID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Ownership.Profile != GraphProfile {
		t.Fatal("evidence did not inherit subject profile", err)
	}
}
func TestRelationalMigrationCompletenessAndSourceOnly(t *testing.T) {
	for _, name := range []string{"unsupported changes cannot derive complete table", "complete table needs supporting migration", "source-only cannot name active target"} {
		t.Run(name, func(t *testing.T) {
			r, p, s, cs := relationalTestSession(t)
			if name == "source-only cannot name active target" {
				mutateRelationalFacet(t, cs, "migration:001_initial", "migration", func(m map[string]jsontext.Value) {
					m["changes"] = jsontext.Value(`[{"operation":"create","description":"claim transient","target":{"kind":"source_only","externalKey":"column:orders:status","expectedKind":"column","qualifiedName":"public.orders.status","reason":"created and dropped before import"}}]`)
				})
			} else {
				mutateRelationalFacet(t, cs, "table:orders", "migration", func(m map[string]jsontext.Value) {
					m["columnsStatus"] = jsontext.Value(`"complete"`)
					m["constraintsStatus"] = jsontext.Value(`"complete"`)
					m["analysisStatus"] = jsontext.Value(`"complete"`)
					m["gaps"] = jsontext.Value(`[]`)
				})
				if name == "complete table needs supporting migration" {
					cs = slices.DeleteFunc(cs, func(c ImportCommand) bool {
						return c.Node != nil && c.Node.Kind == "migration" || c.Edge != nil && strings.HasPrefix(c.Edge.ToKey, "migration:") || c.Evidence != nil && (strings.HasPrefix(c.Evidence.SubjectKey, "migration:") || strings.HasPrefix(c.Evidence.SubjectKey, "contains:migration:"))
					})
				}
			}
			v, _ := stageRelational(t, r, p, s, cs, "invalid-derivation")
			if v.State != "needs_resolution" || v.CandidateHash != nil {
				t.Fatalf("unsupported/invented migration state accepted %+v", v)
			}
		})
	}
}
func TestRelationalDeletionScopeGuards(t *testing.T) {
	for _, mode := range []string{"partial", "unverified", "provider mismatch"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := testRepo(t)
			first, ids := commitRelationalFixture(t, r, "postgresql", "v1")
			p := &first.Project
			state, err := loadSourceState(t.Context(), r.db.R, p.ID, p.CurrentRevisionID)
			if err != nil {
				t.Fatal(err)
			}
			repository := primarySource(*state).RepositoryID
			in := relationalReconcileInput(t, p, repository, "postgresql", "v1", "deletion")
			switch mode {
			case "partial":
				in.GraphScope.Status = "partial"
				in.GraphScope.Gaps = []string{"Source inventory incomplete"}
			case "unverified":
				in.Manifest.Snapshot.Consistency = "unverified"
			case "provider mismatch":
				in.Manifest.Provider.Namespace = "other-provider"
			}
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if mode == "provider mismatch" {
				assertFault(t, err, "backend_incompatible_provider")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Scope/consistency stay immutable in the stored session.

			cs := []ImportCommand{{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: "index:orders:legacy_note_idx", ExpectedID: ids["index:orders:legacy_note_idx"], Reason: "Source removed index"}}, {Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "edge", ExternalKey: "contains:index:orders:legacy_note_idx", ExpectedID: ids["contains:index:orders:legacy_note_idx"], Reason: "Source removed containment"}}}
			v, _ := stageRelational(t, r, p, s, cs, "unsafe-deletion")
			if v.CandidateHash != nil || !slices.ContainsFunc(v.Diagnostics, func(d ImportDiagnostic) bool { return d.Code == "backend_unsafe_deletion" }) {
				t.Fatalf("unsafe deletion accepted %+v", v)
			}
			current, err := r.Node(t.Context(), p.ID, p.CurrentRevisionID, ids["index:orders:legacy_note_idx"])
			if err != nil || current.ID != ids["index:orders:legacy_note_idx"] {
				t.Fatal("failed deletion mutated head", err)
			}
		})
	}
}
func TestRelationalDescriptionRetainsExistingStringContract(t *testing.T) {
	r, p, s, cs := relationalTestSession(t)
	relationalCommand(cs, "table:orders").Node.Attributes["description"] = relationalRaw(t, strings.Repeat("x", MaxRelationalNativeBytes+1))
	v, _ := stageRelational(t, r, p, s, cs, "description")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
}
func TestRelationalRevisionCompareExactIntegerTokens(t *testing.T) {
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	before, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	after := *before
	after.Nodes = slices.Clone(before.Nodes)
	for i := range before.Nodes {
		if before.Nodes[i].ID != ids["column:orders:total"] {
			continue
		}
		for _, side := range []*Node{&before.Nodes[i], &after.Nodes[i]} {
			fs, _, _ := relationalFacetObject(side.Kind, side.Attributes)
			m, _ := relationalObject(fs["sql"])
			value := `9007199254740992`
			if side == &after.Nodes[i] {
				value = `9007199254740993`
			}
			m["ordinal"] = jsontext.Value(`{"status":"known","value":` + value + `}`)
			fs["sql"] = relationalRaw(t, m)
			side.Attributes = replaceRelationalFacets(side.Kind, side.Attributes, fs)
		}
	}
	status, err := r.Import(t.Context(), out.Project.ID, out.SessionID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	hashes := []string{}
	for _, state := range []*RevisionState{before, &after} {
		for _, n := range state.Nodes {
			if n.ID == ids["column:orders:total"] {
				if err := validateRelationalAttributes(n.Kind, n.Attributes, false, true); err != nil {
					t.Fatal(err)
				}
			}
		}
		candidate := &graphCandidate{Sources: state.Sources, Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, Coverage: state.Revision.Coverage}
		raw, err := candidateJSON(&status.Session, candidate)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, hashBytes(raw))
	}
	if hashes[0] == hashes[1] {
		t.Fatal("same pinned session/evidence lost adjacent ordinal distinction in candidate hash")
	}
	delta, err := CompareRevisionStates(t.Context(), *before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(delta.Changes, func(change RecordDelta) bool {
		return change.ID == ids["column:orders:total"] && slices.Contains(change.ChangeKinds, "modified") && slices.Contains(change.ChangedPaths, "/attributes/facets")
	}) {
		t.Fatalf("adjacent exact int64 values compared equal: %+v", delta)
	}
}

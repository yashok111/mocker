package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func relationalRaw(t *testing.T, v any) jsontext.Value {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func known(v any) map[string]any { return map[string]any{"status": "known", "value": v} }
func unknown(reason string) map[string]any {
	return map[string]any{"status": "unknown", "reason": reason}
}
func relationalFixtureInput(t *testing.T, p *Project, dialect, version string) BeginImportInput {
	t.Helper()
	in := relationalInput(firstImportFixture(p), false)
	in.IdempotencyKey = "begin-" + dialect + "-" + version
	in.Manifest.RepositoryName = "orders-fixture"
	in.Manifest.Provider = SourceProvider{Name: "orders-fixtures", Version: "1", Namespace: "orders-fixtures", Method: "agent", Profiles: []string{GraphProfile, RelationalProfile}, Limitations: []string{}}
	in.Manifest.Snapshot.Files = []ManifestFile{}
	paths := []string{"schema.sql", "models.go", "migrations/001_initial.sql", "migrations/002_unsupported.sql"}
	if version == "v2" {
		paths = append(paths, "migrations/003_column_change.sql")
	}
	for _, name := range paths {
		path := filepath.Join(dialect, version, name)
		b, err := os.ReadFile(filepath.Join("testdata/relational/orders", path))
		if err != nil {
			t.Fatal(err)
		}
		in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, ManifestFile{Path: path, ContentHash: hashBytes(b), FileType: filepath.Ext(name)[1:], AnalysisStatus: "analyzed"})
	}
	for i := range in.Inventory {
		switch in.Inventory[i].Category {
		case "files":
			in.Inventory[i].KnownCount = int64(len(paths))
		case "datastores":
			in.Inventory[i].KnownCount = 1
		case "migrations":
			in.Inventory[i].KnownCount = int64(len(paths) - 2)
		}
		in.Inventory[i].Denominator = new(in.Inventory[i].KnownCount)
	}
	return in
}

// Templates are independently authored from source; expected.json is never an input.
func relationalFixture(t *testing.T, p *Project, s *ImportSession, dialect, version string) []ImportCommand {
	return relationalFixtureWithHistory(t, p, s, dialect, version, nil)
}
func relationalFixtureWithHistory(t *testing.T, p *Project, s *ImportSession, dialect, version string, history map[string]string) []ImportCommand {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata/relational/orders", dialect, version, "commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	replacements := []string{"@repositoryId@", s.RepositoryID, "@snapshotId@", s.SnapshotID}
	if version == "v2" {
		replacements = append(replacements, "@v1RevisionId@", p.CurrentRevisionID)
		for _, key := range []string{"column:orders:legacy_note", "index:orders:legacy_note_idx"} {
			id := history[key]
			if id == "" {
				t.Fatalf("v2 requires historical identity %s", key)
			}
			replacements = append(replacements, "@v1Object:"+key+"@", id)
		}
	}
	raw = []byte(strings.NewReplacer(replacements...).Replace(string(raw)))
	var cs []ImportCommand
	if err = json.Unmarshal(raw, &cs, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	return cs
}
func stageRelational(t *testing.T, r *Repo, p *Project, s *ImportSession, cs []ImportCommand, key string) (*ImportPreview, map[string]string) {
	t.Helper()
	hash, err := ImportBatchHash(cs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, key, ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: cs})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, id := range b.Identities {
		ids[id.ExternalKey] = id.ID
	}
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	return v, ids
}
func commitRelationalFixture(t *testing.T, r *Repo, dialect, version string) (*ImportCommitResult, map[string]string) {
	t.Helper()
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, relationalFixtureInput(t, p, dialect, version))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, relationalFixture(t, p, s, dialect, version), "fixture")
	if v.State != "ready" {
		t.Fatalf("relational preview: %+v", v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "fixture-commit")
	if err != nil {
		t.Fatal(err)
	}
	return out, ids
}
func TestRelationalSourceFixtureImport(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			r, _ := testRepo(t)
			out, ids := commitRelationalFixture(t, r, dialect, "v1")
			if out.Revision.SchemaVersion != "2" {
				t.Fatal("schema2 required")
			}
			n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["column:orders:total"])
			if err != nil {
				t.Fatal(err)
			}
			var facets map[string]map[string]jsontext.Value
			if err = json.Unmarshal(n.Attributes["facets"], &facets); err != nil {
				t.Fatal(err)
			}
			want := "numeric(18,2)"
			if dialect == "sqlite" {
				want = "NUMERIC(18,2)"
			}
			var native struct {
				Status string `json:"status"`
				Value  string `json:"value"`
			}
			_ = json.Unmarshal(facets["sql"]["nativeType"], &native)
			if native.Status != "known" || native.Value != want {
				t.Fatalf("native type lost: %s", facets["sql"]["nativeType"])
			}
			for _, table := range []string{"backend_identity_bindings", "backend_import_identities"} {
				var count int
				query := `SELECT count(*) FROM backend_identity_bindings WHERE project_id=? AND external_key='column:orders:scratch_status'`
				if table == "backend_import_identities" {
					query = `SELECT count(*) FROM backend_import_identities i JOIN backend_import_sessions s ON s.id=i.session_id WHERE s.project_id=? AND i.external_key='column:orders:scratch_status'`
				}
				if err := r.db.R.QueryRowContext(t.Context(), query, out.Project.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("source-only allocated in %s: %d %v", table, count, err)
				}
			}
			if ids["column:orders:scratch_status"] != "" {
				t.Fatal("source-only target allocated")
			}
		})
	}
}

func assertRelationalSubset(t *testing.T, expected, actual jsontext.Value, path string, ids map[string]string, revision string) {
	t.Helper()
	if len(expected) == 0 {
		return
	}
	switch expected[0] {
	case '{':
		var want, got map[string]jsontext.Value
		if json.Unmarshal(expected, &want) != nil || json.Unmarshal(actual, &got) != nil {
			t.Errorf("%s: expected object got %s", path, actual)
			return
		}
		if string(want["status"]) == `"unknown"` {
			if string(got["status"]) != `"unknown"` {
				t.Errorf("%s: unknown became %s", path, actual)
			}
			return
		}
		for key, value := range want {
			if key == "gaps" || key == "description" || key == "reason" {
				continue
			}
			wire := key
			switch key {
			case "columnKeys":
				wire = "columnIds"
			case "dependencyKeys":
				wire = "dependencyIds"
			case "parentKeys":
				wire = "parentIds"
			case "fromColumnKey":
				wire = "fromColumnId"
			case "toColumnKey":
				wire = "toColumnId"
			case "columnKey":
				wire = "columnId"
			case "objectKey":
				wire = "objectId"
			case "revisionPin":
				wire = "revisionId"
			}
			if key == "revisionPin" {
				value = relationalRaw(t, revision)
			} else if wire != key {
				if value[0] == '[' {
					var keys []string
					_ = json.Unmarshal(value, &keys)
					resolved := []string{}
					for _, k := range keys {
						resolved = append(resolved, ids[k])
					}
					value = relationalRaw(t, resolved)
				} else {
					var k string
					_ = json.Unmarshal(value, &k)
					value = relationalRaw(t, ids[k])
				}
			}
			if key == "changes" {
				var changes, actualChanges []jsontext.Value
				_ = json.Unmarshal(value, &changes)
				_ = json.Unmarshal(got[wire], &actualChanges)
				for _, c := range changes {
					var cm map[string]jsontext.Value
					_ = json.Unmarshal(c, &cm)
					var ct map[string]jsontext.Value
					_ = json.Unmarshal(cm["target"], &ct)
					var kind, k string
					_ = json.Unmarshal(ct["kind"], &kind)
					_ = json.Unmarshal(ct["objectKey"], &k)
					if kind == "source_only" {
						_ = json.Unmarshal(ct["externalKey"], &k)
					}
					matched := false
					for _, a := range actualChanges {
						var am map[string]jsontext.Value
						_ = json.Unmarshal(a, &am)
						var at map[string]jsontext.Value
						_ = json.Unmarshal(am["target"], &at)
						wireKey := "objectId"
						wantedID := ids[k]
						if kind == "source_only" {
							wireKey = "externalKey"
							wantedID = k
						}
						var aid string
						_ = json.Unmarshal(at[wireKey], &aid)
						if string(am["operation"]) == string(cm["operation"]) && string(at["kind"]) == string(ct["kind"]) && aid == wantedID {
							assertRelationalSubset(t, c, a, path+"/changes/"+k, ids, revision)
							matched = true
							break
						}
					}
					if !matched {
						t.Errorf("%s: missing expected migration change %s", path, c)
					}
				}
				continue
			}
			assertRelationalSubset(t, value, got[wire], path+"/"+wire, ids, revision)
		}
	case '[':
		var want, got []jsontext.Value
		if json.Unmarshal(expected, &want) != nil || json.Unmarshal(actual, &got) != nil || len(want) != len(got) {
			t.Errorf("%s: ordered array mismatch expected %s got %s", path, expected, actual)
			return
		}
		for i := range want {
			assertRelationalSubset(t, want[i], got[i], fmt.Sprintf("%s/%d", path, i), ids, revision)
		}
	default:
		wb, err := canonicalValue(expected)
		if err != nil {
			t.Fatal(err)
		}
		gb, err := canonicalValue(actual)
		if err != nil || string(wb) != string(gb) {
			t.Errorf("%s: expected %s got %s", path, expected, actual)
		}
	}
}
func assertRelationalOracle(t *testing.T, r *Repo, out *ImportCommitResult, ids map[string]string, dialect, version, historyRevision string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata/relational/orders", dialect, version, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Nodes map[string]jsontext.Value `json:"nodes"`
		Edges map[string]jsontext.Value `json:"edges"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Nodes) != len(oracle.Nodes) {
		t.Errorf("node inventory expected%d got%d", len(oracle.Nodes), len(state.Nodes))
	}
	edges := map[string]Edge{}
	for _, e := range state.Edges {
		edges[e.ExternalKey] = e
	}
	for key, want := range oracle.Nodes {
		n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids[key])
		if err != nil {
			t.Errorf("%s unavailable: %v", key, err)
			continue
		}
		var expected struct {
			Kind      string                    `json:"kind"`
			Name      string                    `json:"name"`
			ParentKey string                    `json:"parentKey"`
			Facets    map[string]jsontext.Value `json:"facets"`
			Native    struct {
				Path  string `json:"path"`
				Start int    `json:"startLine"`
				End   int    `json:"endLine"`
			} `json:"nativeDefinitionSource"`
		}
		_ = json.Unmarshal(want, &expected)
		if n.Kind != expected.Kind || n.Name != expected.Name || expected.ParentKey != "" && (n.ParentID == nil || *n.ParentID != ids[expected.ParentKey]) {
			t.Errorf("%s identity/hierarchy mismatch %+v", key, n)
		}
		fs, _, err := relationalFacetObject(n.Kind, n.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		for fk, facet := range expected.Facets {
			assertRelationalSubset(t, facet, fs[fk], key+"/"+fk, ids, historyRevision)
		}
		if expected.Native.Path != "" {
			source, err := os.ReadFile(filepath.Join("testdata/relational/orders", expected.Native.Path))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.SplitAfter(string(source), "\n")
			native := strings.Join(lines[expected.Native.Start-1:expected.Native.End], "")
			field := "nativeDefinition"
			fk := "sql"
			if n.Kind == "view" || n.Kind == "symbol" || n.Kind == "migration" {
				field = "definition"
			}
			if n.Kind == "migration" {
				fk = "migration"
			}
			var values map[string]jsontext.Value
			_ = json.Unmarshal(fs[fk], &values)
			var value string
			_ = json.Unmarshal(values[field], &value)
			if value != native {
				t.Errorf("%s native definition differs from source bytes", key)
			}
		}
	}
	for key, want := range oracle.Edges {
		e, ok := edges[key]
		if !ok {
			t.Errorf("%s reference edge missing", key)
			continue
		}
		var expected struct {
			Kind    string                    `json:"kind"`
			FromKey string                    `json:"fromKey"`
			ToKey   string                    `json:"toKey"`
			Facets  map[string]jsontext.Value `json:"facets"`
		}
		_ = json.Unmarshal(want, &expected)
		if e.Kind != expected.Kind || e.From != ids[expected.FromKey] || e.To != ids[expected.ToKey] {
			t.Errorf("%s endpoints mismatch", key)
		}
		fs, _, err := relationalFacetObject(e.Kind, e.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		for fk, facet := range expected.Facets {
			assertRelationalSubset(t, facet, fs[fk], key+"/"+fk, ids, historyRevision)
		}
	}
}
func TestRelationalIndependentSourceOracle(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			r, _ := testRepo(t)
			out, ids := commitRelationalFixture(t, r, dialect, "v1")
			assertRelationalOracle(t, r, out, ids, dialect, "v1", "")
		})
	}
}

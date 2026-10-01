package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRelationalFacetComparisonIndependentOracle(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		for _, version := range []string{"v1", "v2"} {
			t.Run(dialect+"/"+version, func(t *testing.T) {
				r, _ := testRepo(t)
				out, ids := commitRelationalFixture(t, r, dialect, "v1")
				if version == "v2" {
					out, ids = databaseCommitV2(t, r, out, ids, dialect)
				}
				var oracle struct {
					Drift struct {
						KnownDifferences []struct {
							SubjectKey, LeftFacetKey, RightFacetKey string
							ChangedPaths                            []string
						}
						UnknownComparisons []struct{ SubjectKey, LeftFacetKey, RightFacetKey, Property string }
					}
				}
				b, err := os.ReadFile(filepath.Join("testdata/relational/orders", dialect, version, "expected.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(b, &oracle, json.MatchCaseInsensitiveNames(true)); err != nil {
					t.Fatal(err)
				}
				state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
				if err != nil {
					t.Fatal(err)
				}
				comparisons := map[string]*FacetComparison{}
				for _, n := range state.Nodes {
					c, err := CompareRelationalFacets(n.Kind, n.Attributes, false)
					if err != nil {
						t.Fatal(err)
					}
					comparisons[n.ID] = c
				}
				for _, e := range state.Edges {
					c, err := CompareRelationalFacets(e.Kind, e.Attributes, true)
					if err != nil {
						t.Fatal(err)
					}
					comparisons[e.ID] = c
				}
				for _, want := range oracle.Drift.KnownDifferences {
					c := comparisons[ids[want.SubjectKey]]
					if c == nil {
						t.Fatalf("missing comparison %s", want.SubjectKey)
					}
					pair := comparisonPair(t, c, want.LeftFacetKey, want.RightFacetKey)
					if pair.Status != "different" {
						t.Fatalf("%s: %+v", want.SubjectKey, pair)
					}
					for _, path := range want.ChangedPaths {
						if !slices.Contains(pair.ChangedPaths, path) {
							t.Fatalf("%s missing %s: %+v", want.SubjectKey, path, pair)
						}
					}
				}
				for _, want := range oracle.Drift.UnknownComparisons {
					left, right := want.LeftFacetKey, want.RightFacetKey
					if left == "" {
						left, right = "sql", "orm"
					}
					pair := comparisonPair(t, comparisons[ids[want.SubjectKey]], left, right)
					if pair.Status != "unknown" {
						t.Fatalf("uncertain %s compared as %+v", want.SubjectKey, pair)
					}
				}
			})
		}
	}
}

func comparisonPair(t *testing.T, c *FacetComparison, a, b string) FacetPairDifference {
	t.Helper()
	if c != nil {
		for _, p := range c.Pairs {
			if p.LeftFacetKey == a && p.RightFacetKey == b || p.LeftFacetKey == b && p.RightFacetKey == a {
				return p
			}
		}
	}
	t.Fatalf("missing pair %s/%s in %+v", a, b, c)
	return FacetPairDifference{}
}

func TestRelationalFacetComparisonKnownNullUnknownAndDefinition(t *testing.T) {
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["column:payments:amount"])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, left, right, want string }{
		{"equal null", `{"status":"known","value":null}`, `{"status":"known","value":null}`, "consistent"},
		{"null versus literal", `{"status":"known","value":null}`, `{"status":"known","value":"NULL"}`, "different"},
		{"equal unknown", `{"status":"unknown","reason":"unavailable"}`, `{"status":"unknown","reason":"unavailable"}`, "unknown"},
		{"null versus unknown", `{"status":"known","value":null}`, `{"status":"unknown","reason":"unavailable"}`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
			base, _ := relationalObject(fs["sql"])
			base["defaultExpression"] = jsontext.Value(tc.left)
			fs["a"] = relationalRaw(t, base)
			base["defaultExpression"] = jsontext.Value(tc.right)
			fs["b"] = relationalRaw(t, base)
			delete(fs, "sql")
			delete(fs, "orm")
			attrs := replaceRelationalFacets(n.Kind, n.Attributes, fs)
			before := relationalRaw(t, attrs)
			c, err := CompareRelationalFacets(n.Kind, attrs, false)
			if err != nil || c.Status != tc.want {
				t.Fatalf("%+v %v", c, err)
			}
			if string(before) != string(relationalRaw(t, attrs)) {
				t.Fatal("comparator mutated source")
			}
		})
	}
	view, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["view:order_summaries"])
	if err != nil {
		t.Fatal(err)
	}
	fs, _, _ := relationalFacetObject(view.Kind, view.Attributes)
	m, _ := relationalObject(fs["sql"])
	fs = map[string]jsontext.Value{"a": relationalRaw(t, m)}
	m["definition"] = relationalRaw(t, "SELECT different_source_text")
	fs["b"] = relationalRaw(t, m)
	c, err := CompareRelationalFacets(view.Kind, replaceRelationalFacets(view.Kind, view.Attributes, fs), false)
	if err != nil || c.Status != "consistent" || !c.Pairs[0].DefinitionDifferent || len(c.Pairs[0].ChangedPaths) != 0 {
		t.Fatalf("definition inferred semantic difference: %+v %v", c, err)
	}
}

func TestRelationalComputedReadMetadataIsNotStored(t *testing.T) {
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["column:orders:user_id"])
	if err != nil {
		t.Fatal(err)
	}
	var read map[string]jsontext.Value
	if err := json.Unmarshal(relationalRaw(t, n), &read); err != nil {
		t.Fatal(err)
	}
	if read["facetComparison"] == nil {
		t.Fatal("schema2 node read lacks computed facetComparison")
	}
	for _, typ := range []string{"nodes", "edges"} {
		kind := "column"
		if typ == "edges" {
			kind = "references"
		}
		page, err := r.QueryGraph(t.Context(), out.Project.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: typ, Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		if typ == "nodes" {
			for _, n := range page.Nodes {
				if n.FacetComparison == nil {
					t.Fatal("graph node lacks computed comparison")
				}
			}
		} else {
			for _, e := range page.Edges {
				if e.FacetComparison == nil {
					t.Fatal("graph edge lacks computed comparison")
				}
			}
		}
	}
	state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range state.Nodes {
		if n.FacetComparison != nil {
			t.Fatal("computed node metadata leaked into canonical state")
		}
	}
	for _, e := range state.Edges {
		if e.FacetComparison != nil {
			t.Fatal("computed edge metadata leaked into canonical state")
		}
	}
	status, err := r.Import(t.Context(), out.Project.ID, out.SessionID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	rawCandidate, err := candidateJSON(&status.Session, &graphCandidate{Sources: state.Sources, Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, Coverage: state.Revision.Coverage})
	if err != nil || strings.Contains(string(rawCandidate), "facetComparison") {
		t.Fatal("read metadata entered candidate hash input", err)
	}
	var count int
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_graph_records WHERE revision_id=? AND instr(document,'facetComparison')>0`, out.Revision.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stored computed metadata: %d %v", count, err)
	}
	for _, raw := range []string{`{"facetComparison":{"status":"different"}}`} {
		var n ImportNode
		var e ImportEdge
		if json.Unmarshal([]byte(raw), &n, json.RejectUnknownMembers(true)) == nil || json.Unmarshal([]byte(raw), &e, json.RejectUnknownMembers(true)) == nil {
			t.Fatal("read metadata accepted in imports")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.QueryGraph(ctx, out.Project.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"}); err == nil {
		t.Fatal("canceled read succeeded")
	}
}

func TestRelationalFacetComparisonIncompleteChangesDoNotAssertAbsence(t *testing.T) {
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["migration:001_initial"])
	if err != nil {
		t.Fatal(err)
	}
	fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
	m, _ := relationalObject(fs["migration"])
	fs = map[string]jsontext.Value{"a": relationalRaw(t, m)}
	m["derivationStatus"] = relationalRaw(t, "partial")
	m["gaps"] = relationalRaw(t, []string{"Only a subset of source changes was established"})
	m["changes"] = jsontext.Value(`[]`)
	fs["b"] = relationalRaw(t, m)
	c, err := CompareRelationalFacets(n.Kind, replaceRelationalFacets(n.Kind, n.Attributes, fs), false)
	if err != nil || c.Status != "unknown" || len(c.Pairs[0].ChangedPaths) != 0 {
		t.Fatalf("partial migration inventory asserted absence %+v %v", c, err)
	}
}

func TestRelationalFacetComparisonExactIntegersAndOrderedPairs(t *testing.T) {
	r, _ := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["column:orders:total"])
	if err != nil {
		t.Fatal(err)
	}
	fs, _, _ := relationalFacetObject(n.Kind, n.Attributes)
	m, _ := relationalObject(fs["sql"])
	m["ordinal"] = jsontext.Value(`{"status":"known","value":9007199254740992}`)
	fs = map[string]jsontext.Value{"a": relationalRaw(t, m)}
	m["ordinal"] = jsontext.Value(`{"status":"known","value":9007199254740993}`)
	fs["b"] = relationalRaw(t, m)
	c, err := CompareRelationalFacets(n.Kind, replaceRelationalFacets(n.Kind, n.Attributes, fs), false)
	if err != nil || c.Status != "different" || !slices.Equal(c.Pairs[0].ChangedPaths, []string{"/ordinal"}) {
		t.Fatalf("adjacent exact int64 values collapsed %+v %v", c, err)
	}
	delete(fs, "b")
	c, err = CompareRelationalFacets(n.Kind, replaceRelationalFacets(n.Kind, n.Attributes, fs), false)
	if err != nil || c.Status != "unknown" || len(c.Pairs) != 0 {
		t.Fatal("single facet claimed cross-source consistency", c, err)
	}
	page, err := r.QueryGraph(t.Context(), out.Project.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "edges", ID: ids["references:orders:user_fk"]})
	if err != nil || len(page.Edges) != 1 {
		t.Fatal("edge read", err)
	}
	e := page.Edges[0]
	fs, _, _ = relationalFacetObject(e.Kind, e.Attributes)
	m, _ = relationalObject(fs["sql"])
	fs = map[string]jsontext.Value{"a": relationalRaw(t, m)}
	var pairs []jsontext.Value
	_ = json.Unmarshal(m["columnPairs"], &pairs)
	slices.Reverse(pairs)
	m["columnPairs"] = relationalRaw(t, pairs)
	fs["b"] = relationalRaw(t, m)
	c, err = CompareRelationalFacets(e.Kind, replaceRelationalFacets(e.Kind, e.Attributes, fs), true)
	if err != nil || c.Status != "different" || !slices.Equal(c.Pairs[0].ChangedPaths, []string{"/columnPairs"}) {
		t.Fatalf("ordered pairs treated as sets %+v %v", c, err)
	}
}

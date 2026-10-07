package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

func lineageQueryID(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }
func lineageQueryState(t *testing.T, count int) *RevisionState {
	t.Helper()
	s := &RevisionState{Revision: Revision{ID: lineageQueryID(1), ProjectID: lineageQueryID(2), SchemaVersion: LineageSchemaVersion, SemanticHash: "pinned", Coverage: Coverage{Status: "complete", Gaps: []string{}}}, Sources: []SourceSnapshot{{ID: lineageQueryID(3), Role: "primary", Provider: SourceProvider{Profiles: []string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile}, Limitations: []string{"provider limitation"}}}}}
	for i := 0; i < count; i++ {
		n := Node{ID: lineageQueryID(100 + i), Kind: "api_field", EvidenceIDs: []string{lineageQueryID(100000 + i)}, Attributes: map[string]jsontext.Value{"analysisStatus": jsontext.Value(`"complete"`)}}
		s.Nodes = append(s.Nodes, n)
		s.Evidence = append(s.Evidence, Evidence{ID: n.EvidenceIDs[0], SubjectID: n.ID, Status: "explicit"})
	}
	return s
}
func lineageQueryRef(s *RevisionState, i int) LineageValueRef {
	return LineageValueRef{Kind: "api_field", NodeID: s.Nodes[i].ID}
}
func lineageQueryMapping(t *testing.T, s *RevisionState, id int, sources []LineageValueRef, dest LineageValueRef, kind string) {
	t.Helper()
	status := "complete"
	gaps := []string{}
	if kind == "unknown_transform" {
		status = "unsupported"
		gaps = []string{"unknown"}
	}
	a := LineageMappingAttributes{Sources: sources, Destination: dest, Transform: LineageTransform{Kind: kind, Description: "inert"}, AnalysisStatus: status, Gaps: gaps}
	raw := relationalRaw(t, a)
	var attrs map[string]jsontext.Value
	if err := json.Unmarshal(raw, &attrs); err != nil {
		t.Fatal(err)
	}
	n := Node{ID: lineageQueryID(10000 + id), Kind: "field_mapping", ParentID: new(s.Nodes[0].ID), Attributes: attrs, EvidenceIDs: []string{lineageQueryID(200000 + id)}}
	s.Nodes = append(s.Nodes, n)
	s.Evidence = append(s.Evidence, Evidence{ID: n.EvidenceIDs[0], SubjectID: n.ID, Status: "explicit"})
}
func lineageQueryRun(t *testing.T, s *RevisionState, seed LineageValueRef, direction string, depth int) *LineagePage {
	t.Helper()
	p, err := projectLineage(t.Context(), s, LineageQueryInput{RevisionID: s.Revision.ID, Seed: seed, Direction: direction, MaxDepth: depth, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestLineageQueryHyperedgesAndWitnesses(t *testing.T) {
	s := lineageQueryState(t, 5)
	a, b, c, d, e := lineageQueryRef(s, 0), lineageQueryRef(s, 1), lineageQueryRef(s, 2), lineageQueryRef(s, 3), lineageQueryRef(s, 4)
	lineageQueryMapping(t, s, 2, []LineageValueRef{a}, c, "copy")
	lineageQueryMapping(t, s, 1, []LineageValueRef{a, b}, d, "aggregate")
	lineageQueryMapping(t, s, 3, []LineageValueRef{c}, d, "copy")
	lineageQueryMapping(t, s, 4, []LineageValueRef{d}, e, "copy")
	lineageQueryMapping(t, s, 5, []LineageValueRef{e}, a, "copy")
	p := lineageQueryRun(t, s, a, "forward", 8)
	if len(p.Items) != 5 || p.VisitedValueCount != 4 {
		t.Fatalf("co-input or cycle traversed: %+v", p)
	}
	want := []string{lineageQueryID(10001), lineageQueryID(10004)}
	if !slices.Equal(p.Items[3].WitnessMappingIDs, want) || p.Items[3].Via != d || p.Items[3].Depth != 2 {
		t.Fatalf("first shortest witness: %+v", p.Items[3])
	}
	if !slices.Equal(p.Items[0].ExpandedValues, []LineageValueRef{d}) {
		t.Fatalf("forward expands only destination %+v", p.Items[0])
	}
	rev := lineageQueryRun(t, s, d, "reverse", 8)
	if !slices.Equal(rev.Items[0].ExpandedValues, []LineageValueRef{a, b}) {
		t.Fatalf("reverse ordered inputs: %+v", rev.Items[0])
	}
}
func TestLineageQueryDepthAndConstants(t *testing.T) {
	for _, tt := range []struct {
		name      string
		more      bool
		kind      string
		boundary  bool
		truncated bool
	}{{"terminal", false, "copy", false, false}, {"depth", true, "copy", true, true}, {"constant", false, "constant", false, false}, {"empty unknown", false, "unknown_transform", true, false}} {
		t.Run(tt.name, func(t *testing.T) {
			s := lineageQueryState(t, 3)
			a, b, c := lineageQueryRef(s, 0), lineageQueryRef(s, 1), lineageQueryRef(s, 2)
			src := []LineageValueRef{a}
			direction := "forward"
			seed := a
			if tt.kind == "constant" || tt.kind == "unknown_transform" {
				src = []LineageValueRef{}
				direction = "reverse"
				seed = b
			}
			lineageQueryMapping(t, s, 1, src, b, tt.kind)
			if tt.more {
				lineageQueryMapping(t, s, 2, []LineageValueRef{b}, c, "copy")
			}
			p := lineageQueryRun(t, s, seed, direction, 1)
			if len(p.Items) != 1 || p.Items[0].Depth != 1 || (p.Items[0].Expansion == "boundary") != tt.boundary || p.Truncated != tt.truncated {
				t.Fatalf("depth semantics %+v", p)
			}
			if tt.boundary && len(p.Items[0].ExpandedValues) != 0 {
				t.Fatal("boundary crossed")
			}
		})
	}
}
func TestLineageQueryProofAndPathPropagation(t *testing.T) {
	for _, name := range []string{"explicit", "redaction", "mapping stale", "endpoint stale", "owner stale", "missing evidence", "inferred", "partial", "unsupported"} {
		t.Run(name, func(t *testing.T) {
			s := lineageQueryState(t, 3)
			a, b, c := lineageQueryRef(s, 0), lineageQueryRef(s, 1), lineageQueryRef(s, 2)
			lineageQueryMapping(t, s, 1, []LineageValueRef{a}, b, "copy")
			lineageQueryMapping(t, s, 2, []LineageValueRef{b}, c, "copy")
			m := &s.Nodes[3]
			wantStatus := "explicit"
			boundary := false
			review := false
			switch name {
			case "redaction":
				m.Attributes["transform"] = jsontext.Value(`{"kind":"copy","description":"redacted","redacted":true}`)
			case "mapping stale":
				m.Freshness = &AssertionFreshness{Status: "stale", Reasons: []string{"old"}}
				wantStatus = "stale"
				boundary = true
				review = true
			case "endpoint stale":
				s.Nodes[1].Freshness = &AssertionFreshness{Status: "stale"}
				wantStatus = "stale"
				boundary = true
				review = true
			case "owner stale":
				s.Nodes[0].Freshness = &AssertionFreshness{Status: "stale"}
				wantStatus = "stale"
				boundary = true
				review = true
			case "missing evidence":
				m.EvidenceIDs = []string{lineageQueryID(999999)}
				wantStatus = "unresolved"
				boundary = true
				review = true
			case "inferred":
				s.Evidence[3].Status = "inferred"
				wantStatus = "inferred"
				review = true
			case "partial":
				m.Attributes["analysisStatus"] = jsontext.Value(`"partial"`)
				m.Attributes["gaps"] = jsontext.Value(`["known partial"]`)
				review = true
			case "unsupported":
				m.Attributes["analysisStatus"] = jsontext.Value(`"unsupported"`)
				m.Attributes["gaps"] = jsontext.Value(`["unsupported"]`)
				m.Attributes["transform"] = jsontext.Value(`{"kind":"unknown_transform","description":"unknown","redacted":false}`)
				boundary = true
				review = true
			}
			p := lineageQueryRun(t, s, a, "forward", 8)
			if p.Items[0].Status != wantStatus || p.Items[0].RequiresReview != review || (p.Items[0].Expansion == "boundary") != boundary {
				t.Fatalf("proof combination %+v", p.Items[0])
			}
			if boundary {
				if len(p.Items) != 1 {
					t.Fatal("crossed boundary")
				}
			} else if p.Items[1].RequiresReview != review || p.Items[1].Status != wantStatus {
				t.Fatalf("lost path proof %+v", p.Items[1])
			}
		})
	}
}
func TestLineageQueryPaginationBindings(t *testing.T) {
	s := lineageQueryState(t, 4)
	a := lineageQueryRef(s, 0)
	for i := 1; i < 4; i++ {
		lineageQueryMapping(t, s, i, []LineageValueRef{a}, lineageQueryRef(s, i), "copy")
	}
	in := LineageQueryInput{RevisionID: s.Revision.ID, Seed: a, Direction: "forward", Limit: 1, MaxDepth: 8}
	p, err := projectLineage(t.Context(), s, in)
	if err != nil || len(p.Items) != 1 || p.NextCursor == "" {
		t.Fatalf("first page %+v %v", p, err)
	}
	cursor := p.NextCursor
	all := []LineageItem{p.Items[0]}
	for p.NextCursor != "" {
		in.Cursor = p.NextCursor
		p, err = projectLineage(t.Context(), s, in)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, p.Items...)
	}
	whole := lineageQueryRun(t, s, a, "forward", 8)
	if !slices.EqualFunc(all, whole.Items, func(a, b LineageItem) bool {
		return a.Mapping.ID == b.Mapping.ID && slices.Equal(a.WitnessMappingIDs, b.WitnessMappingIDs)
	}) {
		t.Fatal("pagination differs")
	}
	for _, name := range []string{"project", "revision", "hash", "seed", "facet", "port", "collection", "direction", "depth", "limit", "bad cursor"} {
		t.Run(name, func(t *testing.T) {
			cp := *s
			cp.Revision = s.Revision
			q := LineageQueryInput{RevisionID: s.Revision.ID, Seed: a, Direction: "forward", Limit: 1, MaxDepth: 8, Cursor: cursor}
			switch name {
			case "project":
				cp.Revision.ProjectID = lineageQueryID(9)
			case "revision":
				cp.Revision.ID = lineageQueryID(9)
				q.RevisionID = cp.Revision.ID
			case "hash":
				cp.Revision.SemanticHash = "changed"
			case "seed":
				q.Seed = lineageQueryRef(s, 1)
			case "facet":
				q.Seed.FacetKey = "sql"
			case "port":
				q.Seed.PortKey = "x"
			case "collection":
				q.Seed.Collection = "outputs"
			case "direction":
				q.Direction = "reverse"
			case "depth":
				q.MaxDepth = 2
			case "limit":
				q.Limit = 2
			case "bad cursor":
				q.Cursor = "garbage"
			}
			if _, err := projectLineage(t.Context(), &cp, q); err == nil {
				t.Fatal("changed cursor accepted")
			}
		})
	}
}
func TestLineageQueryInvalidAndUnsupported(t *testing.T) {
	s := lineageQueryState(t, 1)
	for _, schema := range []string{"1", "2", "3"} {
		t.Run(schema, func(t *testing.T) {
			cp := *s
			cp.Revision.SchemaVersion = schema
			_, err := projectLineage(t.Context(), &cp, LineageQueryInput{RevisionID: s.Revision.ID, Seed: lineageQueryRef(s, 0), Direction: "forward"})
			f, ok := errors.AsType[*FaultError](err)
			if !ok || f.Status != 422 {
				t.Fatalf("unsupported %v", err)
			}
		})
	}
	for _, raw := range []string{`{}`, `{"revisionId":"x","seed":{},"direction":"forward"}`, `{"revisionId":"00000000-0000-4000-8000-000000000001","seed":{"kind":"api_field","nodeId":"00000000-0000-4000-8000-000000000100"},"direction":"forward","maxDepth":0}`, `{"revisionId":"00000000-0000-4000-8000-000000000001","seed":{"kind":"api_field","nodeId":"00000000-0000-4000-8000-000000000100"},"direction":"forward","limit":null}`, `{"revisionId":"00000000-0000-4000-8000-000000000001","seed":{"kind":"api_field","nodeId":"00000000-0000-4000-8000-000000000100"},"direction":"forward","proposal":{}}`} {
		var in LineageQueryInput
		if json.Unmarshal([]byte(raw), &in) == nil {
			t.Fatalf("invalid accepted %s", raw)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := projectLineage(ctx, s, LineageQueryInput{RevisionID: s.Revision.ID, Seed: lineageQueryRef(s, 0), Direction: "forward"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored %v", err)
	}
	if _, err := projectLineage(t.Context(), s, LineageQueryInput{Seed: lineageQueryRef(s, 0), Direction: "forward"}); err == nil {
		t.Fatal("implicit head")
	}
}

func TestLineageQueryGlobalBudgets(t *testing.T) {
	for _, name := range []string{"mapping_limit", "value_limit", "reference_limit"} {
		t.Run(name, func(t *testing.T) {
			count := 2
			if name == "value_limit" {
				count = 5121
			}
			if name == "reference_limit" {
				count = 65
			}
			s := lineageQueryState(t, count)
			seed := lineageQueryRef(s, 0)
			direction := "forward"
			switch name {
			case "mapping_limit":
				for i := 0; i < 5001; i++ {
					lineageQueryMapping(t, s, i, []LineageValueRef{seed}, lineageQueryRef(s, 1), "copy")
				}
			case "value_limit":
				direction = "reverse"
				for i := 0; i < 80; i++ {
					sources := []LineageValueRef{}
					for j := 0; j < 64; j++ {
						sources = append(sources, lineageQueryRef(s, 1+i*64+j))
					}
					lineageQueryMapping(t, s, i, sources, seed, "aggregate")
				}
			case "reference_limit":
				direction = "reverse"
				sources := []LineageValueRef{}
				for i := 1; i <= 64; i++ {
					sources = append(sources, lineageQueryRef(s, i))
				}
				for i := 0; i < 309; i++ {
					lineageQueryMapping(t, s, i, sources, seed, "aggregate")
				}
			}
			p := lineageQueryRun(t, s, seed, direction, 8)
			if !p.Truncated || !slices.Equal(p.TruncationReasons, []string{name}) {
				t.Fatalf("independent %s %+v", name, p.TruncationReasons)
			}
			if p.VisitedValueCount > 5000 || p.ExaminedMappingCount > 5000 {
				t.Fatal("budget exceeded")
			}
			if name == "mapping_limit" && p.ExaminedMappingCount != 5000 {
				t.Fatal("examined count")
			}
			if name == "value_limit" && p.VisitedValueCount != 4993 {
				t.Fatalf("expected atomic boundary at 4993 got %d", p.VisitedValueCount)
			}
		})
	}
}

type lineageCancelContext struct {
	context.Context
	calls    int
	cancelAt int
}

func (c *lineageCancelContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
func TestLineageQueryCancellationDuringWork(t *testing.T) {
	s := lineageQueryState(t, 3)
	a, b, c := lineageQueryRef(s, 0), lineageQueryRef(s, 1), lineageQueryRef(s, 2)
	lineageQueryMapping(t, s, 1, []LineageValueRef{a}, b, "copy")
	lineageQueryMapping(t, s, 2, []LineageValueRef{b}, c, "copy")
	// Fixed cancellation points independently exercise node indexing and mapping proof traversal.
	for _, tt := range []struct {
		name string
		at   int
	}{{"indexing", 3}, {"traversal", 30}} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &lineageCancelContext{Context: t.Context(), cancelAt: tt.at}
			_, err := projectLineage(ctx, s, LineageQueryInput{RevisionID: s.Revision.ID, Seed: a, Direction: "forward"})
			if !errors.Is(err, context.Canceled) || ctx.calls < tt.at {
				t.Fatalf("cancellation ignored calls=%d err=%v", ctx.calls, err)
			}
		})
	}
}
func TestLineageQuerySelectedFacetProof(t *testing.T) {
	for _, name := range []string{"selected stale", "other stale", "selected missing", "selected unsupported"} {
		t.Run(name, func(t *testing.T) {
			s := lineageQueryState(t, 2)
			s.Nodes[1].Kind = "column"
			selected := `{"analysisStatus":"complete","gaps":[],"evidenceIds":["` + s.Nodes[1].EvidenceIDs[0] + `"]}`
			other := `{"analysisStatus":"complete","gaps":[],"freshness":{"status":"stale"}}`
			boundary := name != "other stale"
			switch name {
			case "selected stale":
				selected = `{"analysisStatus":"complete","gaps":[],"evidenceIds":["` + s.Nodes[1].EvidenceIDs[0] + `"],"freshness":{"status":"stale"}}`
			case "selected missing":
				selected = `{"analysisStatus":"complete","gaps":[],"evidenceIds":["missing"]}`
			case "selected unsupported":
				selected = `{"analysisStatus":"unsupported","gaps":["facet unsupported"],"evidenceIds":["` + s.Nodes[1].EvidenceIDs[0] + `"]}`
			}
			s.Nodes[1].Attributes = map[string]jsontext.Value{"facets": jsontext.Value(`{"sql":` + selected + `,"other":` + other + `}`)}
			a := lineageQueryRef(s, 0)
			dest := LineageValueRef{Kind: "column", NodeID: s.Nodes[1].ID, FacetKey: "sql"}
			lineageQueryMapping(t, s, 1, []LineageValueRef{a}, dest, "copy")
			p := lineageQueryRun(t, s, a, "forward", 8)
			if (p.Items[0].Expansion == "boundary") != boundary {
				t.Fatalf("selected facet proof %+v", p.Items[0])
			}
		})
	}
}
func TestLineageQueryFullValueIdentityAndUnknownAccess(t *testing.T) {
	s := lineageQueryState(t, 3)
	s.Nodes[0].Kind = "flow_step"
	s.Nodes[0].Attributes = map[string]jsontext.Value{"inputs": jsontext.Value(`[{"key":"x"},{"key":"y"}]`), "outputs": jsontext.Value(`[{"key":"x"}]`), "analysisStatus": jsontext.Value(`"complete"`)}
	input := LineageValueRef{Kind: "port", NodeID: s.Nodes[0].ID, Collection: "inputs", PortKey: "x"}
	other := input
	other.PortKey = "y"
	output := input
	output.Collection = "outputs"
	lineageQueryMapping(t, s, 1, []LineageValueRef{input}, lineageQueryRef(s, 1), "copy")
	lineageQueryMapping(t, s, 2, []LineageValueRef{other}, lineageQueryRef(s, 2), "copy")
	lineageQueryMapping(t, s, 3, []LineageValueRef{output}, lineageQueryRef(s, 2), "copy")
	s.Edges = []Edge{{ID: lineageQueryID(999), Kind: "reads", Attributes: map[string]jsontext.Value{"columnScope": jsontext.Value(`"unknown"`)}}}
	p := lineageQueryRun(t, s, input, "forward", 8)
	if len(p.Items) != 1 || p.Items[0].Mapping.ID != lineageQueryID(10001) || !slices.ContainsFunc(p.Limitations, func(l string) bool { return strings.Contains(l, "Unknown table column access") }) {
		t.Fatalf("full port identity or unknown table accesses %+v", p)
	}
}
func TestLineageQueryOrdersPinnedWitnesses(t *testing.T) {
	r, out, _, ids := lineageOrdersCommitted(t)
	raw, err := os.ReadFile("testdata/lineage/orders/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Request        ImportLineageValueRef `json:"request"`
		Amount         ImportLineageValueRef `json:"amountColumn"`
		Response       ImportLineageValueRef `json:"response"`
		RequestWitness []string              `json:"requestColumnWitness"`
		AmountWitness  []string              `json:"amountResponseWitness"`
		TaxWitness     []string              `json:"reverseTaxWitness"`
	}
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	convert := func(ref ImportLineageValueRef) LineageValueRef {
		return LineageValueRef{Kind: ref.Kind, NodeID: ids[ref.NodeKey], FacetKey: ref.FacetKey, Collection: ref.Collection, PortKey: ref.PortKey}
	}
	for _, tt := range []struct {
		name      string
		seed      ImportLineageValueRef
		direction string
		path      []string
	}{{"request column", expected.Request, "forward", expected.RequestWitness}, {"amount response", expected.Amount, "forward", expected.AmountWitness}, {"reverse tax", expected.Response, "reverse", expected.TaxWitness}} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := r.QueryLineage(t.Context(), out.Project.ID, LineageQueryInput{RevisionID: out.Revision.ID, Seed: convert(tt.seed), Direction: tt.direction, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{}
			for _, key := range tt.path {
				want = append(want, ids[key])
			}
			item := slices.IndexFunc(p.Items, func(i LineageItem) bool { return i.Mapping.ID == want[len(want)-1] })
			if item < 0 || !slices.Equal(p.Items[item].WitnessMappingIDs, want) {
				t.Fatalf("independent expected witness %v items %+v", want, p.Items)
			}
			if p.RevisionID != out.Revision.ID || p.ProjectID != out.Project.ID || p.SemanticHash != out.Revision.SemanticHash || p.Policy != LineageTraversalPolicy {
				t.Fatal("lost immutable pin")
			}
			n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, want[len(want)-1])
			if err != nil {
				t.Fatal(err)
			}
			got, _ := canonicalJSON(p.Items[item].Mapping)
			full, _ := canonicalJSON(n)
			if string(got) != string(full) {
				t.Fatal("mapping was tailored to seed")
			}
		})
	}
	if _, err := r.QueryLineage(t.Context(), lineageQueryID(77), LineageQueryInput{RevisionID: out.Revision.ID, Seed: convert(expected.Request), Direction: "forward"}); err == nil {
		t.Fatal("foreign project accepted")
	}
	if _, err := r.QueryLineage(t.Context(), out.Project.ID, LineageQueryInput{Seed: convert(expected.Request), Direction: "forward"}); err == nil {
		t.Fatal("implicitly resolved latest")
	}
}

func TestLineageQueryCursorBindsValidFullAddresses(t *testing.T) {
	for _, kind := range []string{"port", "column"} {
		t.Run(kind, func(t *testing.T) {
			s := lineageQueryState(t, 3)
			a := LineageValueRef{NodeID: s.Nodes[0].ID}
			var alternatives []LineageValueRef
			if kind == "port" {
				s.Nodes[0].Kind = "flow_step"
				s.Nodes[0].Attributes = map[string]jsontext.Value{"inputs": jsontext.Value(`[{"key":"x"},{"key":"y"}]`), "outputs": jsontext.Value(`[{"key":"x"}]`)}
				a.Kind = "port"
				a.Collection = "inputs"
				a.PortKey = "x"
				b := a
				b.PortKey = "y"
				c := a
				c.Collection = "outputs"
				alternatives = []LineageValueRef{b, c}
			} else {
				s.Nodes[0].Kind = "column"
				facet := `{"analysisStatus":"complete","gaps":[],"evidenceIds":["` + s.Nodes[0].EvidenceIDs[0] + `"]}`
				s.Nodes[0].Attributes = map[string]jsontext.Value{"facets": jsontext.Value(`{"sql":` + facet + `,"other":` + facet + `}`)}
				a.Kind = "column"
				a.FacetKey = "sql"
				b := a
				b.FacetKey = "other"
				alternatives = []LineageValueRef{b}
			}
			for i := 1; i <= 2; i++ {
				lineageQueryMapping(t, s, i, []LineageValueRef{a}, lineageQueryRef(s, i), "copy")
			}
			in := LineageQueryInput{RevisionID: s.Revision.ID, Seed: a, Direction: "forward", Limit: 1}
			p, err := projectLineage(t.Context(), s, in)
			if err != nil || p.NextCursor == "" {
				t.Fatalf("initial %v %+v", err, p)
			}
			for _, ref := range alternatives {
				in.Seed = ref
				in.Cursor = ""
				if _, err := projectLineage(t.Context(), s, in); err != nil {
					t.Fatalf("alternative must be valid %+v %v", ref, err)
				}
				in.Cursor = p.NextCursor
				_, err = projectLineage(t.Context(), s, in)
				f, ok := errors.AsType[*FaultError](err)
				if !ok || f.Status != 400 || f.Details["field"] != "cursor" {
					t.Fatalf("address not cursor-bound %v", err)
				}
			}
		})
	}
}
func TestLineageQueryMissingAndInvalidTargets(t *testing.T) {
	s := lineageQueryState(t, 1)
	a := lineageQueryRef(s, 0)
	for _, tt := range []struct {
		name   string
		edit   func(*LineageQueryInput)
		status int
	}{{"missing node", func(in *LineageQueryInput) { in.Seed.NodeID = lineageQueryID(99) }, 404}, {"missing revision", func(in *LineageQueryInput) { in.RevisionID = lineageQueryID(99) }, 404}, {"wrong kind", func(in *LineageQueryInput) { in.Seed.Kind = "column"; in.Seed.FacetKey = "sql" }, 400}, {"invalid facet", func(in *LineageQueryInput) { in.Seed.FacetKey = "sql" }, 400}, {"bad port", func(in *LineageQueryInput) {
		in.Seed.Kind = "port"
		in.Seed.Collection = "inputs"
		in.Seed.PortKey = "absent"
	}, 400}, {"bad direction", func(in *LineageQueryInput) { in.Direction = "both" }, 400}, {"negative depth", func(in *LineageQueryInput) { in.MaxDepth = -1 }, 400}, {"excess limit", func(in *LineageQueryInput) { in.Limit = 101 }, 400}} {
		t.Run(tt.name, func(t *testing.T) {
			in := LineageQueryInput{RevisionID: s.Revision.ID, Seed: a, Direction: "forward"}
			tt.edit(&in)
			_, err := projectLineage(t.Context(), s, in)
			f, ok := errors.AsType[*FaultError](err)
			if !ok || f.Status != tt.status {
				t.Fatalf("expected %d got %v", tt.status, err)
			}
		})
	}
	p := lineageQueryRun(t, s, a, "forward", 0)
	if len(p.Items) != 0 || p.NextCursor != "" || p.Truncated || !slices.Contains(p.Limitations, "provider limitation") || !slices.ContainsFunc(p.Limitations, func(l string) bool { return strings.Contains(l, "No imported mapping") }) {
		t.Fatalf("empty scope claim %+v", p)
	}
}

func TestLineageQueryDefaultsAndTruncatedPagination(t *testing.T) {
	s := lineageQueryState(t, 12)
	for i := 0; i < 10; i++ {
		lineageQueryMapping(t, s, i, []LineageValueRef{lineageQueryRef(s, i)}, lineageQueryRef(s, i+1), "copy")
	}
	in := LineageQueryInput{RevisionID: s.Revision.ID, Seed: lineageQueryRef(s, 0), Direction: "forward", Limit: 3}
	p, err := projectLineage(t.Context(), s, in)
	if err != nil {
		t.Fatal(err)
	}
	items := []LineageItem{}
	for {
		if !p.Truncated || !slices.Equal(p.TruncationReasons, []string{"depth_limit"}) {
			t.Fatal("page lost global truncation")
		}
		items = append(items, p.Items...)
		if p.NextCursor == "" {
			break
		}
		in.Cursor = p.NextCursor
		p, err = projectLineage(t.Context(), s, in)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(items) != 8 || items[7].Depth != 8 || items[7].Expansion != "boundary" || len(items[7].ExpandedValues) != 0 {
		t.Fatalf("default depth %+v", items)
	}
	s = lineageQueryState(t, 2)
	seed := lineageQueryRef(s, 0)
	for i := 0; i < 51; i++ {
		lineageQueryMapping(t, s, i, []LineageValueRef{seed}, lineageQueryRef(s, 1), "copy")
	}
	p, err = projectLineage(t.Context(), s, LineageQueryInput{RevisionID: s.Revision.ID, Seed: seed, Direction: "forward"})
	if err != nil || len(p.Items) != 50 || p.NextCursor == "" || p.Truncated || p.ExaminedMappingCount != 51 {
		t.Fatalf("default limit changes graph analysis %+v %v", p, err)
	}
}
func TestLineageQueryStorageOrderIndependence(t *testing.T) {
	s := lineageQueryState(t, 4)
	a, b, c, d := lineageQueryRef(s, 0), lineageQueryRef(s, 1), lineageQueryRef(s, 2), lineageQueryRef(s, 3)
	lineageQueryMapping(t, s, 3, []LineageValueRef{c}, d, "copy")
	lineageQueryMapping(t, s, 2, []LineageValueRef{a}, c, "copy")
	lineageQueryMapping(t, s, 1, []LineageValueRef{a}, b, "copy")
	lineageQueryMapping(t, s, 4, []LineageValueRef{b}, d, "copy")
	first := lineageQueryRun(t, s, a, "forward", 8)
	slices.Reverse(s.Nodes)
	slices.Reverse(s.Evidence)
	slices.Reverse(s.Edges)
	second := lineageQueryRun(t, s, a, "forward", 8)
	raw1, _ := canonicalJSON(first)
	raw2, _ := canonicalJSON(second)
	if string(raw1) != string(raw2) {
		t.Fatal("result depends on storage order")
	}
	for _, item := range second.Items {
		if item.Mapping.ID == lineageQueryID(10003) && !slices.Equal(item.WitnessMappingIDs, []string{lineageQueryID(10002), lineageQueryID(10003)}) {
			t.Fatalf("unexpected independent diamond witness %+v", item)
		}
	}
}

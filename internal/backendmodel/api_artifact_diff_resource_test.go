package backendmodel

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestAPIArtifactDiffLosslessNumberHashGoldens(t *testing.T) {
	// Independent literals freeze the existing RFC8785 number hash domain.
	// Decode retains the original lexeme, while canonical hashing intentionally
	// keeps the established IEEE754 normalization (including the unsafe integer).
	for _, test := range []struct{ raw, canonical string }{
		{"1.25e2", "125"},
		{"-0.000", "0"},
		{"9007199254740991", "9007199254740991"},
		{"9007199254740993", "9007199254740992"},
		{"10000000000000000000000", "1e+22"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			value, err := decodeAPIArtifactDiffValue(t.Context(), test.raw)
			if err != nil {
				t.Fatal(err)
			}
			number, ok := value.(jsonx.Number)
			if !ok || string(number) != test.raw {
				t.Fatalf("number lexeme lost: %#v", value)
			}
			got, err := hashAPIArtifactDiffValue(value, true)
			want := fmt.Sprintf("%x", sha256.Sum256([]byte(test.canonical)))
			if err != nil || got != want {
				t.Fatalf("numeric canonical hash %s want%s err%v", got, want, err)
			}
		})
	}
	value, err := decodeAPIArtifactDiffValue(t.Context(), `{"z":1.25e2,"a":[9007199254740991,1.00]}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := hashAPIArtifactDiffValue(value, true)
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"a":[9007199254740991,1],"z":125}`)))
	if err != nil || got != want {
		t.Fatalf("container numeric canonical hash %s want%s err%v", got, want, err)
	}
}

func TestAPIArtifactDiffDeepScalarAllocationBound(t *testing.T) {
	// About64KiB of combined input and40 nested objects reproduce the ancestor
	// copying problem without approaching owner MaxBody or host memory limits.
	makeDocument := func(value string) string {
		raw, _ := json.Marshal(map[string]any{"description": value})
		doc := string(raw)
		for range 20 {
			doc = `{"properties":{"x":` + doc + `}}`
		}
		return doc
	}
	before, after := makeDocument(strings.Repeat("A", 32<<10)), makeDocument(strings.Repeat("B", 32<<10))
	runtime.GC()
	var start, end runtime.MemStats
	runtime.ReadMemStats(&start)
	changes, truncated, err := apiArtifactObjectDiff(t.Context(), before, after, 1000)
	runtime.ReadMemStats(&end)
	if err != nil || truncated || len(changes) != 1 {
		t.Fatalf("deep scalar diff changes%d truncated%v err%v", len(changes), truncated, err)
	}
	allocated := end.TotalAlloc - start.TotalAlloc
	// A broad linear bound permits decode, encoding/hash buffers and map/sort
	// overhead; retaining copies at each ancestor exceeds it substantially.
	bound := uint64(24*(len(before)+len(after)) + (512 << 10))
	t.Logf("combined input=%d allocated=%d bound=%d", len(before)+len(after), allocated, bound)
	if allocated > bound {
		t.Fatalf("deep scalar allocation amplification: %d bytes > %d", allocated, bound)
	}
}

func TestAPIArtifactDiffGeneratedPathBound(t *testing.T) {
	// This mixed key has2045 escaped UTF-8 bytes:2040Unicode+4escapes+1ASCII.
	key := strings.Repeat("界", 680) + "~/x"
	document := func(key string, value any) string {
		raw, _ := json.Marshal(map[string]any{"p": map[string]any{key: value}})
		return string(raw)
	}
	for _, test := range []struct {
		name, key string
		truncated bool
	}{{"exact2048", key, false}, {"over2048", key + "x", true}, {"largeEscapedKey", strings.Repeat("界~/", 512), true}} {
		t.Run(test.name, func(t *testing.T) {
			changes, truncated, err := apiArtifactObjectDiff(t.Context(), document(test.key, false), document(test.key, true), 1000)
			if err != nil || truncated != test.truncated {
				t.Fatalf("path truncation%v want%v err%v", truncated, test.truncated, err)
			}
			for _, c := range changes {
				if len(c.Pointer) > 2048 {
					t.Fatalf("retained oversized pointer: %d bytes", len(c.Pointer))
				}
			}
			if !test.truncated && (len(changes) != 1 || len(changes[0].Pointer) != 2048) {
				t.Fatalf("threshold changes %+v", changes)
			}
		})
	}
	large := strings.Repeat("界~/", 512)
	values := map[string]any{}
	for i := range 100 {
		values[strconv.Itoa(i)] = false
	}
	before := document(large, values)
	for k := range values {
		values[k] = true
	}
	changes, truncated, err := apiArtifactObjectDiff(t.Context(), before, document(large, values), 1000)
	if err != nil || !truncated {
		t.Fatalf("fan-out oversized paths accepted: changes%d truncated%v err%v", len(changes), truncated, err)
	}
	encoded, _ := json.Marshal(changes)
	if len(encoded) > 64<<10 {
		t.Fatalf("oversized path output %d", len(encoded))
	}
	// Identical unrepresentable paths do not invent missing diff coverage.
	changes, truncated, err = apiArtifactObjectDiff(t.Context(), before, before, 1000)
	if err != nil || truncated || len(changes) != 0 {
		t.Fatalf("unchanged large key truncated %+v %v %v", changes, truncated, err)
	}
}

func TestAPIArtifactDiffArrayPathThresholdAndNumbers(t *testing.T) {
	key := strings.Repeat("界", 680) + "~/x" // root prefix2046bytes; /0 fits2048.
	document := func(values []any) string { raw, _ := json.Marshal(map[string]any{key: values}); return string(raw) }
	changes, truncated, err := apiArtifactObjectDiff(t.Context(), document([]any{false}), document([]any{true}), 1000)
	if err != nil || truncated || len(changes) != 1 || len(changes[0].Pointer) != 2048 {
		t.Fatalf("array threshold changes%d trunc%v err%v", len(changes), truncated, err)
	}
	left, right := make([]any, 11), make([]any, 11)
	for i := range left {
		left[i] = false
		right[i] = false
	}
	right[10] = true
	changes, truncated, err = apiArtifactObjectDiff(t.Context(), document(left), document(right), 1000)
	if err != nil || !truncated {
		t.Fatalf("array /10 overflow accepted %+v %v %v", changes, truncated, err)
	}
	changes, truncated, err = apiArtifactObjectDiff(t.Context(), `{"n":9007199254740992,"equivalent":1.00}`, `{"equivalent":1e0,"n":9007199254740993}`, 1000)
	if err != nil || truncated || len(changes) != 1 || changes[0].Pointer != "/n" {
		t.Fatalf("lossless numeric comparison %+v %v %v", changes, truncated, err)
	}
	changes, truncated, err = apiArtifactObjectDiff(t.Context(), `{"a":[1,2],"x/y~":false}`, `{"x/y~":true,"a":[2,1]}`, 1000)
	if err != nil || truncated || len(changes) != 3 || changes[0].Pointer != "/a/0" || changes[1].Pointer != "/a/1" || changes[2].Pointer != "/x~1y~0" {
		t.Fatalf("small deterministic diff %+v %v %v", changes, truncated, err)
	}
}

type diffCancelContext struct {
	context.Context
	checks, after int
}

func (c *diffCancelContext) Err() error {
	c.checks++
	if c.checks > c.after {
		return context.Canceled
	}
	return nil
}
func TestAPIArtifactDiffCancellationDuringTraversal(t *testing.T) {
	before := `[` + strings.Repeat("false,", 500) + `false]`
	after := `[` + strings.Repeat("false,", 500) + `true]`
	ctx := &diffCancelContext{Context: t.Context(), after: 100}
	_, _, err := apiArtifactObjectDiff(ctx, before, after, 1000)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("uncancelable shared-tree traversal %v", err)
	}
}

func TestAPIArtifactPreviewPathOverflowDisablesApply(t *testing.T) {
	t.Parallel()
	s, base, ids, api := apiPinFixture(t)
	owner := s.artifacts.(*apidesign.Repo)
	key := strings.Repeat("界~/", 512)
	schema := func(value bool) string {
		raw, _ := json.Marshal(map[string]any{key: map[string]any{"type": "object", "properties": map[string]any{"flag": value}}})
		return string(raw)
	}
	document := func(value bool) string {
		return strings.Replace(artifactTestDocument, `"properties":{"total":{"type":"number"},"secret":{"type":"string"}}`, `"properties":`+schema(value), 1)
	}
	initial, err := owner.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: api.Design.Version, Document: document(false), Summary: "Long key", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := pinTestInput(base, ids, initial)
	in.Commands[0].Bindings = []APIPinBindingInput{{SourceNodeID: ids["request"], Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order"}}}
	pinned, _ := applyPinTest(t, s, base.Project.ID, in, "long-key")
	changed, err := owner.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: initial.Design.Version, Document: document(true), Summary: "Change below long key", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in.BaseRevisionID = pinned.Revision.ID
	in.ExpectedVersion = pinned.Project.Version
	in.Commands[0].RevisionID = strconv.FormatInt(changed.Draft.ID, 10)
	preview, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil || !preview.DiffTruncated || preview.CanApply || !slices.ContainsFunc(preview.Diagnostics, func(d APIArtifactDiagnostic) bool { return d.Code == "backend_api_diff_truncated" && d.Pointer == "" }) {
		t.Fatalf("unbounded applicable preview truncated%v canApply%v err%v", preview != nil && preview.DiffTruncated, preview != nil && preview.CanApply, err)
	}
	raw, _ := json.Marshal(preview)
	if len(raw) > 64<<10 {
		t.Fatalf("unbounded preview output %d", len(raw))
	}
	_, err = s.Apply(t.Context(), base.Project.ID, ApplyAPIPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, preview.CandidateHash, "long-key-apply"})
	assertFault(t, err, "backend_api_pins_blocked")
}

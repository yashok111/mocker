package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"
)

func incrementalFile(path, hash string) ManifestFile {
	return ManifestFile{Path: path, ContentHash: strings.Repeat(hash, 64), FileType: "go", AnalysisStatus: "analyzed"}
}

func TestIncrementalManifestCompleteDiff(t *testing.T) {
	before := SourceSnapshot{SnapshotManifest: SnapshotManifest{Files: []ManifestFile{incrementalFile("old.go", "a"), incrementalFile("edit.go", "a"), incrementalFile("same.go", "a")}}}
	after := SnapshotManifest{Files: []ManifestFile{incrementalFile("new.go", "b"), incrementalFile("edit.go", "b"), incrementalFile("same.go", "a")}}
	good := ChangeManifest{Scope: "affected-subgraph", Files: []ChangeManifestFile{
		{Kind: "added", Path: "new.go", AfterHash: strings.Repeat("b", 64)},
		{Kind: "modified", Path: "edit.go", BeforeHash: strings.Repeat("a", 64), AfterHash: strings.Repeat("b", 64)},
		{Kind: "deleted", Path: "old.go", BeforeHash: strings.Repeat("a", 64)},
	}, AffectedRoots: []SourceSubjectRef{}}
	tests := []struct {
		name   string
		mutate func(*ChangeManifest)
		valid  bool
	}{
		{name: "complete diff", valid: true},
		{name: "missing modified", mutate: func(c *ChangeManifest) { c.Files = append(c.Files[:1], c.Files[2:]...) }},
		{name: "invented modified", mutate: func(c *ChangeManifest) {
			c.Files = append(c.Files, ChangeManifestFile{Kind: "modified", Path: "same.go", BeforeHash: strings.Repeat("a", 64), AfterHash: strings.Repeat("b", 64)})
		}},
		{name: "duplicate", mutate: func(c *ChangeManifest) { c.Files = append(c.Files, c.Files[0]) }},
		{name: "normalized collision", mutate: func(c *ChangeManifest) {
			c.Files = append(c.Files, ChangeManifestFile{Kind: "added", Path: "./new.go", AfterHash: strings.Repeat("b", 64)})
		}},
		{name: "wrong before", mutate: func(c *ChangeManifest) { c.Files[1].BeforeHash = strings.Repeat("c", 64) }},
		{name: "wrong after", mutate: func(c *ChangeManifest) { c.Files[0].AfterHash = strings.Repeat("c", 64) }},
		{name: "added with before", mutate: func(c *ChangeManifest) { c.Files[0].BeforeHash = strings.Repeat("a", 64) }},
		{name: "deleted with after", mutate: func(c *ChangeManifest) { c.Files[2].AfterHash = strings.Repeat("a", 64) }},
		{name: "wrong tag", mutate: func(c *ChangeManifest) { c.Files[1].Kind = "added" }},
		{name: "wrong scope", mutate: func(c *ChangeManifest) { c.Scope = "whole" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changes := good
			changes.Files = slices.Clone(good.Files)
			if test.mutate != nil {
				test.mutate(&changes)
			}
			err := ValidateChangeManifest(before, after, changes)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}

func TestIncrementalManifestAvailability(t *testing.T) {
	before := SourceSnapshot{SnapshotManifest: SnapshotManifest{Files: []ManifestFile{incrementalFile("a.go", "a"), incrementalFile("b.go", "b")}}}
	after := before.SnapshotManifest
	after.Files = slices.Clone(before.Files)
	after.Files[0].AnalysisStatus = "unsupported"
	after.Files[0].Reason = "parser unavailable"
	after.Files[1].FileType = "sql"
	changes := ChangeManifest{Scope: "affected-subgraph", Files: []ChangeManifestFile{}, AffectedRoots: []SourceSubjectRef{}}
	if err := ValidateChangeManifest(before, after, changes); err != nil {
		t.Fatal(err)
	}
	actual := incrementalChangedFiles(before, after)
	if !actual["a.go"] || !actual["b.go"] || len(actual) != 2 {
		t.Fatalf("availability changes=%v", actual)
	}
}

func TestIncrementalManifestStrictWire(t *testing.T) {
	for _, raw := range []string{
		`{"scope":"affected-subgraph","files":[{"kind":"added","path":"a.go","afterHash":"` + strings.Repeat("a", 64) + `","beforeHash":""}],"affectedRoots":[]}`,
		`{"scope":"affected-subgraph","files":[],"affectedRoots":[],"extra":true}`,
		`{"scope":"affected-subgraph","files":[],"affectedRoots":[{"recordType":"facet","id":"a"}]}`,
	} {
		var c ChangeManifest
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			t.Fatalf("accepted invalid change manifest %s", raw)
		}
	}
}

func TestIncrementalManifestArraysRequired(t *testing.T) {
	for _, raw := range []string{
		`{"scope":"affected-subgraph","affectedRoots":[]}`,
		`{"scope":"affected-subgraph","files":null,"affectedRoots":[]}`,
		`{"scope":"affected-subgraph","files":[]}`,
	} {
		var changes ChangeManifest
		if err := json.Unmarshal([]byte(raw), &changes); err == nil {
			t.Fatalf("accepted missing array: %s", raw)
		}
	}
}

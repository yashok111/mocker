package backendmodel

import (
	"encoding/json/v2"
	"fmt"
	"testing"
)

func TestCompactCoverageSummaryAndCompleteManifestPages(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "compact-coverage")
	input := firstImportFixture(p)
	for i := range 2000 {
		input.Manifest.Snapshot.Files = append(input.Manifest.Snapshot.Files, ManifestFile{Path: fmt.Sprintf("docs/%04d.txt", i), ContentHash: fixtureHash, FileType: "text", AnalysisStatus: "unsupported", Reason: "Not analyzed"})
	}
	s, err := r.BeginImport(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	result := commitStaged(t, r, p, s, b.AcceptedVersion, "commit")
	in := CoverageQueryInput{RevisionID: result.Revision.ID, Section: "summary", Limit: 100}
	page, err := r.QueryCoverage(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 || page.Summary.FileEntries != 2001 || page.Summary.RevisionID != result.Revision.ID || page.Summary.SemanticHash != result.Revision.SemanticHash {
		t.Fatalf("summary expanded or lost pins: %d %+v", len(raw), page.Summary)
	}
	in.Section, in.SnapshotID = "files", s.SnapshotID
	seen := map[string]bool{}
	for {
		page, err = r.QueryCoverage(t.Context(), p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.File.Path] {
				t.Fatal("duplicate manifest entry")
			}
			seen[item.File.Path] = true
		}
		if page.NextCursor == "" {
			break
		}
		in.Cursor = page.NextCursor
	}
	if len(seen) != 2001 {
		t.Fatalf("manifest union=%d", len(seen))
	}
	in.Cursor = ""
	first, err := r.QueryCoverage(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Cursor, in.Section = first.NextCursor, "snapshots"
	if _, err := r.QueryCoverage(t.Context(), p.ID, in); err == nil {
		t.Fatal("coverage cursor crossed section")
	}
}

func TestCompactSource6CoverageDoesNotResolveGraphPayloads(t *testing.T) {
	t.Parallel()
	r, base, _ := effectiveRepresentationFixture(t)
	if _, err := r.db.W.Exec(`DROP VIEW backend_graph_records_documents`); err != nil {
		t.Fatal(err)
	}
	page, err := r.QueryCoverage(t.Context(), base.Project.ID, CoverageQueryInput{RevisionID: base.Revision.ID, Section: "summary", Limit: 100})
	if err != nil || page.Summary.ModelSchemaVersion != "6" {
		t.Fatalf("summary depends on full source graph: %+v %v", page, err)
	}
}

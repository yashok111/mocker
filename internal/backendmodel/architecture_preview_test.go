package backendmodel

import "testing"

func TestArchitecturePreviewDoesNotPublishAndPinsCursors(t *testing.T) {
	t.Parallel()
	r, pid, q := storedArchitectureFixture(t, 30, 90, 270)
	v, err := r.GetDiagram(t.Context(), pid, q.Pin)
	if err != nil {
		t.Fatal(err)
	}
	in := ArchitecturePreviewInput{Document: v.Document, Level: q.Level, RootID: q.RootID, Section: "gaps", Limit: 1, Origin: "all"}
	var before int
	if err := r.db.R.QueryRow(`SELECT count(*) FROM backend_diagrams WHERE project_id=?`, pid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	page, err := r.PreviewArchitecture(t.Context(), pid, in)
	if err != nil {
		t.Fatal(err)
	}
	if page.DocumentHash != v.Pin.ContentHash || page.TargetHash != v.TargetHash || page.GapSummary.ByCode["unresolved_membership"] != 30 || page.GapSummary.ByCode["authored_unresolved"] != 2 || page.NextCursor == "" {
		t.Fatalf("preview lost exact context: %+v", page)
	}
	in.Cursor = page.NextCursor
	in.Document.Payload.Elements[0].Label = "Changed preview"
	_, err = r.PreviewArchitecture(t.Context(), pid, in)
	assertFault(t, err, "backend_diagram_cursor_mismatch")
	var after int
	if err := r.db.R.QueryRow(`SELECT count(*) FROM backend_diagrams WHERE project_id=?`, pid).Scan(&after); err != nil || before != after {
		t.Fatalf("preview published a diagram: %d %d %v", before, after, err)
	}
}

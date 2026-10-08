package backendmodel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type cancelAfterFirstCheck struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelAfterFirstCheck) Err() error { err := c.Context.Err(); c.cancel(); return err }

func TestArchitecturePageChecksCancellationDuringFiltering(t *testing.T) {
	t.Parallel()
	v, g, _ := ordersProjectionFixture(t)
	in := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "elements", Limit: 1}
	p, err := projectArchitecture(t.Context(), v, g, in)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = architecturePage(cancelAfterFirstCheck{ctx, cancel}, v, p, in)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled page returned success: %v", err)
	}
}

func TestArchitectureCompactKeepsCompleteGapAndMemberPages(t *testing.T) {
	t.Parallel()
	v, g, _ := ordersProjectionFixture(t)
	for i := range 7121 {
		g.State.Edges = append(g.State.Edges, Edge{ID: fmt.Sprintf("90000000-0000-4000-8000-%012d", i), From: "unmapped", To: "unmapped", Kind: "calls"})
	}
	q := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "links", Limit: 2}
	legacy, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(q)
	var input map[string]any
	_ = json.Unmarshal(raw, &input)
	input["responseMode"] = "compact-v1"
	raw, _ = json.Marshal(input)
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatalf("compact query refused: %v", err)
	}
	compact, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(compact.Gaps) != 0 || !reflect.DeepEqual(compact.Items, legacy.Items) {
		t.Fatal("compact response changes rows or repeats gaps")
	}
	raw, _ = json.Marshal(compact)
	var wire struct {
		GapSummary struct {
			Total  int            `json:"total"`
			ByCode map[string]int `json:"byCode"`
		} `json:"gapSummary"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.GapSummary.Total != len(legacy.Gaps) || wire.GapSummary.ByCode["unresolved_membership"] < 7121 {
		t.Fatalf("summary lost gaps: %+v", wire)
	}
	if len(raw) > 10000 {
		t.Fatalf("compact two-item page is %d bytes", len(raw))
	}
	q.Section, q.SubjectID = "members", legacy.Items[0].ArchitectureLink.ID
	members, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil || members.Total != 2 || len(members.Items) != 2 || len(members.Gaps) != 0 {
		t.Fatalf("members lost: %+v %v", members, err)
	}
	q.Section, q.SubjectID, q.Limit = "gaps", "", 500
	got := []DiagramGap{}
	for {
		page, err := ProjectArchitecture(t.Context(), v, g, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			got = append(got, *row.Gap)
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if !reflect.DeepEqual(got, legacy.Gaps) {
		t.Fatal("compact gap pagination lost or reordered evidence")
	}
}

func TestArchitectureCompactCursorCannotCrossResponseMode(t *testing.T) {
	t.Parallel()
	v, g, _ := ordersProjectionFixture(t)
	q := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "elements", Limit: 1}
	page, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("page=%+v %v", page, err)
	}
	raw, _ := json.Marshal(q)
	var input map[string]any
	_ = json.Unmarshal(raw, &input)
	input["responseMode"], input["cursor"] = "compact-v1", page.NextCursor
	raw, _ = json.Marshal(input)
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatal(err)
	}
	_, err = ProjectArchitecture(t.Context(), v, g, q)
	assertFault(t, err, "backend_diagram_cursor_mismatch")
}

func TestArchitectureCompactBoundsQualifiedHistoricalGapSummary(t *testing.T) {
	t.Parallel()
	v, g, _ := ordersProjectionFixture(t)
	for i := range 5000 {
		v.Gaps = append(v.Gaps, diagramGap(fmt.Sprint(i), fmt.Sprintf("historical_ref:%064d", i), "Historical member"))
	}
	page, err := ProjectArchitecture(t.Context(), v, g, DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "elements", Limit: 1, ResponseMode: "compact-v1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 || page.GapSummary.ByCode["historical_ref"] != 5000 {
		t.Fatalf("qualified gaps expanded summary: %d %+v", len(raw), page.GapSummary)
	}
}

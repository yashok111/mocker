package backendmodel

import (
	"encoding/json/v2"
	"testing"
	"uuid"
)

func TestAnnotationPagesFiltersAndCursorInvalidation(t *testing.T) {
	t.Parallel()
	r, p, _, target := annotationFixture(t)
	commands := make([]Command, 0, 5)
	for range 5 {
		commands = append(commands, annotationCommand(target, "note"))
	}
	p = annotationApply(t, r, p, "notes", commands...)
	cursor := ""
	previous := ""
	total := 0
	for _, want := range []int{2, 2, 1} {
		page, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{Limit: 2, Cursor: cursor})
		if err != nil || len(page.Items) != want || page.ProjectID != p.ID || page.ProjectVersion != p.Version {
			t.Fatalf("page: %+v %v", page, err)
		}
		for _, a := range page.Items {
			if a.ID <= previous || a.Target != target || a.TargetStatus != "current" || a.Author != "reviewer" {
				t.Fatalf("note: %+v", a)
			}
			previous = a.ID
			total++
		}
		cursor = page.NextCursor
	}
	if total != 5 || cursor != "" {
		t.Fatalf("pagination end: %d %q", total, cursor)
	}
	page, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []AnnotationListInput{{TargetID: target.ID}, {RecordType: "evidence"}, {RevisionID: "bad"}, {AnnotationID: "bad"}, {Limit: 501}, {Limit: -1}, {Cursor: "garbage"}, {Limit: 1, Cursor: page.NextCursor, Orphaned: new(false)}, {Limit: 1, Cursor: page.NextCursor, RecordType: "node"}} {
		_, err := r.ListAnnotations(t.Context(), p.ID, in)
		assertFault(t, err, "backend_invalid")
	}
	other := createProject(t, r, "other")
	_, err = r.ListAnnotations(t.Context(), other.ID, AnnotationListInput{Cursor: page.NextCursor})
	assertFault(t, err, "backend_invalid")
	for _, in := range []AnnotationListInput{{AnnotationID: commands[0].AnnotationID}, {RecordType: "node", TargetID: target.ID}, {Orphaned: new(false)}} {
		filtered, err := r.ListAnnotations(t.Context(), p.ID, in)
		if err != nil || len(filtered.Items) == 0 {
			t.Fatalf("filtered: %+v %v", filtered, err)
		}
	}
	for _, in := range []AnnotationListInput{{AnnotationID: uuid.NewV7().String()}, {RecordType: "edge"}, {Orphaned: new(true)}, {RevisionID: p.CurrentRevisionID}} {
		filtered, err := r.ListAnnotations(t.Context(), p.ID, in)
		if err != nil || len(filtered.Items) != 0 {
			t.Fatalf("empty filter: %+v %v", filtered, err)
		}
	}
	before, _ := json.Marshal(p)
	after, err := r.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(before) != string(afterJSON) {
		t.Fatal("reads mutated project")
	}
	p = annotationApply(t, r, p, "rename", Command{Type: "rename_project", Name: "Changed"})
	_, err = r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{Limit: 1, Cursor: page.NextCursor})
	f := assertFault(t, err, "backend_annotation_page_conflict")
	if f.Status != 409 || f.CurrentVersion != p.Version {
		t.Fatalf("cursor conflict: %+v", f)
	}
}

func TestAnnotationOrphanEditHistoricalBindingAndRestartReplay(t *testing.T) {
	t.Parallel()
	r, p, old, target := annotationFixture(t)
	bound := target
	bound.RevisionID = p.CurrentRevisionID
	note := annotationCommand(target, "original")
	historical := annotationCommand(bound, "history")
	p = annotationApply(t, r, p, "notes", note, historical)
	initial, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range initial.Items {
		if a.TargetStatus != "current" {
			t.Fatalf("initial status %+v", a)
		}
	}
	s := beginRepeat(t, r, p, old)
	batch := sendCommands(t, r, p, s, s.Version, "delete", ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "node", ExternalKey: "handler", ExpectedID: target.ID, Reason: "removed"}})
	committed := commitStaged(t, r, p, s, batch.AcceptedVersion, "delete-commit")
	p = &committed.Project
	headBefore := annotationSemanticBytes(t, r, p.ID)
	orphaned, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{Orphaned: new(true)})
	if err != nil || len(orphaned.Items) != 1 || orphaned.Items[0].ID != note.AnnotationID || orphaned.Items[0].Body != "original" {
		t.Fatalf("orphans: %+v %v", orphaned, err)
	}
	history, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{RevisionID: bound.RevisionID, Orphaned: new(false)})
	if err != nil || len(history.Items) != 1 || history.Items[0].TargetStatus != "historical" {
		t.Fatalf("history: %+v %v", history, err)
	}
	edit := note
	edit.Type = "update_annotation"
	edit.Body = "edited after deletion"
	in := CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "orphan-edit", Commands: []Command{edit}}
	out, err := r.ApplyAs(t.Context(), p.ID, in, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != p.Version+1 || out.CurrentRevisionID != p.CurrentRevisionID {
		t.Fatalf("orphan edit %+v", out)
	}
	edited, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{AnnotationID: note.AnnotationID})
	if err != nil || len(edited.Items) != 1 || edited.Items[0].Target != target || edited.Items[0].TargetStatus != "orphaned" || edited.Items[0].Body != edit.Body || edited.Items[0].Author != "editor" || !edited.Items[0].CreatedAt.Equal(orphaned.Items[0].CreatedAt) {
		t.Fatalf("edited orphan: %+v %v", edited, err)
	}
	foreign := createProject(t, r, "foreign")
	for _, newTarget := range []AnnotationTarget{{RecordType: "node", ID: uuid.NewV7().String()}, {RecordType: "node", ID: target.ID, RevisionID: foreign.CurrentRevisionID}} {
		bad := edit
		bad.Target = &newTarget
		_, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: out.Version, IdempotencyKey: "bad-rebind", Commands: []Command{{Type: "rename_project", Name: "no"}, bad}})
		assertFault(t, err, "backend_not_found")
	}
	p = out
	if annotationSemanticBytes(t, r, p.ID) != headBefore {
		t.Fatal("metadata mutated source/artifact bytes")
	}
	r = annotationRestart(t, r)
	replay, err := r.ApplyAs(t.Context(), p.ID, in, "different-replay-actor")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(replay)
	want, _ := json.Marshal(out)
	if string(got) != string(want) {
		t.Fatalf("replay: %s want %s", got, want)
	}
	stable, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{AnnotationID: note.AnnotationID})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = json.Marshal(stable)
	want, _ = json.Marshal(edited)
	if string(got) != string(want) {
		t.Fatalf("restart list: %s want %s", got, want)
	}
	// Rebinding an orphan to the still-existing historical target is valid.
	edit.Target = &bound
	p = annotationApply(t, r, p, "historical-rebind", edit)
	rebound, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{AnnotationID: note.AnnotationID})
	if err != nil || rebound.Items[0].TargetStatus != "historical" {
		t.Fatalf("rebind %+v %v", rebound, err)
	}
	_, err = r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "missing-create", Commands: []Command{annotationCommand(target, "missing")}})
	assertFault(t, err, "backend_not_found")
}

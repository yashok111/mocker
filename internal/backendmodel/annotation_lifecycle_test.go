package backendmodel

import (
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

func annotationRestart(t *testing.T, r *Repo) *Repo {
	t.Helper()
	path := r.db.Path()
	if err := r.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	return NewRepo(db)
}
func TestAnnotationTombstoneIdentityQuotaAndReplay(t *testing.T) {
	t.Parallel()
	r, p, _, target := annotationFixture(t)
	var first Command
	var firstInput CommandsInput
	var firstResult *Project
	for batch := range 10 {
		commands := make([]Command, 100)
		for i := range commands {
			commands[i] = annotationCommand(target, "note")
		}
		in := CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: fmt.Sprint("quota-", batch), Commands: commands}
		out, err := r.ApplyAs(t.Context(), p.ID, in, "creator")
		if err != nil {
			t.Fatal(err)
		}
		if batch == 0 {
			first = commands[0]
			firstInput = in
			firstResult = out
		}
		p = out
	}
	_, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "overflow", Commands: []Command{annotationCommand(target, "extra")}})
	assertFault(t, err, "backend_annotation_quota")
	remove := CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "remove", Commands: []Command{{Type: "remove_annotation", AnnotationID: first.AnnotationID}}}
	removed, err := r.Apply(t.Context(), p.ID, remove)
	if err != nil {
		t.Fatal(err)
	}
	p = removed
	var body, targetID, created, updated, deleted string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT body,target_id,created_at,updated_at,deleted_at FROM backend_annotations WHERE id=?`, first.AnnotationID).Scan(&body, &targetID, &created, &updated, &deleted); err != nil {
		t.Fatal(err)
	}
	if body != "" || targetID != target.ID || created == "" || updated == "" || deleted == "" {
		t.Fatalf("tombstone %q %q %q %q %q", body, targetID, created, updated, deleted)
	}
	for _, command := range []Command{first, annotationCommand(target, "new identity")} {
		_, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "recreate", Commands: []Command{command}})
		if command.AnnotationID == first.AnnotationID {
			assertFault(t, err, "backend_annotation_conflict")
		} else {
			assertFault(t, err, "backend_annotation_quota")
		}
	}
	again := remove
	again.ExpectedVersion = p.Version
	again.IdempotencyKey = "remove-again"
	_, err = r.Apply(t.Context(), p.ID, again)
	assertFault(t, err, "backend_not_found")
	r = annotationRestart(t, r)
	replay, err := r.ApplyAs(t.Context(), p.ID, firstInput, "different")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(replay)
	want, _ := json.Marshal(firstResult)
	if string(got) != string(want) {
		t.Fatal("create replay changed")
	}
	replay, err = r.Apply(t.Context(), p.ID, remove)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = json.Marshal(replay)
	want, _ = json.Marshal(removed)
	if string(got) != string(want) {
		t.Fatal("remove replay changed")
	}
	firstInput.Commands = append([]Command(nil), firstInput.Commands...)
	firstInput.Commands[0].Body = "changed"
	_, err = r.Apply(t.Context(), p.ID, firstInput)
	assertFault(t, err, "backend_idempotency_conflict")
	page, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{AnnotationID: first.AnnotationID})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("removed listed %+v %v", page, err)
	}
}
func TestAnnotationTextQuotaRemovalFreesBytes(t *testing.T) {
	t.Parallel()
	r, p, _, target := annotationFixture(t)
	body := strings.Repeat("界", 5461) + "a" // Exactly16KiB UTF-8; character count is smaller.
	var first Command
	for batch := range 16 {
		commands := make([]Command, 32)
		for i := range commands {
			commands[i] = annotationCommand(target, body)
		}
		if batch == 0 {
			first = commands[0]
		}
		p = annotationApply(t, r, p, fmt.Sprint("bytes-", batch), commands...)
	}
	overflow := annotationCommand(target, "a")
	_, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "overflow", Commands: []Command{overflow}})
	assertFault(t, err, "backend_annotation_quota")
	p = annotationApply(t, r, p, "free", Command{Type: "remove_annotation", AnnotationID: first.AnnotationID}, annotationCommand(target, body))
	page, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{Limit: 500})
	if err != nil || len(page.Items) != 500 || page.NextCursor == "" {
		t.Fatalf("byte boundary %+v %v", page, err)
	}
	p = annotationApply(t, r, p, "shrink", Command{Type: "update_annotation", AnnotationID: page.Items[0].ID, Target: &target, Body: "x"})
	p = annotationApply(t, r, p, "freed", overflow)
	if p.Version == 0 {
		t.Fatal("missing version")
	}
}
func TestAnnotationCASRollbackAndVersionExhaustion(t *testing.T) {
	t.Parallel()
	r, p, _, target := annotationFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, actor := range []string{"one", "two"} {
		wg.Go(func() {
			_, err := r.ApplyAs(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: actor, Commands: []Command{annotationCommand(target, actor)}}, actor)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			assertFault(t, err, "backend_version_conflict")
		}
	}
	if success != 1 {
		t.Fatalf("CAS successes %d", success)
	}
	p, _ = r.Get(t.Context(), p.ID)
	note := annotationCommand(target, "rollback")
	if _, err := r.db.W.ExecContext(t.Context(), `CREATE TRIGGER annotation_fail BEFORE INSERT ON backend_annotations WHEN NEW.body='forced' BEGIN SELECT RAISE(ABORT,'forced annotation failure'); END`); err != nil {
		t.Fatal(err)
	}
	in := CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "trigger", Commands: []Command{note, annotationCommand(target, "forced")}}
	if _, err := r.Apply(t.Context(), p.ID, in); err == nil {
		t.Fatal("injected failure succeeded")
	}
	page, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{AnnotationID: note.AnnotationID})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("rollback %+v %v", page, err)
	}
	if _, err := r.db.W.ExecContext(t.Context(), `DROP TRIGGER annotation_fail`); err != nil {
		t.Fatal(err)
	}
	p = annotationApply(t, r, p, "trigger", in.Commands...)
	if _, err := r.db.W.ExecContext(t.Context(), `UPDATE backend_projects SET version=? WHERE id=?`, int64(math.MaxInt64), p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: math.MaxInt64, IdempotencyKey: "exhausted", Commands: []Command{annotationCommand(target, "last")}})
	assertFault(t, err, "backend_version_exhausted")
}
func TestAnnotationCompetesWithImportCAS(t *testing.T) {
	t.Parallel()
	r, p, old, target := annotationFixture(t)
	s := beginRepeat(t, r, p, old)
	commands := fixtureCommands(s)
	commands[0].Node.Name = "Renamed handler"
	batch := sendCommands(t, r, p, s, s.Version, "renamed-handler", commands...)
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	note := annotationCommand(target, "CAS")
	input := CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "note", Commands: []Command{note}}
	out, err := r.ApplyAs(t.Context(), p.ID, input, "creator")
	if err != nil {
		t.Fatal(err)
	}
	_, err = commitFixture(t, r, p, s, preview, "next-source")
	assertFault(t, err, "backend_version_conflict")
	committed, err := commitFixture(t, r, out, s, preview, "next-source")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.ApplyAs(t.Context(), p.ID, input, "new")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(replay)
	want, _ := json.Marshal(out)
	if string(got) != string(want) {
		t.Fatal("receipt advanced after source import")
	}
	_, err = r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: out.Version, IdempotencyKey: "stale", Commands: []Command{annotationCommand(target, "stale")}})
	assertFault(t, err, "backend_version_conflict")
	page, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{AnnotationID: note.AnnotationID})
	if err != nil || len(page.Items) != 1 || page.Items[0].TargetStatus != "current" || page.Items[0].Body != "CAS" {
		t.Fatalf("annotation after source rename: %+v %v", page, err)
	}
	foreign := createProject(t, r, "foreign")
	foreignTarget := target
	foreignTarget.RevisionID = foreign.CurrentRevisionID
	_, err = r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: committed.Project.Version, IdempotencyKey: "foreign", Commands: []Command{annotationCommand(foreignTarget, "foreign")}})
	assertFault(t, err, "backend_not_found")
	edge := target
	edge.RecordType = "edge"
	_, err = r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: committed.Project.Version, IdempotencyKey: "wrong-type", Commands: []Command{annotationCommand(edge, "not edge")}})
	assertFault(t, err, "backend_not_found")
	absent := Command{Type: "update_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: "missing"}
	_, err = r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: committed.Project.Version, IdempotencyKey: "unknown", Commands: []Command{absent}})
	assertFault(t, err, "backend_not_found")
}

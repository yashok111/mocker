package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func assertWireDocument(t *testing.T, got any, expected string) {
	t.Helper()
	actual, err := canonicalJSON(got)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalValue([]byte(expected))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(want) {
		t.Fatalf("receipt wire changed\n got: %s\nwant: %s", actual, want)
	}
}
func assertCurrentSessionShape(t *testing.T, s any) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["mode"]) != `"initial"` || string(fields["graphScope"]) != "null" {
		t.Fatalf("current session shape %s", b)
	}
}
func TestLegacyImportSessionReceiptsPreserveWireShape(t *testing.T) {
	for _, operation := range []string{"begin", "abort"} {
		t.Run(operation, func(t *testing.T) {
			r, db := testRepo(t)
			p := createProject(t, r, "project")
			in := firstImportFixture(p)
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			assertCurrentSessionShape(t, s)
			scope, key := "import:"+p.ID+":begin", in.IdempotencyKey
			abort := AbortImportInput{ExpectedImportVersion: s.Version, IdempotencyKey: "abort"}
			if operation == "abort" {
				out, err := r.AbortImport(t.Context(), p.ID, s.ID, abort)
				if err != nil {
					t.Fatal(err)
				}
				assertCurrentSessionShape(t, out)
				scope = "import:" + p.ID + ":" + s.ID + ":abort"
				key = abort.IdempotencyKey
			}
			// Restore the exact absence of B0.3 fields in both the B0.2 response and session.
			if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_command_receipts SET response=json_remove(response,'$.mode','$.graphScope') WHERE scope=? AND key=?`, scope, key); err != nil {
				t.Fatal(err)
			}
			if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_import_sessions SET document=json_remove(document,'$.mode','$.graphScope') WHERE id=?`, s.ID); err != nil {
				t.Fatal(err)
			}
			var response, digest string
			if err := db.R.QueryRowContext(t.Context(), `SELECT response,request_hash FROM backend_command_receipts WHERE scope=? AND key=?`, scope, key).Scan(&response, &digest); err != nil {
				t.Fatal(err)
			}
			dbPath := db.Path()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			if err := reopened.Migrate(t.Context(), slog.Default()); err != nil {
				t.Fatal(err)
			}
			r = NewRepo(reopened)
			var replay *ImportSession
			if operation == "begin" {
				replay, err = r.BeginImport(t.Context(), p.ID, in)
			} else {
				replay, err = r.AbortImport(t.Context(), p.ID, s.ID, abort)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertWireDocument(t, replay, response)
			status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
			if err != nil {
				t.Fatal(err)
			}
			assertCurrentSessionShape(t, status.Session)
			page, err := r.Imports(t.Context(), p.ID, ListInput{})
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("sessions %+v %v", page, err)
			}
			assertCurrentSessionShape(t, page.Items[0])
			var after, afterDigest string
			if err := reopened.R.QueryRowContext(t.Context(), `SELECT response,request_hash FROM backend_command_receipts WHERE scope=? AND key=?`, scope, key).Scan(&after, &afterDigest); err != nil {
				t.Fatal(err)
			}
			if after != response || afterDigest != digest {
				t.Fatal("receipt storage changed during replay/read")
			}
		})
	}
}
func TestLegacyProjectAndCommitReceiptsKeepOriginalCapabilities(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "project")
	begin := firstImportFixture(p)
	s, err := r.BeginImport(t.Context(), p.ID, begin)
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	commit := CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"}
	out, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	// B0.2 project capabilities exclude the two newly advertised features.
	oldCapabilities := []string{"backend-projects", "backend-project-metadata", "backend-revisions", "backend-graph-query", "backend-source-import"}
	oldProject := *p
	oldProject.Capabilities = oldCapabilities
	projectResponse, err := json.Marshal(oldProject)
	if err != nil {
		t.Fatal(err)
	}
	out.Project.Capabilities = oldCapabilities
	commitResponse, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.W.ExecContext(t.Context(), `UPDATE backend_command_receipts SET response=? WHERE scope='create' AND key='project'`, string(projectResponse)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.W.ExecContext(t.Context(), `UPDATE backend_command_receipts SET response=? WHERE scope=? AND key='commit'`, string(commitResponse), "import:"+p.ID+":"+s.ID+":commit"); err != nil {
		t.Fatal(err)
	}
	again, err := r.Create(t.Context(), CreateInput{Name: "Orders", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	assertWireDocument(t, again, string(projectResponse))
	committed, err := r.CommitImport(t.Context(), p.ID, s.ID, commit)
	if err != nil {
		t.Fatal(err)
	}
	assertWireDocument(t, committed, string(commitResponse))
	live, err := r.Get(t.Context(), p.ID)
	if err != nil || len(live.Capabilities) <= len(oldCapabilities) {
		t.Fatalf("current capabilities %+v %v", live, err)
	}
}

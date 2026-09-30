package backendmodel

import (
	"database/sql/driver"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"modernc.org/sqlite"
)

func TestImportStatusUsesOneDatabaseSnapshot(t *testing.T) {
	// A SQLite view pauses the actual session SELECT after it has captured the
	// old document. The writer publishes a real batch before status continues;
	// both halves must still describe the same snapshot.
	captured := make(chan struct{})
	release := make(chan struct{})
	var armed atomic.Bool
	functionName := "status_snapshot_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
	if err := sqlite.RegisterScalarFunction(functionName, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if armed.CompareAndSwap(true, false) {
			close(captured)
			<-release
		}
		return args[0], nil
	}); err != nil {
		t.Fatal(err)
	}
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`ALTER TABLE backend_import_sessions RENAME TO backend_import_sessions_store`,
		`CREATE VIEW backend_import_sessions AS SELECT id,project_id,state,version,` + functionName + `(document) AS document FROM backend_import_sessions_store`,
		`CREATE TRIGGER backend_import_sessions_update INSTEAD OF UPDATE ON backend_import_sessions BEGIN UPDATE backend_import_sessions_store SET state=NEW.state,version=NEW.version,document=NEW.document WHERE id=OLD.id; END`,
	} {
		if _, err := db.W.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	armed.Store(true)
	type response struct {
		status *ImportStatus
		err    error
	}
	done := make(chan response, 1)
	go func() { status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{}); done <- response{status, err} }()
	select {
	case <-captured:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	commands := fixtureCommands(s)
	hash, err := ImportBatchHash(commands)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "published", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	var got response
	select {
	case got = <-done:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.status.Session.AcceptedBatchCount != int64(len(got.status.AcceptedBatches)) {
		t.Fatalf("incoherent status: session version=%d batchCount=%d, acceptedBatches=%+v", got.status.Session.Version, got.status.Session.AcceptedBatchCount, got.status.AcceptedBatches)
	}
	for _, batch := range got.status.AcceptedBatches {
		if batch.AcceptedVersion > got.status.Session.Version {
			t.Fatalf("batch version=%d is ahead of session version=%d", batch.AcceptedVersion, got.status.Session.Version)
		}
	}
}

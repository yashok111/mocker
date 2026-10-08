package backendmodel

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendblob"
)

func TestStorageUsageSeparatesClosedHistoryReceiptsAndTransientBudget(t *testing.T) {
	t.Parallel()
	r, p, old, batch := committedBase(t)
	before, err := r.StorageUsage(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.ActiveStagingBytes != 0 || before.ClosedImportBytes == 0 || before.RetainedLogicalBytes == 0 || before.DurableReceiptBytes == 0 || before.RemainingStagingBytes != MaxProjectStagingBytes {
		t.Fatalf("closed history charged as staging: %+v", before)
	}
	s := beginRepeat(t, r, p, old)
	reservation, ok := r.db.ReserveTransient("backend:"+p.ID, 8192, MaxProjectStagingBytes)
	if !ok {
		t.Fatal("reserve transient")
	}
	defer reservation.Release()
	active, err := r.StorageUsage(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.ActiveStagingBytes == 0 || active.TransientReservedBytes != 8192 || active.RemainingStagingBytes != MaxProjectStagingBytes-active.ActiveStagingBytes-8192 {
		t.Fatalf("admission accounting mismatch: %+v", active)
	}
	_, err = r.AbortImport(t.Context(), p.ID, s.ID, AbortImportInput{ExpectedImportVersion: s.Version, IdempotencyKey: "abort-usage"})
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
	after, err := r.StorageUsage(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ActiveStagingBytes != 0 || after.TransientReservedBytes != 0 || after.RemainingStagingBytes != MaxProjectStagingBytes || after.ClosedImportBytes <= before.ClosedImportBytes {
		t.Fatalf("closing session did not release active budget: %+v", after)
	}
	if _, err := r.Revision(t.Context(), p.ID, p.CurrentRevisionID); err != nil {
		t.Fatal("historical revision lost", err)
	}
	replay := putFixture(t, r, p, old)
	if replay.AcceptedVersion != batch.AcceptedVersion {
		t.Fatal("original batch receipt changed")
	}
}

func TestStorageUsageIncludesSharedObservationPayloads(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	a := createProject(t, r, "usage-a")
	b := createProject(t, r, "usage-b")
	raw := `{"record":"event1","fixture":"` + strings.Repeat("x", 1<<18) + `"}`
	err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := backendblob.Exec(t.Context(), tx, `INSERT INTO backend_observation_blobs(hash,document) VALUES(?,?)`, fixtureHash, raw); err != nil {
			return err
		}
		for _, pid := range []string{a.ID, b.ID} {
			for _, set := range []string{"one", "two"} {
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_observation_sets(project_id,id,version,name,context,logical_bytes,record_count) VALUES(?,?,1,'Fixture','{}',?,1)`, pid, set, len(raw)); err != nil {
					return err
				}
				if _, err := backendblob.Exec(t.Context(), tx, `INSERT INTO backend_observation_versions(project_id,set_id,version,content_hash,document) VALUES(?,?,1,?,'{}')`, pid, set, fixtureHash); err != nil {
					return err
				}
				if _, err := backendblob.Exec(t.Context(), tx, `INSERT INTO backend_observation_members(project_id,set_id,record_id,hash,introduced_version) VALUES(?,?,'event1',?,1)`, pid, set, fixtureHash); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []string{a.ID, b.ID} {
		usage, err := r.StorageUsage(t.Context(), pid)
		if err != nil {
			t.Fatal(err)
		}
		if usage.RetainedLogicalBytes < int64(2*len(raw)) || usage.RetainedDistinctPayloadBytes < int64(len(raw)) || usage.RetainedDistinctPayloadBytes >= int64(2*len(raw)) {
			t.Fatalf("shared observation bytes omitted or double counted: %+v", usage)
		}
	}
}

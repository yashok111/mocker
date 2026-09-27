package auth_test

import (
	"database/sql"
	"testing"

	"github.com/yashok111/mocker/internal/workspaces"
)

func TestEnsureMCPUserRollsBackFailedTransfer(t *testing.T) {
	t.Parallel()
	mgr, db := newTestManager(t)
	ctx := t.Context()
	legacy, err := mgr.EnsureUser(ctx, "mcp", "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspaces.NewRepo(db).Create(ctx, workspaces.CreateInput{Name: "legacy", Slug: "legacy", OwnerID: &legacy.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER reject_transfer BEFORE UPDATE OF owner_id ON workspaces
			BEGIN SELECT RAISE(ABORT, 'transfer failed'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if user, err := mgr.EnsureMCPUser(ctx, "admin"); err == nil || user != nil {
		t.Fatalf("failed transfer returned user=%+v err=%v", user, err)
	}
	var count, owner int64
	if err := db.R.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE name = 'admin'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.R.QueryRowContext(ctx, "SELECT owner_id FROM workspaces WHERE slug = 'legacy'").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if count != 0 || owner != legacy.ID {
		t.Fatalf("partial transfer: new users=%d, owner=%d, want 0 and %d", count, owner, legacy.ID)
	}
}

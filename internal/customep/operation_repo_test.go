package customep_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
)

// TestRepo_operationIDIsUniqueAmongCustomRows is D3's transaction-side
// half over the custom table alone (the spec half needs the admin plane's
// fixture and lives in internal/admin): a second row claiming the same
// operationId is refused with customep.ErrOperationIDTaken naming the holder, an
// update keeping its own id is not a self-collision, and a row with no id
// never collides.
func TestRepo_operationIDIsUniqueAmongCustomRows(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "opids")
	repo := customep.NewRepo(db)
	ctx := context.Background()

	first, err := repo.Create(ctx, wsID, &customep.Row{Method: "GET", Path: "/a", Operation: &customep.Operation{OperationID: "listA"}})
	if err != nil {
		t.Fatalf("create the first row: %v", err)
	}
	_, err = repo.Create(ctx, wsID, &customep.Row{Method: "GET", Path: "/b", Operation: &customep.Operation{OperationID: "listA"}})
	if !errors.Is(err, customep.ErrOperationIDTaken) || !strings.Contains(err.Error(), "custom endpoint GET /a") {
		t.Errorf("second row with the same id: err = %v; want customep.ErrOperationIDTaken naming GET /a", err)
	}
	if _, err := repo.Create(ctx, wsID, &customep.Row{Method: "GET", Path: "/c"}); err != nil {
		t.Errorf("a row with no operationId: %v", err)
	}
	if _, err := repo.Create(ctx, wsID, &customep.Row{Method: "GET", Path: "/d", Operation: &customep.Operation{OperationID: "listD"}}); err != nil {
		t.Errorf("a distinct id: %v", err)
	}
	expect := first.EditVersion
	if _, err := repo.UpdateExpecting(ctx, wsID, first.ID, &expect, func(cur *customep.Row) error {
		cur.Operation.Summary = "kept"
		return nil
	}); err != nil {
		t.Errorf("an update keeping its own id: %v", err)
	}
	other := insertWorkspace(t, db, "other")
	if _, err := repo.Create(ctx, other, &customep.Row{Method: "GET", Path: "/a", Operation: &customep.Operation{OperationID: "listA"}}); err != nil {
		t.Errorf("the same id in another workspace: %v; ids are per workspace", err)
	}
}

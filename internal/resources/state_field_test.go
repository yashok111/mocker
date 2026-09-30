package resources

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestCheckStateFieldTxUsesWorkspaceResourceIdentity(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	workspace := insertWorkspace(t, db, "state-field", nil, domain.Settings{})
	other := insertWorkspace(t, db, "other", nil, domain.Settings{})
	insertResourceRow(t, db, workspace, "/orders", "orderId", "integer")
	insertResourceRow(t, db, other, "/orders", "status", "integer")
	for _, tc := range []struct {
		name, field, family string
		invalid             bool
	}{
		{"business field", "status", "/orders", false},
		{"resource identity", "orderId", "/orders", true},
		{"missing family", "status", "/missing", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Write(t.Context(), func(tx *sql.Tx) error {
				return CheckStateFieldTx(t.Context(), tx, workspace, tc.family, tc.field)
			})
			if tc.invalid {
				field, ok := errors.AsType[*RuleFamilyError](err)
				if !ok || field.Family != tc.family {
					t.Fatalf("field admission=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

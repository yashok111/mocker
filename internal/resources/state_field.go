package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CheckStateFieldTx admits a business state field against the projected
// workspace's resource metadata. The ID field is pinned by every entity write,
// so using it as state would silently undo the selected transition.
func CheckStateFieldTx(ctx context.Context, tx *sql.Tx, workspaceID int64, family, stateField string) error {
	var idField string
	err := tx.QueryRowContext(ctx, "SELECT id_field FROM resources WHERE workspace_id=? AND route_family=?", workspaceID, family).Scan(&idField)
	if errors.Is(err, sql.ErrNoRows) {
		return &RuleFamilyError{Family: family, Message: "не найдено семейство для поля состояния"}
	}
	if err != nil {
		return fmt.Errorf("check state field for family %q: %w", family, err)
	}
	if stateField == idField {
		return &RuleFamilyError{Family: family, Message: "поле состояния совпадает с полем ID сущности"}
	}
	return nil
}

package workspaces

import (
	"context"
	"database/sql"
	"errors"
)

// ManagedDesign returns the owning API design, or zero for an ordinary workspace.
// Membership is immutable and is created in the same transaction as both runtimes.
func (r *Repo) ManagedDesign(ctx context.Context, workspaceID int64) (int64, error) {
	var id int64
	err := r.db.R.QueryRowContext(ctx, "SELECT design_id FROM api_design_workspaces WHERE workspace_id=?", workspaceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

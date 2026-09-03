// Decline: the slug-guarded transition that deletes a family and its rows.
// Split out of repo.go 2026-09-03; the text is unchanged.
package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/router"
)

// childFamiliesTx returns every OTHER confirmed family of workspaceID whose
// router.ParentFamily is family — D7.1's own read, inside tx. There is no
// stored parent_family column to query directly (D9/D4.2: it would be a
// second source of truth for a pure function of the row's own
// route_family), so this walks the whole roster and recomputes it.
func childFamiliesTx(ctx context.Context, tx *sql.Tx, workspaceID int64, family string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT route_family FROM resources WHERE workspace_id = ?", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("read resource roster for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	var children []string
	for rows.Next() {
		var rf string
		if err := rows.Scan(&rf); err != nil {
			return nil, fmt.Errorf("scan resource roster row for workspace %d: %w", workspaceID, err)
		}
		if router.ParentFamily(rf) == family {
			children = append(children, rf)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource roster for workspace %d: %w", workspaceID, err)
	}
	return children, nil
}

// --- Decline -----------------------------------------------------------

// Decline is D4's "declined" transition: write state='declined', and if a
// resources row exists delete it (entities cascade). confirmSlug is
// optional on the wire and required by the server exactly when a resources
// row exists (R10) — a decline that destroys nothing needs no slug. The
// existence test, the slug comparison and the DELETE are all inside ONE
// transaction (D13 clause 43): a confirm committing between an outside
// check and the delete would make the guard vacuous over exactly the data
// it exists to protect.
func (r *Repo) Decline(ctx context.Context, workspaceID int64, routeFamily, confirmSlug string) error {
	core, err := r.readWorkspaceCore(ctx, workspaceID)
	if err != nil {
		return err
	}

	// The 404 unknown_family read happens OUTSIDE this transaction,
	// exactly like Confirm's: specs.Repo.EnsureSuggestions may itself
	// write (the lazy backfill), and the writer pool is ONE connection —
	// calling it from inside this package's own db.Write would deadlock
	// waiting for a connection this same goroutine is already holding.
	sugg, err := r.findSuggestion(ctx, core.specID, routeFamily)
	if err != nil {
		return fmt.Errorf("decline %q for workspace %d: %w", routeFamily, workspaceID, err)
	}
	hasSuggestion := sugg != nil

	now := time.Now().UTC()
	writeErr := r.db.Write(ctx, func(tx *sql.Tx) error {
		var resourceID int64
		exists := false
		switch err := tx.QueryRowContext(ctx,
			"SELECT id FROM resources WHERE workspace_id = ? AND route_family = ?", workspaceID, routeFamily,
		).Scan(&resourceID); {
		case err == nil:
			exists = true
		case errors.Is(err, sql.ErrNoRows):
		default:
			return fmt.Errorf("check existing resource for %q: %w", routeFamily, err)
		}

		if !exists && !hasSuggestion {
			return ErrUnknownFamily
		}

		if exists {
			// D7.1: a family with a still-CONFIRMED child is refused,
			// read and compared inside this same transaction as the
			// delete below (D7.2 — an unwritten resources.parent_id
			// cascade means this check is the only thing stopping a
			// decline from destroying a child's rows the operator never
			// named). Checked BEFORE the slug: no confirmSlug fixes a
			// decline this transaction cannot perform at all.
			children, cerr := childFamiliesTx(ctx, tx, workspaceID, routeFamily)
			if cerr != nil {
				return cerr
			}
			if len(children) > 0 {
				return fmt.Errorf("decline %q for workspace %d: %w: %s", routeFamily, workspaceID, ErrChildConfirmed, strings.Join(children, ", "))
			}

			var slug string
			if err := tx.QueryRowContext(ctx, "SELECT slug FROM workspaces WHERE id = ?", workspaceID).Scan(&slug); err != nil {
				return fmt.Errorf("read workspace %d slug: %w", workspaceID, err)
			}
			switch {
			case confirmSlug == "":
				return ErrConfirmSlugRequired
			case confirmSlug != slug:
				return ErrConfirmSlugMismatch
			}
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO resource_decisions (workspace_id, route_family, state) VALUES (?, ?, 'declined')
			ON CONFLICT (workspace_id, route_family) DO UPDATE SET state = 'declined'`,
			workspaceID, routeFamily); err != nil {
			return fmt.Errorf("write decision for %q: %w", routeFamily, err)
		}

		if exists {
			if _, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE id = ?", resourceID); err != nil {
				return fmt.Errorf("delete resource %d: %w", resourceID, err)
			}
		}

		return bumpRevisionTx(ctx, tx, workspaceID, now)
	})
	if writeErr != nil {
		return fmt.Errorf("decline %q for workspace %d: %w", routeFamily, workspaceID, writeErr)
	}
	return nil
}

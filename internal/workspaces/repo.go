// Package workspaces owns the workspaces table: creating, reading, updating
// and deleting the row that anchors everything else in a mock (spec, edits,
// resources, entities, scenarios all hang off workspace_id).
//
// Slug assignment is the one subtle part. A slug becomes a DNS label
// (<slug>.<BaseDomain>, DESIGN §6) and must be unique, so picking one and
// reserving it have to be a single atomic step — otherwise two concurrent
// creates for the same name both see the base slug free and both insert it,
// and the loser gets a confusing 500 instead of "alex-2".
package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/store"
)

// Workspace is one row of the workspaces table (DESIGN §13), with Settings
// decoded from its stored JSON.
type Workspace struct {
	ID   int64
	Slug string
	Name string

	SpecID     *int64
	OwnerID    *int64
	ForkedFrom *int64
	ScenarioID *int64

	// Revision is a cheap monotonic counter bumped on every config edit; the
	// runtime cache keys compiled builds off (workspace_id, revision) and
	// nothing else reads it (DESIGN §12).
	Revision int64

	// EditVersion is the per-row compare-and-swap token (A3, D4): the value a
	// caller must echo back on PATCH /api/workspaces/{id} to prove it read
	// this exact row. Allocated from EditSeq, never workspaces.revision — the
	// two counters answer different questions and must not be conflated
	// (D4's "why not updated_at" argument applies to Revision the same way).
	EditVersion int64
	// EditSeq is the per-workspace monotone sequence AllocateEditVersion
	// draws from. It is bootstrapped to 1 at INSERT time (this row has no
	// prior row to allocate from) and advanced by every later allocation in
	// this workspace, including this row's own.
	EditSeq int64

	Settings domain.Settings

	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateInput is what the caller supplies; everything else (id, timestamps,
// revision) is assigned by Create.
type CreateInput struct {
	Name string
	// Slug, if non-empty, is validated and used as-is (ErrSlugTaken if it
	// collides). If empty, one is derived from Name via [domain.UniqueSlug].
	Slug string

	OwnerID *int64
	SpecID  *int64
	// Settings defaults to [domain.DefaultSettings] when nil, which is what
	// gives each workspace its own random JWT signing key.
	Settings *domain.Settings
	// ForkedFrom (P4b, 2026-09-02) is the source workspace's id when this
	// row is a fork — the `forked_from` column that has existed since P0
	// and was written by nothing until now. It is a raw id, not a slug: a
	// deleted source leaves it dangling on purpose (the column has no ON
	// DELETE clause), and the view renders whatever it holds.
	ForkedFrom *int64
}

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("workspace not found")

// ErrSlugTaken is returned when a slug (explicit or derived) is already in
// use by another workspace.
var ErrSlugTaken = errors.New("slug already taken")

// ErrSlugInvalid wraps a [domain.ValidateSlug] failure on an explicit slug.
var ErrSlugInvalid = errors.New("slug invalid")

// ErrSettingsTooLarge is returned when a settings object marshals to more
// than maxSettingsJSON bytes.
var ErrSettingsTooLarge = errors.New("settings too large")

// maxSettingsJSON caps the marshalled size of a workspace's settings blob
// (round-1 review finding 3). Every real field in [domain.Settings] —
// identity, org, auth params, a short CORS/envelope switch, a custom
// notFoundBody — comfortably fits in low single-digit KiB; 64 KiB leaves
// generous room for a hand-written custom 404 body without letting the
// column become an amplification vector: the mock plane's BySlug path
// (mockplane/plane.go) re-parses this JSON on EVERY unauthenticated request
// to the workspace, so an uncapped blob turns one PATCH into a per-request
// cost multiplier for as long as the workspace exists, with no rate limit to
// fall back on (DESIGN §18 forbids rate-limiting the mock plane).
const maxSettingsJSON = 64 * 1024

// Repo is the workspaces table's data-access layer.
type Repo struct {
	db *store.DB
}

// NewRepo builds a Repo over db.
func NewRepo(db *store.DB) *Repo {
	return &Repo{db: db}
}

// Create inserts a new workspace, reserving its slug atomically: the
// uniqueness probe and the INSERT run inside the same write transaction, so
// two concurrent Creates racing for the same derived slug both succeed with
// different slugs instead of one failing on a lost race. The slug actually
// assigned is on the returned Workspace and must be shown to the caller
// (DESIGN §14): silently handing "alex-2" to someone who typed "alex" points
// their frontend at a colleague's workspace.
func (r *Repo) Create(ctx context.Context, in CreateInput) (*Workspace, error) {
	settings, settingsJSON, err := prepareSettings(in)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	var ws *Workspace

	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		created, iErr := insertTx(ctx, tx, in, settings, settingsJSON, now)
		if iErr != nil {
			return iErr
		}
		ws = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ws, nil
}

// insertTx does the slug reservation and INSERT shared by [Repo.Create] and
// [Repo.EnsureDefault] — split out so EnsureDefault can run its
// "does the owner already have a workspace" check and this insert inside the
// very same write transaction, which is what makes that check atomic (see
// EnsureDefault's doc comment).
// CreateTx is [Repo.Create]'s body inside a transaction the CALLER holds
// (P4b, 2026-09-02): an import or a fork creates the workspace and fills
// its layers in ONE transaction, so no client can list, edit or serve a
// workspace that exists with half of its rows. It runs the same slug
// derivation, validation and settings preparation Create runs; the only
// thing it does not do is open the transaction.
func CreateTx(ctx context.Context, tx *sql.Tx, in CreateInput) (*Workspace, error) {
	settings, settingsJSON, err := prepareSettings(in)
	if err != nil {
		return nil, err
	}
	return insertTx(ctx, tx, in, settings, settingsJSON, time.Now().UTC())
}

func insertTx(ctx context.Context, tx *sql.Tx, in CreateInput, settings domain.Settings, settingsJSON []byte, now time.Time) (*Workspace, error) {
	slug := strings.TrimSpace(in.Slug)
	if slug != "" {
		if verr := domain.ValidateSlug(slug); verr != nil {
			return nil, fmt.Errorf("%w: %w", ErrSlugInvalid, verr)
		}
		taken, terr := slugTakenTx(ctx, tx, slug)
		if terr != nil {
			return nil, terr
		}
		if taken {
			return nil, fmt.Errorf("%w: %q", ErrSlugTaken, slug)
		}
	} else {
		chosen, uerr := domain.UniqueSlug(in.Name, func(candidate string) (bool, error) {
			// Reads through tx, not r.db.R: a different pool cannot see
			// this transaction's own uncommitted reservations, so a
			// concurrent Create deriving the same base slug would see it
			// as free and collide.
			return slugTakenTx(ctx, tx, candidate)
		})
		if uerr != nil {
			return nil, fmt.Errorf("derive slug: %w", uerr)
		}
		slug = chosen
	}

	// edit_seq and edit_version both start at 1 in this one statement: this
	// row cannot allocate from itself (AllocateEditVersion's UPDATE has
	// nothing to update until the INSERT below has run), so bootstrapping is
	// the one case in this design that does not go through the allocator.
	// The sequence's first value is spent here, on the row that carries the
	// sequence, and every later allocation in this workspace goes through
	// store.AllocateEditVersion (D4).
	res, iErr := tx.ExecContext(ctx, `
		INSERT INTO workspaces
			(slug, name, spec_id, owner_id, forked_from, revision, scenario_id, settings, created_at, updated_at, edit_version, edit_seq)
		VALUES (?, ?, ?, ?, ?, 1, NULL, ?, ?, ?, 1, 1)`,
		slug, in.Name, in.SpecID, in.OwnerID, in.ForkedFrom, string(settingsJSON), now.Unix(), now.Unix(),
	)
	if iErr != nil {
		if isUniqueViolation(iErr) {
			return nil, fmt.Errorf("%w: %q", ErrSlugTaken, slug)
		}
		return nil, fmt.Errorf("insert workspace: %w", iErr)
	}
	id, iErr := res.LastInsertId()
	if iErr != nil {
		return nil, fmt.Errorf("workspace id: %w", iErr)
	}

	return &Workspace{
		ID:          id,
		Slug:        slug,
		Name:        in.Name,
		SpecID:      in.SpecID,
		OwnerID:     in.OwnerID,
		ForkedFrom:  in.ForkedFrom,
		Settings:    settings,
		Revision:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
		EditVersion: 1,
		EditSeq:     1,
	}, nil
}

// prepareSettings normalizes in.Settings (or [domain.DefaultSettings] when
// nil) and marshals it, enforcing maxSettingsJSON. Split out of Create so
// EnsureDefault can reuse the identical validation rather than re-deriving
// it and risking the two drifting.
func prepareSettings(in CreateInput) (domain.Settings, []byte, error) {
	settings := domain.DefaultSettings()
	if in.Settings != nil {
		settings = *in.Settings
	}
	settings.Normalize()
	settingsJSON, err := settings.MarshalJSONStable()
	if err != nil {
		return domain.Settings{}, nil, fmt.Errorf("marshal settings: %w", err)
	}
	if len(settingsJSON) > maxSettingsJSON {
		return domain.Settings{}, nil, fmt.Errorf("%w: %d bytes (max %d)", ErrSettingsTooLarge, len(settingsJSON), maxSettingsJSON)
	}
	return settings, settingsJSON, nil
}

// EnsureDefault auto-creates ONE workspace for ownerID against specID, but
// only if ownerID currently owns zero workspaces — DESIGN §14 screen 2's
// "первый вход": MOCKER_DEFAULT_SPEC plus a brand-new user with no
// workspaces yet auto-creates one, and the caller must show the slug it
// picked rather than a colleague's "alex" silently reusing "alex-2" the
// other way around.
//
// The existence check and the insert run inside the SAME write transaction
// insertTx uses for slug reservation, which is what makes "owner has zero
// workspaces" race-safe the same way slug uniqueness already is: store.DB.W
// is a single connection, so store.DB.Write serializes every write against
// every other write process-wide. Two logins racing for the same brand-new
// user cannot both observe zero rows and both insert — the loser's
// transaction begins only after the winner's has committed, so it sees the
// just-inserted row and returns (nil, nil) instead of a second workspace.
//
// A nil, nil return means "the owner already had a workspace" (not a first
// login, or a concurrent request already handled it) — not an error;
// callers branch on the nil to decide whether anything was auto-created.
func (r *Repo) EnsureDefault(ctx context.Context, ownerID int64, name string, specID int64, settingsIn *domain.Settings) (*Workspace, error) {
	in := CreateInput{Name: name, OwnerID: &ownerID, SpecID: &specID, Settings: settingsIn}
	settings, settingsJSON, err := prepareSettings(in)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	var ws *Workspace
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		var one int
		qErr := tx.QueryRowContext(ctx, "SELECT 1 FROM workspaces WHERE owner_id = ? LIMIT 1", ownerID).Scan(&one)
		switch {
		case qErr == nil:
			// Owner already has a workspace — this is not a first login.
			return nil
		case !errors.Is(qErr, sql.ErrNoRows):
			return fmt.Errorf("check existing workspaces for owner %d: %w", ownerID, qErr)
		}

		created, iErr := insertTx(ctx, tx, in, settings, settingsJSON, now)
		if iErr != nil {
			return iErr
		}
		ws = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ws, nil
}

// ByID looks up a workspace by primary key.
func (r *Repo) ByID(ctx context.Context, id int64) (*Workspace, error) {
	return scanOne(r.db.R.QueryRowContext(ctx, selectWorkspace+" WHERE id = ?", id))
}

// BySlug looks up a workspace by its unique slug.
func (r *Repo) BySlug(ctx context.Context, slug string) (*Workspace, error) {
	return scanOne(r.db.R.QueryRowContext(ctx, selectWorkspace+" WHERE slug = ?", slug))
}

// List returns every workspace, ordered by id ascending (deterministic,
// oldest first). When ownerID is non-nil, it filters to that owner.
func (r *Repo) List(ctx context.Context, ownerID *int64) ([]*Workspace, error) {
	query := selectWorkspace
	args := []any{}
	if ownerID != nil {
		query += " WHERE owner_id = ?"
		args = append(args, *ownerID)
	}
	query += " ORDER BY id ASC"

	rows, err := r.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Workspace
	for rows.Next() {
		ws, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspaces: %w", err)
	}
	return out, nil
}

// Update reads the row, applies mutate to it and writes it back, all inside
// one write transaction: a read-modify-write spanning two transactions would
// lose a concurrent edit between the read and the write. Revision is bumped
// by exactly 1 regardless of what mutate changes, per DESIGN §12 (it is a
// cache-invalidation counter, not a change count).
//
// Update is UpdateExpecting's unguarded delegation (A3, D8): it passes nil,
// "no expectation", so every call site that predates A3 compiles and behaves
// unchanged. PATCH /api/workspaces/{id} — the one guarded route over this
// verb — moves to UpdateExpecting.
func (r *Repo) Update(ctx context.Context, id int64, mutate func(*Workspace) error) (*Workspace, error) {
	return r.UpdateExpecting(ctx, id, nil, mutate)
}

// UpdateExpecting is Update's compare-and-swap sibling (A3, D7/D8/D9). expect
// is the edit_version the caller last read; nil means "no expectation" (only
// [Repo.Update]'s delegation and pre-A3 callers pass it — no wire may carry
// nil, only *int64(0) or a positive value).
//
// The five cases, checked and allocated inside the same write transaction so
// two writers holding the same expectation cannot both commit (D7):
//
//   - expect == nil: no check at all, proceeds exactly as before.
//   - *expect == 0: refused with store.ErrEditConflict. A workspaces row
//     always exists by the time PATCH is reachable (D7: "0 is meaningful
//     only for op_overrides"), so a caller claiming "I expect no row" here
//     has misunderstood something, and 0 is never treated as "no
//     expectation".
//   - *expect == current row's EditVersion: proceeds, writes at a freshly
//     allocated version — never expect+1 in place (D4/D9): an in-place
//     increment lives on the row and would collide with a value already
//     handed to a deleted-then-recreated row.
//   - *expect != current row's EditVersion (row present): refused with
//     store.ErrEditConflict carrying the current row.
//   - row absent while expect is sent: refused with
//     store.ErrEditConflict{Gone: true} rather than ErrNotFound — D7's "an
//     expectation was sent and the TARGET ROW is absent ⇒ ErrEditConflict".
//     Only a caller with expect == nil still sees ErrNotFound for a deleted
//     row, which is [Repo.Update]'s existing behaviour, preserved.
//
// The write always allocates a fresh edit_version when it proceeds,
// regardless of whether expect was sent: this verb is the guarded route's
// entire write path over name/settings/specId, the fields PATCH can send
// (D9's criterion — "changes a field the row's guarded route can write").
func (r *Repo) UpdateExpecting(ctx context.Context, id int64, expect *int64, mutate func(*Workspace) error) (*Workspace, error) {
	var ws *Workspace
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, selectWorkspace+" WHERE id = ?", id)
		current, err := scanOne(row)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				if expect != nil {
					return &store.EditConflictError{Gone: true}
				}
				return ErrNotFound
			}
			return err
		}

		if expect != nil && *expect != current.EditVersion {
			return &store.EditConflictError{Current: *current}
		}

		if err := mutate(current); err != nil {
			return fmt.Errorf("mutate workspace %d: %w", id, err)
		}

		current.Settings.Normalize()
		settingsJSON, err := current.Settings.MarshalJSONStable()
		if err != nil {
			return fmt.Errorf("marshal settings: %w", err)
		}
		if len(settingsJSON) > maxSettingsJSON {
			return fmt.Errorf("%w: %d bytes (max %d)", ErrSettingsTooLarge, len(settingsJSON), maxSettingsJSON)
		}

		newVersion, err := store.AllocateEditVersion(ctx, tx, id)
		if err != nil {
			return err
		}

		current.Revision++
		current.UpdatedAt = time.Now().UTC()
		current.EditVersion = newVersion

		_, err = tx.ExecContext(ctx, `
			UPDATE workspaces
			SET slug = ?, name = ?, spec_id = ?, owner_id = ?, forked_from = ?,
			    revision = ?, scenario_id = ?, settings = ?, updated_at = ?, edit_version = ?
			WHERE id = ?`,
			current.Slug, current.Name, current.SpecID, current.OwnerID, current.ForkedFrom,
			current.Revision, current.ScenarioID, string(settingsJSON), current.UpdatedAt.Unix(),
			current.EditVersion, id,
		)
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: %q", ErrSlugTaken, current.Slug)
			}
			return fmt.Errorf("update workspace %d: %w", id, err)
		}

		ws = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ws, nil
}

// Delete removes a workspace. Everything hanging off it (resources, entities,
// overrides, scenarios, checkpoints, traffic) cascades via ON DELETE CASCADE
// in the schema.
func (r *Repo) Delete(ctx context.Context, id int64) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		// forked_from (P4b) REFERENCES workspaces(id) with no ON DELETE
		// clause, and every connection runs with foreign_keys=ON — so the
		// schema's default is NO ACTION, which REFUSES the delete of a fork
		// source ("FOREIGN KEY constraint failed" → a 500) rather than
		// leaving the copy's column dangling as CARVE-OUTS.md's P4b entry
		// describes. The intent was the dangling id; this makes it true by
		// detaching every copy first, in the same transaction.
		if _, err := tx.ExecContext(ctx, "UPDATE workspaces SET forked_from = NULL WHERE forked_from = ?", id); err != nil {
			return fmt.Errorf("detach forks of workspace %d: %w", id, err)
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM workspaces WHERE id = ?", id)
		if err != nil {
			return fmt.Errorf("delete workspace %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete workspace %d: %w", id, err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SlugTaken reports whether slug is already in use. It reads through the
// reader pool and is meant for the admin API's live-validation-as-you-type
// endpoint — NEVER call it from inside a Create/Update transaction, since the
// reader pool cannot see that transaction's own uncommitted writes.
func (r *Repo) SlugTaken(ctx context.Context, slug string) (bool, error) {
	var one int
	err := r.db.R.QueryRowContext(ctx, "SELECT 1 FROM workspaces WHERE slug = ?", slug).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("check slug %q: %w", slug, err)
	}
}

// slugTakenTx is [Repo.SlugTaken]'s transaction-scoped twin: same query,
// reading through tx so a not-yet-committed reservation earlier in the same
// transaction is visible to it.
func slugTakenTx(ctx context.Context, tx *sql.Tx, slug string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM workspaces WHERE slug = ?", slug).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("check slug %q: %w", slug, err)
	}
}

// isUniqueViolation reports whether err is a UNIQUE constraint failure.
// modernc.org/sqlite reports these as a plain error whose message contains
// "UNIQUE constraint failed" — matched by substring so this package does not
// need to import the driver just to compare an error code.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

const selectWorkspace = `
	SELECT id, slug, name, spec_id, owner_id, forked_from, revision, scenario_id, settings, created_at, updated_at, edit_version, edit_seq
	FROM workspaces`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scan logic is
// written once.
type rowScanner interface {
	Scan(dest ...any) error
}

func scan(row rowScanner) (*Workspace, error) {
	var (
		ws           Workspace
		settingsJSON string
		createdAt    int64
		updatedAt    int64
	)
	if err := row.Scan(
		&ws.ID, &ws.Slug, &ws.Name, &ws.SpecID, &ws.OwnerID, &ws.ForkedFrom,
		&ws.Revision, &ws.ScenarioID, &settingsJSON, &createdAt, &updatedAt,
		&ws.EditVersion, &ws.EditSeq,
	); err != nil {
		return nil, err
	}

	settings, err := domain.ParseSettings([]byte(settingsJSON))
	if err != nil {
		return nil, fmt.Errorf("workspace %d: %w", ws.ID, err)
	}
	ws.Settings = settings
	ws.CreatedAt = time.Unix(createdAt, 0).UTC()
	ws.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &ws, nil
}

func scanOne(row *sql.Row) (*Workspace, error) {
	ws, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan workspace: %w", err)
	}
	return ws, nil
}

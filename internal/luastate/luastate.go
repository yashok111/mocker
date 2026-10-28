// Package luastate owns the lua_state table (A23, 0031): the workspace-
// scoped key-value store a Lua endpoint function reaches as mock.state.get/
// set/delete. It exists because the ONE other persistent thing a function
// could see — mock.entities — requires a confirmed resource family from the
// bound spec, and a custom endpoint's route is by definition in no spec:
// "POST assigned → GET shows" for a route proposed to a backend but not yet
// merged had nowhere to keep its rows.
//
// LEAF PACKAGE: internal/store only (plus jsonx for the value type). It
// knows nothing about workspaces beyond the id, nothing about Lua — the
// caller converts — and nothing about HTTP. Two callers reach it:
// internal/mockplane's LuaStateStore seam, per request, and
// internal/resources' resetTx, whose WipeTx call runs INSIDE reset-data's
// own transaction so a reset never leaves state behind — including for a
// workspace with an empty confirmed-family roster, the custom-endpoint-only
// workspace this table exists for (resources/reset.go records the decision).
//
// What this package deliberately does NOT do: export/import/fork (the bundle
// stays v6 — state is DATA, and data beyond entities is a v7 decision nobody
// made), scenarios (a snapshot of configuration, and this is not
// configuration), revision tracking (like entities, it bumps nothing), and
// any kind of TTL — a mock's state lives until reset-data or workspace
// delete takes it.
package luastate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/store"
)

// The three caps, and they are constants and not environment knobs for the
// reason luafn.Timeout is one: a knob is a carve-out away if a real load asks
// for one, and until then every deployment answers the same way. The numbers
// sit next to the entities' own order of magnitude — a session's assignment
// rows are the shape this stores, not a second traffic log.
const (
	// MaxKeyBytes bounds one key. 200 is the same figure luafn's traffic
	// note cap uses: a sessionId, a correlation id, a composite key — the
	// shapes that fit are the shapes this store is for.
	MaxKeyBytes = 200
	// MaxValueBytes bounds one value's JSON encoding. 16 KiB is four times
	// maxFunctionHeaderValue and far over any row-sized payload; a function
	// that wants to move more has entities, or is writing a log.
	MaxValueBytes = 16 << 10
	// MaxKeysPerWorkspace bounds the SET. A thousand keys across a
	// workspace's functions is a thousand session fixtures; more than that
	// is a leak someone should see instead of a store that grows forever.
	MaxKeysPerWorkspace = 1000
)

// The refusals Set makes, mapped by the CALLER onto whatever vocabulary its
// plane speaks (mock.state answers nil + a word; nothing here names HTTP).
var (
	ErrBadKey      = errors.New("luastate: key must be a non-empty string of at most 200 bytes")
	ErrValueTooBig = errors.New("luastate: value exceeds 16 KiB of JSON")
	ErrTooManyKeys = errors.New("luastate: workspace holds 1000 keys already")
)

// Repo is the table's one owner. Every method takes the workspace id
// explicitly — nothing here reads a request, so nothing here could derive
// one.
type Repo struct {
	db *store.DB
}

func NewRepo(db *store.DB) *Repo { return &Repo{db: db} }

// Get answers the stored JSON bytes for key, and found:false — not an error
// — when there is none: a mock.state.get of a key nobody set is the ordinary
// first call of a session, and an error would make every author write a
// guard around the cold start.
func (r *Repo) Get(ctx context.Context, workspaceID int64, key string) (jsonx.RawMessage, bool, error) {
	if err := checkKey(key); err != nil {
		return nil, false, err
	}
	var raw jsonx.RawMessage
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			"SELECT value FROM lua_state WHERE workspace_id = ? AND key = ?", workspaceID, key).Scan(&raw)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("luastate get: %w", err)
	}
	return raw, true, nil
}

// Set stores value — already the exact JSON bytes the caller means to hand
// back — and overwrites any previous value under the same key: a session
// reassigns, and last-write-wins is the whole contract. The count cap gates
// only NEW keys, so a workspace churning a thousand fixed keys never trips
// it and a workspace growing unbounded keys does.
func (r *Repo) Set(ctx context.Context, workspaceID int64, key string, value jsonx.RawMessage) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if len(value) > MaxValueBytes {
		return ErrValueTooBig
	}
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		return setTx(ctx, tx, workspaceID, key, value)
	})
}

// Delete answers true when a row went, false when there was none — the same
// boolean mock.entities.delete answers, for the same reason: deleting an
// expired session is not an error a function should branch on.
func (r *Repo) Delete(ctx context.Context, workspaceID int64, key string) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}
	var deleted bool
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			"DELETE FROM lua_state WHERE workspace_id = ? AND key = ?", workspaceID, key)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		deleted = n > 0
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("luastate delete: %w", err)
	}
	return deleted, nil
}

// WipeTx deletes every key of one workspace inside the CALLER's transaction —
// resources.resetTx's step beside the entity deletes, so a reset-data answers
// for state and entities in one transaction or not at all. It returns the
// number of keys that went, which is how the no-op detection tells "nothing
// to reset" from "state reset, nothing else".
func WipeTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (int64, error) {
	res, err := tx.ExecContext(ctx, "DELETE FROM lua_state WHERE workspace_id = ?", workspaceID)
	if err != nil {
		return 0, fmt.Errorf("luastate wipe: %w", err)
	}
	return res.RowsAffected()
}

// setTx is Set's and no one else's write body, split only so reset.go's
// neighbors can read it beside the entity DELETEs it shares a transaction
// with. The insert-or-update shape: UPDATE first, and INSERT only when the
// update touched nothing — one statement under the count cap, and the cap's
// COUNT races nothing because the single writer connection serializes
// every write.
func setTx(ctx context.Context, tx *sql.Tx, workspaceID int64, key string, value jsonx.RawMessage) error {
	res, err := tx.ExecContext(ctx,
		"UPDATE lua_state SET value = ?, updated_at = ? WHERE workspace_id = ? AND key = ?",
		value, time.Now().Unix(), workspaceID, key)
	if err != nil {
		return fmt.Errorf("luastate set: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM lua_state WHERE workspace_id = ?", workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("luastate set: %w", err)
	}
	if count >= MaxKeysPerWorkspace {
		return ErrTooManyKeys
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO lua_state (workspace_id, key, value, updated_at) VALUES (?, ?, ?, ?)",
		workspaceID, key, value, time.Now().Unix()); err != nil {
		return fmt.Errorf("luastate set: %w", err)
	}
	return nil
}

func checkKey(key string) error {
	if key == "" || len(key) > MaxKeyBytes {
		return ErrBadKey
	}
	return nil
}

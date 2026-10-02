package recordproxy

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/store"
)

var ErrConfirmation = errors.New("workspace slug changed; confirm its current slug")
var ErrConflict = errors.New("proxy configuration changed; reload and retry")
var ErrNotFound = errors.New("recording not found")
var ErrLimit = errors.New("recording quota reached (500 responses or 32 MiB)")

const MaxRecordings = 500
const MaxRecordingBytes = 32 << 20

type Repo struct{ db *store.DB }

func NewRepo(db *store.DB) *Repo { return &Repo{db: db} }

type Recording struct {
	ID          int64            `json:"id"`
	Key         string           `json:"-"`
	Method      string           `json:"method"`
	Path        string           `json:"path"`
	Status      int              `json:"status"`
	ContentType string           `json:"contentType"`
	Body        jsonx.RawMessage `json:"-"`
	BodyText    string           `json:"bodyText"`
	Redacted    bool             `json:"redacted"`
	CreatedAt   int64            `json:"createdAt"`
	UpdatedAt   int64            `json:"updatedAt"`
}

func (r *Repo) Get(ctx context.Context, id int64) (Config, error) {
	c := DefaultConfig()
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var raw string
		var version int64
		err := tx.QueryRowContext(ctx, "SELECT version, config FROM proxy_configs WHERE workspace_id=?", id).Scan(&version, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = jsonx.Unmarshal([]byte(raw), &c); err != nil {
			return err
		}
		c.Version = version
		if c.Operations == nil {
			c.Operations = map[string]string{}
		}
		return nil
	})
	return c, err
}
func nextVersion(ctx context.Context, tx *sql.Tx) (int64, error) {
	result, err := tx.ExecContext(ctx, "INSERT INTO proxy_versions DEFAULT VALUES")
	if err != nil {
		return 0, err
	}
	version, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM proxy_versions WHERE id=?", version)
	return version, err
}
func advanceVersion(ctx context.Context, tx *sql.Tx, id int64) error {
	version, err := nextVersion(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE proxy_configs SET version=? WHERE workspace_id=?", version, id)
	return err
}
func (r *Repo) Save(ctx context.Context, id int64, c Config) (Config, error) {
	if c.Operations == nil {
		c.Operations = map[string]string{}
	}
	expected := c.Version
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		var version int64
		err := tx.QueryRowContext(ctx, "SELECT version FROM proxy_configs WHERE workspace_id=?", id).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if version != expected {
			return ErrConflict
		}
		c.Version, err = nextVersion(ctx, tx)
		if err != nil {
			return err
		}
		raw, err := jsonx.Marshal(c)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO proxy_configs(workspace_id,version,config) VALUES(?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET version=excluded.version,config=excluded.config", id, c.Version, string(raw))
		return err
	})
	return c, err
}

func fence(ctx context.Context, tx *sql.Tx, id, version int64) error {
	var v int64
	err := tx.QueryRowContext(ctx, "SELECT version FROM proxy_configs WHERE workspace_id=?", id).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || err == nil && v != version {
		return ErrConflict
	}
	return err
}
func (r *Repo) Record(ctx context.Context, id int64, c Config, rec Recording) (bool, error) {
	changed := false
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, id, c.Version); err != nil {
			return err
		}
		var old int64
		err := tx.QueryRowContext(ctx, "SELECT LENGTH(body) FROM proxy_recordings WHERE workspace_id=? AND request_key=?", id, rec.Key).Scan(&old)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if exists && c.Overwrite == "first" {
			return nil
		}
		var count, total int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(LENGTH(body)),0) FROM proxy_recordings WHERE workspace_id=?", id).Scan(&count, &total); err != nil {
			return err
		}
		if (!exists && count >= MaxRecordings) || total-old+int64(len(rec.Body)) > MaxRecordingBytes {
			return ErrLimit
		}
		now := time.Now().Unix()
		body := []byte(rec.Body)
		if body == nil {
			body = []byte{}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO proxy_recordings(workspace_id,request_key,method,path,status,content_type,body,redacted,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace_id,request_key) DO UPDATE SET status=excluded.status,content_type=excluded.content_type,body=excluded.body,redacted=excluded.redacted,updated_at=excluded.updated_at`, id, rec.Key, rec.Method, rec.Path, rec.Status, rec.ContentType, body, rec.Redacted, now, now)
		changed = err == nil
		return err
	})
	return changed, err
}

const recordingColumns = "id, request_key, method, path, status, content_type, body, redacted, created_at, updated_at"

func scanRecording(row store.RowScanner) (Recording, error) {
	var out Recording
	err := row.Scan(&out.ID, &out.Key, &out.Method, &out.Path, &out.Status, &out.ContentType, &out.Body, &out.Redacted, &out.CreatedAt, &out.UpdatedAt)
	out.BodyText = string(out.Body)
	return out, err
}
func (r *Repo) Lookup(ctx context.Context, id int64, key string, version int64) (Recording, error) {
	var rec Recording
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		// Fence and row lookup share one SQLite snapshot. The monotonic config
		// token also distinguishes a replacement workspace with a recycled ID.
		if err := fence(ctx, tx, id, version); err != nil {
			return err
		}
		var err error
		rec, err = scanRecording(tx.QueryRowContext(ctx, "SELECT "+recordingColumns+" FROM proxy_recordings WHERE workspace_id=? AND request_key=?", id, key))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return rec, err
}
func (r *Repo) List(ctx context.Context, id int64) ([]Recording, error) {
	out := []Recording{}
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT "+recordingColumns+" FROM proxy_recordings WHERE workspace_id=? ORDER BY id DESC", id)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			rec, err := scanRecording(rows)
			if err != nil {
				return err
			}
			out = append(out, rec)
		}
		return rows.Err()
	})
	return out, err
}
func (r *Repo) Delete(ctx context.Context, id, rid, version int64) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, id, version); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM proxy_recordings WHERE workspace_id=? AND id=?", id, rid)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return advanceVersion(ctx, tx, id)
	})
}
func (r *Repo) Clear(ctx context.Context, id, version int64, confirmSlug string) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		var slug string
		if err := tx.QueryRowContext(ctx, "SELECT slug FROM workspaces WHERE id=?", id).Scan(&slug); err != nil {
			return err
		}
		if slug != confirmSlug {
			return ErrConfirmation
		}
		if err := fence(ctx, tx, id, version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM proxy_recordings WHERE workspace_id=?", id); err != nil {
			return err
		}
		return advanceVersion(ctx, tx, id)
	})
}

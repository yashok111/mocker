package backendblob

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrCanonical = errors.New("canonical storage corrupt: restore verified backup")
var ErrDerived = errors.New("derived storage differs: offline rebuild required")

type Queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Reader interface {
	Queryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func Put(ctx context.Context, tx *sql.Tx, d Domain, raw []byte) (string, error) {
	if d.Owner == "" || d.Schema == "" || d.RecordType == "" {
		return "", fmt.Errorf("%w: empty domain", ErrCanonical)
	}
	key := Key(d, raw)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO backend_payload_blobs(key,owner,schema_version,record_type,byte_length,payload) VALUES(?,?,?,?,?,?)`, key, d.Owner, d.Schema, d.RecordType, len(raw), raw); err != nil {
		return "", err
	}
	b, err := read(ctx, tx, key)
	if err != nil {
		return "", err
	}
	if b.Domain != d || !bytes.Equal(b.Bytes, raw) {
		return "", fmt.Errorf("%w: blob collision %s", ErrCanonical, key)
	}
	return key, nil
}
func read(ctx context.Context, q Queryer, key string) (Blob, error) {
	b := Blob{Key: key}
	var n int64
	err := q.QueryRowContext(ctx, `SELECT owner,schema_version,record_type,byte_length,payload FROM backend_payload_blobs WHERE key=?`, key).Scan(&b.Domain.Owner, &b.Domain.Schema, &b.Domain.RecordType, &n, &b.Bytes)
	if err != nil {
		return b, fmt.Errorf("%w: blob %s: %w", ErrCanonical, key, err)
	}
	if int64(len(b.Bytes)) != n || Key(b.Domain, b.Bytes) != key {
		return b, fmt.Errorf("%w: blob %s hash/length", ErrCanonical, key)
	}
	return b, nil
}
func Get(ctx context.Context, q Queryer, key string) ([]byte, error) {
	b, err := read(ctx, q, key)
	return b.Bytes, err
}

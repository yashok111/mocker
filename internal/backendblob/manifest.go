package backendblob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// encodeValues preserves SQL metadata types; payloads are represented only by
// their keys. The canonical membership retains enough identity and metadata to
// recover an index even when the physical owner row is missing or damaged.
func encodeValues(values []any) ([]byte, error) {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint64(len(values)))
	for _, v := range values {
		switch x := v.(type) {
		case nil:
			b.WriteByte(0)
		case string:
			b.WriteByte(1)
			_ = binary.Write(&b, binary.BigEndian, uint64(len(x)))
			b.WriteString(x)
		case int64:
			b.WriteByte(2)
			_ = binary.Write(&b, binary.BigEndian, x)
		case []byte:
			b.WriteByte(3)
			_ = binary.Write(&b, binary.BigEndian, uint64(len(x)))
			b.Write(x)
		default:
			return nil, fmt.Errorf("unsupported canonical SQL metadata %T", v)
		}
	}
	return b.Bytes(), nil
}
func decodeValues(raw []byte, want int) ([]any, error) {
	b := bytes.NewReader(raw)
	var n uint64
	if err := binary.Read(b, binary.BigEndian, &n); err != nil || n != uint64(want) { //nolint:gosec // G115: want is a column count, never negative
		return nil, fmt.Errorf("%w: metadata arity", ErrCanonical)
	}
	out := make([]any, want)
	for i := range out {
		typ, err := b.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("%w: metadata type", ErrCanonical)
		}
		switch typ {
		case 0:
		case 2:
			var v int64
			if err := binary.Read(b, binary.BigEndian, &v); err != nil {
				return nil, err
			}
			out[i] = v
		case 1, 3:
			var size uint64
			if err := binary.Read(b, binary.BigEndian, &size); err != nil || size > uint64(b.Len()) { //nolint:gosec // G115: a reader length is never negative
				return nil, fmt.Errorf("%w: metadata length", ErrCanonical)
			}
			data := make([]byte, int(size))
			if _, err := io.ReadFull(b, data); err != nil {
				return nil, err
			}
			if typ == 1 {
				out[i] = string(data)
			} else {
				out[i] = data
			}
		default:
			return nil, fmt.Errorf("%w: metadata type %d", ErrCanonical, typ)
		}
	}
	if b.Len() != 0 {
		return nil, fmt.Errorf("%w: metadata trailing bytes", ErrCanonical)
	}
	return out, nil
}
func identity(values ...any) (string, error) {
	raw, err := encodeValues(values)
	return hex.EncodeToString(raw), err
}
func rowMap(o Owner, values []any) map[string]any {
	m := make(map[string]any, len(values))
	for i, c := range o.Columns {
		m[c.Name] = values[i]
	}
	return m
}
func ownerIdentity(ctx context.Context, q Queryer, o Owner, values []any) (project, id, member string, err error) {
	return ownerIdentityMode(ctx, q, o, values, false)
}
func ownerIdentityMode(ctx context.Context, q Queryer, o Owner, values []any, legacy bool) (project, id, member string, err error) {
	m := rowMap(o, values)
	if o.ProjectColumn != "" {
		project, _ = m[o.ProjectColumn].(string)
	} else if o.ProjectSQL != "" {
		if !legacy && strings.Contains(o.ProjectSQL, "FROM backend_revisions ") {
			var mid string
			mid, err = identity(m[o.ProjectArg])
			if err != nil {
				return
			}
			err = q.QueryRowContext(ctx, "SELECT project_id FROM backend_payload_members WHERE owner='backend_revisions' AND member_type='document' AND member_id=?", mid).Scan(&project)
		} else {
			err = q.QueryRowContext(ctx, o.ProjectSQL, m[o.ProjectArg]).Scan(&project)
		}
		if err != nil {
			return
		}
	}
	group := []any{project}
	for _, k := range o.Group {
		group = append(group, m[k])
	}
	id, err = identity(group...)
	if err != nil {
		return
	}
	var keys []any
	for _, k := range o.PK {
		keys = append(keys, m[k])
	}
	member, err = identity(keys...)
	return
}
func saveMembership(ctx context.Context, tx *sql.Tx, o Owner, values []any) error {
	return saveMembershipMode(ctx, tx, o, values, false)
}
func saveMembershipMode(ctx context.Context, tx *sql.Tx, o Owner, values []any, legacy bool) error {
	project, id, member, err := ownerIdentityMode(ctx, tx, o, values, legacy)
	if err != nil {
		return err
	}
	metadata, err := encodeValues(values)
	if err != nil {
		return err
	}
	if o.payloadCount() == 0 {
		key, e := Put(ctx, tx, Domain{o.Table + "/_metadata", "store26-metadata-v1", "_metadata"}, metadata)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO backend_payload_members(owner,owner_id,version,member_type,member_id,payload_key,metadata,project_id) VALUES(?,?,'raw-owner-v1','_metadata',?,?,?,?)`, o.Table, id, member, key, metadata, project); e != nil {
			return e
		}
	}
	for i, c := range o.Columns {
		if c.Payload {
			if _, err := tx.ExecContext(ctx, `INSERT INTO backend_payload_members(owner,owner_id,version,member_type,member_id,payload_key,metadata,project_id) VALUES(?,?,'raw-owner-v1',?,?,?,?,?)`, o.Table, id, c.Name, member, values[i], metadata, project); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO backend_payload_pending(owner,owner_id,version) VALUES(?,?,'raw-owner-v1')`, o.Table, id)
	return err
}
func manifestDigest(ctx context.Context, q Reader, owner, id, version string) (int64, string, error) {
	var count int64
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM backend_payload_members WHERE owner=? AND owner_id=? AND version=?`, owner, id, version).Scan(&count); err != nil {
		return 0, "", err
	}
	h := sha256.New()
	for _, s := range []string{"backend-payload-manifest-v1", owner, "raw-owner-v1", id, version} {
		stringField(h, s)
	}
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(count)) //nolint:gosec // G115: a count(*) is never negative
	field(h, n[:])
	rows, err := q.QueryContext(ctx, `SELECT member_type,member_id,payload_key,metadata,project_id FROM backend_payload_members WHERE owner=? AND owner_id=? AND version=? ORDER BY member_type,member_id`, owner, id, version)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var typ, mid, key, project string
		var meta []byte
		if err = rows.Scan(&typ, &mid, &key, &meta, &project); err != nil {
			return 0, "", err
		}
		for _, s := range []string{typ, mid, key, project} {
			stringField(h, s)
		}
		field(h, meta)
	}
	return count, hex.EncodeToString(h.Sum(nil)), rows.Err()
}

// Seal is called once after an application's complete write callback, before its
// receipt and memberships commit. A deferred FK prevents unsealed publication.
func Seal(ctx context.Context, tx *sql.Tx) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='backend_payload_pending' AND type='table'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	for {
		var owner, id, version string
		err := tx.QueryRowContext(ctx, `SELECT owner,owner_id,version FROM backend_payload_pending ORDER BY owner,owner_id,version LIMIT 1`).Scan(&owner, &id, &version)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		count, digest, err := manifestDigest(ctx, tx, owner, id, version)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO backend_payload_manifests VALUES(?,?,?,?,?)`, owner, id, version, count, digest); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM backend_payload_pending WHERE owner=? AND owner_id=? AND version=?`, owner, id, version); err != nil {
			return err
		}
	}
}

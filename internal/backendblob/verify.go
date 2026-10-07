package backendblob

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Verify distinguishes loss of canonical history from repairable projections.
func Verify(ctx context.Context, q Reader) error { return verify(ctx, q, true, "") }

// verify checks canonical history always and, with projections, the derived
// physical rows. A non-empty scope limits the ROW checks to that project's
// members (plus the project-less, content-addressed ones), which is what a
// one-project rebuild can vouch for: review 2026-10-06, F163 found the final
// whole-database check let another project's derived damage roll back a
// rebuild that had nothing to do with it. Canonical checks and the schema
// objects (which every rebuild recreates) stay global.
func verify(ctx context.Context, q Reader, projections bool, scope string) error {
	if err := verifyBlobs(ctx, q); err != nil {
		return err
	}
	if err := verifyManifests(ctx, q); err != nil {
		return err
	}
	var unsealed int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM backend_payload_members m LEFT JOIN backend_payload_manifests f ON f.owner=m.owner AND f.owner_id=m.owner_id AND f.version=m.version WHERE f.owner IS NULL`).Scan(&unsealed); err != nil {
		return err
	}
	if unsealed != 0 {
		return fmt.Errorf("%w: unsealed members", ErrCanonical)
	}
	var derived error
	if projections {
		derived = canonicalIndexFault(ctx, q)
	}
	for _, o := range owners {
		if err := checkCanonicalCoverage(ctx, q, o); err != nil {
			return err
		}
		faults := false
		err := walkMembers(ctx, q, o, func(id, version, typ, mid, key, project string, meta []byte, values []any) error {
			if err := verifyMember(ctx, q, o, id, version, typ, mid, key, project, meta, values); err != nil {
				return err
			}
			if !projections || (scope != "" && project != scope && project != "") {
				return nil
			}
			fault, err := projectionFault(ctx, q, o, meta, values)
			faults = faults || fault
			return err
		})
		if err != nil {
			return err
		}
		if projections {
			d, err := verifyOwnerProjection(ctx, q, o, scope, faults)
			if err != nil {
				return err
			}
			if d != nil {
				derived = d
			}
		}
	}
	return derived
}

// canonicalIndexFault reports the last derived lookup index whose definition
// is missing or differs (verify's split, review 2026-10-06, F163, kept every
// check it made in the order it made them).
func canonicalIndexFault(ctx context.Context, q Reader) error {
	var derived error
	for _, obj := range canonicalIndexes {
		var ddl string
		if err := q.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='index' AND name=?", obj.Name).Scan(&ddl); err != nil || normalizeSQL(ddl) != normalizeSQL(obj.SQL) {
			derived = fmt.Errorf("%w: canonical lookup index %s", ErrDerived, obj.Name)
		}
	}
	return derived
}

func verifyBlobs(ctx context.Context, q Reader) error {
	rows, err := q.QueryContext(ctx, `SELECT key FROM backend_payload_blobs ORDER BY key`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err == nil {
			_, err = Get(ctx, q, key)
		}
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func verifyManifests(ctx context.Context, q Reader) error {
	rows, err := q.QueryContext(ctx, `SELECT owner,owner_id,version,member_count,digest FROM backend_payload_manifests ORDER BY owner,owner_id,version`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var owner, id, version, digest string
		var count int64
		if err = rows.Scan(&owner, &id, &version, &count, &digest); err != nil {
			return err
		}
		if _, err = Lookup(owner); err != nil {
			return fmt.Errorf("%w: unknown manifest owner", ErrCanonical)
		}
		n, d, e := manifestDigest(ctx, q, owner, id, version)
		if e != nil {
			return e
		}
		if n != count || d != digest || version != "raw-owner-v1" {
			return fmt.Errorf("%w: manifest %s/%s", ErrCanonical, owner, id)
		}
	}
	return rows.Err()
}

// verifyMember checks one canonical member against its blob, its domain and
// its owner identity: every failure here is canonical loss.
func verifyMember(ctx context.Context, q Reader, o Owner, id, version, typ, mid, key, project string, meta []byte, values []any) error {
	b, err := read(ctx, q, key)
	if err != nil {
		return err
	}
	if o.payloadCount() == 0 {
		if typ != "_metadata" || b.Domain != (Domain{o.Table + "/_metadata", "store26-metadata-v1", "_metadata"}) || !bytes.Equal(b.Bytes, meta) {
			return fmt.Errorf("%w: canonical mapping", ErrCanonical)
		}
	} else if err := verifyPayloadDomain(ctx, q, o, typ, values, b); err != nil {
		return err
	}
	expectedProject, expectedID, expectedMember, err := ownerIdentity(ctx, q, o, values)
	if err != nil {
		return fmt.Errorf("%w: owner authorization %s: %w", ErrCanonical, o.Table, err)
	}
	if expectedProject != project || expectedID != id || expectedMember != mid || version != "raw-owner-v1" {
		return fmt.Errorf("%w: owner identity", ErrCanonical)
	}
	for i, col := range o.Columns {
		if col.Name == typ && values[i] != key {
			return fmt.Errorf("%w: metadata payload binding", ErrCanonical)
		}
	}
	return nil
}

func verifyPayloadDomain(ctx context.Context, q Reader, o Owner, typ string, values []any, b Blob) error {
	c, ok := o.column(typ)
	if !ok || !c.Payload || c.Name != typ {
		return fmt.Errorf("%w: invalid member type", ErrCanonical)
	}
	d, err := domainFor(ctx, q, o, c, values, b.Bytes, false)
	if err != nil {
		return fmt.Errorf("%w: member domain: %w", ErrCanonical, err)
	}
	if d != b.Domain {
		return fmt.Errorf("%w: wrong member domain %s", ErrCanonical, o.Table)
	}
	return nil
}

// projectionFault reports a missing or differing physical row for a member:
// derived damage a rebuild repairs, not an error.
func projectionFault(ctx context.Context, q Reader, o Owner, meta []byte, values []any) (bool, error) {
	got, err := physicalRow(ctx, q, o, values)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	raw, err := encodeValues(got)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(raw, meta), nil
}

// verifyOwnerProjection returns the derived fault for one owner table, if
// any; the error return is reserved for failures that are not derived.
func verifyOwnerProjection(ctx context.Context, q Reader, o Owner, scope string, faults bool) (error, error) {
	var derived error
	var actual, canonical int64
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM "+o.Table).Scan(&actual); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrDerived, o.Table, err)
	}
	if err := q.QueryRowContext(ctx, "SELECT count(DISTINCT member_id) FROM backend_payload_members WHERE owner=?", o.Table).Scan(&canonical); err != nil {
		return nil, err
	}
	// The global count cannot be scoped (physical rows carry no uniform
	// project column), so a scoped check drops it: an in-scope row that is
	// missing is a member fault, and an extra row with no members is
	// canonical loss in checkCanonicalCoverage.
	if (scope == "" && actual != canonical) || faults {
		derived = fmt.Errorf("%w: %s row metadata/count", ErrDerived, o.Table)
	}
	var viewSQL string
	if err := q.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='view' AND name=?", o.Table+"_documents").Scan(&viewSQL); err != nil || normalizeSQL(viewSQL) != normalizeSQL(o.ViewSQL()) {
		derived = fmt.Errorf("%w: read view %s", ErrDerived, o.Table)
	}
	for _, obj := range o.Objects {
		var sqlText string
		if err := q.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type=? AND name=?", obj.Kind, obj.Name).Scan(&sqlText); err != nil || normalizeSQL(sqlText) != normalizeSQL(obj.SQL) {
			derived = fmt.Errorf("%w: schema object %s", ErrDerived, obj.Name)
		}
	}
	return derived, nil
}
func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(s), ";")), " ")
}
func walkMembers(ctx context.Context, q Reader, o Owner, fn func(string, string, string, string, string, string, []byte, []any) error) error {
	rows, err := q.QueryContext(ctx, `SELECT owner_id,version,member_type,member_id,payload_key,project_id,metadata FROM backend_payload_members WHERE owner=? ORDER BY owner_id,version,member_id,member_type`, o.Table)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, version, typ, mid, key, project string
		var meta []byte
		if err = rows.Scan(&id, &version, &typ, &mid, &key, &project, &meta); err != nil {
			return err
		}
		values, err := decodeValues(meta, o.metadataCount())
		if err != nil {
			return err
		}
		if err = fn(id, version, typ, mid, key, project, meta, values); err != nil {
			return err
		}
	}
	return rows.Err()
}
func physicalRow(ctx context.Context, q Queryer, o Owner, values []any) ([]any, error) {
	m := rowMap(o, values)
	where := make([]string, len(o.PK))
	args := make([]any, len(o.PK))
	for i, k := range o.PK {
		where[i] = k + "=?"
		args[i] = m[k]
	}
	out := make([]any, o.metadataCount())
	ptrs := make([]any, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	err := q.QueryRowContext(ctx, "SELECT "+strings.Join(o.projectionColumns(), ",")+" FROM "+o.Table+" WHERE "+strings.Join(where, " AND "), args...).Scan(ptrs...)
	return out, err
}

// A surviving owner with no canonical member is canonical loss, not permission
// to delete the row during recovery. Fail closed even if a whole manifest and
// all its members were removed together.
func checkCanonicalCoverage(ctx context.Context, q Reader, o Owner) error {
	rows, err := q.QueryContext(ctx, "SELECT "+strings.Join(o.PK, ",")+" FROM "+o.Table)
	if err != nil {
		return fmt.Errorf("%w: missing physical owner %s: %w", ErrDerived, o.Table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		values := make([]any, len(o.PK))
		ptrs := make([]any, len(values))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return err
		}
		mid, err := identity(values...)
		if err != nil {
			return err
		}
		var count int
		if err = q.QueryRowContext(ctx, "SELECT count(*) FROM backend_payload_members WHERE owner=? AND member_id=?", o.Table, mid).Scan(&count); err != nil {
			return err
		}
		want := 0
		for _, c := range o.Columns {
			if c.Payload {
				want++
			}
		}
		if want == 0 {
			want = 1
		}
		if count != want {
			return fmt.Errorf("%w: lost ownership %s/%s", ErrCanonical, o.Table, mid)
		}
	}
	return rows.Err()
}

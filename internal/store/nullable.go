package store

import (
	"database/sql"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

// MarshalNullable encodes v as a sql.NullString for a nullable JSON column —
// a nil v is the SQL NULL every such column in this tree already uses for
// "not set" (list_size, delay_ms, and their kin), never the JSON literal
// "null" jsonx.Marshal(nil) would produce, which no reader of these columns
// expects.
//
// Until 2026-09-07 internal/overrides and internal/customep each carried
// their own marshalListSize/marshalDelayMs pair, identical modulo the
// pointed-to type — store may import internal/jsonx (the reverse would be
// the boundary violation, and jsonx must stay a thin codec with no callers
// of its own), so this is where the pair's shared shape belongs, generic
// over the one thing that differed between the copies.
func MarshalNullable[T any](v *T) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := jsonx.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

// UnmarshalNullable decodes ns into a freshly allocated *T, or returns nil
// when ns carries SQL NULL — [MarshalNullable]'s "not set". prefix is the
// caller's OWN already-formatted error context (e.g. "override %d: decode
// list_size" with the row's id already substituted): this function has no
// row to name, so every call site's existing message text — "endpoint %d:
// decode …" and "override %d: decode …" differ only in that prefix — is
// preserved exactly by taking it as a parameter rather than this function
// inventing its own.
func UnmarshalNullable[T any](ns sql.NullString, prefix string) (*T, error) {
	if !ns.Valid {
		return nil, nil
	}
	var v T
	if err := jsonx.Unmarshal([]byte(ns.String), &v); err != nil {
		return nil, fmt.Errorf("%s: %w", prefix, err)
	}
	return &v, nil
}

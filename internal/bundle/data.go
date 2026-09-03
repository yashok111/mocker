// This file owns the P3d data-snapshot document — DataBundle, the JSON
// shape a checkpoint's `data_snap` column carries. It is a SECOND document
// type, not a field on [Bundle]: D3 of the P3d decision document
// (`mocker-p3d-datasnap/artifacts/decisions.md`, a gate workspace outside this repository) walks the
// three shapes considered and why this is the one that shipped — in short,
// [Bundle.Entities] staying null and unconditionally REFUSED by [Validate]
// is a structural guarantee ("no capture site can quietly start carrying
// entity rows a restore has no rule for"), and making that refusal
// conditional on which consumer is calling would turn a guarantee into a
// parameter. A separate type makes the wrong shape unreachable rather than
// merely unexercised, the same move P3c made for the workspace boundary of
// a `ref`.
//
// DataBundle addresses a row's family by `route_family`, never by
// `resources.id` — the identical decision P3c made for the `ref` recipe,
// and for the identical reason (D4): `resources.id` is not stable across
// decline-then-reconfirm (SQLite is free to reuse a deleted rowid with no
// AUTOINCREMENT on the column), while `route_family` is the table's own
// UNIQUE key. See D4 for the full argument and for what the document
// deliberately does NOT carry (entities.id, parent_entity_id, resources.seq
// — each for its own stated reason). P3e's D9, re-decided the same way at
// every depth by P3g D9, is why parent_entity_id is among them BY DESIGN,
// not pending: a self-FK cascade on entities would reach past a restore's
// own per-family DELETE into a family — a DESCENDANT one, not necessarily a
// sibling, and at depth a config rollback naming one root risks reaching
// two more levels of families — the call never named (D9.2, D9.3), a
// larger loss than the orphan-reachable-again
// gap this document accepts instead (D9.1).
package bundle

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/yashok111/mocker/internal/jsonx"
)

// DataVersion is the mockerData value this file WRITES. It is its own
// constant, not [CurrentVersion], for the same reason DataBundle is its own
// type and not a field on Bundle: the two documents live in separate columns
// (config_snap, data_snap) and a version bump to one must never force a
// reader to reinterpret the other's bytes.
//
// P3h raises it from 1 to 2 for [EntityRow.BaseScopeKey] — but [DecodeData]
// and [ValidateData] still ACCEPT a version-1 document (see
// [minDataVersion]): refusing one would delete the only undo P3d shipped for
// a decline and for reset-data from every checkpoint taken before this
// slice (D9).
const DataVersion = 2

// minDataVersion is the oldest mockerData value [DecodeData] and
// [ValidateData] still restore. A version-1 document has no baseScopeKey
// key in its JSON at all (the field did not exist), so jsonx.Unmarshal
// leaves every row's BaseScopeKey at its zero value "" — the empty base
// scope, exactly the meaning D9 asks for: every row of a pre-P3h checkpoint
// lands where a workspace whose base path carries no parameter has always
// served from. Nothing here upgrades the bytes; a version-1 document stays
// version-1 in the database and is reinterpreted the same way on every read.
const minDataVersion = 1

// DataBundle is the whole of a checkpoint's entity-row capture: every
// confirmed family the workspace held at capture time, keyed by its
// route_family string, each carrying every entity row belonging to it.
//
// A family entry is present with `rows: []` when the family was confirmed
// and empty at capture — never omitted (D4). Omitting an empty family would
// mean "this checkpoint has no opinion on this family's rows", which is
// wrong: it has the opinion "there were none", and a restore must be able
// to empty a family that was populated by an anonymous POST X after the
// checkpoint was taken. Only a family this document does not mention at
// all is left untouched by a restore — see internal/checkpoints' own D6 for
// that half of the rule; this file only defines the SHAPE.
type DataBundle struct {
	MockerData int           `json:"mockerData"`
	Families   []FamilyEntry `json:"families"`
}

// FamilyEntry is one confirmed family's captured rows.
type FamilyEntry struct {
	RouteFamily string      `json:"routeFamily"`
	Rows        []EntityRow `json:"rows"`
}

// EntityRow is one entities row, carrying exactly the columns a restore's
// natural-key UPSERT needs and nothing a restore would have to invent a
// rule for. Deliberately absent, each for its own reason (D4):
//
//   - id — a surrogate nothing outside the table addresses a row by; the
//     natural key is (resource_id, scope_key, entity_key) and the mock
//     plane itself serves GET X/{} by entity_key, never by rowid.
//   - parent_entity_id — NULL by design, not NULL pending a slice: P3e's D9,
//     re-decided the same way at every depth by P3g's own D9, keeps the
//     column unwritten on every row this build mints, at any level of a
//     chain, because a live self-FK cascade on entities would let one
//     family's restore silently empty a DESCENDANT family's rows — not
//     necessarily a sibling's — that the checkpoint never named (D9.2,
//     D9.3) — a larger hole than the one this build accepts instead (D9.1,
//     D9.5). A capture that found a non-null value would be capturing a
//     build that does not exist.
//   - the resources.seq counter this key was allocated from — carrying it
//     here would be a DEFECT, not a redundancy: it is already restored, by
//     the OTHER half of the same rollback, via
//     `seq = MAX(excluded.seq, resources.seq)` inside the identical
//     transaction (internal/checkpoints), computed in SQL precisely so a
//     restore can never move it backwards below a key a live POST minted
//     in between. A second copy of the same number in this document would
//     give it two sources of truth in one transaction, disagreeing exactly
//     when the MAX rule fires.
//
// EntityKey is the RAW captured key, replayed verbatim on restore — never
// re-derived positionally. It is not always the decimal form of
// resources.seq: Confirm and reseed both assign keys positionally
// (strconv.Itoa(i+1)) while EntityStore.Create allocates one from the
// counter; the two coincide only because a fresh population starts at 1. A
// captured key set need not be a contiguous 1..N.
//
// BaseScopeKey (P3h, D9) is the row's entities.base_scope_key, captured and
// restored verbatim exactly like ScopeKey — an opaque string this file never
// parses, encoded by the one owner, resources.EncodeScope (D3.1: a second
// encoder or decoder anywhere is the defect this rule forbids, and that
// includes here). `omitempty`: the overwhelming majority of rows belong to
// workspaces whose base path carries no parameter and capture the empty
// string, and an already-large document should not carry a key that says
// nothing beyond "there is no base scope" on every one of them.
type EntityRow struct {
	ScopeKey     string           `json:"scopeKey"`
	EntityKey    string           `json:"entityKey"`
	BaseScopeKey string           `json:"baseScopeKey,omitempty"`
	Data         jsonx.RawMessage `json:"data"`
	CreatedAt    int64            `json:"createdAt"`
	UpdatedAt    int64            `json:"updatedAt"`
}

// EncodeData validates d (the same [ValidateData] a restore itself must
// call before trusting a decoded document — D14/D15 note that call is the
// restore's own, deliberately not folded into [DecodeData]) and returns its
// canonical byte form: struct field order pins the envelope, and the sort
// below makes the order of Families and of each family's Rows part of the
// document's MEANING rather than an accident of whatever order a SELECT
// happened to return them in — two values differing only in that order
// encode to identical bytes, which is [Encode]'s own byte-stability
// property (A19) applied to this document.
//
// Unlike [Encode], this function does not round-trip any nested payload
// through canonicalizeRaw: EntityRow.Data is the raw bytes an entity's own
// row already holds, written once at confirm/populate time through
// internal/jsonx and never hand-edited afterward (there is no editor for a
// confirmed entity — P3a's own carve-out), so there is no second write path
// whose key order this document would need to normalise against.
func EncodeData(d DataBundle) ([]byte, error) {
	if err := ValidateData(d); err != nil {
		return nil, err
	}
	canon := canonicalizeData(d)
	out, err := jsonx.Marshal(canon)
	if err != nil {
		return nil, fmt.Errorf("bundle: encode data: marshal: %w", err)
	}
	return out, nil
}

// DecodeData parses data and gates its version ONLY — it deliberately does
// NOT run [ValidateData], unlike [Decode]'s own call to [Validate]. The
// restore path this document exists for calls ValidateData itself, on the
// decoded value, as its own explicit step (D15); a second call here would
// make that mandated call redundant and leave a mutation of ValidateData
// with no test that could observe it going missing — the same reasoning
// that keeps this file from duplicating any other rule D7 already owns.
func DecodeData(data []byte) (DataBundle, error) {
	var d DataBundle
	if err := jsonx.Unmarshal(data, &d); err != nil {
		return DataBundle{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if d.MockerData < minDataVersion || d.MockerData > DataVersion {
		return DataBundle{}, fmt.Errorf("%w: mockerData %d, this build only knows versions %d..%d",
			ErrInvalid, d.MockerData, minDataVersion, DataVersion)
	}
	return d, nil
}

// ValidateData refuses exactly FIVE shapes — this is its specification, not
// an observation about it (D4 moved the domain here in round 4's cut, out
// of an acceptance property that was defeated three times trying to state
// it as one):
//
//  1. mockerData is outside [minDataVersion]..[DataVersion].
//  2. Two entries share a routeFamily.
//  3. A routeFamily is empty.
//  4. Two rows of one family share (scopeKey, entityKey).
//  5. An entityKey is not a decimal integer string.
//
// The fourth stays narrow at (scopeKey, entityKey) and deliberately does NOT
// widen to include baseScopeKey (P3h, D9): it mirrors the physical
// UNIQUE (resource_id, scope_key, entity_key), which D5.2 leaves narrow on
// the grounds that entity_key is minted from one family-wide counter across
// every base scope (P3g's rule) — so two rows of one family never share an
// entityKey at all, in any base scope, and this pair already identifies at
// most one row. Widening only the codec's half would let a document VALIDATE
// and then fail at the restore's own INSERT with a constraint error instead
// of a validation error — a refusal in the wrong layer, naming the wrong
// thing, on the one path a workspace's entity data is recovered by.
//
// The fifth looks optional and is not: SQLite's `CAST('abc' AS INTEGER)` is
// `0`, so a non-decimal key would pass a SQL restore silently and leave the
// seq counter below a key that exists — the invariant a restore needs to
// hold is not even STATABLE over such a key, which is why this function
// refuses it before any caller reaches SQL at all.
func ValidateData(d DataBundle) error {
	if d.MockerData < minDataVersion || d.MockerData > DataVersion {
		return fmt.Errorf("%w: mockerData %d, this build only knows versions %d..%d",
			ErrInvalid, d.MockerData, minDataVersion, DataVersion)
	}

	seenFamilies := make(map[string]bool, len(d.Families))
	for i, f := range d.Families {
		if f.RouteFamily == "" {
			return fmt.Errorf("%w: families[%d]: routeFamily must not be empty", ErrInvalid, i)
		}
		if seenFamilies[f.RouteFamily] {
			return fmt.Errorf("%w: families[%d]: duplicate routeFamily %q", ErrInvalid, i, f.RouteFamily)
		}
		seenFamilies[f.RouteFamily] = true

		type rowKey struct{ scope, entity string }
		seenRows := make(map[rowKey]bool, len(f.Rows))
		for j, r := range f.Rows {
			if !isDecimalIntegerString(r.EntityKey) {
				return fmt.Errorf("%w: families[%d] (%s) rows[%d]: entityKey %q is not a decimal integer string",
					ErrInvalid, i, f.RouteFamily, j, r.EntityKey)
			}
			if !isJSONObject(r.Data) {
				// The row body is served verbatim as an entity, and every
				// other writer of one stores a JSON object; an import must
				// not be the one door a null or a bare string comes in by.
				return fmt.Errorf("%w: families[%d] (%s) rows[%d]: data must be a JSON object",
					ErrInvalid, i, f.RouteFamily, j)
			}
			key := rowKey{r.ScopeKey, r.EntityKey}
			if seenRows[key] {
				return fmt.Errorf("%w: families[%d] (%s) rows[%d]: duplicate row (scopeKey=%q, entityKey=%q)",
					ErrInvalid, i, f.RouteFamily, j, r.ScopeKey, r.EntityKey)
			}
			seenRows[key] = true
		}
	}
	return nil
}

// isDecimalIntegerString reports whether key is the canonical decimal form
// of an int64 — parseable AND round-tripping back to the identical string.
// The round-trip is what catches what a bare strconv.ParseInt would let
// through silently: "+7" and "007" both parse, but neither is the string
// EntityRow.EntityKey's own doc comment says a restore replays VERBATIM
// (strconv.Itoa never produces either form), so accepting them here would
// let a corrupt or hand-edited snapshot restore a key no live write path
// could ever have produced.
func isDecimalIntegerString(key string) bool {
	n, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		return false
	}
	return strconv.FormatInt(n, 10) == key
}

// canonicalizeData returns a COPY of d with Families sorted by RouteFamily
// and each family's Rows sorted by the compound key
// (ScopeKey, EntityKey-as-decimal-integer) — never a plain string compare
// on EntityKey, which [compareEntityRows]'s own comment explains. d itself
// is never mutated: a caller that goes on to reuse it after calling
// [EncodeData] must see exactly what it built, the same promise [canonicalize]
// makes for [Bundle].
func canonicalizeData(d DataBundle) DataBundle {
	out := d
	if len(d.Families) == 0 {
		return out
	}
	families := make([]FamilyEntry, len(d.Families))
	for i, f := range d.Families {
		rows := make([]EntityRow, len(f.Rows))
		copy(rows, f.Rows)
		slices.SortFunc(rows, compareEntityRows)
		families[i] = FamilyEntry{RouteFamily: f.RouteFamily, Rows: rows}
	}
	slices.SortFunc(families, func(a, b FamilyEntry) int {
		return cmp.Compare(a.RouteFamily, b.RouteFamily)
	})
	out.Families = families
	return out
}

// compareEntityRows is the compound sort D4 asks for: ScopeKey compared
// lexically first (the natural key's own leading column), EntityKey second
// — compared as a DECIMAL INTEGER, not lexically, so "2" sorts before "10"
// the way the counter that minted both actually orders them, rather than
// by SQLite's or Go's default string collation. [ValidateData] has already
// rejected any row whose EntityKey does not parse by the time this runs
// (both of this file's exported entry points reach ValidateData before a
// sort is ever attempted), so the parse below cannot fail on a document
// that passed validation; it is written to ignore rather than propagate a
// parse error for exactly that reason — there is no error channel a
// [slices.SortFunc] comparator has to report through.
func compareEntityRows(a, b EntityRow) int {
	if c := cmp.Compare(a.ScopeKey, b.ScopeKey); c != 0 {
		return c
	}
	an, _ := strconv.ParseInt(a.EntityKey, 10, 64)
	bn, _ := strconv.ParseInt(b.EntityKey, 10, 64)
	return cmp.Compare(an, bn)
}

// isJSONObject reports whether raw is a syntactically valid JSON object.
func isJSONObject(raw jsonx.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{' && jsonx.Valid(t)
}

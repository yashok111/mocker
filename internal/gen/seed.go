package gen

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
)

// --- hash64: a stable, explicit FNV-1a (64-bit) mix -----------------------
//
// Every seed layer in this package (SeedList, SeedItemByID, SeedScalar,
// IDForIndex) is built on this ONE primitive. It is deliberately NOT
// hash/maphash (whose seed is randomized per PROCESS — the opposite of
// DESIGN §9's determinism contract: "один и тот же запрос даёт один и тот
// же ответ") and NOT anything from math/rand (a package-level RNG is
// exactly the shared mutable state the "fully synchronous, no goroutine, no
// shared generator state" rule forbids — two concurrent requests must not
// be able to interleave into each other's values). FNV-1a's constants are
// the well-known 64-bit ones, written out explicitly rather than imported
// from hash/fnv, so the algorithm lives entirely in this file and a body
// generated today is byte-identical to the same body generated after a
// process restart, on a different machine, or after a stdlib version bump.
const (
	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211
)

// fnv1a folds data into the running digest h, one byte at a time.
func fnv1a(h uint64, data []byte) uint64 {
	for _, b := range data {
		h ^= uint64(b)
		h *= fnvPrime64
	}
	return h
}

// hash64 mixes an arbitrary number of byte-string parts into one 64-bit
// digest. Each part is preceded by its own 8-byte big-endian length, so
// hash64([]byte("ab"), []byte("c")) can never collide with
// hash64([]byte("a"), []byte("bc")) — without the length prefix, plain
// concatenation would make the two indistinguishable, which would let two
// DIFFERENT (method, path, params) tuples land on the same seedList.
func hash64(parts ...[]byte) uint64 {
	h := fnvOffset64
	var lenBuf [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(p)))
		h = fnv1a(h, lenBuf[:])
		h = fnv1a(h, p)
	}
	return h
}

// uint64Bytes is the big-endian byte representation of u, for feeding a
// previously-computed seed/digest into hash64 as one more part (layering
// seeds: seedScalar is a function of seedList, seedItemByID is a function
// of seedList, and so on).
func uint64Bytes(u uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], u)
	return b[:]
}

// --- Seed layers (DESIGN §9 "Детерминизм") ---------------------------------

// SeedList is the root seed for one route+status: everything a list route
// and its sibling detail route derive their per-item seeds from. It
// deliberately excludes query parameters (DESIGN §9: "БЕЗ query" —
// pagination params like ?offset=20 must select which SLICE of a set is
// returned, never change which set exists), and path parameters are sorted
// by name so the same {a}/{b} pair hashes identically regardless of the
// order router.Match happened to populate the map in — map iteration order
// in Go is randomized per process, and SeedList must not be.
func SeedList(opts Options, req Request) uint64 {
	names := make([]string, 0, len(req.PathParams))
	for name := range req.PathParams {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([][]byte, 0, 4+2*len(names))
	parts = append(parts,
		uint64Bytes(uint64(opts.Seed)),
		[]byte(req.Method),
		[]byte(req.CanonicalPath),
	)
	for _, name := range names {
		parts = append(parts, []byte(name), []byte(req.PathParams[name]))
	}
	parts = append(parts, []byte(strconv.Itoa(req.Status)))
	return hash64(parts...)
}

// SeedItemByID is the seed for ONE item identified by id, within the family
// rooted at seedList. A list row at global index i and the detail route for
// the SAME id both reach this the same way: the row's id at index i is
// IDForIndex(seedList, i, ...), and the row's OTHER fields — like the detail
// card's — come from SeedItemByID(seedList, thatID's canonical string form),
// never from the index directly. That is what makes "click the row, land on
// the same object" true for any id, including one outside the current page
// window.
func SeedItemByID(seedList uint64, id string) uint64 {
	return hash64(uint64Bytes(seedList), []byte(id))
}

// SeedScalar is the seed for one field within an item (or within a
// non-list body), addressed by dataPath — e.g. "user.profile.avatar_url".
// seed is EITHER a SeedList (top-level, non-list bodies) or a
// SeedItemByID/item-local seed (inside one list item): inside an item,
// dataPath restarts at the item root ("name", not "items[3].name") so a
// list row's field and the matching detail card's field hash identically —
// that restart is the caller's job (the List agent), not something this
// function does on its own.
func SeedScalar(seed uint64, dataPath string) uint64 {
	return hash64(uint64Bytes(seed), []byte(dataPath))
}

// --- Item identity (DESIGN §9 + the launch identity decision) --------------

// IDForIndex derives the id of the item at GLOBAL index i within the family
// rooted at seedList, shaped by idSchema — the (already resolved) schema
// node for the id property itself. It returns the value in its OWN typed
// form (an int64/float64/string/bool matching idSchema's declared type,
// never a bare hash or a raw path-param string) plus that same value's
// canonical string form, which is what SeedItemByID and a detail route's
// path parameter are compared against.
//
// The typed form matters because it is written INTO the generated item as
// the id property's own value: a detail route's path parameter arrives as a
// URL string, but the id property it fills in must come out shaped like the
// schema says (an {type: integer, format: uint} id schema must produce a
// JSON number, not the string "42") or the body fails its own schema and
// disagrees with the list row on the one field that identifies them.
//
// idSchema may be nil or not a map (id property missing from the schema, or
// left unresolved by the caller) — IDForIndex still returns something (a
// plain decimal string), it just can't shape it to a declared type that
// doesn't exist.
func IDForIndex(seedList uint64, i int, idSchema any) (any, string) {
	h := hash64(uint64Bytes(seedList), []byte("id"), uint64Bytes(uint64(int64(i))))
	return typedIDFromHash(h, idSchema)
}

// typedIDFromHash shapes digest h into a value matching schema's declared
// type (falling back to a plain decimal string when schema says nothing
// useful), and reports that value's canonical string form.
func typedIDFromHash(h uint64, schema any) (any, string) {
	m, _ := schema.(map[string]any)
	t := ""
	format := ""
	if m != nil {
		t = PrimaryIDType(m)
		format, _ = m["format"].(string)
	}

	switch t {
	case "integer":
		v := idInteger(h, format)
		return v, strconv.FormatInt(v, 10)
	case "number":
		v := float64(h%1_000_000) / 100
		return v, strconv.FormatFloat(v, 'f', -1, 64)
	case "boolean":
		v := h%2 == 0
		return v, strconv.FormatBool(v)
	case "string":
		s := idString(h, format)
		return s, s
	default:
		// No usable schema: fall back to a plain decimal string, the most
		// common id shape when a spec leaves the id property untyped.
		v := int64(h % 1_000_000_000)
		return strconv.FormatInt(v, 10), strconv.FormatInt(v, 10)
	}
}

// PrimaryIDType reads schema's "type" (string or array form, per JSON
// Schema 2020-12 / OAS 3.1 union types), preferring the first non-"null"
// entry — an id property is occasionally declared nullable, but its
// identity value itself is never actually null.
//
// Exported (P3a): resource derivation (internal/specs) reads a family's
// detail id type at import time with this exact function, so that the type
// [CoerceIDValue] later coerces a POSTed id into at serve time is always
// the type this function would compute right now — one reader, not a
// second copy that could drift from it.
func PrimaryIDType(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return t
	case []any:
		for _, v := range t {
			if s, ok := v.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

// idInteger maps h onto a positive integer range appropriate for format:
// int32/uint32-ish formats stay within a 31-bit range; int64/uint64 and the
// acceptance fixture's own "uint" format stay within a wider but still
// JSON-number-exact range (Go's encoding/json writes an int64 as exact
// decimal text, so precision loss above 2^53 — a JavaScript float64
// concern — does not apply here). Ids are kept positive: "-4821" is a legal
// integer but not a value any real API hands out as an id.
func idInteger(h uint64, format string) int64 {
	switch format {
	case "int64", "uint64", "uint":
		return int64(h % (1 << 40))
	default: // "int32", "uint32", "" and anything else
		return int64(h % (1 << 31))
	}
}

// idString produces a lowercase hex token, or — for format "uuid" — a
// value shaped like RFC 4122 layout (8-4-4-4-12 hex groups). Neither claims
// to be a cryptographically meaningful UUID (no version/variant bits are
// forced); the point is a stable, schema-shaped, humanly-recognizable id.
func idString(h uint64, format string) string {
	h2 := fnv1a(h, []byte("id-lo"))
	if format == "uuid" {
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uint32(h>>32), uint16(h>>16), uint16(h),
			uint16(h2>>48), h2&0xFFFFFFFFFFFF)
	}
	return fmt.Sprintf("id-%016x", h)
}

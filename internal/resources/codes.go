// codes.go names the wire vocabulary of an ENTITY WRITE's refusals, once,
// next to the sentinels themselves.
//
// Three callers used to carry a hand-written copy of the same five-way
// switch: the mock plane's POST/DELETE X (mockplane/resource.go), the admin
// plane's PUT/DELETE .../entities/{key} (admin/entity_write_handlers.go),
// and the Lua host's storeErr (mockplane/function.go). They agreed by
// accident and nothing checked it — `write_busy` in particular was a bare
// string literal at all four of its sites while every sibling code had a
// named constant — so a SIXTH sentinel had to be remembered in three files,
// in three packages, and the one that forgot it answered `store_failed` or
// a 500 with no name at all.
//
// The table is modelled on admin/refusal_codes.go's namedRefusals, which
// solved the identical problem for a custom endpoint's write-time refusals:
// the sentinel and the word are side by side so a reader sees that they
// agree, membership is asked with errors.Is and never by string, and a test
// pins that the list is complete.
package resources

import "errors"

// The entity-write codes as they appear in an error envelope. They are
// constants and not just table cells because a screen and the embedded
// guide quote them, and because the two planes' handlers read better
// naming the code they answer than indexing a slice.
const (
	// CodeUnknownFamily is the 404 both planes answer for a family that is
	// not confirmed — including one declined out from under an in-flight
	// request (ErrResourceGone), which from the caller's side is
	// indistinguishable from never having been confirmed.
	CodeUnknownFamily = "unknown_family"
	// CodeEntityLimit is the 409 over a cap — the row cap, the total byte
	// cap or the per-entity byte cap, all three ErrEntityLimit.
	CodeEntityLimit = "entity_limit"
	// CodeEntityKeyConflict is PUT's 409 for a key that already exists in
	// another base scope of the family.
	CodeEntityKeyConflict = "entity_key_conflict"
	// CodeEntityInvalidKey refuses a key that is not the canonical form of
	// the family's id type. The admin plane answers it for the segment
	// alphabet too, before any sentinel exists — that refusal is the
	// handler's own and is not in this table.
	CodeEntityInvalidKey = "invalid_entity_key"
	// CodeWriteBusy is the 503 for a write that could not obtain the single
	// writer connection in time. It was the one code in this set with no
	// name: four literals, two planes, and a typo in any of them would have
	// been invisible.
	CodeWriteBusy = "write_busy"
)

// entityWriteCode is one row: the sentinel, the envelope code an HTTP
// caller answers, and the shorter word a Lua function reads.
//
// The two names differ for exactly two rows, and deliberately: `bad_key`
// and `key_conflict` are published in the embedded guide (internal/guide/
// functions.md) as the strings a function's second return value carries,
// while `invalid_entity_key` and `entity_key_conflict` are published in
// api/openapi.json as the admin plane's envelope codes. Neither vocabulary
// can be renamed into the other without breaking a written promise, so the
// table carries both rather than pretending one exists.
type entityWriteCode struct {
	sentinel error
	code     string // the error envelope's code, both planes
	lua      string // the word storeErr hands a Lua function
}

// entityWriteCodes is the whole list. Adding a sentinel to this package
// that an entity write can return means adding a row here; codes_test.go
// fails the build of the test binary if one is missing.
var entityWriteCodes = []entityWriteCode{
	{ErrResourceGone, CodeUnknownFamily, "unknown_family"},
	{ErrEntityLimit, CodeEntityLimit, "entity_limit"},
	{ErrEntityKeyConflict, CodeEntityKeyConflict, "key_conflict"},
	{ErrEntityKeyNotCanonical, CodeEntityInvalidKey, "bad_key"},
	{ErrWriteBusy, CodeWriteBusy, "write_busy"},
}

// WriteRefusalCode returns the error-envelope code for an entity write's
// refusal, and false for anything not in the table — which the caller
// answers as its own 500, exactly as it did before this function existed.
// The STATUS is deliberately not returned: the two planes disagree on it
// for the same sentinel (ErrResourceGone is a 404 in admin and a
// fall-through to the generator in the mock plane), and a shared status
// would have to be wrong for one of them.
func WriteRefusalCode(err error) (string, bool) {
	for _, c := range entityWriteCodes {
		if errors.Is(err, c.sentinel) {
			return c.code, true
		}
	}
	return "", false
}

// WriteRefusalLuaCode returns the word a Lua function's second return value
// carries for this refusal, or "store_failed" for anything unmapped — the
// catch-all the host has always answered, so an unknown store failure stays
// one word and never leaks a Go error string into a mock's script.
func WriteRefusalLuaCode(err error) string {
	for _, c := range entityWriteCodes {
		if errors.Is(err, c.sentinel) {
			return c.lua
		}
	}
	return "store_failed"
}

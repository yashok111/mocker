// Package overrides owns the op_overrides table: one row per operation an
// operator has touched (turned off, pinned a status, bound a recipe to a
// data path). It is a LEAF over storage: it imports internal/store,
// internal/recipes, internal/domain, internal/assets, internal/jsonpatch,
// internal/luafn (A18: the shared validator compiles a variant's Lua at
// write time) and the stdlib only — never internal/gen, internal/openapi or
// internal/specs, and NEVER internal/workspaces (HARD RULE 5:
// workspaces.Repo.Update opens its own write transaction on the
// one-connection writer pool, and calling it from inside another db.Write
// callback deadlocks the whole process).
//
// A Row is user input twice over: once when an admin handler decodes a
// request body into one, and again every time the mock plane reads it back
// off an unauthenticated request path. Both directions go through the same
// validation in this file, so a malformed row can never reach a caller as
// anything worse than an error.
package overrides

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/jsonpatch"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/luafn"

	"github.com/yashok111/mocker/internal/recipes"
)

// Row is one op_overrides row, already decoded. Path is RELATIVE — without
// the base path — exactly as operations.path stores it, because an edit has
// to survive both a re-import and a basePath change (the column's own
// comment).
type Row struct {
	ID          int64
	WorkspaceID int64
	Method      string // upper case
	Path        string // relative, leading slash, no base path
	OperationID *int64 // cache of the operations row, may be nil or stale

	OverrideOn   bool // false: the row exists but is switched off wholesale
	RouteOff     bool // true: the route answers 404 (DESIGN §8)
	ActiveStatus *int // nil: keep the document's own choice

	Responses map[string]Variant // key is the status as a decimal string: "200", "409"
	ListSize  *ListSize
	DelayMs   *int

	// FailDirective is PRESERVED, never interpreted in this slice (slice 2
	// gives it meaning). It is copied byte-for-byte between the column and
	// this field with no re-encoding through encoding/json, so nothing this
	// slice does can reformat or lose it.
	FailDirective jsonx.RawMessage
	ValidateReq   *bool

	UpdatedAt time.Time

	// EditVersion is the per-row compare-and-swap token (A3, D4): the value
	// a caller must echo back on PutExpecting/PutManyExpecting to prove it
	// last read this exact row. Allocated fresh from the owning workspace's
	// edit_seq on every write through the guarded route (store.
	// AllocateEditVersion), never incremented in place -- see repo.go's
	// upsertTx and ReplaceAllTx for why an in-place counter is unsafe here.
	EditVersion int64
}

// Variant is responses[status]. Fields this slice does not implement are
// decoded, kept and written back unchanged — a round trip that dropped them
// would silently delete an operator's work the moment slice 2 ships.
// SchemaPatch is no longer one of them: P2e applies it to the resolved
// schema root at runtime (internal/mockplane's buildRuntime) and, on the
// one door a caller can change it through, validates it when it actually
// changes (internal/admin's ingress gate) — this package still only stores
// and preserves the bytes it is handed, exactly as before.
//
// The JSON keys are camelCase (bodyEncoding, schemaPatch, mediaType) while
// DESIGN §13's column comment and the migration write them snake_case. That
// is DELIBERATE and not a transcription slip: this blob is the admin API's
// wire shape, and domain.Settings already ships camelCase over that same
// wire (jwtTtlSec, signingKey, notFoundBody). One convention per wire beats
// matching a comment in a DDL nobody serializes.
type Variant struct {
	Mode         string                    `json:"mode"`                   // "generated" (default) | "pinned"
	When         []Condition               `json:"when,omitempty"`         // evaluated by Condition.Match (when.go); ValidateConditions gates what Put/PutMany accept
	Body         jsonx.RawMessage          `json:"body,omitempty"`         // pinned: the literal body
	BodyEncoding string                    `json:"bodyEncoding,omitempty"` // "" | "base64"
	BodyRef      string                    `json:"bodyRef,omitempty"`      // pinned: "asset:<name>" — the body IS an uploaded asset of this workspace (A6, DESIGN §32.3); exclusive with Body, BodyEncoding and MediaType
	MediaType    string                    `json:"mediaType,omitempty"`
	Headers      map[string]string         `json:"headers,omitempty"`
	SchemaPatch  jsonx.RawMessage          `json:"schemaPatch,omitempty"` // applied at runtime by internal/mockplane; this package only stores and preserves it, ingress-validated on change by internal/admin
	Recipes      map[string]recipes.Recipe `json:"recipes,omitempty"`     // data path pattern -> recipe
	// Schema is P7a's (DESIGN §34.3): an inline JSON Schema a CUSTOM
	// endpoint's generated response is walked from — the row has no spec
	// document to resolve a SchemaPtr into, so the schema travels on the
	// variant itself and reaches gen.Body as the inline root
	// (gen.Request.PatchedSchema, the same door a patched spec schema
	// already takes). It lives on this shared type rather than on a
	// customep-only wrapper so the contract, the bundle and the MCP inputs
	// keep ONE response shape (decisions.md mocker-p7-api-design D2); on an
	// op_overrides row it is REFUSED BY NAME by the admin plane (a spec
	// operation already has a schema — change it with schemaPatch), and
	// ValidateVariant below checks only what is knowable without a
	// document: an object, under the schemaPatch size cap. Whether every
	// `$ref` in it resolves is customep.ValidateSchemaDoc's job, run by the
	// admin plane against the bound spec at write time.
	Schema jsonx.RawMessage `json:"schema,omitempty"`
	// Function is A18's (docs/A18-endpoint-functions.md D5): the Lua source
	// that PRODUCES this variant's response instead of it being assembled.
	// It lives on the shared type for the reason Schema does — one response
	// shape across the contract, the bundle and the MCP inputs — but unlike
	// Schema it is legal on BOTH writers: a spec operation's override and a
	// custom endpoint alike, because logic is what the Workspace layer is
	// for and a spec operation has none of its own to conflict with.
	//
	// Exclusivity is PER VARIANT and not per row (D5, correcting the gate's
	// own first draft): a variant carrying a function refuses body, bodyRef,
	// recipes and schemaPatch, while the OTHER statuses of the same row are
	// untouched — a function-200 beside a pinned-401 is the sign-in shape
	// this feature exists for. `when[]` is allowed: selection is unchanged
	// and the function runs only when its variant is selected.
	Function string `json:"function,omitempty"`
}

// Condition is one entry of Variant.When: one simple predicate over the
// request's query, a header or a top-level body field. DESIGN §12's
// "Условия срабатывания (when)" — the first response variant whose when[]
// matches wins over active_status. [Condition.Match] (when.go) is what
// finally gives it meaning; [ValidateConditions] is the write-time gate
// that keeps a shape this build cannot evaluate out of a fresh row.
type Condition struct {
	In    string `json:"in"` // "query" | "header" | "body"
	Name  string `json:"name"`
	Op    string `json:"op"` // "equals" | "contains" | "exists"
	Value string `json:"value,omitempty"`
}

// ListSize is the list_size column: a fixed n, or a [lo,hi] range.
type ListSize struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// ErrNotFound is returned when a lookup finds no matching override row.
var ErrNotFound = errors.New("override not found")

// ErrWorkspaceNotFound is returned by Put/PutMany/Delete when the target
// workspace does not exist. It is distinct from ErrNotFound (which means
// "no override for this operation" — a normal, expected state) so a caller
// can tell "nothing to show" from "wrote against a workspace that is gone".
var ErrWorkspaceNotFound = errors.New("overrides: workspace not found")

// ErrInvalidRow wraps every reason a Row or one of its nested Variants is
// rejected as structurally broken: an unknown Variant.Mode or
// Variant.BodyEncoding, a responses key that is not a 3-digit status code, a
// base64 body that does not decode, a recipe that fails
// [recipes.Recipe.Validate], a method/path that cannot identify an
// operation, or an inverted/negative ListSize or DelayMs.
var ErrInvalidRow = errors.New("overrides: invalid row")

// A18's two named refusals of a variant's function, wrapped TOGETHER with
// ErrInvalidRow so every existing errors.Is(ErrInvalidRow) site keeps its
// 400 and the admin plane can also put the NAME in the envelope's code —
// which the gate document, the embedded guide and api/openapi.json all
// promised (`400 bad_function`, `400 function_and_body`) and the server did
// not deliver until the A18 review: it answered `bad_request` with prose,
// so an agent branching on the code never matched. The sentinel's text IS
// the code; internal/admin's refusalCode reads it through errors.Is and
// never by string. customep reuses ErrBadFunction for the two stream hooks,
// since a compile error is one refusal wherever the Lua lives.
var (
	ErrBadFunction     = errors.New("bad_function")
	ErrFunctionAndBody = errors.New("function_and_body")
)

// normalizeAndValidate upper-cases Method, requires a leading-slash Path,
// defaults a nil Responses to an empty map (so it marshals to "{}" — the
// column's own DEFAULT — rather than the JSON literal "null"), and then
// validates everything a Row carries. It is the single gate both Put and
// PutMany write through, so "reject a malformed row" is enforced in exactly
// one place regardless of which entry point the caller used.
func normalizeAndValidate(row *Row) error {
	row.Method = upperASCII(row.Method)
	if row.Method == "" {
		return fmt.Errorf("%w: method is empty", ErrInvalidRow)
	}
	if row.Path == "" || row.Path[0] != '/' {
		return fmt.Errorf("%w: path %q must start with \"/\"", ErrInvalidRow, row.Path)
	}
	if row.Responses == nil {
		row.Responses = map[string]Variant{}
	}
	if err := validateResponses(row.Responses); err != nil {
		return err
	}
	if row.ListSize != nil {
		if row.ListSize.Min < 0 || row.ListSize.Max < row.ListSize.Min {
			return fmt.Errorf("%w: listSize has an inverted or negative range [%d,%d]",
				ErrInvalidRow, row.ListSize.Min, row.ListSize.Max)
		}
	}
	if row.DelayMs != nil && *row.DelayMs < 0 {
		return fmt.Errorf("%w: delayMs must not be negative, got %d", ErrInvalidRow, *row.DelayMs)
	}
	if row.ActiveStatus != nil && !ValidHTTPStatus(*row.ActiveStatus) {
		return fmt.Errorf("%w: activeStatus %d is outside 100..599", ErrInvalidRow, *row.ActiveStatus)
	}
	return nil
}

// ValidHTTPStatus reports whether status is one net/http will write.
// http.ResponseWriter.WriteHeader PANICS on anything outside 100..599
// (checkWriteHeaderCode), and the mock plane writes a stored status
// verbatim on every request to that operation — so a value that slips
// through here is not one bad response but a 500 with a stack trace per
// request until the row is edited. Both the activeStatus a row pins and
// the keys of its responses map run through this, and internal/customep
// calls the same function for its own ActiveStatus rather than keeping a
// second copy of the range.
func ValidHTTPStatus(status int) bool {
	return status >= 100 && status <= 599
}

// ValidateResponses checks every entry of a responses map: the key must be
// the 3-digit decimal status DESIGN §13 documents, and the variant itself
// must pass ValidateVariant. internal/customep stores the same Variant
// JSON shape in its own table and calls this SAME function rather than
// re-implementing it — see ValidateVariant's own comment for why op_overrides'
// read path (repo.go's scan(), reached on every unauthenticated request)
// runs through here too, not just the write path.
func ValidateResponses(responses map[string]Variant) error {
	for status, v := range responses {
		if !isThreeDigitStatus(status) || !ValidHTTPStatus(int(status[0]-'0')*100) {
			return fmt.Errorf("%w: response key %q is not a status code in 100..599", ErrInvalidRow, status)
		}
		if err := ValidateVariant(v); err != nil {
			// ValidateVariant's error already wraps ErrInvalidRow; wrapping it
			// again here would only bury the specific reason behind a second
			// copy of the same sentinel.
			return fmt.Errorf("response %s: %w", status, err)
		}
	}
	return nil
}

// validateResponses is ValidateResponses under its original unexported
// name. repo.go:460 (scan(), not owned by this file) calls it directly, so
// the name stays as a one-line caller into the exported implementation —
// there is exactly one implementation, not two.
func validateResponses(responses map[string]Variant) error {
	return ValidateResponses(responses)
}

// maxPinnedBodyBytes mirrors gen.DefaultMaxBytes: the same ceiling a
// generated body is held to by default. Unlike a generated body — bounded
// on every single call by gen.Options.MaxBytes — a pinned body is written
// ONCE and then served verbatim on every subsequent unauthenticated
// request, with nothing downstream re-measuring it (round-1 findings
// #3/#8: the only ceiling on the write was MOCKER_MAX_BODY, 10mb by
// default, over 2x this). This package cannot reference gen.DefaultMaxBytes
// directly (it is a leaf over storage — see the package doc comment — and
// must not import internal/gen), so the value is mirrored, not imported —
// the same way respond.go's own JSON-shape check calls the one definition
// of that rule, httpx.IsJSONMediaType, rather than reimplementing it.
//
// This is the first of two gates: the second is mockplane/respond.go's own
// re-check against the LIVE cfg.MaxResponse at serve time, which can differ
// from this fixed default if an operator has configured
// MOCKER_MAX_RESPONSE away from it.
const maxPinnedBodyBytes = 4 << 20

// maxRecipesPerVariant bounds how many recipe bindings ONE response variant
// may carry. [recipes.Set.Lookup] scans every bound pattern that could
// possibly match a given data path — cheap for the handful of bindings an
// operator writes by hand, but with nothing bounding the COUNT, a single
// stored row can force that scan to repeat per leaf/array-node of every
// generated body (round-1 finding #7): measured, 200,000 non-matching
// bindings turned an 83,649-byte response's 1.87ms generation into 3.034s,
// on an unauthenticated GET DESIGN §18 forbids rate-limiting. 1000 is
// generous for any legitimate single-operation binding set (recipes bind
// named fields an operator actually cares about, not enumerated
// thousands-deep) while sitting two orders of magnitude below every
// attack size actually measured.
const maxRecipesPerVariant = 1000

// validateFunctionVariant is A18 D5's exclusivity, extracted so
// ValidateVariant stays under the complexity bar rather than carrying a
// thirteenth gocyclo suppression. It has one subject — a variant that produces
// its response by running Lua carries no second producer — and the caller
// reads as one line.
func validateFunctionVariant(v Variant) error {
	// A18 D5. One producer per variant, so there is no precedence to
	// document and no order for a later reader to get wrong: a variant
	// either has a function or has a body, and every other shape is
	// refused by name. mediaType is NOT in this list — a function
	// chooses its own type by returning a table or a string, and a
	// variant that also declares one is refused for the same reason a
	// bodyRef's is, one line down: the two could disagree and nothing
	// at write time can say which wins.
	switch {
	case len(v.Body) > 0 || v.BodyEncoding != "":
		return fmt.Errorf("%w: %w: function and body are exclusive: one producer per variant", ErrInvalidRow, ErrFunctionAndBody)
	case v.BodyRef != "":
		return fmt.Errorf("%w: %w: function and bodyRef are exclusive: one producer per variant", ErrInvalidRow, ErrFunctionAndBody)
	case len(v.Recipes) > 0:
		return fmt.Errorf("%w: %w: function and recipes are exclusive: a function builds its own body", ErrInvalidRow, ErrFunctionAndBody)
	case len(v.SchemaPatch) > 0:
		return fmt.Errorf("%w: %w: function and schemaPatch are exclusive: a function's body is not walked from a schema", ErrInvalidRow, ErrFunctionAndBody)
	case v.MediaType != "":
		return fmt.Errorf("%w: %w: function takes no mediaType: a table return is JSON and a string return is the function's own bytes", ErrInvalidRow, ErrFunctionAndBody)
	}
	// Compiled at WRITE time so a syntax error is a 400 carrying the
	// parser's own words, never a 500 on the first anonymous request
	// (D8). This is the shared validator, so both writers and every
	// other door into this package — the bundle, a checkpoint restore,
	// an import — get the check without repeating it.
	if err := luafn.Validate(v.Function); err != nil {
		return fmt.Errorf("%w: %w: function does not compile: %w", ErrInvalidRow, ErrBadFunction, err)
	}
	return nil
}

// ValidateVariant rejects an unknown Mode or BodyEncoding, proves a base64
// body actually decodes, bounds a pinned body's size and a variant's recipe
// count, runs every bound recipe through [recipes.Recipe.Validate], and
// rejects a When condition this build cannot evaluate (see
// ValidateConditions for exactly which shapes that is). SchemaPatch and the
// row's FailDirective are PRESERVED ONLY (their comments say so) and are
// deliberately not interpreted or shape-checked here — this slice does not
// know their eventual schema, and rejecting valid future input on a guess
// would be worse than accepting it unread.
//
// This is the SAME check op_overrides' read path runs on every
// unauthenticated request too (repo.go's scan(), via ValidateResponses) —
// a row already sitting in the table, written by an older build or by
// hand, fails HERE with a returned error rather than a panic, exactly like
// an unknown Mode or an invalid recipe already did before this slice (see
// TestRepo_scan_malformedResponsesIsAnErrorNotAPanic). [Condition.Match]
// itself stays total over any condition shape regardless — an
// unrecognised In or Op never matches, never errors, never panics — it is
// only the STORED shape of a FRESH write that this function refuses.
// function sat exactly ON the bar before A18: the one call it gained took it
// to 21. The function-exclusivity block is already extracted
// (validateFunctionVariant above); extracting the bodyRef block too would be a
// pure move through code this slice does not otherwise touch, which is a wider
// diff than the subject warrants.
//
//nolint:gocyclo // a branch per refusal IS the specification here, and this
func ValidateVariant(v Variant) error {
	switch v.Mode {
	case "", "generated", "pinned":
		// ok
	default:
		return fmt.Errorf("%w: unknown mode %q", ErrInvalidRow, v.Mode)
	}
	switch v.BodyEncoding {
	case "", "base64":
		// ok
	default:
		return fmt.Errorf("%w: unknown bodyEncoding %q", ErrInvalidRow, v.BodyEncoding)
	}
	if v.Function != "" {
		if err := validateFunctionVariant(v); err != nil {
			return err
		}
	}
	if v.BodyRef != "" {
		// A6 (D5): a reference is a pinned body of a different origin, and
		// it carries NO other body field. body/bodyEncoding would be a
		// second body; mediaType is refused rather than "must agree" as
		// §32.3 first said (a declared narrowing, CARVE-OUTS.md): agreement
		// is unknowable at write time — the asset may be uploaded later or
		// replaced under the same name with another type — so the asset's
		// stored type is the only type such a variant has. Existence is
		// deliberately NOT checked here (this package has no asset store,
		// and a name uploaded after the write would fail a check that was
		// right when it ran); a missing asset is a serve-time outcome,
		// asset_missing in the traffic.
		if v.Mode != "pinned" {
			return fmt.Errorf("%w: bodyRef requires mode \"pinned\", got %q", ErrInvalidRow, v.Mode)
		}
		if len(v.Body) > 0 || v.BodyEncoding != "" {
			return fmt.Errorf("%w: bodyRef and body/bodyEncoding are exclusive", ErrInvalidRow)
		}
		if v.MediaType != "" {
			return fmt.Errorf("%w: bodyRef takes no mediaType: the asset's stored type is served", ErrInvalidRow)
		}
		if _, ok := assets.NameFromBodyRef(v.BodyRef); !ok {
			return fmt.Errorf("%w: bodyRef %q must be %q followed by an asset name ([A-Za-z0-9._-]{1,128})",
				ErrInvalidRow, v.BodyRef, assets.BodyRefPrefix)
		}
	}
	bodyBytes := len(v.Body)
	if v.BodyEncoding == "base64" {
		var encoded string
		if err := jsonx.Unmarshal(v.Body, &encoded); err != nil {
			return fmt.Errorf("%w: base64 body must be a JSON string: %w", ErrInvalidRow, err)
		}
		// Decode-and-discard is the proof: a body that will not decode now
		// would fail identically (but much later, on an unauthenticated
		// request) when the mock plane tries to serve it. The DECODED
		// length, not the base64 text's own (longer) length, is what the
		// mock plane actually serves — see pinnedBody (mockplane/respond.go)
		// — so that is what maxPinnedBodyBytes below measures.
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("%w: base64 body does not decode: %w", ErrInvalidRow, err)
		}
		bodyBytes = len(decoded)
	}
	if v.Mode == "pinned" && bodyBytes > maxPinnedBodyBytes {
		return fmt.Errorf("%w: pinned body is %d bytes, over the %d-byte limit", ErrInvalidRow, bodyBytes, maxPinnedBodyBytes)
	}
	if len(v.Recipes) > maxRecipesPerVariant {
		return fmt.Errorf("%w: %d recipes bound, over the %d-recipe limit per response", ErrInvalidRow, len(v.Recipes), maxRecipesPerVariant)
	}
	for pattern, r := range v.Recipes {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("%w: recipe %q: %w", ErrInvalidRow, pattern, err)
		}
	}
	if err := ValidateConditions(v.When); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRow, err)
	}
	if err := ValidateSchemaShape(v.Schema); err != nil {
		return fmt.Errorf("%w: schema: %w", ErrInvalidRow, err)
	}
	return nil
}

// ValidateSchemaShape is the document-free half of a response schema's
// validation (P7a D2): a JSON object, at most jsonpatch.MaxPatchBytes on
// the wire — DESIGN §34.3's "at most the size a schemaPatch may be", read
// off the one constant rather than copied. An empty field is "no schema"
// and passes. The `$ref` half needs the bound document and lives in
// internal/customep (ValidateSchemaDoc), which this package must not
// import — the dependency runs the other way.
func ValidateSchemaShape(raw jsonx.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > jsonpatch.MaxPatchBytes {
		return fmt.Errorf("%d bytes, over the %d-byte limit a schema may be", len(raw), jsonpatch.MaxPatchBytes)
	}
	var obj map[string]any
	if err := jsonx.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("must be a JSON object: %w", err)
	}
	if obj == nil {
		return errors.New("must be a JSON object, got null")
	}
	return nil
}

func isThreeDigitStatus(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := range 3 {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// upperASCII upper-cases only what an HTTP method ever contains, instead of
// pulling in strings.ToUpper's full-Unicode-casing machinery for a value
// that must already be one of GET/POST/PUT/PATCH/DELETE/... to mean
// anything.
func upperASCII(s string) string {
	b := []byte(s)
	changed := false
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}

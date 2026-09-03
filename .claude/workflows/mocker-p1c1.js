export const meta = {
  name: 'mocker-p1c1',
  description: 'P1c slice 1: the recipes engine, op_overrides and the auth preset — the point where a frontend can log in',
  phases: [
    { title: 'Kernel', detail: 'internal/recipes: the recipe model, path matching, the value kinds, HS256 token minting', model: 'sonnet' },
    { title: 'Engine', detail: 'internal/gen: recipeValue, Request.Recipes, the copy/omit post-pass, the listSize recipe', model: 'sonnet' },
    { title: 'Gate 1', detail: 'two reviewers on recipes+gen, one fix agent if either raises a blocker', model: 'sonnet' },
    { title: 'Store', detail: 'internal/overrides (op_overrides repo) and internal/authpreset (derivation), in parallel', model: 'sonnet' },
    { title: 'Gate 2', detail: 'two reviewers on the two new store packages', model: 'sonnet' },
    { title: 'Serve', detail: 'mockplane runtime, then respond, then the admin handlers — serial, one package at a time', model: 'sonnet' },
    { title: 'Gate 3', detail: 'two reviewers on the serving path and the admin surface', model: 'sonnet' },
    { title: 'Accept', detail: 'acceptance on the real document, then the docker stack', model: 'sonnet' },
    { title: 'Review', detail: 'correctness (sonnet) and security (opus) across the whole slice', model: 'opus' },
    { title: 'Fix', detail: 'apply the findings, at most two rounds', model: 'sonnet' },
    { title: 'Verify', detail: 'check the fix semantically, not just that it compiles', model: 'opus' },
  ],
}

// ---------------------------------------------------------------------------
// Run-level decisions, logged so the transcript records them where they were
// made rather than leaving them to be re-derived from the diff.
// ---------------------------------------------------------------------------

const SPEC_PATH = (typeof args !== 'undefined' && args && args.specPath) || 'output.swagger.json'

// cmd, go.mod and go.sum are in the list deliberately, not for completeness:
// serve:admin wires the overrides repo into the real binary, and P1a's
// post-mortem records a nil in main.go that would have shipped as a dead
// feature because no reviewer's diff included it. go.mod/go.sum are the
// tripwire for HARD RULE 2, which the security reviewer is told to check.
const P1C_PATHS =
  'internal/recipes internal/overrides internal/authpreset internal/gen internal/mockplane internal/admin internal/server internal/specs internal/router internal/domain internal/store internal/config internal/openapi internal/workspaces internal/httpx internal/testspec cmd go.mod go.sum scripts Makefile README.md'

log(`Acceptance spec: ${SPEC_PATH} (gitignored, 347 KB; every test that reads it skips itself when it is absent, and NO agent may read or cat it — jq only).`)

log('SCOPE — this run is SLICE 1 of P1c and nothing else: the recipes engine, the op_overrides row (storage + application at request time) and the auth preset. The phase criterion is auth end to end: a frontend hitting the mocked login endpoint gets a token it can decode and an expiry it can schedule against.')

log('DEFERRED TO SLICE 2, deliberately, so nobody reads a gap as a defect: the when[] conditions (§12 — this slice PARSES and PRESERVES them, and never evaluates them), the session layer with forced status / SESSION-level delay / pause / fail_next and POST /__mocker/state, custom_endpoints, the traffic buffer and its poll endpoint, and with them §19 P1\'s "создать правку из ответа" — building an override out of a recorded response has no input until traffic exists. schema_patch is stored and round-tripped here but APPLIED in P2, where §19 puts it. The faker/template/sequence recipes are P2 and ref is P3. The React UI is P1d.')

log('NOT DEFERRED, and worth separating from the sentence above because the names collide: the op_overrides delay_ms COLUMN is applied in this slice (it is part of the row), while the SESSION-level delay directive is slice 2. Same for status: active_status on the row is this slice, the session force-status is slice 2.')

log('TWO ITEMS CROSS A PHASE BOUNDARY ON PURPOSE, logged so a reviewer holding §19 does not read them as scope creep. §19 puts "не-JSON и base64" and "задержки" in P2; this slice implements a base64-encoded pinned body and the per-operation delay because both are single fields of a row it already decodes, and leaving them stored-but-ignored would be a lie in the data. Likewise §19\'s P1 recipe list is const/jwt/now/identity/copy; the kernel also implements enum, null, omit and listSize — four cheap kinds from the same §9 table, and listSize is what makes the everyday "пустой список / сто элементов" scenario possible at all.')

log('TWO §10 RULES ARE NEITHER IMPLEMENTED NOR NEEDED HERE, and are named so the gate does not report them as gaps: domain.AuthSettings.RequireHeader (declared, defaulted false, referenced nowhere — the 401-without-a-header switch) and the automatic "SameSite=None; Secure" on cookies the mock emits (§10 lines 459-461). Neither is on the path to "фронт логинится": the first is an opt-in refusal and the second needs the mock to set a cookie at all, which no recipe in this slice does. Both go with the slice that gives the mock a cookie to set.')

log('DECISION — no JWT library. HARD RULE 2 (no new runtime dependency) holds, and a compact JWS is three base64url segments and one HMAC-SHA256: crypto/hmac + crypto/sha256 + encoding/base64 + encoding/json. DESIGN §10 also wants alg:none to be selectable, which most libraries deliberately refuse to mint. The mock NEVER verifies an incoming token, so this is a formatter, not a security boundary.')

log('DECISION — recipes travel on gen.Request, not on gen.Options. A Generator is built once per (workspace, revision) and shared by every concurrent request against it; recipes vary per (operation, status). Putting them in Options would mean one Generator per operation, or a mutable Generator — the second is the data race the P1b runtime was designed to make impossible.')

log('DECISION — internal/gen stops being a pure leaf: it imports internal/recipes, which imports internal/domain. P1b noted gen as "openapi + stdlib only, settings passed as primitives". The identity dictionary is a domain type (domain.Identity) and re-declaring it inside gen would give the same concept two definitions that drift. The dependency runs one way only — domain imports nothing of ours — so there is no cycle.')

log('DECISION — copy and omit are a POST-PASS over the finished body, not inline hooks in walkObject. copy ($.id) needs a sibling that may not have been generated yet when the copying property is visited, and property visit order is not a contract; a post-pass sees the whole object. omit rides along for free. Only const/enum/identity/jwt/now/null are inline in recipeValue.')

log('DECISION — a recipe whose value cannot be losslessly coerced to the declared type DECLINES rather than writes. A jwt recipe on an integer field, or identity.roles (an array) on a string field, would otherwise turn a 114/114 schema-conformance result into a silent regression. Coercion that IS allowed: a number or a bool into a string field, a numeric string into a number or integer field. Coercion that is NOT: an array or an object into a scalar field, a non-numeric string into a number. This rule is what keeps the acceptance test honest.')

log('DECISION — recipe paths are matched against the ABSOLUTE data path, which the walker must reconstruct: inside a generated list item, gen restarts dataPath at "" so each item seeds independently (list.go generateItems). The walker therefore gains a recipePrefix field used ONLY for recipe lookup and NEVER for seeding — touching the seed inputs would reshuffle every list in every workspace and break the P1b list contract.')

log('DECISION — an override write bumps workspaces.revision INSIDE ITS OWN TRANSACTION. See HARD RULE 5: the writer pool is one connection, so calling workspaces.Repo.Update (which opens its own db.Write) from inside an overrides transaction deadlocks the process, and doing it as a second, separate transaction leaves a window where the row is written but the runtime cache still serves the old build.')

log('Agent count: 16 call sites — 19 agents on the typical path, 30 worst case (three gate fixes, six gate re-reviews, a second fix/verify round). Above the medium guideline of 15, deliberately and for the same reason as P1a and P1b: the phase spans six packages whose seams are the actual risk, and collapsing them into fewer agents means one agent holding two packages worth of context and reviewing its own seam.')

// ---------------------------------------------------------------------------
// Signature blocks. Pasted VERBATIM into the agent that writes each package AND
// into every agent that calls it. P0's post-mortem: a contract left as an
// optional field of a report schema is a contract two agents will guess
// differently, and two adjacent string parameters swapped compiles silently.
// ---------------------------------------------------------------------------

const SIG_RECIPES = `  // package recipes  (internal/recipes) — A LEAF: imports internal/domain and the
  // stdlib ONLY. Never internal/gen, internal/openapi, internal/store or
  // internal/specs: three packages above it import this one.

  type Kind string

  const (
      KindConst    Kind = "const"     // always this value
      KindEnum     Kind = "enum"      // one of a list, chosen from the seed
      KindCopy     Kind = "copy"      // the value of a sibling field ("$.id")
      KindIdentity Kind = "identity"  // a field of domain.Identity (DESIGN §10)
      KindJWT      Kind = "jwt"       // a structurally valid compact JWS
      KindNow      Kind = "now"       // real clock, with an offset
      KindNull     Kind = "null"      // force null
      KindOmit     Kind = "omit"      // drop the property entirely
      KindListSize Kind = "listSize"  // the length of the array at this path
  )

  // Recipe is ONE rule. Only the fields its Kind uses are populated; the rest
  // stay zero, which is what keeps the stored JSON small and diffable.
  type Recipe struct {
      Kind   Kind            \`json:"kind"\`
      Value  json.RawMessage \`json:"value,omitempty"\`   // const: the literal. enum: a JSON array. listSize: n or [lo,hi]
      Field  string          \`json:"field,omitempty"\`   // identity: "id"|"name"|"email"|"roles"|"org.id"|"org.name"|"org.type". copy: "$.id"
      Offset string          \`json:"offset,omitempty"\`  // now: "+3600s", "-7d", "" for exactly now
      Format string          \`json:"format,omitempty"\`  // now: "epoch"|"epoch_ms"|"iso" (default epoch when the target is numeric, iso when it is a string)
      Claims json.RawMessage \`json:"claims,omitempty"\`  // jwt: a claim template merged OVER the identity-derived claims
      TTLSec int             \`json:"ttlSec,omitempty"\`  // jwt: overrides domain.AuthSettings.JWTTTLSec
  }

  // Validate rejects a recipe whose Kind is unknown or whose required field for
  // that Kind is missing or malformed. Called on every decode path: an override
  // row is user input.
  func (r Recipe) Validate() error

  // Deferred reports the kinds Value cannot evaluate alone — copy needs a
  // sibling, omit needs the parent object, listSize needs the array. The caller
  // (internal/gen) handles those three; everything else goes through Value.
  func (k Kind) Deferred() bool

  // Set is the compiled bindings for ONE response variant: pattern -> recipe.
  type Set struct{ /* unexported */ }

  // Compile validates every recipe and precompiles every pattern. A nil or
  // empty map compiles to a nil *Set, and a nil *Set is a legal receiver for
  // every method below — callers must not need a nil check at each call site.
  func Compile(bindings map[string]Recipe) (*Set, error)

  func (s *Set) Len() int
  func (s *Set) Bindings() map[string]Recipe

  // Lookup returns the recipe bound to dataPath. Patterns address data paths as
  // DESIGN §9 writes them: "items[*].status", "user.profile.avatar_url",
  // "roles[*]". "[*]" matches any index; "[3]" matches exactly index 3. The MOST
  // SPECIFIC pattern wins (an exact-index pattern over a wildcard one, a longer
  // literal prefix over a shorter one); ties break on the lexicographically
  // smaller pattern so the choice is deterministic rather than map-order.
  func (s *Set) Lookup(dataPath string) (Recipe, bool)

  // ListSizeAt returns the length bounds a listSize recipe pins at dataPath.
  // lo == hi for a fixed size. ok is false when no listSize recipe is bound.
  func (s *Set) ListSizeAt(dataPath string) (lo, hi int, ok bool)

  // Env is everything a recipe needs that is not in the recipe itself.
  type Env struct {
      Identity domain.Identity     // DESIGN §10's dictionary
      Auth     domain.AuthSettings // signing key, alg, ttl
      Now      time.Time           // the REAL clock, frozen once per response
      Seed     uint64              // gen.SeedScalar for this data path — enum picks from it
      Type     string              // the declared JSON type of the target node: "string"|"integer"|"number"|"boolean"|"array"|"object"|"" when unknown
      Format   string              // the declared format, "" when none
  }

  // Value evaluates r. ok=false means "decline — fall through to the next value
  // source", which is what a recipe returns when its value cannot be coerced to
  // Env.Type without lying about the schema, and also what it returns for every
  // Deferred kind. err is reserved for a recipe that is structurally broken.
  //
  // Value NEVER returns ErrOmit: omit is a Deferred kind, so Value declines and
  // internal/gen's post-pass deletes the property. The sentinel lives here only
  // so that the post-pass and this package name the same condition.
  func (r Recipe) Value(env Env, dataPath string) (any, bool, error)

  var (
      ErrOmit   = errors.New("recipes: omit this property")
      ErrRecipe = errors.New("recipes: invalid recipe")
  )

  // MintJWT builds a compact JWS: base64url(header).base64url(payload).base64url(sig),
  // no padding, HS256 over auth.SigningKey — or the literal empty third segment
  // when auth.Alg is "none". Claims from the identity (sub, name, email, roles,
  // org) are computed first, then extra is merged OVER them, then iat and exp
  // are stamped from now and ttlSec. EXPORTED because the auth preset's preview
  // shows a sample token before anything is written.
  //
  // exp and iat are epoch SECONDS. DESIGN §10 is explicit about the unit and
  // about why: a client scheduling setTimeout((exp-now)*1000) on a millisecond
  // value overflows the 32-bit timer and refreshes in a tight loop.
  func MintJWT(auth domain.AuthSettings, id domain.Identity, extra map[string]any, ttlSec int, now time.Time) (string, error)

  // IdentityField projects one dotted field of the identity ("id", "org.id",
  // "roles"). ok is false for a field the identity does not carry.
  func IdentityField(id domain.Identity, field string) (any, bool)

  // NowValue evaluates a "now" recipe: offset parsed as a signed Go-ish
  // duration with day support ("+3600s", "-7d", "+2h"), rendered per format.
  func NowValue(offset, format string, now time.Time) (any, error)

  // Coerce adapts v to the declared type. ok=false means the value would have to
  // lie about the schema to fit — the caller then declines the recipe.
  func Coerce(v any, jsonType, format string) (any, bool)

  // MatchPattern reports whether a compiled-style pattern matches an absolute
  // data path. Exported for the auth preset, which shows the operator which
  // paths a proposed binding would hit.
  func MatchPattern(pattern, dataPath string) bool`

const SIG_GEN = `  // package gen  (internal/gen) — WRITTEN AND GREEN in P1b. This phase adds
  // exactly the following and changes nothing else about its public surface:

  type Options struct {
      Seed     int64
      ListSize int
      NullRate float64
      MaxBytes int64
      MaxDepth int
      Now      func() time.Time

      // NEW in P1c. Per workspace, stable for the life of a Generator.
      Identity domain.Identity
      Auth     domain.AuthSettings
  }

  type Request struct {
      Method        string
      CanonicalPath string
      PathParams    map[string]string
      Query         url.Values
      Status        int
      ListFamily    string
      IDParam       string

      // NEW in P1c. nil means "no recipes bound to this variant" and MUST
      // reproduce P1b's output byte for byte — that invariant is an acceptance
      // assertion, not a hope.
      Recipes *recipes.Set
  }

  // UNCHANGED, and every existing caller keeps compiling:
  func New(res *openapi.Resolver, opts Options) *Generator
  func (g *Generator) Body(v ResponseVariant, req Request) ([]byte, error)
  func (g *Generator) Headers(v ResponseVariant, req Request) map[string]string
  func SeedList(opts Options, req Request) uint64
  func SeedScalar(seed uint64, dataPath string) uint64
  func SeedItemByID(seedList uint64, id string) uint64
  func IDForIndex(seedList uint64, i int, idSchema any) (any, string)`

const SIG_OVERRIDES = `  // package overrides  (internal/overrides) — imports internal/store,
  // internal/recipes, internal/domain and the stdlib. NOT internal/workspaces
  // (HARD RULE 5: its Update opens its own write transaction), NOT internal/gen.

  // Row is one op_overrides row, already decoded. Path is RELATIVE — without the
  // base path — exactly as operations.path stores it, because an edit has to
  // survive both a re-import and a basePath change (the column's own comment).
  type Row struct {
      ID           int64
      WorkspaceID  int64
      Method       string             // upper case
      Path         string             // relative, leading slash, no base path
      OperationID  *int64             // cache of the operations row, may be nil or stale
      OverrideOn   bool               // false: the row exists but is switched off wholesale
      RouteOff     bool               // true: the route answers 404 (DESIGN §8)
      ActiveStatus *int               // nil: keep the document's own choice
      Responses    map[string]Variant // key is the status as a decimal string: "200", "409"
      ListSize     *ListSize
      DelayMs      *int
      FailDirective json.RawMessage   // PRESERVED, never interpreted in this slice (slice 2)
      ValidateReq  *bool
      UpdatedAt    time.Time
  }

  // Variant is responses[status]. Fields this slice does not implement are
  // decoded, kept and written back unchanged — a round trip that dropped them
  // would silently delete an operator's work the moment slice 2 ships.
  //
  // The JSON keys are camelCase (bodyEncoding, schemaPatch, mediaType) while
  // DESIGN §13's column comment and the migration write them snake_case. That
  // is DELIBERATE and not a transcription slip: this blob is the admin API's
  // wire shape, and domain.Settings already ships camelCase over that same wire
  // (jwtTtlSec, signingKey, notFoundBody). One convention per wire beats
  // matching a comment in a DDL nobody serializes.
  type Variant struct {
      Mode         string                    \`json:"mode"\`                   // "generated" (default) | "pinned"
      When         []Condition               \`json:"when,omitempty"\`         // PRESERVED ONLY — slice 2 evaluates these
      Body         json.RawMessage           \`json:"body,omitempty"\`         // pinned: the literal body
      BodyEncoding string                    \`json:"bodyEncoding,omitempty"\` // "" | "base64"
      MediaType    string                    \`json:"mediaType,omitempty"\`
      Headers      map[string]string         \`json:"headers,omitempty"\`
      SchemaPatch  json.RawMessage           \`json:"schemaPatch,omitempty"\`  // PRESERVED ONLY — P2 applies it
      Recipes      map[string]recipes.Recipe \`json:"recipes,omitempty"\`      // data path pattern -> recipe
  }

  type Condition struct {
      In    string \`json:"in"\`    // "query" | "header" | "body"
      Name  string \`json:"name"\`
      Op    string \`json:"op"\`    // "equals" | "contains" | "exists"
      Value string \`json:"value,omitempty"\`
  }

  // ListSize is the list_size column: a fixed n, or a [lo,hi] range.
  type ListSize struct {
      Min int \`json:"min"\`
      Max int \`json:"max"\`
  }

  type Repo struct{ /* unexported */ }
  func NewRepo(db *store.DB) *Repo

  // ForWorkspace returns every override of one workspace in ONE query, keyed by
  // OpKey(method, path). The runtime build calls this once per (workspace,
  // revision); a query per operation on that path is the N+1 DESIGN §18 forbids.
  func (r *Repo) ForWorkspace(ctx context.Context, workspaceID int64) (map[string]*Row, error)

  func (r *Repo) Get(ctx context.Context, workspaceID int64, key string) (*Row, error)

  // Put upserts one operation's override and bumps workspaces.revision IN THE
  // SAME TRANSACTION, returning the new revision. mutate receives the existing
  // row, or a zero row with Method/Path already filled when there is none.
  //
  // NEVER "INSERT OR REPLACE": op_overrides carries a resource_id column that
  // Row deliberately does not model (it is P3's), and REPLACE deletes the row
  // and inserts a new one, nulling it on every edit. Use an explicit
  // INSERT ... ON CONFLICT (workspace_id, method, path) DO UPDATE SET <the
  // columns Row owns>.
  func (r *Repo) Put(ctx context.Context, workspaceID int64, key string, mutate func(*Row) error) (*Row, int64, error)

  // PutMany is the auth preset's path: many rows, one transaction, ONE revision
  // bump. Applying a 40-binding preset through Put would bump the revision 40
  // times and rebuild the runtime 40 times.
  func (r *Repo) PutMany(ctx context.Context, workspaceID int64, rows []*Row) (int64, error)

  // Delete removes one override and bumps the revision. Deleting a row that is
  // not there is not an error: the caller wanted no override, and there is none.
  func (r *Repo) Delete(ctx context.Context, workspaceID int64, key string) (int64, error)

  var ErrNotFound = errors.New("override not found")

  // OpKey is method + " " + path, percent-encoded for use as ONE path segment of
  // an admin URL (DESIGN §14: "метод + относительный путь в percent-encoding").
  // The key must survive a spec version change and a basePath edit, which is
  // exactly why it is built from the relative path and never from a row id.
  func OpKey(method, path string) string
  func ParseOpKey(key string) (method, path string, err error)`

const SIG_PRESET = `  // package authpreset  (internal/authpreset) — imports internal/openapi,
  // internal/recipes, internal/domain and the stdlib. NOT internal/specs and NOT
  // internal/gen: it takes its operations as a plain slice so it stays testable
  // without a database and cannot pull a cycle in behind it.

  // Operation is what the caller (the admin handler) hands in: one response
  // variant of one operation, with the pointer to its already-resolved schema.
  type Operation struct {
      Method    string // upper case
      Path      string // RELATIVE, as operations.path stores it
      Status    int
      SchemaPtr string // gen.ResponseVariant.SchemaPtr — "" when the response declares no schema
      OpPointer string // operations.pointer, for the security[] of that operation
  }

  // Binding is ONE proposed recipe, with the reason shown to the operator. The
  // preset is a PROPOSAL: DESIGN §10 requires the mapping to be visible and
  // editable BEFORE anything is written.
  // NOTE: Binding carries NO opKey. The percent-encoding that has to round-trip
  // through overrides.ParseOpKey is owned by internal/overrides, and this
  // package must not import it (it would drag internal/store in behind it). Two
  // packages inventing that encoding separately is a silent miss: the rows land
  // keyed to operations that do not exist and the runtime simply never consults
  // them. The admin handler computes overrides.OpKey(b.Method, b.Path).
  type Binding struct {
      Method   string         \`json:"method"\`
      Path     string         \`json:"path"\`
      Status   int            \`json:"status"\`
      DataPath string         \`json:"dataPath"\` // the pattern the recipe binds to
      Recipe   recipes.Recipe \`json:"recipe"\`
      Reason   string         \`json:"reason"\`   // human text: "field name \"token\" on an auth path -> jwt"
      Source   string         \`json:"source"\`   // "auth-path" | "identity-alias" | "expiry-field"
  }

  type Proposal struct {
      Bindings   []Binding \`json:"bindings"\`
      Schemes    []string  \`json:"schemes"\`    // securitySchemes found, as "name: type scheme"
      AuthPaths  []string  \`json:"authPaths"\`  // the paths that matched DESIGN §10's trigger list
      Notes      []string  \`json:"notes"\`      // what was skipped and why — a silent skip reads as "nothing to do"
      SampleJWT  string    \`json:"sampleJwt"\`  // one minted token so the operator sees what will be served
  }

  // Derive walks each operation's response schema through res and proposes
  // bindings. It NEVER writes anything: the admin handler applies an (edited)
  // list through overrides.Repo.PutMany.
  //
  // Bindings are ordered deterministically (Method, Path, Status, DataPath) —
  // NOT by an opKey, which this package cannot compute (see the note above):
  // the preview is shown to a human and a reshuffling list is unreviewable.
  func Derive(res *openapi.Resolver, doc *openapi.Document, ops []Operation, set domain.Settings, now time.Time) (*Proposal, error)`

// ---------------------------------------------------------------------------
// Shared context blocks. Each one is small on purpose (guide, cost rule 2: a
// blob interpolated into N agents is paid N times) and is composed per agent
// from only the blocks that agent actually needs.
// ---------------------------------------------------------------------------

const CTX_CORE = `You are building phase P1c (slice 1) of "mocker", a self-hosted mock-backend service written in Go.
Repo root is your CWD. Module path git.sumka.site/yakov/mocker. Go 1.26, stdlib net/http and database/sql
over SQLite (modernc.org/sqlite). No third-party router, no OpenAPI library — DESIGN §2 rejects both.

WHAT ALREADY EXISTS, committed, reviewed and green (HEAD is P1b):
- internal/config, internal/store (SQLite pools + the ONE migration), internal/domain (Settings, Identity,
  AuthSettings, slugs), internal/auth, internal/httpx, internal/workspaces, internal/admin (login, CSRF,
  workspaces, specs), internal/server (the host dispatcher), cmd/mocker.
- internal/openapi — Load, normalize, the lazy $ref Resolver (budget 32), the operation index.
- internal/specs — import, operations/responses rows, Routes, Variants, Normalized.
- internal/router — the sorted route table, Match, CanonicalPath, HasCanonical.
- internal/gen — P1b's response generator: the layered seed, the schema walk with byte/depth/node budgets,
  allOf/oneOf/discriminator composition, value sources by format and field name, the list contract.
- internal/mockplane — the per-workspace runtime (document + resolver + generator + variants) behind an LRU
  and singleflight, and respond.go: variant choice, Accept negotiation with 406, 204/HEAD, declared
  headers, the envelope, the settings delay.

WHAT THIS SLICE ADDS, and nothing else: the recipes engine (internal/recipes), its integration into the
generator, the op_overrides row (internal/overrides) applied at request time, the auth preset
(internal/authpreset) and the admin endpoints that expose both. The phase criterion is DESIGN §19's
"фронт логинится": a mocked login endpoint answers a token a frontend can decode, with an expiry it can
schedule a refresh against.

HARD RULE 1 — FILE OWNERSHIP. You own exactly the files your task lists. Do not create, edit, move or
delete any other file, including tests. Another agent owns it and your edit will be lost or will collide.

HARD RULE 2 — NO NEW RUNTIME DEPENDENCY. go.mod's runtime requirements are modernc.org/sqlite,
golang.org/x/crypto (argon2) and golang.org/x/sync (singleflight). github.com/santhosh-tekuri/jsonschema/v6
is TEST-ONLY and must never be reachable from ./cmd/mocker. In particular: no JWT library — a compact JWS
is crypto/hmac + crypto/sha256 + encoding/base64 + encoding/json.

HARD RULE 3 — THE SQLITE SCHEMA IS FROZEN. op_overrides, custom_endpoints, traffic and scenarios were all
created in P0's single migration (internal/store/migrations/0001_init.sql). Use the columns that are there.
Do NOT add a migration, a column or a table. If something seems to need one, it belongs in a later slice —
record it in "todo".

HARD RULE 4 — NEVER READ THE BIG FILES WHOLE. DESIGN.md is Russian and 87 KB: read only the line ranges you
are given. ${SPEC_PATH} is 347 KB: query it with jq, never Read or cat it.

HARD RULE 5 — THE WRITER POOL IS ONE CONNECTION. store.DB.Write takes the single writer connection for the
whole callback (store.go: SetMaxOpenConns(1)). Calling db.Write from INSIDE another db.Write callback — for
example calling workspaces.Repo.Update to bump a revision while holding an overrides transaction — blocks
forever waiting for a connection that will never be returned. It is a process-wide deadlock, not a slow
path. An override write and its revision bump therefore happen in ONE db.Write, with a direct
"UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?".

HARD RULE 6 — NO RECIPES MUST MEAN NO CHANGE. With no override rows, every byte the mock produces must be
identical to what P1b produced. Any refactor of internal/gen that shifts a seed input, a data path or an
iteration order breaks it, and it breaks every workspace in the installation at once.
BE CAREFUL WHAT PROVES THIS. Generating twice through two independently constructed Generators proves the
package is deterministic WITHIN ONE BUILD — it cannot see a change that shifted every body consistently,
which is exactly the failure mode this rule exists for. The guard is a GOLDEN captured from the tree BEFORE
this phase edits it (internal/gen/testdata/p1b_body_hashes.json, produced by the Engine agent as its first
action), and the acceptance test compares against that file. P1b's own TestAcceptance_Determinism is the
within-build check and stays as it is; it is not this rule's guard.
THAT FILE IS WRITE-ONCE FOR THIS RUN. Only the Engine agent's STEP ZERO may create it. No later agent —
including any fix agent that owns every file in the tree — may edit it, delete it, or run the test with
MOCKER_REGENERATE_GOLDEN set. A hash that stops matching is a defect in the code, never in the golden;
regenerating it keeps the assertion alive while making it prove nothing, which is worse than deleting it
because it looks like a pass.

WORKING RULES
- Write the tests with the code, in the same package, table-driven where it fits the existing style.
- Never weaken, skip or delete an existing test to reach green. If an existing test genuinely encodes
  behaviour this slice changes, say so in "deviations" with the file and line.
- Finish with all four clean and paste the real output of the last one into "verified":
    test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
- Comments explain WHY, in the voice of the surrounding code. No "// set x to 1".
`

const CTX_RECIPES = `
DESIGN §9 "Рецепты" (lines 375-409) and §10 (lines 412-471) are the specification. The digest, which is
what you build against:

Recipes are addressed BY A PATH IN THE DATA, not by a schema pointer: "items[*].status",
"user.profile.avatar_url". A recipe outranks every other source of a value — it beats a spec-declared
example, a const and a default. That is the whole point: the operator is overruling the document.

The value-source priority in internal/gen/values.go's leafValue is already written and is the contract:
    recipe -> example/examples -> const -> default -> enum -> generated
The recipe hook (recipeValue in values.go) exists and always declines today.

THERE ARE TWO SEAMS, NOT ONE, and values.go's own comment says so in passing ("media-type-level is handled
above this package, in gen.go's Body/contentExample"). gen.go's Body short-circuits on w.contentExample(v)
BEFORE walkSchema ever runs, so on an operation whose response declares a media-type example the whole body
is returned and recipeValue is never called once. DESIGN §9 line 339 ranks the recipe above that example
too: "рецепт → example/examples (media type → named → схема → const → default) → enum". So the
contentExample branch has to be gated on "this variant binds no recipe" — with no recipes the branch behaves
exactly as it does today (HARD RULE 6), and with recipes bound the walk runs and the recipe wins.
This is latent rather than urgent on the acceptance document, which carries ZERO media-type examples
(measured with jq, including on all three /auth/ operations) — but an override that silently does nothing
is precisely the failure the gates are told to treat as a blocker.

The kinds this slice implements, and their exact semantics:
- const    — the literal Value, coerced to the declared type or declined.
- enum     — one member of the Value array, chosen from Env.Seed so the same field on the same request
             always lands on the same member (this is the seed layer everything else uses).
- copy     — the value of a sibling field of the SAME object: "$.id" means "this object's id".
- identity — a field of the workspace identity: id, name, email, roles, org.id, org.name, org.type.
- jwt      — a compact JWS. Claims from the identity, then the recipe's Claims template merged over them,
             then iat/exp stamped from the REAL clock. exp = now + ttl, in epoch SECONDS.
- now      — the real clock plus an offset: "+3600s", "-7d". Rendered as epoch seconds, epoch
             milliseconds or ISO-8601 per Format, defaulting by the declared type.
- null     — force null.
- omit     — drop the property from the object entirely.
- listSize — pin the length of the array at that path.

TIME IS SPLIT, and the split is load-bearing (DESIGN §9 "Время"): ordinary generated fields derive from the
SEED, so the mock is reproducible. jwt and now derive from the REAL clock, because a token that expired
yesterday makes the installation unusable today. Everything else stays seeded.

DESIGN §10's identity alias table, which the auth preset uses:
    id, user_id, uid, sub                         -> identity.id
    name, full_name, fullName, display_name       -> identity.name
    email, login                                  -> identity.email
    roles, permissions                            -> identity.roles
    organization_id, org_id, app_id               -> identity.org.id

DESIGN §10's rules that are not negotiable:
- The token's sub and the id served by the profile endpoint come from ONE identity. A frontend that
  compares them and finds them different loops on the login screen forever.
- Lifetimes are epoch SECONDS. Milliseconds overflow a 32-bit setTimeout (max ~24.8 days) and fire
  immediately, which is an infinite refresh loop.
- Incoming tokens are NEVER verified. The mock accepts any Authorization header and none.
`

const CTX_SPEC = `
THE ACCEPTANCE DOCUMENT — ${SPEC_PATH}, a real production OpenAPI 3.0 document, gitignored, 347 KB.
NEVER Read or cat it; query it with jq. Facts measured on it TODAY with jq, not remembered:
- 110 paths, 130 operations. Two different variant counts circulate, and mixing them up produces a wrong
  assertion: taking each operation's PRIMARY response variant, 114 of the 130 carry a schema and P1b
  proves all 114 generate and validate. Counting EVERY declared response selector instead, the document
  has 419 variants. Say which one you mean whenever you report a number.
- securitySchemes: signAuth (apiKey), bearerAuth (http bearer), apiAuth (apiKey), BearerAuth (http bearer).
- The paths matching DESIGN §10's trigger list are exactly three, all POST:
    POST /api/v1/auth/token    -> 200 {response: {\$ref JWTInfo}}   (also declares 409,410,422,423,429,default)
    POST /api/v1/auth/refresh  -> 200 {response: {\$ref JWTInfo}}   (also 401, default)
    POST /api/v1/auth/userinfo -> 200 {response: {\$ref AuthUserInfo}} (also 422, default)
  There is NO /login, NO /me, NO /oauth/*, NO /userinfo at the root — only these three under /auth/.
- JWTInfo: required [token, refresh_token, token_expires_at, is_whitelisted];
  token and refresh_token are type string (described "JWT"), token_expires_at is integer/int64
  ("TS до которого живет access token"), is_whitelisted is boolean.
- AuthUserInfo: required [app_id]; properties app_id (integer int64) and vk_id (integer).
- Identity-alias property names PRESENT in the document, with occurrence counts:
    id 40, name 38, roles 20, organization_id 5, org_id 4, fullName 3, app_id 2, user_id 2, permissions 2.
- Identity-alias names ABSENT from the document — do NOT write an assertion against them:
    email, login, sub, uid, full_name, display_name, access_token, id_token, expires_in, exp — all ZERO.
- Token/expiry-shaped property names present: token, refresh_token, token_expires_at, expiresAt,
  expire_time, expire_count (expire_count is a COUNT, not a time — an expiry heuristic that grabs it is
  wrong, and it is in the document specifically to catch that).

Every test that reads this document goes through internal/testspec (testspec.Bytes(t) /
testspec.RepoRoot(t)), which SKIPS the test when the file is absent and fails on any other read error. A
fresh clone has no such file. Never invent a private copy of that helper.
`

const CTX_TEST = `
TESTING CONVENTIONS IN THIS REPO
- Tests live beside the code, package-internal where they need internals (the existing gen tests do).
- go test ./... -race -count=1 must pass. The mock plane serves concurrent requests over ONE shared
  runtime, so anything you add there is race-tested by construction, not by hope.
- A test that reads the acceptance document uses internal/testspec and skips cleanly without it. Report
  every SKIP your run prints and why — P1a shipped an acceptance test that silently skipped itself in the
  normal "go test ./..." run, and it took a hand check to notice.
- Never assert on a field name you have not confirmed with jq. Three separate measurement mistakes in P1b
  reached agent prompts because they were remembered instead of run. Verify, then assert.
`

const INTENDED = `
WHAT THIS SLICE IS FOR, so a reviewer does not report a deliberate boundary as a gap:
- IN: internal/recipes (the engine), its integration into internal/gen, internal/overrides (the
  op_overrides row: read, write, revision bump, applied at request time), internal/authpreset (derivation
  and preview), the admin endpoints for both, acceptance on the real document, and the docker smoke check.
- OUT, by decision, going to slice 2: the when[] conditions (this slice PARSES and PRESERVES them and
  never evaluates them — a stored condition must survive a round trip untouched), the session layer
  (forced status, delay, pause, fail_next), POST /__mocker/state, custom_endpoints, the traffic buffer and
  its poll endpoint. OUT to P2: schema_patch application (stored and round-tripped only), and the
  faker/template/sequence recipes. OUT to P3: the ref recipe and stateful resources. OUT to P1d: all UI.
- The 404 that /__mocker/state answers with code "not_implemented_yet" STAYS in this slice, and so does
  its test (internal/mockplane/plane_test.go, TestServeHTTP_NotImplementedYet). It is slice 2's endpoint.
`

// ---------------------------------------------------------------------------
// Structured returns.
// ---------------------------------------------------------------------------

const SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['files', 'summary', 'verified', 'contracts'],
  properties: {
    files: { type: 'array', maxItems: 40, items: { type: 'string' }, description: 'repo-relative paths you created or modified' },
    summary: { type: 'string', description: 'what you implemented, 3-8 sentences' },
    verified: { type: 'string', description: 'the exact commands you ran and their real output (gofmt/build/vet/test)' },
    contracts: {
      type: 'array', maxItems: 40, items: { type: 'string' },
      description: 'every exported signature you defined — funcs, methods, types AND error sentinels — one per line, as written in the code',
    },
    deviations: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'anything done differently from the task or DESIGN, with the reason' },
    todo: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'deliberately left for slice 2 or later' },
  },
}

const REVIEW_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['findings'],
  properties: {
    findings: {
      type: 'array',
      maxItems: 40,
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['file', 'line', 'severity', 'summary', 'failure', 'fix'],
        properties: {
          file: { type: 'string' },
          line: { type: 'integer' },
          severity: { type: 'string', enum: ['blocker', 'major', 'minor'] },
          summary: { type: 'string' },
          failure: { type: 'string', description: 'concrete inputs/state -> wrong behaviour' },
          fix: { type: 'string', description: 'one-line fix' },
        },
      },
    },
    verdict: { type: 'string' },
  },
}

const ACCEPT_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['files', 'summary', 'verified', 'measurements', 'passed', 'smoke'],
  properties: {
    files: { type: 'array', maxItems: 40, items: { type: 'string' } },
    summary: { type: 'string' },
    verified: { type: 'string', description: 'the real output of the commands you ran' },
    passed: { type: 'boolean', description: 'true only if every acceptance assertion you were able to RUN passes. A missing prerequisite (no docker, no acceptance document) goes in deviations and lowers smoke/passed rather than being reported as a pass you did not observe' },
    smoke: {
      type: 'string',
      enum: ['passed', 'failed', 'skipped-no-docker', 'not-applicable'],
      description: '"not-applicable" for an agent that does not run make smoke. A missing docker daemon is skipped-no-docker, NOT failed — the two mean opposite things to every reviewer downstream',
    },
    measurements: {
      type: 'array', maxItems: 20, items: { type: 'string' },
      description: 'the real numbers your test printed: bindings proposed, tokens decoded, operations still byte-identical, variants still schema-valid',
    },
    contracts: { type: 'array', maxItems: 20, items: { type: 'string' } },
    deviations: { type: 'array', maxItems: 20, items: { type: 'string' } },
    todo: { type: 'array', maxItems: 20, items: { type: 'string' } },
  },
}

const VERIFY_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['green', 'output', 'unresolved'],
  properties: {
    green: {
      type: 'boolean',
      description: 'true only if gofmt, build, vet and test -race are ALL clean. make smoke skipped for lack of docker does NOT make this false; make smoke FAILING does.',
    },
    output: { type: 'string', description: 'the real tail of the failing command, or the passing test summary' },
    smoke: { type: 'string', enum: ['passed', 'failed', 'skipped-no-docker'] },
    unresolved: {
      type: 'array',
      maxItems: 40,
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['n', 'why'],
        properties: {
          n: { type: 'integer', minimum: 1, description: 'the number from the list you were given THIS round; the list is renumbered from 1 every round' },
          why: { type: 'string' },
        },
      },
    },
  },
}

const logDeviations = (label, results) => {
  for (const r of results) {
    for (const d of (r && r.deviations) || []) log(`${label} deviation: ${d}`)
  }
}

// Later lines supersede earlier ones: a gate fix's contracts are appended after
// the original author's, so a corrected signature appears last.
const contractsOf = (results) =>
  results
    .filter(Boolean)
    .flatMap((r, i) => (i === 0 ? [] : ['--- later lines supersede any identical symbol above ---']).concat(r.contracts || []))
    .join('\n')

// P1a and P1b filtered a contract list by regex before handing it to the next
// agent. Both uses are gone from this script and the helper with them: the
// filters were case-sensitive, and every seam that matters here is unexported
// Go (lower-case), so the filter dropped exactly the lines the next agent
// needed. The lists are capped at 40 items by SCHEMA; passing them whole costs
// nothing worth this failure mode.

// The diff command every reviewer runs. The ";" and the "." are both
// load-bearing and were both blockers found by P1b's pre-flight gate: with "&&"
// and a path list, one path nobody happened to create makes "git add -N" exit
// 128 having added nothing, and the reviewer then reads an EMPTY diff and
// approves it as "the section produced nothing".
const gateCtx = (paths) => `Repo root is your CWD. READ-ONLY: do not modify any file. Running tests is expected.
TO SEE THE SECTION'S DIFF — most of these files are NEW and plain "git diff HEAD" does not show untracked
files at all:
    git add -N . ; git diff HEAD -- ${paths}
Intent-to-add stages nothing for commit. Your co-reviewer runs the same command concurrently: on
".git/index.lock: File exists", wait a second and retry.
Do NOT widen the path list: .claude holds ~137 KB of untracked workflow script and diffing them costs more
than the code you are here for.
HEAD is P1b — committed, reviewed and green — so this diff is exactly what this run has produced so far.
DESIGN.md is Russian and 87 KB; never read it whole. The section index: §6 136-174, §7 175-245, §8 246-296,
§9 297-411, §10 412-471, §12 509-556, §13 557-820, §14 821-916, §15 917-982, §18 1088-1105, §19 1106-1128.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it, query it with jq only.
Report ONLY defects you can point at with a file and a line. No praise, no style nits that do not change
behaviour. An empty findings array is the correct answer for a clean vector.`

// A gate: two reviewers on one section, then a single fix agent if either found
// a blocker, then a bounded one-round re-review of only the vectors that raised
// one. Majors are carried to the end of the run rather than fixed here.
const runGate = async (name, vectors, fixCtx) => {
  const results = await parallel(vectors.map((v) => () => agent(v.prompt, { label: v.label, phase: name, model: v.model || 'sonnet', schema: REVIEW_SCHEMA })))
  const findings = results.filter(Boolean).flatMap((r) => r.findings || [])
  for (const [i, r] of results.entries()) {
    if ((r?.findings || []).length >= 40) log(`WARNING: ${vectors[i].label} returned the schema maximum of 40 findings — its list is probably TRUNCATED, so this section is not fully reported.`)
  }
  const blockers = findings.filter((f) => f.severity === 'blocker')
  const dead = vectors.filter((_, i) => !results[i]).map((v) => v.label)
  if (dead.length) log(`WARNING: ${name} reviewer(s) returned nothing: ${dead.join(', ')}. That vector did NOT run — treat the section as unreviewed along it.`)
  log(`${name}: ${findings.length} findings, ${blockers.length} blockers`)
  for (const f of findings.filter((x) => x.severity !== 'blocker')) log(`${name} ${f.severity}: ${f.file}:${f.line} — ${f.summary}`)

  let fix = null
  if (blockers.length) {
    const list = blockers.map((f, i) => `${i + 1}. ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`).join('\n')
    fix = await agent(`${fixCtx}

${name} found ${blockers.length} BLOCKER finding(s) in the section just written. Fix them, changing as
little as possible — every edit must map to a numbered finding. You own every file the findings name.

${list}

RULES
- Verify each finding against the code FIRST. Reviewers false-positive. If one is wrong, do NOT "fix" it:
  record it in "deviations" with the evidence that it is wrong.
- Add or extend a test for every real defect you fix, so it cannot come back silently.
- Never weaken, skip or delete a test to reach green.
- Finish with gofmt/build/vet/test -race all clean and paste the real output into "verified".`,
      { label: `${name}:fix`, phase: name, model: 'sonnet', schema: SCHEMA })
    if (fix) logDeviations(`${name} fix`, [fix])
  }

  let unresolved = []
  if (blockers.length && !fix) {
    log(`${name}: the fix agent returned NOTHING — carrying its ${blockers.length} blocker(s) unfixed into the end-of-run fix list.`)
    unresolved = blockers
  } else if (blockers.length && fix) {
    const dirty = vectors.filter((_, i) => (results[i]?.findings || []).some((f) => f.severity === 'blocker'))
    const fixScope = (fix.files || []).join(' ')
    const again = await parallel(dirty.map((v) => () => agent(`${v.prompt}

RE-REVIEW, ROUND 2. You raised blocker findings on this section and a fix agent has since edited it.
NARROW YOUR DIFF: the fix touched only ${fixScope || '(it reported NO file list, so the command below diffs the whole tree — read only what falls inside the section paths above)'}.
Diff THAT, not the whole section again — you already read the section this round:
    git add -N . ; git diff HEAD -- ${fixScope || '.'}
The "git add -N ." is not redundant: the fix was told to ADD tests, and a file created after this round's
first diff was never intent-added, so without it a new test file reads as absent and you would report a
fixed finding as still open.
IF THAT DIFF COMES BACK EMPTY, the file list was wrong (the fix agent reports its own paths and can get
them wrong, or may have deleted rather than modified a file) — fall back to the section paths above. An
empty diff is NEVER evidence that the fix holds.
Two things only, and keep them separate:
1. For each blocker you raised, is it ACTUALLY fixed — semantically, not just compiling? A fix that
   silences the symptom (an assertion deleted, a check moved after the thing it guarded, a race hidden
   behind a mutex on the hot path) is NOT fixed; report it again.
2. Did the fix BREAK something else in your vector? Only report what the edit itself introduced.
Do not re-audit the whole section and do not raise new findings unrelated to the fix. An empty findings
array means the fix holds.`, { label: `${v.label}:recheck`, phase: name, model: v.model || 'sonnet', schema: REVIEW_SCHEMA })))

    const reFindings = again.filter(Boolean).flatMap((r) => r.findings || [])
    for (const f of reFindings.filter((x) => x.severity === 'minor')) log(`${name} recheck minor: ${f.file}:${f.line} — ${f.summary}`)
    unresolved = reFindings.filter((f) => f.severity === 'blocker' || f.severity === 'major')
    for (const [i, v] of dirty.entries()) {
      if (again[i]) continue
      const orphaned = (results[vectors.indexOf(v)]?.findings || []).filter((f) => f.severity === 'blocker')
      unresolved = unresolved.concat(orphaned)
      log(`${name}: ${v.label}'s recheck died — carrying its ${orphaned.length} original blocker(s) forward unverified.`)
    }
    if (unresolved.length) log(`${name} RE-REVIEW: ${unresolved.length} finding(s) survived the fix — carried into the end-of-run fix list.`)
    else if (again.every(Boolean)) log(`${name} re-review: the fix holds.`)
    if (again.some((r) => !r)) log(`${name} re-review: a reviewer returned nothing — that vector's fix is UNVERIFIED. Do NOT read the absence of findings as a pass.`)
  }
  // dead travels with the result: a gate whose two reviewers both died returns
  // findings: [] and blockers: [] — structurally identical to a clean gate. The
  // log() above says so, but the run's RETURN VALUE is what gets read later, and
  // "nobody reviewed this" must not arrive there as silence.
  return { findings, fix, unresolved, dead }
}

// ------------------------------------------------------------------ Kernel --
// internal/recipes is a new package nothing else has seen yet, and three later
// sections are written against its signatures. It runs ALONE and FIRST for the
// same reason P1b's kernel did: two agents inventing the same types in parallel
// is how a phase stops compiling an hour in.
//
// Model: sonnet. This is the one call where the routing tier deserves an
// explicit note, because it mints tokens. The three-prong opus test fails on
// prong 2: every risky property here (three segments, base64url without
// padding, exp in SECONDS, sub tied to identity.id, alg:none) is checked by a
// cheap downstream step — the gate reads it and the acceptance test DECODES a
// minted token against the real document. The auto-round-up clause for crypto
// covers reasoning that guards a security boundary; this mock never verifies an
// incoming token and DESIGN §10 says so twice, so a signature defect costs
// realism, not access.
phase('Kernel')

const kernel = await agent(`${CTX_CORE}${CTX_RECIPES}${CTX_TEST}

YOUR TASK: write internal/recipes, the whole package, from nothing.

YOU OWN, and nothing else:
  internal/recipes/recipes.go      — Kind, Recipe, Validate, Deferred, the sentinels
  internal/recipes/set.go          — Set, Compile, Lookup, ListSizeAt, MatchPattern, Bindings
  internal/recipes/value.go        — Recipe.Value, IdentityField, NowValue, Coerce
  internal/recipes/jwt.go          — MintJWT
  internal/recipes/recipes_test.go, set_test.go, value_test.go, jwt_test.go
(Split differently if a file gets unwieldy, but stay inside internal/recipes/.)

THESE SIGNATURES ARE FIXED. Three later sections are written against them verbatim; do not rename, do not
reorder parameters, do not "improve" them. If one is genuinely unimplementable, implement the rest and say
so loudly in "deviations" — do not silently substitute your own.

${SIG_RECIPES}

WHAT MATTERS, in the order it will bite:

1. PATTERN MATCHING is the part everything else rests on. Data paths as internal/gen produces them:
   the root is "", a property is joined with "." (schema.go's joinPath), an array element is "name[3]"
   (fmt.Sprintf("%s[%d]")). So real paths look like "user.profile.avatar_url", "items[2].status",
   "roles[0]". Patterns use the same grammar plus "[*]" for any index. Precedence when two patterns match
   the same path must be TOTAL and deterministic: an exact index beats a wildcard, more literal segments
   beat fewer, and a tie breaks on the lexicographically smaller pattern. Table-test the precedence, not
   just the matching — "map iteration order decides which recipe wins" is a bug that only shows up in
   production, under a different Go build, on a Tuesday.

2. COERCION decides whether a recipe is honest. Env.Type is the declared JSON type of the node the recipe
   is about to fill. Allowed: a number into a string field (its JSON form), a numeric string into a
   number/integer field, a bool into a string. NOT allowed, and must return ok=false so the caller falls
   through to normal generation: an array or object into a scalar field, a non-numeric string into a
   number, a JWT into an integer. Env.Type == "" means the schema declared no type — then the value goes
   through as-is. This one rule is what keeps the acceptance number at 114/114 schema-valid.

3. MintJWT. header {"alg":"HS256","typ":"JWT"} (or {"alg":"none","typ":"JWT"}), payload = identity claims
   first (sub from identity.id, name, email, roles, and org when present), then the caller's extra map
   merged OVER them, then iat and exp stamped last so a template cannot accidentally forge its own expiry.
   base64url, NO padding (base64.RawURLEncoding). Signature = HMAC-SHA256 over "header.payload" with
   auth.SigningKey as the key, base64url-raw. alg "none" emits the two segments and a trailing dot with an
   EMPTY third segment — that is what the format requires, and a decoder that gets a garbage signature
   instead will throw.
   exp = now.Unix() + ttl, and ttl comes from ttlSec when non-zero, else auth.JWTTTLSec, else 3600.
   SECONDS. A test must assert the arithmetic in seconds AND that the value is not a millisecond
   timestamp (a cheap way to pin it: exp - now.Unix() is in [1, 86400*30], and exp is ~10 digits).
   Determinism: two calls with the same "now" produce byte-identical tokens; that is what makes them
   testable. JSON claim marshalling must therefore be key-ordered (encoding/json sorts map keys already —
   assert it rather than assume it).

4. NowValue. Offsets: an optional sign, digits, a unit of s/m/h/d ("+3600s", "-7d", "+90m"). Formats:
   "epoch" (int64 seconds), "epoch_ms" (int64 milliseconds — never the default, only when asked for
   explicitly), "iso" (RFC3339 in UTC). Default when Format is "": epoch seconds for a numeric target,
   RFC3339 for a string target. An unparseable offset is ErrRecipe, not a silent zero.

5. Recipe.Value returns (nil, false, nil) — decline — for a Deferred kind. Never guess: copy and omit and
   listSize are the CALLER's job and a value invented here would be wrong twice over.

6. Validate is called on every decode of user input. An unknown Kind, a const with no Value, an enum with
   an empty array, an identity with an unknown Field, a listSize with a negative or inverted range: all
   ErrRecipe with a message naming the offending field. A malformed override row must never panic the mock
   plane, which is reachable unauthenticated.

TESTS: table-driven over the pattern precedence, every coercion pair (allowed and refused), the JWT
structure (three segments, decodable header and payload, sub == identity.id, exp arithmetic, alg none),
NowValue's four formats and its rejections, and Validate's rejections. No network, no clock reads other
than an injected "now" — a test that calls time.Now() is a test that fails at midnight.`,
  { label: 'recipes:kernel', phase: 'Kernel', model: 'sonnet', schema: SCHEMA })

if (!kernel) {
  // P1b's script stopped here for the same reason and it is the right call:
  // internal/recipes is a NEW package that five later sections import. If it is
  // genuinely not on disk, nothing after this point can "go build ./...", and
  // the remaining 17 agents would each run to completion against a tree that
  // cannot compile — the whole run's budget spent producing nothing, with a
  // structurally normal-looking return value at the end. The work may well be on
  // disk (an agent can die on its report having written every file), but the
  // script cannot tell the two apart and the downside is asymmetric.
  log('FATAL: the recipes kernel agent returned nothing. Five later sections import internal/recipes; stopping rather than running 17 agents against a tree that may not compile. Check internal/recipes by hand — the work may be on disk — and resume from the Engine phase if it is.')
  return { phase: 'P1c-1', failed: 'kernel', acceptance: [], findings: 0, actionable: 0, rounds: [], stillOpen: [], unreviewed: [], criterionProven: false }
}
logDeviations('Kernel', [kernel])

const kernelContracts = contractsOf([kernel])

// ------------------------------------------------------------------ Engine --
// internal/gen integration. ONE agent, alone: it edits four files of a package
// six other packages depend on, and HARD RULE 6 (no recipes -> byte-identical
// output) is a property of the package as a whole, not of any one file.
phase('Engine')

const engine = await agent(`${CTX_CORE}${CTX_RECIPES}${CTX_TEST}

YOUR TASK: plug the recipes engine into internal/gen.

YOU OWN, and nothing else:
  internal/gen/recipes.go        — NEW: the recipe seam, the copy/omit post-pass, the Env builder
  internal/gen/recipes_test.go   — NEW
  internal/gen/values.go         — recipeValue only (it is a stub that always declines today)
  internal/gen/gen.go            — the two new Options fields, the new Request field, wiring in Body, and
                                   the gate on the contentExample short-circuit described above
  internal/gen/list.go           — the listSize recipe at the page level
  internal/gen/schema.go         — the walker's new recipePrefix field and its propagation, PLUS walkArray
                                   and arrayLength for the listSize recipe (both live in schema.go, not
                                   list.go — arrayLength at line 624, walkArray at 464)
  internal/gen/gen_test.go, list_test.go, values_test.go — additions only, never a deletion
  internal/gen/schema_test.go    — call-site updates and additions only, never a deletion. It constructs a
                                   walker directly (line 65) and calls w.arrayLength (line 483), so a
                                   signature change there stops the package compiling and NO other agent
                                   owns this file

DO NOT TOUCH internal/gen/seed.go, compose.go or names.go. The seed layers are a published contract with an
acceptance test behind them, and a change there reshuffles every mock in every workspace.

THE RECIPES PACKAGE IS WRITTEN. Its signatures, verbatim:

${SIG_RECIPES}

What the kernel agent actually reported building (later lines supersede earlier ones):
${kernelContracts || '(it reported nothing — read internal/recipes/*.go)'}

STEP ZERO, BEFORE YOU EDIT A SINGLE LINE OF internal/gen — capture the golden that HARD RULE 6 is measured
against. Right now the tree's internal/gen is byte-for-byte what P1b committed (the only thing this run has
added so far is internal/recipes, a new package nothing imports yet), so this is your one chance to record
what "unchanged" means. Do it in this order:
  1. Write internal/gen/golden_p1b_test.go (you own it), in package gen_test — NOT package gen. That is
     forced, not stylistic: enumerating the corpus needs a real *specs.Repo, internal/specs imports
     internal/gen, and a test in package gen importing specs is an import cycle. internal/gen/
     acceptance_test.go is already package gen_test for exactly this reason and its header says so.
     THEREFORE REUSE ITS HELPERS, DO NOT COPY THEM — a redeclaration in the same package does not compile.
     Already declared there: acceptanceFixture, loadAcceptance, acceptanceTestConfig, acceptanceLogger,
     fixedClock, samplePathParams, pickPrimaryVariant, findOp, newSchemaOracle, fakeAcceptanceSpecSource,
     fakeAcceptanceWorkspaceSource, acceptancePlane, acceptanceRequest. Prefix everything you declare with
     "golden".
     Put the enumeration in ONE named helper, because a later agent regenerates the same corpus and
     compares — two hand-written enumerations WILL diverge, and a divergence is indistinguishable from the
     regression this golden exists to catch:
         func goldenCorpus(t *testing.T, fx *acceptanceFixture) map[string]string
     It must, for every operation in fx.ops and EVERY variant in fx.variants[op.ID], build the request
     exactly as acceptance_test.go lines 389-392 already do — this shape is part of the hash, because
     SeedList hashes Method, CanonicalPath, the sorted PathParams and Status:
         req := gen.Request{
             Method: strings.ToUpper(op.Method), CanonicalPath: op.CanonicalPath,
             PathParams: samplePathParams(op.Path), Query: url.Values{}, Status: v.HTTPStatus,
         }
     ListFamily and IDParam stay zero (they change list generation, and a golden must pin one shape).
     Options are fixed: Seed 12345, ListSize 10, NullRate 0, MaxBytes 4<<20, Now: fixedClock.
     THE MAP KEY IS THE SELECTOR, NOT THE STATUS:
         fmt.Sprintf("%s %s #%s", strings.ToUpper(op.Method), op.Path, v.Selector)
     op.Path is the RELATIVE path, never CanonicalPath. And it must be v.Selector because HTTPStatus is
     NOT unique per variant: internal/specs/index.go's classifySelector maps BOTH "default" and "2XX" to
     200, and this document has 88 operations that declare a 200 AND a default. Measured with jq: 419
     variants collapse to 331 distinct (method, path, httpStatus) keys — so a status-keyed golden would
     silently overwrite the primary 200 body with the default error body for 88 operations, including
     POST /api/v1/auth/token, which is this phase's own criterion. Nothing would fail; the guard would
     just quietly stop guarding the bodies that matter. The selector is a JSON object key, so it is unique
     within an operation by construction.
     The value is the hex sha256 of the body bytes, or "-" when Body returns nothing, so a variant that
     stops producing a body is a visible change rather than a missing key.
  2. Have the test write internal/gen/testdata/p1b_body_hashes.json (os.MkdirAll first — the directory does
     not exist) with that map plus the options it used, ONLY under MOCKER_REGENERATE_GOLDEN=1; without the
     env var it READS the file and compares. It skips cleanly, via internal/testspec, when the acceptance
     document is absent. Run it once with the env var set, then once without.
     EXPECT ONE ENTRY PER RESPONSE VARIANT: the document declares 419 of them across its 130 operations
     (measured with jq). The familiar 130/114 figures are per-OPERATION — 114 is how many operations carry
     a response schema — and they are the wrong target here. If your count comes out near 130 you have
     silently switched to one variant per operation (pickPrimaryVariant), which shrinks the guard by three
     quarters while looking like compliance. State the real number in "summary" and paste the run's output
     into "verified" (the schema you return has no "measurements" field; putting it there loses it).
     LIST internal/gen/testdata/p1b_body_hashes.json in your "files" array — a later step checks for it by
     name to confirm HARD RULE 6 is guarded at all.
  3. Only then start implementing. At the end, run the same test WITHOUT the env var: every hash must still
     match. If one does not, you have broken HARD RULE 6 — find it, do not regenerate the golden. The
     golden is regenerated only by a human who has decided the output SHOULD change.
This file is the phase's regression guard and later agents assert against it, so it is not optional and it
cannot be produced after the fact.

WHAT YOU ADD TO gen, exactly:

${SIG_GEN}

1. recipeValue (values.go). It is the FIRST step of leafValue's priority chain and its doc comment already
   says this phase plugs in here. Build a recipes.Env from the walker (Identity and Auth from Options, Now
   from w.now — the clock is ALREADY frozen once per response, do not call time.Now() again, Seed from
   SeedScalar(w.seedList, dataPath), Type and Format from the schema node) and call Recipe.Value. Decline
   (return false) when there is no *Set, no binding, or the recipe declined — every one of those falls
   through to the existing chain unchanged.

1b. THE SECOND SEAM, in gen.go's Body. contentExample short-circuits and returns the whole body before
   walkSchema runs, so on a variant that declares a media-type example no recipe would ever be consulted.
   Gate that branch on "this variant binds no recipe" — req.Recipes == nil || req.Recipes.Len() == 0 — so
   that with no recipes the behaviour is bit-for-bit what it is today (HARD RULE 6) and with recipes bound
   the walk runs. Do not otherwise rewrite contentExample: its oversized-example fallthrough is existing,
   tested behaviour.

2. THE ABSOLUTE PATH. Recipes are bound to absolute data paths, but inside a generated list item the walk
   RESTARTS dataPath at "" (list.go's generateItems: iw.walkNode(itemsNode, "", 0), with a per-item seed).
   Add a recipePrefix field to the walker, set it where the item walk restarts (e.g. "items[3]"), and use
   prefix+"."+dataPath for recipe LOOKUP ONLY.
   THIS IS THE TRAP: recipePrefix must never reach a seed input, a filter, a field-name classification or
   anything else. SeedScalar(w.seedList, dataPath) stays exactly as it is. If you find yourself passing the
   prefixed path to anything but the recipe lookup, stop — that is HARD RULE 6 breaking.

3. THE COPY/OMIT POST-PASS (recipes.go). After the body value is built and BEFORE it is marshalled, walk
   the finished value once, applying:
   - copy: Recipe.Field is "$.<name>" — the value of <name> in the object CONTAINING the matched path. A
     missing sibling leaves the generated value alone (and is worth a debug log, not an error).
   - omit: delete the property from its parent object; for an array element, drop the element and close
     the gap.
   Walk with an explicit budget — the same shape as walkBudget's node ceiling — so a pathological body plus
   a pathological pattern cannot spin. The post-pass must be a no-op, allocating nothing, when the Set is
   nil or binds no deferred kind: this is the hot path for every request in every workspace.
   NOTE the honest consequence and put it in the doc comment: omit on a REQUIRED property produces a body
   that does not satisfy its schema. That is the operator overruling the document on purpose (DESIGN §9
   lists omit as a recipe), so it is allowed — but it must be documented at the seam, because the
   acceptance test asserts schema conformance and someone will need to know why an omit-ed field is exempt.

4. listSize (list.go and walkArray). Set.ListSizeAt at the array's own path pins the length: it overrides
   Options.ListSize for that array, and for a range the length is picked from the seed so it stays
   reproducible. It NEVER overrides the schema's own minItems/maxItems (a pinned 50 on a maxItems:10 array
   is a schema violation the operator did not ask for — clamp, and only clamp). It also never overrides an
   explicit ?limit= in the query for a paged list: the client asked, the client wins.

5. Options.Identity/Options.Auth are per-workspace and stable; Request.Recipes is per (operation, status).
   The Generator stays immutable and shared across concurrent requests — that property is what
   go test -race is aimed at, and it is why recipes cannot live in Options.

TESTS THAT MUST EXIST:
- Byte-identity: for a handful of representative schemas (an object, a list, a nested list, a
  self-referencing one), Body with Request.Recipes == nil is byte-identical to Body before this change.
  Pin it by generating twice through two independently constructed Generators, exactly as P1b's test does.
- Priority: a recipe beats a media-type example, a const and a default on the same field.
- Precedence: "items[*].status" applies to every item; "items[0].status" beats it on item 0 only.
- copy: "$.id" on a list item copies THAT item's id, not the first item's — the classic off-by-one that
  makes every row of a table look identical.
- Coercion refusal: a jwt recipe bound to an integer field leaves the generated integer alone and the body
  still satisfies the schema.
- listSize: pinned length, range from the seed, clamped by maxItems, and beaten by an explicit ?limit=.
- Race: a table test running one Generator from several goroutines with recipes bound (go test -race).`,
  { label: 'gen:engine', phase: 'Engine', model: 'sonnet', schema: SCHEMA })

if (!engine) log('WARNING: the gen engine agent returned NOTHING. Check internal/gen by hand — the work may be on disk.')
// The golden cannot be captured after the fact: STEP ZERO is the only moment
// internal/gen is still byte-for-byte P1b. If it did not land, HARD RULE 6 is
// unguarded for the rest of the run and every byte-identity result below is
// worth nothing — which is a thing to know now, not at the end.
if (engine && !(engine.files || []).some((f) => f.includes('p1b_body_hashes.json'))) {
  log('WARNING: the Engine did not report internal/gen/testdata/p1b_body_hashes.json among its files. HARD RULE 6 may be unguarded for this whole run, and the golden CANNOT be captured now that internal/gen has been edited. Check by hand before believing any byte-identity claim below.')
}
logDeviations('Engine', [engine])

// ------------------------------------------------------------------ Gate 1 --
phase('Gate 1')

const gate1Scope = 'internal/recipes internal/gen internal/domain'
const gate1Ctx = `${gateCtx(gate1Scope)}

THE SECTION UNDER REVIEW: internal/recipes (new — the recipe model, pattern matching, the value kinds and
HS256 token minting) and its integration into internal/gen (recipeValue, Request.Recipes, the copy/omit
post-pass, the listSize recipe). DESIGN §9 lines 375-409 and §10 lines 412-471 are the specification.
${INTENDED}`

const gate1 = await runGate('Gate 1', [
  {
    label: 'gate1:determinism',
    prompt: `${gate1Ctx}

YOUR VECTOR: determinism and the value chain — the two properties this whole product rests on.
- HARD RULE 6: with Request.Recipes == nil, is the output PROVABLY unchanged from P1b? Do not take the
  test's word for it. Read every edit to internal/gen and ask whether it can alter a seed input, a data
  path, an iteration order or a budget decision when no recipe is bound. The recipePrefix field is the
  prime suspect: find every place it is read and confirm none of them is a seed, a filter or a field-name
  classification. Run:  go test ./internal/gen/... -race -count=1
- Is anything that must be seeded reading the real clock instead? Only jwt and now may. Conversely, is
  anything that must be real-clock (exp, iat) reading the seed? Both directions are defects.
- Pattern precedence: is it TOTAL and deterministic, or does a tie fall through to Go map order? Construct
  a Set where two patterns match one path and check the winner is stable across runs (run the test 10x, or
  read the code and prove the ordering).
- The priority chain in leafValue: does a recipe really outrank an example, a const and a default, and does
  a DECLINING recipe leave the rest of the chain byte-identical?
- The copy post-pass on list items: does "$.id" resolve within the item that owns the field, or does it
  leak the first item's value across every row?
- Coercion: find a pair (recipe kind, declared type) where a value gets written that the schema rejects.
  That is a blocker — P1b's 114/114 schema-conformance result is the number this slice must not lower.
  ONE EXEMPTION, deliberate and documented at the seam: the omit recipe on a REQUIRED property produces a
  body its schema rejects. DESIGN §9 lists omit as a recipe, so that is the operator overruling the
  document on purpose. Report it only if it is undocumented, or if it happens without an omit binding.
- The media-type example seam: gen.go's contentExample returns before walkSchema. Is it gated on "no
  recipes bound"? Ungated, every override on an operation that declares a media-type example silently does
  nothing. Gated wrongly (e.g. on the Set being non-nil but empty), P1b's output changes when it must not.
- Budgets: can the post-pass or the pattern matcher be made to spin or allocate unboundedly by a crafted
  pattern plus a large body?`,
  },
  {
    label: 'gate1:token',
    prompt: `${gate1Ctx}

YOUR VECTOR: the token and the identity contract — DESIGN §10 is unusually specific and every rule in it is
there because a real frontend broke on its absence.
- Decode a minted token yourself. Write a tiny Go test or use base64 + jq in the shell:
  three segments, base64url with NO padding, a header that parses, a payload that parses.
- exp and iat in epoch SECONDS. §10 line 450: milliseconds overflow a 32-bit setTimeout and produce an
  infinite refresh loop. A millisecond value anywhere in the token or in a "now" default is a BLOCKER.
- sub == identity.id, and the same identity feeds both the token and any profile projection. A mismatch
  loops a frontend on the login screen — §10 line 447.
- The claim merge order: identity claims first, the template over them, iat/exp stamped LAST. A template
  that can overwrite exp is a footgun; a template that cannot add a custom claim is useless. Which is it?
- alg "none": does it emit the trailing dot with an empty third segment, or a garbage signature that a
  decoder will reject?
- The signing key comes from domain.AuthSettings.SigningKey, which is generated per workspace
  (domain.NewSigningKey). Is it ever logged, echoed into a response, or defaulted to a constant when
  empty? A constant fallback is a blocker in spirit even though the mock verifies nothing.
- IdentityField: does it cover the fields §10's alias table needs (id, name, email, roles, org.id,
  org.name, org.type) and decline the rest, or does it silently return nil for a typo?
- Validate: feed it malformed recipes (unknown kind, const with no value, empty enum, inverted listSize
  range, an identity field that does not exist). Anything that panics is a blocker — the mock plane runs
  this unauthenticated.`,
  },
], `${CTX_CORE}${CTX_RECIPES}

You own internal/recipes/*.go and the internal/gen files this section touched: recipes.go, values.go,
gen.go, list.go, schema.go, and the test files recipes_test.go, gen_test.go, list_test.go, values_test.go,
schema_test.go (it constructs a walker directly and calls arrayLength, so a fix to either lands there) and
golden_p1b_test.go. Do not edit anything outside those. internal/gen/testdata/p1b_body_hashes.json is
WRITE-ONCE (HARD RULE 6): you may fix the test that reads it, never the file itself, and never with
MOCKER_REGENERATE_GOLDEN set.

${SIG_RECIPES}

${SIG_GEN}`)

// ------------------------------------------------------------------- Store --
// Two NEW packages that share no file and no symbol: overrides talks to SQLite,
// authpreset is a pure function over a resolver. They run in parallel, which is
// safe here in a way it was not for P1b's Serve phase — different packages,
// different directories, no mutual references. Each is told to build only its
// own package first so a half-written sibling cannot make it think it broke the
// tree, then to run the full build at the end.
phase('Store')

const storeCtx = `${CTX_CORE}${CTX_TEST}

TWO AGENTS RUN IN PARALLEL IN THIS SECTION, in two different new packages: internal/overrides (the
op_overrides row) and internal/authpreset (the derivation). They share no file and neither imports the
other. While you work, the OTHER package may be half-written: run "go build ./internal/<yours>/..." and
"go test ./internal/<yours>/... -race" while iterating, and only at the END run the full "go build ./..." /
"go test ./... -race -count=1". If the full build fails ONLY inside the other agent's package, say so in
"deviations" and do not try to fix it — it is not yours.
The same applies to gofmt, and it matters more because gofmt WRITES: while iterating run
"gofmt -l ./internal/<yours>", and if the final repo-wide "gofmt -l ." lists a file outside your package,
do NOT format it — report it in "deviations". Formatting the other agent's half-written file is an edit to
a file you do not own.

${SIG_RECIPES}
`

const store = await parallel([
  () => agent(`${storeCtx}

YOUR TASK: internal/overrides — the op_overrides row: decode, encode, read, write, and the revision bump.

YOU OWN, and nothing else:
  internal/overrides/overrides.go   — Row, Variant, Condition, ListSize, the JSON shapes
  internal/overrides/repo.go        — Repo, ForWorkspace, Get, Put, PutMany, Delete
  internal/overrides/opkey.go       — OpKey, ParseOpKey
  internal/overrides/*_test.go

THESE SIGNATURES ARE FIXED — the mock plane and three admin handlers are written against them verbatim:

${SIG_OVERRIDES}

THE TABLE ALREADY EXISTS (HARD RULE 3 — no migration). From internal/store/migrations/0001_init.sql:

  CREATE TABLE op_overrides (
    id             INTEGER PRIMARY KEY,
    workspace_id   INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    method         TEXT NOT NULL,
    path           TEXT NOT NULL,             -- WITHOUT the base path
    operation_id   INTEGER REFERENCES operations(id) ON DELETE SET NULL,  -- cache only
    override_on    INTEGER NOT NULL DEFAULT 1,
    route_off      INTEGER NOT NULL DEFAULT 0,
    active_status  INTEGER,
    responses      TEXT NOT NULL DEFAULT '{}',
    list_size      TEXT,
    delay_ms       TEXT,
    fail_directive TEXT,
    validate_req   INTEGER,
    resource_id    INTEGER REFERENCES resources(id) ON DELETE SET NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (workspace_id, method, path)
  );

WHAT MATTERS:

1. THE REVISION BUMP IS THE POINT. The mock plane caches a compiled runtime keyed by (workspace_id,
   revision); an override that does not bump the revision is an override nobody will ever see. Read HARD
   RULE 5 again: the bump goes in the SAME db.Write transaction as the row write, as a direct
   "UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?" — never by calling
   workspaces.Repo.Update, which opens its own write transaction on a one-connection pool and deadlocks.
   Return the NEW revision so the handler can report it. A write to a workspace that does not exist must
   fail cleanly, not create an orphan row.

2. ROUND-TRIP FIDELITY. Variant carries four fields this slice does not implement — When, SchemaPatch,
   BodyEncoding on a body it does not pin, and the row's FailDirective. They must survive read -> modify one
   unrelated field -> write, byte-preserved. A test must prove it with a Variant carrying every field
   populated, including two conditions and a schema patch. Dropping them would silently delete an
   operator's work the moment slice 2 ships.

3. OPKEY. DESIGN §14: "метод + относительный путь в percent-encoding" — it must survive a spec version
   change and a basePath edit, which is why it is built from the RELATIVE path and never from a row id. It
   goes in ONE path segment of an admin URL, so encode it such that "/" inside the path does not split the
   segment (url.PathEscape does not escape "/" — check what you use, and round-trip-test it against paths
   with slashes, braces, unicode and a query-looking suffix). ParseOpKey rejects a malformed key with an
   error rather than half-parsing it.

4. VALIDATION ON DECODE. responses is user-supplied JSON. Every recipe goes through recipes.Recipe.Validate,
   an unknown mode is rejected, a status key that is not a 3-digit number is rejected, base64 bodies are
   decoded and re-encoded to prove they are decodable. A malformed row must produce an error, never a
   panic: the mock plane reads these rows on an unauthenticated request path.

5. ForWorkspace is ONE query for the whole workspace. It is called once per runtime build; a query per
   operation is the N+1 DESIGN §18 forbids on the hot path.

6. Row.Path is the RELATIVE path, exactly as internal/specs stores operations.path — leading slash, no base
   path, "{param}" segments intact. Normalize what you accept (upper-case the method, reject an empty or
   non-slash-prefixed path) so two spellings of one operation cannot both get a row past the UNIQUE
   constraint.

TESTS: a real temporary SQLite database via internal/store (look at internal/workspaces/repo_test.go for
the existing harness style), covering: insert then read back with every field populated; update one field
and prove the others round-trip; revision incremented by exactly one per Put and per PutMany (NOT per row);
delete of a missing row is a no-op that still bumps nothing it should not; concurrent Puts from several
goroutines under -race; a malformed responses blob decoded into an error rather than a panic.`,
    { label: 'store:overrides', phase: 'Store', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${storeCtx}${CTX_RECIPES}${CTX_SPEC}

YOUR TASK: internal/authpreset — derive the auth preset from a document. Derivation and preview only; it
never writes anything.

YOU OWN, and nothing else:
  internal/authpreset/authpreset.go   — Operation, Binding, Proposal, Derive
  internal/authpreset/names.go        — the alias tables and the field heuristics
  internal/authpreset/*_test.go

THESE SIGNATURES ARE FIXED — the admin handler is written against them verbatim:

${SIG_PRESET}

THE RESOLVER YOU WALK (internal/openapi, written and green — do not modify it):
  func NewResolver(doc *Document, budget int) *Resolver
  func (r *Resolver) Resolve(pointer string) (any, error)     // follows $ref CHAINS
  func (r *Resolver) ResolveNode(node any) (any, error)       // resolves a node that may itself be a $ref
  openapi.DefaultRefBudget is the budget the rest of the code uses.
A schema node is a map[string]any as decoded from JSON: "properties" is a map, "items" is a node, "allOf"
/"oneOf"/"anyOf" are arrays. Type-assert with the comma-ok form ALWAYS: the document is user-supplied and a
bare assertion is a panic reachable from an admin upload.

WHAT DERIVE DOES:

1. Collect the security schemes from the document (components.securitySchemes) into Proposal.Schemes as
   readable strings. On the acceptance document they are exactly: signAuth (apiKey), bearerAuth
   (http bearer), apiAuth (apiKey), BearerAuth (http bearer). Their presence is what makes the preset
   applicable at all; record them even when no binding comes out of them.

2. Decide which operations are AUTH PATHS, per DESIGN §10 line 455: paths matching /auth/*, /login,
   /token, /refresh, /oauth/*, /userinfo, /me — matched on the RELATIVE path, case-insensitively, on
   whole segments (a path containing the word "token" as part of a longer segment, like
   "/journal/accounts/link/{tokenId}", is NOT an auth path, and the acceptance document contains exactly
   that trap: /api/v1/journal/tokens, /api/v1/id/{token} and two /linkInfo/{tokenId} paths).

3. Walk each operation's response schema (SchemaPtr through the resolver) collecting leaf property paths,
   with a visited set for $ref cycles and a depth budget — the acceptance document contains a
   self-referencing schema and an unbounded walk hangs the admin request. Bind:
   - ON AN AUTH PATH: a string property named token / access_token / refresh_token / id_token / jwt gets a
     jwt recipe. A refresh_token gets its own jwt recipe with a longer ttl (a refresh token that expires
     with the access token is useless) — state the multiplier in the Reason.
   - EXPIRY FIELDS, on any path: a NUMERIC property named exp / expires_in / *_expires_at / expiresAt /
     expires_at / not_after / valid_until gets a now recipe with the auth ttl as its offset, in epoch
     seconds. expires_in is a DURATION, not a timestamp — it gets the ttl as a plain number, not a
     future epoch. Do NOT match expire_count: it is a count, it is in the acceptance document, and it
     exists in this brief precisely to catch a name-substring heuristic.
   - IDENTITY ALIASES, on any path: DESIGN §10's table (id/user_id/uid/sub -> identity.id;
     name/full_name/fullName/display_name -> identity.name; email/login -> identity.email;
     roles/permissions -> identity.roles; organization_id/org_id/app_id -> identity.org.id).
     BUT NOT BLINDLY: a bare "id" on a list-item schema is the item's own identity, and rewriting every id
     in every list to the logged-in user's id turns a 40-row table into 40 copies of one row. Bind a bare
     "id" ONLY on an auth path or on a profile-shaped response (a schema whose properties also carry a
     name/email/roles alias). Everywhere else, bind the EXPLICIT aliases (user_id, organization_id, org_id,
     app_id, roles, permissions) and put what you skipped, and why, into Notes. This judgement is the
     difference between a preset that helps and one an operator has to undo by hand.
   - The type must match: an identity.roles (array) binding onto a string property is refused, and a jwt
     onto an integer is refused. recipes.Coerce is the arbiter — call it rather than reimplementing the
     rule.

4. Every Binding carries a Reason in plain words ("property \\"token\\" on auth path POST /api/v1/auth/token
   -> jwt") and a Source. Bindings are sorted by (Method, Path, Status, DataPath) so the preview is stable — a
   human is about to read it and approve it.

5. Notes must say what was NOT bound and why: an operation with no schema, a walk that hit its depth
   budget, an alias refused on type, bare ids skipped outside profile shapes. A silent skip reads as
   "there was nothing to do".

6. SampleJWT: one recipes.MintJWT over the passed-in settings and now, so the preview shows the operator
   the actual token that will be served. If the identity has no id, the sample still mints (with the
   claim absent) rather than failing the whole preview.

TESTS:
- Unit tests over small hand-written documents: the auth-path matcher including the four trap paths, the
  expiry matcher including expire_count, the bare-id rule (bound on a profile shape, skipped on a list),
  the type refusals, the cycle-safe walk (a schema that $refs itself).
- ONE acceptance test over ${SPEC_PATH} through internal/testspec (skips when the file is absent), pinning
  the real numbers: the three auth paths are found, POST /api/v1/auth/token gets jwt bindings on token and
  refresh_token, token_expires_at gets a now binding, POST /api/v1/auth/userinfo gets an identity binding
  on app_id -> identity.org.id. Print the counts with t.Logf so the run has real measurements. Do not
  assert a total binding count you have not measured — measure it with the test, then pin it.`,
    { label: 'store:preset', phase: 'Store', model: 'sonnet', schema: SCHEMA }),
])

const storeOK = store.filter(Boolean)
const storeGap = [!store[0] && 'store:overrides', !store[1] && 'store:preset'].filter(Boolean).join(', ')
if (storeGap) log(`WARNING: ${storeGap} returned NOTHING. The work may still be on disk — the next section is written against the signature blocks in this script, so it proceeds; verify those packages by hand.`)
logDeviations('Store', storeOK)

// ------------------------------------------------------------------ Gate 2 --
phase('Gate 2')

const gate2Scope = 'internal/overrides internal/authpreset internal/store internal/specs'
const gate2Ctx = `${gateCtx(gate2Scope)}

THE SECTION UNDER REVIEW: two new packages — internal/overrides (the op_overrides row: decode, encode,
read, write, revision bump) and internal/authpreset (deriving the auth preset from a document). DESIGN §10
lines 412-471, §13 lines 557-820 (the schema; op_overrides is at lines 670-694 of DESIGN and lines 165-189
of internal/store/migrations/0001_init.sql), §14 lines 821-866.
internal/store and internal/specs are in the diff path list on purpose: the schema is FROZEN this phase, so
any change under internal/store — a new migration above all — is a HARD RULE 3 violation and a blocker.
${INTENDED}`

const gate2 = await runGate('Gate 2', [
  {
    label: 'gate2:persistence',
    prompt: `${gate2Ctx}

YOUR VECTOR: persistence correctness — the transaction, the revision and the round trip.
- HARD RULE 5, the expensive one: does any write path call store.DB.Write from inside another db.Write
  callback (directly, or via workspaces.Repo.Update, or via any helper that opens its own transaction)?
  The writer pool is SetMaxOpenConns(1) (internal/store/store.go), so that is a permanent deadlock of the
  whole process, not a slow query. Trace every call under Put, PutMany and Delete.
- Is the revision bumped in the SAME transaction as the row write? A bump in a second transaction leaves a
  window where the row exists and the runtime cache still serves the old build; no bump at all means the
  override is invisible forever. Check PutMany bumps ONCE, not once per row.
- Round-trip fidelity: write a Variant with when[], schemaPatch, bodyEncoding, headers and recipes all
  populated, read it back, modify one unrelated field, write again, read again. Anything dropped is a
  blocker — it silently deletes an operator's work when slice 2 ships. Run the package's own tests and
  then check whether they actually cover this or merely assert on a Variant with two fields set.
- OpKey: round-trip it against paths with "/", "{param}", unicode, a percent sign and a "?" — and confirm
  it survives being one segment of an admin URL (does the encoding escape "/"? url.PathEscape does not).
- SQL: placeholders everywhere, no string-built WHERE; the UNIQUE (workspace_id, method, path) conflict
  handled as an upsert rather than surfacing as a raw driver error; rows.Err() checked; every Rows and Stmt
  closed. A missing rows.Err() is how a truncated ForWorkspace becomes "this workspace has no overrides".
- Decode of user JSON: is every path guarded against a panic (a nil map write, a bare type assertion, an
  unbounded recursion in a nested body)? These rows are read on the unauthenticated mock-plane path.
- N+1: is ForWorkspace really one query?`,
  },
  {
    label: 'gate2:derivation',
    prompt: `${gate2Ctx}

YOUR VECTOR: does the preset actually produce the right bindings on the REAL document, and does it refuse
the wrong ones? Verify with jq against ${SPEC_PATH} and by running the package's tests — do not trust the
brief and do not trust the test names.
- The auth-path matcher against the four traps that exist in this document: /api/v1/journal/tokens,
  /api/v1/id/{token}, /api/v1/journal/accounts/link/{tokenId}, /api/v1/journal/accounts/linkInfo/{tokenId}.
  If any of them is treated as an auth path, real fields get overwritten with tokens. Confirm the three
  genuine ones (/api/v1/auth/token, /auth/refresh, /auth/userinfo) ARE found.
- The expiry heuristic against expire_count (a count, present in the document). A now recipe on it is a
  blocker.
- The bare-"id" rule: "id" appears 40 times in this document. Binding identity.id to all of them turns
  every list into 40 copies of one row — the single most damaging thing this preset can do. Check where
  the code draws the line and whether the line holds on a list-item schema that happens to carry a "name".
- Type refusals: identity.roles (an array) onto a string property, jwt onto an integer, identity.org.id
  (whatever type it is in settings) onto a string. Does recipes.Coerce actually gate them, or is the check
  written twice and only one copy is right?
- Schema-walk safety: a $ref cycle, a schema 30 levels deep, an allOf of 200 branches. Is there a visited
  set AND a depth budget? This runs inside an admin HTTP request; an unbounded walk hangs the handler.
- Determinism of the output order — a preview a human approves must not reshuffle between two GETs.
- Does Derive write ANYTHING (a database call, a file, a mutation of its inputs)? It must not: DESIGN §10
  requires the mapping to be shown and edited before anything is applied. In particular, does it mutate the
  maps it walked (a normalizer that "fixes" a node in place mutates the caller's parsed document)?
- Notes: is every skip recorded? An empty Notes with 40 skipped ids is a report that lies by omission.`,
  },
], `${CTX_CORE}${CTX_RECIPES}

You own internal/overrides/*.go and internal/authpreset/*.go. Do not edit anything outside those two
directories — in particular, do not touch internal/store (the schema is frozen) or internal/gen.

${SIG_OVERRIDES}

${SIG_PRESET}`)

// ------------------------------------------------------------------- Serve --
// SERIAL, three stages, and this is a correction P1b paid for: two agents
// writing different files of the SAME Go package while referencing each other's
// symbols can neither of them reach "go build clean", so neither finishes. The
// admin stage is a different package but goes last for a cheaper reason — it
// calls into both of the others' finished shapes.
phase('Serve')

const serveCtx = `${CTX_CORE}${CTX_TEST}

${SIG_RECIPES}

${SIG_OVERRIDES}

${SIG_GEN}
`

const serveRuntime = await agent(`${serveCtx}

YOUR TASK, stage 1 of 3 in this package: carry the overrides into the compiled runtime.

YOU OWN, and nothing else:
  internal/mockplane/runtime.go        — the runtime struct and buildRuntime
  internal/mockplane/overrides.go      — NEW: the per-operation lookup the serving path uses
  internal/mockplane/routes.go         — the OverrideSource interface ONLY (SpecSource is declared there,
                                         so its sibling belongs there; the Plane STRUCT lives in plane.go)
  internal/mockplane/plane.go          — the Plane's new field and ONE new method, described below
  internal/mockplane/runtime_test.go, routes_test.go — additions only
DO NOT TOUCH internal/mockplane/respond.go or respond_test.go: stage 2 owns them and edits them next.
DO NOT TOUCH internal/mockplane/plane_test.go's TestServeHTTP_NotImplementedYet — POST /__mocker/state
stays a 404 with code "not_implemented_yet" through this whole slice; it is slice 2's endpoint.

WHAT EXISTS (read it before editing):
- runtime.go holds "type runtime struct { table *router.Table; gen *gen.Generator; variants
  map[int64][]gen.ResponseVariant; settings domain.Settings }", built by buildRuntime and cached by
  routeCache.getOrBuild under singleflight, keyed by (workspace_id, revision). A runtime is IMMUTABLE
  after runtimeFor returns it — every field written once, never again — and that is precisely what lets any
  number of concurrent requests read a shared *runtime with no lock. Keep that property. It is what
  go test -race is aimed at.
- routes.go declares "type SpecSource interface" (the specs.Repo subset the plane needs) and holds the LRU.
- plane.go's New(cfg, src, specs, log) has SEVEN call sites in SIX files, and you own exactly one of them:
    cmd/mocker/main.go:129, internal/server/server_test.go:88 and :255, internal/server/p1a_test.go:93,
    internal/mockplane/plane_test.go:62, internal/gen/acceptance_test.go:883 — none of them yours —
    and internal/mockplane/routes_test.go:184, which is.
  DO NOT CHANGE New's PARAMETER LIST. Your definition of done is "go build ./... and go test ./... -race
  clean", and adding a parameter makes that unreachable: five files you may not touch stop compiling, one
  of them inside internal/gen, a package that was already gated and signed off this run.

WHAT YOU ADD:
1. An OverrideSource interface in mockplane — the internal/overrides.Repo subset the plane needs, declared
   the same way SpecSource already is (so tests can fake it and so the plane does not depend on the
   concrete type). Exactly one method is needed for the serving path:
       ForWorkspace(ctx context.Context, workspaceID int64) (map[string]*overrides.Row, error)
   Inject it with a new exported method rather than a constructor parameter:
       func (p *Plane) SetOverrides(src OverrideSource)
   cmd/mocker/main.go calls it once (stage 3 wires that); a Plane whose SetOverrides was never called
   behaves exactly as it does today, which is what keeps all six other call sites compiling untouched.
   It is called during startup, before the plane serves anything — say so in its doc comment, because a
   setter on a type read by many goroutines is otherwise a fair thing for a reviewer to call a race.
   One consequence worth a line in the same comment: a runtime already sitting in the LRU keeps serving
   without overrides until its workspace's revision changes. That is correct — the cache key is
   (workspace, revision) and every override write bumps the revision — but it is worth saying out loud so
   nobody later "fixes" it by clearing the cache from the setter.
2. buildRuntime loads the overrides ONCE per (workspace, revision) — the same build the LRU already caches
   — and stores them on the runtime, keyed so that respond.go can find the row for a matched route in O(1)
   without rebuilding a key per request. THE KEY IS NOT THE CANONICAL PATH. router.Route carries BOTH:
   Route.CanonicalPath ("/a/{}"), and Route.Path, whose own doc comment (internal/router/router.go:34-38)
   says it is "relative — WITHOUT the base path — exactly as stored in operations.path" — which is
   byte-identical to overrides.Row.Path. So the key is overrides.OpKey(Route.Method, Route.Path), exact and
   with nothing to canonicalize. Keying on CanonicalPath instead would silently collapse a stale
   "/a/{name}" row onto "/a/{id}"; router.go:39-42's comment about CanonicalPath being "the override key
   P2 needs" is about custom endpoints and conflicts, not about this lookup. Expose a single lookup helper
   in overrides.go that stage 2 and the tests both call, and say in its doc comment which field it keys on
   and why. Getting this wrong means every override silently misses and every test still passes.
2b. COMPILE THE RECIPES HERE, at build time, not per request. Each variant's recipes map goes through
   recipes.Compile once and the resulting *recipes.Set is stored on the runtime, addressed by (route key,
   status as a decimal string) — NOT by the route key alone: Variant.Recipes is per status, and respond.go
   only learns which status it is answering with after chooseVariant runs, so a map keyed by operation
   alone is unusable at exactly the moment it is needed — and the status it keys on is the one ACTUALLY
   being served, after the row's active_status has been applied, not chooseVariant's original pick.
   DESIGN §18
   forbids per-request work on the mock plane, and Compile both validates and precompiles patterns. A map
   that fails Compile is LOGGED and that variant serves with no recipes at all — a stored row must never be
   able to fail the whole runtime build, because that would take the workspace's entire mock down.
3. gen.Options gains Identity and Auth this phase — fill them from ws.Settings here, next to Seed/ListSize/
   NullRate/MaxBytes. Without it every identity and jwt recipe silently produces nothing.
4. An override row must survive a spec that no longer has that operation (a re-import): a row whose path
   matches no route is simply never consulted. It must NOT be an error and must NOT drop the whole map.
5. The plane must keep working with a nil OverrideSource, exactly as it already does with a nil SpecSource:
   cmd/mocker will pass a real one, but the P0 test helpers pass nil and they must not have to change.

IN YOUR "contracts" FIELD — and this OVERRIDES the schema's own description of that field as "every
exported signature" — list the UNEXPORTED helpers, runtime fields and map shapes stage 2 must call as
well as the exported ones, exactly as you declared them. Everything that matters at this seam is
package-internal (the runtime struct is lowercase), so a contracts list of only exported names hands the
next agent nothing and it will guess.

TESTS: buildRuntime loads overrides and the lookup finds the row for a real matched route (including a
parameterised path); a row for an unknown operation is inert; nil source is safe; -race with several
goroutines resolving the same runtime.
While you iterate, "go build ./internal/mockplane/..." is enough; end with the full four-command check.`,
  { label: 'serve:runtime', phase: 'Serve', model: 'sonnet', schema: SCHEMA })

if (!serveRuntime) log('WARNING: serve:runtime returned NOTHING. Stage 2 proceeds against the signature block; verify internal/mockplane/runtime.go by hand.')
logDeviations('Serve runtime', [serveRuntime])

const serveRespond = await agent(`${serveCtx}${CTX_RECIPES}

YOUR TASK, stage 2 of 3 in this package: apply the override when answering.

YOU OWN, and nothing else:
  internal/mockplane/respond.go
  internal/mockplane/respond_test.go
DO NOT TOUCH runtime.go, overrides.go or routes.go — stage 1 just wrote them and its shapes are fixed.
DO NOT TOUCH plane_test.go's TestServeHTTP_NotImplementedYet.

WHAT STAGE 1 BUILT (its own report; the code is on disk, read it before you rely on this):
${contractsOf([serveRuntime]) || '(it reported nothing — read internal/mockplane/runtime.go and overrides.go)'}

WHAT respond.go DOES TODAY, in order: settings delay -> chooseVariant -> degraded short-circuit (empty 200)
-> 204/205 no-body -> Accept negotiation with 406 -> declared headers (CR/LF sanitised) -> body -> envelope
-> Content-Type/Content-Length -> write. Every one of those steps has a test. Add to the chain; do not
rewrite it.

WHAT YOU ADD, in the order it must happen (DESIGN §6's flowchart, lines 136-162):
1. route_off -> 404. An operation switched off answers exactly the 404 the plane already answers for an
   unknown route (serveNoRoute's shape), not a new body nobody has seen. override_on == false disables the
   WHOLE row: the operation behaves as if there were no override at all.
2. active_status: when the row pins a status, that variant answers instead of chooseVariant's pick. A
   pinned status with no matching variant in the document is NOT an error — answer that status with no
   body rather than 500, and log it. (The when[] conditions that would otherwise select a variant are
   slice 2: they are parsed and preserved, never evaluated. Do not implement them here.)
3. mode "pinned": the variant's Body is served verbatim — decoded from base64 when BodyEncoding says so —
   with the variant's MediaType (falling back to the document's), its Headers on top of the declared ones,
   and NO generation at all. The envelope still applies to a JSON pinned body: settings.envelope is a
   workspace-wide statement about the wire shape, and a pinned body that skips it answers a shape the
   frontend does not parse. Content-Length must be right.
4. mode "generated" (the default, and the case that matters for this slice): read the *recipes.Set stage 1
   compiled at runtime-build time and pass it on gen.Request.Recipes. LOOK IT UP BY THE STATUS YOU ARE
   ACTUALLY SERVING — after step 2's active_status has been applied, not by chooseVariant's original pick.
   Stage 1 keyed the map by (route key, status) for exactly this reason: get it wrong and recipes simply
   never fire on any operation whose status the operator pinned, silently, with every test still green. Do NOT call recipes.Compile here —
   that would put a validation pass and an allocation on the hot path DESIGN §18 says must stay empty. If
   stage 1 did not compile them (read runtime.go and overrides.go — you may not edit either), say so in
   "deviations" and pass what it did store; do not reach into its files to fix it.
5. list_size and delay_ms from the row override the workspace settings for this operation. The existing
   clamp on the delay (maxSimulatedDelay, 30s, context-cancellable) still applies — an operator must not be
   able to pin a 10-minute delay and hold a connection.
6. Everything else stays exactly as it is: 204/205 and HEAD still answer no body, 406 still fires on an
   incompatible Accept, degraded operations still answer an empty 200, declared headers are still
   CR/LF-sanitised (a pinned header value is user input and goes through the same sanitiser — a header from
   an override row is the most direct response-splitting vector this phase adds).

TESTS: route_off 404; override_on=false is inert; active_status picks another declared status and also
handles an undeclared one; a pinned JSON body served verbatim with its media type, its headers and the
envelope; a pinned base64 body; a generated body with a const recipe visible in the output; a jwt recipe
producing a decodable token through the full serving path; per-operation delay clamped; HEAD on a pinned
body still empty with the right Content-Type; -race across concurrent requests to one runtime with recipes
bound.`,
  { label: 'serve:respond', phase: 'Serve', model: 'sonnet', schema: SCHEMA })

if (!serveRespond) log('WARNING: serve:respond returned NOTHING. Verify internal/mockplane/respond.go by hand.')
logDeviations('Serve respond', [serveRespond])

const serveAdmin = await agent(`${serveCtx}${CTX_RECIPES}

${SIG_PRESET}


YOUR TASK, stage 3 of 3: the admin surface for overrides and the auth preset.

YOU OWN, and nothing else:
  internal/admin/override_handlers.go   — NEW
  internal/admin/preset_handlers.go     — NEW
  internal/admin/server.go              — the new routes and any new Server field ONLY
  internal/admin/override_handlers_test.go, preset_handlers_test.go — NEW
  cmd/mocker/main.go                    — YES, you edit it, and it is not optional: see item 0
DO NOT TOUCH internal/admin/auth_handlers.go, security.go, workspace_handlers.go, spec_handlers.go or their
tests, and do not touch anything under internal/mockplane, internal/overrides or internal/authpreset.

WHAT EXISTS: Server holds cfg, sessions, ws (*workspaces.Repo), db, log and a specsRepo it constructs
itself in New — because New's signature is shared with cmd/mocker/main.go, which this package does not own.
Follow that precedent for the overrides repo. Handler() registers routes on an http.ServeMux with Go 1.22
patterns ("GET /api/workspaces/{id}") and wraps everything in
Chain(mux, securityHeaders, rateLimitLogin, attachSession, enforceCSRF). Existing helpers: parseWorkspaceID,
decodeJSON, and the httpx respond/err helpers. Read spec_handlers.go for the house style of a view type
plus a handler; match it.

THE ROUTES (DESIGN §14 lines 828-862):
  GET    /api/workspaces/{id}/operations           — the merged view
  GET    /api/workspaces/{id}/operations/{opKey}
  PUT    /api/workspaces/{id}/operations/{opKey}
  DELETE /api/workspaces/{id}/operations/{opKey}
  GET    /api/workspaces/{id}/auth-preset          — the PROPOSAL (nothing written)
  POST   /api/workspaces/{id}/auth-preset          — apply an (edited) list of bindings
DESIGN §14 lists the operations routes verbatim; it does NOT name an auth-preset endpoint, so these two are
this slice's addition — record that in "deviations" as a deliberate DESIGN extension, with the reason: §10
requires the mapping to be shown and edited BEFORE anything is written, which needs a preview call and an
apply call.

WHAT MATTERS:
0. WIRE THE PLANE IN THE REAL BINARY. This is item zero because without it every other line here works in
   tests and does nothing in production. cmd/mocker/main.go:129 currently reads
       mockPlane := mockplane.New(cfg, ws, specRepo, log)
   Stage 1 added a setter to inject the override source without changing New's signature:
       func (p *Plane) SetOverrides(src OverrideSource)
   Construct the overrides repo (overrides.NewRepo(db), the same db main.go already has) and call
   mockPlane.SetOverrides(...) immediately after New, during startup, before the server begins serving.
   Skip this and the shipped binary runs with a nil source: every override silently misses in production
   while every Go test passes. That exact failure — a nil in main.go shipping as a dead feature — is in
   this project's HANDOFF as a P1a near-miss.
1. The merged view is what the UI will render (P1d) and what a human uses to check the preset: for every
   operation of the workspace's spec, its method, relative path, opKey, declared statuses, and the override
   state when a row exists (route_off, active_status, per-status mode, how many recipes). Build it from
   specs.Repo.Operations + specs.Repo.Variants + overrides.Repo.ForWorkspace — THREE queries for the whole
   list, not three per operation. A workspace with no spec answers an empty list, not a 404.
   specs.Repo.Operations(ctx, specID, limit, offset) is PAGED, and its doc comment says limit <= 0 means
   ALL rows — pass 0, do not invent a magic number and do not let an HTTP ?limit= reach it unclamped. The
   acceptance document has 130 operations; a view that silently truncates lies about which operations
   exist.
2. PUT is a full replacement of that operation's override document, and it must ROUND-TRIP the fields this
   slice does not implement (when[], schemaPatch, fail_directive): a UI that GETs, edits one field and PUTs
   back must not silently delete them. Reject an unknown status key, an unknown mode, an invalid recipe
   (recipes.Recipe.Validate) with 400 and a message naming the field — never a 500, and never a panic.
3. Every write answers the NEW revision, so a caller can tell the mock plane picked it up. The bump happens
   inside the overrides repo (HARD RULE 5) — do not bump it here as well, or a single edit jumps the
   revision by two and invalidates the cache twice.
4. GET /auth-preset builds the authpreset.Operation list from specs (method, RELATIVE path, status,
   SchemaPtr, OpPointer) and calls
     Derive(res *openapi.Resolver, doc *openapi.Document, ops []Operation, set domain.Settings, now time.Time)
   — note it needs BOTH a resolver and the document. Build them exactly the way
   internal/mockplane/runtime.go:92-101 does: specs.Repo.Normalized -> openapi.Load -> openapi.NewResolver(
   doc, openapi.DefaultRefBudget). "set" is the WORKSPACE settings (domain.Settings), not domain.AuthSettings
   — two adjacent struct parameters are exactly what P0's post-mortem says gets swapped silently. It returns
   the Proposal and writes NOTHING.
5. POST /auth-preset takes a list of bindings — the SAME shape GET returned, possibly edited or filtered by
   the operator. A Binding carries Method, Path, Status, DataPath, Recipe: it deliberately does NOT carry an
   opKey, because the percent-encoding belongs to internal/overrides. Compute it here with
   overrides.OpKey(b.Method, b.Path). Validate every recipe, group them into overrides.Row values by that
   key and status
   (merging into any existing row's responses rather than replacing the whole row: an operator who already
   pinned a 409 body must not lose it), and applies them with ONE PutMany, hence ONE revision bump. It must
   NOT re-derive server-side and apply what it derived: that would silently ignore the operator's edits,
   which is the exact thing §10 says must not happen.
6. Authorisation: these are admin-plane routes behind the existing session and CSRF middleware. Do not
   add a new bypass, do not register anything outside /api/, and confirm the CSRF enforcement covers PUT
   and DELETE (read security.go — if it only lists POST, that is a finding for your report, not a silent
   fix in someone else's file).
7. Errors: a workspace that does not exist is 404; a malformed opKey is 400; a missing override is 404 on
   GET and a no-op 204 on DELETE.

TESTS in the style of internal/admin/spec_handlers_test.go (which builds a real stack over a temp DB):
log in, create a workspace, import a small inline spec, attach it, then: GET operations (empty override
state), PUT an override with recipes, GET it back byte-equal including a when[] you never implemented, GET
auth-preset on a spec whose login endpoint returns a token-shaped field, POST a filtered subset of the
bindings and assert only those landed, DELETE, and the revision increments across the sequence.`,
  { label: 'serve:admin', phase: 'Serve', model: 'sonnet', schema: SCHEMA })

if (!serveAdmin) log('WARNING: serve:admin returned NOTHING. Verify internal/admin by hand.')
logDeviations('Serve admin', [serveAdmin])

const serveOK = [serveRuntime, serveRespond, serveAdmin].filter(Boolean)

// ------------------------------------------------------------------ Gate 3 --
phase('Gate 3')

// internal/gen is deliberately NOT here. Its diff against HEAD is the Engine
// phase's work — the largest in the run — and Gate 1 already reviewed it, so a
// second and third reviewer reading it whole would be pure re-billing. The
// boundary check on it is a file-list check, not a diff read: see gate3Ctx.
const gate3Scope = 'internal/mockplane internal/admin internal/server cmd go.mod go.sum'
const gate3Ctx = `${gateCtx(gate3Scope)}

THE SECTION UNDER REVIEW: the serving path and the admin surface — the runtime now carries override rows,
respond.go applies them (route_off, active_status, pinned bodies, recipes, per-operation delay and list
size), and internal/admin exposes the merged operations view, the override CRUD and the auth preset
(preview + apply). DESIGN §6 lines 136-162 (the request path), §8 lines 246-296 (the router and CORS),
§14 lines 821-866 (the admin API), §15 lines 917-982 (the threat model), §18 lines 1088-1105 (load).

TWO THINGS TO KNOW BEFORE YOU REPORT THEM AS DEFECTS:
- internal/gen is NOT in your diff path list on purpose: the Engine phase rewrote it earlier in this run
  and Gate 1 reviewed that in full. Your check on it is the FILE LIST only —
      git add -N . ; git diff --stat HEAD -- internal/gen
  (the "git add -N ." is repeated here on purpose: --stat shows nothing for an untracked file, and a NEW
  stray file is exactly what this check exists to catch)
  The EXPECTED file list is: recipes.go, values.go, gen.go, list.go, schema.go, their _test.go files,
  golden_p1b_test.go and testdata/p1b_body_hashes.json — those last two are the HARD RULE 6 golden the
  Engine phase is required to produce, not a stray. Anything OUTSIDE that list means an agent in THIS
  section crossed a package boundary, and that is the finding. Do not read the diff's body.
- Plane.SetOverrides is a deliberate startup-only setter, not a mutable field on the request path. The
  right check is that cmd/mocker/main.go calls it once during startup, before the server begins serving —
  which gives a happens-before edge to every request goroutine. Reporting the setter itself as a race, or
  "fixing" it with a mutex the hot path then takes on every request, is a defect rather than a fix.
${INTENDED}`

const gate3 = await runGate('Gate 3', [
  {
    label: 'gate3:hotpath',
    prompt: `${gate3Ctx}

YOUR VECTOR: the request path — correctness and the cost per request.
- Does the RECIPE lookup use the status actually being served, after active_status was applied, or
  chooseVariant's original pick? The compiled Set is keyed by (route key, status); keying the lookup on
  the wrong one means recipes never fire on any operation with a pinned status — silently, with every
  test green. Read both sides of that key rather than trusting either file's comment.
- Does an override ever silently MISS? The route carries a canonical path ("/a/{}") and the override row a
  relative path with names ("/a/{id}"). Find the key both sides build and prove they agree, on a
  parameterised path, on a path with two parameters, and on a workspace whose basePath is non-empty. A
  mismatch here makes every test pass and every override invisible in production.
- Is the runtime still IMMUTABLE after publication? Any field written after runtimeFor returns, any lazily
  compiled recipe cached into a shared map on first use, any map the serving path writes to — that is a
  data race on a structure many goroutines read. Run: go test ./internal/mockplane/... -race -count=1
- Per-request cost: is recipes.Compile called per request or once per runtime build? Is the override map
  copied per request? Is anything parsing JSON per request that could have been parsed at build time?
  DESIGN §18 forbids per-request work on the mock plane.
- Order of operations against DESIGN §6: route_off before generation, delay applied once (not twice, once
  from settings and once from the row), 204/205 and HEAD still bodyless, 406 still fires, degraded still
  answers empty 200, envelope still applied — including to a pinned body.
- Content-Length correctness on a pinned body, on a base64-decoded body, and on HEAD.
- The admin merged view: three queries or three per operation? Check the code, then check it against a
  workspace with 130 operations.
- Does PUT round-trip when[] and schemaPatch? GET, edit one field, PUT, GET again — anything missing is a
  blocker (it deletes an operator's work when slice 2 ships).
- Does POST /auth-preset apply the operator's edited list, or re-derive and apply its own? The second
  ignores the human, which is the exact failure DESIGN §10 forbids.
- Revision: exactly one bump per admin write. Two bumps (repo plus handler) means two runtime rebuilds and
  a UI that shows a revision the plane never had.
- DOES cmd/mocker/main.go ACTUALLY CALL mockPlane.SetOverrides(...), after mockplane.New and before the
  server starts serving? A missing call is a BLOCKER: every override in the shipped binary is dead, and
  not one Go test would notice, because the tests wire their own source. Read main.go, do not infer it.`,
  },
  {
    label: 'gate3:security',
    prompt: `${gate3Ctx}

YOUR VECTOR: security. This section is where operator-supplied data first reaches the wire on the OPEN mock
plane. DESIGN §15 (lines 917-982) says the mock plane is deliberately reachable by everyone in the contour
and the admin password is SHARED across the team, so "an authenticated admin did it" is a low bar, not a
defence; §18 (1088-1105) forbids a rate limit on the mock plane, so the per-request cost IS the defence.
- Response splitting: a pinned header name or value, a pinned media type, or a generated value reaching
  WriteHeader with a CR or LF in it. Follow every new header path to setSafeHeader — or find the one that
  bypasses it.
- A pinned body is arbitrary operator bytes served with an operator-chosen Content-Type on a host a browser
  trusts. Is text/html servable? Is that acceptable here (the mock plane is a separate host per workspace,
  no admin session cookie is scoped to it) — reason it through and say which way it falls rather than
  assuming. Check what stops a pinned body from being served on the ADMIN host.
- Panic reachability from an unauthenticated request: a bare type assertion on a decoded override blob, a
  nil map write, a slice index on a malformed pattern, a base64 decode of attacker-shaped input. The
  recover middleware turns each into a 500, which denies that workspace's mock to whoever depends on it.
- Resource cost: what is the largest response one crafted override row can make the plane produce, and
  what bounds it? A pinned body has no MaxBytes gate unless someone added one; a listSize recipe on a
  nested schema multiplies at every level (DESIGN §9 "Границы"). Measure the worst case you can construct.
- The signing key: is domain.AuthSettings.SigningKey ever returned by an admin endpoint (the merged view,
  the preset preview, an error message) or written to a log? The preview returns a SAMPLE TOKEN on purpose;
  the key must not travel with it.
- The admin routes: behind attachSession and enforceCSRF, both of them, for PUT and DELETE as well as POST?
  Read security.go rather than assuming the chain covers every method. A CSRF check that only lists POST
  leaves PUT and DELETE open to a cross-site form.
- Authorisation on the workspace id: can workspace A's session write workspace B's overrides? P0 chose a
  shared password and no per-user isolation (§15), so the answer may legitimately be "yes, by design" —
  confirm which, and confirm the handler at least verifies the workspace EXISTS before writing rows keyed
  to it.
- Still no outbound requests anywhere in the tree: no http.Get, no http.Client.Do, no net.Dial. URL import
  remains deferred and an unreviewed SSRF surface is a blocker.
Do NOT run "make smoke" — the acceptance section runs it, it rewrites .env and runs "docker compose down -v"
on entry and in its exit trap, and a concurrent second run manufactures a phantom failure.
Report each finding as a concrete attack: what is sent, what comes back.`,
  },
], `${CTX_CORE}${CTX_RECIPES}

You own the internal/mockplane and internal/admin files this section touched, plus cmd/mocker/main.go if
the findings name it. Do not edit internal/recipes, internal/overrides, internal/authpreset or internal/gen
— those were gated already and a fix reaching into them is how a reviewed package silently changes.

${SIG_OVERRIDES}

${SIG_PRESET}`)

// ------------------------------------------------------------------ Accept --
// Serial: the acceptance test first (it is the phase criterion), then the docker
// check (which rewrites .env and tears the stack down, so nothing may run
// alongside it).
phase('Accept')

const acceptSpec = await agent(`${CTX_CORE}${CTX_RECIPES}${CTX_SPEC}${CTX_TEST}

YOUR TASK: prove the phase criterion on the REAL document and on the real stack, in Go tests.

YOU OWN, and nothing else:
  internal/server/p1c_test.go        — NEW: the end-to-end criterion through the whole stack
  internal/gen/acceptance_p1c_test.go — NEW: the document-wide invariants. It joins package gen_test, whose
    namespace ALREADY holds, from internal/gen/acceptance_test.go: acceptanceFixture, loadAcceptance,
    acceptanceTestConfig, acceptanceLogger, findOp, pickPrimaryVariant, isGenuine2xxSelector,
    samplePathParams, concretePath, fixedClock, schemaOracle, newSchemaOracle, conformanceFailure,
    decodeNumberPreserving, fakeAcceptanceSpecSource, fakeAcceptanceWorkspaceSource, acceptancePlane,
    acceptanceRequest — plus goldenCorpus and friends from the Engine's golden_p1b_test.go. REUSE them:
    loadAcceptance for the document, newSchemaOracle for the santhosh-tekuri validation, fixedClock for
    the clock, goldenCorpus for HARD RULE 6. Redeclaring any of them does not compile. Prefix every symbol
    you DO declare with p1c
You may READ anything. Do not modify any non-test file: if an assertion fails because the code is wrong,
that is a FINDING — report it in "deviations" with file and line, do not patch the code to make your test
pass. A test bent to fit a defect is worse than no test.

THE CRITERION, DESIGN §19 line 1111: "фронт логинится" — a frontend logs in. Concretely, and these are the
assertions:

1. END TO END, through internal/server's real dispatcher. WRITE YOUR OWN HARNESS in p1c_test.go, modelled
   on internal/server/p1a_test.go's buildStackWithSpecs (read it — it is package server_test, opens a temp
   store, migrates, builds auth/workspaces/specs, admin.New, mockplane.New and server.New, then wraps the
   dispatcher in httpx.Chain). You may NOT edit p1a_test.go, and its harness does NOT wire an overrides
   source into the plane — so a copy that omits that wiring cannot serve a single override and the whole
   criterion below is untestable. Wire it by calling plane.SetOverrides(overrides.NewRepo(db)) on the plane
   you build, before you serve anything through it — the same call stage 3 adds to cmd/mocker/main.go.
   p1c_test.go joins package server_test alongside p1a_test.go and server_test.go, which ALREADY declare:
   inlineSpec, widgetItem, buildStackWithSpecs, buildStack, testConfig, testLogger, testPassword,
   adminOrigin, do, jsonRequest, login, fakeSource, fakePlane, recordingHandler. REUSE testConfig,
   testLogger, do, jsonRequest and login(t, handler, name) (*http.Cookie, string) — do not write a second
   login/CSRF handshake that will drift from theirs — and prefix everything you DO declare with p1c
   (p1cInlineSpec, buildP1cStack). A redeclaration does not compile and you may not edit either file.
   Then: log in, create a workspace, import an inline OAS document whose
   login operation returns a token-shaped object, attach it, GET /api/workspaces/{id}/auth-preset, POST the
   proposal back, then hit the mock plane on the workspace host and assert on the response:
   - the token field is THREE base64url segments; the header decodes to JSON with an alg; the payload
     decodes to JSON; sub equals the workspace identity's id;
   - exp is an epoch value in SECONDS: exp - time.Now().Unix() is within (0, ttl+60], and exp has ten
     digits, not thirteen. Assert the digit count explicitly — that is the DESIGN §10 failure that produces
     an infinite refresh loop, and it is invisible to a "is it a number" check;
   - the expiry FIELD of the response (token_expires_at-shaped) is likewise seconds and in the future;
   - two requests one second apart both produce a decodable token (the clock moves; the mock must not
     freeze exp into the seed).
   Use an inline document, not ${SPEC_PATH}: this test must run in a fresh clone.

2. ON THE REAL DOCUMENT (skipping via internal/testspec when it is absent), through authpreset.Derive plus
   internal/gen — no HTTP needed:
   - the three auth paths are found and only those three (the four token-shaped traps are NOT bound);
   - POST /api/v1/auth/token binds jwt on token and refresh_token, and now on token_expires_at;
   - POST /api/v1/auth/userinfo binds identity on app_id;
   - APPLYING the whole proposal, every schema-bearing variant of all 130 operations still generates AND
     still validates against its own schema with the santhosh-tekuri oracle P1b already uses (P1b's number
     is 114 of 114 — report the number you actually measure, and if it is lower, name every operation that
     dropped out and why; that is a finding, not a fixture to update);
   - HARD RULE 6, and read this carefully because the obvious test does not test it: with NO recipes
     bound, every body must match internal/gen/testdata/p1b_body_hashes.json — the golden the Engine agent
     captured from the tree BEFORE it edited internal/gen. Two runs of the CURRENT generator agreeing with
     each other proves determinism within one build and would happily pass a change that shifted every
     body consistently, which is precisely the failure this rule exists for.
     CALL the enumeration helper the Engine already wrote — goldenCorpus(t, fx) in
     internal/gen/golden_p1b_test.go, package gen_test — and compare its result to the file. Do NOT
     re-implement the enumeration: the hash depends on the exact gen.Request shape (SeedList hashes
     Method, CanonicalPath, the sorted PathParams and Status), so a second hand-written corpus diverges
     and its mismatch is indistinguishable from the regression this golden exists to catch. Report the
     number of hashes compared. Do NOT regenerate the golden under any circumstances.
   - determinism WITH recipes: two runs differ ONLY in the time-derived fields (jwt segments, now values).
     Normalise those out and assert byte-identity on everything else.
   Print every count with t.Logf so the run produces real measurements rather than a pass/fail bit.

3. Report every SKIP the full suite prints and why. An acceptance test that skips itself in a normal
   "go test ./..." is the exact failure P1a shipped and a human caught by hand.

Finish with the four commands clean and paste the real output of the last one into "verified". Put the
numbers you measured into "measurements".`,
  { label: 'accept:spec', phase: 'Accept', model: 'sonnet', schema: ACCEPT_SCHEMA })

if (!acceptSpec) log('WARNING: accept:spec returned NOTHING — the phase criterion is unproven by that agent. The review section is told to verify it independently.')
logDeviations('Accept spec', [acceptSpec])

const acceptStack = await agent(`${CTX_CORE}${CTX_TEST}

YOUR TASK: prove this slice through docker, the way P0's post-mortem says is the only proof worth having —
"go test зелёный ≠ фаза готова; единственная проверка, которой стоит верить, — make smoke".

YOU OWN, and nothing else:
  scripts/smoke.sh
  README.md   — only if a documented endpoint list or a phase status is now wrong
  Makefile    — only if a new target is genuinely needed
Do not modify any Go file. A failing check is a FINDING for "deviations", not a reason to edit the code.

WHAT EXISTS: scripts/smoke.sh (353 lines) already brings up the compose stack, logs in with a cookie jar,
reads the csrf token from the login response and sends it as X-CSRF-Token on every unsafe call (its
http_json helper), creates a workspace, imports a small inline spec, attaches it and runs P1b's six checks
(import 201, attach 200, health carries spec+revision, the list route answers 200 with JSON of the right
shape, twice byte-identically, a detail card equal to its list row, HEAD empty). COMPOSE_ENV_FILES=/dev/null
is set deliberately — do not remove it. The inline spec it imports is self-contained on purpose: the real
acceptance document is gitignored and absent inside the container.

WHAT YOU ADD — extend the inline spec with a login-shaped operation (a POST returning an object with a
string "token" and an integer "token_expires_at"), then assert, over HTTP, through the real stack:
1. GET /api/workspaces/{id}/auth-preset returns a proposal naming that operation, and writes nothing (the
   revision is unchanged after the GET — check it via /__mocker/health).
2. POST /api/workspaces/{id}/auth-preset applies it and the revision increments by exactly one.
3. The mock plane's login route now answers a token with THREE dot-separated segments whose payload
   base64url-decodes to JSON containing sub. Decode it in the shell (base64 -d with the padding fixed up,
   then jq) — that is the whole point of the phase and a length check is not it.
4. token_expires_at is an integer in the FUTURE and has ten digits, not thirteen (seconds, not
   milliseconds — DESIGN §10).
5. PUT an override on some operation with a const recipe, and the mock returns that constant on the next
   request; DELETE it and the generated value comes back. Both through the admin API with the csrf token.
6. route_off on an operation answers 404.
Keep every new check in the existing style (the pass/fail counter, the same helpers), keep the script
idempotent and keep the exit trap intact.

Run it: "docker compose version" first — if docker is absent, set smoke to "skipped-no-docker" and say so;
that is not a failure. If it IS available, run "make smoke" and report the real output. A failing check is
the most important thing you can report today: report it with the exact curl and the exact response.`,
  { label: 'accept:stack', phase: 'Accept', model: 'sonnet', schema: ACCEPT_SCHEMA })

if (!acceptStack) log('WARNING: accept:stack returned NOTHING — make smoke was not observed. The review section is told to run it.')
logDeviations('Accept stack', [acceptStack])

// ------------------------------------------------------------------ Review --
phase('Review')

const acceptance = [acceptSpec, acceptStack].filter(Boolean)
const touched = [kernel, engine, gate1.fix, ...storeOK, gate2.fix, ...serveOK, gate3.fix, ...acceptance]
  .filter(Boolean)
  .flatMap((r) => r.files || [])
  .join(', ')

const criterionState = [
  acceptSpec
    ? `The acceptance agent reports passed=${acceptSpec.passed}. Its measurements: ${(acceptSpec.measurements || []).join(' | ') || '(none reported)'}`
    : 'The acceptance agent DIED without reporting. Treat the phase criterion as unproven and verify it yourself.',
  acceptStack
    ? `The docker stack check reports passed=${acceptStack.passed}, smoke=${acceptStack.smoke || '(not reported)'}. ${acceptStack.smoke === 'failed' ? 'A FAILING make smoke outranks every code-level finding — explain it first.' : acceptStack.smoke === 'skipped-no-docker' ? 'Docker was ABSENT, so the end-to-end criterion is unproven rather than broken — do not read this as a failure, and do not read it as a pass either.' : ''}`
    : 'The docker stack check DIED without reporting: make smoke was not observed to pass. Run it yourself if docker is available.',
].join('\n')

const reviewCtx = `Repo root is your CWD. Do not modify any file — you are auditing, not fixing. Running the
test suite is expected.
TO SEE THE DIFF, SCOPED TO THIS SLICE — most files it produced are NEW and plain "git diff HEAD" does not
show untracked files at all:
    git add -N . ; git diff HEAD -- ${P1C_PATHS}
The ";" and the "." are both deliberate: with "&&" and a path list, one path nobody created makes
"git add -N" exit 128 having added nothing, and you would review an EMPTY diff believing the phase produced
nothing. Intent-to-add stages nothing for commit. Your co-reviewer runs the same command concurrently: on
".git/index.lock: File exists", wait a second and retry.
Do NOT widen it: .claude holds ~137 KB of untracked workflow script.
HEAD is P1b — committed, reviewed and green — so this diff is exactly what this slice produced.
DESIGN.md is Russian and 87 KB — never read it whole. The index: §6 136-174, §7 175-245, §8 246-296,
§9 297-411, §10 412-471, §12 509-556, §13 557-820, §14 821-916, §15 917-982, §18 1088-1105, §19 1106-1128.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it, query it with jq only.

P1c slice 1 of mocker was just written: internal/recipes (the engine), its integration into internal/gen,
internal/overrides (the op_overrides row), internal/authpreset (the derivation), the serving path that
applies overrides and the admin endpoints that expose them.
Files written or changed by this run: ${touched}
${criterionState}
${INTENDED}
Report ONLY defects you can point at with a file and a line. No praise, no style nits that do not change
behaviour. Empty findings array if your vector is clean.`

const reviews = await parallel([
  () => agent(`${reviewCtx}

YOUR VECTOR: correctness across the whole slice, and especially what falls BETWEEN the sections the three
gates each looked at separately.
- Verify the phase criterion YOURSELF rather than trusting the acceptance agent's report: write or run a
  test that mints a token through the full serving path and decodes it. sub == identity.id, exp in epoch
  SECONDS (ten digits), the expiry field in the future. That is the phase.
- HARD RULE 6, the regression that would be worst to ship: with no override rows, is the mock's output
  still byte-identical to P1b's? The guard is internal/gen/testdata/p1b_body_hashes.json, captured from
  the pre-change tree. Check that it EXISTS, that it holds ONE ENTRY PER RESPONSE VARIANT — 419 on this
  document, counting every declared selector; a count near 130 means the golden was silently narrowed to
  one variant per operation and guards a quarter of what it should — that a test
  actually reads it in a normal run (not only under an env var), and that it was not regenerated mid-run —
  a golden rewritten after the change proves nothing, and "git log" cannot tell you because this run
  commits nothing. Then read the internal/gen diff and ask whether it could shift a seed input, a data
  path or an iteration order. P1b's numbers were 130/130 byte-identical and 114/114 schema-valid —
  reproduce them, do not quote them.
- The seam between the override row and the route: prove a real override on a parameterised path actually
  fires end to end, with a non-empty basePath. A key mismatch there passes every unit test and misses every
  override in production.
- Round-trip: PUT an override carrying when[], schemaPatch and a fail_directive, GET it back, PUT again.
  Anything dropped silently deletes an operator's work.
- The recipes priority chain end to end: a recipe beats an example, a declining recipe changes nothing, a
  coercion refusal leaves a schema-valid value.
- errors compared with == instead of errors.Is; nil map writes; unchecked type assertions on document or
  override data (both are user input, and the mock plane is unauthenticated).
- Tests that assert nothing, are skipped in the normal run, or would still pass with the implementation
  deleted. Report every SKIP the suite prints and why.
- go test ./... -race -count=1, and a concurrent hammer on one runtime if the mockplane tests support it.
If docker is available ("docker compose version"), run "make smoke" and report it if it fails. If docker is
absent, say so and move on — it is not a finding.`,
    { label: 'review:correctness', phase: 'Review', model: 'sonnet', schema: REVIEW_SCHEMA }),

  () => agent(`${reviewCtx}

YOUR VECTOR: security, across the whole slice. Two new attack surfaces open here at once: operator-supplied
override rows reach the wire on the OPEN mock plane, and the product now mints tokens.
DESIGN §15 (917-982) says the mock plane is reachable by everyone in the contour and the admin password is
SHARED, so "an admin did it" is a low bar; §18 (1088-1105) forbids rate limiting the mock plane, so the
per-request cost is the defence.
- The signing key (domain.AuthSettings.SigningKey, random per workspace): trace every path it can reach —
  an admin response body, the preset preview, a log line, an error message, the traffic buffer, a token's
  own payload. It must never leave the process. The preview returns a SAMPLE TOKEN by design; the key
  travelling with it is a blocker.
- Token minting: is the claim template able to overwrite exp or alg? Is alg taken from operator input
  anywhere (an "alg" claim in a template that reaches the header)? Can a recipe make the mock sign with an
  empty key silently rather than refusing?
- Response splitting through a pinned header name/value or a pinned media type reaching WriteHeader with
  CR/LF. Find the path that bypasses setSafeHeader, or confirm there is none.
- Panic reachability from an UNAUTHENTICATED request through override data: a bare type assertion on a
  decoded blob, a base64 decode, a pattern matcher indexing a slice, a recipe post-pass on a
  deeply-nested body. Each one is a 500 that denies that workspace's mock.
- Cost: the largest response a single crafted override row can produce, and what bounds it. A pinned body
  bypasses MaxBytes unless someone added a check; a listSize recipe multiplies at every nesting level
  (DESIGN §9 "Границы"). State the worst case in bytes, measured.
- Memory: can anything per-REQUEST accumulate — a compiled Set cached per request path, a memo keyed by
  query string, a map on the shared runtime? That is an unauthenticated leak.
- The admin surface: session and CSRF on PUT and DELETE, not just POST (read internal/admin/security.go).
  No new route outside /api/. No new bypass in the chain.
- go list -deps ./cmd/mocker | grep santhosh   — a non-empty result is a blocker (the JSON Schema oracle is
  test-only). go mod verify clean. Still no http.Get / http.Client.Do / net.Dial anywhere in the tree.
Do NOT run "make smoke" — the correctness reviewer may run it, you two run concurrently, and it rewrites
.env and runs "docker compose down -v" on entry and in its exit trap, so a second run manufactures a
phantom failure.
Report each finding as a concrete attack: what the attacker sends, what they get. File and line required.`,
    { label: 'review:security', phase: 'Review', model: 'opus', schema: REVIEW_SCHEMA }),
])

const findings = reviews.filter(Boolean).flatMap((r) => r.findings || [])
for (const [i, r] of reviews.entries()) {
  if ((r?.findings || []).length >= 40) log(`WARNING: ${['review:correctness', 'review:security'][i]} returned the schema maximum of 40 findings — probably TRUNCATED.`)
  if (!r) log(`WARNING: ${['review:correctness', 'review:security'][i]} returned nothing — that vector did NOT run and the slice is unreviewed along it.`)
}
const minors = findings.filter((f) => f.severity === 'minor')
for (const m of minors) log(`minor: ${m.file}:${m.line} — ${m.summary}`)

const gateMajors = [...gate1.findings, ...gate2.findings, ...gate3.findings].filter((f) => f.severity === 'major')
const gateSurvivors = [...gate1.unresolved, ...gate2.unresolved, ...gate3.unresolved]
// Deduped: a gate blocker that survived its re-review and was then independently
// re-raised by the whole-slice review would otherwise reach the fix agent twice,
// as two numbered items describing one defect.
const seenFinding = new Set()
const actionable = [...gateSurvivors, ...gateMajors, ...findings.filter((f) => f.severity === 'blocker' || f.severity === 'major')]
  .filter((f) => {
    const key = `${f.file}:${f.line}:${f.summary}`
    if (seenFinding.has(key)) return false
    seenFinding.add(key)
    return true
  })
let outstanding = actionable
log(`Carrying ${gateMajors.length} unfixed gate majors and ${gateSurvivors.length} gate findings that survived a re-review into the fix list.`)
if (actionable.length > 20) log(`NOTE: ${actionable.length} actionable findings — a large fix list; nothing is dropped, but the fix agent gets all of them.`)
log(`Review: ${findings.length} findings — ${actionable.length} actionable, ${minors.length} minor (logged, not fixed)`)

// Every dead reviewer, collected. Without this the run's return value cannot
// distinguish "two vectors audited this and found nothing" from "both vectors
// died and nobody looked" — the individual log() warnings scroll away, and an
// empty findings list is the same value in both cases.
const unreviewed = [
  ...(gate1.dead || []), ...(gate2.dead || []), ...(gate3.dead || []),
  ...['review:correctness', 'review:security'].filter((_, i) => !reviews[i]),
]
const criterionProven = Boolean(acceptSpec && acceptSpec.passed)
if (unreviewed.length) log(`WARNING: ${unreviewed.length} review vector(s) NEVER RAN: ${unreviewed.join(', ')}. Whatever they cover is unaudited — an empty findings list from this run is not evidence along those vectors.`)
if (!criterionProven) log('WARNING: the phase criterion was NOT observed to pass by the acceptance agent (it died, or reported passed=false). Do not read a green build as a met criterion.')

if (outstanding.length === 0) {
  log('Nothing actionable: skipping Fix and Verify.')
  return {
    phase: 'P1c-1',
    acceptance: acceptance.map((a) => ({ files: a.files, passed: a.passed, measurements: a.measurements })),
    findings: findings.length,
    actionable: 0,
    rounds: [],
    stillOpen: [],
    unreviewed,
    criterionProven,
  }
}

// --------------------------------------------------------------- Fix/Verify --
// Fix is otherwise terminal: a semantically wrong but compiling fix survives
// gofmt, build, vet and the tests, and nobody is watching this run.
//
// DELIBERATE DEVIATION from the section-gate rule, logged rather than left to be
// noticed: Fix mutates artifacts and is checked by ONE opus verifier, not by two
// reviewers along different vectors. The verifier's job here is narrower than a
// review — for each numbered finding, is it actually fixed — and it re-runs the
// full suite plus make smoke, which is the check a second reviewer would mostly
// duplicate. This is the shape P0, P1a and P1b all shipped.
const rounds = []
let lastOutput = ''
for (let round = 1; round <= 2 && outstanding.length > 0; round++) {
  phase('Fix')
  const list_ = outstanding
    .map((f, i) => `${i + 1}. [${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`)
    .join('\n')

  const fixed = await agent(`${CTX_CORE}${CTX_RECIPES}${INTENDED}

HARD RULE 1 DOES NOT APPLY TO YOU: you run alone and own every file this slice touched. HARD RULES 2 (no new
runtime dependency), 3 (no migration), 4 (never read the big files whole), 5 (no nested db.Write) and 6 (no
recipes must mean byte-identical output) STILL HOLD.

Round ${round}. Two reviewers audited the whole slice. Apply the findings below, changing as little as
possible — every edit must map to a numbered finding.
${lastOutput ? `\nThe previous round ended NOT green. This was the failing output:\n${lastOutput}\n` : ''}
${list_}

RULES
- Verify each finding against the code FIRST. Reviewers false-positive. If a finding is wrong, do NOT
  "fix" it — record it in "deviations" with the evidence that it is wrong.
- A finding needing a design change rather than a code change goes to "todo", not into a hasty redesign.
- Add or extend a test for every real defect you fix, so it cannot come back silently.
- Never weaken, skip or delete a test to reach green. In particular: never make an acceptance test skip,
  never relax a schema-conformance assertion into a percentage, and never drop the byte-identity assertion.
- Finish with all four clean and paste the real output of the last one into "verified":
    test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
  Then, only if "docker compose version" succeeds, run "make smoke" and paste its result too.`,
    { label: `fix:round${round}`, phase: 'Fix', model: 'sonnet', schema: SCHEMA })

  if (fixed) logDeviations(`Fix round ${round}`, [fixed])
  // A dead fix agent must not be silent: Verify is about to be told "a fix
  // agent just claimed to have addressed the findings", and if none did, its
  // per-finding verdicts are about unchanged code.
  else log(`Fix round ${round}: the fix agent returned NOTHING. Its work may be on disk, but nothing was reported — the verifier below is checking a tree that may be untouched, and every finding it calls "still open" should be read that way.`)

  phase('Verify')
  const verdict = await agent(`Repo root is your CWD. Do not modify any file.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it; query it with jq only.
DESIGN.md is 87 KB — never read it whole (§9 297-411, §10 412-471, §13 557-820, §14 821-916, §19 1106-1128).
${INTENDED}

A fix agent just claimed to have addressed the findings below. Its own report is self-assessed, so check it.
Three things, in this order:

1. Run, and report the REAL output:
     test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
   "green" is true only if all FOUR are clean. Also report every test that SKIPPED and why — a phase
   criterion that only runs under an environment variable is not a criterion.
2. Run "docker compose version". If it succeeds, run "make smoke" and set "smoke" to passed or failed; if
   docker is absent, set it to "skipped-no-docker". A skipped smoke does NOT make green false; a FAILING
   smoke means the phase criterion or an earlier phase's check broke and green must be false.
3. For EACH numbered finding below, decide: fixed, credibly rebutted, or still open. A fix that compiles is
   not a fix — check the semantics. Specifically distrust: a byte-identity assertion "fixed" by deleting
   it; a schema-conformance failure "fixed" by excluding the operation; an override key mismatch "fixed" in
   the test rather than in the code; a token expiry "fixed" to milliseconds because a test wanted more
   digits; a deadlock "fixed" by moving the bump out of the transaction; a race silenced by a mutex on the
   hot path; a panic "fixed" by a recover; the test-only JSON Schema dependency leaking into ./cmd/mocker.
   ONE MORE, and check it explicitly because it is invisible in a passing test run: a HARD RULE 6 hash
   mismatch "fixed" by regenerating internal/gen/testdata/p1b_body_hashes.json. That golden is write-once,
   captured before internal/gen was edited, and this run commits nothing so git cannot tell you. Run
       ls --time-style=+%s -l internal/gen/testdata/p1b_body_hashes.json internal/gen/recipes.go
   The golden is captured at the very start of the Engine phase and internal/gen/recipes.go is written
   after it, so the golden must be OLDER than recipes.go. Newer means it was regenerated after the edits,
   the regression guard is now vacuous, and green must be false.
   Do NOT compare it against seed.go, compose.go or names.go: this phase is forbidden to touch those, they
   keep their pre-run timestamps, and the golden being newer than them is expected and means nothing.

${outstanding.map((f, i) => `${i + 1}. [${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}`).join('\n')}

List in "unresolved" ONLY the numbers that are still open, with why. The list is renumbered from 1 every
round: use the numbers as given above.`,
    { label: `verify:round${round}`, phase: 'Verify', model: 'opus', schema: VERIFY_SCHEMA })

  if (!verdict) {
    log(`Verify round ${round} returned nothing. Treating the round as unverified and stopping the loop rather than declaring a pass nobody checked.`)
    rounds.push({ round, green: null, smoke: null, unresolved: 'verifier died' })
    break
  }

  rounds.push({ round, green: verdict.green, smoke: verdict.smoke, unresolved: verdict.unresolved })
  lastOutput = verdict.green ? '' : verdict.output || ''
  log(`Verify round ${round}: green=${verdict.green}, smoke=${verdict.smoke}, still open=${(verdict.unresolved || []).length}`)

  const stillOpen = new Set((verdict.unresolved || []).map((u) => u.n))
  const kept = outstanding.filter((_, i) => stillOpen.has(i + 1))
  if (kept.length < stillOpen.size) {
    log('Verifier numbering drifted from the list it was given; carrying ALL findings into the next round rather than trusting a partial mapping.')
    outstanding = actionable
  } else if (!verdict.green && kept.length === 0) {
    log('Not green but no finding named as open — the failure is outside the finding list. Carrying the full list into one more round with the failing output attached.')
    outstanding = actionable
  } else {
    outstanding = kept
  }

  if (verdict.green && outstanding.length === 0) {
    log(`Green after round ${round}.`)
    break
  }
}

return {
  phase: 'P1c-1',
  acceptance: acceptance.map((a) => ({ files: a.files, passed: a.passed, measurements: a.measurements })),
  findings: findings.length,
  actionable: actionable.length,
  rounds,
  stillOpen: outstanding.map((f) => `${f.file}:${f.line} — ${f.summary}`),
  unreviewed,
  criterionProven,
}

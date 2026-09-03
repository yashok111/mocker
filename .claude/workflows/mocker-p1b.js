export const meta = {
  name: 'mocker-p1b',
  description: 'P1b of mocker (Go): the response generator — layered seed, list contract, schema composition, response serving',
  whenToUse: 'Run once, from the repo root, after P1a is green. Second slice of phase P1 (DESIGN.md §9, including "Тело, тип и статус", plus the serving rules of §8).',
  phases: [
    { title: 'Kernel', detail: 'internal/gen: seed layers, schema walk, budgets, clock injection — the contract owner', model: 'sonnet' },
    { title: 'Values', detail: 'composition (allOf/oneOf/discriminator) and value sources (examples, formats, field names), parallel', model: 'sonnet' },
    { title: 'Gate 1', detail: 'review determinism and composition before the list contract and the serving path are built on them', model: 'sonnet' },
    { title: 'List', detail: 'the list contract: pagination, stable total, item identity, list row == detail card', model: 'sonnet' },
    { title: 'Serve', detail: 'the per-workspace runtime (doc+resolver+generator) and the response path that replaces the 501', model: 'sonnet' },
    { title: 'Gate 2', detail: 'review the serving path: hot-path correctness and the open mock plane as an attack surface', model: 'sonnet' },
    { title: 'Accept', detail: 'acceptance against the real 130-operation spec with a JSON-Schema oracle, then make smoke', model: 'sonnet' },
    // A heterogeneous phase, so this entry cannot mirror both calls. It names the
    // CHEAP tier deliberately: meta.phases[].model is only ever a fallback, and a
    // reviewer added later without an explicit model should default to sonnet, not
    // to the opus that only review:security is meant to spend.
    { title: 'Review', detail: 'correctness (sonnet) + security (opus)', model: 'sonnet' },
    { title: 'Fix', detail: 'apply blocker/major findings', model: 'sonnet' },
    { title: 'Verify', detail: 're-run the suite and check each finding was really fixed', model: 'opus' },
  ],
}

// `args` is documented as a script global that is undefined when not passed, but
// a bare reference would be a ReferenceError if that ever stopped holding, and
// this line runs before the first agent — a total loss for one missing guard.
const SPEC_PATH = (typeof args !== 'undefined' && args && args.specPath) || 'output.swagger.json'

// ---------------------------------------------------------------------------
// Decisions taken before launch, each with the clause or the measurement that
// forced it. Logged so the run's transcript explains itself without this file.
// ---------------------------------------------------------------------------
log(`Acceptance spec: ${SPEC_PATH} (gitignored, 347 KB; every test that reads it skips itself when it is absent, and NO agent may read or cat it — jq only).`)

log('SCOPE — P1b is the RESPONSE GENERATOR and nothing else. HANDOFF.md tagged three import gaps as "P1b" too (Swagger 2.0 conversion, YAML input, URL import); they move to their own later slice. Reason, stated so it is a decision and not a silent cut: they are orthogonal to DESIGN §19\'s P1 criterion ("фронт логинится, проходит read-only сценарий и видит согласованные список и карточку"), which runs entirely through the generator, and folding them in would put this script past 25 agents — the guide\'s rule 4 says split at phase boundaries so each run completes and caches instead of being interrupted. The URL importer additionally carries the whole SSRF surface (§15) and deserves a security review that is not competing for attention with 130 operations of generated JSON.')

log('DEFERRED to P1c, not missing: the recipes engine (const/enum/faker/template/sequence/copy/identity/jwt/now — DESIGN §9 "Рецепты"), the auth preset (§10), op_overrides and pinned bodies (§13), force-status from the session layer (§12), and the "when" conditions. DESIGN §19 lists them inside P1 and that stands — this is a slice boundary inside P1, not a scope cut. The generator is written so a recipe layer sits ON TOP of it: value resolution goes through ONE choke point (see the value-source priority below) whose first branch is "a recipe supplied this value", left as a documented hook that nothing calls yet.')

log('DECISION — item identity, and why it is not DESIGN §9\'s literal wording. §9 requires two things at once: an item at global index offset+i must be reproducible on any page, and "GET /{id} того же семейства маршрутов генерируется сидом элемента с этим id". Deriving the item seed from the INDEX satisfies the first and breaks the second — the detail route only ever sees an id, and cannot invert an index out of it unless ids are dense integers (this document has 97 int64 ids but also 10 uuid ones). So the item seed is derived from the item IDENTITY: idForIndex(i) = f(seedList, i) fills the id field of the row at global index i, and EVERY item — list row and detail card alike — has its remaining fields generated from seedItemByID(id). Both §9 properties hold, for uuid ids as well as integers, and for an id that is not in the current page window.')

log('DECISION — no faker dependency; deterministic generators are hand-written over hash-derived state. §9 pins the seed contract as explicit layers (hash64(seed, ...), hash64(seedList, i)); a library RNG makes a value depend on how many draws happened before it, so the seed of an item would depend on call order — precisely the bug the layered seed exists to prevent (§9: "иначе списки рассыпаются"). §9 also demands a fully synchronous walk so no generator state is shared between concurrent requests. Cost, stated: the field-name realism corpus (§9 "Реалистичность") is ours to write.')

log('DECISION — ONE new dependency, TEST-ONLY: github.com/santhosh-tekuri/jsonschema/v6 as the acceptance oracle. DESIGN §8 already names this exact library for optional request validation, so this is not a new supply-chain decision, only an earlier one. It is the only way to prove the phase\'s real claim — "a generated body satisfies the schema the spec declares" — across 114 operations without hand-writing a validator that would itself need reviewing. HARD CONSTRAINT: imported from _test.go files ONLY; the shipped binary must not link it, and the acceptance agent proves that with "go list -deps ./cmd/mocker | grep santhosh" returning nothing.')

log('DECISION — the generator takes an injected clock (Options.Now). §9 splits time in two: ordinary fields derive from the seed, deadline-shaped fields (exp, *_expires_at, expires_in, not_after) derive from the REAL now, "фиксированный exp делает инсталляцию неработающей на следующий день". Without injection the determinism test cannot assert byte equality at all, and a test that excludes the time fields would prove nothing about the ones that matter.')

log('DECISION — internal/gen is a LEAF: it imports internal/openapi and the stdlib, nothing else. Not domain (settings are passed as primitives in Options), not router (the sibling-list family is computed by the caller and passed in Request.ListFamily), not specs. Import graph for this phase: gen -> openapi | specs -> openapi, router, gen | mockplane -> router, gen (NOT specs) | admin -> specs. An earlier phase already put SpecSource in mockplane precisely to keep that last arrow absent; extending that interface is how the new data reaches the plane.')

log('DECISION — the envelope (settings.envelope, DESIGN §13) wraps in the SERVING layer, not in the generator. The generated body must be the thing the schema describes, or the acceptance oracle validates the wrapper against the payload schema and every operation fails for the wrong reason.')

log('The 501 from P1a dies this phase. A matched route answers a generated body; an operation whose row carries parse_error answers an EMPTY 200, which is DESIGN §7\'s invariant for an unparsed operation and is now finally implementable (P1a could not: an empty 200 there would have been indistinguishable from a working mock).')

log('HARD RULE 3 — the SQLite schema is FROZEN. No migration, no new column. Declared response headers (§9 "Объявленные заголовки ответа проставляются") are therefore addressed at generation time by building a pointer from data that IS stored: operations.pointer + "/responses/" + escaped(selector) + "/headers", resolved lazily. Anything that wants a new column is a finding for the next phase, not an edit.')

log('THREE THINGS §19 PUTS IN P2 ARE DONE HERE, deliberately, so nobody reads them as scope creep: the deterministic oneOf branch (§19 lists "oneOf по хешу" under P2, but §9 defines the rule as part of composition and a generator that cannot answer a oneOf schema answers nothing for that operation); settings.DelayMs (§19 lists "задержки" under P2, but §6 names задержка as part of response assembly and the field already exists in domain.Settings with a default that nothing honours); and the non-JSON placeholder body (§19 lists "не-JSON и base64" under P2, but §9 requires the declared media type to be honoured and the acceptance document carries a text/csv response — answering it with JSON is a wrong Content-Type, not a deferral). The P2 items NOT pulled in: base64 pinned bodies, and the session layer\'s per-request delay and fail_next, which need §12.')

log('DEFERRED, and it has to be said out loud because §9 asks for it and nothing here delivers it: "невыполнимая композиция — операция помечается ошибкой схемы и это видно в UI". P1b returns ErrUnsatisfiable and logs it, but MARKING the operation would need a column in operations, and HARD RULE 3 freezes the schema this phase. The mark lands with the "Спеки" screen work; until then the failure is visible in the server log and in the generated body being absent, not in the UI.')

log('The Serve phase is SERIAL, not parallel — a pre-flight correction. Its two agents write different files of the SAME Go package and each references the other\'s symbol (respond.go takes the *runtime that runtime.go declares; routes.go calls the serveGenerated that respond.go defines). Run concurrently, neither could reach its own "go build ./... clean" exit criterion until the other landed, and the predictable escape — each declaring the missing symbol itself — is a duplicate declaration the moment the peer lands. Every earlier parallel pair in this project was in different packages. Stage 1 keeps the tree green by moving P1a\'s 501 into respond_stub.go and widening routes_test.go\'s fakeSpecSource (which implements only Routes and would stop compiling the moment SpecSource grows); stage 2 deletes the stub file and owns the two test files that assert the 501 — routes_test.go x11 and server/p1a_test.go x1, assigned to nobody in the first draft, which made the phase impossible to finish green. plane_test.go has NO 501: its TestServeHTTP_NotImplementedYet asserts a 404 for the P1c endpoint POST /__mocker/state and must not be touched — a measurement I got wrong twice before checking it.')

log('Agent count: 16 typical, 24 worst case (both gate fixes, all four gate re-reviews, two fix/verify rounds). Above the medium guideline of 15, deliberately and with the same justification as P1a: every agent past the eight producers is error-correction, and this phase\'s defects (a seed that is not stable, a body that does not satisfy its schema) are invisible to the compiler.')

// ---------------------------------------------------------------------------
// Signature blocks. Pasted verbatim into BOTH the agent that writes the package
// AND every agent that calls it: parallel agents cannot negotiate at runtime,
// and a swapped pair of string parameters compiles silently.
// ---------------------------------------------------------------------------

const SIG_GEN = `  // package gen  (internal/gen) — A LEAF: imports internal/openapi and the stdlib ONLY.
  // Written by the Kernel agent. Everything else in this phase calls it.

  // Options is everything the generator needs that is NOT per-request. It is
  // primitives, not domain.Settings, so gen stays a leaf.
  //
  // ZERO VALUES ARE DEFAULTS, exactly as openapi.NewResolver treats a
  // non-positive budget: a caller that leaves a field at 0 gets the package
  // default, never a budget of zero. This matters because nothing in
  // config.Config or domain.Settings supplies MaxDepth at all — the caller
  // has nothing to fill it from, and a literal 0 would omit every optional
  // nested property in the whole document.
  type Options struct {
      Seed     int64            // domain.Settings.Seed; 0 is a legal seed, not a default
      ListSize int              // domain.Settings.ListSize; <=0 -> DefaultListSize
      NullRate float64          // domain.Settings.NullRate; 0 means "never null", which IS the default
      MaxBytes int64            // config.MaxResponse; <=0 -> DefaultMaxBytes. Generation stops growing the value past it
      MaxDepth int              // <=0 -> DefaultMaxDepth. Recursion budget for self-referencing schemas
      Now      func() time.Time // injected clock; nil means time.Now
  }
  const ( DefaultListSize = ...; DefaultMaxDepth = ...; DefaultMaxBytes = ... ) // you pick the numbers and justify them

  // Request is what the generator knows about the call being answered.
  type Request struct {
      Method        string            // upper case
      CanonicalPath string            // RELATIVE, {param} segments already replaced by {} — router.Route.CanonicalPath
      PathParams    map[string]string // by parameter name, as captured by router.Match
      Query         url.Values
      Status        int               // the HTTP status actually being sent
      // ListFamily is the canonical path of the sibling LIST route when this
      // request is a detail route (".../{}" whose prefix is itself a route),
      // otherwise "". The caller computes it — gen must not import router.
      ListFamily string
  }

  // ResponseVariant is one row of operation_responses joined with the two
  // fields of its operation the serving path needs. specs.Repo produces these;
  // mockplane consumes them. Declared HERE so mockplane never imports specs.
  type ResponseVariant struct {
      OpRowID    int64
      Selector   string // "200" | "2XX" | "default", exactly as in the spec
      HTTPStatus int    // what is actually sent: 2XX -> 200, default -> 200
      IsDefault  bool
      MediaType  string // "" when the response declares no content
      SchemaPtr  string // JSON pointer to the RESOLVED schema node, "" when there is none
      OpPointer  string // operations.pointer, e.g. "#/paths/~1api~1v1~1x/get"
      Degraded   bool   // operations.parse_error != nil -> answer an empty 200 (DESIGN §7)
  }

  type Generator struct{ /* unexported */ }

  // New builds a generator over one document. A Generator is immutable after
  // construction and safe for concurrent use: every request-scoped value lives
  // on the stack, and the openapi.Resolver is already concurrency-safe.
  func New(res *openapi.Resolver, opts Options) *Generator

  // Body generates the response body for one request. It takes the whole
  // variant, not just v.SchemaPtr, because DESIGN §9's value-source priority
  // starts ABOVE the schema: "example/examples (media type -> named -> схема
  // -> const -> default)", and the media-type-level example lives on the
  // content object — v.OpPointer + "/responses/" + escaped(v.Selector) +
  // "/content/" + escaped(v.MediaType) — which a bare schema pointer cannot
  // reach. Passing only the pointer would silently drop the first level of
  // the priority order.
  // Returns (nil, nil) — not an error — when v.SchemaPtr is "" (no declared
  // content). That covers MOST 204s but NOT all: measured, 5 of this document's
  // 20 chosen-204 variants declare application/json content with a schema
  // (POST /api/v1/journal/accounts/link/{tokenId}, PUT .../leave, PUT and
  // DELETE .../member/{userId}, PUT .../managers/{userId}). That is a spec bug
  // and it is real, so Body WILL return a body for them — the 204/205
  // suppression in the serving layer is what guarantees the wire stays empty.
  // Do not "simplify away" either half on the strength of the other.
  // It never returns a body larger than Options.MaxBytes.
  func (g *Generator) Body(v ResponseVariant, req Request) ([]byte, error)

  // Headers generates the response headers the spec declares for this variant.
  // HOW TO REACH THEM, and this is not optional detail: resolve the RESPONSE
  // OBJECT first —
  //     node, err := res.Resolve(v.OpPointer + "/responses/" + escaped(v.Selector))
  // — and read ["headers"] off the RESOLVED node. Resolve chases $refs; plain
  // pointer navigation into ".../headers" does NOT (openapi/resolver.go says
  // so explicitly of its lookup step). Measured on the acceptance document:
  // 221 of 419 responses are $ref'd, and BOTH of the document's header
  // declarations sit in components/responses, reachable only through a $ref.
  // Navigating from the referring site finds nothing and, since absent headers
  // are not an error, §9's "объявленные заголовки ответа проставляются" would
  // be silently unimplemented with every test still green.
  // Absent or unresolvable headers are not an error: the map comes back empty.
  func (g *Generator) Headers(v ResponseVariant, req Request) map[string]string

  // Seed layers (DESIGN §9). Exported so tests can assert them directly.
  func SeedList(opts Options, req Request) uint64
  func SeedItemByID(seedList uint64, id string) uint64
  func SeedScalar(seed uint64, dataPath string) uint64
  func IDForIndex(seedList uint64, i int, idSchema any) (any, string) // typed id, and its canonical string form

  // Errors. NONE of them is fatal to a request: the serving path logs and
  // answers something honest rather than 500ing on a bad schema.
  var (
      ErrUnsatisfiable = errors.New("gen: schema constraints cannot be satisfied") // allOf with minimum > maximum
      ErrNoSchema      = errors.New("gen: schema pointer resolves to nothing")
  )`

const SIG_OPENAPI = `  // package openapi  (internal/openapi) — WRITTEN AND GREEN. Do not modify it.
  func Load(raw []byte) (*Document, *Report, error)   // Load is idempotent on already-normalized input
  func (d *Document) Root() map[string]any
  func (d *Document) Normalized() []byte
  func (d *Document) BasePath() (string, BasePathOrigin)
  const DefaultRefBudget = 32
  func NewResolver(d *Document, budget int) *Resolver  // safe for concurrent use
  func (r *Resolver) Resolve(pointer string) (any, error)              // "#/components/schemas/X"
  func (r *Resolver) ResolveNode(node any) (any, error)                // follows a {"$ref": ...} node
  func (r *Resolver) ResolveNodePointer(node any) (pointer string, resolved any, err error)
  var ErrBudgetExhausted, ErrCycle, ErrPointerNotFound error           // never fatal to an import`

const SIG_SERVE = `  // The seam between the two Serve agents. BOTH signatures are fixed here
  // up front: stage 1 has to call a symbol stage 2 has not written yet, and
  // stage 2 has to accept a type stage 1 named without seeing this file again.

  // internal/mockplane/runtime.go — written by serve:runtime
  type runtime struct {
      table    *router.Table
      gen      *gen.Generator
      variants map[int64][]gen.ResponseVariant // keyed by router.Route.OpRowID
      settings domain.Settings                 // the workspace's, read fresh at build time
  }
  func (p *Plane) runtimeFor(ctx context.Context, ws *workspaces.Workspace) (*runtime, error)

  // internal/mockplane/respond.go — written by serve:respond
  func (p *Plane) serveGenerated(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, m *router.Match)

  // internal/router — the ONE addition this phase makes to it (serve:runtime writes it)
  func (t *Table) HasCanonical(method, canonicalPath string) bool

  // internal/specs — the TWO additions this phase makes to it (serve:runtime writes them)
  func (r *Repo) Normalized(ctx context.Context, specID int64) ([]byte, error)
  func (r *Repo) Variants(ctx context.Context, specID int64) (map[int64][]gen.ResponseVariant, error)

  // internal/mockplane — SpecSource grows from one method to three. cmd/mocker
  // passes *specs.Repo, which must still satisfy it.
  type SpecSource interface {
      Routes(ctx context.Context, specID int64) ([]router.Route, error)
      Normalized(ctx context.Context, specID int64) ([]byte, error)
      Variants(ctx context.Context, specID int64) (map[int64][]gen.ResponseVariant, error)
  }`

// ---------------------------------------------------------------------------
// Context blocks. Each agent gets ONLY the blocks its own task needs — an
// inlined blob is paid once per agent, so nothing shared goes in that a given
// agent will not read.
// ---------------------------------------------------------------------------

const CTX_CORE = `You are building phase P1b of "mocker", a self-hosted mock-backend service written in Go.

YOUR CWD IS THE REPO ROOT. Use repo-relative paths everywhere; never absolute ones.
Module path: git.sumka.site/yakov/mocker. Go 1.26.

P1b = THE RESPONSE GENERATOR. A request that matches a route stops answering 501 and starts answering
data that satisfies the schema the spec declares.

NOT P1b — do not build it, and do not report it as missing: the recipes engine and its UI (§9 "Рецепты"),
the auth preset, jwt/identity recipes (§10), op_overrides and pinned bodies, force-status and the session
layer (§12), stateful resources (§11), scenarios and checkpoints, the React UI, WS/SSE, Swagger 2.0
conversion, YAML input, URL import, request validation (§8 settings.validateRequests), traffic logging,
and multi-file/ZIP specs.

THE SPEC IS DESIGN.md (Russian, ~1230 lines, 87 KB). NEVER read it whole — you will be told the exact
line ranges for your task. Section index (line numbers are current):
  §4 Слои ответа 98-112 · §6 Путь запроса 136-174 · §7 Импорт 175-245 · §8 Роутер 246-296
  §9 ГЕНЕРАТОР 297-411 (Детерминизм 299-336 · Приоритет источника 337-341 · Реалистичность 342-347 ·
     Композиция 348-365 · Тело, тип и статус 366-374 · Рецепты 375-405, P1c, read only for the hook ·
     ГРАНИЦЫ 406-408 — recursion depth, array length, MOCKER_MAX_RESPONSE, and they are P1b's, not P1c's)
  §13 Схема SQLite 557-820 · §15 Модель угроз 917-982 · §18 Нагрузка 1088-1105 · §19 Фазы 1106-1128
The DB schema's real source of truth is internal/store/migrations/0001_init.sql (operations at line 54,
operation_responses at line 70) — read that, not §13, when you need a column name.

P0 AND P1a ARE WRITTEN AND GREEN. This digest IS your contract: open only the files your own task names.

  internal/config/config.go     Config{..., MaxBody, MaxResponse, MaxEntities, RuntimeCache, Dev, ...}
                                MaxResponse defaults to 4mb, RuntimeCache to 32.
  internal/domain/settings.go   Settings{Seed int64, BasePath string, ListSize int, NullRate float64,
                                Envelope *string, Identity, Auth, CORS, ValidateRequests bool,
                                DelayMs int, NotFoundBody json.RawMessage}; DefaultSettings()
  internal/store/store.go       DB{W,R *sql.DB}; two pools, ONE writer.
  internal/httpx/respond.go     JSON(w, status, v); Err(w, status, code, msg); ErrDetails(..., details)
  internal/workspaces/          Workspace{ID, Slug, Name, SpecID *int64, Revision int64, Settings domain.Settings}
  internal/openapi/             loaded document, lazy $ref resolver (see the signature block)
  internal/router/              Table{Match(method, segments) (*Match, bool), Len()}; Route{OpRowID,
                                OperationLabel, Method, Path, CanonicalPath, Custom, SourceOrder};
                                Match{Route *Route, Params map[string]string}; Build(routes, basePath)
  internal/specs/               Repo: Import, ByID, List, Operations, Responses, Routes, Report, Delete
                                Operation{ID, SpecID, Method, Path, CanonicalPath, OperationID *string,
                                Pointer string, ParseError *string, SourceOrder}
                                Response{ID, OperationID, Selector, HTTPStatus, IsDefault, MediaType
                                *string, StatusOrigin, SchemaPtr *string}
  internal/mockplane/           Plane: resolves the workspace by Host, CORS/preflight, /__mocker/health,
                                then routes.go: an LRU+singleflight cache of router.Tables keyed by
                                (workspace id, revision), and serveMatchedRoute — THE 501 THIS PHASE KILLS.
  internal/admin/               admin API behind session auth + CSRF; /api/specs written in P1a.
  cmd/mocker/main.go            wires config -> store -> repos -> planes -> server.

HARD RULES
1. You own ONLY the files your task names. Another agent may be writing a neighbouring file in the same
   package right now. Never edit a file assigned to someone else — not even to fix an obvious bug in it.
2. NO new runtime dependency. go.mod gains nothing from you. (One test-only dependency is pre-approved for
   the acceptance agent alone; if you are not that agent, the rule is absolute.)
3. The SQLite schema is FROZEN. No migration, no new column, no altered index.
4. NEVER read ${SPEC_PATH} (347 KB) or DESIGN.md whole. jq for the first, exact line ranges for the second.
5. Finish with all four clean, and paste the REAL output of the last one into "verified":
     test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
   (test -z is not decoration: "gofmt -l x && go build" exits 0 even when gofmt lists files.)
`

const CTX_SEED = `
THE SEED CONTRACT (DESIGN §9 "Детерминизм", lines 299-336, plus the launch decision on item identity).
This is the phase's core invariant. Read lines 299-336 before writing a line of it.

  seedList     = hash64(opts.Seed, method, canonicalPath, pathParams sorted by name, status)   // NO query
  seedScalar   = hash64(seedList-or-item-seed, dataPath)                                       // e.g. "user.profile.avatar_url"
                 // INSIDE AN ITEM THE dataPath RESTARTS AT THE ITEM ROOT. A list row's "name" and a
                 // detail card's "name" must hash the same string — if the row uses "items[3].name"
                 // while the card uses "name", both sides pick the same item seed and still produce
                 // different objects, and row == card fails for a reason no compiler shows.
  seedItemByID = hash64(seedList, canonical string form of the item's id)
  idForIndex(i)= a value shaped by the id field's own schema, derived from hash64(seedList, "id", i)

RULES THAT FALL OUT OF IT, each of which the acceptance test checks:
- THE IDENTITY IS WRITTEN INTO THE ITEM, not only used as a seed. A row's id property takes IDForIndex's
  value; a card's id property takes the path parameter's value COERCED TO THE ID PROPERTY'S OWN SCHEMA
  TYPE — the same typed form IDForIndex produces, never the raw URL string. The fixture's broadcastId is
  {type: integer, format: uint}, so writing the path parameter through verbatim emits {"id":"42"}, which
  fails schema validation AND makes row != card on the one field that identifies them.
  Everything else on both sides comes from SeedItemByID. Seed the object correctly but generate its id like any other scalar, and the card answers
  with an id different from the one that was requested — "row == card" then fails on the id itself, or
  quietly passes because "shared fields" was defined to exclude it.
- A list row and the detail card for the same id are THE SAME OBJECT. The detail route computes its
  family's seedList (Request.ListFamily, with the id path param removed) and then seedItemByID(id) —
  the id it was asked for. The list row at global index i takes id = idForIndex(i) and then generates
  its other fields from seedItemByID(that id). Both sides land on the same seed for any id, including
  one outside the current page window.
- "Global index" means offset+i, never the position on the page: ?offset=20 continues the same set.
- total is the SAME on every page (it comes from listSize, not from the page length).
- Two identical requests produce byte-identical bodies. With a frozen Options.Now that includes the
  time fields, and the acceptance test freezes it precisely so nothing is exempt.
- The walk is fully synchronous. No goroutine, no shared mutable generator state, no package-level RNG.
  Two concurrent requests must not be able to interleave into each other's values.

TIME (§9): ordinary time fields derive from the seed. Deadline-shaped fields — exp, *_expires_at,
expires_in, not_after, *_valid_until — derive from opts.Now(), because a frozen exp makes the
installation stop working the next day. That split is by FIELD NAME and it is deliberate.
`

const CTX_SPEC = `
THE ACCEPTANCE DOCUMENT: ${SPEC_PATH}, a real internal OpenAPI 3.0.3 API, 347 KB, gitignored.
NEVER Read or cat it. Query it with jq. These numbers are measured, not estimated — trust them and
re-measure with jq only when your task depends on a detail they do not cover:

  130 operations across 110 path items. 0 parse errors. Base path: ambiguous -> empty (P1a).
  114 of 130 have a 2xx response with a schema (after one $ref hop). 221 of 419 responses are $ref'd.
  Media types across all responses: application/json x401, text/csv x1. Nothing else.
  Response statuses: 200 x101, 201 x9, 204 x21, 400 x82, 401 x17, 403 x31, 404 x14, 409 x7, 410 x1,
    413 x2, 422 x8, 423 x1, 429 x2, 500 x17, and "default" x106.
  Composition keywords: allOf x11, oneOf x1, anyOf 0, discriminator 0, enum x44, nullable x5.
  format is nearly useless here — int64 x97, uint x39, uuid x10, uri x5, binary x3, date-time x2,
    int32 x2, bigint x2. NO email, NO date, NO password. This is exactly why §9 puts FIELD NAME
    ahead of format: 42 distinct property names match the name heuristics, format matches almost none.
  Most common property names: response x182, id x40, name x38, items x24, roles x20, type x19,
    title x18, count x16, code x12, description x12, limit x9, link x9, photo x8.
    ("response" is that high because the API wraps payloads in {"response": ...} in the SPEC ITSELF —
     that is the declared schema, not the settings.envelope feature. Generate what the schema says.)
  Pagination query parameters, by frequency: limit x10, offset x5, page x5, search x4, filter x2.

  THE LIST/DETAIL FIXTURE the acceptance test uses (verify with jq, do not re-pick it lightly):
    GET /api/v1/broadcasts              -> object {items: array, total: int, limit: int, offset: int}
                                           query params: limit, offset, statuses, min_time_begin,
                                           max_time_begin, min_time_end, max_time_end, order
    GET /api/v1/broadcasts/{broadcastId}-> object with id, name, description, organization_id, owner_id,
                                           time_begin, time_end, speakers, administrators, ...
  Only ONE operation returns a top-level array; lists in this document are objects wrapping an array
  property. A list detector that only handles a top-level array finds one list in the whole document.
`

const CTX_TEST = `
TESTING RULES
- Table-driven, standard library only. No testify, no gomock.
- A test that would still pass with the implementation deleted is worse than no test.
- Never weaken, skip or delete a test to reach green.
- Tests that need ${SPEC_PATH} must SKIP (t.Skip) when it is absent, so a fresh clone still builds green.
  internal/specs/acceptance_test.go already solves this: it walks UP from the working directory looking
  for go.mod and skips if it finds neither the marker nor the file. Reuse that approach — a test that
  silently skips because a relative path did not resolve from the package directory is a phantom pass.
- go test ./... -race -count=1 must be clean. -race is not optional in this phase: the runtime cache and
  the generator are read concurrently by every request on the mock plane.
`

const INTENDED = `
INTENDED, NOT DEFECTS — do not report these as findings:
- A matched route with no schema (a 204, or a response with no content) answers no body. That is correct.
- An operation whose row carries parse_error answers an EMPTY 200 with no body. DESIGN §7's invariant.
- settings.envelope wraps in the serving layer, deliberately outside the generator.
- The recipes engine, identity/jwt, op_overrides, force-status and the session layer are P1c. The value
  resolution path has ONE documented hook where a recipe will plug in; nothing calls it yet, by design.
- internal/gen imports openapi and the stdlib only. Passing settings as primitives in Options rather than
  passing domain.Settings is what keeps it a leaf.
- github.com/santhosh-tekuri/jsonschema/v6 in go.mod is the acceptance oracle and is imported from
  _test.go files only. It must not appear in "go list -deps ./cmd/mocker".
- Swagger 2.0 conversion, YAML input and URL import remain unimplemented in this phase by decision, and
  URL import still answers 400.
- gen.Body takes the whole ResponseVariant rather than a bare schema pointer. That is NOT over-general:
  DESIGN §9's value-source priority starts with the MEDIA-TYPE-level example, which lives on the content
  object, one level above the schema. Narrowing it back to a pointer silently deletes the first level of
  the priority order.
- settings.NotFoundBody stays unimplemented. It replaces the mock plane's 404 body, and the 404 path is
  not what this phase touches; it lands with the "no route matched" work, not with the generator.
- THE ITEM SEED IS DERIVED FROM THE ITEM'S IDENTITY, not from DESIGN §9's literal "seedItem(i) =
  hash64(seedList, i)". Deliberate: a detail route only ever sees an id and cannot invert an index out of
  it unless ids are dense integers (this document has 97 int64 ids and 10 uuid ones). Both of §9's stated
  properties still hold — see the seed contract the agents were given.
- THREE items DESIGN §19 files under P2 are implemented here on purpose, and this list is exhaustive:
  the deterministic oneOf branch (§9 defines it as part of composition, and a generator that cannot answer
  a oneOf schema answers nothing for that operation); settings.DelayMs (§6 names задержка as part of
  response assembly, and the field already exists in domain.Settings with a default nothing honours); and
  the non-JSON placeholder body (§9 "Тело, тип и статус" requires the declared media type to be honoured,
  and the acceptance document has a text/csv response the acceptance test asserts — answering it with JSON
  would be a wrong Content-Type, not a deferral). NOT accompanied by base64 pinned bodies, nor by the
  session layer's per-request delay or fail_next, which stay in P1c/P2.
`

// ---------------------------------------------------------------------------
// Schemas. Structured returns everywhere: an agent's prose is not parseable and
// the fan-in steps below key off these fields.
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
    todo: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'deliberately left for P1c+' },
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
    passed: { type: 'boolean', description: 'true only if every acceptance assertion you were able to RUN passes. If a prerequisite was missing (no docker, no module proxy, no acceptance document), say so in deviations and set smoke/passed accordingly rather than reporting a pass you did not observe' },
    smoke: {
      type: 'string',
      enum: ['passed', 'failed', 'skipped-no-docker', 'not-applicable'],
      description: 'the docker check: "not-applicable" for an agent that does not run make smoke. A missing docker daemon is skipped-no-docker, NOT failed — the two mean opposite things to every reviewer downstream',
    },
    measurements: {
      type: 'array', maxItems: 20, items: { type: 'string' },
      description: 'the real numbers your test printed: operations generated, bodies validated, failures by reason',
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

const filterContracts = (lines, re) => {
  // The supersession separator and const declarations are kept unconditionally:
  // a filter that drops "--- later lines supersede ---" turns a corrected
  // signature into an ambiguous duplicate, and dropping "const DefaultMaxDepth
  // = ..." hides the exact numbers a caller has to respect.
  const kept = lines.split('\n').filter((l) => re.test(l) || l.startsWith('---') || /^\s*const /.test(l))
  return kept.length ? kept.join('\n') : '(nothing reported — the signature block above IS the contract)'
}

// A gate: two reviewers on one section, then a single fix agent if either found
// a blocker. Majors are carried to the end of the run rather than fixed here,
// so "major" does not silently mean "ignored" at a gate and "fix it" later.
const runGate = async (name, vectors, fixCtx) => {
  // sonnet is the section-gate default; v.model lets one vector be escalated
  // explicitly rather than having the override silently ignored here.
  const results = await parallel(vectors.map((v) => () => agent(v.prompt, { label: v.label, phase: name, model: v.model || 'sonnet', schema: REVIEW_SCHEMA })))
  const findings = results.filter(Boolean).flatMap((r) => r.findings || [])
  // REVIEW_SCHEMA caps findings at 40. A reviewer that returns exactly 40 was
  // probably truncated, and a silent truncation reads as "that is all there is".
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

  // RE-REVIEW, once, and only the vectors that raised a blocker. The guide's
  // section-gate rule is "fix -> re-review before proceeding"; without it the
  // gate's own fixes are self-assessed, in exactly the packages three later
  // sections are built on. Bounded to one round and to the affected vectors, so
  // a clean gate costs nothing and a dirty one costs at most two extra agents.
  let unresolved = []
  if (blockers.length && !fix) {
    // The fix agent died. Without this branch its blockers would vanish: the
    // end-of-run list only picks up gate MAJORS and this gate's `unresolved`,
    // so an unfixed blocker in a package three later sections build on would
    // reach the return value as silence.
    log(`${name}: the fix agent returned NOTHING — carrying its ${blockers.length} blocker(s) unfixed into the end-of-run fix list.`)
    unresolved = blockers
  } else if (blockers.length && fix) {
    const dirty = vectors.filter((_, i) => (results[i]?.findings || []).some((f) => f.severity === 'blocker'))
    const fixScope = (fix.files || []).join(' ')
    const again = await parallel(dirty.map((v) => () => agent(`${v.prompt}

RE-REVIEW, ROUND 2. You raised blocker findings on this section and a fix agent has since edited it.
NARROW YOUR DIFF: the fix touched only ${fixScope || '(it reported no file list — fall back to the section scope above)'}.
Diff THAT, not the whole section again — you already read the section this round:
    git add -N . ; git diff HEAD -- ${fixScope || '<the section paths above>'}
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
    // A recheck agent that DIED verified nothing, so its vector's original
    // blockers are carried forward rather than treated as closed. Without this,
    // silence from a dead reviewer and silence from a satisfied one are the same
    // value, and the blockers disappear between here and the end-of-run list.
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
  return { findings, fix, unresolved }
}

// ---------------------------------------------------------------- phase 1 --
// The kernel owns the package's types and the seed contract. It runs ALONE and
// FIRST: everything else in the phase is written against signatures it defines,
// and two agents inventing the same types in parallel is how a phase stops
// compiling an hour in.
phase('Kernel')

const kernel = await agent(`${CTX_CORE}${CTX_SEED}${CTX_TEST}

YOU ARE THE CONTRACT OWNER for internal/gen. Three later agents write files in your package against the
signatures you define, so your exported API is fixed BEFORE they start and must match this block exactly:

${SIG_GEN}

The openapi package you build on (already written, do not modify):

${SIG_OPENAPI}

YOUR FILES — create these and nothing else:
  internal/gen/gen.go        Options, Request, ResponseVariant, Generator, New, Body, Headers, the errors
  internal/gen/seed.go       SeedList, SeedItemByID, SeedScalar, IDForIndex, the hash64 primitive
  internal/gen/schema.go     the schema walk: object, array, scalar, required, budgets
  internal/gen/gen_test.go, internal/gen/seed_test.go, internal/gen/schema_test.go

DO NOT create compose.go, values.go, names.go or list.go — three other agents own those files and will
write them after you (you run alone and first). Instead, declare the seams they plug into and leave each a single-line stub or an
interface your walk calls:
  - composition (allOf/oneOf/anyOf/discriminator) is dispatched from your walk to a function the Values
    agent implements: give it a name and a signature, and have your walk call it. Its file is theirs.
  - the value source for a leaf (example/enum/const/default/format/field name) is likewise dispatched to
    a function the Values agent implements.
  - the list contract is dispatched from Body: if a request is a list request, a function the List agent
    implements produces the body. Deciding WHEN a request is a list is not your call either — export the
    seam and let its implementation answer "not a list, carry on".
  State those seam signatures VERBATIM in your "contracts" array — they are the interface three agents
  code against, and a mismatch there is a compile break nobody can fix without a round trip.

  TEMPORARY STUBS — the shape matters, not just the fact. The tree must build before those three files
  exist, so write a trivial implementation of each seam, but put EACH IN ITS OWN FILE:
      internal/gen/stub_compose.go   internal/gen/stub_values.go   internal/gen/stub_list.go
  each holding ONLY that one seam's stub and a comment saying which agent deletes it. The owning agent
  removes the whole file with "rm" and lands its own. Do NOT put the stubs inside gen.go or schema.go:
  three agents would then have to edit one file you own, concurrently, to delete different lines from it —
  the last writer would silently undo the others.

WHAT YOU IMPLEMENT
1. The seed layers exactly as CTX_SEED specifies. hash64 must be a stable, explicit function (FNV-1a or
   xxhash-style written out) — NOT maphash, whose seed differs per process, and NOT anything from
   math/rand. A body generated today and the same body generated after a restart must be identical.
2. The schema walk over a RESOLVED node: object (properties, required, additionalProperties),
   array (items, minItems/maxItems, default length from Options.ListSize), and the scalar dispatch.
   Every $ref is followed through the resolver LAZILY, one hop at a time — never build a dereferenced
   tree (DESIGN §2 rejects it: "взрывается на рекурсии", and this document has a self-referencing
   schema).
3. Budgets, which are what stop a self-referencing schema from eating the process:
   - Options.MaxDepth bounds recursion. At the limit, an optional object property is OMITTED and a
     required one gets the smallest legal value for its type (null is NOT legal unless the schema says so).
   - Options.MaxBytes bounds the serialized result. §9: "Большой listSize на вложенной схеме умножается
     на каждом уровне — режем по размеру результата." Track the growing size DURING generation and stop
     growing arrays; do not generate 40 MB and then truncate the bytes into invalid JSON.
   - Both must be provably terminating: write a test with a schema that references itself through an
     array of itself, and assert it returns rather than hangs. Use a test timeout, not patience.
4. x-nullable / NullRate: null ONLY if type is exactly ["null"], if default is null, or with probability
   NullRate (default 0) derived from seedScalar — never from a package RNG.
5. readOnly / writeOnly by direction: a response body omits writeOnly properties, including when they are
   listed in required (that combination is a spec bug, and omitting is the honest reading — record it).
6. Body returns (nil, nil) for an empty schema pointer, and Headers returns an empty map when the response
   declares no headers. Neither is an error.
   WRITE A FIXTURE FOR THE NON-EMPTY HEADERS PATH, because the real document cannot exercise it: its only
   two "headers" maps hang off a 404 and a "default" response of operations that also declare a 200, and
   the selection rule always picks the 200 — so Headers returns empty for all 130 operations and a broken
   implementation would look perfect. The fixture: an operation whose 2xx response is a $ref into
   components/responses, and THAT response carries a headers map. Both halves matter — the $ref (because
   plain pointer navigation into a referring site finds no "headers") and the header itself.

READ FIRST (and only these): DESIGN.md lines 297-341 (the seed contract), 348-365 (composition — you do
not implement it, but x-nullable and readOnly/writeOnly, your items 4 and 5, are defined there), 366-374
(body, type and status) and 406-408 (the boundaries your budgets implement); then
internal/openapi/resolver.go and internal/openapi/openapi.go for the resolver's real behaviour on cycles
and budget exhaustion — including which of its methods chase a $ref and which do not.`,
  { label: 'gen:kernel', phase: 'Kernel', model: 'sonnet', schema: SCHEMA })

if (!kernel) {
  log('FATAL: the kernel agent returned nothing. Every later phase codes against its signatures; stopping instead of building on a package that may not exist.')
  return { phase: 'P1b', failed: 'kernel', acceptance: [], findings: 0, actionable: 0, rounds: [], stillOpen: [] }
}
logDeviations('Kernel', [kernel])
const kernelContracts = contractsOf([kernel])

// ---------------------------------------------------------------- phase 2 --
// Two agents, two files each, no shared file, both against the kernel's fixed
// contract. They are genuinely independent: composition rewrites a schema node
// before the walk descends, value sources produce a leaf after it stops.
phase('Values')

const valuesCtx = `${CTX_CORE}${CTX_SEED}${CTX_TEST}

The kernel of internal/gen is WRITTEN. This is its API, and the seams it left you, as the kernel agent
actually reported them:

${SIG_GEN}

CONTRACTS AS WRITTEN (later lines supersede earlier ones):
${kernelContracts}

Read internal/gen/gen.go, seed.go and schema.go before writing anything — the seam signature in the code
wins over any paraphrase above.

YOUR SEAM'S STUB: the kernel left a trivial implementation in a file of its own, named in your task below.
Land your file, then "rm" that whole stub file — the whole file, in one operation. Never open another
agent's stub file, and never edit a kernel file to remove a stub: someone else is doing the same thing to
the same file at the same time, and the last write wins.

A PEER IS LANDING ANOTHER FILE OF THIS SAME PACKAGE RIGHT NOW. So a build you run mid-flight can fail on
code that is not yours: a duplicate declaration naming a stub_*.go that is not yours, or an error inside
compose.go/values.go/names.go you did not write. That is the peer mid-landing, not your bug — wait ~30
seconds and re-run. Do NOT edit or delete their file to make your build pass, and do NOT report a failure
that is only their window. Your own file plus your own stub deletion is the whole of your job.
FOR THE SAME REASON, RUN "gofmt -w" ON YOUR OWN FILES ONLY, never "gofmt -w .". If "gofmt -l" names a
file that is not yours, leave it — the peer is saving it right now, and the one place in this phase where
two agents can write one file is a formatter told to fix the whole tree.
BOUNDED, THOUGH: if it is still broken after THREE attempts, stop retrying — the peer may have died
mid-landing, leaving a stub deleted with nothing to replace it or a file with no stub deleted, and no
amount of waiting fixes that. Record exactly what the build says in "deviations" and finish. The gate
reviews the tree next and its fix agent owns all of internal/gen, so a half-landed peer is recoverable;
an agent looping on a build it cannot influence is not.`

const values = await parallel([
  () => agent(`${valuesCtx}${CTX_SPEC}

YOUR FILES — create these and nothing else:
  internal/gen/compose.go, internal/gen/compose_test.go
  and "rm internal/gen/stub_compose.go" once yours compiles.

YOU IMPLEMENT SCHEMA COMPOSITION. Read DESIGN.md lines 348-365 first — it is precise and it is the
acceptance criterion for your work:

1. allOf is an INTERSECTION OF CONSTRAINTS, not an overwrite of one branch by the next. required unions;
   properties merge; numeric and string bounds NARROW (minimum = max of the branches, maximum = min,
   likewise minLength/maxLength); additionalProperties:false in ANY branch closes the result. A merge
   that lets a later branch widen an earlier branch's bound is the classic bug here.
2. An UNSATISFIABLE composition (minimum 10 with maximum 5, or required x with additionalProperties:false
   and no x property) must NOT quietly produce a number anyway. Return ErrUnsatisfiable so the operation
   is marked, per §9: "не повод сгенерировать число: операция помечается ошибкой схемы".
3. oneOf / anyOf: the branch is chosen DETERMINISTICALLY by hashing the data path (§9 — the same field in
   the same object always picks the same branch), then the produced value is checked against the chosen
   branch and, for oneOf, against the OTHERS — oneOf requires satisfying EXACTLY ONE. On a miss, bounded
   retry over the remaining branches, then give up honestly rather than looping.
4. discriminator: pick the branch through mapping (falling back to the schema name when mapping is
   absent), and ALWAYS set the discriminator property itself to the value that selected the branch. §9:
   "без него zod/io-ts отвергает всё тело." A discriminated body missing its own discriminator is a
   blocker-grade defect, so test it explicitly.
5. Composition nodes may themselves be $ref'd, and a branch may be $ref'd. Resolve lazily through the
   resolver, one hop at a time; a cycle or an exhausted budget is a marked failure, not a panic.

MEASURED, so you know what the real document exercises: allOf x11, oneOf x1, anyOf 0, discriminator 0.
Your allOf path is on the hot path for real data; your discriminator path is exercised only by your own
fixtures, so write those fixtures as if they were the only proof it works — they are.`,
    { label: 'gen:compose', phase: 'Values', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${valuesCtx}${CTX_SPEC}

YOUR FILES — create these and nothing else:
  internal/gen/values.go, internal/gen/names.go, internal/gen/values_test.go
  and "rm internal/gen/stub_values.go" once yours compiles.

YOU IMPLEMENT WHERE A LEAF VALUE COMES FROM. Read DESIGN.md lines 337-347 first. The priority order is
fixed and is not yours to reorder:

  recipe -> example/examples (media type -> named -> schema -> const -> default) -> enum -> generated

1. The RECIPE branch is P1c. Leave it as ONE documented hook — a single function or interface with a
   comment naming §9 "Рецепты" — that currently always says "no recipe". Do NOT implement recipes. Do NOT
   design a recipe format. One hook, nothing more.
2. examples: OpenAPI 3.0 wrote "example", 3.1 writes "examples"; P1a's normalizer already migrated
   "example" -> "examples", so read the normalized form but do not assume the singular is gone from every
   nesting level you meet. An example that is present is used VERBATIM — it is the spec author telling
   you what the field looks like, and it beats anything you would invent.
   THE SHAPES, so your fixture does not encode the same wrong guess the implementation does. Read
   internal/openapi/normalize.go rather than assuming — normalizeDialect walks EVERY object node, not just
   schema nodes, so its "example -> one-element examples array" rewrite lands on media-type objects too.
   That means at a media type you may meet EITHER form: the OAS map of named Example Objects (payload
   under "value") OR a one-element array the normalizer made from a singular "example". Handle both. A
   type switch that assumes "media type means map" silently misses the commonest OAS 3.0 spelling, and
   this document cannot catch it — all 17 of its examples are schema-level singulars, none at a media
   type. At schema level the normalized form is always the array.
   THE ORDER INSIDE THIS LEVEL IS "media type -> named -> schema -> const -> default", and the FIRST of
   those does not live on the schema at all: it is on the content object, one level above the schema. That
   is why Body takes the whole ResponseVariant.
   HOW TO REACH IT WITHOUT GETTING IT WRONG: do NOT hand-build the pointer from OpPointer — 221 of this
   document's 419 responses are $ref'd, and plain pointer navigation does not chase a $ref, so a
   hand-built ".../responses/200/content/..." resolves to nothing for more than half the corpus. Two
   correct routes: strings.TrimSuffix(v.SchemaPtr, "/schema"), which is already anchored at the RESOLVED
   response (the indexer anchors schema_ptr at the last $ref hop — see internal/specs/index.go), or
   Resolve(v.OpPointer + "/responses/" + escaped(v.Selector)) and then index ["content"][mediaType] on the
   resolved node. Measured here: 0 media-type examples and 17 schema-level ones — so this level is
   invisible in this document and would ship broken unnoticed. Write a fixture for it.
3. enum: pick deterministically from seedScalar. const and default are exact values.
4. GENERATED values, and this is where a real document is won or lost. format FIRST (int64, int32, uint,
   bigint, uuid, uri, binary, date-time — measured: those are literally all this document uses), then the
   FIELD NAME, which is what actually carries meaning here: 97 int64 and 39 uint tell you nothing, while
   "email", "photo", "image", "cover", "link", "avatar", "*_url", "phone", "*_at", "*_date", "time_*",
   "name", "first_name",
   "title", "description", "status", "code", "count", "color", "slug", "id" tell you everything. §9 is
   blunt about why: "без разбора имён фронт падает на new URL(\\"fugiat cupidatat\\")". A URL-shaped field
   must produce a parseable URL; an email-shaped field must produce something with an @ and a dot; a
   *_at field must produce a parseable timestamp.
5. Respect the schema's own constraints on what you generate: minLength/maxLength, minimum/maximum,
   multipleOf, and pattern where you can satisfy it cheaply (do NOT write a regex generator — if a
   pattern cannot be satisfied by your generator, fall back to a value that satisfies the rest and record
   it as a limitation).
6. TIME, per CTX_SEED: ordinary time fields derive from the seed so they are stable; deadline-shaped
   names (exp, *_expires_at, expires_in, not_after, *_valid_until) derive from Options.Now(). Do NOT call
   time.Now() directly anywhere — the injected clock is the only clock, and the acceptance test freezes it.
7. Every value derives from a seed passed in. No package-level RNG, no math/rand global, no time-based
   entropy, no map iteration order leaking into a value.

names.go is the field-name corpus and its lookup: keep it a plain table plus small helpers, written so a
human can add a name in one line. It is data, not cleverness.`,
    { label: 'gen:values', phase: 'Values', model: 'sonnet', schema: SCHEMA }),
])

const valuesOK = values.filter(Boolean)
const valuesGap = [!values[0] && 'gen:compose', !values[1] && 'gen:values'].filter(Boolean).join(', ')
if (valuesGap) log(`WARNING: Values agent(s) returned nothing: ${valuesGap}. Their files may exist anyway — an agent can die on its structured report with the work on disk. The gate reviews the tree, not the reports, and the List agent is told to check.`)
logDeviations('Values', valuesOK)

// ---------------------------------------------------------------- phase 3 --
// Gate 1. The list contract and the whole serving path are built on the seed
// layers and the composition rules; a defect in either is not something a
// compiler or a later test surfaces, it is something a user notices as
// "the mock reshuffles itself".
phase('Gate 1')

// The paths P1b actually writes. HEAD is P1a (committed before this run
// precisely so a diff means "what this phase did"), so the scoping no longer
// has to exclude an uncommitted previous phase — it excludes .claude's workflow
// scripts, which are 185 KB of untracked non-code, and it tells each reviewer
// where the phase lives.
// The trailing group (store, openapi, domain, config, specs/index.go) is not
// where P1b works — it is there so the diff can PROVE it did not. HARD RULE 3
// freezes the SQLite schema and HARD RULE 2 forbids a runtime dependency, but a
// fix agent is told "you own every file the findings name": a migration
// 0002_*.sql, or a change to how index.go anchors schema_ptr, would otherwise
// land outside every reviewer's diff. These paths are committed and untouched,
// so they cost zero bytes unless something really did change.
const P1B_PATHS = 'internal/gen internal/mockplane internal/testspec internal/router internal/specs internal/server/p1a_test.go internal/admin/spec_handlers.go internal/admin/spec_handlers_test.go cmd/mocker/main.go scripts/smoke.sh README.md Makefile go.mod go.sum internal/store internal/openapi internal/domain internal/config'

// The diff command is SCOPED, per gate, and that is a cost decision, not tidiness.
// Measured on this tree: an unscoped "git add -N . && git diff HEAD" is ~446 KB
// today and past 600 KB by the Review phase — of which 185 KB is the two workflow
// scripts themselves (untracked, not gitignored) and ~180 KB is P1a's already-
// reviewed code. Six reviewers running it re-bill that as cache-read every turn:
// ~750k tokens of diff to review ~250 KB of new Go. Committing P1a before this
// run removed the bulk of that (measured: the P1B_PATHS diff is 0 bytes at
// launch, so every byte a reviewer reads is this phase's own work). What the
// scoping still buys is keeping the 126 KB untracked workflow script out of six
// reviewers' contexts. It does not make reviewers cheaper than producers —
// reading a diff is inherently the larger half of a run like this.
const gateCtx = (paths) => `Repo root is your CWD. READ-ONLY: do not modify any file. Running tests is expected.
TO SEE THE DIFF, SCOPED TO WHAT YOU ARE REVIEWING — most of these files are NEW, and plain "git diff HEAD"
does not show untracked files at all:
    git add -N . ; git diff HEAD -- ${paths}
Two things about that exact form. The ";" is not "&&": a pathspec that matches nothing makes "git add -N"
exit 128 having added NOTHING, and with "&&" you would then see no diff at all and conclude the section is
empty — some of the paths above are files an agent may have failed to create. And "git add -N ." rather
than the path list, for the same reason; the scoping lives in the diff, which tolerates a missing path.
Intent-to-add stages nothing for commit. Your co-reviewer runs the same command at the same time: if git
reports ".git/index.lock: File exists", wait a second and retry — contention, not a repo problem.
Do NOT widen the diff paths: the workflow scripts under .claude are not code under review.
HEAD IS PHASE P1a, committed and green, so this diff is P1b's work and nothing else. Anything you see in
it was written by this run.
DESIGN.md is Russian and 87 KB — never read it whole. The acceptance document ${SPEC_PATH} is 347 KB —
never Read or cat it, query it with jq only.
Report ONLY defects you can point at with a file and a line. No praise, no style nits that do not change
behaviour. Empty findings array if your vector is clean.`

const gate1 = await runGate('Gate 1', [
  {
    label: 'gate1:determinism',
    prompt: `${gateCtx('internal/gen')}
${INTENDED}

Under review: internal/gen — the kernel (gen.go, seed.go, schema.go) and the two files just added
(compose.go, values.go, names.go). DESIGN.md §9 lines 297-365 is the specification.

YOUR VECTOR: determinism and correctness of the seed layers and the composition rules. This is the
phase's core invariant and almost none of it is visible to the compiler.
- hash64: is it a fixed, explicit function? maphash (per-process seed), map iteration order, time, or
  anything from math/rand's global source anywhere in a value's derivation is a BLOCKER — the mock would
  reshuffle on restart, and no test in this package would notice.
- Is seedList really independent of the query string, and does it really include method, canonical path,
  path params and status? Are path params ordered deterministically (sorted), or is a map ranged over?
- Does an item's non-id field derivation go through seedItemByID(id) — so a detail card can reach the
  same seed as a list row — rather than through the index?
- Does the walk ever branch on something that is not derived from a seed (a length from len(map), a
  choice from the first key of a map, a value from a slice whose order came from a map)?
- allOf: do bounds NARROW in both directions, does required union, does additionalProperties:false in any
  branch close the result? Find the case where a later branch widens an earlier bound — it is the classic
  defect here and it is silent.
- oneOf: is the result checked against the OTHER branches too, so "exactly one" actually holds?
- discriminator: is the discriminator property itself always set on the produced object?
- Unsatisfiable compositions: ErrUnsatisfiable, or a number generated anyway?
- Budgets: prove to yourself that a self-referencing schema terminates. Write the fixture if the tests do
  not have one and report what happened. Is MaxBytes enforced DURING generation, or by truncating bytes
  afterwards (which produces invalid JSON — a blocker)?
- Concurrency: any shared mutable state on Generator, any package-level variable written at request time.
  Run: go test ./internal/gen/... -race -count=2
- Tests that assert nothing, or that would still pass with the implementation deleted.`,
  },
  {
    label: 'gate1:efficiency',
    prompt: `${gateCtx('internal/gen')}
${INTENDED}

Under review: internal/gen (gen.go, seed.go, schema.go, compose.go, values.go, names.go).

YOUR VECTOR: simplification, reuse and hot-path efficiency. This code runs on EVERY request to the mock
plane, which DESIGN §18 (lines 1088-1105) requires to survive a full e2e run without a rate limit.
- Work that is per-document but done per-request: re-parsing the document, rebuilding a table of field
  names, recompiling a regexp inside the walk, re-resolving the same pointer for every property.
  regexp.MustCompile at package scope is fine; inside a loop it is a finding.
- Allocation churn on the hot path: building intermediate maps per property, string concatenation in a
  loop for the data path, a []byte round-tripped through string repeatedly.
- Duplication between the three new files and the kernel — two implementations of "resolve this node",
  two field-name tables, two copies of the bounds-narrowing logic. Say which one should survive.
- Reimplementation of something that already exists: internal/openapi already resolves pointers and
  already normalizes the dialect; internal/router already canonicalizes paths. A second copy in gen is a
  finding even when it works, because they will drift.
- Code that is more general than the phase needs: a plugin registry where a switch would do, an interface
  with one implementation and no second one coming. Say so plainly.
- Anything that would make the recipe layer (P1c) hard to add at the documented hook.
Do NOT report formatting or naming preferences. Only things that cost time, memory, or a future edit.`,
  },
], `${CTX_CORE}${CTX_SEED}${INTENDED}

THE PACKAGE'S EXPORTED API IS FIXED — three later agents code against it. Change a signature only if a
finding genuinely requires it, and put the new signature in "contracts" so they are told:

${SIG_GEN}

HARD RULE 1 DOES NOT APPLY TO YOU: you run alone and own every file in internal/gen. Rules 2-5 still hold.`)

// ---------------------------------------------------------------- phase 4 --
// The list contract is where DESIGN §9 says a real frontend breaks first, and
// it is the one part that needs BOTH the seed layers and the value sources
// finished, so it runs alone after the gate rather than beside them.
phase('List')

const genContracts = contractsOf([kernel, ...valuesOK, gate1.fix].filter(Boolean))

const list = await agent(`${CTX_CORE}${CTX_SEED}${CTX_TEST}${CTX_SPEC}

internal/gen's kernel, composition and value sources are WRITTEN and reviewed.

${SIG_GEN}

CONTRACTS AS WRITTEN (later lines supersede earlier ones):
${filterContracts(genContracts, /gen\.|func |type |var Err|Seed|ID(For)?Index|Options|Request|Response|Generator/)}

Read internal/gen/gen.go and schema.go before writing — the code wins over any paraphrase.

YOUR FILES — create these and nothing else:
  internal/gen/list.go, internal/gen/list_test.go
  and "rm internal/gen/stub_list.go" once yours compiles — the whole file, which holds nothing else.

YOU IMPLEMENT THE LIST CONTRACT. Read DESIGN.md lines 299-336 — the contract is stated there as a list of
requirements and every one of them is checked by the acceptance test:

1. DETECT a list response. In this document exactly ONE operation returns a top-level array; the rest wrap
   an array in an object ({items, total, limit, offset} — see the fixture in the context above). So:
   a top-level array is a list, AND an object with exactly one array-typed property is a list, preferring
   a property named items/data/results/list/content/rows when there is more than one candidate. When
   nothing looks like a list, say so and let the ordinary walk handle it — do not force pagination onto
   an object that is not a page.
2. PAGE LENGTH from the query: limit / per_page / size. OFFSET from offset / page / cursor (page is
   1-based and multiplies by the page length; cursor is opaque — accept an integer-ish cursor, ignore
   what you cannot parse). Clamp both HARD: a hostile or careless ?limit=1000000 on an OPEN mock plane
   must not allocate a million items. Clamp to something defensible, log the clamp in your summary, and
   respect Options.MaxBytes as the ultimate bound.
3. total is the SAME on every page and comes from Options.ListSize, not from the page length. Fields named
   total/count/total_count/totalCount get it; limit/offset/page get the request's own values echoed back.
4. GLOBAL INDEX: the item at page position i on a page starting at offset is the item with global index
   offset+i, and it must be identical to that item on any other page that contains it. Its id is
   IDForIndex(seedList, offset+i, idSchema) and its other fields come from SeedItemByID(seedList, id).
5. DETAIL ROUTES: when Request.ListFamily is non-empty, this is a detail card. Compute the FAMILY's
   seedList — same method, ListFamily as the canonical path, the id path parameter REMOVED, status 200 —
   then generate the object from SeedItemByID(familySeedList, the id from the path). The card and the row
   must be byte-identical for the shared fields. This is the acceptance test's headline assertion:
   "фронт видит согласованные список и карточку".
6. POSTFILTER: query parameters DECLARED IN THE SPEC whose names match a property of the item schema are
   applied as a filter, and the page is topped up to the requested length afterwards (§9: "с догенерацией
   до запрошенной длины"). A filter that matches nothing legitimately returns an empty page — do not
   invent matches. Only spec-declared parameters filter; an arbitrary unknown query parameter must be
   ignored, not treated as a filter.
7. Nested lists inside an item still obey Options.ListSize and the byte budget — the multiplication §9
   warns about is real (a 25-item list of objects each holding a 25-item list is 625 objects).

TEST with fixtures you write, not with the real document (the acceptance agent does that): a list schema
shaped like the fixture above, a detail schema sharing its item schema, and assertions for offset
continuation, stable total, and row == card.`,
  { label: 'gen:list', phase: 'List', model: 'sonnet', schema: SCHEMA })

if (list) logDeviations('List', [list])
else log('WARNING: the list agent returned nothing. Check internal/gen/list.go on disk before assuming it did not run — the Serve agents and the acceptance test both depend on it.')

// ---------------------------------------------------------------- phase 5 --
// Serve. TWO AGENTS, STRICTLY SERIAL, and that is a correction: they write
// different files of the SAME Go package, and their halves reference each
// other (respond.go takes a *runtime that runtime.go declares; routes.go calls
// serveGenerated that respond.go defines). Run in parallel, neither could
// reach "go build ./... clean" — its own exit criterion — until the other
// landed, and the predictable escape is for each to declare the missing symbol
// itself, which is a duplicate declaration the moment the peer lands. Every
// earlier parallel pair in this project was in DIFFERENT packages; this one is
// not, so it pipelines instead. Stage 1 keeps the tree green by moving P1a's
// 501 into a stub file of its own; stage 2 deletes that file and owns every
// test that asserted the 501.
phase('Serve')

const serveCtx = `${CTX_CORE}${CTX_TEST}

internal/gen is WRITTEN: kernel, composition, value sources and the list contract.

${SIG_GEN}

THE SEAM. Both halves are fixed here so neither stage has to invent the other's signature — you run
SERIALLY (runtime first, then respond), so stage 2 can and must read what stage 1 actually landed:

${SIG_SERVE}

CONTRACTS AS WRITTEN in internal/gen (later lines supersede earlier ones):
${filterContracts(contractsOf([kernel, ...valuesOK, gate1.fix, list].filter(Boolean)), /gen\.|func |type |var Err|Seed|ID(For)?Index|Options|Request|Response|Generator|List/)}

Read internal/mockplane/routes.go and plane.go before writing anything. P1a left the seam deliberately:
serveRoute matches, then calls serveMatchedRoute, which answers the temporary 501 THIS PHASE KILLS.`

const serveRuntime = await agent(`${serveCtx}

YOU ARE STAGE 1 OF 2. Stage 2 writes respond.go after you and owns every test that asserts P1a's 501.
YOUR JOB IS THE PLUMBING, AND THE TREE MUST BE GREEN WHEN YOU FINISH — including the existing tests.

YOUR FILES:
  internal/mockplane/runtime.go       (new — the per-workspace runtime and its cache)
  internal/mockplane/routes.go        (modify — the cache now holds a runtime, not a bare *router.Table;
                                       serveRoute calls p.serveGenerated)
  internal/mockplane/respond_stub.go  (new, TEMPORARY — see below)
  internal/mockplane/runtime_test.go  (new)
  internal/mockplane/routes_test.go   (ONLY to keep it compiling — see the paragraph below. Change no
                                       assertion: stage 2 owns those.)
  internal/router/router.go           (add HasCanonical ONLY — do not touch the comparator or Match)
  internal/router/router_test.go      (add its test)
  internal/specs/repo.go              (add Normalized and Variants ONLY — do not touch Import or Index)
  internal/specs/repo_test.go         (add their tests)
  cmd/mocker/main.go                  (only if the wiring genuinely changes)
DO NOT create or edit internal/mockplane/respond.go, plane_test.go or internal/server/p1a_test.go.

routes_test.go IS A NARROW EXCEPTION AND YOU MUST HANDLE IT, or you cannot finish green. Its
fakeSpecSource (around line 23) implements ONLY Routes. The moment SpecSource grows to three methods it
stops satisfying the interface and the whole package stops compiling — and even after that, runtimeFor
would call Normalized on a fake that has no document, so serveRoute's error branch would answer 500 and
every existing 501 assertion would fail for a reason that has nothing to do with them. So: give
fakeSpecSource the two new methods, returning a small VALID normalized document (enough for openapi.Load
and NewResolver to succeed) and a variants map that matches the routes it already serves. Touch nothing
else in that file — no assertion, no test name. Stage 2 rewrites the assertions; you only keep the
package compiling and the existing expectations true.

respond_stub.go IS HOW YOU STAY GREEN. Move P1a's serveMatchedRoute and notImplementedDetails OUT of
routes.go and into respond_stub.go, renamed to the seam's signature:
    func (p *Plane) serveGenerated(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, m *router.Match)
answering the SAME 501 it answers today, with a comment naming stage 2 as the agent that deletes the whole
file. Behaviour is unchanged by you, so every existing 501 assertion still passes and your "go test ./...
-race" really is clean. Stage 2 rm's the file and rewrites those tests. Do NOT weaken or delete a test to
get there, and do NOT leave the 501 in routes.go.

WHAT YOU BUILD

1. specs.Repo.Normalized(ctx, specID) — the stored normalized document bytes.
   specs.Repo.Variants(ctx, specID) — ONE query joining operation_responses to operations, returning
   map[operationRowID][]gen.ResponseVariant with Selector, HTTPStatus, IsDefault, MediaType, SchemaPtr,
   OpPointer (operations.pointer) and Degraded (operations.parse_error IS NOT NULL). One query for the
   whole spec, not one per operation: this runs on a cold cache while a request waits.
   Read internal/store/migrations/0001_init.sql lines 54-81 for the exact columns. The schema is FROZEN.

2. router.Table.HasCanonical(method, canonicalPath) — does the table contain a route with this method and
   canonical path. The response path needs it to tell a detail route from an ordinary one; the alternative
   is mockplane re-deriving route families from paths, which would put a second copy of the router's own
   knowledge outside the router.

3. The runtime and its cache. P1a's routeCache holds a *router.Table keyed by (workspace id, revision),
   LRU-bounded by cfg.RuntimeCache and built under singleflight. Widen the cached value to the runtime
   struct in the seam above, keeping every property it already has:
   - keyed by (workspace id, revision) so a settings edit is an ordinary cache miss — never compare a
     stored revision by hand;
   - singleflight so N concurrent cold requests build ONCE (building now costs a 347 KB parse, so this
     matters far more than it did in P1a);
   - LRU eviction bounded by cfg.RuntimeCache;
   - immutable after build: nothing mutates a runtime after it is published, so concurrent readers need
     no lock. A generator that lazily fills a cache internally must do so in a way that is race-free —
     go test -race is run against exactly this.
   Building one means: Normalized -> openapi.Load (idempotent on already-normalized input — assert that
   with a test, because the whole design leans on it) -> openapi.NewResolver -> gen.New with Options
   filled from ws.Settings and cfg.MaxResponse -> Variants -> router.Build. Load's report is discarded
   here on purpose: the import already recorded it.
   A workspace with no spec, or a Plane built with a nil SpecSource, keeps answering exactly the 404 it
   answers today. Do not change that branch.

4. serveRoute calls p.serveGenerated(w, r, ws, rt, match) on a match. routes.go keeps no 501 of its own —
   the only copy left in the tree is the temporary one in respond_stub.go, which stage 2 deletes.

DESIGN to read: lines 246-296 (§8, the router and the request path) and 1088-1105 (§18, why the hot path
has no room for per-request work).`,
  { label: 'serve:runtime', phase: 'Serve', model: 'sonnet', schema: SCHEMA })

if (serveRuntime) logDeviations('Serve runtime', [serveRuntime])
else log('WARNING: serve:runtime returned nothing. Its files may still be on disk — stage 2 is told to verify runtime.go and the widened SpecSource exist before coding against them.')

const serveRespond = await agent(`${serveCtx}

YOU ARE STAGE 2 OF 2. Stage 1 landed runtime.go, the widened SpecSource, router.HasCanonical and the two
new specs queries, and parked P1a's 501 in internal/mockplane/respond_stub.go behind the seam signature.
READ internal/mockplane/runtime.go AND routes.go FIRST — the code stage 1 actually wrote wins over any
paraphrase here. If runtime.go is missing or the seam does not match, say so in "deviations" and build
against what is really there rather than duplicating a type that already exists.

WHAT STAGE 1 REPORTS HAVING WRITTEN (its own words; the code still wins):
${serveRuntime ? (serveRuntime.contracts || []).join('\n') || '(it reported no contracts)' : '(stage 1 returned nothing — verify everything against the tree before you use it)'}

YOUR FILES:
  internal/mockplane/respond.go       (new — everything about what goes on the wire)
  internal/mockplane/respond_test.go  (new)
  internal/mockplane/respond_stub.go  (DELETE it — "rm", whole file, once respond.go compiles)
  internal/mockplane/routes_test.go   (11 http.StatusNotImplemented assertions across 6 test functions —
                                       TestServeRoute_MatchAndNoMatch x4, the same-table-across-requests
                                       test x3, and one each in the single-flight, CORS-on-501, HEAD and
                                       OpRowID tests. Stage 1 is FINISHED: the whole file is yours now,
                                       including fakeSpecSource — extend its document and variants map
                                       if an assertion needs a real schema to check against. Convert
                                       every 501 you find and only those.)
  internal/server/p1a_test.go         (1 whole-stack assertion: a known operation answers 501 naming its
                                       operationId)
DO NOT TOUCH internal/mockplane/plane_test.go. It has NO 501 assertion. Its TestServeHTTP_NotImplementedYet
asserts a 404 with code "not_implemented_yet" for POST /__mocker/state — the P1c stateful-resources
endpoint, deliberately unimplemented and explicitly out of this phase's scope. The name looks like your
target and is not; converting it would quietly delete a correct assertion that no gate would catch.

THE TWO FILES ABOVE ARE YOURS AND THEY ARE CURRENTLY RIGHT ABOUT A BEHAVIOUR YOU ARE DELETING. Convert
each 501 assertion into the generated-response assertion that replaces it. Do NOT delete the tests and do
NOT weaken them: they also carry CORS, HEAD, singleflight and route-precedence coverage that must survive
intact. A test that asserted "501 names the operationId" becomes "200 carries a body that matches the
operation's declared schema" — the same test, pointed at the new truth.
DO NOT edit runtime.go, routes.go, plane.go, internal/router or internal/specs — stage 1 owns those and
they are done. If one of them is genuinely wrong, record it in "deviations"; a later fix agent owns every
file and will apply it.

YOU IMPLEMENT serveGenerated. Read DESIGN.md lines 366-374 ("Тело, тип и статус") and 246-296 (§8) first.

1. CHOOSE THE VARIANT for the matched operation from rt.variants[route.OpRowID], by DESIGN §7 step 5's
   rule: the lowest numeric 2xx, then "2XX", then "default". "2XX" and "default" are NOT sendable codes —
   the row's HTTPStatus already carries what to send (2XX -> 200, default -> 200). Sending the literal
   string, or 0, is a blocker-grade defect; test it.
   No variants at all for a matched route: answer 200 with no body rather than 500 — the route exists,
   the document just declared nothing.
2. DEGRADED operations (variant.Degraded) answer an EMPTY 200 with no body. That is DESIGN §7's invariant
   for an operation that could not be parsed, and P1b is where it finally becomes implementable.
3. ACCEPT NEGOTIATION with an explicit fallback and a 406, per §9 "Тело, тип и статус". */* and a missing
   Accept take the variant's own media type. An Accept that excludes every media type the operation
   declares gets 406 with the standard error body — not a 200 in the wrong type. q-values: parse them well
   enough to honour q=0 as a refusal; you do not need a full RFC 9110 implementation, but say in your
   summary exactly what you implemented.
4. 204 and 205 NEVER have a body. HEAD never has a body — P1a's router already resolves HEAD against the
   GET route, so you receive a GET route and must suppress the body while keeping the headers a GET would
   have sent, including Content-Type and Content-Length.
5. DECLARED RESPONSE HEADERS via gen.Headers(variant, req). Never let a generated header value contain a
   CR or an LF — a header value from an attacker-controllable document is a response-splitting vector.
5a. settings.DelayMs: sleep for it before writing, clamped to something sane, and cancellable via the
   request context so a shutdown is not held hostage by it. It is in domain.Settings with a default, DESIGN
   §6 lists "задержка" as part of response assembly, and nothing in the tree applies it yet — this is where
   it belongs. The per-request delay of §12's session layer is P1c and is NOT yours.
6. settings.Envelope: when set, the body is wrapped as {"<envelope>": <body>} — in THIS layer, never in
   the generator. Not applied to an empty body, and not applied twice.
7. Build the gen.Request: Method, CanonicalPath (the RELATIVE one from the route), PathParams from
   router.Match.Params, Query from the URL, Status from the chosen variant, and ListFamily — the canonical
   path of the sibling list route when this route's canonical path ends in "/{}" and rt.table.HasCanonical
   reports the prefix exists for the same method. That field is what makes a detail card match its list
   row; getting it wrong silently produces two different people.
8. Non-JSON media types (this document has one text/csv response) get a placeholder body of the right
   type, not JSON with the wrong Content-Type.
9. A generation error is never a 500 to the user of a mock: log it with the operation and answer the most
   honest thing you can (an empty body of the declared type). A mock that 500s on its own schema is
   useless to the frontend that is trying to boot against it. Say what you chose in your summary.
10. Content-Type is set exactly once, Content-Length is consistent with what is written, and the body is
   never larger than cfg.MaxResponse (the generator already bounds it — do not truncate it again here,
   assert it).

Your tests build a Plane with a fake SpecSource — no database, no docker. Cover: variant choice across
{200,201,204,default}, 406, HEAD, 204, envelope on and off, a degraded operation, and a detail route
reaching the same object as its list row.`,
  { label: 'serve:respond', phase: 'Serve', model: 'sonnet', schema: SCHEMA })

const serve = [serveRuntime, serveRespond]
const serveOK = serve.filter(Boolean)
if (!serveRespond) log('WARNING: serve:respond returned nothing. If respond_stub.go is still on disk the phase still answers 501 — Gate 2 and the acceptance agent are told to check the tree, not the reports.')
logDeviations('Serve respond', [serveRespond].filter(Boolean))

// ---------------------------------------------------------------- phase 6 --
// Gate 2. The serving path is the first code in the project where an unauthenticated
// caller inside the contour drives a generator: DESIGN §15 says the mock plane is
// open by design, and §18 forbids a rate limit on it.
phase('Gate 2')

const gate2 = await runGate('Gate 2', [
  {
    label: 'gate2:correctness',
    prompt: `${gateCtx('internal/mockplane internal/router internal/specs/repo.go internal/specs/repo_test.go internal/gen/list.go internal/gen/list_test.go cmd/mocker/main.go')}
${INTENDED}

Under review: internal/mockplane (runtime.go, respond.go, the modified routes.go), the two new
internal/specs methods, router.HasCanonical, AND internal/gen/list.go — the list contract landed after
Gate 1 and this is its only section gate, so it is your scope too.
DESIGN §7 step 5 (lines 216-221), §8 (246-296), §9 "Тело, тип и статус" (366-374) and the list contract
inside §9 "Детерминизм" (299-336), §18 (1088-1105).

YOUR VECTOR: correctness on the serving path AND the list contract.
- The list contract, which the phase criterion rests on: is "total" really independent of the page (it
  comes from ListSize, not from len(items))? Does the item at global index offset+i come out identical on
  a page that starts at 0 and a page that starts at offset? Does a detail route reach the SAME object as
  its list row — via the family seedList with the id path parameter removed — for an id that is NOT in the
  first page? Is ?limit=1000000 clamped before anything is allocated, and is the clamp a constant rather
  than something derived from the request?
- Is a list detected by the array PROPERTY as well as by a top-level array? Measured: exactly one
  operation in the acceptance document returns a top-level array, so a detector that only handles that
  case finds one list in 130 operations and every other list silently degrades to a plain object.
- Variant choice: lowest numeric 2xx -> "2XX" -> "default". Can a literal "2XX" or "default" ever reach
  WriteHeader? Can a 0 status? What happens when an operation has ONLY error responses declared?
- Does a degraded operation really answer an empty 200, and does a 204 really carry no body and no
  Content-Length mismatch? Does HEAD suppress the body while keeping the headers?
- 406: is it reachable, and does a missing or */* Accept still get the declared type? Is a q=0 refusal
  honoured? Does the 406 path still set the CORS headers (§8: they go on 404s and 500s too — a CORS-less
  406 shows the browser a CORS error instead of the real reason)?
- ListFamily: is it computed from the ROUTE TABLE (HasCanonical) rather than from string surgery on the
  path? Does a detail route under a base path still find its family? A wrong family here means the card
  and the row are different objects — the exact failure DESIGN §9 says breaks the frontend first.
- The runtime cache: keyed by (workspace, revision); a settings edit bumps revision and therefore misses;
  singleflight really shared (N concurrent cold requests build ONCE, not N); LRU bounded by
  cfg.RuntimeCache; nothing mutated after publication. Run: go test ./internal/mockplane/... -race -count=2
- specs.Variants: ONE query for the whole spec, not N+1. Does it read parse_error and pointer correctly?
  Does it survive a spec with zero operations? Check the column names against
  internal/store/migrations/0001_init.sql (operations at 54, operation_responses at 70).
- openapi.Load on already-normalized bytes: is idempotence actually asserted by a test, or assumed?
- Anything in the request path that does per-request work that belongs on the cached runtime.
- Tests that assert nothing, or that would still pass with the implementation deleted.`,
  },
  {
    label: 'gate2:security',
    prompt: `${gateCtx('internal/mockplane internal/router internal/specs/repo.go internal/specs/repo_test.go internal/gen/list.go internal/gen/list_test.go cmd/mocker/main.go')}
${INTENDED}

Under review: internal/mockplane (runtime.go, respond.go, routes.go), internal/specs' two new methods,
internal/gen as it is reached from a request. DESIGN §15 (lines 917-982) and §18 (1088-1105).

YOUR VECTOR: security and resource exhaustion. THE MOCK PLANE IS OPEN — §15 says any caller inside the
contour reaches it with no credentials, and §18 forbids putting a rate limit on it. Every input below is
attacker-controlled: the path, the path parameters, the query string, the Accept header, the Host.
Assume an unauthenticated attacker inside the contour and report each finding as a concrete attack.
- ?limit=1000000, ?limit=-1, ?limit=abc, ?page=999999999, ?offset=-5, a 100 KB query string, and the same
  with per_page/size/cursor. Where exactly is the clamp, and does it happen BEFORE anything is allocated?
  An allocation proportional to an unclamped query integer is a blocker.
- A schema that references itself (this document has one). Can a request drive unbounded recursion or
  allocation? Is MaxDepth applied on every path into the walk, including through allOf branches, array
  items and additionalProperties? Is MaxBytes enforced during generation rather than after?
- Nested multiplication: a list of listSize items each containing a list of listSize items. What is the
  worst case a single request can cost, in bytes and in time? State the number.
  The budget code lives in internal/gen/schema.go, compose.go and values.go, which are NOT in your diff
  (they were Gate 1's section). READ THOSE FILES DIRECTLY for the budget questions — do not widen the
  diff to include them, and do not re-audit Gate 1's ground beyond what a request can actually reach.
- The runtime cache: can an attacker force unbounded cache growth or unbounded rebuilds — many slugs,
  many revisions, a workspace that does not exist? Is the cache bounded by cfg.RuntimeCache in entries,
  and does an entry hold a 347 KB document plus a resolver memo that can grow unboundedly with requests?
  A memo that grows per REQUEST rather than per DOCUMENT is a memory leak reachable from the open plane.
- Response splitting: a generated or spec-declared header value containing CR or LF. Is it rejected or
  sanitized before WriteHeader?
- Information leak: does any error body, log line or generated value echo the uploaded document's
  contents to an unauthenticated caller beyond what the mock plane exposes by design? Does an error
  mention file paths, SQL, or internal hostnames from the spec's servers[]?
- SQL: the two new specs methods — parameterized, or built by concatenation? Is specID ever
  attacker-controlled on the mock plane (it comes from the workspace row, so say whether that holds)?
- Does any new code path perform an OUTBOUND request? URL import is deferred and none should exist:
  no http.Get, no http.Client.Do, no net.Dial. If one exists it is a blocker.
Do NOT run "make smoke" — it rewrites .env and runs "docker compose down -v", and another agent may be
using the stack.`,
  },
], `${CTX_CORE}${CTX_SEED}${INTENDED}

THE SEAM AND THE GENERATOR'S API ARE FIXED — the acceptance test codes against both. Change a signature
only if a finding genuinely requires it, and report the new one in "contracts". The seed contract above is
here because this gate also covers internal/gen/list.go: a finding like "the card does not reach the row's
seed" cannot be fixed by someone who was never told what the seed layers are.

${SIG_SERVE}

${SIG_GEN}

HARD RULE 1 DOES NOT APPLY TO YOU: you run alone and own every file the findings name. Rules 2-5 still hold.`)

// ---------------------------------------------------------------- phase 7 --
// Accept. Two steps, strictly ordered: prove the generator against the real
// document, then prove the whole stack in docker. P0's lesson, banked in
// HANDOFF.md: "go test зелёный != фаза готова" — make smoke is the only check
// that exercises the phase criterion end to end.
phase('Accept')

const acceptSpec = await agent(`${CTX_CORE}${CTX_TEST}${CTX_SPEC}${CTX_SEED}
${INTENDED}

Every producer of P1b has landed and both gates have run. You write the ACCEPTANCE TEST: the proof that
the generator answers a real 130-operation document correctly, not just the fixtures its authors wrote.

THE API YOU ARE TESTING:

${SIG_GEN}

CONTRACTS AS ACTUALLY WRITTEN (later lines supersede earlier ones; a gate fix may have changed a
signature after the block above was authored, so READ internal/gen/gen.go — the code wins over both):
${filterContracts(contractsOf([kernel, ...valuesOK, gate1.fix, list, ...serveOK, gate2.fix].filter(Boolean)), /gen\.|func |type |var Err|Seed|ID(For)?Index|Options|Request|Response|Generator|List/)}

TWO THINGS YOU CANNOT INFER AND WOULD GET WRONG:
1. WRITE IT AS "package gen_test", not "package gen". internal/specs and internal/mockplane both import
   internal/gen, so an in-package test that imports either — and assertion 4 below needs a Plane, while
   the variants come from specs — is an import cycle that will not compile.
2. DO NOT HAND-BUILD SchemaPtr. It is NOT "#/paths/~1x/get/responses/200/content/application~1json/schema":
   the indexer anchors it at the last $ref hop of the RESOLVED response (internal/specs/index.go), which
   is the only form that resolves for the 221 of 419 responses that are $ref'd. Get variants the way the
   runtime does — import the document with specs.Repo and call Variants — exactly as
   internal/specs/acceptance_test.go already imports it. Hand-rolling the pointer fails silently on more
   than half the corpus: generation returns nothing and the test reads as "no schema declared".

YOU MAY ADD ONE DEPENDENCY, TEST-ONLY: github.com/santhosh-tekuri/jsonschema/v6.
  go get github.com/santhosh-tekuri/jsonschema/v6 && go mod tidy
It must be imported from _test.go files ONLY. Prove the shipped binary does not link it and paste the
output into "verified":
  go list -deps ./cmd/mocker | grep santhosh   # must print nothing
If the module cannot be fetched (no proxy, no network), STOP and report that in "deviations" rather than
hand-writing a validator — say so plainly and write every other assertion below without the oracle.

YOUR FILES:
  internal/gen/acceptance_test.go       (the main body of this work)
  internal/testspec/testspec.go         (new, tiny: the shared "find the acceptance document or skip"
                                         helper. internal/specs/acceptance_test.go already has a private
                                         copy that walks up to go.mod — move it here and make that file
                                         use it, so the next phase does not write a third copy.)
  internal/specs/acceptance_test.go     (edit ONLY to use the shared helper)

THE ASSERTIONS. Every one runs against ${SPEC_PATH}, and the test skips itself when it is absent:

1. SCHEMA CONFORMANCE — the headline. For EVERY operation with a 2xx response carrying a schema
   (measured: 114 of 130 — 113 with a JSON schema plus the single text/csv one), generate the body and
   validate it against that response's schema compiled with
   jsonschema/v6. Report the real counts: generated, validated, failed-with-reason. A failure is a test
   failure — do not thread a tolerance percentage through it. If a specific class of failure is a genuine
   OAS-3.0-vs-JSON-Schema-2020-12 dialect artefact (x-nullable, readOnly, discriminator), name that class
   explicitly, prove it is the dialect and not the generator with a concrete example, and exclude that
   class BY NAME with a comment — never by a count threshold.
2. DETERMINISM — with a FROZEN Options.Now, generate every operation's body twice, from two separately
   constructed Generators, and require byte equality. Then change only Options.Seed and require the
   bodies to differ, so the test cannot pass by generating constants.
3. THE LIST CONTRACT on GET /api/v1/broadcasts (items/total/limit/offset) and its detail route
   GET /api/v1/broadcasts/{broadcastId}:
   - ?limit=10&offset=0 and ?limit=10&offset=10 return disjoint sets and the SAME total;
   - ?limit=5&offset=5 returns exactly the tail of ?limit=10&offset=0;
   - take an id from a list row, request the detail route with it, and require the shared fields to be
     identical. This is the HALF of DESIGN §19's P1 criterion that P1b owns — "видит согласованные список
     и карточку". The other half, "фронт логинится", needs P1c's recipes and auth preset; do not claim
     the whole criterion is met;
   - ?limit=1000000 does not allocate a million items (assert the response size and the item count).
4. NEGOTIATION AND EMPTY BODIES, driven through the mock plane rather than the generator alone (build a
   Plane with a fake SpecSource over the real document, like internal/mockplane's tests do):
   - Accept: application/xml on a JSON operation -> 406; Accept: */* -> the declared type;
   - the 20 operations whose ONLY 2xx variant is a 204 answer no body. Measured and deliberate: 21
     operations declare a 204, but GET /api/v1/organizations/{organizationId}/invites declares 200 AND
     204, and the selection rule (lowest numeric 2xx) correctly picks the 200, which HAS a body. Assert
     at the level of the chosen VARIANT, not "this operation declares a 204 somewhere" — otherwise the
     assertion is false and the only ways out are a failing test or weakening it, both forbidden;
   - HEAD on a GET route answers no body with the same status;
   - the single text/csv response answers text/csv, not JSON.
5. BUDGETS — the self-referencing schema in this document generates and terminates. Assert with a test
   timeout, not patience. A generated body never exceeds Options.MaxBytes.

Report the real numbers in "measurements": operations attempted, bodies generated, bodies validated,
failures by reason, the largest body produced, and the wall-clock of generating all 130.
Set "passed" true ONLY if every assertion above passes. Set "smoke" to "not-applicable" — you do not run
make smoke, the next agent does, and leaving it blank reads downstream as a missing docker check.
Finish with gofmt/build/vet/test -race clean.`,
  { label: 'accept:spec', phase: 'Accept', model: 'sonnet', schema: ACCEPT_SCHEMA })

if (acceptSpec) {
  logDeviations('Accept', [acceptSpec])
  for (const m of acceptSpec.measurements || []) log(`measured: ${m}`)
  if (acceptSpec.passed === false) log('WARNING: the acceptance agent reports the criterion NOT met. The Review phase is told to treat that as the first thing to explain.')
} else {
  log('WARNING: the acceptance agent returned nothing. The stack check and the reviewers are told the phase criterion is UNPROVEN.')
}

const acceptStack = await agent(`${CTX_CORE}${CTX_TEST}
${INTENDED}

The acceptance test against the real document has run. You prove the SAME thing through docker, end to
end, because a green "go test" is not a finished phase — that is this project's banked lesson from P0,
where two defects survived the entire agent fleet and only "make smoke" caught them.

YOUR FILES:
  scripts/smoke.sh   (extend — do not rewrite what is there)
  README.md          (update the "Что делает P0, а что — ещё нет" section: a matched route now answers
                      generated data, not 404/501)
  Makefile           (only if a target genuinely needs to change)
  internal/admin/spec_handlers.go  (THREE STALE STRINGS ONLY, no logic: lines ~164, ~186 and ~221 tell the
                      user that URL import, Swagger 2.0 and YAML "land in P1b". After this phase that is
                      false — those three moved to their own later slice. USE PHASE-NEUTRAL WORDING ("a
                      later phase"), NOT another phase number: P1c is already spoken for by the recipes
                      engine, and a second wrong promise is not better than the first. Change no
                      behaviour: they still answer 400.)
  internal/openapi/openapi.go  (FOUR MORE OF THE SAME, same rule, no logic: lines ~41, ~74, ~204 and ~213
                      say YAML "is not decoded until P1b" and Swagger 2.0 "is converted in P1b" — two of
                      them in error messages a user actually sees. Retag phase-neutrally. Touch nothing
                      else in that file: it is P1a code, reviewed and green.)
  internal/admin/spec_handlers_test.go  (REQUIRED BY THE ABOVE, or you cannot finish green: line ~186
                      asserts strings.Contains(lower(message), "p1b") for the Swagger 2.0 case. Retag that
                      assertion AND its subtest name at ~167 AND its failure message at ~187 — all three
                      say "P1b", and leaving the name stale makes the test lie about what it checks. Same
                      test, same behaviour. Change nothing else in that file.)

scripts/smoke.sh today: brings the compose stack up on a throwaway password and a fresh volume, logs into
the admin API, creates workspace "alex", runs three Host-header checks, tears everything down in a trap.
Read it before touching it. It sets COMPOSE_ENV_FILES=/dev/null for a reason documented in its header.

ADD, keeping every existing check intact and the script idempotent on re-run:
1. Import a spec through the admin API (POST /api/specs — the document goes in a JSON string field, not
   multipart; P1a's CSRF gate hard-requires Content-Type: application/json). Use a SMALL inline document
   written by the script itself — 2 or 3 operations including a list and its detail route. Do NOT use
   ${SPEC_PATH}: it is gitignored, absent on a fresh clone, and 347 KB of internal API has no business in
   a smoke script. If the script cannot construct the document, skip that block loudly rather than
   silently.
2. Attach it to workspace "alex" and confirm /__mocker/health reports the spec and a bumped revision.
3. GET the list route on the workspace Host. Assert: 200, Content-Type application/json, a body that
   parses as JSON, and the declared list shape (items present, total present). A 501 anywhere is a
   FAILURE — killing it is the whole point of this phase.
4. GET the same route twice and assert the two bodies are BYTE-IDENTICAL. Determinism is the phase's
   core promise and this is the cheapest possible end-to-end proof of it.
5. Take an id from the list body with jq, GET the detail route with it, and assert the shared fields
   match the list row — the half of DESIGN §19's P1 criterion this phase owns, proved in shell. (The
   other half, "фронт логинится", waits on P1c's auth preset. Do not word the check as if it were the
   whole criterion.)
6. HEAD on the same route returns no body.

Then RUN it: "docker compose version" first; if docker is available run "make smoke" and paste the REAL
output into "verified", setting "smoke" to passed or failed. If docker is absent, set "smoke" to
"skipped-no-docker" — that is NOT the same as failed and downstream reviewers act on the difference. Never
claim a pass you did not observe.
Keep the script's style: set -euo pipefail, the same fail_count/report pattern, the same cleanup trap.`,
  { label: 'accept:stack', phase: 'Accept', model: 'sonnet', schema: ACCEPT_SCHEMA })

if (acceptStack) {
  logDeviations('Accept stack', [acceptStack])
  for (const m of acceptStack.measurements || []) log(`smoke: ${m}`)
  // This is the check this project learned to trust the hard way (HANDOFF.md: two
  // P0 defects survived the whole agent fleet and only make smoke caught them), so
  // a failure here has to reach the reviewers and the fix list, not just the run's
  // return value.
  if (acceptStack.smoke === 'failed') log('WARNING: make smoke FAILED. The reviewers are told to treat that as the first thing to explain, ahead of any code-level finding.')
  else if (acceptStack.smoke === 'skipped-no-docker') log('NOTE: docker was absent, so make smoke did not run. The end-to-end criterion is UNPROVEN — that is not the same as broken, and the reviewers are told so.')
} else {
  log('WARNING: the stack agent returned nothing — make smoke was not observed to pass. Treat the end-to-end criterion as unproven.')
}

// ---------------------------------------------------------------- phase 8 --
phase('Review')

const acceptance = [acceptSpec, acceptStack].filter(Boolean)
const touched = [kernel, ...valuesOK, gate1.fix, list, ...serveOK, gate2.fix, ...acceptance]
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

const reviewCtx = `Repo root is your CWD. Do not modify any file — you are auditing, not fixing. ("Any file"
is literal: almost nothing in this phase is tracked yet, so "tracked" would have exempted the whole tree.)
Running the test suite is expected.
TO SEE THE DIFF, SCOPED TO P1B — most files this phase produced are NEW, and plain "git diff HEAD" does
not show untracked files at all:
    git add -N . ; git diff HEAD -- ${P1B_PATHS}
The ";" is deliberate and so is the ".": with "&&" and a path list, a single path no agent happened to
create makes "git add -N" exit 128 having added nothing, and you would review an EMPTY diff believing the
phase produced nothing. Intent-to-add stages nothing for commit. Your co-reviewer runs the same command
concurrently: on ".git/index.lock: File exists", wait a second and retry.
Do NOT widen it: .claude holds the two workflow scripts (185 KB of untracked non-code) and diffing them
costs more than the code you are here for.
HEAD is phase P1a — committed, reviewed and green — so this diff is exactly what P1b produced. The files
it touches inside internal/openapi, internal/router, internal/specs and internal/admin are additions to
already-reviewed packages: read the surrounding code for context, review the change.
DESIGN.md is Russian and 87 KB — never read it whole; the section index is: §7 175-245, §8 246-296,
§9 297-411, §15 917-982, §18 1088-1105, §19 1106-1128.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it, query it with jq only.

Phase P1b of mocker was just written: the response generator (internal/gen — layered seed, schema walk,
composition, value sources, the list contract), the per-workspace runtime and the response path in
internal/mockplane that replaces P1a's 501, two new internal/specs queries, router.HasCanonical, and the
acceptance tests.
Files written or changed by this run: ${touched}
${criterionState}
${INTENDED}
Report ONLY defects you can point at with a file and a line. No praise, no style nits that do not change
behaviour. Empty findings array if your vector is clean.`

const reviews = await parallel([
  () => agent(`${reviewCtx}

YOUR VECTOR: correctness and bugs, across the whole phase. The two gates already audited internal/gen for
determinism and internal/mockplane for the serving path — do not repeat them mechanically; hunt what
falls BETWEEN the sections, and verify the claims rather than the code's intentions.
- Verify the phase criterion yourself with jq and a targeted Go test, not by trusting the acceptance
  agent's report: does a body generated for a real operation actually satisfy that operation's schema, and
  does a detail card really equal its list row? An acceptance test that skipped itself, or that asserts
  against fixtures instead of ${SPEC_PATH}, is a blocker — that exact failure happened in P1a and was
  caught by hand.
- The seam between gen and mockplane: is gen.Request built with the RELATIVE canonical path (the one
  stored in operations.path, without the base path)? A request built from the absolute path silently
  changes every seed the moment a workspace edits its base path, and every mock in it reshuffles.
- Status handling end to end: operations.http_status vs selector, 204 with a body, HEAD with a body,
  a "default"-only operation, an operation with only 4xx declared.
- The empty-200 invariant for a degraded operation (DESIGN §7): implemented, or claimed?
- Does anything reintroduce the 501? Does any path still answer 404 for a route that exists?
- gen concurrency under -race with real traffic shape: run
    go test ./... -race -count=1
  and, if the mockplane tests support it, a parallel test hitting one runtime from several goroutines.
- N+1 queries, a query per request, a document parsed per request — anything the runtime cache should
  have absorbed.
- errors compared with == instead of errors.Is; nil map writes; unchecked type assertions on document
  nodes (the document is user-supplied: a type assertion without the comma-ok form is a panic reachable
  from the open mock plane).
- Tests that assert nothing, are skipped in normal runs, or would still pass with the implementation
  deleted. Report every SKIP the suite prints and why it skipped.
If docker is available ("docker compose version"), run "make smoke" and report it if it fails. If docker
is absent, say so and move on — it is not a finding.`,
    { label: 'review:correctness', phase: 'Review', model: 'sonnet', schema: REVIEW_SCHEMA }),

  () => agent(`${reviewCtx}

YOUR VECTOR: security. P1b is the first phase where an UNAUTHENTICATED caller drives code that allocates
and computes on their behalf. DESIGN §15 (lines 917-982) says the mock plane is deliberately open to
everyone in the contour, and §18 (1088-1105) forbids a rate limit on it, so "an attacker could send many
requests" is not a defence — the per-request cost is the defence.
Gate 2 already looked at the serving path. Your job is the whole phase, and the parts of it a section
reviewer could not see: what an attacker can do by combining an uploaded DOCUMENT with a crafted REQUEST.
- The document is attacker-supplied (anyone with the shared admin password uploads it — §15 says the
  password is shared across the team, so treat "authenticated admin" as a low bar). The request is
  attacker-supplied by anyone at all. Find the combination that costs the server the most: a schema
  designed to multiply (nested arrays, allOf chains, a self-reference through an array) plus a query
  string that maximizes the page length. State the worst case in bytes and seconds, measured.
- Where exactly are limit/offset/page/cursor clamped, and is the clamp before or after the allocation?
- Can the resolver memo, the runtime cache, or any per-workspace structure grow with REQUESTS rather than
  with documents? That is a memory leak reachable without credentials.
- Response splitting: any header value from the document or from a generated value reaching WriteHeader
  with a CR or LF in it.
- Does a generated body or an error message leak anything the open plane does not already expose —
  internal hostnames from servers[], example values containing tokens, SQL, file paths, stack traces?
- Panic reachability: a type assertion, a slice index, a map write, or a division on data taken from the
  document, reachable from an unauthenticated request. The recover middleware turns it into a 500, which
  is a denial of that workspace's mock for whoever is depending on it — report it as a real finding.
- The new test-only dependency: is jsonschema/v6 genuinely test-only? Run
    go list -deps ./cmd/mocker | grep santhosh
  and report a non-empty result as a blocker. Check go.mod and go.sum are consistent (go mod verify).
- Still no outbound requests anywhere in the tree: no http.Get, no http.Client.Do, no net.Dial. URL import
  is deferred and an SSRF surface that nobody reviewed is a blocker.
- The admin plane's CSRF and auth gates: unchanged by this phase, or quietly widened by a new route?
Do NOT run "make smoke" — the correctness reviewer runs it, you two run concurrently, and it is not
concurrency-safe: it rewrites .env in the repo root and runs "docker compose down -v" on entry and in its
exit trap, so a second run tears down the first mid-assertion and manufactures a phantom failure.
Report each finding as a concrete attack: what the attacker sends, what they get. File and line required.`,
    { label: 'review:security', phase: 'Review', model: 'opus', schema: REVIEW_SCHEMA }),
])

const findings = reviews.filter(Boolean).flatMap((r) => r.findings || [])
for (const [i, r] of reviews.entries()) {
  if ((r?.findings || []).length >= 40) log(`WARNING: ${['review:correctness', 'review:security'][i]} returned the schema maximum of 40 findings — probably TRUNCATED.`)
  if (!r) log(`WARNING: ${['review:correctness', 'review:security'][i]} returned nothing — that vector did NOT run and the phase is unreviewed along it.`)
}
const minors = findings.filter((f) => f.severity === 'minor')
for (const m of minors) log(`minor: ${m.file}:${m.line} — ${m.summary}`)

// Gate majors never had a fix agent of their own — the gates only spawn one for blockers. Carrying them
// here is what stops "major" meaning "fix it" at the end of the run and "ignore it" at a gate, in the
// very packages three later sections were built on.
const gateMajors = [...gate1.findings, ...gate2.findings].filter((f) => f.severity === 'major')
// Blockers that survived a gate's own re-review: fixed once, still wrong.
const gateSurvivors = [...gate1.unresolved, ...gate2.unresolved]
const actionable = [...gateSurvivors, ...gateMajors, ...findings.filter((f) => f.severity === 'blocker' || f.severity === 'major')]
let outstanding = actionable
log(`Carrying ${gateMajors.length} unfixed gate majors and ${gateSurvivors.length} gate findings that survived a re-review into the fix list.`)
if (actionable.length > 20) log(`NOTE: ${actionable.length} actionable findings — a large fix list; nothing is dropped, but the fix agent gets all of them.`)
log(`Review: ${findings.length} findings — ${actionable.length} actionable, ${minors.length} minor (logged, not fixed)`)

if (outstanding.length === 0) {
  log('Nothing actionable: skipping Fix and Verify.')
  // Same shape as the final return, and counts rather than objects: the raw
  // findings array and the agents' "verified" fields hold whole go test dumps,
  // and the orchestrator re-bills whatever it reads on every later turn.
  return {
    phase: 'P1b',
    acceptance: acceptance.map((a) => ({ files: a.files, passed: a.passed, measurements: a.measurements })),
    findings: findings.length,
    actionable: 0,
    rounds: [],
    stillOpen: [],
  }
}

// --------------------------------------------------------------- phases 9-10 --
// Fix, then VERIFY the fix. The verify pass exists because Fix is otherwise
// terminal: a semantically wrong but compiling fix survives gofmt, build, vet
// and the tests, and nobody is watching this run. Bounded to two rounds.
const rounds = []
let lastOutput = ''
for (let round = 1; round <= 2 && outstanding.length > 0; round++) {
  phase('Fix')
  const list_ = outstanding
    .map((f, i) => `${i + 1}. [${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`)
    .join('\n')

  const fixed = await agent(`${CTX_CORE}${CTX_SEED}${INTENDED}

HARD RULE 1 DOES NOT APPLY TO YOU: you run alone and own every file. HARD RULE 2 (no new runtime
dependency), 3 (no migration) and 4 (never read the big files whole) STILL HOLD.

Round ${round}. Two reviewers audited the P1b tree. Apply the findings below, changing as little as
possible — every edit must map to a numbered finding.
${lastOutput ? `\nThe previous round ended NOT green. This was the failing output:\n${lastOutput}\n` : ''}
${list_}

RULES
- Verify each finding against the code FIRST. Reviewers false-positive. If a finding is wrong, do NOT
  "fix" it — record it in "deviations" with the evidence that it is wrong.
- A finding needing a design change rather than a code change goes to "todo", not into a hasty redesign.
- Add or extend a test for every real defect you fix, so it cannot come back silently.
- Never weaken, skip or delete a test to reach green. In particular: never make the acceptance test skip,
  and never relax a schema-conformance assertion into a percentage.
- Finish with all four clean and paste the real output of the last one into "verified":
    test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
  Then, only if "docker compose version" succeeds, run "make smoke" and paste its result too.`,
    { label: `fix:round${round}`, phase: 'Fix', model: 'sonnet', schema: SCHEMA })

  if (fixed) logDeviations(`Fix round ${round}`, [fixed])

  phase('Verify')
  const verdict = await agent(`Repo root is your CWD. Do not modify any file.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it; query it with jq only.
DESIGN.md is 87 KB — never read it whole (§7 175-245, §8 246-296, §9 297-411, §19 1106-1128).
${INTENDED}

A fix agent just claimed to have addressed the findings below. Its own report is self-assessed, so check
it. Three things, in this order:

1. Run, and report the REAL output:
     test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
   "green" is true only if all FOUR are clean. Also report every test that SKIPPED and why — a phase
   criterion that only runs under an environment variable is not a criterion.
2. Run "docker compose version". If it succeeds, run "make smoke" and set "smoke" to passed or failed; if
   docker is absent, set it to "skipped-no-docker". A skipped smoke does NOT make green false; a FAILING
   smoke means the phase criterion or P0's acceptance check broke and green must be false.
3. For EACH numbered finding below, decide: fixed, credibly rebutted, or still open. A fix that compiles
   is not a fix — check the semantics. Specifically distrust: a clamp moved after the allocation it was
   supposed to prevent; a determinism bug "fixed" by removing the assertion; a schema-conformance failure
   "fixed" by excluding the operation; the acceptance test made to skip; a 406 or 204 case "fixed" so its
   own test passes while DESIGN §9's rule changed; a data race silenced with a mutex that serializes the
   whole hot path; the test-only dependency leaking into ./cmd/mocker.

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
    // The verifier referenced a number that is not in this round's list — its
    // numbering drifted, so its per-finding verdicts cannot be trusted to
    // narrow anything. Keep the whole list rather than silently dropping work.
    log('Verifier numbering drifted from the list it was given; carrying ALL findings into the next round rather than trusting a partial mapping.')
    outstanding = actionable
  } else if (!verdict.green && kept.length === 0) {
    // Not green, yet nothing named as open: something broke that no finding
    // covers. Another blind fix round on an empty list would do nothing.
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
  phase: 'P1b',
  acceptance: acceptance.map((a) => ({ files: a.files, passed: a.passed, measurements: a.measurements })),
  findings: findings.length,
  actionable: actionable.length,
  rounds,
  stillOpen: outstanding.map((f) => `${f.file}:${f.line} — ${f.summary}`),
}

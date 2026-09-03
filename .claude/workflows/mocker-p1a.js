export const meta = {
  name: 'mocker-p1a',
  description: 'P1a of mocker (Go): OpenAPI import, lazy $ref resolver, operation index, route table, /api/specs',
  whenToUse: 'Run once, from the repo root, after P0 is green. First slice of phase P1 (DESIGN.md §7, §8).',
  phases: [
    { title: 'Foundation', detail: 'openapi (detect, normalize, lazy $ref resolver) and router (leaves, parallel), then the specs repo', model: 'sonnet' },
    { title: 'Gate 1', detail: 'review the import core and the router before three sections are built on them', model: 'sonnet' },
    { title: 'Index', detail: 'operations + operation_responses indexing, wired into Import', model: 'sonnet' },
    { title: 'Gate 2', detail: 'review the index against DESIGN §7 step 5 and against the real document', model: 'sonnet' },
    { title: 'Serve', detail: 'admin /api/specs, workspace spec_id, and the route table wired into the mock plane', model: 'sonnet' },
    { title: 'Integrate', detail: 'reconcile drift, wire main.go, make the whole tree build and vet clean', model: 'sonnet' },
    { title: 'Accept', detail: 'acceptance tests against the real 130-operation spec; full suite plus make smoke', model: 'sonnet' },
    { title: 'Review', detail: 'correctness (sonnet) + security (opus)', model: 'opus' },
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
log(`Acceptance spec: ${SPEC_PATH} (gitignored; every test that reads it skips itself when it is absent).`)
log('NO new OpenAPI dependency. DESIGN §2 rejects ready-made OpenAPI tooling as "заложник строгой мета-схемы" and rejects full dereference as "взрывается на рекурсии"; §7 requires a LAZY resolver, raw+normalized storage and an import that never fails. A strict model library fights all three, so the document is walked as a generic tree and the JSON-pointer resolver is hand-written.')
log('DEFERRED to P1b — Swagger 2.0 conversion. DESIGN §2 does say we write the converter ourselves, but §19\'s P1 row asks only for "Импорт (servers[], один файл)", the acceptance document is OAS 3.0.3, and there is no real 2.0 document to validate a converter against — it would ship exercised by nothing but its own fixtures. Detect() still recognises Swagger 2.0 and Load returns a clear ErrUnsupportedFormat naming the phase.')
log('DEFERRED to P1b — YAML input. There is no YAML decoder in go.mod or go.sum, and HARD RULE 2 forbids adding a dependency, so P1a imports JSON only. This is a real cut on the phase\'s headline feature and is called out here rather than buried in one agent\'s prompt: choosing a YAML dependency is a decision for P1b, not something an agent should make mid-run.')
log('DEFERRED to P1b — URL import. source:"url" answers 400. DESIGN §19\'s P1 row does not ask for it, it ships OFF by default (an empty MOCKER_URL_IMPORT_ALLOWLIST), and a correct implementation carries the whole SSRF surface: redirects, userinfo smuggling, DNS rebinding between check and dial, link-local and decimal-IP literals. The security reviewer is told to prove no outbound request exists anywhere in the tree.')
log('DEVIATION FROM DESIGN §7 step 3 — base path. §7 says the prefix is "первый сервер, variables раскрыты дефолтами", which assumes a root servers[]. The acceptance document has NO root servers[] and puts servers on each path item, disagreeing (97 localhost:8080, 9 api.example.com/api/v1, 4 empty). So the rule is extended: root servers wins; failing that, a consensus across path items; failing that, BaseAmbiguous and an empty base path plus a report warning. An empty base path is what makes the stored paths match the document, and the user can edit the hint in settings afterwards.')
log('DEVIATION FROM DESIGN §7 — the default $ref budget is 32, not 6. Measured on the acceptance document: the longest $ref hop chain reachable from an operation is 12 (two operations tie), and 28 of 130 operations exceed 6 hops. HONEST SCOPE OF THAT NUMBER: P1a\'s own resolution is shallow — the indexer follows a response to the first non-$ref node, at most about two hops — so 32 is forward-looking headroom for P1b\'s generator, which walks into schemas, rather than a fix P1a needs today. The cycle guard, not the budget, is what stops infinite recursion. DESIGN §7 should be amended after this phase.')
log('Matched-route-without-a-generator answers 501 naming the matched operationId. DESIGN\'s "пустой 200" invariant covers an UNPARSED operation, not a matched one; an empty 200 here would be indistinguishable from a real mock and would mislead the P1d frontend. P1b replaces the 501 with generated data.')
log('POST /api/specs takes the document as a JSON string field, not multipart. The admin CSRF gate hard-requires Content-Type: application/json (DESIGN §15, and §8: "На admin-плоскости парсер строгий — толерантный превращает POST в «простой» CORS-запрос и открывает CSRF"). Loosening that gate for one upload route would undo a P0 security invariant; a spec is text, so a JSON string costs nothing.')
log('Two endpoints beyond §14\'s list: GET /api/specs/{id}/operations (the "Спеки" screen must show what was imported) and DELETE /api/specs/{id} (an import that cannot be undone makes the screen a trap).')
log('OUT of P1a, logged so nobody reports them missing: resource_suggestions derivation (§7 step 6 — consumed only by §11, which is P3), auth-profile detection (§7 step 7 — §10, which is P1c), migrate-workspaces (§5 diff — P4), custom endpoints (P2; only the router\'s precedence branch for them is written now, because retrofitting a comparator is where ordering bugs hide), an operation with parse_error answering an empty 200 (§7 invariant — P1b, when there is a body to answer with), optional request validation (§8 settings.validateRequests — P2, it needs a JSON-schema dependency), the mock plane\'s request-body parsing rules (§8 — P1b, nothing reads a request body yet), and MOCKER_DEFAULT_SPEC auto-create on first login (§14 — P1c, it needs the auth preset).')
log('The Serve phase has NO dual gate, unlike Foundation and Index. Deliberate: its artifacts are consumed only by Integrate (compiler-guided) and Accept (the test suite), both of which surface a defect immediately and cheaply, and the Review phase audits Serve\'s diff before any fix lands. A gate there would be three more agents guarding a step that already fails loudly.')
log('Agent count: 17 typical, 21 worst case (both gate fixes plus two fix/verify rounds). Above the medium guideline of 15, deliberately: every agent past the producers is error-correction. Pre-flight review of earlier drafts of this script found five blockers, four of which would only have surfaced after an hour of agent work.')

// ---------------------------------------------------------------------------
// Signature blocks. Pasted verbatim into BOTH the agent that writes the package
// AND every agent that calls it: parallel agents cannot negotiate at runtime,
// and a swapped pair of string parameters compiles silently.
//
// THE IMPORT GRAPH, and the phase order that makes it buildable:
//   openapi -> (nothing of ours)     |  Foundation, parallel
//   router  -> (nothing of ours)     |  Foundation, parallel
//   specs   -> openapi, router       |  Foundation, AFTER both
//   mockplane -> router              |  Serve      (NOT specs: SpecSource is declared over router.Route)
//   admin   -> specs                 |  Serve
// A package is never written before something it imports. An earlier draft put
// specs in the same phase as its dependency and its build could not pass.
// ---------------------------------------------------------------------------

const SIG_OPENAPI = `  // package openapi  (internal/openapi) — A LEAF, it imports nothing of ours.
  type Format string
  const (
      FormatOAS31    Format = "oas31"
      FormatOAS30    Format = "oas30"
      FormatSwagger2 Format = "swagger2"
  )
  func Detect(raw []byte) (Format, error)              // by the openapi/swagger fields, DESIGN §7 step 1

  type BasePathOrigin string
  const (
      BaseFromRootServers BasePathOrigin = "root-servers"  // servers[] at the document root
      BaseFromPathServers BasePathOrigin = "path-servers"  // no root servers; every path item agreed
      BaseAmbiguous       BasePathOrigin = "ambiguous"     // path items disagreed -> base path left empty
      BaseAbsent          BasePathOrigin = "absent"        // no servers anywhere
  )

  type Warning struct { Pointer, Code, Message string }
  type Report struct {
      Format         Format
      BasePath       string
      BasePathOrigin BasePathOrigin
      Warnings       []Warning
      Operations     int
      Degraded       int
  }
  func (r *Report) Add(pointer, code, msg string)

  type Document struct{ ... }
  func Load(raw []byte) (*Document, *Report, error)
  func (d *Document) Format() Format
  func (d *Document) Raw() []byte                      // exactly the bytes handed in
  func (d *Document) Normalized() []byte               // canonical OAS 3.0 JSON
  func (d *Document) Root() map[string]any
  func (d *Document) Title() string
  func (d *Document) Version() string
  func (d *Document) BasePath() (string, BasePathOrigin)

  const DefaultRefBudget = 32                          // NOT DESIGN's 6 — see the launch log
  type Resolver struct{ ... }
  func NewResolver(d *Document, budget int) *Resolver
  func (r *Resolver) Resolve(pointer string) (any, error)   // "#/components/responses/X"
  func (r *Resolver) ResolveNode(node any) (any, error)     // follows a CHAIN of $ref to the first
                                                            // non-$ref node, memoized
  var ErrNotADocument, ErrUnsupportedFormat error       // not a document at all / Swagger 2.0 or YAML
  var ErrBudgetExhausted, ErrCycle, ErrPointerNotFound error`

const SIG_ROUTER = `  // package router  (internal/router) — A LEAF, it imports nothing of ours.
  type Route struct {
      OpRowID        int64       // specs.Operation.ID, the DATABASE row id. 0 for a custom endpoint.
      OperationLabel string      // the OpenAPI operationId, a human label. May be "". NEVER a key.
      Method         string      // upper case; HEAD is matched as GET by Match, never stored twice
      Path           string      // WITHOUT the base path, as stored in operations.path
      CanonicalPath  string      // parameters replaced by {} (DESIGN §8)
      Custom         bool        // custom endpoints win ties (DESIGN §8 rule 3) — none exist until P2
      SourceOrder    int64       // final tie-break; SQLite row order is not stable (DESIGN §8 rule 4)
  }
  type Match struct { Route *Route; Params map[string]string }
  type Table struct{ ... }
  func Build(routes []Route, basePath string) *Table   // the ONE place paths are glued (DESIGN §7 step 3)
  func (t *Table) Match(method string, segments []string) (*Match, bool)
  func (t *Table) Len() int
  func CanonicalPath(path string) string               // THE one implementation; nobody reimplements it
  func Conflicts(routes []Route) []string              // two CUSTOM routes sharing a canonical path
  // Match takes SEGMENTS, not a path string, so router imports nothing from mockplane. The caller
  // normalises with mockplane.NormalizeSegments. Gluing it the other way round would be an import cycle.`

const SIG_SPECS = `  // package specs  (internal/specs) — imports internal/openapi and internal/router.
  type Spec struct {
      ID int64; Name, Version string
      Format, Source, SourceRef, BasePath, Hash string
      CreatedAt time.Time; CreatedBy *int64
  }
  type Operation struct {
      ID, SpecID int64
      Method, Path, CanonicalPath string        // Path is stored WITHOUT the base path (DESIGN §7 step 3)
      OperationID, Summary, Tag *string         // OperationID is the OpenAPI label, never a key
      SourceOrder int64; Pointer string; ParseError *string
  }
  type Response struct {
      ID, OperationID int64
      Selector string; HTTPStatus int; IsDefault bool
      MediaType *string; StatusOrigin string; SchemaPtr *string
  }
  type ImportInput struct { Name, Source, SourceRef string; Document []byte; CreatedBy *int64 }
  type ImportResult struct { Spec *Spec; Report *openapi.Report; Operations []*Operation }
  type Repo struct{ ... }
  func NewRepo(db *store.DB, cfg *config.Config) *Repo
  func (r *Repo) Import(ctx context.Context, in ImportInput) (*ImportResult, error)
  func (r *Repo) ByID(ctx context.Context, id int64) (*Spec, error)
  func (r *Repo) List(ctx context.Context) ([]*Spec, error)
  func (r *Repo) Report(ctx context.Context, specID int64) (*openapi.Report, error)
  func (r *Repo) Operations(ctx context.Context, specID int64, limit, offset int) ([]*Operation, error)
                                                       // limit <= 0 means all; the SQL does the paging,
                                                       // so no caller ever loads every row to slice it
  func (r *Repo) Responses(ctx context.Context, operationID int64) ([]*Response, error)
  func (r *Repo) Routes(ctx context.Context, specID int64) ([]router.Route, error)
  func (r *Repo) AttachedWorkspaces(ctx context.Context, specID int64) ([]string, error)  // slugs
  func (r *Repo) Delete(ctx context.Context, id int64) error
  func (r *Repo) ReplaceOperations(ctx context.Context, tx *sql.Tx, specID int64,
                                   ops []*Operation, resp map[int][]*Response) error
  // ReplaceOperations' map key is the INDEX INTO ops, not an id and not source_order: row ids do not
  // exist until the insert happens. It takes the caller's *sql.Tx because Import does everything in ONE
  // transaction. It is idempotent: it deletes this spec's rows before inserting.
  var ErrNotFound, ErrDuplicate, ErrAttached, ErrTooLarge error
  // These lines give NAMES ONLY. Declare each with errors.New — a bare "var X error" is nil and
  // errors.Is would match nothing. ErrTooLarge is what Import returns for a document over cfg.MaxBody.
  var ErrNotADocument = openapi.ErrNotADocument       // DELIBERATE ALIAS. Import wraps openapi's error
  var ErrUnsupportedFormat = openapi.ErrUnsupportedFormat  // with %w, so errors.Is works through both
  // ErrAttached is a bare sentinel and cannot carry data: a handler that needs the workspace slugs calls
  // AttachedWorkspaces for them.`

// ---------------------------------------------------------------------------
// Context blocks, split so nobody pays for a section they do not need.
// ---------------------------------------------------------------------------

const CTX_CORE = `You are building phase P1a of "mocker", a self-hosted mock-backend service written in Go.

YOUR CWD IS THE REPO ROOT. Use repo-relative paths everywhere; never absolute ones.
Module path: git.sumka.site/yakov/mocker. Go 1.26.

THE SPEC IS DESIGN.md (Russian, ~1190 lines, 84 KB). NEVER read it whole.
Find sections with: rg -n '^## ' DESIGN.md    then Read only the line range you were told to read.
For a single fact prefer a targeted search (rg -n 'canonical_path' DESIGN.md) over a whole section.

P1a = OpenAPI import + operation index + route table + the /api/specs admin surface.
NOT P1a, do not build and do not report as missing: the data generator (§9), the recipes engine, the
auth preset (§10), stateful resources (§11), scenarios and checkpoints (§12, §17), the React UI, WS/SSE,
ZIP and multi-file specs, resource_suggestions derivation, migrate-workspaces, URL import (source:"url"
answers 400), SWAGGER 2.0 CONVERSION (P1b — Detect recognises it, Load refuses it), YAML INPUT (P1b —
JSON only, there is no YAML decoder in go.mod and HARD RULE 2 forbids adding one), optional request
validation (§8 settings.validateRequests), the mock plane's request-body parsing rules, and an operation
with parse_error answering an empty 200.

P0 IS WRITTEN, COMMITTED AND GREEN. This digest IS your contract: open only the files your own task
names, not all of them.

  internal/config/config.go   Config{Addr,BaseDomain,AdminHost,Routing,ReservedPrefix,AuthMode,
                              SharedPasswordHash,DefaultSpec,DataDir,MaxBody,MaxResponse,MaxEntities,
                              TrafficMaxBody,TrafficRetention,CheckpointRetention,RuntimeCache,
                              TrustProxy,URLImportAllowlist,LogLevel,Dev}
                              Load() (*Config,error); (c) IsWorkspaceHost(host) (slug string, ok bool);
                              (c) IsAdminHost(host) bool; (c) CookieSecure() bool == !Dev; (c) DBPath()
  internal/store/store.go     DB{W,R *sql.DB}; Open(ctx,path); (db) Migrate(ctx,*slog.Logger);
                              (db) Write(ctx, func(tx *sql.Tx) error) error; (db) Close().
                              W is a ONE-connection writer pool, R is read-only. Every multi-statement
                              write goes through db.Write so it shares one transaction AND one connection.
                              foreign_keys is ON — a FK violation is a real error, not a warning.
  internal/store/migrations/0001_init.sql   the FULL §13 schema. Every table P1a needs already exists:
                              specs, operations, operation_responses. DO NOT ADD A MIGRATION.
  internal/domain/            Settings{Seed,BasePath,ListSize,NullRate,Envelope,Identity,Auth,CORS,...};
                              DefaultSettings(); ParseSettings([]byte); NormalizeBasePath(string);
                              ValidateSlug; Slugify; UniqueSlug(name, taken func(string)(bool,error))
  internal/httpx/respond.go   JSON(w,status,v); Err(w,status,code,msg); ErrDetails(w,status,code,msg,details);
                              NoContent(w); CodeBadRequest/CodeUnauthorized/CodeForbidden/CodeNotFound/
                              CodeConflict/CodeTooLarge/CodeInternal
  internal/httpx/middleware.go Chain(h,...mw); Recover(log); RequestLog(log); MaxBody(int64)
  internal/workspaces/repo.go Workspace{ID,Slug,Name,SpecID *int64,OwnerID,ForkedFrom,ScenarioID,Revision,
                              Settings domain.Settings,CreatedAt,UpdatedAt};
                              CreateInput{Name,Slug,OwnerID,SpecID *int64,Settings *domain.Settings};
                              NewRepo(db); Create; ByID; BySlug; List; Update(ctx,id,mutate func(*Workspace) error);
                              Delete; SlugTaken; ErrNotFound/ErrSlugTaken/ErrSlugInvalid.
                              Update BUMPS Revision ITSELF (repo.go:253) — never bump it by hand inside
                              the mutate closure or every edit counts twice.
                              It has NO operations accessor and MUST NOT grow one — workspaces must not
                              learn about specs.
  internal/mockplane/plane.go Source interface { BySlug(ctx,slug) (*workspaces.Workspace,error) } — ONE
                              method, satisfied by *workspaces.Repo and by two test fakes;
                              New(cfg, src Source, log) *Plane; (p) ServeHTTP; (p) ServeSlug(w,r,slug);
                              NormalizeSegments(path) []string  <- SEGMENT-based, use THIS;
                              NormalizePath(path) string  <- flattens percent-decoded segments, never
                              use it for matching (that was a real P0 defect).
                              plane.go line ~139 holds the literal comment
                              "Step 5: everything else 404s until the route table arrives in P1".
  internal/admin/             server.go: New(cfg, sessions, ws *workspaces.Repo, db *store.DB, log) — it
                              ALREADY receives *store.DB, so anything needing a new repo constructs it
                              internally. security.go: the CSRF gate (Origin/Referer + token + strict
                              Content-Type: application/json). Copy workspace_handlers.go's shape.
  internal/server/server.go   New(cfg, admin http.Handler, mock *mockplane.Plane, log) http.Handler

HARD RULES
1. Write ONLY the files your task lists. Other agents own every other file, in parallel, right now.
   A COMPILE ERROR IN A PACKAGE YOU DO NOT OWN BELONGS TO AN AGENT RUNNING RIGHT NOW. Do not fix it, do
   not stub it, do not work around it — record it in "deviations". Your finish command names your
   packages exactly; do not widen it to ./... .
2. Do NOT edit go.mod / go.sum. Do NOT run "go get" or "go mod tidy". Do NOT add a third-party OpenAPI
   library: DESIGN §2 rejects ready-made OpenAPI tooling ("заложник строгой мета-схемы") and full
   dereference ("взрывается на рекурсии"). Walk the document as generic map[string]any / []any.
   (golang.org/x/sync IS already in go.mod and go.sum — singleflight may be imported directly.)
   If a build tells you to run "go get", that is a phase-ordering problem, not your problem: record it.
3. Do NOT add a migration. The whole §13 schema exists; if a column seems missing, you have misread it —
   check with: rg -n -A20 'CREATE TABLE <name>' internal/store/migrations/0001_init.sql
4. Match the existing style: package doc comment saying WHY, comments that give the reason for a decision
   rather than restating the code, errors wrapped with %w, table-driven tests.
5. Modern Go: slices/maps helpers, strings.Cut*, errors.Is/As, min/max, range-over-int, method-aware
   http.ServeMux patterns ("GET /api/specs/{id}" + r.PathValue). No third-party router.
6. Skills: if .claude/skills/ is non-empty, invoke ONLY the one or two named in your task. If the
   directory is empty (a fresh clone — skills are gitignored), skip this rule silently.
7. Times are stored as INTEGER unix seconds; documents as BLOB; JSON payloads as TEXT. Every exported
   symbol gets a doc comment. gofmt everything you write.
8. THE IMPORT INVARIANT (DESIGN §7): "импорт никогда не падает". A malformed operation is recorded with
   operations.parse_error and a Report warning; it never aborts the import and never panics. Load returns
   an error ONLY for input that is not a document (ErrNotADocument) or a format P1a defers
   (ErrUnsupportedFormat). A $ref that cannot be resolved — budget, cycle, missing target — is a Report
   WARNING, never a parse_error and never a failed import.
`

const CTX_TEST = `
TEST CONFIG — config.Config has NO defaults constructor, so a bare literal in a test is a trap:
MaxBody:0 makes httpx.MaxBody 413 every request AND makes the specs repo refuse every document,
ReservedPrefix:"" makes health unmatchable, and Dev:false sets a Secure cookie that a cookiejar over
plain-http httptest silently drops. Any test that builds a Config by hand MUST set at minimum:
  &config.Config{BaseDomain:"mock.local", AdminHost:"mocker.local", Routing:config.RoutingHost,
                 ReservedPrefix:"/__mocker", AuthMode:config.AuthShared, DataDir:t.TempDir(),
                 MaxBody:10<<20, MaxResponse:4<<20, RuntimeCache:32, Dev:true}
Admin POSTs in tests need all three of: Content-Type: application/json, Origin: http://mocker.local,
X-CSRF-Token from the login response — otherwise the admin plane's own rules answer 403.
`

// Every number here was verified on this machine with jq and a graph walker.
// An earlier draft of this block was wrong about $ref and would have mis-briefed
// every agent that read it.
const CTX_SPEC = `
THE REAL ACCEPTANCE SPEC: ${SPEC_PATH} in the repo root. It is GITIGNORED (an internal API document), so
it exists on this machine and will NOT exist in a fresh clone. Any test that reads it MUST resolve it via
os.Getenv("MOCKER_TEST_SPEC") with a fallback to "${SPEC_PATH}" and t.Skip() when the file is absent.
Never commit it, never copy it into testdata/.
NEVER Read or cat this file — it is 347 KB, roughly 90K tokens, and it will swamp your context. Query it
ONLY with jq, and only for the field you need.

Its MEASURED shape — verified with jq and a graph walker, not estimated:
  OpenAPI 3.0.3, 347486 bytes, 110 paths, 130 operations (51 GET, 53 POST, 14 DELETE, 10 PUT, 2 PATCH)
  every operation HAS an operationId, none duplicated; every operation has a 2xx response

  $ref IS EVERYWHERE — 781 of them, 587 inside .paths. This is the load-bearing fact of the phase:
    221 of the 419 response entries under path operations ARE a $ref. 220 have "$ref" as their only key;
      one (DELETE /api/v1/account, 200) carries a "description" sibling. A $ref WITH SIBLINGS IS STILL A
      $ref — follow it and ignore the siblings (OAS 3.0 says siblings are ignored). Do not test for
      "exactly one key".
    51 distinct components/responses are referenced and all of them carry content
    parameters are $ref'd too
    179 components.schemas, 175 of them referenced
    a RECURSIVE schema exists: components.schemas.FeedbackSection.properties.children.items.$ref points
      back at FeedbackSection. Exactly one operation reaches it: GET /api/v1/feedback/section
    the longest $ref hop chain reachable from an operation is 12 (two operations tie, e.g.
      GET /api/v1/achievements/{achievementId}); 28 of 130 operations exceed 6 hops
  Anything that reads a response WITHOUT resolving $ref first sees 221 empty responses and records NULL
  media_type and NULL schema_ptr for them. That is the single most likely way to fail this phase, and
  every hand-written fixture test still passes while it happens.

  NO root servers[]. servers sits on the PATH ITEM: 97 paths -> http://localhost:8080 (path component ""),
    9 paths -> https://api.example.com/api/v1 (path component "/api/v1"), 4 paths -> an EMPTY array.
    They disagree, so the base path is ambiguous and resolves to "".
  "default" appears as a response code 106 times; codes seen: 200,201,204,400,401,403,404,409,410,413,
    422,423,429,500. 17 response entries resolve to NO content at all: 16 of them 204 and one a 200
    (DELETE /api/v1/requests/{requestId}). Allow a NULL media_type on exactly those 17 — never key the
    allowance on "the status is 204", which is wrong for one of them and invites hard-coding the document.
  MEDIA TYPES, and note WHERE they live — this is where an assertion goes wrong if you skim:
    inline responses under paths: application/json 181, and NOTHING else
    components/responses:         application/json 51, text/csv 1 (that one HAS a schema,
                                  {"type":"string","format":"binary"}, and is referenced exactly once)
    request bodies:               application/json 44, multipart/form-data 2, text/plain 3
                                  — request bodies are NOT indexed by P1a at all
  allOf 11, oneOf 1, anyOf 0, discriminator 0, nullable 5, example 17, examples 0
`

// Deliberate deviations, handed to every REVIEWER. Without this a reviewer reads
// DESIGN, sees the script contradicting it, files a blocker, and a fix agent
// "corrects" the code back into something the acceptance test then rejects.
const INTENDED = `
INTENDED DEVIATIONS — these are decided, not defects. Do NOT report them, and do NOT "fix" them:
- DESIGN §7 step 2 says Swagger 2.0 is converted "через swagger2openapi". That is a stale Node-era
  reference, superseded by §2 ("его пишем сами"), and the converter itself is DEFERRED to P1b anyway:
  Detect recognises Swagger 2.0 and Load refuses it with ErrUnsupportedFormat.
- YAML input is deferred to P1b. There is no YAML decoder in go.mod and adding one is forbidden.
- URL import (source:"url") is deferred to P1b and answers 400. No outbound request should exist in the
  tree at all.
- DESIGN §7 step 3 says the base path is "первый сервер". That assumes a root servers[]; the acceptance
  document has none and puts disagreeing servers on each path item. The rule is deliberately extended to
  root -> path-item consensus -> BaseAmbiguous with an empty base path. An empty base path here is
  CORRECT, not a bug.
- The $ref budget is 32, not DESIGN's 6. Measured: chains of 12 exist and 28 of 130 operations exceed 6.
- A matched route answers 501 naming the operationId. DESIGN's "пустой 200" invariant is about an
  UNPARSED operation, not a matched one. P1b replaces the 501 with generated data.
- POST /api/specs takes JSON with the document as a string field, not multipart, so the admin CSRF gate
  keeps its strict Content-Type check.
- Two endpoints exist that DESIGN §14 does not list: GET /api/specs/{id}/operations (the "Спеки" screen
  must show what was imported) and DELETE /api/specs/{id} (an import that cannot be undone makes the
  screen a trap). Both are deliberate.
- DESIGN §7 calls one column "response_selector"; the migration and the code call it "selector". The
  migration is P0 and frozen, and renaming it would need a migration, which is forbidden.

DELIBERATELY ABSENT from P1a — do NOT report these as missing: the data generator (§9), the recipes
engine, the auth preset (§10), stateful resources (§11), scenarios and checkpoints (§12, §17), the React
UI, WS/SSE, ZIP and multi-file specs, resource_suggestions derivation and GET /api/specs/:id/suggestions
(§7 step 6 -> §11 -> P3), auth-profile detection (§7 step 7 -> §10 -> P1c), migrate-workspaces (§5 -> P4),
custom endpoints (P2), an operation with parse_error answering an empty 200 (P1b), optional request
validation (§8 settings.validateRequests -> P2), the mock plane's request-body parsing rules (P1b), and
MOCKER_DEFAULT_SPEC auto-create on first login (§14 -> P1c).
`

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
    todo: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'deliberately left for P1b+' },
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
    for (const d of r.deviations || []) log(`${label} deviation: ${d}`)
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
  const kept = lines.split('\n').filter((l) => re.test(l))
  return kept.length ? kept.join('\n') : '(nothing reported — the signature block above IS the contract)'
}

// ---------------------------------------------------------------- phase 1 --
phase('Foundation')

// openapi and router are both leaves — they import nothing of ours and nothing
// of each other, so they are genuinely parallel. specs imports both and runs
// after. An earlier draft ran all three at once and none of them could build.
const leaves = await parallel([
  () => agent(`${CTX_CORE}${CTX_SPEC}
YOUR TASK: the openapi package — load, detect, normalise, and the lazy $ref resolver.
Files you own (create them):
  internal/openapi/openapi.go
  internal/openapi/normalize.go
  internal/openapi/resolver.go
  internal/openapi/openapi_test.go
  internal/openapi/testdata/*.json
Skills (if present): golang-safety, golang-testing.

Read DESIGN.md "## 7. Импорт спеки" (rg -n '^## 7' DESIGN.md, then read ~40 lines) IN FULL. It is short
and every sentence is a requirement — except step 2 (Swagger 2.0), which P1a defers.

IMPLEMENT EXACTLY THESE SIGNATURES. Three later agents compile against them:
${SIG_OPENAPI}

REQUIREMENTS THAT ARE NOT NEGOTIABLE
- Load: detect by the "openapi"/"swagger" fields (§7 step 1). A Swagger 2.0 document, or YAML input,
  returns ErrUnsupportedFormat with a message naming P1b. YAML and rubbish both fail the JSON decode, so
  tell them apart explicitly: input that does not parse as JSON but whose first non-blank line matches
  ^\s*(openapi|swagger)\s*: is YAML -> ErrUnsupportedFormat; anything else that does not parse is
  ErrNotADocument — do NOT write a converter and do NOT add a YAML
  decoder. JSON OAS 3.0/3.1 is what P1a imports.
  Raw() returns the caller's bytes UNCHANGED — §7 stores raw + normalized and the hash is over raw.
- Base path (§7 step 3), extended deliberately because the acceptance document breaks the literal rule:
  the prefix is the PATH COMPONENT of a server URL with {variables} expanded from their defaults.
  Root servers[] wins -> BaseFromRootServers. With no root servers[], look at the path items: if every
  path item that HAS a non-empty servers array agrees on the same path component, use it ->
  BaseFromPathServers; if they disagree -> base path "" and BaseAmbiguous plus a Report warning naming
  both candidates; no servers anywhere -> "" and BaseAbsent.
  NEVER prefix the stored path. The base path is a HINT that lands in specs.base_path.
- The resolver is LAZY and memoised, with a depth budget (DefaultRefBudget = 32) and a set of visited
  pointers PER BRANCH — not a global visited set, which would wrongly reject a schema legitimately
  referenced twice from different branches (175 of the acceptance document's 179 schemas are referenced,
  many more than once). Budget exhaustion returns ErrBudgetExhausted; a cycle returns ErrCycle. Neither is
  fatal: the caller records a warning. Resolve must never recurse without a bound and never stack-overflow.
  ResolveNode follows a CHAIN of $ref to the first non-$ref node. A $ref node with sibling keys is still a
  $ref: follow it, ignore the siblings.
  The memo must not let a cached result smuggle a value past the budget on a deeper branch.
- Dialect normalisation (§7 step 4), all three: boolean exclusiveMinimum/exclusiveMaximum -> the numeric
  form; "example" -> "examples"; "nullable: true" -> BOTH an "x-nullable" marker for the generator AND an
  honest union type for the validator. DESIGN says one representation alone is not acceptable.

TESTS: table-driven, with fixtures you write. Cover at minimum:
  detect for 3.1 / 3.0 / 2.0 / a JSON object that is none of them / not JSON at all;
  Swagger 2.0 and YAML both refused with ErrUnsupportedFormat, not a panic and not a silent import;
  a $ref chain LONGER than the budget -> ErrBudgetExhausted, and the caller can carry on;
  a CYCLIC fixture (A -> B -> A) -> ErrCycle, no hang, no stack overflow — this is the test that matters
    most; the acceptance document contains exactly this shape;
  the same $ref reached twice from two different branches -> resolves BOTH times (guards the per-branch
    visited set against a global one);
  a chain of 12 hops resolving successfully under the default budget;
  a $ref node carrying a "description" sibling -> still followed;
  a pointer that escapes or misses: "#/nope/nope", a pointer with ~0 and ~1 escapes;
  base path: root servers; no root but agreeing path servers; no root and DISAGREEING path servers; an
    empty servers array; a server URL with a {variable} and a default;
  all three dialect normalisations, asserted on the normalised output.
Finish with EXACTLY these, scoped to your own package:
  test -z "$(gofmt -l internal/openapi)" && go build ./internal/openapi/ && go vet ./internal/openapi/ && go test ./internal/openapi/ -race -count=1`,
    { label: 'openapi', phase: 'Foundation', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${CTX_CORE}
YOUR TASK: the route table. Files you own (create them):
  internal/router/router.go
  internal/router/router_test.go
Skills (if present): golang-design-patterns, golang-testing.

Read DESIGN.md "## 8. Роутер" (rg -n '^## 8' DESIGN.md, then read ~50 lines) IN FULL. The ordering rules
and the canonical_path semantics are stated there exactly and are not open to interpretation.

IMPLEMENT EXACTLY THESE SIGNATURES. The specs repo, the indexer and the mock plane all compile against them:
${SIG_ROUTER}

YOUR PACKAGE IMPORTS NOTHING OF OURS. Match takes SEGMENTS precisely so that internal/router does not
import internal/mockplane: mockplane imports router, and the other direction would be a cycle. Do not
import mockplane for NormalizeSegments — the caller normalises and hands you the segments.

REQUIREMENTS THAT ARE NOT NEGOTIABLE
- Build is THE ONE PLACE the base path is glued to a route path (DESIGN §7 step 3: "Склейка происходит
  ровно в одном месте — при сборке маршрутов рантайма"). Route.Path stays relative; the table matches
  against basePath + Path. Change the base path, rebuild, and every override still works because its key
  was never absolute.
- CanonicalPath is THE one implementation of "replace every {param} with {}". The indexer calls it to fill
  operations.canonical_path — that is why it is exported. Nobody else may reimplement it: §8 makes
  canonical_path the key for override and conflict semantics in P2, and two implementations WILL drift.
- Sort order, exactly DESIGN §8's four rules in this priority: (1) more static segments wins;
  (2) at equal count, a static segment beats a parameter at the leftmost position where they differ;
  (3) at equal specificity a Custom route wins; (4) then SourceOrder ascending.
  Implement rule 3 now even though no custom routes exist until P2: it is one comparator branch, and
  retrofitting a comparator is where ordering bugs hide.
- A {param} matches exactly one segment and never across a "/". Params are returned percent-DECODED.
- HEAD matches a GET route (DESIGN §8: "HEAD матчится как GET"). Resolve it in Match; never store a second
  row for it.
- Conflicts reports two CUSTOM routes sharing a canonical path (DESIGN §8: the second cannot be created).
  A custom route canonically equal to a SPEC operation is NOT a conflict — it is an override, and it must
  sort ahead of the spec route.
- The table is immutable after Build and safe for concurrent Match from many goroutines, with no mutex on
  the read path. Do not retain a slice or map the caller can mutate afterwards.

TESTS: table-driven. Cover: /users/{id} vs /users/me (static wins); /a/{x}/c vs /a/b/{y} (leftmost static
wins); a custom route beating a spec route at equal specificity; SourceOrder as the final tie-break; HEAD
resolving to the GET route; a parameter NOT matching two segments; params returned decoded; base path
applied at Build and not baked into Route (build the same routes with two different base paths, assert
both match); CanonicalPath with zero, one and two parameters; Conflicts finding a custom/custom clash and
NOT flagging a custom/spec override; and a concurrent test (-race, several goroutines calling Match)
proving the read path is lock-free and safe.
Finish with EXACTLY these, scoped to your own package:
  test -z "$(gofmt -l internal/router)" && go build ./internal/router/ && go vet ./internal/router/ && go test ./internal/router/ -race -count=1`,
    { label: 'router', phase: 'Foundation', model: 'sonnet', schema: SCHEMA }),
])

const leavesOK = leaves.filter(Boolean)
if (leavesOK.length < 2) {
  log(`ABORT: Foundation leaves produced ${leavesOK.length}/2. The specs repo imports both.`)
  return { error: 'foundation leaves incomplete', leavesOK }
}
logDeviations('Foundation', leavesOK)

const specsRepoAgent = await agent(`${CTX_CORE}${CTX_TEST}
YOUR TASK: the specs repository — persistence for an imported document. Files you own (create them):
  internal/specs/repo.go
  internal/specs/repo_test.go
Skills (if present): golang-database, golang-testing.

Read the tables you write with a targeted search, NOT by opening the whole migration:
  rg -n -A14 'CREATE TABLE specs' internal/store/migrations/0001_init.sql
  rg -n -A16 'CREATE TABLE operations' internal/store/migrations/0001_init.sql
  rg -n -A12 'CREATE TABLE operation_responses' internal/store/migrations/0001_init.sql
and DESIGN.md §7's "Инварианты" paragraph.

IMPLEMENT EXACTLY THESE SIGNATURES. The indexer, the admin API and the mock plane compile against them:
${SIG_SPECS}

You CALL internal/openapi and internal/router, both just written. Their contracts:
${SIG_OPENAPI}

${SIG_ROUTER}

And their signatures AS ACTUALLY WRITTEN:
${contractsOf(leavesOK)}

THE INDEX ITSELF IS NOT YOURS. Another agent writes Index() in the next phase and wires it into Import
then. You declare and implement ReplaceOperations (pure SQL) and have Import call it with an empty slice
for now. Say so in "todo".

REQUIREMENTS THAT ARE NOT NEGOTIABLE
- Deduplicate by the hash of RAW (§7 "дедупликация по хешу raw"): sha256, hex. A second import of
  byte-identical input returns the EXISTING spec and ErrDuplicate, and writes nothing.
- The whole import is ONE store.Write transaction, and ReplaceOperations takes that same *sql.Tx.
  specs + operations + operation_responses must never be half-written.
- Import wraps openapi's errors with %w so errors.Is(err, ErrNotADocument) and
  errors.Is(err, ErrUnsupportedFormat) both work through the alias declared in the signature block.
- Report is NOT stored — there is no column and HARD RULE 3 forbids a migration. Repo.Report RE-DERIVES it
  by running openapi.Load over the stored raw bytes, then merges in the persisted per-operation half from
  operations.parse_error. Document in the doc comment that this is a re-derivation, not a cache.
- Operations pages IN SQL with LIMIT/OFFSET; limit <= 0 means all. A handler must never have to load every
  row to slice it.
- Delete: workspaces.spec_id REFERENCES specs(id) with NO ON DELETE clause and foreign_keys is ON, so
  deleting an attached spec raises a constraint error. Check AttachedWorkspaces first and return
  ErrAttached; only an unattached spec is deleted, cascading its operations.
- Routes(ctx, specID) builds []router.Route from the operations rows, filling ALL SEVEN fields:
  OpRowID = operations.id, OperationLabel = operations.operation_id or "" when NULL, Method, Path and
  CanonicalPath straight from their columns, Custom = false, SourceOrder = source_order.
- Size limit: refuse a document larger than cfg.MaxBody with a typed error BEFORE parsing it.
- Never log the document body: it may carry internal hostnames or secrets in examples.

TESTS: real SQLite via store.Open+Migrate in t.TempDir(). Cover: import stores raw and normalized
unchanged; hash dedup returns the same id and ErrDuplicate; base path from the report lands in
specs.base_path; ByID/List; Operations paging (limit, offset, limit<=0); Delete on an unattached spec
cascades operations (assert rows gone); Delete on an ATTACHED spec returns ErrAttached and deletes
nothing; a document over the size limit is refused; a Swagger 2.0 document surfaces ErrUnsupportedFormat
through errors.Is; ReplaceOperations is idempotent (calling it twice leaves one set of rows); Report
survives reopening the database (re-derivation works from raw alone); Routes fills every field.
Finish with EXACTLY these, scoped to your own package:
  test -z "$(gofmt -l internal/specs)" && go build ./internal/specs/ && go vet ./internal/specs/ && go test ./internal/specs/ -race -count=1`,
  { label: 'specs-repo', phase: 'Foundation', model: 'sonnet', schema: SCHEMA })

if (!specsRepoAgent) {
  log('ABORT: the specs repo agent returned nothing. The index, the admin API and the plane all need it.')
  return { error: 'specs repo agent returned nothing', leavesOK }
}
logDeviations('specs-repo', [specsRepoAgent])

let foundationOK = [...leavesOK, specsRepoAgent]

// ------------------------------------------------------------------ gate 1 --
phase('Gate 1')

const gateCtx = `Repo root is your CWD. READ-ONLY: do not modify any file.
DESIGN.md is Russian and 84 KB — NEVER read it whole. Find sections with rg -n '^## ' DESIGN.md and read
only the range you need.
${INTENDED}`

const gate1 = await parallel([
  () => agent(`${gateCtx}
Three packages were just written: internal/openapi, internal/router, internal/specs. Four more sections
are about to be built on them, so a defect here is cheapest to catch now.

YOUR VECTOR: the import core (internal/openapi and internal/specs) — correctness AND robustness against a
hostile document. Check specifically:
- the resolver's budget and cycle guard: is the visited set PER BRANCH (a global set wrongly rejects a
  schema referenced twice from different branches)? Can any input make it recurse without a bound? Can a
  memo hit smuggle a result past the budget on a deeper branch? Does ResolveNode follow a CHAIN of $ref to
  a non-$ref node, or only one hop? Does it still follow a $ref node that carries sibling keys?
- base path derivation. Note the extended rule in the INTENDED list above — an empty base path with
  BaseAmbiguous is the CORRECT outcome for a document whose path items disagree, not a defect.
- operations.path must be stored WITHOUT the base path. Any code that prefixes it is a blocker.
- the import transaction: is everything inside ONE store.Write, and does ReplaceOperations use that same
  *sql.Tx rather than opening its own?
- hash dedup over RAW bytes, not normalised. Errors wrapped with %w so errors.Is works through the alias.
- Delete against an attached spec: checked first, or a raw FK constraint error handed to the caller?
- Operations paging done in SQL rather than by loading every row.
- can any document make the parser or resolver PANIC? Type assertions on map[string]any without the
  two-value form, indexing []any without a length check, nil map writes. Is the document size capped
  BEFORE parsing, against cfg.MaxBody? Does any error, log line or warning echo the document body?
- tests that assert nothing, or would still pass with the implementation deleted.
Run: go test ./internal/openapi/ ./internal/specs/ -race -count=1 and report anything failing or skipped.
Report only defects you can point at with file and line.`,
    { label: 'gate1:import', phase: 'Gate 1', model: 'sonnet', schema: REVIEW_SCHEMA }),

  () => agent(`${gateCtx}
Three packages were just written: internal/openapi, internal/router, internal/specs.

YOUR VECTOR: the router against DESIGN §8. Read "## 8. Роутер" first (rg -n '^## 8' DESIGN.md, ~50 lines).
Check the comparator implements all four rules in the stated priority, including the leftmost-static rule
and the custom-wins tie-break; that the sort is stable where it must be and falls through to SourceOrder
deterministically; that a {param} cannot match across a "/"; that Build is the ONLY place a base path is
glued and Route.Path is left relative; that HEAD resolves to GET without a second row; that
CanonicalPath's rule matches what §8 says and that nothing else in the tree reimplements it; that
Conflicts distinguishes a custom/custom clash from a custom-over-spec override; and that the table is
genuinely immutable and lock-free after Build — look for a slice or map shared with the caller and mutated
later, and for a Route pointer handed out that a caller could write through.
Run: go test ./internal/router/ -race -count=1
Report only defects you can point at with file and line.`,
    { label: 'gate1:router', phase: 'Gate 1', model: 'sonnet', schema: REVIEW_SCHEMA }),
])

let gate1Fix = null
const gate1Findings = gate1.filter(Boolean).flatMap((r) => r.findings || [])
const gate1Blockers = gate1Findings.filter((f) => f.severity === 'blocker')
log(`Gate 1: ${gate1Findings.length} findings, ${gate1Blockers.length} blockers`)
for (const f of gate1Findings.filter((f) => f.severity !== 'blocker')) log(`gate1 ${f.severity}: ${f.file}:${f.line} — ${f.summary}`)

if (gate1Blockers.length > 0) {
  const list = gate1Blockers.map((f, i) => `${i + 1}. ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`).join('\n')
  gate1Fix = await agent(`${CTX_CORE}${CTX_SPEC}${INTENDED}
You own internal/openapi/, internal/router/ and internal/specs/ for this task only.
Do NOT write a real-document test: the Accept phase owns internal/specs/acceptance_test.go and its
MOCKER_TEST_SPEC helper, and a second helper in the same package is a redeclaration. Two reviewers found
blockers in the foundation that four more sections are about to be built on. Verify each against the code
FIRST — reviewers false-positive; if a finding is wrong, say so in "deviations" with the evidence instead
of "fixing" it. Add a test for every real defect you fix.

${list}

Finish with: test -z "$(gofmt -l internal/openapi internal/router internal/specs)" && go build ./internal/openapi/ ./internal/router/ ./internal/specs/ && go vet ./internal/openapi/ ./internal/router/ ./internal/specs/ && go test ./internal/openapi/ ./internal/router/ ./internal/specs/ -race -count=1`,
    { label: 'gate1:fix', phase: 'Gate 1', model: 'sonnet', schema: SCHEMA })
  if (gate1Fix) {
    logDeviations('Gate 1 fix', [gate1Fix])
    foundationOK = [...foundationOK, gate1Fix]
  }
}

// ---------------------------------------------------------------- phase 2 --
phase('Index')

const asWritten = contractsOf(foundationOK)

const indexer = await agent(`${CTX_CORE}${CTX_TEST}${CTX_SPEC}
YOUR TASK: the operation indexer — turn a normalised document into operations + operation_responses rows.
Files you own:
  internal/specs/index.go        (create)
  internal/specs/index_test.go   (create)
  internal/specs/repo.go         (MODIFY, exactly two changes: (a) have Import call Index and pass the
                                  result to ReplaceOperations; (b) extend Repo.Report to re-run Load AND
                                  Index. HARD RULE 1 exception — the agent that wrote repo.go finished in
                                  a previous phase and nobody else is editing it now.)
Skills (if present): golang-database, golang-testing.

Read DESIGN.md §7 step 5 ("Индексация операций") word by word — the response-selection rule is stated
exactly, and three columns exist precisely because "2XX" and "default" are not sendable status codes.

The packages you build on are written. Their signatures AS ACTUALLY WRITTEN:
${asWritten}

And the contracts you were promised:
${SIG_OPENAPI}

${SIG_SPECS}

IMPLEMENT
  func Index(doc *openapi.Document, res *openapi.Resolver, rep *openapi.Report) (ops []*Operation, resp map[int][]*Response)
  // the map key is the INDEX INTO ops, because row ids do not exist until the insert.

REQUIREMENTS THAT ARE NOT NEGOTIABLE
- RESOLVE $ref BEFORE READING A RESPONSE. This is the single most likely way to fail this phase: in the
  acceptance document 221 of 419 response entries are a $ref (220 with "$ref" as their only key, one with
  a "description" sibling — a $ref with siblings is still a $ref). Reading .content off such a node gives
  nil, and you would write NULL media_type and NULL schema_ptr for more than half the responses while
  every hand-written fixture test passes. Call res.ResolveNode on the response node before inspecting it.
  A resolve failure — budget, cycle, missing target — is rep.Add(...) plus an indexed row with a NULL
  schema_ptr. It is NEVER a parse_error and NEVER an aborted import.
- One row per (method, path) pair from paths[]. method upper case. path EXACTLY the paths[] key, never
  prefixed with the base path (§7 step 3, and UNIQUE(spec_id, method, path)).
- canonical_path: call router.CanonicalPath. Do NOT write your own — §8 makes canonical_path the key for
  override and conflict semantics in P2, and a second implementation will drift from the router's.
- Tag = tags[0] or NULL, Summary = the operation's "summary" or NULL. The column is a single TEXT and
  the acceptance document carries 131 tag entries across 130 operations, so at least one operation has
  two and the first one wins.
- source_order = document order, starting at 0. SQLite row order is not stable and §8 rule 4 uses this as
  the final tie-break.
- pointer = the JSON pointer to the operation node, e.g. "#/paths/~1api~1v1~1account/get". Escape ~ and /
  as ~0 and ~1 — every path key in the acceptance document contains slashes.
- Per-status response variants into operation_responses, media type PER STATUS (§7 step 5):
    selector      = what the document said ("200", "2XX", "default")
    http_status   = what we would actually send: the numeric code; 200 for "2XX"; 200 for "default"
    status_origin = "numeric" | "2XX" | "default" | "fallback"
    is_default    = 1 on the variant chosen by the rule below
  DEFAULT SELECTION, in this order: lowest numeric 2xx -> "2XX" -> "default". With none of those, mark the
  lowest response of any kind as "fallback"; with no responses at all, synthesise one 200/"fallback" so the
  runtime always has something to answer with. "default" appears 106 times in the acceptance document.
- schema_ptr = a JSON pointer to the response schema node, NULL when there is none — and 17 responses in
  the acceptance document legitimately have none (16 of them 204, one a 200). When the response came
  through a $ref, the
  pointer must address the RESOLVED location (inside components/responses/...), not the referring site: a
  pointer nobody can dereference later is worse than NULL.
- Non-JSON media types are INDEXED with their media_type; only their bodies are P2's problem.
- An operation that cannot be parsed gets a row with parse_error set and a Report warning; it never aborts
  the import. Skip non-operation keys inside a path item: "parameters", "servers", "summary",
  "description", "$ref" — every one of the 110 path items in the acceptance document carries "servers".
- Extend Repo.Report so it re-runs Load AND Index, otherwise index-time warnings (budget, cycle,
  unresolved pointer) exist nowhere after the call returns. Keep the doc comment saying it is a
  re-derivation, not a cache.

TESTS: table-driven UNIT tests only — the real-document acceptance test belongs to a later agent, so do
not write one here (two agents writing a MOCKER_TEST_SPEC helper into the same package would collide).
Cover: the selection rule for each of {only 200}, {200 and 201}, {only 2XX}, {only default}, {only 404},
{no responses}; a response that is a pure $ref; a response that is a $ref WITH a description sibling; a
$ref that fails to resolve -> warning, not parse_error; canonical_path via router.CanonicalPath with zero,
one and two parameters; pointer escaping for a path containing slashes; a path item carrying "servers" and
"parameters" keys; a 204 with no content -> NULL schema_ptr.
Finish with EXACTLY these, scoped to your own package:
  test -z "$(gofmt -l internal/specs)" && go build ./internal/specs/ && go vet ./internal/specs/ && go test ./internal/specs/ -race -count=1`,
  { label: 'indexer', phase: 'Index', model: 'sonnet', schema: SCHEMA })

if (!indexer) {
  log('ABORT: the indexer returned nothing. Without it an import stores a spec with no operations.')
  return { error: 'indexer returned nothing', foundationOK }
}
logDeviations('Index', [indexer])

let indexOK = [indexer]

// ------------------------------------------------------------------ gate 2 --
phase('Gate 2')

const gate2 = await parallel([
  () => agent(`${gateCtx}
The operation indexer (internal/specs/index.go) was just written, and Repo.Import now calls it. The admin
API and the mock plane are about to be built on it.

YOUR VECTOR: DESIGN §7 step 5 and the SQL. Read that step first (rg -n '^## 7' DESIGN.md, ~40 lines).
Check: the default-selection rule (lowest numeric 2xx -> "2XX" -> "default" -> fallback) and the three
columns each holding what §7 says they hold — anything that could send "2XX" or "default" as a literal
HTTP status is a blocker; is_default landing on exactly one variant per operation; source_order being
document order; pointer escaping (~0/~1) for path keys containing slashes; canonical_path coming from
router.CanonicalPath rather than a second implementation; non-operation keys inside a path item being
skipped; a resolve failure recorded as a warning and NOT as parse_error; ReplaceOperations still
idempotent after the wiring; Repo.Report re-running both Load and Index; and the whole import still inside
ONE transaction.
Run: go test ./internal/specs/ -race -count=1
Report only defects you can point at with file and line.`,
    { label: 'gate2:rules', phase: 'Gate 2', model: 'sonnet', schema: REVIEW_SCHEMA }),

  () => agent(`${gateCtx}
The operation indexer (internal/specs/index.go) was just written.

YOUR VECTOR: does it survive the REAL document? The acceptance spec is ${SPEC_PATH} in the repo root
(gitignored, present on this machine). It is 347 KB — NEVER Read or cat it, query it ONLY with jq.
VERIFY YOUR CLAIMS AGAINST IT — do not reason from the code alone.
Check:
- does Index RESOLVE a response's $ref before reading its content? In that document 221 of 419 response
  entries are a $ref (220 pure, one carrying a "description" sibling). An indexer that reads .content
  directly, or that tests for "exactly one key", writes NULL media_type for most of them while every
  hand-written fixture test still passes. You cannot count the RESULTING rows — no real-document test
  exists yet, and you may not create one. Instead do both halves: (a) with jq, state the document-side
  expectation — 419 response entries, 402 that must end up with a media_type and 17 that must not; and
  (b) name the exact line of internal/specs/index.go that resolves a response's $ref before reading
  .content, or state plainly that no line does. If (b) finds nothing, that IS the finding.
- the recursive schema (components.schemas.FeedbackSection, reached by GET /api/v1/feedback/section):
  does the code terminate on it, and is the complaint a warning rather than a parse_error?
- the 4 path items with an EMPTY servers array and the 110 that carry "servers" at all: skipped correctly?
- schema_ptr addressing the resolved node, and dereferenceable — spot-check two of them with jq.
- the single text/csv response (in components/responses, referenced once, and it HAS a schema): would it
  be indexed with its media type and a non-NULL schema_ptr?
Run: go test ./internal/specs/ -race -count=1
Report only defects you can point at with file and line.`,
    { label: 'gate2:document', phase: 'Gate 2', model: 'sonnet', schema: REVIEW_SCHEMA }),
])

let gate2Fix = null
const gate2Findings = gate2.filter(Boolean).flatMap((r) => r.findings || [])
const gate2Blockers = gate2Findings.filter((f) => f.severity === 'blocker')
log(`Gate 2: ${gate2Findings.length} findings, ${gate2Blockers.length} blockers`)
for (const f of gate2Findings.filter((f) => f.severity !== 'blocker')) log(`gate2 ${f.severity}: ${f.file}:${f.line} — ${f.summary}`)

if (gate2Blockers.length > 0) {
  const list = gate2Blockers.map((f, i) => `${i + 1}. ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`).join('\n')
  gate2Fix = await agent(`${CTX_CORE}${CTX_SPEC}${INTENDED}
You own internal/specs/ for this task only.
Do NOT write a real-document test: the Accept phase owns internal/specs/acceptance_test.go and its
MOCKER_TEST_SPEC helper, and a second helper in the same package is a redeclaration. Two reviewers found blockers in the operation indexer that two
more sections are about to be built on. Verify each against the code FIRST — reviewers false-positive; if
a finding is wrong, say so in "deviations" with the evidence instead of "fixing" it. Add a test for every
real defect you fix.

${list}

Finish with: test -z "$(gofmt -l internal/specs)" && go build ./internal/specs/ && go vet ./internal/specs/ && go test ./internal/specs/ -race -count=1`,
    { label: 'gate2:fix', phase: 'Gate 2', model: 'sonnet', schema: SCHEMA })
  if (gate2Fix) {
    logDeviations('Gate 2 fix', [gate2Fix])
    indexOK = [...indexOK, gate2Fix]
  }
}

// ---------------------------------------------------------------- phase 3 --
phase('Serve')

const allContracts = contractsOf([...foundationOK, ...indexOK])
const specsContracts = filterContracts(allContracts, /specs\.|NewRepo|^\s*type (Repo|Spec|Operation|Response|Import)|^\s*(var )?Err\w*\s*(=|,|\s+error)|Repo\)|ImportInput|ImportResult/)
// The router filter deliberately excludes any line mentioning specs: wire-plane is forbidden to import
// internal/specs, and (r *Repo) Routes(...) ([]router.Route, error) would otherwise be handed to it.
const routerContracts = filterContracts(allContracts, /^\s*type (Table|Route|Match)|Table\)|CanonicalPath|Conflicts|func Build/)

const serve = await parallel([
  () => agent(`${CTX_CORE}${CTX_TEST}
YOUR TASK: the /api/specs admin surface, plus attaching a spec to a workspace.
Files you own:
  internal/admin/spec_handlers.go        (create)
  internal/admin/spec_handlers_test.go   (create)
  internal/admin/server.go               (MODIFY: register the new routes, and construct the specs repo
                                          INTERNALLY with specs.NewRepo(db, cfg). admin.New already
                                          receives *store.DB — do NOT change its signature, or
                                          cmd/mocker/main.go stops compiling and you do not own it.)
  internal/admin/workspace_handlers.go   (MODIFY: accept specId on create and patch only)
Skills (if present): golang-security, api-design.

DO NOT import internal/mockplane or internal/server from internal/admin or from its tests. Another agent
is rewriting internal/mockplane right now, and pulling it into your build would put a red package inside
your own finish command — internal/admin imports neither today, and that is exactly why your build is
safe. Expect "go build ./..." to be red for the whole phase; that is someone else's package, not yours.

Read the endpoint list in DESIGN.md §14 (rg -n '/api/specs' DESIGN.md) and copy the shape of the existing
handlers in internal/admin/workspace_handlers.go — status codes, error bodies, CSRF, the session lookup.

The contract you call:
${SIG_SPECS}

As actually written by the agents that built it:
${specsContracts}

ENDPOINTS (the P1a subset — the rest of §14's spec routes belong to later phases)
  POST   /api/specs                   import a document
  GET    /api/specs                   list
  GET    /api/specs/{id}              one spec
  GET    /api/specs/{id}/report       the import report
  GET    /api/specs/{id}/operations   paged: ?limit=&offset=, default 100/0, limit CLAMPED to 500.
                                      Pass them THROUGH to Repo.Operations — the repo pages in SQL, so
                                      never load every row to slice it, and never write SQL in this package.
  DELETE /api/specs/{id}              delete; on specs.ErrAttached answer 409 and call
                                      Repo.AttachedWorkspaces for the slugs to put in the error details
                                      (ErrAttached is a bare sentinel and carries nothing itself)

POST /api/specs TAKES JSON, NOT MULTIPART — a deliberate decision: the admin CSRF gate in
internal/admin/security.go hard-requires Content-Type: application/json, and DESIGN §8 says why ("На
admin-плоскости парсер строгий — толерантный превращает POST в «простой» CORS-запрос и открывает CSRF").
Loosening it for one upload route would undo a P0 security invariant. Body:
  { "name": "...", "source": "upload", "document": "<the whole spec document as a JSON string>" }
- source "url" is DEFERRED to P1b: answer 400 saying URL import is not available yet. Do NOT implement a
  fetch, do not read cfg.URLImportAllowlist. No outbound request may exist anywhere in this package.
- specs.ErrDuplicate answers 200 with the existing spec and "duplicate": true, not 409 — re-uploading the
  same file is normal and the hash makes it idempotent.
- specs.ErrNotADocument is 400. specs.ErrUnsupportedFormat (Swagger 2.0, YAML) is 400 with a message
  naming P1b. specs.ErrTooLarge is 413 with httpx.CodeTooLarge. Anything else IS a genuine 500: HARD
  RULE 8 says the import cannot fail on the DOCUMENT, but it can still fail on the DATABASE, and
  reporting a locked or full database as "bad request" sends the operator hunting the wrong thing.

WORKSPACES: add specId to POST /api/workspaces and PATCH /api/workspaces/{id}. workspaces.CreateInput
already carries SpecID *int64 and the table already has the column; the API just never exposed it, so a
spec could be imported and never attached. Validate that the spec exists, 404 if not. On attach, copy the
spec's base_path into the workspace's settings.BasePath when the workspace has none yet (DESIGN §7 step 3:
a hint copied into settings "где его можно править"). Do NOT bump Revision by hand — workspaces.Repo.Update
already does it, and a manual bump makes every edit count twice.

TESTS: httptest over the real admin handler with a real SQLite database, logging in the way the existing
admin tests do. Cover: import a small inline document; list; get; report; operations paging and the limit
clamp; delete an unattached spec; delete an ATTACHED spec -> 409 naming the workspace; a second identical
import -> duplicate:true; a non-document -> 400; a Swagger 2.0 document -> 400 naming P1b; source "url" ->
400; attaching a spec to a workspace and seeing settings.basePath filled; attaching a non-existent spec ->
404; every mutating route rejected without a CSRF token.
Finish with EXACTLY these, scoped to your own package:
  test -z "$(gofmt -l internal/admin)" && go build ./internal/admin/ && go vet ./internal/admin/ && go test ./internal/admin/ -race -count=1`,
    { label: 'admin-specs', phase: 'Serve', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${CTX_CORE}${CTX_TEST}
YOUR TASK: wire the route table into the mock plane. Files you own — and ONLY these:
  internal/mockplane/routes.go        (create)
  internal/mockplane/routes_test.go   (create)
  internal/mockplane/plane.go         (MODIFY: Step 5, the New signature, the new interface)
  internal/mockplane/plane_test.go    (MODIFY: its fake and the New call)
Skills (if present): golang-concurrency, golang-testing.

YOU DO NOT OWN cmd/mocker/main.go OR internal/server/server_test.go. Changing mockplane.New's signature
breaks both, and the Integrate agent — which runs alone, after this phase — fixes them. Expect
"go build ./cmd/mocker/" and "go test ./internal/server/" to be RED while you work; that is correct and
expected, note it in "deviations" and do not touch those files. Your finish command does not build them.

internal/mockplane/plane.go line ~139 holds the literal comment
  "Step 5: everything else 404s until the route table arrives in P1"
That is your integration point and the only place in plane.go whose BEHAVIOUR you may change.

The contracts you call:
${SIG_ROUTER}

As actually written:
${routerContracts}

THE INTERFACE SHAPE IS DECIDED — do not redesign it. Do NOT widen the existing Source interface and do NOT
give workspaces.Repo an operations accessor: workspaces must not learn about specs. Add a SECOND narrow
interface and a fourth parameter:
    // SpecSource reads the route table rows of a spec. [*specs.Repo] satisfies it.
    type SpecSource interface {
        Routes(ctx context.Context, specID int64) ([]router.Route, error)
    }
    func New(cfg *config.Config, src Source, specs SpecSource, log *slog.Logger) *Plane
Declaring it over router.Route rather than over a specs type is what keeps internal/mockplane from
importing internal/specs. A nil SpecSource is legal and means "no spec support" — test fakes pass nil and
the plane then behaves exactly as it does today.

IMPLEMENT
- A per-workspace route table built from SpecSource.Routes and glued to settings.BasePath by router.Build,
  cached in memory and keyed by (workspace id, revision) so a revision bump invalidates it. Cache size
  comes from cfg.RuntimeCache, evicting least-recently-used. Building is SINGLE-FLIGHT: N concurrent
  requests for a cold workspace build the table ONCE (DESIGN §20 lists a stalled event loop as a real
  risk). golang.org/x/sync/singleflight is already in go.mod and go.sum.
- Step 5 becomes: normalise with NormalizeSegments, then match against the table.
    no spec attached, or a nil SpecSource -> the existing 404, unchanged
    no route matches                      -> 404, shaped like the existing not-found response
    a route matches                       -> 501 with a JSON body naming the matched operation:
      {"error":{"code":"not_implemented","message":"...",
                "details":{"operationId":"<Route.OperationLabel — the OpenAPI STRING label>",
                           "canonicalPath":"...","method":"..."}}}
  Route.OpRowID is a database row id and must NEVER appear in a response body.
  The 501 is deliberate and temporary: P1b's generator replaces it.
- CORS headers must still be set on the 404 and the 501 (DESIGN §8: "CORS-заголовки ставятся и на 404,
  и на 500"). Verify you did not bypass the code that sets them.

TESTS: table-driven plus an httptest pass over the real plane, using a fake SpecSource — you do not own
internal/specs and must not import it in a test either. Cover: a request matching a route returns 501
naming the right operationId STRING; a request matching nothing returns 404; a workspace with no spec
returns 404; a nil SpecSource behaves as today; CORS headers on both; HEAD matched as GET with an empty
body; the cache returning the same table twice and a DIFFERENT one after the revision bumps; and a
concurrent test (-race, several goroutines hitting a cold workspace) proving the table is built once.
Finish with EXACTLY these, scoped to the one package you own:
  test -z "$(gofmt -l internal/mockplane)" && go build ./internal/mockplane/ && go vet ./internal/mockplane/ && go test ./internal/mockplane/ -race -count=1`,
    { label: 'wire-plane', phase: 'Serve', model: 'sonnet', schema: SCHEMA }),
])

const serveOK = serve.filter(Boolean)
logDeviations('Serve', serveOK)
const serveGap = [!serve[0] && 'admin-specs', !serve[1] && 'wire-plane'].filter(Boolean).join(', ') || 'none'
if (serveGap !== 'none') log(`WARNING: Serve agent(s) returned nothing: ${serveGap}. The Integrate agent is told to finish that work.`)

// ---------------------------------------------------------------- phase 4 --
// Integrate and Accept are two agents on purpose. As one, the task was
// reconciliation across six packages PLUS two new test files PLUS a docker smoke
// run — 40-60 tool calls, each re-billing a growing context.
phase('Integrate')

const integrate = await agent(`${CTX_CORE}

HARD RULES 1 AND 2 ARE RELAXED FOR YOU: you run alone and may touch any file to make the tree compile.
HARD RULE 3 (no migration) and the no-new-dependency rule STILL HOLD.

YOUR TASK: make the whole tree build and vet cleanly, and wire the binary.

1. cmd/mocker/main.go and internal/server/server_test.go are YOURS this phase, and they are currently
   broken on purpose: the Serve phase changed mockplane.New to take a fourth argument, a SpecSource.
   - cmd/mocker/main.go (the mockplane.New call, around line 127) MUST pass a REAL one:
     specs.NewRepo(db, cfg). It is the one caller that must not pass nil — nil compiles, passes every test
     in this run, and ships a binary in which this whole phase does nothing.
   - internal/server/server_test.go has two mockplane.New calls (around lines 88 and 255) and a fake
     Source; nil is the right argument for those.
2. Then run go build ./... and go vet ./... and fix whatever else the parallel agents left inconsistent —
   a signature that drifted from its contract, an interface implementation missed, an import cycle.
   Change as little as possible and prefer fixing the CALLER over changing a published contract; if you do
   change a contract, say so in "deviations" with every call site you updated.
3. Phase-3 agents that returned NOTHING, whose work you must therefore finish: ${serveGap}
   If that says "none", write no new features and no new tests — reconcile only. If it names an agent,
   that section is yours and this restriction is lifted for it: implement the minimum that compiles and
   record what is missing in "todo".
4. Do NOT weaken, skip or delete a test to reach a green build. If a test does not compile, fix it to the
   new contract; if that is impossible, say so in "deviations" rather than deleting it.
5. Finish with both clean and paste the real output into "verified":
     test -z "$(gofmt -l .)"    go build ./...    go vet ./...
   Do not run the full test suite — the next agent does that.`,
  { label: 'integrate', phase: 'Integrate', model: 'sonnet', schema: SCHEMA })

if (!integrate) {
  log('ABORT: the integrate agent returned nothing. The tree may not compile — a human should look.')
  return { error: 'integrate agent returned nothing', foundationOK, indexOK, serveOK }
}
logDeviations('Integrate', [integrate])

// ---------------------------------------------------------------- phase 5 --
phase('Accept')

// Two agents, SEQUENTIAL. Not one, because writing eight jq-checked assertions
// over a 347 KB document AND the hardest test in the phase AND the whole suite
// AND docker is 25-40 turns of a growing context. Not parallel, because
// accept:stack runs "go test ./..." which compiles internal/specs INCLUDING the
// test file accept:spec is still writing.
const acceptSpec = await agent(`${CTX_CORE}${CTX_TEST}${CTX_SPEC}

HARD RULE 1 IS RELAXED FOR YOU only for the one file below. HARD RULES 2 and 3 HOLD.

YOUR TASK: prove P1a's import, index and route table on the REAL document.
File you own: internal/specs/acceptance_test.go (create).

You are the ONLY agent writing a real-document test, so the MOCKER_TEST_SPEC helper is yours to name.
Resolve the path with os.Getenv("MOCKER_TEST_SPEC"), falling back to "${SPEC_PATH}", and t.Skip when the
file does not exist. NEVER Read the whole 347 KB file — query it with jq when you need to check a fact.

ASSERT, on that document:
  - import succeeds and the report's format is oas30
  - exactly 130 operations are indexed, with zero parse_error
  - every stored operations.path is BYTE-IDENTICAL to its paths[] key — assert against the document
    itself, not a hard-coded list
  - the base path origin is BaseAmbiguous and the base path is ""
  - every operation has exactly one is_default response
  - NO response row has a NULL media_type where the document's response RESOLVES to something carrying
    content. 221 of the 419 response entries are a $ref, so an indexer that skipped resolution fails here
    and nowhere else. Exactly 17 entries carry no content at all — 16 of them 204 and one a 200
    (DELETE /api/v1/requests/{requestId}); allow a NULL media_type on exactly those, and do NOT write the
    allowance as "status == 204", which is wrong for one of them.
  - exactly one response row has media_type "text/csv", and its schema_ptr is NOT NULL
  - GET /api/v1/feedback/section — the operation whose schema tree contains the recursive FeedbackSection
    — is indexed with no parse_error. Note honestly in "verified" that this assertion does NOT exercise
    the cycle guard: every response $ref in this document lands in components/responses in ONE hop and
    P1a never descends into a schema, so the cycle guard's only real test is the openapi unit fixture.
  - re-importing the same bytes returns the existing spec (dedup by raw hash)

THEN THE ROUTE TABLE, which nothing else in this run exercises against real data. Build it:
    routes, _ := repo.Routes(ctx, specID); table := router.Build(routes, "")
and assert:
  - table.Len() == 130
  - router.Conflicts(routes) is empty (canonical paths are unique per method in this document)
  - DESIGN §8 rule 1 — a static segment beats a parameter sibling — holds for all eight real collisions.
    Match("GET", segments) must pick the STATIC route, not the {param} one, for:
      /api/v1/achievements/by-category            (vs /api/v1/achievements/{achievementId})
      /api/v1/broadcasts/count                    (vs /api/v1/broadcasts/{broadcastId})
      /api/v1/broadcasts/reminders-intervals      (vs the same)
      /api/v1/organizations/requests              (vs /api/v1/organizations/{organizationId})
      /api/v1/organizations/{id}/grades/academic-years  (vs .../grades/{gradeId})
      /api/v1/quizzes/folders                     (vs /api/v1/quizzes/{quizId})
      /api/v1/quizzes/subjects                    (vs the same)
      /api/v1/quizzes/grades                      (vs the same)
    A comparator that gets this wrong silently mis-routes eight endpoints of a real API, and every
    synthetic router test still passes.
  - the {param} routes still match their own shape, and Params carries the decoded value

Report the real numbers you measured in "verified". If an assertion disagrees with the document, check
with jq FIRST; only if the document really disagrees do you change the assertion, and then say so in
"deviations" with the jq output that proves it.
Finish with: test -z "$(gofmt -l internal/specs)" && go test ./internal/specs/ -race -count=1`,
  { label: 'accept:spec', phase: 'Accept', model: 'sonnet', schema: SCHEMA })

const acceptStack = await agent(`${CTX_CORE}${CTX_TEST}

HARD RULE 1 IS RELAXED FOR YOU only for the one file below. HARD RULES 2 and 3 HOLD.

YOUR TASK: prove the stack end to end, then prove P0 still works.
File you own: internal/server/p1a_test.go (create).

1. Write internal/server/p1a_test.go — a full-stack pass shaped like internal/server/server_test.go: log
   in, create a workspace, import a small INLINE document (NOT the real acceptance spec — this test must
   run in a fresh clone), attach the spec to the workspace, then over the mock host assert that a path
   from that document returns 501 naming its operationId STRING, and that an unknown path returns 404.
   CRITICAL: build the plane with a REAL SpecSource — specs.NewRepo(db, cfg). The newTestServer helper in
   server_test.go passes nil (correctly, for P0's own tests), and reusing it unchanged makes every mock
   request 404, so the 501 assertion becomes unreachable and the phase's headline behaviour ships
   unproven. Do not weaken the assertion to 404 — fix the wiring.
2. Run, and paste the real output into "verified":
     test -z "$(gofmt -l .)"    go build ./...    go vet ./...    go test ./... -race -count=1
3. Then the P0 acceptance check. FIRST run "docker compose version"; if that fails, write
   "smoke: skipped (no docker)" in "verified" and do NOT treat it as a failure. If docker is there, run
   "make smoke" — it must still exit 0, since P1a must not have broken P0's Host dispatch.`,
  { label: 'accept:stack', phase: 'Accept', model: 'sonnet', schema: SCHEMA })

const acceptance = [acceptSpec, acceptStack].filter(Boolean)
if (acceptance.length) logDeviations('Accept', acceptance)
if (!acceptSpec) log('WARNING: accept:spec returned nothing — this run has NO real-document verification. Everything downstream is unproven against the 130-operation spec.')
if (!acceptStack) log('WARNING: accept:stack returned nothing — the full suite and the P0 smoke check were never run for this phase.')

// ---------------------------------------------------------------- phase 6 --
phase('Review')

const touched = [...foundationOK, ...indexOK, ...serveOK, integrate, ...acceptance].filter(Boolean).flatMap((r) => r.files || []).join(', ')

const reviewCtx = `Repo root is your CWD. Do not modify any tracked source file — you are auditing, not fixing. Running the
test suite is expected.
TO SEE THE DIFF: almost every file this phase produced is NEW, and "git diff HEAD" does not show
untracked files at all. Use "git add -N . && git diff HEAD" — intent-to-add makes them appear as
additions and stages nothing for commit.
DESIGN.md is Russian and 84 KB — never read it whole; find sections with rg -n '^## ' DESIGN.md.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it, query it with jq only.

Phase P1a of mocker was just written: OpenAPI import, a lazy $ref resolver, the operation index, the route
table, the /api/specs admin surface, and the route table wired into the mock plane.
Files written or changed by this run: ${touched}
The commit before this run is P0, green and reviewed, so "git diff HEAD" shows exactly what to review.
${INTENDED}
Report ONLY defects you can point at with a file and a line. No praise, no style nits that do not change
behaviour. Empty findings array if your vector is clean.`

const reviews = await parallel([
  () => agent(`${reviewCtx}

YOUR VECTOR: correctness and bugs. Hunt for defects that produce wrong behaviour:
- $ref resolution in the indexer. The acceptance document has 221 $ref response entries out of 419; verify
  with jq that the indexed rows actually carry media_type and schema_ptr for them rather than trusting the
  tests. A schema_ptr addressing the referring site instead of the resolved node is a blocker — nobody can
  dereference it later.
- the response-selection rule against DESIGN §7 step 5, and the three columns each holding what they
  should. "2XX" and "default" are NOT sendable codes; anything that could send them literally is a blocker.
- the base path: stored paths must be relative, glued in exactly ONE place (router.Build). Any second
  gluing site, or a path stored with the prefix, is a blocker.
- the router's comparator against DESIGN §8's four rules, including leftmost-static and custom-wins. Is the
  sort stable where it must be? Does an equal-specificity pair fall through to SourceOrder deterministically?
- segment matching: a {param} must not match across "/", and a percent-encoded %2F inside a segment must
  not act as a separator (that exact defect shipped in P0 and was fixed — check it did not come back).
- canonical_path computed in ONE place (router.CanonicalPath) rather than reimplemented in the indexer.
- the per-workspace table cache: keyed by revision, correct eviction, a real single-flight (N concurrent
  cold requests build ONCE). Any data race, any table mutated after Build.
- the resolver: per-branch visited set, a memo that cannot smuggle a result past the budget, no unbounded
  recursion, a $ref with sibling keys still followed.
- SQL against internal/store/migrations/0001_init.sql: column names, UNIQUE constraints, whether
  ReplaceOperations is genuinely idempotent, whether Operations pages in SQL, and whether Delete handles
  the workspaces.spec_id foreign key rather than surfacing a raw constraint error.
- cmd/mocker/main.go passing a REAL SpecSource rather than nil — a nil there ships a binary where the whole
  phase does nothing, and no test in this run would catch it.
- workspaces Revision bumped once per edit, not twice (Repo.Update bumps it itself).
- errors compared with == instead of errors.Is; nil map writes; unchecked type assertions on the document.
- tests that assert nothing or would still pass with the implementation deleted.
Run "go test ./... -race -count=1" yourself and report anything that fails, is skipped, or is flaky.
If docker is available ("docker compose version"), also run "make smoke" and report it if P0's acceptance
check no longer passes. If docker is absent, say so and move on — it is not a finding.`,
    { label: 'review:correctness', phase: 'Review', model: 'sonnet', schema: REVIEW_SCHEMA }),

  () => agent(`${reviewCtx}

YOUR VECTOR: security. P1a adds the first code path that takes a large untrusted document from a user.
Assume an attacker who can log into the admin plane (the password is shared across the team — DESIGN §15
says so) and an attacker inside the contour who cannot.
Check at minimum:
- the CSRF gate: are ALL the new mutating routes (POST /api/specs, DELETE /api/specs/{id}, the modified
  workspace routes) inside the middleware that checks Origin/Referer AND the token AND the strict
  Content-Type? Is any new route registered outside it? Is the token compared in constant time? This is
  the highest-value check in P1a: the test that would catch a mis-registration was written by the same
  agent that registered the routes, so it is self-certifying.
- authorisation: can an unauthenticated request reach any /api/specs route, including the read-only ones?
- resource exhaustion: is the document size capped BEFORE parsing and against cfg.MaxBody? Can a deeply
  nested or hugely fan-out document exhaust memory or the stack? Can the resolver be made to loop or
  allocate without bound? Is ?limit= on /api/specs/{id}/operations actually clamped AND pushed into SQL,
  or can a caller pull every row? Is the per-workspace table cache bounded by cfg.RuntimeCache, and can an
  attacker who can reach the OPEN mock plane force unbounded table builds for slugs that do not exist?
- URL import is DEFERRED and must answer 400. Verify NO code path performs an outbound request anywhere in
  the tree — no http.Get, no http.Client.Do, no net.Dial behind a flag. If one exists it is a blocker: an
  unreviewed SSRF surface this phase deliberately did not sign up for.
- information leak: does any error body, log line or report warning echo the uploaded document? It may
  contain internal hostnames, tokens in examples, or customer data. Does the mock plane's 501 echo
  attacker-controlled path data unescaped, and does it reveal more of the workspace's spec structure than
  the open mock plane already exposes by design?
- injection: SQL built by concatenation anywhere in the new code; a JSON pointer or path from the document
  reaching a query, a log line or a file operation.
Do NOT run "make smoke". The correctness reviewer runs it, you two run concurrently, and it is not
concurrency-safe: it rewrites .env in the repo root and runs "docker compose down -v" on entry and again
in its exit trap, so a second run tears down the first mid-assertion and manufactures a phantom
"P0 acceptance broke" blocker.
Report each as a concrete attack: what the attacker sends, what they get. File and line required.`,
    { label: 'review:security', phase: 'Review', model: 'opus', schema: REVIEW_SCHEMA }),
])

const findings = reviews.filter(Boolean).flatMap((r) => r.findings || [])
const minors = findings.filter((f) => f.severity === 'minor')
for (const m of minors) log(`minor: ${m.file}:${m.line} — ${m.summary}`)

// Gate majors never had a fix agent of their own — the gates only spawn one for blockers. Carrying them
// here is what stops "major" meaning "fix it" at the end of the run and "ignore it" at the gate, in the
// very packages four later sections were built on.
const gateMajors = [...gate1Findings, ...gate2Findings].filter((f) => f.severity === 'major')
const actionable = [...gateMajors, ...findings.filter((f) => f.severity === 'blocker' || f.severity === 'major')]
let outstanding = actionable
log(`Carrying ${gateMajors.length} unfixed gate majors into the fix list.`)
if (actionable.length > 20) log(`NOTE: ${actionable.length} actionable findings — a large fix list; nothing is dropped, but the fix agent gets all of them.`)
log(`Review: ${findings.length} findings — ${actionable.length} actionable, ${minors.length} minor (logged, not fixed)`)

if (outstanding.length === 0) {
  log('Nothing actionable: skipping Fix and Verify.')
  return { integrate, acceptance, findings, rounds: [], stillOpen: [] }
}

// ------------------------------------------------------------ phases 7-8 --
// Fix, then VERIFY the fix. The verify pass exists because Fix is otherwise
// terminal: a semantically wrong but compiling fix survives gofmt, build, vet
// and the tests, and nobody is watching this run. Bounded to two rounds.
const rounds = []
let lastOutput = ''
for (let round = 1; round <= 2 && outstanding.length > 0; round++) {
  phase('Fix')
  const list = outstanding
    .map((f, i) => `${i + 1}. [${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`)
    .join('\n')

  const fixed = await agent(`${CTX_CORE}${CTX_SPEC}${INTENDED}

HARD RULES 1 AND 2 DO NOT APPLY TO YOU: you run alone and own every file. HARD RULE 3 (no migration) and
the no-new-dependency rule STILL HOLD.

Round ${round}. Two reviewers audited the P1a tree. Apply the findings below, changing as little as
possible — every edit must map to a numbered finding.
${lastOutput ? `\nThe previous round ended NOT green. This was the failing output:\n${lastOutput}\n` : ''}
${list}

RULES
- Verify each finding against the code FIRST. Reviewers false-positive. If a finding is wrong, do NOT
  "fix" it — record it in "deviations" with the evidence that it is wrong.
- A finding needing a design change rather than a code change goes to "todo", not into a hasty redesign.
- Add or extend a test for every real defect you fix, so it cannot come back silently.
- Never weaken, skip or delete a test to reach green.
- Finish with all four clean and paste the real output of the last one into "verified":
    test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
  Then, only if "docker compose version" succeeds, run "make smoke" and paste its result too.`,
    { label: `fix:round${round}`, phase: 'Fix', model: 'sonnet', schema: SCHEMA })

  if (fixed) logDeviations(`Fix round ${round}`, [fixed])

  phase('Verify')
  const verdict = await agent(`Repo root is your CWD. Do not modify any file.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it; query it with jq only.
${INTENDED}

A fix agent just claimed to have addressed the findings below. Its own report is self-assessed, so check
it. Three things, in this order:

1. Run, and report the REAL output:
     test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
   "green" is true only if all FOUR are clean.
2. Run "docker compose version". If it succeeds, run "make smoke" and set "smoke" to passed or failed; if
   docker is absent, set it to "skipped-no-docker". A skipped smoke does NOT make green false; a FAILING
   smoke means P0's acceptance check broke and green must be false.
3. For EACH numbered finding below, decide: fixed, credibly rebutted, or still open. A fix that compiles is
   not a fix — check the semantics. Specifically distrust: a size cap applied after the body is already in
   memory; a CSRF-covered route quietly re-registered outside the middleware; a router comparator "fixed"
   so its tests pass but DESIGN §8's rule order changed; $ref resolution "fixed" by hard-coding the
   acceptance document's shape; a test weakened, skipped or deleted to reach green.
   To see the diff, run "git add -N . && git diff HEAD" — most of this phase is NEW files and a plain
   "git diff HEAD" shows none of them, which would leave you unable to check this at all.
   Put anything still open in "unresolved" as {n: <the finding number from THE LIST BELOW>, why: "..."}.
   THE LIST IS RENUMBERED FROM 1 EVERY ROUND — use the numbers you see below, not from an earlier round.

${list}`,
    { label: `verify:round${round}`, phase: 'Verify', model: 'opus', schema: VERIFY_SCHEMA })

  rounds.push({ round, fixed, verdict })

  if (!verdict) {
    log(`Round ${round}: verification agent returned nothing — stopping with ${outstanding.length} findings unverified.`)
    break
  }
  const unresolved = verdict.unresolved || []
  log(`Round ${round}: green=${verdict.green}, smoke=${verdict.smoke || 'not reported'}, ${unresolved.length} unresolved`)
  for (const u of unresolved) log(`still open: #${u.n} ${u.why}`)

  if (verdict.green && unresolved.length === 0) {
    outstanding = []
    break
  }

  const stillOpen = new Set(unresolved.map((u) => u.n))
  const kept = outstanding.filter((_, i) => stillOpen.has(i + 1))
  lastOutput = verdict.green ? '' : (verdict.output || '').slice(0, 4000)

  if (kept.length < stillOpen.size) {
    // Some n did not map to a finding in THIS round's list. Dropping them
    // silently is how a run reports success over open blockers.
    log(`WARNING: ${stillOpen.size - kept.length} unresolved entries did not map to a finding number — carrying the FULL actionable set forward.`)
    outstanding = actionable
  } else if (!verdict.green && kept.length === 0) {
    // Red, but nothing attributed to a finding: the likeliest shape when the fix
    // agent broke something unrelated.
    outstanding = actionable
    log('Round ended NOT green with nothing attributed to a finding — re-running the full actionable set with the failing output attached.')
  } else {
    outstanding = kept
  }

  if (round === 2 && outstanding.length > 0) {
    log(`STOPPING after 2 rounds with ${outstanding.length} findings still open — a human should look at these.`)
  }
}

return { integrate, acceptance, findings, rounds, stillOpen: outstanding }

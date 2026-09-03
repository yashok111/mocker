export const meta = {
  name: 'mocker-p0',
  description: 'Finish P0 of mocker (Go): auth+sessions, workspaces repo, mock plane, admin API, docker, wiring, review',
  whenToUse: 'Run once, from the repo root, to complete phase P0 as defined in DESIGN.md §19.',
  phases: [
    { title: 'Foundation', detail: 'auth+sessions, workspaces repo, ops/docker files', model: 'sonnet' },
    { title: 'Gate 1', detail: 'review the foundation before two more sections build on it', model: 'sonnet' },
    { title: 'Planes', detail: 'mock plane (host resolve, CORS, health) + admin plane (login, CSRF, workspaces API)', model: 'sonnet' },
    { title: 'Integrate', detail: 'host dispatch, main.go, acceptance test, green build+vet+test', model: 'sonnet' },
    { title: 'Review', detail: 'correctness (sonnet) + security (opus)', model: 'sonnet' },
    { title: 'Fix', detail: 'apply blocker/major findings', model: 'sonnet' },
    { title: 'Verify', detail: 're-run the suite and check each finding was really fixed', model: 'sonnet' },
  ],
}

// ---------------------------------------------------------------------------
// Signature blocks. These are pasted verbatim into BOTH the agent that writes
// the package AND every agent that calls it, because the parallel agents cannot
// negotiate at runtime and a swapped pair of string parameters compiles
// silently.
// ---------------------------------------------------------------------------

const SIG_AUTH = `  // package auth
  func HashPassword(plain string) (string, error)            // argon2id, PHC-encoded
  func VerifyPassword(encoded, plain string) (bool, error)   // constant-time
  type User struct { ID int64; Name string; Role string; CreatedAt time.Time }
  type Session struct { ID string; UserID int64; CSRFToken string; CreatedAt, ExpiresAt time.Time }
  type Principal struct { UserID int64; Name string }
  type Challenge struct { RedirectURL string }               // OIDC seam, unused in P0
  type Result struct { Principal *Principal; Challenge *Challenge }
  type Provider interface {
      Login(ctx context.Context, r *http.Request) (Result, error)
      Logout(ctx context.Context, sessionID string) error
      Mode() string
  }
  type SharedPassword struct{ ... }                          // implements Provider
  func NewSharedPassword(cfg *config.Config) *SharedPassword
  type Manager struct{ ... }
  func NewManager(db *store.DB, cfg *config.Config, p Provider) *Manager
  func (m *Manager) Login(ctx context.Context, r *http.Request) (*Session, *User, error)
  func (m *Manager) Lookup(ctx context.Context, sessionID string) (*Session, *User, error)
  func (m *Manager) Logout(ctx context.Context, sessionID string) error
  func (m *Manager) PurgeExpired(ctx context.Context) (int64, error)
  func (m *Manager) CookieName() string                      // "mocker_session"
  func (m *Manager) SetCookie(w http.ResponseWriter, s *Session, secure bool)
  func (m *Manager) ClearCookie(w http.ResponseWriter, secure bool)
  var ErrBadPassword, ErrBadName, ErrNoSession error`

const SIG_WS = `  // package workspaces
  type Workspace struct {
      ID int64; Slug, Name string
      SpecID, OwnerID, ForkedFrom, ScenarioID *int64
      Revision int64
      Settings domain.Settings
      CreatedAt, UpdatedAt time.Time
  }
  type CreateInput struct { Name, Slug string; OwnerID, SpecID *int64; Settings *domain.Settings }
  type Repo struct{ ... }
  func NewRepo(db *store.DB) *Repo
  func (r *Repo) Create(ctx context.Context, in CreateInput) (*Workspace, error)
  func (r *Repo) ByID(ctx context.Context, id int64) (*Workspace, error)
  func (r *Repo) BySlug(ctx context.Context, slug string) (*Workspace, error)
  func (r *Repo) List(ctx context.Context, ownerID *int64) ([]*Workspace, error)
  func (r *Repo) Update(ctx context.Context, id int64, mutate func(*Workspace) error) (*Workspace, error)
  func (r *Repo) Delete(ctx context.Context, id int64) error
  func (r *Repo) SlugTaken(ctx context.Context, slug string) (bool, error)   // read-only helper
  var ErrNotFound, ErrSlugTaken, ErrSlugInvalid error`

// Shared preamble, ~3 KB. Paid once per agent, so DESIGN.md (84 KB) is
// REFERENCED and the spine is DIGESTED here rather than opened by everyone.
const CTX = `You are finishing phase P0 of "mocker", a self-hosted mock-backend service written in Go.

YOUR CWD IS THE REPO ROOT. Use repo-relative paths everywhere; never absolute ones.
Module path: git.sumka.site/yakov/mocker. Go 1.26. CGO_ENABLED=0 is the RELEASE build only —
locally cgo is on, which is what makes "go test -race" work.

THE SPEC IS DESIGN.md (Russian, ~1180 lines, 84 KB). NEVER read it whole.
Find sections with: rg -n '^## ' DESIGN.md    then Read only the line range you were told to read.
For a single fact prefer a targeted search (rg -n '__mocker' DESIGN.md) over reading a whole section.

THE SPINE BELOW IS ALREADY WRITTEN, COMMITTED AND GREEN. This digest IS your contract:
open only the spine files your own task names, not all of them.

  internal/config/config.go   Config{Addr,BaseDomain,AdminHost,Routing,ReservedPrefix,AuthMode,
                              SharedPasswordHash,DefaultSpec,DataDir,MaxBody,MaxResponse,MaxEntities,
                              TrafficMaxBody,TrafficRetention,CheckpointRetention,RuntimeCache,
                              TrustProxy,URLImportAllowlist,Dev}
                              Load() (*Config,error); (c) IsWorkspaceHost(host) (slug string, ok bool);
                              (c) IsAdminHost(host) bool; (c) CookieSecure() bool == !Dev; (c) DBPath();
                              TrustProxy{Enabled,Hops,CIDRs}.TrustsPeer(netip.Addr) bool
  internal/store/store.go     DB{W,R *sql.DB}; Open(ctx,path) (*DB,error); (db) Migrate(ctx,*slog.Logger);
                              (db) Write(ctx, func(tx *sql.Tx) error) error;  (db) SchemaVersion(ctx);
                              (db) Close(). W is a ONE-connection writer pool, R is read-only.
  internal/store/migrations/0001_init.sql   the full schema; every table already exists
  internal/domain/settings.go Settings{Seed,BasePath,ListSize,NullRate,Envelope,Identity,Auth,CORS,
                              ValidateRequests,DelayMs,NotFoundBody}; DefaultSettings(); ParseSettings([]byte);
                              (s) Normalize(); NormalizeBasePath(string); NewSigningKey();
                              CORSSettings{Mode,Credentials} with CORSReflect/CORSOff
  internal/domain/slug.go     ValidateSlug(string) error; Slugify(string) string (transliterates Cyrillic);
                              UniqueSlug(name string, taken func(string) (bool, error)) (string, error);
                              ErrSlugReserved
  internal/httpx/respond.go   JSON(w,status,v); Err(w,status,code,msg); ErrDetails(w,status,code,msg,details);
                              NoContent(w); ErrorBody/ErrorDetail; CodeBadRequest/CodeUnauthorized/
                              CodeForbidden/CodeNotFound/CodeConflict/CodeTooLarge/CodeInternal
  internal/httpx/middleware.go Middleware; Chain(h,...mw); Recover(log); RequestLog(log); MaxBody(int64);
                              StatusRecorder
  internal/httpx/peer.go      Peer{Addr,Forwarded,Trusted}.Client() netip.Addr;
                              ResolvePeer(r,trust) Peer; ForwardedProto(r,trust) string

TEST CONFIG — config.Config has NO defaults constructor, so a bare literal in a test is a trap:
MaxBody:0 makes httpx.MaxBody 413 every request, ReservedPrefix:"" makes health unmatchable, and
Dev:false sets a Secure cookie that a cookiejar over plain-http httptest silently drops. Any test that
builds a Config by hand MUST set at minimum:
  &config.Config{BaseDomain:"mock.local", AdminHost:"mocker.local", Routing:config.RoutingHost,
                 ReservedPrefix:"/__mocker", AuthMode:config.AuthShared, DataDir:t.TempDir(),
                 MaxBody:10<<20, MaxResponse:4<<20, RuntimeCache:32, Dev:true}
Admin POSTs in tests need all three of: Content-Type: application/json, Origin: http://mocker.local,
X-CSRF-Token from the login response — otherwise the admin plane's own rules answer 403.

HARD RULES
1. Write ONLY the files your task lists. Other agents own every other file, in parallel, right now.
2. Do NOT edit go.mod / go.sum; do NOT run "go get" or "go mod tidy". Every dependency P0 needs is
   already there (golang.org/x/crypto is in go.mod).
3. Match the existing style: package doc comment saying WHY, comments that give the reason for a
   decision rather than restating the code, errors wrapped with %w, table-driven tests.
4. Modern Go: slices/maps helpers, strings.Cut*/SplitSeq, errors.Is/As, min/max, range-over-int,
   method-aware http.ServeMux patterns ("GET /api/x/{id}" + r.PathValue). No third-party router.
5. Skills: if .claude/skills/ is non-empty, invoke ONLY the one or two named in your task. If the
   directory is empty (a fresh clone — skills are gitignored), skip this rule silently.
6. Times are stored as INTEGER unix seconds; JSON blobs as TEXT. Every exported symbol gets a doc
   comment. gofmt everything you write.
`

const SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['files', 'summary', 'verified', 'contracts'],
  properties: {
    files: { type: 'array', maxItems: 40, items: { type: 'string' }, description: 'repo-relative paths you created or modified' },
    summary: { type: 'string', description: 'what you implemented, 3-8 sentences' },
    verified: { type: 'string', description: 'the exact commands you ran and their real output (build/vet/test)' },
    contracts: {
      type: 'array', maxItems: 30, items: { type: 'string' },
      description: 'the exported signatures you actually defined, one per line, as written in the code',
    },
    deviations: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'anything done differently from the task or DESIGN, with the reason' },
    todo: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'deliberately left for P1+' },
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
    green: { type: 'boolean', description: 'true only if gofmt, build, vet and test -race are ALL clean' },
    output: { type: 'string', description: 'the real tail of the failing command, or the passing test summary' },
    unresolved: {
      type: 'array', maxItems: 40, items: { type: 'string' },
      description: 'findings that were neither fixed nor credibly rebutted, one per line as "<n>. <file>:<line> <why still open>"',
    },
  },
}

const logDeviations = (label, results) => {
  for (const r of results) {
    for (const d of r.deviations || []) log(`${label} deviation: ${d}`)
  }
}

log('P0 pulls preflight/CORS forward from P1 on purpose: the mock plane must answer OPTIONS from day one, and a stub would be rewritten immediately. MOCKER_ROUTING=path is also implemented now because the dispatcher is being written anyway.')

// ---------------------------------------------------------------- phase 1 --
phase('Foundation')

const foundation = await parallel([
  () => agent(`${CTX}
YOUR TASK: the auth package. Files you own (create them):
  internal/auth/password.go
  internal/auth/session.go
  internal/auth/provider.go
  internal/auth/auth_test.go
Skills (if present): golang-security, golang-testing.

Read DESIGN.md section "## 15. Авторизация и модель угроз" — that is the threat model you implement,
including the parts stating what is deliberately NOT protected.

IMPLEMENT EXACTLY THESE SIGNATURES. Two other agents are compiling against them right now:
${SIG_AUTH}

The Provider shape comes from DESIGN §15 verbatim and exists so a later phase can drop in OIDC. So:
Manager.Login MUST delegate to the Provider (it takes *http.Request precisely so an OIDC provider can
read the callback), and SharedPassword.Login reads the name and password from the JSON body. A Provider
that Manager never calls is dead code that will rot before P5 needs it. Note the name Principal, not
Identity — domain.Identity already means something else (§10, who the MOCK pretends the caller is).

REQUIREMENTS THAT ARE NOT NEGOTIABLE
- Login is GET-OR-CREATE BY NAME (DESIGN §13/§15). A stale cookie must never mint a second user with
  the same name and orphan their workspaces. users.name is UNIQUE — handle the race inside ONE
  store.Write transaction with INSERT ... ON CONFLICT DO NOTHING followed by SELECT.
- Session id and CSRF token: 256 bits from crypto/rand, base64url, unpadded, independent of each other.
  Never log either.
- Verify the password BEFORE touching the database, and take the same code path for a bad password
  whether or not the name exists — no user enumeration by response or by timing.
- Name: trimmed, 1..64 runes, no control characters, Cyrillic allowed. Empty is rejected.
- Session TTL 14 days. Lookup rejects an expired session AND deletes it.
- Cookie: httpOnly, SameSite=Lax, Path=/, Secure only when secure==true, MaxAge from the TTL, and
  NEVER a Domain attribute — a Domain would hand the admin session to every workspace subdomain.
- argon2id: time=1, memory=64*1024, threads=4, keyLen=32, saltLen=16, and PARSE the parameters back out
  of the encoded hash when verifying instead of assuming them.

TESTS: table-driven, real SQLite via store.Open+Migrate in t.TempDir(). Cover: hash/verify round trip,
wrong password, tampered encoded hash, get-or-create returning the SAME user id twice for one name,
expired session rejected and deleted, Logout, and the cookie attributes for secure true and false
(assert there is no Domain).
Finish with: gofmt -l internal/auth && go build ./internal/auth/ && go vet ./internal/auth/ && go test ./internal/auth/ -race -count=1`,
    { label: 'auth', phase: 'Foundation', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${CTX}
YOUR TASK: the workspaces package. Files you own (create them):
  internal/workspaces/repo.go
  internal/workspaces/repo_test.go
Skills (if present): golang-database, golang-testing.

Read the workspaces table with a targeted search rather than a whole section:
  rg -n -A14 'CREATE TABLE workspaces' internal/store/migrations/0001_init.sql
and DESIGN.md's "Рантайм" bullets in "## 12" for what revision means.

IMPLEMENT EXACTLY THESE SIGNATURES. Two other agents are compiling against them right now:
${SIG_WS}

REQUIREMENTS THAT ARE NOT NEGOTIABLE
- Slug reservation is ATOMIC. Do the uniqueness probe and the INSERT in ONE store.Write transaction:
      err := r.db.Write(ctx, func(tx *sql.Tx) error {
          slug := in.Slug
          if slug == "" {
              slug, err = domain.UniqueSlug(in.Name, func(s string) (bool, error) {
                  return slugTakenTx(ctx, tx, s)      // reads through tx, NOT db.R
              })
          }
          ...
      })
  Repo.SlugTaken (which reads db.R) stays a read-only helper for the admin API — do NOT call it from
  inside the transaction; a different pool cannot see the transaction's own writes.
- Translate a UNIQUE violation on workspaces.slug into ErrSlugTaken, not a 500. modernc.org/sqlite
  reports it as an error whose message contains "UNIQUE constraint failed" — match that substring;
  do not import the driver.
- An explicit in.Slug is validated with domain.ValidateSlug. The slug ACTUALLY chosen is returned to the
  caller and must be shown to the user (DESIGN §14 screen 2): silently handing "alex-2" to somebody who
  typed "alex" is how people point their frontend at a colleague's workspace.
- Update reads the row, runs mutate() on it and writes back, all INSIDE one write transaction (a
  read-modify-write spanning two transactions loses a concurrent edit), and bumps revision by exactly 1.
- Settings are stored as JSON TEXT and read back through domain.ParseSettings, so a row written by an
  older version gains new defaults instead of zero values. Create fills domain.DefaultSettings() when
  in.Settings is nil, which is what gives each workspace its own random signing key.
- List is ordered by id ASC, deterministic.

TESTS: table-driven, real SQLite in t.TempDir(). Cover: create with an explicit slug; create deriving a
slug from a Cyrillic name; duplicate slug -> ErrSlugTaken; reserved slug -> error; ByID/BySlug missing ->
ErrNotFound; Update bumps revision exactly once and persists settings; Delete; List filtered by owner;
and two concurrent Creates racing for the same derived slug both succeeding with different slugs.
Finish with: gofmt -l internal/workspaces && go build ./internal/workspaces/ && go vet ./internal/workspaces/ && go test ./internal/workspaces/ -race -count=1`,
    { label: 'workspaces', phase: 'Foundation', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${CTX}
YOUR TASK: deployment and developer-experience files. NO Go code. Files you own:
  Dockerfile
  .dockerignore
  docker-compose.yml
  Makefile
  .env.example
  README.md
  scripts/smoke.sh
  deploy/Caddyfile.example
  .gitignore            (append only — it exists; do not remove existing entries)
Skills: none apply to this task; skip rule 5.

Read DESIGN.md "## 16. Развёртывание" — its env-var table is authoritative. Copy EVERY variable into
.env.example with its documented default and a one-line comment. Also read "## 19. Фазы" so README does
not promise P1 features.

HOW P0 IS VERIFIED — READ THIS BEFORE WRITING ANYTHING. There is no DNS and no TLS anywhere in this
verification. The dispatcher decides on the Host header, so a Host header is the whole test:
  curl -s -H 'Host: alex.mock.local'  http://localhost:8080/__mocker/health     # -> 200 + JSON
  curl -s -H 'Host: nope.mock.local'  http://localhost:8080/__mocker/health     # -> 404
  curl -s -H 'Host: mocker.local'     http://localhost:8080/healthz             # -> 200
Use the domain pair mock.local / mocker.local consistently in every file you write.

- docker-compose.yml: one service, named volume for /data, env_file .env, and it MUST publish
  127.0.0.1:8080:8080 or the commands above have nothing to hit. OMIT the healthcheck and say why in a
  comment: the final image is distroless, so there is no shell and no curl to run one, and the binary
  has no healthcheck subcommand.
- .env.example: MOCKER_DEV=1 is MANDATORY in the localhost section, with the reason spelled out —
  without it CookieSecure() is true, curl will not send a Secure cookie over plain http, and admin
  login fails before the routing is ever exercised. Include MOCKER_ADDR (:8080), MOCKER_LOG_LEVEL
  (info) and MOCKER_DEV alongside DESIGN §16's table. Give MOCKER_SHARED_PASSWORD_HASH a placeholder
  and point at "make hash-password".
- Dockerfile: multi-stage. Builder golang:1.26 (alpine if it exists), copy go.mod/go.sum first for layer
  caching, use --mount=type=cache,target=/go/pkg/mod, then
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mocker ./cmd/mocker
  Final stage gcr.io/distroless/static:nonroot, USER nonroot, VOLUME /data, EXPOSE 8080,
  ENTRYPOINT ["/mocker"]. cmd/mocker does not exist yet — another agent writes it this run. Do NOT try
  to build the image.
- scripts/smoke.sh: bring the stack up with docker compose, wait for the port, run the three curl checks
  above, assert the status codes and that the health body contains the slug, print a clear pass/fail
  line per check, then tear down. set -euo pipefail. Must pass shellcheck.
- Makefile: build, run, test (go test ./... -race -count=1), lint (go vet + gofmt -l), fmt, docker,
  smoke, hash-password, clean. .PHONY. No clever shell.
- deploy/Caddyfile.example: wildcard TLS for *.mock.corp.internal plus the admin host, reverse-proxying
  to mocker:8080 with X-Forwarded-Proto. Label it clearly at the top: illustrative, for the customer's
  contour, NOT exercised by P0 — and note that MOCKER_TRUST_PROXY must name the proxy's CIDR or the
  Secure cookie and the traffic source both break (DESIGN §15/§16).
- README.md in Russian, matching DESIGN.md's tone (direct, no marketing): what mocker is in three
  sentences; what P0 does and does not do today; a quick start whose FIRST command is docker compose up
  and whose verification is the Host-header curls above (do NOT mention /etc/hosts — it is not needed);
  the env table; how to generate the password hash; repo layout; how to run the tests.

You may not run go commands. Verify by reading your own files back, running shellcheck on smoke.sh, and
checking every variable in .env.example against DESIGN §16 plus the three named above.`,
    { label: 'ops', phase: 'Foundation', model: 'sonnet', schema: SCHEMA }),
])

const foundationOK = foundation.filter(Boolean)
logDeviations('Foundation', foundationOK)
if (foundationOK.length < 3) {
  log(`ABORT: Foundation produced ${foundationOK.length}/3. Everything downstream compiles against these packages.`)
  return { error: 'foundation incomplete', foundation: foundationOK }
}

// ------------------------------------------------------------------ gate 1 --
phase('Gate 1')

const gate1 = await parallel([
  () => agent(`Repo root is your CWD. READ-ONLY: do not modify any file.
Two packages were just written: internal/auth and internal/workspaces. Two more sections are about to be
built on them, so a defect here is cheapest to catch now.
YOUR VECTOR: correctness. Check the SQL against internal/store/migrations/0001_init.sql, the atomicity of
slug reservation (probe and insert must share one transaction and one connection), revision bumping
exactly once, errors.Is vs ==, expired-session handling, and tests that would still pass with the
implementation deleted. Run: go test ./internal/auth/ ./internal/workspaces/ -race -count=1 and report
anything that fails or is skipped.
Report only defects you can point at with file and line.`,
    { label: 'gate1:correctness', phase: 'Gate 1', model: 'sonnet', schema: REVIEW_SCHEMA }),

  () => agent(`Repo root is your CWD. READ-ONLY: do not modify any file.
Two packages were just written: internal/auth and internal/workspaces.
YOUR VECTOR: security of the auth package specifically. Check argon2id parameters and that they are
parsed back from the encoded hash rather than assumed; salt uniqueness per hash; constant-time
comparison of both the password hash and the CSRF token; whether a bad password and an unknown name are
distinguishable by response or by code path; session id entropy (256 bits of crypto/rand) and that
neither id nor CSRF token is ever logged; cookie attributes, above all the ABSENCE of a Domain attribute
— a Domain would hand the admin session to every workspace subdomain. Also check SQL for string
concatenation.
Report each as a concrete attack: what the attacker sends, what they get. File and line required.`,
    { label: 'gate1:security', phase: 'Gate 1', model: 'sonnet', schema: REVIEW_SCHEMA }),
])

const gate1Findings = gate1.filter(Boolean).flatMap((r) => r.findings || [])
const gate1Blockers = gate1Findings.filter((f) => f.severity === 'blocker')
log(`Gate 1: ${gate1Findings.length} findings, ${gate1Blockers.length} blockers`)

if (gate1Blockers.length > 0) {
  const list = gate1Blockers.map((f, i) => `${i + 1}. ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`).join('\n')
  const gate1Fix = await agent(`${CTX}
You own internal/auth/ and internal/workspaces/ for this task only. Two reviewers found blockers in the
foundation two more sections are about to be built on. Verify each against the code FIRST — reviewers
false-positive; if a finding is wrong, say so in "deviations" with the evidence instead of "fixing" it.
Add a test for every real defect you fix.

${list}

Finish with: gofmt -l internal/auth internal/workspaces && go build ./... && go vet ./internal/auth/ ./internal/workspaces/ && go test ./internal/auth/ ./internal/workspaces/ -race -count=1`,
    { label: 'gate1:fix', phase: 'Gate 1', model: 'sonnet', schema: SCHEMA })
  if (gate1Fix) logDeviations('Gate 1 fix', [gate1Fix])
}

// ---------------------------------------------------------------- phase 2 --
phase('Planes')

const asWritten = foundationOK.flatMap((r) => r.contracts || []).join('\n')

const planes = await parallel([
  () => agent(`${CTX}
YOUR TASK: the mock plane. Files you own (create them):
  internal/mockplane/plane.go
  internal/mockplane/cors.go
  internal/mockplane/plane_test.go
Skills (if present): golang-testing.

You compile against the workspaces package. Its dictated contract:
${SIG_WS}
The signatures its author reported as actually written:
${asWritten}
If the two disagree, the code on disk wins — read internal/workspaces/repo.go and follow it.

Read DESIGN.md "## 6. Путь запроса" and the preflight/normalisation parts of "## 8. Роутер".
For the reserved prefix, search rather than read a section: rg -n '__mocker' DESIGN.md

Implement:
  type Source interface { BySlug(ctx context.Context, slug string) (*workspaces.Workspace, error) }
  type Plane struct{ ... }
  func New(cfg *config.Config, src Source, log *slog.Logger) *Plane
  func (p *Plane) ServeHTTP(w http.ResponseWriter, r *http.Request)
  func (p *Plane) ServeSlug(w http.ResponseWriter, r *http.Request, slug string)
  func NormalizePath(p string) string
ServeSlug exists because MOCKER_ROUTING=path has no workspace host to parse: the dispatcher extracts the
slug from /w/{slug}/... and hands it over. ServeHTTP resolves the slug from Host and then calls ServeSlug,
so there is exactly one code path after resolution.

THE ORDER OF OPERATIONS IS THE POINT OF P0 (DESIGN §6) AND IS EXACTLY:
  1. resolve the workspace from Host FIRST (cfg.IsWorkspaceHost -> slug -> src.BySlug)
  2. THEN set the CORS headers for the resolved workspace — before any branch, so a later 404 or a
     panic-500 still carries them (a browser that sees a CORS error instead of the real status hides
     the actual cause)
  3. THEN the preflight short-circuit (OPTIONS carrying Access-Control-Request-Method) -> 204
  4. THEN the reserved prefix (cfg.ReservedPrefix, default "/__mocker")
  5. THEN everything else -> 404; the route table itself arrives in P1

Behaviour:
- Unknown slug: 404 whose body distinguishes "the host arrived, this workspace does not exist" from
  "the host never arrived", naming the slug that was tried. A preflight for an unknown workspace still
  answers 204 using domain.DefaultSettings().CORS.
- A host that is not under the base domain at all: 404 naming the expected host shape.
- GET/HEAD {prefix}/health -> 200 with EXACTLY the DESIGN §14 shape:
  {"ok":true,"workspace":"<slug>","revision":<n>,"spec":<specID or null>}
- Any other {prefix}/... path -> 404, code "not_implemented_yet", naming the phase that brings it.
- HEAD is treated as GET with an empty body.
- Recover from panics INSIDE the plane (httpx.Recover wrapped around your handler in New), so a
  panic-500 keeps the CORS headers set in step 2.

cors.go — the full preflight set (DESIGN §8): Access-Control-Allow-Origin (the echoed Origin, never "*"
when credentials are on), Vary: Origin, Access-Control-Allow-Methods
"GET,POST,PUT,PATCH,DELETE,OPTIONS", Access-Control-Allow-Credentials when settings.CORS.Credentials,
an echo of Access-Control-Request-Headers, Access-Control-Max-Age 600. On normal responses:
Allow-Origin, Vary, Allow-Credentials and Access-Control-Expose-Headers. settings.CORS.Mode ==
domain.CORSOff emits none. A request with no Origin header gets no CORS headers at all.

NormalizePath: strip the query, collapse "//", drop a trailing "/", percent-decode PER SEGMENT so an
encoded "/" inside a segment does not split it.

TESTS: httptest with a fake Source, no database needed. Cover: health on a known slug (assert the exact
key set), unknown slug 404, non-base-domain host 404, preflight 204 with every required header, preflight
for an unknown workspace, CORS headers present on a 404, credentials mode never emitting "*", mode off,
HEAD, ServeSlug used directly, and a NormalizePath table including percent-encoded segments.
Finish with: gofmt -l internal/mockplane && go build ./internal/mockplane/ && go vet ./internal/mockplane/ && go test ./internal/mockplane/ -race -count=1`,
    { label: 'mockplane', phase: 'Planes', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${CTX}
YOUR TASK: the admin plane. Files you own (create them):
  internal/admin/server.go
  internal/admin/security.go
  internal/admin/auth_handlers.go
  internal/admin/workspace_handlers.go
  internal/admin/admin_test.go
Skills (if present): golang-security, golang-testing.

You compile against two packages written minutes ago. Their dictated contracts:
${SIG_AUTH}

${SIG_WS}
The signatures their authors reported as actually written:
${asWritten}
If the two disagree, the code on disk wins — read internal/auth/*.go and internal/workspaces/repo.go.

Read DESIGN.md "## 14. Admin API и UI" (the route list; implement only the P0 subset below) and
"## 15. Авторизация и модель угроз" IN FULL — its CSRF paragraph is a requirement, not advice.

Implement:
  type Server struct{ ... }
  func New(cfg *config.Config, sessions *auth.Manager, ws *workspaces.Repo, log *slog.Logger) *Server
  func (s *Server) Handler() http.Handler
  func UserFrom(ctx context.Context) (*auth.User, bool)
Handler() returns the mux wrapped in the ADMIN-SPECIFIC middleware only (auth, CSRF, security headers,
rate limit). It must NOT add Recover, RequestLog or MaxBody — main wraps those once around the whole
dispatcher, and wrapping twice doubles the log lines and the body limit.

P0 ROUTES (method-aware ServeMux patterns, nothing else):
  GET   /healthz              -> 200 {"ok":true}                  no auth, no CSRF
  GET   /readyz               -> 200, or 503 when the database does not answer
  POST  /api/auth/login       {password, name} -> sets the cookie, returns {user, csrfToken}
  POST  /api/auth/logout      -> 204, clears the cookie
  GET   /api/me               -> {user, csrfToken} or 401
  GET   /api/workspaces       -> the caller's workspaces; ?all=1 returns everyone's
  POST  /api/workspaces       {name, slug?} -> 201 with the workspace, INCLUDING the slug chosen
  GET   /api/workspaces/{id}
  PATCH /api/workspaces/{id}  {name?, settings?}
  DELETE /api/workspaces/{id} -> 204

SECURITY REQUIREMENTS — each is a real attack, not ceremony. Assume an attacker who is inside the
contour and can host a page on a sibling subdomain.
- CSRF: SameSite=Lax does not protect against that page. EVERY state-changing request
  (POST/PUT/PATCH/DELETE) must satisfy ALL of:
    (a) Content-Type is exactly application/json — a tolerant parser turns the POST into a "simple"
        CORS request and hands the attacker a plain form submission,
    (b) Origin equals the configured admin host; when Origin is absent, Referer must, and when BOTH
        are absent the request is REJECTED, not allowed,
    (c) the X-CSRF-Token header equals the session's csrf_token, compared with
        subtle.ConstantTimeCompare.
  Login is exempt from (c) only — there is no session yet — but not from (a) or (b).
- Wrong Content-Type answers 415. Malformed JSON answers 400 (httpx.CodeBadRequest), never 500.
- The admin plane serves NO CORS headers at all. It is same-origin only.
- Bodies: json.Decoder with DisallowUnknownFields, over a body already limited by main's MaxBody.
- Rate limit POST /api/auth/login only: an in-memory token bucket keyed by
  httpx.ResolvePeer(r, cfg.TrustProxy).Client(), 10 attempts per minute, answering 429. PRUNE the map
  (stale keys must not accumulate) — an unbounded map keyed by remote address is a memory exhaustion
  bug. The mock plane gets NO rate limit (DESIGN §18).
- Auth middleware stores the session and user in the request context behind an unexported key type;
  UserFrom is the only accessor.
- Cookies: secure = httpx.ForwardedProto(r, cfg.TrustProxy) == "https" || cfg.CookieSecure().
- Ownership: owner_id is a LABEL, not a trusted identity (DESIGN §15). Any logged-in user may read and
  edit any workspace; GET /api/workspaces merely DEFAULTS to the caller's own. Write that in a comment
  so nobody later "fixes" it into an authorisation system the design explicitly disclaims.
- Security headers on every response: X-Content-Type-Options: nosniff, X-Frame-Options: DENY,
  Referrer-Policy: no-referrer.

TESTS: httptest against a real SQLite database (t.TempDir + store.Open + Migrate + a real auth.Manager
and workspaces.Repo). See the TEST CONFIG block above — a bare Config literal will fail in three
different ways. Cover: login sets an httpOnly cookie with no Domain; wrong password 401; /api/me without
a cookie 401; a POST with a valid session but no CSRF token 403; a POST with a foreign Origin 403; a POST
with neither Origin nor Referer 403; Content-Type text/plain 415; create workspace 201 with the chosen
slug in the body; create with a taken slug 409; PATCH bumps revision; DELETE 204 then GET 404; the login
rate limit answering 429.
Finish with: gofmt -l internal/admin && go build ./internal/admin/ && go vet ./internal/admin/ && go test ./internal/admin/ -race -count=1`,
    { label: 'admin', phase: 'Planes', model: 'sonnet', schema: SCHEMA }),
])

const planesOK = planes.filter(Boolean)
logDeviations('Planes', planesOK)
if (planesOK.length < 2) {
  log(`ABORT: Planes produced ${planesOK.length}/2. Integration cannot wire a plane that does not exist.`)
  return { error: 'planes incomplete', foundation: foundationOK, planes: planesOK }
}

// ---------------------------------------------------------------- phase 3 --
phase('Integrate')

const allContracts = [...foundationOK, ...planesOK].flatMap((r) => r.contracts || []).join('\n')
const allTodo = [...foundationOK, ...planesOK].flatMap((r) => r.todo || []).join('\n')
const allFiles = [...foundationOK, ...planesOK].flatMap((r) => r.files || [])

const integration = await agent(`${CTX}
Every other package is on disk now. Contracts as their authors reported them:
${allContracts}

Deliberately left for later by them:
${allTodo}

YOUR TASK: wire it together and make the whole tree green. Files you own (create them):
  cmd/mocker/main.go
  internal/server/server.go
  internal/server/server_test.go
HARD RULE 1 is relaxed for you: you MAY make MINIMAL fixes to other files where they do not compile or a
signature disagrees with its caller — but prefer an adapter in your own files, and list every foreign
file you touched. HARD RULE 2 is also relaxed: you MAY run go mod tidy. You are the only agent running.
Skills (if present): golang-safety.

internal/server/server.go — the host dispatcher, the thing P0 exists to prove:
  func New(cfg *config.Config, admin http.Handler, mock *mockplane.Plane, log *slog.Logger) http.Handler
  Host mode: cfg.IsAdminHost(r.Host) -> admin; cfg.IsWorkspaceHost(r.Host) -> mock.ServeHTTP;
  neither -> 404 whose body names BOTH expected host shapes, because in a closed contour the real
  question is "which of DNS, TLS or my config is wrong".
  Path mode (cfg.Routing == config.RoutingPath, DESIGN §16's emergency mode): one origin for both
  planes. /api/*, /healthz and /readyz go to admin; /w/{slug}/... strips the prefix from r.URL.Path and
  calls mock.ServeSlug(w, r, slug) — IsWorkspaceHost cannot help here because path mode does not even
  require a base domain. Add a comment naming what path mode breaks: absolute paths, Location headers
  and cookie Path.

cmd/mocker/main.go:
  - subcommand "hash-password": reads the password from the first argument or stdin, prints the argon2id
    hash for MOCKER_SHARED_PASSWORD_HASH and exits. Must work with NO env configured.
  - normal start: config.Load() (print EVERY validation error, exit 2), a slog JSON logger whose level
    comes from MOCKER_LOG_LEVEL (add the field to config.Config and to its Load — DESIGN §16 lists it),
    store.Open + Migrate, then auth.NewSharedPassword / auth.NewManager / workspaces.NewRepo /
    admin.New / mockplane.New, the dispatcher from internal/server, wrapped ONCE in
    httpx.Recover + httpx.RequestLog + httpx.MaxBody, an http.Server with ReadHeaderTimeout,
    IdleTimeout and a WriteTimeout, graceful shutdown on SIGINT/SIGTERM with a 15s drain, and a startup
    log line naming the admin host and the workspace host pattern.
  - a janitor goroutine calling sessions.PurgeExpired hourly, stopped by the root context.

internal/server/server_test.go — the P0 ACCEPTANCE TEST from DESIGN §19, encoded without DNS or TLS:
  build the full stack over httptest with BaseDomain "mock.local" and AdminHost "mocker.local" (see the
  TEST CONFIG block; Dev:true is required or the cookiejar drops the session cookie), log in through the
  admin API, create a workspace named "alex" (remember Origin + X-CSRF-Token + application/json), then
  assert, by setting req.Host rather than by resolving anything:
    - GET http://alex.mock.local/__mocker/health  -> 200, body {"ok":true,"workspace":"alex","revision":1,"spec":null}
    - GET http://nope.mock.local/__mocker/health  -> 404
    - GET http://mocker.local/healthz             -> 200
    - an unrelated Host                           -> 404 naming both host shapes
  The point is that the dispatcher, not DNS, is under test.

FINISH BY RUNNING all four, and do not report success until every one is clean:
  gofmt -l .        (must print nothing)
  go build ./...
  go vet ./...
  go test ./... -race -count=1
Paste the real output of the last command into "verified". If something is broken beyond your remit, say
so explicitly in "deviations" — never delete or skip a test to get green.`,
  { label: 'integrate', phase: 'Integrate', model: 'sonnet', schema: SCHEMA })

if (!integration) {
  log('ABORT: integration failed. Reviewing a tree that does not compile wastes both reviewers.')
  return { error: 'integration failed', foundation: foundationOK, planes: planesOK }
}
logDeviations('Integrate', [integration])

// ---------------------------------------------------------------- phase 4 --
phase('Review')

const touched = [...allFiles, ...(integration.files || [])].join(', ')

const reviewCtx = `Repo root is your CWD. READ-ONLY: do not modify any file — your job is findings, not edits.
This is P0 of the Go service described in DESIGN.md (Russian; locate sections with rg -n '^## ' DESIGN.md
and read only what you need — never the whole file).
Files written or changed by this run: ${touched}
The commit before this run is the spine, so "git diff HEAD" shows exactly what to review.
P0 deliberately has NO spec import, NO response generation and NO route table — do not report those as
missing. Judge only what is present.
Report ONLY defects you can point at with a file and a line. No praise, no style nits that do not change
behaviour. Empty findings array if your vector is clean.`

const reviews = await parallel([
  () => agent(`${reviewCtx}

YOUR VECTOR: correctness and bugs. Hunt for defects that produce wrong behaviour: SQL that disagrees with
internal/store/migrations/0001_init.sql; a read-modify-write that spans two transactions or two pools;
revision not bumping or bumping twice; error values compared with == instead of errors.Is; nil map or
pointer dereferences; goroutine and context leaks (the janitor, the rate limiter); time units confused
(seconds vs milliseconds); an off-by-one in the X-Forwarded-For hop index; HEAD versus GET; path
normalisation that mangles percent-encoded segments; middleware applied twice; and tests that assert
nothing or would still pass with the implementation deleted.
Run "go test ./... -race -count=1" yourself and report anything that fails, is skipped, or is flaky.`,
    { label: 'review:correctness', phase: 'Review', model: 'sonnet', schema: REVIEW_SCHEMA }),

  () => agent(`${reviewCtx}

YOUR VECTOR: security. The mock plane is deliberately open, so the admin plane is the only boundary that
exists. Assume an attacker inside the corporate contour who can host a page on a sibling subdomain.
Check at minimum:
- CSRF: is EVERY state-changing route covered by the Origin/Referer check AND the token check AND the
  strict Content-Type check? Is any mutating route registered outside the middleware? Is the token
  compared in constant time? Can a request with NEITHER Origin nor Referer slip through?
- Session cookies: httpOnly, SameSite, how Secure is derived, MaxAge, and the ABSENCE of a Domain
  attribute — a Domain hands the admin session to every workspace subdomain. Is the session id 256 bits
  of crypto/rand, and is it or the CSRF token ever logged?
- Passwords: argon2id parameters, salt uniqueness, constant-time compare, and whether a bad password and
  an unknown name are distinguishable by response or by timing.
- Host parsing: can a crafted Host ("alex.mock.local.evil.com", "MOCKER.LOCAL", a port, a trailing dot,
  an IPv6 literal, "alex.mock.local:443@x", an empty Host) reach the wrong plane or resolve to the wrong
  workspace? Host confusion is the highest-value bug class in this codebase — spend your time here.
- X-Forwarded-For / X-Forwarded-Proto: can an untrusted client forge either? Does a forged forwarded
  proto turn the Secure cookie off?
- Injection: SQL built by concatenation; user-controlled values reaching a log line unescaped; response
  splitting through a header echoed from the request (the CORS echo paths are the candidates).
- Resource exhaustion: unbounded bodies, unbounded slug or name length, and whether the login rate
  limiter's map is ever pruned.
Report each as a concrete attack: what the attacker sends, what they get.`,
    { label: 'review:security', phase: 'Review', model: 'opus', schema: REVIEW_SCHEMA }),
])

const findings = reviews.filter(Boolean).flatMap((r) => r.findings || [])
const minors = findings.filter((f) => f.severity === 'minor')
for (const m of minors) log(`minor: ${m.file}:${m.line} — ${m.summary}`)

let outstanding = findings.filter((f) => f.severity === 'blocker' || f.severity === 'major')
log(`Review: ${findings.length} findings — ${findings.length - minors.length} actionable, ${minors.length} minor (logged, not fixed)`)

if (outstanding.length === 0) {
  log('Nothing actionable: skipping Fix and Verify.')
  return { integration, findings, rounds: 0 }
}

// ------------------------------------------------------------ phases 5-6 --
// Fix, then VERIFY the fix. The verify pass exists because Fix is otherwise
// terminal: a semantically wrong but compiling security fix (== where a
// constant-time compare belongs) survives gofmt, build, vet and the tests, and
// nobody is watching this run. Bounded to two rounds — no unbounded loop.
const rounds = []
for (let round = 1; round <= 2 && outstanding.length > 0; round++) {
  phase('Fix')
  const list = outstanding
    .map((f, i) => `${i + 1}. [${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`)
    .join('\n')

  const fixed = await agent(`${CTX}

HARD RULES 1 and 2 DO NOT APPLY TO YOU: you run alone, you own every file, and you may run go mod tidy.

Round ${round}. Two reviewers audited the P0 tree. Apply the findings below, changing as little as
possible — every edit must map to a numbered finding.

${list}

RULES
- Verify each finding against the code FIRST. Reviewers false-positive. If a finding is wrong, do NOT
  "fix" it — record it in "deviations" with the evidence that it is wrong.
- A finding needing a design change rather than a code change goes to "todo", not into a hasty redesign.
- Add or extend a test for every real defect you fix, so it cannot come back silently.
- Finish with all four clean and paste the real output of the last one into "verified":
    gofmt -l .   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1`,
    { label: `fix:round${round}`, phase: 'Fix', model: 'sonnet', schema: SCHEMA })

  if (fixed) logDeviations(`Fix round ${round}`, [fixed])

  phase('Verify')
  const verdict = await agent(`Repo root is your CWD. READ-ONLY: do not modify any file.

A fix agent just claimed to have addressed the findings below. Its own report is self-assessed, so check
it. Two things, in this order:

1. Run, and report the REAL output:
     gofmt -l .   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
   "green" is true only if all four are clean.
2. For EACH numbered finding, decide: fixed, credibly rebutted, or still open. A fix that compiles is not
   a fix — check the semantics. Specifically distrust: a constant-time comparison replaced by ==, a CSRF
   check moved somewhere it no longer covers every mutating route, a cookie attribute quietly dropped, a
   test weakened or deleted to reach green. Read "git diff HEAD" to see exactly what changed.
   List anything still open in "unresolved", one line each as "<n>. <file>:<line> <why still open>".

${list}`,
    { label: `verify:round${round}`, phase: 'Verify', model: 'opus', schema: VERIFY_SCHEMA })

  rounds.push({ round, fixed, verdict })

  if (!verdict) {
    log(`Round ${round}: verification agent returned nothing — stopping.`)
    break
  }
  log(`Round ${round}: green=${verdict.green}, ${verdict.unresolved.length} unresolved`)
  for (const u of verdict.unresolved) log(`still open: ${u}`)

  if (verdict.green && verdict.unresolved.length === 0) {
    outstanding = []
    break
  }
  // Carry only what the verifier says is still open into the next round.
  const stillOpen = new Set(verdict.unresolved.map((u) => u.split('.')[0].trim()))
  outstanding = outstanding.filter((_, i) => stillOpen.has(String(i + 1)))
  if (round === 2 && outstanding.length > 0) {
    log(`STOPPING after 2 rounds with ${outstanding.length} findings still open — a human should look at these.`)
  }
}

return { integration, findings, rounds, stillOpen: outstanding }

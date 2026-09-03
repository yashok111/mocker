export const meta = {
  name: 'mocker-p1d1',
  description: 'P1d slice 1: the admin UI ships inside the binary — Vite/React app, go:embed, login, workspaces, the Connect panel, proven by a real browser through the docker image',
  phases: [
    { title: 'Foundation', detail: 'web/ scaffold, internal/webui + the mount, build plumbing', model: 'sonnet' },
    { title: 'Gate 1', detail: 'two reviewers on the toolchain and the serving path', model: 'sonnet' },
    { title: 'Shell', detail: 'API client and types, login and session, workspaces screen', model: 'sonnet' },
    { title: 'Gate 2', detail: 'two reviewers on the app shell', model: 'sonnet' },
    { title: 'Connect', detail: 'the «Подключить» panel and its browser-side probe', model: 'sonnet' },
    { title: 'Gate 3', detail: 'two reviewers on the panel', model: 'sonnet' },
    { title: 'Accept', detail: 'Playwright through the image, then the curl-level smoke section', model: 'sonnet' },
    { title: 'Gate 4', detail: 'two reviewers on the acceptance harness', model: 'sonnet' },
    { title: 'Final', detail: 'repo-wide bars, an opus security review, one fix round, verification', model: 'sonnet' },
  ],
}

// ---------------------------------------------------------------------------
// The decisions. Every one was checked by command on this box before it was
// written down. The pre-flight gate that reviewed this list returned 9 blockers
// and ~20 majors against its first draft, and a second (delta) round found 3
// more that the first round's own fixes had introduced. All are corrected in
// place below — a superseded rule is REPLACED, never left sitting under its
// replacement, because an agent follows whichever wording it reads last, and
// this gate has twice caught a stale sentence a few lines under its own
// replacement in a different consumer.
// ---------------------------------------------------------------------------

log('SCOPE — this run is SLICE 1 of P1d: the first web UI in this tree. Nothing here changes how a mock answers a request. What ships: web/ (Vite + React + TypeScript + Tailwind + TanStack Query), internal/webui (the built dist, embedded with go:embed and served by the admin plane), an API client that speaks this project\'s cookie+CSRF protocol, and three screens — «Вход» (DESIGN §14 screen 1), the workspace list with creation (screen 2 minus its auto-create half) and the «Подключить» panel (screen 4, browser half only). The criterion is a real Chromium, driving the DOCKER IMAGE, that logs in, creates a workspace and sees «Проверить» read that workspace\'s own mock host cross-origin.')

log('SCOPE IS CUT DELIBERATELY, and the cut is the load-bearing decision. P1\'s UI row in DESIGN §19 also names the «Спеки» screen, the status tabs with pinned bodies, «создать правку из ответа»/«создать endpoint из запроса» and the traffic feed (the custom-endpoint SCREEN is a P2 row item; only the two conversions are P1). All of them are repetitions of a vertical that does not exist yet: no build, no embed, no serving path, no API client, no session handling, no browser harness. Slice 1 builds the vertical and the three cheapest screens on it; slices 2 and 3 add screens against a proven spine. A single run that tried all seven screens would be deciding the spine\'s hardest questions (mount semantics, CSP, dev proxy, browser harness) inside agents whose real task was a tree view.')

log('SCOPE CUT #2, added by the pre-flight gate and worth more than it costs: PATH MODE DOES NOT GET THE UI in this slice. internal/server/servePath answers everything that is not /healthz, /readyz, /api/* or /w/{slug}/... with a diagnostic JSON 404, and forwarding that default to the admin handler would make the subtest "unmatched path 404s naming the path-mode shapes" wrong. That would be the ONE authorised edit to a pre-existing test in this run — and an authorised-edit exception has to be repeated verbatim in every tripwire or a correct run reports its own required output as tampering, which cost the previous phase a full gate round. Cutting it makes the rule absolute: NO pre-existing *_test.go may be modified, full stop. What it costs, stated honestly: with MOCKER_ROUTING=path (the emergency mode for a contour without wildcard DNS) the UI is unreachable and "/" keeps answering its JSON 404 naming the shapes path mode accepts. internal/server/ leaves this run\'s file list entirely; the branch and its test are the first thing P1d-2 does.')

log('DEFERRED, each named so a gap does not read as an oversight. (1) The «Спеки» screen, the endpoint editor, the custom-endpoint screen and the traffic feed — P1d-2/P1d-3; every API they need already shipped in P1a..P1c-2. (2) Monaco: DESIGN §14 names it in the stack line, but §19 defers «дерево схемы с рецептами» to P2 and NOTHING in this slice edits a JSON body — Monaco arrives with the editor that needs it. (3) The server side of «Проверить» (mocker dialling https://<slug>.<base>/__mocker/health by external name): rg over non-test sources shows this tree has NO outbound HTTP client at all, and POST /api/specs with source:"url" is refused with 400 — the first one is an SSRF surface under §15 and gets its own slice with MOCKER_URL_IMPORT_ALLOWLIST, which exists in config and is used by nobody. (4) MOCKER_DEFAULT_SPEC auto-creating a first workspace: the knob is parsed and read by nothing, recorded as dead in HANDOFF, and it belongs with the screen that shows the chosen slug. (5) Scenarios, history, resources — P2/P3, not P1 at all. (6) i18n: the interface is Russian, hard-coded, in DESIGN §14\'s own words, which also forbid «рецепт», «JSON Patch» and «матчер» from appearing in it. (7) HANDOFF.md is NOT written by this run — it is written by hand after it, as every phase before this one was.')

log('DECISION — the SPA is a WRAPPER around the admin mux, never a pattern inside it, and this was measured rather than reasoned. Built the real net/http.ServeMux (Go 1.26) with this project\'s route set plus the catch-alls the first draft proposed: PUT /api/workspaces went from "405 Allow: GET, HEAD, POST" to a JSON 404, POST /api/me from 405 to 404, GET /api/auth/login (a POST-only route) from 405 to 404, and POST /healthz answered the SPA with 200. Probing with mux.Handler(r) fails identically — it returns an EMPTY pattern for a method mismatch too. So the rule is explicit and lives in one place: the UI answers only when the method is GET or HEAD AND the path is not /healthz, not /readyz, is not exactly "/api" and does not start with "/api/". Everything else reaches the mux untouched and keeps the exact status it has today. Verified nothing else pins this: rg "405|MethodNotAllowed|page not found" over internal/admin/*_test.go, internal/server/*_test.go and scripts/smoke.sh matches nothing.')

log('DECISION — internal/admin imports internal/webui and mounts it UNCONDITIONALLY inside Handler(). Not a New() parameter, not a setter. admin.Server.Handler() has 8 call sites across 7 files (six test files plus cmd/mocker/main.go:167) and none of them could pass a UI; admin.New\'s signature is held fixed by the Server struct\'s own field comments and by the SetLiveState/SetTraffic doc comments, which spell out why each dependency arrives the way it does; and a setter left unset means either a nil handler (a panic in six test files) or an "if s.ui != nil" — which is exactly the conditional wiring that ships a dead feature with a fully green suite, twice recorded in HANDOFF. Unconditional mounting means there is no wiring step to forget: cmd/mocker/main.go is not touched by this run at all.')

log('DECISION — the embedded bytes live in internal/webui/dist, and the handler takes its filesystem as a PARAMETER. Verified by building both: "//go:embed dist" over a directory holding only .gitkeep fails to compile ("contains no embeddable files"), "//go:embed all:dist" compiles and fs.Stat(sub, "index.html") then returns a not-exist the handler branches on. So all: is load-bearing, internal/webui/dist/.gitkeep exists and is NOT git-ignored (so the commit that lands this run picks it up), and a binary built without `make ui` serves one honest 503 naming the command instead of a blank 200. The signature is webui.Handler(fsys fs.FS, next http.Handler, cfg, log) with webui.Dist as the embedded default, because a test written over the EMBEDDED FS is state-dependent — it would assert the SPA on a developer\'s machine and the 503 on a fresh clone, and `make test` is a standing bar that must be green in both states. Every unit test drives the handler over fstest.MapFS.')

log('DECISION — the image CANNOT be built without the node stage, and that is this slice\'s real guard. .dockerignore excludes /internal/webui/dist/ entirely (including the .gitkeep), so a locally built dist cannot ride into the context on "COPY . ."; the node stage builds the app and its output is COPYed into the Go stage BEFORE go build. If that COPY is missing, misordered or points at the wrong path, "//go:embed all:dist" fails the image build outright ("pattern all:dist: no matching files found") instead of silently shipping a 503 placeholder that every green test in this repo would still pass. Verified: that is exactly what the embed directive does with no directory present. Tripwire for the gates, and it reads the WORKTREE rather than the index because the index cannot see a file Vite just deleted: `test -f internal/webui/dist/.gitkeep` must succeed, `git check-ignore --no-index -q` on it must exit 1 (without --no-index git skips an indexed path and answers "not ignored" whatever the rules say), and `git status --porcelain -uall internal/webui/dist` must list the .gitkeep and nothing else (without -uall an untracked directory collapses to one line that looks the same whether the ignore rule works or is missing).')

log('DECISION — the Go bars are SCOPED: ./cmd/... ./internal/... for build, vet and test, and "gofmt -l ./cmd ./internal" (never "gofmt -l ." and never a file list from git ls-files, which cannot see this run\'s untracked new files), because web/node_modules sits inside the Go module. Verified: the go tool walks it, `go list ./...` returns packages under web/node_modules, and a single .go file shipped by any transitive npm dependency would fail `go build ./...`, `go vet ./...` and `gofmt -l .` for every agent, unfixably. (Measured cost of the walk itself: 3000 synthetic packages took `go list ./...` from 0.10s to 0.43s.) The Makefile\'s test, lint AND fmt targets change to the scoped form in the same slice — fmt included, because "gofmt -w ." would rewrite .go files shipped by an npm package — so the bar in the prompts and the bar a human runs stay the same command. Rejected: naming the directory _web so the go tool skips it — it works, and it costs every reader an explanation forever.')

log('DECISION — the dev proxy rewrites BOTH Host and Origin, with the mechanism written out because Vite has no option for it. server.proxy has changeOrigin (which rewrites Host only); the Origin header needs configure(proxy) { proxy.on("proxyReq", (p) => { p.setHeader("host", ADMIN_HOST); p.setHeader("origin", ADMIN_ORIGIN) }) }. Both are load-bearing on different Go checks: the dispatcher routes on Host, so an un-rewritten 127.0.0.1:8080 gets the unknown-host 404 before the admin plane is reached; and enforceCSRF compares the Origin header\'s hostname to MOCKER_ADMIN_HOST, so an un-rewritten http://localhost:5173 is a 403 on every POST — which an agent would then "fix" by weakening originAllowed. One env var (VITE_ADMIN_HOST, default mocker.local) feeds both.')

log('DECISION — this slice adds a Content-Security-Policy, because it is the slice that starts serving HTML: default-src \'self\'; script-src \'self\'; base-uri \'none\'; frame-ancestors \'none\'; object-src \'none\'; img-src \'self\' data:; style-src \'self\' \'unsafe-inline\'; connect-src \'self\' http://*.<baseDomain>:* https://*.<baseDomain>:*. script-src is written out even though default-src would cover it, because three separate checks in this run assert on script-src and an assertion about an absent directive cannot fail. connect-src is DERIVED FROM cfg.BaseDomain rather than left as "*": the only cross-origin request the app ever makes is to a workspace host under that domain, the port must be wildcarded (a host-source with no port matches only the scheme default, and this project runs on 8080), and in path mode the workspace URL is on the admin origin, which \'self\' already covers. Two Vite settings ride along (build.modulePreload.polyfill = false, build.assetsInlineLimit = 0) to keep the output free of anything a \'self\' policy would refuse. NOTE, and this replaces the first draft\'s confident claim: whether Vite emits an inline script or a data: URI asset for a given version is NOT settled by this document. It is made OBSERVABLE instead — the browser acceptance fails on any pageerror or CSP console violation on any screen it visits, which catches it whatever the mechanism turns out to be.')

log('DECISION — the acceptance stack runs with MOCKER_RESERVED_PREFIX=/__ctl, and the script PROVES it took effect before the browser starts. .env.example pins the default value, so writing /__ctl means stripping that line and appending the new one (smoke.sh\'s own grep -v shape), and a silent failure there would restore the exact hole this defence exists to close. So e2e.sh curls the workspace host twice first: {prefix-default}/health must answer 404 and /__ctl/health must answer 200. It is the difference between a criterion and a decoration. With the default /__mocker, a panel that hard-codes the prefix and rebuilds the URL from window.location produces byte-identical requests to one that reads workspaceView.url and config.reservedPrefix — the assertion passes with the entire seam this slice exists to build deleted. Under /__ctl the hard-coded version 404s. Three more assertions come from the same reasoning: the panel must RENDER the workspace and revision it read (not merely "not throw"), the indicator must be proven against traffic that is actually RECORDED — the mock plane returns from its reserved-prefix branch BEFORE the traffic tee, so the health probe never appears in the feed and only a request to a non-reserved path can move that number, and there is a NEGATIVE control — delete the workspace, press «Проверить» again, and the panel must go red with the honest three-cause copy.')

log('DECISION — scripts/e2e.sh owns its whole environment, reusing scripts/smoke.sh\'s proven shape, and its isolation is the compose PROJECT NAME, not a separate env file. docker-compose.yml pins `env_file: - path: .env` at a fixed relative path, and --env-file/COMPOSE_ENV_FILES change only the interpolation file — they never reach the container, so an .env.e2e would be silently ignored and the stack would come up on whatever .env happened to be there. e2e.sh therefore writes repo-root .env exactly as smoke.sh does (back up the developer\'s, write ours from .env.example, restore in an EXIT trap), and passes -p mocker-e2e so it cannot tear down a stack the developer left running. The other two obligations are smoke.sh\'s too: login needs a KNOWN argon2id hash (generated through the image), and the volume must start empty or the slug "alex" is taken and comes back as "alex-2", breaking the URL assertion. Only one agent per round touches docker. One thing it does NOT need: MOCKER_DEV=1 already lives in .env.example (line 26) and every `make smoke` already runs with it, load-bearingly — curl and Chromium both refuse a Secure cookie over http. No agent may move or delete that line.')

log('DECISION — @playwright/test is pinned to 1.62.1 by number, the only version in this document. That is the release owning ~/.cache/ms-playwright/chromium-1234, already installed on this box, so no browser download sits on the critical path — and `npm ping` proves the npm registry, not cdn.playwright.dev, which is where browsers come from. Everything else is pinned to whatever `npm view <pkg> version` returns AT RUN TIME: no other version number appears anywhere in this run, so no agent can build a different tree by following a different line.')

log('DECISION — the UI is told the reserved prefix; it never hard-codes it. The auth response (POST /api/auth/login and GET /api/me) gains a "config" object — reservedPrefix, baseDomain, routing — written by the Foundation section alongside the mount, because it is consumed by the Shell section\'s types and by the Connect section\'s probe, and landing it later would rewrite an already-written response type. Everything else the panel needs already exists: workspaceView.url is built server-side from the request scheme, <slug>.<baseDomain> and the port from r.Host, so a browser on http://mocker.local:8080 receives http://alex.mock.local:8080 and reconstructs nothing.')

log('DECISION — the cross-origin probe is readable, verified in the code before it became a criterion. internal/mockplane/plane.go sets CORS headers at step 2 for EVERY request, before any branch, including the reserved prefix, and domain.DefaultSettings() is {Mode: reflect, Credentials: true}. The known edge is what the UI copy must respect: a host that resolves but names NO workspace answers 404 through Plane.unresolved, which sets CORS headers only on the preflight branch, so the browser reports a network error rather than a 404. The panel therefore lists three possible causes (name does not resolve, certificate not trusted, workspace gone) and asserts none of them.')

log('DECISION — no router library, no state library beyond TanStack Query, and every interactive element carries a data-testid. Three screens do not pay for a router; a switch over location.pathname with history.pushState is smaller than the dependency. The testids exist so the browser test never selects on Russian copy, which is the text most likely to be reworded in the next slice.')

log('DECISION — the screen-2 empty state gets substitute copy, decided here rather than by whichever agent reaches it. DESIGN §14 mandates «спеки ещё нет: загрузите или попросите коллегу» with a link to the «Спеки» screen — and that screen is out of scope, so the mandated link would be dead. The substitute: «У вас пока нет воркспейсов» plus the create form, and one muted line — «Воркспейс можно создать и без спеки: пока он будет отвечать только на кастомные endpoint\'ы. Экран «Спеки» появится в следующем срезе». No dead link, no invented navigation.')

log('GUARD — the real one, and it is not the golden. internal/gen/testdata/p1b_body_hashes.json (419 hashes) stays a do-not-touch rule, but by its own test — which line of THIS slice\'s diff could make it fail? — the answer is none: nothing here is upstream of the generator. The guard that CAN fail is the existing, already-reviewed test corpus of internal/admin and internal/server plus scripts/smoke.sh\'s pre-existing sections: the mount sits in front of every admin route, and those tests and checks are what notices if it swallows one. Its LIMIT is stated too, because a guard oversold is a guard misread: every admin-plane request in that corpus is a GET on /api/* or /healthz, so it cannot see the 405-on-wrong-method class at all — cases 4-6 of the mount\'s own new tests are the only guard there, which is why the Gate 1 reviewer re-derives them independently. So: the corpus must pass UNMODIFIED. NO pre-existing *_test.go may be modified in this run — there is no authorised exception, since path mode is cut — and every gate diffs for a deleted or weakened assertion. Adding new test files is expected and is not a violation.')

log('Agent count: 18 call sites — 22 agents on a clean path and 35 in the worst case (four gate fixes, eight gate re-reviews, the final fix round), measured by executing this script against a stub harness rather than counted by hand. Model routing: every producer and every gate reviewer is sonnet; the ONE opus agent is the end-of-run security review of the mount, because it is the only step whose miss — an unauthenticated static handler mounted in front of an authenticated plane — is both uncatchable downstream and expensive to undo. Concurrency on this box is min(16, cpus-2) = 2, so a gate\'s two reviewers run together and everything else is serial by design: exactly one agent per round may bring the compose stack up, and `make ui` and `make test` run under the Makefile\'s memory cap because this box\'s kernel OOM killer wiped the whole user slice twice on 2026-08-18.')

// ---------------------------------------------------------------------------
// Paths and contracts.
// ---------------------------------------------------------------------------

// The diff scope for the gates. web/ is a directory here: most of this run's
// output is new files under it, and "git add -N ." plus this list is what makes
// them visible at all. internal/server is deliberately absent — path mode is cut.
const P1D1_PATHS = 'web internal/webui internal/admin Dockerfile .dockerignore .gitignore Makefile README.md scripts'

// The scoped Go bar, spelled once. Pasted into every prompt that runs it, so no
// two agents run a different command and disagree about what "green" means.
const GO_BAR = `  test -z "$(gofmt -l ./cmd ./internal)"   # a lister exits 0 whether or not it printed; this does not
  go build ./cmd/... ./internal/...
  go vet ./cmd/... ./internal/...
  go test ./cmd/... ./internal/... -race -count=1 -p 2
The scope is ./cmd ./internal and NOT ./... — web/node_modules sits inside this Go module, the go tool
walks it, and one .go file shipped by a transitive npm dependency would fail the whole bar for reasons
no agent here can fix. gofmt is given the two DIRECTORIES rather than a file list from git: it walks
them, so it sees the files this run creates, while "gofmt -l $(git ls-files '*.go')" is blind to every
new untracked file — measured, and it means an unformatted new file passes the bar.`

// The UI bar, spelled once for the same reason. The touch is not decoration:
// Vite's emptyOutDir deletes the tracked placeholder on EVERY build, and only
// the Makefile's ui target puts it back — so six agents running the raw npm
// build would end the run with internal/webui/dist/.gitkeep gone and a fresh
// clone unable to compile.
const UI_BAR = `  npm run --prefix web typecheck
  npm run --prefix web test
  npm run --prefix web build
  touch internal/webui/dist/.gitkeep   # Vite's emptyOutDir just deleted it; the tracked placeholder is
                                       # what lets a clone with no npm run still compile
Run all four FROM THE REPO ROOT, and run the last two as a PAIR: the build empties
internal/webui/dist and the touch puts the placeholder back. Those two lines are the ONE exception to
any "do not touch internal/" or "read-only" rule you were given — writing internal/webui/dist/** is
what building the UI means, and leaving the placeholder deleted breaks a fresh clone's compile.`

// What a REVIEWER runs. Same checks minus the build, because a reviewer that
// rebuilds is a reviewer mutating the tree — and in the final phase it would be
// emptying internal/webui/dist under the security reviewer reading it.
const UI_CHECK = `  npm run --prefix web typecheck
  npm run --prefix web test
Do NOT run the npm build: it empties internal/webui/dist, and only the agents told to re-create the
placeholder afterwards may do that.`

const SIG_API = `  // THE ADMIN API AS IT EXISTS AT HEAD. Every shape below was copied out of the Go source, not
  // remembered. Field names are the JSON tags. Nothing here may be "improved" client-side: if a field
  // is missing, the fix is a Go change owned by the Foundation section, not a guess in TypeScript.

  // Error body — every non-2xx a HANDLER produces (internal/httpx/respond.go):
  //   { "error": { "code": string, "message": string, "details"?: unknown } }
  // NOT universal, and this was measured: an /api/ path no pattern matches falls to net/http's own
  // 404 ("404 page not found", text/plain), and a method mismatch on a real route is a 405 with an
  // Allow header. Neither is JSON, both are today's behaviour, and NOTHING in this slice may change
  // them — the rule is only that the SPA never answers them.

  // POST /api/auth/login   body { "name": string, "password": string }
  //   200 -> { "user": UserView, "csrfToken": string, "config": ServerConfig } + session cookie
  //   401 { code: "unauthorized", message: "invalid credentials" } — the SAME answer for a wrong
  //       password and a bad name, on purpose (a different status would leak that the password was right)
  //   415 unless the Content-Type header PARSES to the media type application/json (parameters like
  //       "; charset=utf-8" are accepted — the server calls mime.ParseMediaType, it does not compare
  //       strings)
  //   403 when Origin (or, absent it, Referer) does not have hostname == MOCKER_ADMIN_HOST
  //   429 { code: "rate_limited" } — 10 attempts/minute per (client address, submitted name)
  // GET  /api/me           -> 200 { user, csrfToken, config } | 401
  // POST /api/auth/logout  -> clears the session; needs the CSRF header like any other POST
  //
  // UserView     = { id: number, name: string, role: string, createdAt: number /* unix seconds */ }
  // ServerConfig = { reservedPrefix: string, baseDomain: string, routing: "host" | "path" }
  //   ServerConfig is NEW in this slice, written by the Foundation section (see SIG_CONFIG).
  //
  // Server-side name rules, enforced in internal/auth: trimmed, non-empty, no control characters,
  // at most 64 RUNES. A client that pre-validates uses exactly these and no others.

  // CSRF, from internal/admin/security.go — three independent checks on every POST/PUT/PATCH/DELETE:
  //   1. the Content-Type header must PARSE to the media type application/json (parameters such as
  //      "; charset=utf-8" are fine — the check is mime.ParseMediaType, not string equality)
  //   2. Origin, or Referer when Origin is absent, must have hostname == MOCKER_ADMIN_HOST
  //   3. X-CSRF-Token must equal the session's token
  //   Login is exempt from (3) ONLY. A GET never carries the header.

  // GET    /api/workspaces            -> 200 WorkspaceView[]   (a bare array, no envelope). It lists
  //                                   the CALLER'S OWN workspaces; ?all=1 lists everyone's.
  // POST   /api/workspaces            body { name: string, slug?: string, specId?: number|null }
  //                                   -> 201 WorkspaceView; name is required and trimmed
  // GET    /api/workspaces/{id}       -> 200 WorkspaceView
  // PATCH  /api/workspaces/{id}       body { name?, settings?, specId? } -> 200 WorkspaceView
  // DELETE /api/workspaces/{id}       -> 204, no body
  //
  // WorkspaceView = {
  //   id: number, slug: string, name: string,
  //   specId: number|null, ownerId: number|null, forkedFrom: number|null,
  //   scenarioId: number|null, revision: number,
  //   settings: unknown,   // large; this slice does not read into it and must not type it in detail
  //   url: string,         // this workspace's own externally reachable origin: scheme + <slug>.<baseDomain>
  //                        // + the port from the request's Host. NO base path, NO trailing slash.
  //   createdAt: number, updatedAt: number
  // }
  // The four id-ish fields are POINTERS in Go and are therefore nullable in TypeScript.

  // GET /api/workspaces/{id}/traffic?limit=<n>
  //   -> { rows: TrafficRow[], rate1m: number, dropped: number }
  //   rate1m is computed SERVER-side and is exactly §14 screen 4's "сюда пришло N запросов за минуту".
  // GET /api/workspaces/{id}/traffic/poll?since=<id>&limit=<n>
  //   -> { rows: TrafficRow[], lastId: number, dropped: number }   // the feed's cursor protocol; NO rate
  //   TrafficRow = { id, ts (RFC3339 string), method, path, status, durationMs, matchedKind, ... }
  //   This slice reads rate1m and, in the acceptance test, a row's path. Type the rest as unknown.

  // The mock plane, cross-origin (internal/mockplane):
  // GET {config.reservedPrefix}/health on a workspace origin ->
  //   { ok: true, workspace: string, revision: number, spec: number|null }
  // CORS headers are set for every request before any branch and the default policy reflects the
  // Origin, so this body is READABLE from the admin origin. A host that resolves but names no
  // workspace answers 404 WITHOUT CORS headers, so the browser sees a network error instead — the UI
  // must not report that as "DNS is broken".`

const SIG_CONFIG = `  // NEW in internal/admin/auth_handlers.go, written by the Foundation section and by nobody else.
  // The auth response grows one field; the two existing fields keep their names and shapes.
  //
  // The struct tags are spelled as comments below only because this block travels through a JavaScript
  // template literal; in the code they are ordinary backtick tags, exactly as the rest of the package
  // writes them.
  //
  //   type serverConfigView struct {
  //       ReservedPrefix string    // json tag: reservedPrefix   — cfg.ReservedPrefix, e.g. "/__mocker"
  //       BaseDomain     string    // json tag: baseDomain       — cfg.BaseDomain
  //       Routing        string    // json tag: routing          — "host" | "path", from cfg.Routing
  //   }
  //
  //   type authResponse struct {
  //       User      userView          // json tag: user       (unchanged)
  //       CSRFToken string            // json tag: csrfToken  (unchanged)
  //       Config    serverConfigView  // json tag: config     (NEW)
  //   }
  //
  // Returned by BOTH handleLogin and handleMe, through ONE constructor helper — two hand-written
  // literals drift, and the client bootstraps on one and logs in through the other.`

const SIG_WEBUI = `  // package webui (internal/webui) — a LEAF: the stdlib only, plus internal/config for the CSP.
  // It must not import internal/admin, internal/auth or internal/store.
  //
  //   //go:embed all:dist
  //   var distFS embed.FS
  //
  //   // Dist is the embedded build output, rooted at the directory index.html sits in.
  //   var Dist fs.FS
  //
  //   // Handler serves the SPA from fsys. next is the admin mux: every request this handler does not
  //   // answer is passed to it UNCHANGED. fsys is a parameter, not the embedded FS, so a test can
  //   // drive both the built and the not-built states — see the decisions log.
  //   func Handler(fsys fs.FS, next http.Handler, cfg *config.Config, log *slog.Logger) http.Handler
  //
  //   // Built reports whether fsys holds a real UI (index.html exists).
  //   func Built(fsys fs.FS) bool
  //
  // THE RULE, and it is the whole contract. Handler answers a request only when ALL of:
  //   - r.Method is GET or HEAD, and
  //   - r.URL.Path is not "/healthz", not "/readyz", is not exactly "/api", and does not start with "/api/"
  // Everything else goes to next.ServeHTTP unchanged, so the admin plane's 405s, its JSON 404s and its
  // CSRF rejections stay exactly what they are today. Measured on the real ServeMux: registering the UI
  // as a pattern instead turns PUT /api/workspaces from "405 Allow: GET, HEAD, POST" into a 404 and
  // answers POST /healthz with HTML and a 200. The API prefix list is ONE constant next to the check.
  //
  // What the paths it does NOT answer keep doing, unchanged and untouched by this slice: an /api/
  // path no pattern matches falls to net/http's own plain-text "404 page not found", and a method
  // mismatch on a real route is a 405 with an Allow header. Do not add a JSON catch-all to "fix"
  // either — measured, that is exactly what steals the 405s.
  //
  // What it answers, when it does answer:
  //   - an existing file under fsys -> that file, Content-Type from its extension
  //   - /assets/* (Vite's content-hashed output) -> Cache-Control: public, max-age=31536000, immutable
  //   - anything else -> index.html with Cache-Control: no-cache and status 200 (client routing:
  //     /w/7 is a real address a person can reload)
  //   - Built(fsys) == false -> 503 text/plain, one line naming \`make ui\`
  //   - Content-Security-Policy on every response it serves:
  //       default-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none';
  //       object-src 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline';
  //       connect-src 'self' http://*.<cfg.BaseDomain>:* https://*.<cfg.BaseDomain>:*
  //     (omit both host sources when cfg.BaseDomain is empty — legal in path mode — rather than
  //     emitting the meaningless "http://*.:*")
  //     script-src is spelled out even though default-src covers it, so the tests that assert on it
  //     have a subject. connect-src is built from cfg.BaseDomain — the panel's cross-origin fetch goes
  //     to a workspace host under that domain and nowhere else — and the ":*" is load-bearing: a CSP
  //     host-source without a port matches only the scheme's default port, and this runs on 8080.
  //
  // MOUNTED by internal/admin/server.go, unconditionally, inside the existing httpx.Chain:
  //   return httpx.Chain(webui.Handler(webui.Dist, mux, s.cfg, s.log), s.securityHeaders, s.rateLimitLogin, s.attachSession, s.enforceCSRF)
  // No New() parameter, no setter, no change to cmd/mocker/main.go, and no nil to forget.`

const SIG_BUILD = `  // THE BUILD CONTRACT. Written once, here, because three agents depend on it and only the first
  // one writes it.
  //
  // web/package.json scripts, exact names — the Makefile, the Dockerfile and the acceptance script
  // all call these and none of them may invent a different name:
  //   "dev"       vite
  //   "build"     tsc --noEmit && vite build      (type errors fail the build, they do not warn)
  //   "typecheck" tsc --noEmit
  //   "test"      vitest run
  //   "e2e"       playwright test
  //
  // web/vite.config.ts, the settings that are not defaults and are load-bearing:
  //   build.outDir            = '../internal/webui/dist'   // go:embed cannot reach outside its package
  //   build.emptyOutDir       = true                       // deletes .gitkeep; the Makefile re-creates it
  //   build.modulePreload     = { polyfill: false }        // nothing inline for the CSP to refuse
  //   build.assetsInlineLimit = 0                          // no data: URI assets, same reason
  //   server.proxy for '/api', '/healthz', '/readyz' -> http://127.0.0.1:8080, rewriting BOTH headers:
  //     configure(proxy) { proxy.on('proxyReq', (p) => { p.setHeader('host', ADMIN_HOST); p.setHeader('origin', ADMIN_ORIGIN) }) }
  //     ADMIN_HOST comes from VITE_ADMIN_HOST (default mocker.local); ADMIN_ORIGIN is 'http://' + it.
  //
  // Makefile targets (the build-plumbing agent owns all of them):
  //   ui       — npm ci --prefix web && npm run --prefix web build, under $(CAP), then re-create
  //              internal/webui/dist/.gitkeep so a later fresh clone still compiles
  //   ui-dev   — npm run --prefix web dev
  //   e2e      — ./scripts/e2e.sh          (the script itself is written later, by the Accept section)
  //   test/lint/fmt change to the SCOPED Go bar: ./cmd/... ./internal/... for build/vet/test, and
  //   "gofmt -l ./cmd ./internal" for lint ("gofmt -w ./cmd ./internal" for fmt). Never "gofmt -l ."
  //   (it walks web/node_modules) and never a git ls-files file list (blind to untracked new files).
  //   build, test, lint and smoke keep working with NO node installed. Hard requirement, not a nicety.
  //
  // Dockerfile: a node:24-alpine stage with WORKDIR /src, COPY web/ /src/web/, npm ci and npm run build
  // run in /src/web (so Vite's ../internal/webui/dist resolves to /src/internal/webui/dist), then the
  // Go stage COPYs --from that stage into /src/internal/webui/dist BEFORE go build.
  // NODE_OPTIONS=--max-old-space-size=1024 on the node stage: this box has 7.8 GB with swap half used.
  // .dockerignore must let web/ into the context, and must EXCLUDE web/node_modules AND
  // /internal/webui/dist/ — the exclusion is the guard: with no dist in the context, "//go:embed
  // all:dist" fails the image build outright unless the node stage's COPY actually landed.`

const SIG_UI = `  // THE TYPESCRIPT SEAM. web/src/api/* is written by ONE agent; every screen imports from it and
  // nothing else talks to the network. Pasted verbatim into the writer AND into every caller.
  //
  //   // web/src/api/types.ts   — mirrors SIG_API field for field
  //   export type UserView = { id: number; name: string; role: string; createdAt: number }
  //   export type ServerConfig = { reservedPrefix: string; baseDomain: string; routing: 'host' | 'path' }
  //   export type AuthResponse = { user: UserView; csrfToken: string; config: ServerConfig }
  //   export type WorkspaceView = { id: number; slug: string; name: string; specId: number | null;
  //     ownerId: number | null; forkedFrom: number | null; scenarioId: number | null; revision: number;
  //     settings: unknown; url: string; createdAt: number; updatedAt: number }
  //   export type TrafficSummary = { rows: unknown[]; rate1m: number; dropped: number }
  //   export type ApiError = { code: string; message: string; details?: unknown }
  //
  //   // web/src/api/client.ts
  //   export class ApiFailure extends Error { readonly status: number; readonly code: string }
  //   export function setCsrfToken(token: string | null): void
  //   export function apiGet<T>(path: string): Promise<T>
  //   export function apiSend<T>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, body?: unknown): Promise<T>
  //     — Content-Type: application/json on every send, X-CSRF-Token from the last setCsrfToken,
  //       credentials: 'same-origin', and ApiFailure carrying the server's error.code and the status.
  //       A 401 is thrown like any other failure; the session layer decides what a 401 means.
  //
  //   // web/src/api/queries.ts — the TanStack Query layer, the ONLY place a query key is spelled
  //   export const qk = { me: ['me'] as const, workspaces: ['workspaces'] as const,
  //                       workspace: (id: number) => ['workspace', id] as const,
  //                       traffic: (id: number) => ['traffic', id] as const }
  //   export function useMe(): UseQueryResult<AuthResponse>
  //   export function useWorkspaces(): UseQueryResult<WorkspaceView[]>
  //   export function useWorkspace(id: number): UseQueryResult<WorkspaceView>
  //   export function useTrafficSummary(id: number, enabled: boolean): UseQueryResult<TrafficSummary>
  //   export function useCreateWorkspace(): UseMutationResult<WorkspaceView, Error, { name: string }>
  //   export function useDeleteWorkspace(): UseMutationResult<void, Error, number>
  //   export function useLogin(): UseMutationResult<AuthResponse, Error, { name: string; password: string }>
  //   export function useLogout(): UseMutationResult<void, Error, void>
  //
  //   // web/src/session.tsx — one provider, one hook, no other consumer of AuthResponse
  //   export function SessionProvider(props: { children: React.ReactNode }): JSX.Element
  //   export function useSession(): { state: 'loading' | 'anonymous' | 'authenticated';
  //                                   user: UserView | null; config: ServerConfig | null }`

// ---------------------------------------------------------------------------
// Shared context blocks.
// ---------------------------------------------------------------------------

const CTX_CORE = `You are building phase P1d (SLICE 1) of "mocker", a self-hosted mock-backend service written in Go.
A team points its frontend at a per-person mock of the corporate OpenAPI instead of a shared staging
backend. The repo root is your CWD; the module is git.sumka.site/yakov/mocker.

WHERE THE PROJECT STANDS. P0 through P1c-2 are committed, reviewed and green: spec import, the
generator with its layered seed, the router, per-operation overrides, recipes (jwt/now/identity/copy),
the auth preset, when[] conditions, a RAM live-state layer, traffic recording with redaction, custom
endpoints, and the entire admin JSON API those screens will need. HEAD is 727eec2. There is NO user
interface of any kind: no web/, no go:embed of any UI, no node stage in the Dockerfile. This slice is
the first one.

READING THE DOCS. DESIGN.md is Russian and 87 KB — NEVER read it whole. The sections that matter here:
§14 (Admin API and the ten screens) lines 821-916, §15 (auth and the threat model) lines 917-982,
§16 (deployment) lines 983-1062, §19 (phases) lines 1106-1128. Read a RANGE with sed -n 'A,Bp'.
README.md and HANDOFF.md are both Russian and both safe to read whole.

HOUSE STYLE, and it is enforced by the review gates.
- Comments explain WHY, never WHAT. A comment that restates the code is noise; a comment that records
  the alternative you rejected and the reason is the point. Read three neighbouring files before you
  write one — this codebase has a voice, and matching it is part of the task.
- No dead knobs, no TODO markers, no placeholder implementations. If something is deferred, it is
  named in your report's todo list, not left as a stub that looks finished.
- Errors carry context. In Go: fmt.Errorf with %w and a sentence a human can act on.
- Tests assert behaviour, not implementation. NO pre-existing *_test.go file may be modified in this
  run — there is no authorised exception. Adding new test files is expected.

YOUR REPORT IS JSON, AND AN UNPARSEABLE ONE KILLS YOU. This is not hypothetical: on the first attempt
at this very run an agent finished its work correctly, emitted a 6.5 KB report that failed to parse
five times, and was recorded as dead — its files survived on disk, its contracts did not, and every
later agent lost the seam it was supposed to publish. So: keep every string field SHORT and plain.
Paste the DECISIVE lines of a command's output (the last few, the failing ones, the counts), never a
whole log; strip escape sequences and box-drawing characters; keep "verified" under ~2000 characters.
A precise summary of what a command printed is worth more than the log itself.`

const CTX_UI = `
THE FRONTEND, AND WHAT IT IS FOR (DESIGN §14). The interface is Russian and its audience is a frontend
developer who does not know OpenAPI. §14 is explicit that the words «рецепт», «JSON Patch» and «матчер»
never appear in it. Copy is short, concrete, and says what happened rather than what failed abstractly.

TypeScript rules for this repo:
- strict mode, noUncheckedIndexedAccess, no "any" — ever. An unknown server shape is "unknown" and is
  narrowed at the boundary, not cast.
- No default exports. Named exports only, so a rename is a compiler error rather than a silent miss.
- Every interactive element carries data-testid. The browser test selects on testids, never on Russian
  copy: copy gets reworded in the next slice and a test that breaks on a wording change is noise.
- Components are functions; state is TanStack Query for anything the server owns and useState for the
  rest. There is no global store in this slice and adding one is out of scope.
- Tailwind utility classes in the markup; no CSS-in-JS, no component library, no webfont. Nothing may
  be downloaded at runtime — the contour this ships into may have no internet at all.

Accessibility floor, not a full audit: every input has a <label>, the submit button is a real
<button type="submit"> inside a <form>, focus is visible, and an error is announced with role="alert".`

const CTX_GO = `
THE GO SIDE OF THIS SLICE. Two packages, and no others:
- internal/webui — NEW, a leaf. The embedded dist and the handler that serves it.
- internal/admin — the mount, plus the auth response's new config object.
internal/server, cmd/mocker and every other package are OUT of this run's scope: path mode is cut
(see the decisions log) and the mount needs no wiring in main.

Rules this project already enforces and that no agent may relax:
- A new exported symbol needs a doc comment that says why it exists, not what it does.
- internal/webui may import the stdlib and internal/config. Not internal/admin, not internal/auth,
  not internal/store — a leaf that grows a dependency on the plane it serves is how a static handler
  ends up holding a session.

THE GO BAR, and it is SCOPED. Run exactly this:
${GO_BAR}`

const CTX_TEST = `
TESTING BAR. Every Go change ships with tests in the same run, table-driven where there is more than
one case, named for the behaviour they pin (TestHandler_UnknownAPIPath_StaysPlain404, not TestHandler2).
Every TypeScript change ships with a vitest test for the logic that is not markup — the client's
header handling, the error narrowing, the probe's interpretation of a response. Markup is proven by
the browser test, not by snapshotting the DOM.

Do NOT write a test that passes whether or not the feature exists. The two shapes this project has
been bitten by, both recorded in HANDOFF: an assertion whose before and after look identical (assert
what CHANGED — the status, the key, the origin), and a guard that hashes something upstream of
everything the phase edits (ask which line of your own diff could make this test fail; if the answer
is none, the test is decoration).`

const INTENDED = `
WHAT IS DELIBERATELY NOT HERE (so you do not report a gap as a defect):
- The «Спеки» screen, the endpoint editor with its status tabs and pinned bodies, the custom-endpoint
  screen and the traffic feed with its two conversions — P1d-2/P1d-3. Their APIs already exist.
- Monaco and any JSON editing at all — it arrives with the editor that needs it (P2 per DESIGN §19).
- Path mode's UI: internal/server keeps answering its diagnostic 404 for "/" under MOCKER_ROUTING=path.
  Cut on purpose so that no pre-existing test needs editing; it is the first thing P1d-2 does.
- The SERVER side of «Проверить» (mocker dialling the workspace's external name itself). This tree has
  no outbound HTTP client at all; the first one is an SSRF surface and gets its own slice.
- MOCKER_DEFAULT_SPEC auto-creating a first workspace — the knob is parsed and read by nothing, which
  is recorded, not overlooked.
- Scenarios, checkpoints, resources, bundles, WS/SSE, i18n, dark mode, a component library.
- HANDOFF.md is written by hand after the run, not by any agent in it.
- The P1b MaxBytes soft-ceiling debt (HANDOFF.md carries the measured numbers).`

// ---------------------------------------------------------------------------
// Report schemas. Every required field is one more thing an agent must produce
// or die retrying, so "required" is what the run genuinely cannot proceed
// without; everything else is optional and tested for absence.
// ---------------------------------------------------------------------------

const SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['files', 'summary', 'verified', 'contracts'],
  properties: {
    files: { type: 'array', maxItems: 60, items: { type: 'string' }, description: 'repo-relative paths you created or modified' },
    summary: { type: 'string', description: 'what you implemented, 3-8 sentences' },
    verified: { type: 'string', description: 'the exact commands you ran and the DECISIVE lines of their real output — the scoped Go bar, and/or npm typecheck/test/build. Keep it under ~2000 characters of plain text: paste the last lines and the counts, never a whole log, and strip escape sequences. This field is JSON, and a report that fails to parse five times kills your agent' },
    contracts: {
      type: 'array', maxItems: 40, items: { type: 'string' },
      description: 'every signature another agent must call — exported OR unexported, Go OR TypeScript: functions, types, hooks, component props, script names, data-testids, file paths another agent must write to. One per line, as written in the code.',
    },
    deviations: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'anything done differently from the task or DESIGN, with the reason' },
    todo: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'deliberately left for a later phase' },
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
    output: { type: 'string', description: 'the real output of any command this vector was told to run and report — the decisive lines only, under ~2000 characters of plain text with escape sequences stripped. A report that fails to parse five times kills your agent' },
    skips: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'every test the suite reported as SKIP, with the reason it printed' },
  },
}

const ACCEPT_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['files', 'summary', 'verified', 'passed'],
  properties: {
    files: { type: 'array', maxItems: 40, items: { type: 'string' } },
    summary: { type: 'string' },
    verified: { type: 'string', description: 'the REAL output of the commands you ran — the decisive lines, pasted rather than described, but under ~3000 characters of plain text: the browser run\'s summary and any failure, not the whole transcript. Strip escape sequences and progress bars. This field is JSON, and a report that fails to parse five times kills your agent' },
    passed: { type: 'boolean', description: 'true only if every acceptance assertion this agent was given actually ran and passed in front of it. A missing prerequisite (no docker daemon, no browser binary) goes in deviations and makes this FALSE — a pass you did not observe is not a pass' },
    browser: {
      type: 'string',
      enum: ['passed', 'failed', 'skipped-no-docker', 'skipped-no-browser', 'not-applicable'],
      description: '"not-applicable" for an agent that does not drive a browser. A missing docker daemon is skipped-no-docker, NOT failed — the two mean opposite things to every reviewer downstream',
    },
    smoke: { type: 'string', enum: ['passed', 'failed', 'skipped-no-docker', 'not-applicable'] },
    measurements: {
      type: 'array', maxItems: 20, items: { type: 'string' },
      description: 'the real numbers: the workspace URL the panel showed, the health body the probe read, the rate the indicator reached after a request to a NON-reserved path (the reserved prefix is never recorded in traffic), dist file count and total size, the image build time, every SKIP printed',
    },
    contracts: { type: 'array', maxItems: 20, items: { type: 'string' } },
    goldenIntact: { type: 'boolean', description: 'git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json came back clean (true) or dirty (false)' },
    testsUnmodified: { type: 'boolean', description: 'this command comes back EMPTY: git diff HEAD --no-renames --diff-filter=MD -- $(git ls-tree -r --name-only HEAD | grep "_test\\.go$"). It lists the test files that existed at HEAD and shows only Modifications and Deletions, so newly ADDED test files — which are expected — cannot make it false. There is no authorised exception: any output at all means a pre-existing test was touched' },
    skips: { type: 'array', maxItems: 20, items: { type: 'string' } },
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
      description: 'true only if the scoped Go bar (gofmt -l ./cmd ./internal, plus build, vet and test -race over ./cmd/... ./internal/...) and the UI bar (npm run typecheck, npm run test, npm run build) are ALL clean. An e2e run skipped for lack of docker does NOT make this false; an e2e run FAILING does.',
    },
    output: { type: 'string', description: 'the real TAIL of the failing command, or the passing summaries — under ~2000 characters of plain text, escape sequences stripped. A report that fails to parse five times kills your agent' },
    browser: { type: 'string', enum: ['passed', 'failed', 'skipped-no-docker', 'skipped-no-browser'] },
    criterion: {
      type: 'string',
      enum: ['observed-passing', 'observed-failing', 'not-observed'],
      description: 'did YOU observe the criterion pass in the tree as it stands NOW — a real browser, through the image, logging in, creating a workspace, and the panel rendering the workspace and revision it read cross-origin. "not-observed" if you did not run it; never infer it from a green build',
    },
    goldenIntact: { type: 'boolean', description: 'git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json came back clean' },
    testsUnmodified: { type: 'boolean', description: 'this command comes back empty: git diff HEAD --no-renames --diff-filter=MD -- $(git ls-tree -r --name-only HEAD | grep "_test\\.go$") — no pre-existing test file modified or deleted anywhere in the run. New test files do not appear in it and are expected' },
    skips: { type: 'array', maxItems: 20, items: { type: 'string' } },
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

// ---------------------------------------------------------------------------
// Harness. agentSafe exists because agent() does not merely return null on a
// dead subagent: five invalid structured reports make the call THROW, and an
// unhandled throw at a SERIAL step aborts the whole run — losing the
// orchestration of every finished phase while its work sits on disk. That
// happened on the previous phase. Every serial await goes through this; calls
// inside parallel() are already null-on-throw by the harness.
// ---------------------------------------------------------------------------

const agentSafe = async (prompt, opts) => {
  try {
    return await agent(prompt, opts)
  } catch (err) {
    log(`AGENT DIED: ${opts && opts.label} threw (${String((err && err.message) || err).slice(0, 200)}). Treating it as a dead agent — its work may still be on disk, and every downstream step is told to read the tree rather than its report.`)
    return null
  }
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

// The diff command every reviewer runs. The ";" and the "." are both
// load-bearing: with "&&" and a path list, one path nobody happened to create
// makes "git add -N" exit 128 having added nothing, and the reviewer then reads
// an EMPTY diff and approves it as "the section produced nothing".
const gateCtx = (paths) => `Repo root is your CWD. READ-ONLY: do not modify any file. Running tests is expected.
TO SEE THE SECTION'S DIFF — most of this run's output is NEW files and plain "git diff HEAD" does not
show untracked files at all:
    git add -N . ; git diff HEAD -- ${paths}
Intent-to-add stages nothing for commit. Your co-reviewer runs the same command concurrently: on
".git/index.lock: File exists", wait a second and retry.
NEVER widen that path list to "." — web/node_modules holds tens of thousands of files, and
.claude/workflows (TRACKED — .wf/ is the ignored one) holds ~750 KB of workflow script, including this
run's own. One thing is already there BEFORE this run starts and is NOT a finding, whoever reports it:
"?? .claude/workflows/mocker-p1d1.js" — this very script, untracked on purpose until the phase is
committed. Ignore it wherever it shows up; anything ELSE in git status is this run's doing.
One more exclusion for your own sake: web/package-lock.json is a NEW file of ~3,300 lines that renders
in full in any diff of "web". Unless your vector is supply chain, exclude it —
    git add -N . ; git diff HEAD -- ${paths} ':(exclude)web/package-lock.json'
— and inspect it, if you must, with jq '.packages | keys | length' instead of reading it.
HEAD is 727eec2 — P1c slice 2 plus a one-line HANDOFF correction, committed, reviewed and green — so
this diff is exactly what this run has produced so far.
DESIGN.md is Russian and 87 KB; never read it whole. The index: §6 136-174, §8 246-296, §14 821-916,
§15 917-982, §16 983-1062, §19 1106-1128.
Report ONLY defects you can point at with a file and a line. No praise, no style nits that do not
change behaviour. An empty findings array is the correct answer for a clean vector.`

// A gate: two reviewers on one section, then a single fix agent if either found
// a blocker, then a bounded one-round re-review of only the vectors that raised
// one. Majors are NOT fixed here — they are returned in `unresolved` so the
// final round sees them. That is a behaviour, not a comment: an earlier draft
// said majors ride to the end and then dropped them on the floor.
const runGate = async (name, vectors, fixCtx) => {
  const results = await parallel(vectors.map((v) => () => agent(v.prompt, { label: v.label, phase: name, model: v.model || 'sonnet', schema: REVIEW_SCHEMA })))
  const findings = results.filter(Boolean).flatMap((r) => r.findings || [])
  for (const [i, r] of results.entries()) {
    if ((r?.findings || []).length >= 40) log(`WARNING: ${vectors[i].label} returned the schema maximum of 40 findings — its list is probably TRUNCATED, so this section is not fully reported.`)
  }
  const blockers = findings.filter((f) => f.severity === 'blocker')
  const majors = findings.filter((f) => f.severity === 'major')
  const dead = vectors.filter((_, i) => !results[i]).map((v) => v.label)
  if (dead.length) log(`WARNING: ${name} reviewer(s) returned nothing: ${dead.join(', ')}. That vector did NOT run — treat the section as unreviewed along it.`)
  log(`${name}: ${findings.length} findings, ${blockers.length} blockers`)
  for (const f of findings.filter((x) => x.severity !== 'blocker')) log(`${name} ${f.severity}: ${f.file}:${f.line} — ${f.summary}`)

  let fix = null
  if (blockers.length) {
    const list = blockers.map((f, i) => `${i + 1}. ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`).join('\n')
    fix = await agentSafe(`${fixCtx}

${name} found ${blockers.length} BLOCKER finding(s) in the section just written. Fix them, changing as
little as possible — every edit must map to a numbered finding. You own every file the findings name.

${list}

RULES
- Verify each finding against the code FIRST. Reviewers false-positive. If one is wrong, do NOT "fix"
  it: record it in "deviations" with the evidence that it is wrong.
- Add or extend a test for every real defect you fix, so it cannot come back silently.
- Never weaken, skip or delete a test to reach green. Never touch
  internal/gen/testdata/p1b_body_hashes.json, and never modify a PRE-EXISTING *_test.go file — there
  is no authorised exception in this run.
- Never run "npm install"; the lockfile is fixed for this run.
- Finish with the bars for what you touched all clean and paste the REAL output into "verified".
  Go, if you touched Go:
${GO_BAR}
  TypeScript, if you touched anything under web/ — all four lines, the last two as a pair:
${UI_BAR}`,
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
NARROW YOUR READING: the fix agent reports having touched ${fixScope || '(nothing — it returned no file list)'}.
Treat that as a HINT, never as the scope. Diff the section's whole path list, because a fix agent's
self-reported list can be incomplete and an edit outside it would then be invisible forever:
    git add -N . ; git diff HEAD -- ${P1D1_PATHS} ':(exclude)web/package-lock.json'
The "git add -N ." is not redundant: the fix was told to ADD tests, and a file created after this
round's first diff was never intent-added, so without it a new test file reads as absent and you would
report a fixed finding as still open. An empty diff is NEVER evidence that the fix holds — it means
the fix agent wrote nothing.
Two things only, and keep them separate:
1. For each blocker you raised, is it ACTUALLY fixed — semantically, not just compiling? A fix that
   silences the symptom (an assertion deleted, a check moved after the thing it guarded, a header set
   on a path nothing takes) is NOT fixed; report it again.
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
  // findings: [] and blockers: [] — structurally identical to a clean gate.
  // The majors of the FIRST pass ride out in unresolved too (deduplicated
  // against anything the re-review already carried), because nothing else in
  // the run would ever look at them again.
  const seen = new Set(unresolved.map((f) => `${f.file}:${f.line}:${f.summary}`))
  const carriedMajors = majors.filter((f) => !seen.has(`${f.file}:${f.line}:${f.summary}`))
  if (carriedMajors.length) log(`${name}: ${carriedMajors.length} major(s) carried to the final round rather than fixed here.`)
  return { findings, fix, unresolved: unresolved.concat(carriedMajors), dead }
}

// --------------------------------------------------------------- Foundation --
// Three agents, strictly serial, and the order is forced by real dependencies:
// the scaffold must have BUILT once before the Go handler can be written against
// a real dist, and the build plumbing comes last because it wraps a Dockerfile
// stage around a build whose script names and output path are the scaffold's to
// decide. Nothing here is parallelisable — every pair shares a file or an
// artefact, which is the recorded deadlock shape ("two agents writing different
// files of the same module in parallel, referencing each other's symbols").
//
// Model: sonnet for all three. None clears the three-prong opus test — every
// risky property (the mount's method/prefix rule, the embed pattern, the
// Dockerfile stage order) is checked by Gate 1 immediately after, which is
// prong 2's definition of cheaply caught.
phase('Foundation')

const scaffold = await agentSafe(`${CTX_CORE}${CTX_UI}${CTX_TEST}${INTENDED}

IF THIS WORK IS ALREADY THERE, VERIFY IT INSTEAD OF REDOING IT. A previous attempt may have completed
every file and then failed to report (that is exactly how this run started). Look first: if web/
already holds package.json, package-lock.json, the configs and src/, and internal/webui/dist holds a
built index.html plus hashed assets, then your job is to CHECK every requirement below against what is
there, fix only what is actually wrong, and report the contracts. Do not rewrite correct files, and do
not re-run "npm install" unless web/node_modules is missing — a second install can move the lockfile
for no reason.

YOUR TASK — create the frontend toolchain, and nothing else. You are the FIRST agent of this run and
the ONLY one that may create web/package.json or web/package-lock.json. No later agent runs
"npm install <pkg>": a second install rewrites the lockfile and the image's "npm ci" then fails on a
mismatch that has nothing to do with the change that caused it. So think now about what the whole
slice needs — three screens, a vitest suite AND a Playwright browser test — and pin all of it here.

FILES YOU OWN (create them; touch nothing else in the repo):
  web/package.json, web/package-lock.json, web/tsconfig.json, web/tsconfig.node.json,
  web/vite.config.ts, web/vitest.config.ts (or the test block inside vite.config.ts — your call, but
  SIG_BUILD's script names are fixed), web/index.html, web/src/main.tsx, web/src/App.tsx (a
  placeholder shell — the next section replaces its contents), web/src/index.css, web/src/vite-env.d.ts
Your BUILD writes internal/webui/dist/** — that is generated output and it is expected; you simply do
not create or hand-edit any file under internal/ yourself (the .gitkeep placeholder in that directory
belongs to the next agent).
You may NOT touch: Makefile, Dockerfile, .dockerignore, .gitignore, README.md, and anything else under
internal/ or cmd/. Other agents own those and a collision here costs a gate round.

${SIG_BUILD}

WHAT TO PIN. Resolve versions AT RUN TIME (npm view <pkg> version) and pin every one EXACTLY — no ^,
no ~. The set: react, react-dom, @tanstack/react-query, and as devDependencies vite,
@vitejs/plugin-react, typescript, tailwindcss, @tailwindcss/vite, vitest, jsdom,
@testing-library/react, @testing-library/jest-dom, @types/react, @types/react-dom.
ONE EXCEPTION, pinned by number: "@playwright/test": "1.62.1". That release owns the chromium build
already sitting in ~/.cache/ms-playwright on this box, so the browser acceptance never waits on a
download from cdn.playwright.dev (which npm's registry reachability says nothing about).
Tailwind 4 is configured through its Vite plugin and a single @import "tailwindcss" in index.css —
no tailwind.config.js and no postcss.config.js unless the version you actually resolve needs them.

SCOPE VITEST TO src/**. A later section adds Playwright specs under web/e2e/, and vitest's default
include (**/*.{test,spec}.*) would collect them, import @playwright/test and die. Set
test.include to 'src/**/*.{test,spec}.{ts,tsx}' and say why in a comment.

THE ONE VERSION RISK, AND WHAT TO DO ABOUT IT. The current majors are new (TypeScript and Vite both
shipped a major recently). Try current first; if "npm run build" or "npm run typecheck" fails for a
reason that is clearly a toolchain incompatibility rather than your code, step DOWN one major on the
offending package, pin that, and record both the failure and the choice in "deviations". Do not spend
the run fighting a toolchain: a working pinned older major beats a broken newest.

VITE CONFIG — the settings that are not defaults, each with its reason, written into the file as a
comment (SIG_BUILD above gives the exact values):
  build.outDir            — go:embed cannot reach outside its own package directory.
  build.emptyOutDir       — a stale asset must not ship; the Makefile re-creates the .gitkeep this
                            deletes (not your file).
  build.modulePreload / build.assetsInlineLimit — the app is served under a CSP whose script-src is
                            'self' and whose default-src is 'self'. Anything Vite would inline (a
                            preload polyfill, a small asset as a data: URI) is a thing that policy can
                            refuse, and the failure mode is a blank page with only a console error.
  server.proxy            — BOTH the Host and the Origin request headers are rewritten, with the
                            proxy.on('proxyReq') mechanism SIG_BUILD spells out; Vite has no option
                            for the Origin one. The Go side checks them in two different places: the
                            dispatcher routes on Host (an un-rewritten 127.0.0.1:8080 gets the
                            unknown-host 404 before the admin plane is reached) and the CSRF
                            middleware compares Origin's hostname to MOCKER_ADMIN_HOST (an
                            un-rewritten http://localhost:5173 is a 403 on every POST).

THE PLACEHOLDER APP. web/src/App.tsx renders a single centred "mocker" heading and nothing else. It
exists so the build has something to compile and so Gate 1 can see real bytes. Do not write screens,
routing, API calls or a query client — those are files another agent owns.

PROVE IT, and paste the real output into "verified". Run every line FROM THE REPO ROOT — a "cd web"
persists across your shell calls and then every path below resolves one level too deep:
  npm install --prefix web        (this is what creates the lockfile)
  npm run --prefix web typecheck
  npm run --prefix web build
  ls -la internal/webui/dist internal/webui/dist/assets
  grep -o '<script[^>]*>' internal/webui/dist/index.html
The last one is a real check: every script tag in the built index.html must carry a src= attribute.
An inline script means something will be blocked by the CSP in production while every local test passes.

REPORT "contracts" — the package.json script names, the outDir path, the dist layout (names are
content-hashed, so give the SHAPE: index.html plus assets/<name>-<hash>.js|.css), the resolved version
of every pinned dependency, and the env var name your proxy reads.`,
  { label: 'ui:scaffold', phase: 'Foundation', model: 'sonnet', schema: SCHEMA })

const scaffoldContracts = contractsOf([scaffold].filter(Boolean))
if (!scaffold) log('ui:scaffold returned nothing. Its files may still be on disk — every later agent is told to read the tree rather than this report, and Gate 1 sees whatever actually exists.')
else logDeviations('ui:scaffold', [scaffold])

// The FIRST stop condition, and it sits here rather than at the end of the
// section because everything after it is written against a built app: if the
// scaffold's files are not on disk, the next two agents would work in a tree
// where nothing compiles and the run would still return a normal-looking
// object. It is deliberately cheap and asks four yes/no questions.
const kernelScaffold = await agentSafe(`Repo root is your CWD. READ-ONLY: change nothing, create nothing.

Did the frontend toolchain actually land? Run exactly these and report what they printed, briefly:
  ls -la web/package.json web/vite.config.ts web/src/main.tsx internal/webui/dist/index.html
  ls web/node_modules | head -3
  npm run --prefix web typecheck

Report ONE finding, and only one, if any of these is true — file "kernel", line 1, severity "blocker",
with "failure" naming which command failed and "fix" saying "re-run the scaffold agent" (the schema
requires both fields, and an invalid report five times over kills this check and with it the run's
first stop condition): web/package.json is missing, web/vite.config.ts is missing,
internal/webui/dist/index.html is missing, web/node_modules is absent, or the typecheck fails.
If all of them are fine, return an EMPTY findings array — that is the correct answer and the run
continues. Keep "output" under 1000 characters.`,
  { label: 'kernel:scaffold', phase: 'Foundation', model: 'haiku', schema: REVIEW_SCHEMA })

if (kernelScaffold && (kernelScaffold.findings || []).some((f) => f.severity === 'blocker')) {
  for (const f of kernelScaffold.findings) log(`KERNEL (scaffold): ${f.summary} — ${f.failure}`)
  log('STOPPING: the frontend toolchain is not on disk. Every agent after this one is written against a built app, and would work in a tree where nothing compiles while the run still returned a normal-looking object.')
  return {
    phase: 'P1d-1 — the admin UI ships inside the binary',
    green: false,
    criterion: 'not-observed',
    browser: null,
    goldenIntact: null,
    testsUnmodified: null,
    stoppedAt: 'Foundation (scaffold)',
    why: (kernelScaffold.findings || []).map((f) => f.summary),
    unresolved: kernelScaffold.findings || [],
    deadAgents: scaffold ? [] : ['ui:scaffold'],
    files: (scaffold && scaffold.files) || [],
  }
}
if (!kernelScaffold) log('WARNING: the scaffold kernel check returned nothing — the toolchain is UNVERIFIED and the run continues blind.')

const webuiGo = await agentSafe(`${CTX_CORE}${CTX_GO}${CTX_TEST}${INTENDED}

YOUR TASK — make the built frontend part of the binary, and mount it on the admin plane without
changing a single answer that plane gives today.

The previous agent created web/ and ran a real build, so internal/webui/dist exists and holds
index.html plus assets/. Look at it (ls, not cat — the JS is minified) before you start. It reported:
${scaffoldContracts || '(the scaffold agent reported nothing — read web/package.json, web/vite.config.ts and internal/webui/dist yourself and work from what is actually there)'}

FILES YOU OWN:
  internal/webui/webui.go          (new package)
  internal/webui/webui_test.go     (new)
  internal/webui/dist/.gitkeep     (a NEW file, and it must not be git-ignored — see below)
  internal/admin/server.go         (the mount, and only the mount)
  internal/admin/auth_handlers.go  (the config object, and only that)
  internal/admin/webui_mount_test.go and internal/admin/auth_config_test.go (NEW files — you may not
  modify any pre-existing *_test.go, in this package or anywhere else)
You may NOT touch: internal/server/*, cmd/*, the Makefile, the Dockerfile, .gitignore, README.md,
anything under web/.

${SIG_WEBUI}

${SIG_CONFIG}

THE PLACEHOLDER, AND WHY IT IS TRACKED. "//go:embed dist" over a directory holding only .gitkeep does
NOT compile — verified on this box: "pattern dist: cannot embed directory dist: contains no embeddable
files". "//go:embed all:dist" DOES, because all: includes dotfiles. So create the placeholder file
listed above, use all:, and have Built(fsys) report whether index.html exists. A fresh
clone with no npm run must still build green and must serve one honest 503 naming \`make ui\` — never
a blank 200.

WHY fsys IS A PARAMETER. A test written over the EMBEDDED FS is state-dependent: it would assert the
SPA on a machine where someone ran \`make ui\` and the 503 on a fresh clone, and \`make test\` is a
standing bar that has to be green in both. Handler takes fs.FS; Dist is the embedded default and is
used by exactly one caller, the admin mount. Every unit test drives Handler over fstest.MapFS.

PATH SAFETY. Reject anything that is not fs.ValidPath before it reaches the FS: ../, an encoded %2e%2e,
a leading //, a NUL byte. Test it.

THE MOUNT, and this is the part the pre-flight gate rewrote after measuring it. Do NOT register the UI
as a mux pattern. Measured on the real net/http.ServeMux with this project's route set:
mux.Handle("/", ui) plus mux.Handle("/api/", jsonNotFound) turned PUT /api/workspaces from
"405 Allow: GET, HEAD, POST" into a JSON 404, GET /api/auth/login (a POST-only route) from 405 into a
404, and answered POST /healthz with the SPA and a 200. Probing with mux.Handler(r) fails identically:
it returns an EMPTY pattern for a method mismatch too. So the UI WRAPS the mux, exactly as SIG_WEBUI
states, inside the existing httpx.Chain so the security headers still cover the HTML. Write the reason
next to the call, in this codebase's voice.

THE CONFIG OBJECT. The browser cannot derive the reserved prefix or the base domain: MOCKER_ADMIN_HOST
is forbidden from sitting under MOCKER_BASE_DOMAIN, so window.location tells it nothing. handleLogin
and handleMe both answer it, through ONE constructor helper — two literals drift, and the client
bootstraps on one and logs in through the other.

TESTS THAT MUST EXIST (behaviour, through the admin plane's real Handler() where the mount matters):
  1. GET /nope                  -> 200, index.html's bytes, Cache-Control: no-cache
  2. GET /assets/<file>         -> 200, right Content-Type, Cache-Control contains "immutable"
  3. GET /api/definitely-nope   -> 404 and NOT html. It is net/http's own plain-text "404 page not
     found" today; assert that it is unchanged (the status, and that the body is not the SPA), and do
     NOT add a JSON catch-all — measured, an "/api/" pattern steals the 405s below.
  4. PUT /api/workspaces        -> 405 (registered GET/POST only) — the exact status it returns TODAY
  5. GET /api/auth/login        -> 405 (registered POST-only) — likewise
  6. POST /healthz              -> 405 — likewise
     4 and 6 are STATE-CHANGING methods, so enforceCSRF runs before the mux: without
     "Content-Type: application/json" the answer is 415, and without an Origin whose hostname equals
     cfg.AdminHost it is 403. Measured through the real chain. Give those two requests both headers,
     or you pin the wrong status and a correct mount looks broken. Case 5 is a GET and needs neither.
  7. HEAD /                     -> 200, no body
  8. GET / over an EMPTY fstest.MapFS -> 503 naming make ui
  9. GET /../../etc/passwd and an encoded variant -> never served from outside fsys
 10. every response the UI serves carries the CSP header; it names script-src 'self' explicitly, no
     directive carries 'unsafe-inline' or 'unsafe-eval' except style-src, and connect-src names the
     configured base domain with a wildcard port
 11. GET /api/me and POST /api/auth/login still answer exactly as before, including the new config
     object with the values cfg actually holds
Cases 4-6 are the regression tests for the measured defect above: they pass before your change too,
and their whole job is to fail if someone later "simplifies" the mount into a pattern.

PROVE IT, and paste the real output into "verified":
${GO_BAR}
  rg -n 'webui\\.' internal/admin/server.go
The rg output is the FIRST line of your summary. On the previous phase an agent reported a wiring task
complete with the file untouched, and the reviewer told to grep for the same thing passed it — so this
is a thing you paste, not a thing you claim.`,
  { label: 'go:webui', phase: 'Foundation', model: 'sonnet', schema: SCHEMA })

if (!webuiGo) log('go:webui returned nothing — the mount may or may not exist. Gate 1 diffs the tree, not this report.')
else logDeviations('go:webui', [webuiGo])

const plumbing = await agentSafe(`${CTX_CORE}${CTX_GO}${CTX_TEST}${INTENDED}

YOUR TASK — make the UI part of the image and of the developer's muscle memory. You are the last agent
of this section; the app builds (web/) and the handler exists and is mounted (internal/webui,
internal/admin), both already tested.

What the two previous agents reported:
${contractsOf([scaffold, webuiGo].filter(Boolean)) || '(both reported nothing — read web/package.json, web/vite.config.ts, internal/webui/webui.go and internal/admin/server.go yourself)'}

FILES YOU OWN: Dockerfile, .dockerignore, .gitignore, Makefile, README.md.
You may NOT touch: anything under web/, internal/webui/*, internal/admin/*, internal/server/*, cmd/*,
and no *_test.go anywhere.

${SIG_BUILD}

1. THE IMAGE, and it carries this slice's real guard. Add a node stage that runs the real build and
   copy its output into the Go stage BEFORE "go build". Then make it IMPOSSIBLE for the image to be
   built without that stage: .dockerignore excludes /internal/webui/dist/ entirely, so a dist built on
   the developer's machine cannot ride in on "COPY . ." and quietly satisfy the embed. Verified
   behaviour: with no dist directory in the context, "//go:embed all:dist" fails the build outright
   ("pattern all:dist: no matching files found") instead of shipping a 503 placeholder that every
   green test in this repo would still pass. Keep excluding web/node_modules (a 300 MB context nobody
   needs) and let web/ itself in. NODE_OPTIONS=--max-old-space-size=1024 on the node stage: this box
   has 7.8 GB of RAM with swap already half used and its kernel OOM killer wiped the whole user slice
   twice on 2026-08-18. Use npm ci, not npm install — that is why the lockfile is committed.

2. .gitignore. Today it carries "/web/node_modules/" and "/web/dist/". The built output moved: ignore
   the CONTENTS of internal/webui/dist (the "dist/*" form — a rule on the directory itself cannot be
   undone by a negation for a file inside it, and then the placeholder silently is not tracked at all)
   while keeping .gitkeep tracked, and drop the stale /web/dist/ line.
   Prove it, and paste all three — note --no-index, without which git skips a path that is already in
   the index and answers "not ignored" whatever the rules say:
     test -f internal/webui/dist/.gitkeep ; echo "placeholder present: $?"
     git check-ignore --no-index -q internal/webui/dist/.gitkeep ; echo "check-ignore exit: $? (want 1 = not ignored)"
     git status --porcelain -uall internal/webui/dist
   The -uall is load-bearing: without it git collapses an untracked directory to the single line
   "?? internal/webui/dist/", which looks identical whether the ignore rule works or is missing
   entirely. With it, a working rule shows only the .gitkeep and a broken one shows every built asset.

3. THE MAKEFILE. ui, ui-dev, e2e as SIG_BUILD spells them, plus .PHONY and the one-line help text each
   target carries in this file. \`make ui\` runs under $(CAP) — read that block's comment first; it
   degrades to nothing where systemd-run is absent and that is deliberate — and re-creates
   internal/webui/dist/.gitkeep afterwards, because Vite's emptyOutDir deletes it and the next fresh
   clone would not compile. \`make e2e\` calls ./scripts/e2e.sh, which does not exist yet: the Accept
   section writes it. Add the target anyway and say so in the help text.
   CHANGE test, lint AND fmt TO THE SCOPED GO BAR: web/node_modules now sits inside this Go module,
   the go tool walks it, and one .go file shipped by a transitive npm dependency would fail
   "go build ./...", "go vet ./..." and "gofmt -l ." for everybody, unfixably — while "gofmt -w ."
   would rewrite that dependency's own files. Use ./cmd/... ./internal/... for build/vet/test and
   "gofmt -l ./cmd ./internal" / "gofmt -w ./cmd ./internal" for lint and fmt. Do NOT use a file list
   from "git ls-files": this run commits nothing, so every .go file it creates is untracked and
   invisible to that form — measured. Write the reason into the Makefile as a comment.
   \`make build\`, \`make test\`, \`make lint\` and \`make smoke\` must NOT require node. Hard requirement.

4. README. A short section on the UI in the file's existing Russian voice and table formatting:
   \`make ui\` builds it into the binary, \`make ui-dev\` runs Vite with the proxy (and VITE_ADMIN_HOST,
   and why it rewrites Host AND Origin), \`make e2e\` runs the browser acceptance, one sentence that a
   binary built without \`make ui\` answers 503 and says so, and one sentence that under
   MOCKER_ROUTING=path the UI is not served yet (it arrives in the next slice) — that is a deliberate
   cut, and a README that hides it turns a known gap into a bug report.

PROVE IT, and paste the real output into "verified":
${GO_BAR}
  test -f internal/webui/dist/.gitkeep ; echo "placeholder present: $?"
  git check-ignore --no-index -q internal/webui/dist/.gitkeep ; echo "check-ignore exit: $? (want 1)"
  git status --porcelain -uall internal/webui/dist
  docker compose build            (the whole point: the image must build WITH the UI inside it)
Compose REFUSES to start without a repo-root .env — docker-compose.yml declares it as the service's
env_file and it is gitignored, so it is absent in a fresh tree (scripts/smoke.sh writes one before it
builds, for exactly this reason). If .env is missing, "cp .env.example .env", build, then delete it
again; if the developer already has one, leave it completely alone.
You are the only agent in this section allowed to run docker. If the daemon is unavailable, say so in
"deviations" — never report a build you did not run.`,
  { label: 'build:plumbing', phase: 'Foundation', model: 'sonnet', schema: SCHEMA })

if (!plumbing) log('build:plumbing returned nothing — the Dockerfile, Makefile and .gitignore may be unwritten. Gate 1 checks the tree.')
else logDeviations('build:plumbing', [plumbing])

const foundationOK = [scaffold, webuiGo, plumbing].filter(Boolean)
log(`Foundation: ${foundationOK.length}/3 agents reported, ${foundationOK.flatMap((r) => r.files || []).length} files.`)

// The SECOND stop condition: the Go half. A Foundation agent that dies before
// its files land is not survivable by "read the tree instead": thirteen later agents would
// run npm against an app that does not exist, or work in a tree where
// //go:embed all:dist cannot compile, and the run would still return a
// structurally normal object. This one cheap agent is the difference between a
// failed run and a run that reports success for work nothing produced.
const kernel = await agentSafe(`Repo root is your CWD. READ-ONLY: change nothing, create nothing.

Answer one question: did the Foundation of this run actually land? Run exactly these and report what
they printed:
  ls -la web/package.json web/vite.config.ts internal/webui/webui.go internal/webui/dist/index.html
  go build ./cmd/... ./internal/...
  rg -n 'webui\\.' internal/admin/server.go
  npm run --prefix web typecheck

Report ONE finding, and only one, if any of the following is true — file "kernel", line 1, severity
"blocker", with "failure" naming which command failed and "fix" saying "re-run the Foundation section"
(the schema requires both fields, and an invalid report five times over kills this check entirely,
which would silently disable the run's only stop condition): web/package.json is missing,
internal/webui/webui.go is missing, the Go build fails, or the admin mount (the rg above) prints
nothing. Put the real command output in "output" either way. If all
four are fine, return an EMPTY findings array — that is the correct answer, and the run continues.`,
  { label: 'foundation:kernel', phase: 'Foundation', model: 'haiku', schema: REVIEW_SCHEMA })

if (kernel && (kernel.findings || []).some((f) => f.severity === 'blocker')) {
  for (const f of kernel.findings) log(`KERNEL: ${f.summary} — ${f.failure}`)
  log('STOPPING: the Foundation did not land. Every later section would work against a tree that cannot build, and the run would still return a normal-looking object. Fix the Foundation by hand (or re-run this workflow with resumeFromRunId) before anything else runs.')
  return {
    phase: 'P1d-1 — the admin UI ships inside the binary',
    green: false,
    criterion: 'not-observed',
    browser: null,
    goldenIntact: null,
    testsUnmodified: null,
    stoppedAt: 'Foundation',
    why: (kernel.findings || []).map((f) => f.summary),
    unresolved: kernel.findings || [],
    deadAgents: [
      ...(scaffold ? [] : ['ui:scaffold']),
      ...(webuiGo ? [] : ['go:webui']),
      ...(plumbing ? [] : ['build:plumbing']),
    ],
    files: foundationOK.flatMap((r) => r.files || []),
  }
}
if (!kernel) log('WARNING: the kernel check agent returned nothing — the Foundation is UNVERIFIED and the run continues blind.')

// ------------------------------------------------------------------ Gate 1 --
// Two vectors, because this section's risk sits in exactly two places: whether
// the mount changed an answer the admin plane already gives (invisible to the
// existing suite, since no current test asks for a method a route does not
// have), and whether the build path really produces an image with the UI in it
// (a claim three later sections depend on and none of them can check).
phase('Gate 1')

const gate1Ctx = gateCtx('web internal/webui internal/admin Dockerfile .dockerignore .gitignore Makefile README.md')

const foundationFixCtx = `${CTX_CORE}${CTX_GO}${CTX_TEST}

Repo root is your CWD. You own every file this section produced: web/* (config and scaffold only),
internal/webui/*, the mount in internal/admin/server.go, the config object in
internal/admin/auth_handlers.go, that package's NEW test files, Dockerfile, .dockerignore, .gitignore,
Makefile, README.md. internal/server/, cmd/ and every pre-existing *_test.go are out of bounds.
${SIG_WEBUI}
${SIG_BUILD}`

const gate1 = await runGate('Gate 1', [
  {
    label: 'gate1:semantics',
    model: 'sonnet',
    prompt: `${gate1Ctx}

VECTOR — DID THE MOUNT CHANGE AN ANSWER THE ADMIN PLANE ALREADY GAVE? That is the defect class this
section exists to avoid and the one the existing suite cannot see.

Read internal/webui/webui.go and the mount in internal/admin/server.go, then judge whether the tests
in this section actually PIN the list below. Report a finding when the behaviour is wrong OR when
nothing pins it — an unpinned correct behaviour is one refactor away from being an unpinned wrong one.
  - PUT /api/workspaces is 405 (that path is registered for GET and POST). Not 404, not the SPA.
  - GET /api/auth/login is 405 (POST-only). POST /healthz is 405.
  - GET /api/definitely-nope is 404 and NOT the SPA's html. It is net/http's own plain-text "404 page
    not found" today and this slice does not change it — a client that gets HTML from an API call
    reports "invalid JSON" and nobody finds the cause. Do NOT ask for a JSON body here and do not
    accept a fix that adds an "/api/" catch-all: measured, that pattern steals the 405s in the three
    checks above.
  - GET /nope is index.html with 200 and Cache-Control: no-cache; /assets/* is immutable with the
    right Content-Type.
  - A path escaping the embedded FS (../, %2e%2e, a leading //) cannot read anything outside it.
  - The CSP is on what the UI serves and script-src has no 'unsafe-inline'.
  - The UI handler is INSIDE the existing httpx.Chain, so securityHeaders / rateLimitLogin /
    attachSession / enforceCSRF still wrap every API answer. A UI mounted outside the chain silently
    drops all four.
  - The auth config object comes from ONE helper used by both handleLogin and handleMe, and reports
    what cfg actually holds rather than a literal.
  - internal/webui imports nothing beyond the stdlib and internal/config.
If you probe those statuses yourself, carry the same caveat the writer was given: PUT /api/workspaces
and POST /healthz are state-changing, so enforceCSRF answers 415 without "Content-Type: application/json"
and 403 without an Origin whose hostname is cfg.AdminHost — a probe missing either header reports a
false blocker against a correct mount. You may write a scratch Go test OUTSIDE the repo (in a temp
dir); do not add or modify any file inside the repo.

Run and report the real tail in "output":
  go test ./internal/webui/... ./internal/admin/... -race -count=1
Report EVERY SKIP the suite prints, in "skips".`,
  },
  {
    label: 'gate1:buildpath',
    model: 'sonnet',
    prompt: `${gate1Ctx}

VECTOR — IS THE BUILD PATH REAL, AND DOES IT STAY REAL FOR SOMEONE WITH NO NODE? Three later sections
and the whole acceptance rest on claims made here, and none of them can verify these.

Verify by command and say which line proves each answer:
  1. The scoped Go bar is green right now:
     test -z "$(gofmt -l ./cmd ./internal)" ; go build ./cmd/... ./internal/... ; go vet ./cmd/... ./internal/...
     Then the harder question, answered by READING: would it still be green on a fresh clone where
     internal/webui/dist holds only .gitkeep? The embed pattern must be "all:dist" — plain "dist" does
     not compile against a dotfile-only directory.
  2. The placeholder: "test -f internal/webui/dist/.gitkeep" must succeed (the npm build deletes it on
     every run — if it is gone, some agent built without re-creating it, and a fresh clone would then
     fail to compile), "git check-ignore --no-index -q internal/webui/dist/.gitkeep" must exit 1 —
     --no-index is load-bearing, git skips a path already in the index and answers "not ignored"
     whatever the rules say — and "git status --porcelain -uall internal/webui/dist" must list the
     .gitkeep and NOTHING else. -uall is load-bearing too: without it an untracked directory collapses
     to one line that looks the same whether the ignore rule works or is absent. The ignore rule must be the "dist/*" form: a rule on the directory itself cannot be undone
     by a negation for a file inside it, and the placeholder then cannot be tracked at all.
  3. .dockerignore: web/ must be IN the context, web/node_modules must be out, and /internal/webui/dist/
     must be EXCLUDED. That exclusion is the guard — with no dist in the context, the embed fails the
     image build unless the node stage's COPY landed. If a locally built dist can still reach the
     image, the acceptance can go green with the node stage broken, and that is a blocker.
  4. The Dockerfile: name the exact lines that build the UI and copy it, and confirm the COPY happens
     BEFORE go build and lands on the path internal/webui embeds. A wrong path here ships the 503
     placeholder while every test in the repo stays green.
  5. The Makefile: build, test, lint and smoke must NOT require node — read the targets AND their
     dependencies. test, lint AND fmt must use the SCOPED Go bar: ./cmd/... ./internal/... for
     build/vet/test, "gofmt -l ./cmd ./internal" for lint and "gofmt -w ./cmd ./internal" for fmt.
     Never "gofmt -l ." (it walks web/node_modules) and never a file list from git ls-files (blind to
     this run's untracked new files) — and "gofmt -w ." would rewrite a dependency's shipped .go
     files. \`make ui\` must re-create the .gitkeep after building.
  6. web/vite.config.ts: outDir, emptyOutDir, modulePreload.polyfill === false, assetsInlineLimit === 0,
     and a dev proxy that rewrites BOTH host and origin via proxy.on('proxyReq'). changeOrigin alone
     rewrites Host only and leaves every POST a 403.
  7. package.json: exact pins with no ^ or ~, @playwright/test pinned to 1.62.1 (the release owning the
     chromium already cached on this box), and script names that match what the Makefile and Dockerfile
     actually call. A script renamed in one place is a build that fails only inside the image.
  8. grep the BUILT internal/webui/dist/index.html: every <script> must carry src=. Paste it into
     "output".
Do NOT run "docker compose build" — the previous agent already did and its output is in the section
report; a second concurrent image build on this box is how a phantom failure gets manufactured.`,
  },
], foundationFixCtx)

// -------------------------------------------------------------------- Shell --
// Serial for the same reason as the Foundation: each agent consumes the previous
// one's exported surface. The first owns the ENTIRE network boundary; the other
// two import from it and never call fetch. That is the seam this section is
// built around, so SIG_UI is pasted into all three.
phase('Shell')

const shellCtx = `${CTX_CORE}${CTX_UI}${CTX_TEST}${INTENDED}

WHAT EXISTS ALREADY (this run's Foundation section, reviewed and green): web/ builds with Vite into
internal/webui/dist, which the Go binary embeds and the admin plane serves. The placeholder
web/src/App.tsx renders one heading. Everything below replaces it.

${SIG_API}

${SIG_UI}

BARS FOR EVERY AGENT IN THIS SECTION, run from the repo root and pasted REAL into "verified":
${UI_BAR}
Do not run docker, do not touch Go, and never run "npm install" — the lockfile is fixed for this run.`

const apiClient = await agentSafe(`${shellCtx}

YOUR TASK — write the entire network boundary, once, so that no screen ever calls fetch.

FILES YOU OWN (new): web/src/api/types.ts, web/src/api/client.ts, web/src/api/queries.ts,
web/src/api/client.test.ts, plus the QueryClientProvider line in web/src/main.tsx.
You may NOT touch: App.tsx, any screen, session.tsx, package.json, or anything under internal/ or
web/e2e — except internal/webui/dist/**, which the UI bar's build necessarily rewrites (see it above).

The Go side of the config object, so the two files are written against the same struct rather than
against two readings of one sentence:
${SIG_CONFIG}

types.ts mirrors SIG_API field for field. Where the server sends something this slice does not read
(WorkspaceView.settings, a traffic row's payload), the type is "unknown" — not "any", not a guessed
shape. A field the server always sends is still declared even if unused here: a type that lies about
the wire is worse than one that is incomplete.

client.ts implements exactly what SIG_UI declares, and the four things that are easy to get wrong:
  - Content-Type: application/json on EVERY send. The admin plane answers 415 without it, and that is
    a CSRF defence, not a formality — do not drop it for a body-less DELETE.
  - X-CSRF-Token on every non-GET, from the last setCsrfToken. A GET never carries it.
  - credentials: 'same-origin'. The session is an httpOnly cookie; nothing in TypeScript can read it,
    and code that appears to is code that is wrong.
  - the error path: parse { error: { code, message } }, throw ApiFailure carrying status and code, and
    fall back to a readable message when the body is not JSON at all (a reverse proxy's HTML 502).
A 401 is thrown like any other failure. The client does NOT redirect and does not know what a session
is — a client that navigates on its own makes every failing request look like a routing bug.

queries.ts is the TanStack Query layer and the ONLY file where a query key is spelled.
useTrafficSummary owns its own cadence — refetchInterval of a few seconds, gated by its "enabled"
argument — because the panel that consumes it may not touch this file, and a screen that re-implements
polling in a useEffect is how two timers end up fighting. After a create or a delete, invalidate
qk.workspaces; after login or logout, set/clear the CSRF token and reset
qk.me. Set the client's defaults deliberately and comment why: no refetch-on-window-focus for a tool
that sits open on a second monitor, one retry, and staleTime 0 for anything a person just changed.

TESTS (vitest, fetch stubbed — this is the part a browser test cannot see):
  - a send carries both headers; a get carries neither.
  - setCsrfToken(null) then a send: the header is absent, not the string "null".
  - a 401 with a JSON error body throws ApiFailure with status 401 and the server's code.
  - a 500 with an HTML body still throws ApiFailure with a readable message rather than letting a JSON
    parse error escape to the caller.
  - a 204 resolves instead of throwing on an empty body.

REPORT "contracts" — the exact exported signatures including the query keys, so the next two agents
import instead of guessing.`,
  { label: 'ui:api', phase: 'Shell', model: 'sonnet', schema: SCHEMA })

if (!apiClient) log('ui:api returned nothing — the next agents are told to read web/src/api/ from the tree.')
else logDeviations('ui:api', [apiClient])

const authUI = await agentSafe(`${shellCtx}

WHAT THE NETWORK BOUNDARY LOOKS LIKE (previous agent, this section):
${contractsOf([apiClient].filter(Boolean)) || '(it reported nothing — read web/src/api/*.ts and import what is actually there)'}

YOUR TASK — the app shell: who is logged in, what is on screen, and the «Вход» screen itself
(DESIGN §14 screen 1: password + name; вход = get-or-create by name).

FILES YOU OWN (new, except main.tsx's provider line which the previous agent added):
web/src/App.tsx, web/src/routes.ts, web/src/session.tsx, web/src/screens/Login.tsx,
web/src/ui/*.tsx (the three or four primitives the screens share: Button, Input, Field, Alert),
web/src/screens/Login.test.tsx.
You may NOT touch: web/src/api/*, the not-yet-written screens, package.json, or anything under
internal/ — except internal/webui/dist/**, which the UI bar's build rewrites (see it above).

SESSION. SessionProvider bootstraps with useMe() on mount and exposes exactly three states:
'loading', 'anonymous', 'authenticated'. The distinction matters on screen: 'loading' renders NOTHING
(never the login form — a form that flashes for 200 ms on every reload and then vanishes is the most
irritating bug a tool like this can have), 'anonymous' renders Login, 'authenticated' renders the app.
A 401 from any query moves the app to 'anonymous' and clears the CSRF token; that decision lives HERE,
not in the client.

THE AUTHENTICATED SHELL. Above the routed screen, render a thin header: the signed-in user's name and
a «Выйти» button wired to useLogout, both with data-testids. Nothing else in this run renders a logout
control, and the browser acceptance ends by logging out — a session that cannot be ended from the
interface is also a session a person cannot hand over on a shared box.

ROUTING. No router library. A switch over location.pathname with history.pushState and a popstate
listener, in web/src/routes.ts. Two routes in this slice: "/" (the workspace list) and "/w/:id" (one
workspace, whose only panel in this slice is «Подключить»). An unknown path renders the list plus a
short «такой страницы нет» line rather than a blank screen. Keep the route table a plain data
structure — the next slice adds four more screens to it.

LOGIN SCREEN. One form, two fields (имя, пароль), one submit. The server's real rules are in SIG_API:
trimmed, non-empty, at most 64 RUNES ([...name].length, NOT name.length — an emoji is two UTF-16 code
units and a client stricter than the server locks people out of names it would accept), no control
characters. Pre-validate with exactly those.
Failure copy, which is what decides whether this screen is usable in a closed contour:
  - 401 -> «Неверный пароль или имя». The server deliberately answers the same for both; the UI must
    not invent a distinction it cannot know.
  - 429 -> «Слишком много попыток. Подождите минуту» (the limit is 10/minute per name and address).
  - a network failure or a non-JSON answer -> «Сервер не ответил», plus the status if there was one.
  - anything else -> the server's own message. Never a raw exception, never "undefined".
The error is announced with role="alert"; the submit button is disabled while the mutation is in
flight and says so.

TESTS (vitest + Testing Library): the form submits name and password; a 401 renders the Russian copy
and not the raw error; a 65-rune name is refused client-side and NO request is made (assert that);
the loading state renders neither the form nor the app.

REPORT "contracts" — SessionProvider/useSession's exact shape, the route table's type, the names of
the shared ui/ primitives, and every data-testid, so the next screens do not invent a second Button.`,
  { label: 'ui:auth', phase: 'Shell', model: 'sonnet', schema: SCHEMA })

if (!authUI) log('ui:auth returned nothing — the workspaces screen is told to read the tree for App/session/routes.')
else logDeviations('ui:auth', [authUI])

const workspacesUI = await agentSafe(`${shellCtx}

WHAT EXISTS (both previous agents of this section):
${contractsOf([apiClient, authUI].filter(Boolean)) || '(they reported nothing — read web/src/api/*.ts, web/src/session.tsx, web/src/routes.ts and web/src/ui/ and use what is actually there)'}

YOUR TASK — the workspace list and its creation: DESIGN §14 screen 2, minus the auto-create half.

FILES YOU OWN (new): web/src/screens/Workspaces.tsx, web/src/screens/Workspaces.test.tsx, and
web/src/screens/Workspace.tsx — the single-workspace page shell the Connect panel slots into next
section. Render the workspace's name, slug and revision, and leave a clearly marked region with a
comment naming the panel that lands there; do NOT write the panel.
You may NOT touch: web/src/api/*, App.tsx, session.tsx, routes.ts, ui/*, package.json, or anything
under internal/ except internal/webui/dist/**, which the UI bar's build rewrites. If you need a
route or a primitive that does not exist, say so in "deviations" and work around it — never edit
another agent's file.

THE LIST. Name, slug, revision, and whether a spec is attached (specId is null on a workspace created
without one: say «спека не привязана», never "null"). A row navigates to /w/:id. Creation is one input
plus a button; the server derives the slug and returns it, so SHOW the slug it chose — §14 screen 2 is
explicit that the deterministic suffix (alex, alex-2) must be visible, never silent. Deletion asks for
confirmation and names what is being deleted.

EMPTY AND ERROR STATES, which are most of this screen's value:
  - zero workspaces -> «У вас пока нет воркспейсов» plus the create form, focused, plus one muted
    line: «Воркспейс можно создать и без спеки: пока он будет отвечать только на кастомные
    endpoint'ы. Экран «Спеки» появится в следующем срезе». Use exactly this substitute copy: DESIGN's
    own empty state links to the «Спеки» screen, which is out of scope, and a dead link is worse than
    an honest sentence.
  - the list query failing -> the server's message and a retry button; never a blank page.
  - a name the server rejects (empty after trimming) -> the field's own inline error.
  - a slug collision the server could not resolve -> the server's message verbatim. Do not invent copy
    for a case you cannot reproduce.

TESTS (vitest + Testing Library, network stubbed at the client boundary): rows render from a stubbed
response; creating invalidates the list and shows the returned slug; DELETING asks for confirmation
first and then invalidates the list (the browser acceptance deletes over the API rather than through
this button, so this test is the only thing that covers it at all); the zero state renders the create
form and the muted line; a failing list renders the error and the retry.

REPORT "contracts" — the components' names and every data-testid, because the browser test selects on
those and on nothing else.`,
  { label: 'ui:workspaces', phase: 'Shell', model: 'sonnet', schema: SCHEMA })

if (!workspacesUI) log('ui:workspaces returned nothing — Gate 2 diffs the tree.')
else logDeviations('ui:workspaces', [workspacesUI])

const shellOK = [apiClient, authUI, workspacesUI].filter(Boolean)
log(`Shell: ${shellOK.length}/3 agents reported.`)

// ------------------------------------------------------------------ Gate 2 --
phase('Gate 2')

const shellPaths = 'web/src'
const shellFixCtx = `${CTX_CORE}${CTX_UI}${CTX_TEST}

Repo root is your CWD. You own every file this section produced under web/src/. Do not touch anything
under internal/, cmd/ or web/e2e, do not edit package.json, and never run "npm install".
${SIG_API}
${SIG_UI}`

const gate2 = await runGate('Gate 2', [
  {
    label: 'gate2:contract',
    model: 'sonnet',
    prompt: `${gateCtx(shellPaths)}

VECTOR — DOES THE CLIENT ACTUALLY SPEAK THIS SERVER'S PROTOCOL? You are the only reviewer in this run
who reads BOTH sides. Read internal/admin/auth_handlers.go, internal/admin/security.go and
internal/admin/workspace_handlers.go, then web/src/api/*.ts, and compare field by field and header by
header. A wrong field name is a BLOCKER, not a nit: it compiles, it type-checks, and it fails only in
a browser nobody has run yet.

  - every JSON field name and nullability in types.ts against the Go struct tags (specId, ownerId,
    forkedFrom, scenarioId are pointers in Go and therefore nullable in TypeScript).
  - Content-Type: application/json on every state-changing request, including a body-less DELETE —
    without it the server answers 415.
  - X-CSRF-Token on every non-GET and on no GET; its source is the last login or /api/me.
  - credentials on the fetch, and nothing anywhere trying to read or set the session cookie in script.
  - the 401 path: the client throws, the SESSION layer decides. A screen or client that navigates on
    its own is the bug where a failed request looks like a routing defect.
  - error narrowing: is "unknown" narrowed at the boundary, or is there an "any" or a cast letting a
    malformed body through as a typed object? Search for ": any" and for "as " casts.
  - query keys and invalidation: is the list actually invalidated after create and delete? Is the CSRF
    token cleared on logout? A stale token after re-login is a 403 storm.
  - rune counting on the name: the server counts RUNES (max 64); name.length counts UTF-16 code units,
    so 33 emoji pass the client and fail the server. Check which one the code uses.

Run npm run --prefix web typecheck and npm run --prefix web test; put the real tails in "output" and
every SKIP in "skips". Do not run the npm build — it deletes internal/webui/dist/.gitkeep, and only
the agents told to re-create it may do that.`,
  },
  {
    label: 'gate2:ux',
    model: 'sonnet',
    prompt: `${gateCtx(shellPaths)}

VECTOR — IS THIS THE INTERFACE DESIGN §14 DESCRIBES, AND IS IT USABLE WHEN THINGS GO WRONG? Read
DESIGN.md lines 821-916 with sed (Russian; do not read the file whole) before judging anything.

  - Jargon: §14 states that «рецепт», «JSON Patch» and «матчер» never appear in the interface. Grep the
    strings. Also flag English leaking into Russian copy ("Loading...", "Error") and raw server codes
    shown to a person.
  - The three session states: does 'loading' render NOTHING rather than flashing the login form on
    every reload?
  - Empty states: zero workspaces must be an invitation with a focused create form and the muted line
    about creating a workspace without a spec — not an empty list, and not a link to the «Спеки»
    screen, which does not exist in this slice.
  - The created slug is SHOWN. §14 screen 2 requires the deterministic suffix (alex, alex-2) to be
    visible. A UI that just navigates away is a finding.
  - Failure copy: 401 must not claim to know whether the password or the name was wrong; 429 must say
    to wait; a network failure must not render "undefined" or a stack.
  - The accessibility floor: a <label> per input, a real <form> with a submit button, role="alert" on
    errors, visible focus. Not a full audit — these four.
  - data-testid on everything the browser test will need: the login fields and button, the header's
    user name and its «Выйти» button, the create form, each workspace row and its link. The acceptance selects on testids, never on Russian copy.
  - Anything that breaks with a workspace name containing a quote, an emoji, or 60 characters.

You may run npm run --prefix web test to see what is pinned, but this vector is judged by reading the
components.`,
  },
], shellFixCtx)

// The gate's fix agent is appended LAST, after the three authors: if it renamed
// a data-testid or changed a signature to close a blocker, the corrected line
// must be the one a later section reads. An earlier draft computed this before
// the gate ran, so every correction the gate made was invisible downstream.
const shellContracts = contractsOf([...shellOK, gate2.fix].filter(Boolean))

// ------------------------------------------------------------------ Connect --
// One agent, one screen — but the screen where three seams meet (the workspace's
// own URL, the reserved prefix, and a cross-origin fetch), so it gets its own
// section and its own gate rather than riding along with the shell.
phase('Connect')

const connect = await agentSafe(`${CTX_CORE}${CTX_UI}${CTX_TEST}${INTENDED}

${SIG_API}

${SIG_UI}

WHAT EXISTS (this run's Shell section, reviewed and green):
${shellContracts || '(the shell agents reported nothing — read web/src/api/, web/src/session.tsx and web/src/screens/ and use what is actually there)'}

YOUR TASK — the «Подключить» panel, DESIGN §14 screen 4, browser half. Read that screen's paragraph
first: sed -n '821,916p' DESIGN.md. It is the screen that turns "the mock exists" into "my frontend
talks to it", and in a closed contour it is where a missing corporate root certificate gets diagnosed.

FILES YOU OWN (new): web/src/screens/Connect.tsx, web/src/connect/probe.ts,
web/src/connect/recipes.ts, web/src/connect/probe.test.ts, web/src/connect/recipes.test.ts, plus the
one line in web/src/screens/Workspace.tsx that renders the panel into the region left for it.
You may NOT touch: web/src/api/*, session.tsx, App.tsx, routes.ts, Login.tsx, Workspaces.tsx,
package.json, anything under internal/ or web/e2e.

WHAT THE PANEL SHOWS
1. The workspace's address — WorkspaceView.url, exactly as the server sent it. Do not rebuild it from
   window.location and the slug: the admin host is forbidden from sitting under the base domain, the
   port comes from the request, and the server already did this correctly.
2. Copy buttons. THE TRAP, and it is not theoretical: navigator.clipboard is undefined outside a
   secure context, and this tool runs over plain http on an internal name in every dev and smoke setup
   there is. A copy button that silently does nothing is worse than no copy button. Feature-detect,
   fall back to a selection-based copy, and if neither works select the text and say «скопируйте
   вручную». Test the fallback path.
3. The connection recipes from §14, each with its own copy button and one line of honest context: an
   environment variable (always correct), "?apiBase=" (works only if the frontend supports it — say so
   explicitly), a devtools override, and a curl one-liner that is copy-pasteable as-is.
4. «Сюда пришло N запросов за минуту» — GET /api/workspaces/{id}/traffic?limit=1 already returns
   rate1m, computed server-side for exactly this indicator. Poll it while the panel is visible (a few
   seconds apart), stop when document.visibilityState is 'hidden', and show 0 honestly rather than
   hiding the indicator. Do NOT use /traffic/poll for this: that is the feed's cursor protocol and
   carries no rate.
5. «Проверить» — the browser-side check.

THE PROBE, and its interpretation is the whole point of the screen. It fetches
\`\${ws.url}\${config.reservedPrefix}/health\` — the prefix comes from the session config, NEVER a
hard-coded "/__mocker". That is configurable, the acceptance stack deliberately runs with a
non-default value, and a hard-coded prefix will 404 there. Then:
  - 200, ok:true, workspace === ws.slug -> green «Мок отвечает», and RENDER the workspace and revision
    you read. Rendering the values you got is what makes this observable rather than "nothing threw".
  - 200 but workspace !== ws.slug -> «Отвечает другой воркспейс: X» — a wildcard-DNS or proxy
    misconfiguration, and the one case where a green tick would be a lie.
  - an HTTP error status -> show the status and the server's message.
  - the fetch REJECTS (network / CORS) -> the interesting one. Verified in the code: the mock plane
    sets CORS headers for every request before any branch, so a workspace that EXISTS answers readably;
    but a host that resolves and names NO workspace answers 404 through a path that sets no CORS
    headers, and the browser reports a network error rather than a 404. So the copy must NOT assert a
    single cause. List three, in the order worth checking in a corporate contour: the name does not
    resolve (DNS/wildcard), the certificate is not trusted (the contour's root CA is not installed in
    THIS browser — §14 names this as the failure the two-sided check exists to identify), or the
    workspace no longer exists. One sentence each.
  - never leave the button spinning: a timeout of a few seconds reporting «Не дождались ответа».
probe.ts holds this as a pure function over a fetch result so it can be tested without a browser;
Connect.tsx renders its outcome. probe.test.ts covers every branch above, including the rejection, and one component test covers the
resilience rule below: with the traffic query failing, «Проверить» and its last result stay rendered.

TWO RULES THE ACCEPTANCE ASSERTS ON, so they are contracts rather than taste.
FIRST: before «Проверить» has ever been pressed, the probe-result element is NOT rendered at all — the
browser test asserts its testid is absent, then present with the values read cross-origin. A panel that
always renders a result region holding «ещё не проверяли» satisfies every other line of this brief and
fails that assertion.
SECOND, and the acceptance depends on it too: a FAILING traffic poll degrades the indicator
and nothing else. The workspace can disappear under this panel (someone deletes it in another tab; the
acceptance deletes it deliberately to prove the probe can go red), and when it does, the traffic query
starts answering 404. The «Проверить» button and its last result must stay rendered — a panel that
replaces itself with an error page the moment a background poll fails cannot be used to diagnose
anything, which is this screen's entire job.

WHAT YOU MAY NOT PROMISE. The SERVER side of this check — mocker dialling the workspace's external
name itself, which is what would let the UI say "the server reaches it and your browser does not, so
it is your certificate store" — is a later slice. Do not write copy claiming the server checked
anything, and do not add a second button that does nothing.

BARS, pasted real into "verified":
${UI_BAR}

REPORT "contracts" — the panel's exported names and every data-testid on it, including the ones for the
probe's result, the workspace/revision it renders, and the rate indicator. The browser test in the next
section selects on those and on nothing else.`,
  { label: 'ui:connect', phase: 'Connect', model: 'sonnet', schema: SCHEMA })

if (!connect) log('ui:connect returned nothing — Gate 3 diffs the tree.')
else logDeviations('ui:connect', [connect])

// ------------------------------------------------------------------ Gate 3 --
phase('Gate 3')

const connectPaths = 'web/src/screens web/src/connect'
const connectFixCtx = `${CTX_CORE}${CTX_UI}${CTX_TEST}

Repo root is your CWD. You own web/src/screens/Connect.tsx, web/src/connect/* and the one render line
in web/src/screens/Workspace.tsx. Nothing else, and never "npm install".
${SIG_API}`

const gate3 = await runGate('Gate 3', [
  {
    label: 'gate3:probe',
    model: 'sonnet',
    prompt: `${gateCtx(connectPaths)}

VECTOR — DOES THE PANEL TELL THE TRUTH? This screen's whole job is diagnosis, so a confident wrong
answer is worse here than anywhere else in the app.

Read internal/mockplane/plane.go (the dispatch order and where setCORS runs), internal/mockplane/cors.go
and internal/admin/workspace_handlers.go (workspaceURL) before judging. Then:
  - the probe uses config.reservedPrefix, never a hard-coded "/__mocker". A hard-coded prefix is a
    BLOCKER: the acceptance stack runs with a non-default prefix precisely to catch it.
  - the URL comes from WorkspaceView.url verbatim, nothing is rebuilt from window.location, and no
    double slash can appear when the path is appended.
  - the "another workspace answered" branch exists. Without it a wildcard-DNS misconfiguration shows a
    green tick, which is the one outcome that makes this screen actively harmful.
  - the green branch RENDERS the workspace and revision it read. A branch that only flips a colour
    cannot be told apart from "the fetch did not throw".
  - the rejection branch does not assert a single cause. Verify against the code that a
    resolves-but-unknown host really answers without CORS headers on the non-preflight path, and that
    the copy therefore lists DNS, certificate trust and "no such workspace".
  - no copy claims the SERVER checked anything — that half does not exist in this slice.
  - a timeout exists; the button cannot spin forever.
  - rate1m comes from GET /traffic?limit=1 (server-computed) and not from counting rows off
    /traffic/poll, and polling stops when the tab is hidden.
  - the clipboard fallback: navigator.clipboard is undefined outside a secure context, and every dev
    and smoke setup here is plain http on an internal name. An unguarded call is a BLOCKER — the button
    is dead in exactly the environment this ships into.
  - the two contracts the acceptance asserts on: the probe-result element is ABSENT until «Проверить»
    is pressed the first time, and a 404 from the traffic query degrades only the indicator — the
    button and its last result stay rendered. A panel that unmounts itself when a background poll fails
    cannot be used to diagnose the failure it is reporting.
Run npm run --prefix web test and put the real tail in "output". Do not run the npm build: it deletes
internal/webui/dist/.gitkeep and only the agents told to re-create it may do that.`,
  },
  {
    label: 'gate3:scope',
    model: 'sonnet',
    prompt: `${gateCtx(connectPaths)}

VECTOR — DID THIS SECTION STAY INSIDE ITS LINES, AND IS THE TREE STILL GREEN? Small vector; run it
fast and precisely.

  1. Ownership: this section could create web/src/screens/Connect.tsx and web/src/connect/*, and add
     ONE render line to web/src/screens/Workspace.tsx. Diff the whole UI tree
     (git add -N . ; git diff HEAD -- web/src) and report ANY other file it modified — especially
     web/src/api/*, session.tsx, routes.ts or another screen. An edit to a sibling's file is a blocker
     even when it looks harmless: it is how two agents' assumptions silently diverge.
  2. package.json and package-lock.json must be untouched by this section — run
     "git add -N . ; git diff HEAD -- web/package.json web/package-lock.json" (your section diff above
     does not cover them). A dependency added here is a blocker: the image installs from the lockfile
     with npm ci, and a lockfile written by a second agent is a mismatch that surfaces only there.
  3. Bars, exactly these:
${UI_CHECK}
     Paste the real tails into "output". Then grep the ALREADY-BUILT internal/webui/dist/index.html
     (the Connect section built it): a <script> without src= is a blocker, because the CSP blocks it
     and the page renders blank.
  4. Any new "any", any new "as " cast, any new default export, any component calling fetch directly
     instead of going through web/src/api.
  5. Dead code left behind: a second Button primitive, an unused helper, a commented-out block.`,
  },
], connectFixCtx)

// ------------------------------------------------------------------- Accept --
// Two agents, serial, and the order is not cosmetic: both drive docker compose,
// and two of them in one round tear down each other's stack — the phantom
// failure this project has already paid for. The browser acceptance goes first
// because it is the criterion; the curl-level smoke section goes second because
// it must not be the thing that discovers the UI does not load.
phase('Accept')

const acceptCtx = `${CTX_CORE}${CTX_TEST}${INTENDED}

${SIG_API}

WHAT EXISTS (this run, all sections reviewed and green): web/ builds into internal/webui/dist, which
the binary embeds and the admin plane serves; the login, workspaces and «Подключить» screens work
against the real API. The image builds the UI in its own node stage — verified in the Foundation
section — and .dockerignore makes it impossible for a locally built dist to ride in instead.

${SIG_BUILD}

THE UI'S TESTIDS AND EXPORTS, from every section that wrote them — the gate fixes last, so a corrected
line supersedes the original. Select on these and NEVER on Russian copy:
${contractsOf([apiClient, authUI, workspacesUI, connect, gate2.fix, gate3.fix].filter(Boolean)) || '(no section reported — read web/src/**/*.tsx and take the data-testid values from the source)'}`

const e2e = await agentSafe(`${acceptCtx}

YOUR TASK — the phase criterion: a real Chromium, driving the DOCKER IMAGE, proving the whole vertical.
Nothing else in this run proves it. Every earlier bar passes with the UI missing from the image.

FILES YOU OWN — all three are NEW files that do not exist yet: web/playwright.config.ts,
web/e2e/*.spec.ts, and scripts/e2e.sh.
You may NOT touch: package.json or package-lock.json (@playwright/test is already pinned there at
1.62.1 — the release owning the chromium already cached on this box; if it is missing, that is a
finding for your report, not an npm install), .gitignore (instead, point playwright's outputDir and
reporter output at a path OUTSIDE the repo, e.g. under /tmp, so no artefact ever needs ignoring),
docker-compose.yml, the Makefile (its e2e target already calls your script), anything under web/src
or internal/.

WHAT THE SCRIPT MUST DO — read scripts/smoke.sh FIRST and follow its shape; it discharged these obligations
before you and getting them wrong manufactures failures that have nothing to do with the UI:
  - docker-compose.yml pins its service env file ("env_file: - path: .env") at a FIXED relative path, so the container's
    environment comes from repo-root .env and from nowhere else: --env-file and COMPOSE_ENV_FILES set
    only the interpolation file and never reach the process. Write repo-root .env — back up the
    developer's real one, write yours from .env.example, restore it in a trap on EXIT. Do not invent
    an .env.e2e; it would be silently ignored and the stack would come up on whatever .env was there.
  - login needs a KNOWN password: pick a throwaway one into a shell variable and hash it through the
    image the way smoke.sh does, but under YOUR project name:
    docker compose -p mocker-e2e run --rm -T mocker hash-password "$E2E_PASSWORD"
    Write that hash into the repo-root .env you just wrote. Export COMPOSE_ENV_FILES=/dev/null as
    smoke.sh does, or compose interpolates .env against itself and prints one warning per "$" in the
    argon2id hash — which reads exactly like the real hash-mangling failure it is not.
  - the volume must start EMPTY, or the slug "alex" is already taken and comes back as "alex-2",
    which breaks the URL the panel asserts on. Bring the stack down with -v before and after.
  - run under YOUR OWN compose project name (-p mocker-e2e), which is the whole isolation from
    scripts/smoke.sh; the env file cannot be isolated (see above), so the two scripts must never run
    at the same time — this workflow serialises them and only one agent per round touches docker.
  - set MOCKER_RESERVED_PREFIX=/__ctl in that .env. Note .env.example ALREADY pins the default value,
    so strip that line before appending yours (smoke.sh does exactly this with the password hash:
    grep -v '^MOCKER_...=' into a temp file, move it back, then append). This is what makes the
    criterion mean something: with the default /__mocker, a panel that hard-codes the prefix and
    rebuilds the URL from window.location sends byte-identical requests to one that reads the server's
    config — the whole seam this slice exists to build could be deleted and the test would still pass.
  - PROVE THE PREFIX TOOK EFFECT after the readiness poll below and before the browser starts, because
    a silent failure there re-opens
    exactly that hole — and get the ORDER right: the mock plane resolves the workspace FIRST and 404s
    an unknown host before it ever looks at the reserved prefix, so probing a slug that does not exist
    yet proves nothing and would fail the script for the wrong reason. So: log in over the API and
    create a THROWAWAY workspace (smoke.sh:145-150 is the shape; use a slug that is NOT "alex", which
    the browser test creates later), then curl that workspace's host twice with its Host header —
    /__mocker/health must answer 404 and /__ctl/health must answer 200 — then DELETE the throwaway
    workspace so the browser starts against an empty list. Fail the script with a clear message if
    either curl is wrong: two requests, and they are what stands between this run and a criterion that
    passes with the whole config seam deleted.
  - wait for readiness by polling GET /healthz with the admin Host header, as smoke.sh does; never
    sleep a fixed number of seconds.
  - on failure, dump the container logs (docker compose -p mocker-e2e logs --tail 80) BEFORE tearing
    down: a browser test that fails against a crashed server otherwise leaves no evidence at all.
  - propagate the browser run's exit status as the script's own. A harness that always exits 0 is the
    most expensive kind of green.

web/playwright.config.ts — chromium only, one worker, no retries, testDir './e2e' (vitest owns
src/**, and without this playwright collects the unit tests too), and reporter [['list']] — the default
HTML reporter starts a blocking report server on failure when CI is unset, which would hang this agent
instead of returning a failure. Plus the launch flag that makes the whole thing possible:
  --host-resolver-rules="MAP mocker.local 127.0.0.1, MAP *.mock.local 127.0.0.1"
MAP preserves the port, so http://mocker.local:8080 and http://alex.mock.local:8080 both reach the
compose stack. baseURL is the admin origin. Take the password and the base URL from environment
variables the script exports; hard-coding either makes the two files drift.

THE SPEC — in this order, because each step is the previous step's evidence:
  1. Open "/" and see the login screen — served from bytes inside the binary.
  2. Log in with the throwaway password. Assert the app renders and, through the browser's own
     context, that GET /api/me now answers 200. A real browser storing a real cookie is the assertion
     here; nothing else in this run tests that the cookie survives a browser at all.
  3. Create workspace "alex" through the UI. Assert the row appears AND that the slug shown is "alex"
     (a fresh volume must never need "alex-2" — if it does, the teardown is broken and every later
     assertion is against the wrong host).
  4. Open the workspace. FIRST assert the probe has no result yet (the result testid is absent), then
     press «Проверить» and assert the workspace slug and the revision appear INSIDE the probe-result
     element, by its own testid. Both values are also on the page already — the workspace shell renders
     slug and revision from the admin API — so an assertion that merely finds them somewhere on screen
     is satisfied by a panel that never fetched anything. It is the pre/post transition inside the
     result element that carries the evidence, together with step 6's red branch. This is the criterion: it
     requires the dist embedded, the handler mounted, the cookie accepted, the CSRF header correct,
     the workspace created through the API, the URL built server-side, the reserved prefix taken from
     the server's config (/__ctl here — a hard-coded /__mocker 404s), and the mock plane answering
     cross-origin with CORS headers.
  5. Make REAL traffic and watch the indicator: request a NON-reserved path on the workspace origin
     (any path — it will 404, which is recorded) and assert «N запросов за минуту» becomes at least 1.
     Give this assertion an explicit timeout of at least twice the panel's poll interval: the recorder
     batches writes (half a second) and the panel polls every few seconds, so Playwright's 5-second
     default can expire before a perfectly working indicator updates.
     The reserved prefix is deliberately NOT recorded in traffic (the mock plane returns from the
     reserved branch before the traffic tee), so the probe itself must never be expected to move this
     number — assert it only after traffic that does count.
  6. NEGATIVE CONTROL, and it is not optional — it runs BEFORE the logout, because deleting needs the
     session, and it must not navigate away from the panel, because the workspace page is the only
     place «Проверить» exists. With the panel still mounted, delete the workspace from the SAME browser
     context without touching the page: page.request.delete('/api/workspaces/<id>') with
     'Content-Type: application/json', an Origin header naming the admin host, and the CSRF token read
     from a page.request.get('/api/me') — the request context shares the page's cookies. Then press
     «Проверить» again and assert the panel goes RED with the copy naming the three causes. A probe
     that cannot fail proves nothing, and this is exactly the case (a host that resolves but names no
     workspace) where the mock plane answers without CORS headers, so the browser sees a network error
     rather than a 404.
  7. Reload: still logged in. Then log out through the header's control: back to the login screen, and
     GET /api/me answers 401.
  0. FIRST, before the first navigation — this one is deliberately out of order, and the numbering
     above is otherwise strict: register page.on('pageerror') and page.on('console') and collect
     everything they report. At the end of the spec, assert nothing matching a CSP violation or a page
     error was recorded on any screen visited. Registered after the first goto, these handlers see
     nothing on the login screen, which is the screen most likely to be blanked by the CSP. This is how the run finds out whether the strict CSP actually lets the built app run —
     the alternative is a blank page in production and a green suite here.

PROVE IT, and paste the REAL output into "verified": ./scripts/e2e.sh, in full, including the browser
test's summary. In "measurements" put the workspace URL the panel showed, the health body the probe
read, the rate the indicator reached, and the image build time. If docker or the browser is missing,
say so in "deviations" and set browser to skipped-no-docker / skipped-no-browser: a prerequisite you
did not have is not a pass, and the fields exist so nobody downstream has to guess.

Set "passed" to true ONLY if every assertion above actually ran and passed in front of you: it is the
run's own answer to "was the criterion met", and a prerequisite you did not have makes it false, not
true-with-a-note.

Also fill "goldenIntact" (git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json comes back
clean) and "testsUnmodified" — run exactly this, and it must print NOTHING:
    git diff HEAD --no-renames --diff-filter=MD -- $(git ls-tree -r --name-only HEAD | grep '_test\\.go$')
It lists the test files that existed at HEAD and shows only modifications and deletions, so the test
files this run ADDED (which are expected) cannot show up in it. Do not substitute "git ls-files": after
any reviewer's "git add -N ." that form reports every new test file as a forbidden edit.`,
  { label: 'accept:e2e', phase: 'Accept', model: 'sonnet', schema: ACCEPT_SCHEMA })

if (!e2e) log('accept:e2e returned nothing — the criterion is UNOBSERVED unless the final verify agent runs it.')
else {
  log(`accept:e2e: passed=${e2e.passed} browser=${e2e.browser || 'n/a'} goldenIntact=${e2e.goldenIntact} testsUnmodified=${e2e.testsUnmodified}`)
  for (const m of e2e.measurements || []) log(`accept:e2e measured: ${m}`)
  logDeviations('accept:e2e', [e2e])
}

const smoke = await agentSafe(`${acceptCtx}

The browser acceptance ran before you and its stack is DOWN (it tears its own down with -v in a trap).
You are the only agent allowed to touch docker in this round. What it reported:
${e2e ? `passed=${e2e.passed}, browser=${e2e.browser}, ${(e2e.measurements || []).join('; ')}` : '(it returned nothing — read scripts/e2e.sh and web/e2e/ to see what is already covered, and do not duplicate it)'}

YOUR TASK — extend scripts/smoke.sh so the curl-level bar covers the UI too. smoke.sh must keep
working on a box with NO node and NO browser: it is the check a person runs to answer "is this build
sane", and adding an npm dependency to it would take that away.

FILES YOU OWN: scripts/smoke.sh, and only the section you add to it. Do not restructure the file, do
not touch its existing checks, and do not change its env/backup/teardown machinery — other checks
depend on it and it is not this slice's business.

THREE OF THESE ARE HEADER ASSERTIONS, and check() (scripts/smoke.sh:156) captures only the status and
the body — it never sees a response header. Add a SECOND helper beside it (curl -sD - or -o /dev/null
-D -), in the same style, and leave check() byte-identical. A new helper is not a restructured file.

ADD:
  - GET / on the admin host: 200, Content-Type text/html, and the body contains a <script src= tag
    (the built app, not a placeholder). This is the check that fails if the image ever ships without
    the UI.
  - GET /api/definitely-nope: 404 and NOT the SPA's HTML. Assert the status and that the body is not
    the app — it is net/http's own plain-text "404 page not found" today, and this slice does not
    change it. Do not assert a JSON shape here: the admin mux has no /api/ catch-all and adding one
    would steal the 405s below.
  - GET /api/auth/login: 405. The route is POST-only, and this pins that the mount did not swallow the
    method handling.
  - GET on a hashed asset (find its name by grepping the served index.html — do not hard-code a hash):
    200 and a Cache-Control containing "immutable".
  - GET /: Cache-Control: no-cache. A cacheable index.html is how a stale UI outlives a deploy.
  - GET /w/anything (a client route): 200 and the same HTML as "/" — the SPA fallback, which is what
    makes a reloaded deep link work.
Keep every check's failure message in the file's voice: what was expected, what came back.

PROVE IT: run ./scripts/smoke.sh in full and paste the REAL output into "verified" — every PASS line
and the exit status. If the docker daemon is unavailable, set smoke to skipped-no-docker and say so in
"deviations"; do not report checks you did not run. Set browser to "not-applicable" — you do not drive
one. Fill "goldenIntact" and "testsUnmodified" the same way the previous agent did: goldenIntact is
git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json coming back clean, and
testsUnmodified is this printing NOTHING:
    git diff HEAD --no-renames --diff-filter=MD -- $(git ls-tree -r --name-only HEAD | grep '_test\\.go$')`,
  { label: 'accept:smoke', phase: 'Accept', model: 'sonnet', schema: ACCEPT_SCHEMA })

if (!smoke) log('accept:smoke returned nothing — the curl-level UI checks may be missing from scripts/smoke.sh.')
else {
  log(`accept:smoke: passed=${smoke.passed} smoke=${smoke.smoke || 'n/a'}`)
  logDeviations('accept:smoke', [smoke])
}

// ------------------------------------------------------------------ Gate 4 --
phase('Gate 4')

const acceptPaths = 'web/e2e web/playwright.config.ts scripts'
const acceptFixCtx = `${CTX_CORE}${CTX_TEST}

Repo root is your CWD. You own web/playwright.config.ts, web/e2e/*, scripts/e2e.sh and the UI section
of scripts/smoke.sh. Do not touch package.json, .gitignore, docker-compose.yml, the Makefile, web/src
or anything under internal/. You are the only agent running docker this round.`

const gate4 = await runGate('Gate 4', [
  {
    label: 'gate4:criterion',
    model: 'sonnet',
    prompt: `${gateCtx(acceptPaths)}

VECTOR — IS THE CRITERION OBSERVED, OR MERELY ASSERTED? A guard, a tripwire and a criterion are the
parts that look identical whether they work or not, so attack this one hardest.

For EACH assertion in web/e2e/*.spec.ts, answer one question in writing: could it still pass with the
feature absent? Report every one where the answer is yes. In particular:
  - Does the probe assertion read VALUES out of the response (workspace, revision) and render them, or
    does it only check a colour/class that a hard-coded string could produce?
  - Does the stack really run with MOCKER_RESERVED_PREFIX=/__ctl? Grep scripts/e2e.sh. Without it a
    hard-coded "/__mocker" in the UI passes and the config seam is untested.
  - Is the negative control real — the workspace deleted, the probe re-run, the red branch asserted?
    A probe that cannot fail proves nothing.
  - Is the traffic-indicator assertion made against traffic that is actually RECORDED? The mock plane
    returns from the reserved-prefix branch before the traffic tee, so the health probe itself never
    appears in traffic; an assertion expecting it to would be waiting for a number that never moves.
  - Does the login step prove a REAL browser stored the cookie (a subsequent authenticated call), or
    only that a form submitted?
  - Are the pageerror / CSP-violation listeners registered before the first navigation, and does the
    test actually FAIL on them, or are they collected and ignored?
  - Does the slug assertion pin "alex" exactly? "alex-2" means a dirty volume and every later
    assertion is against the wrong host.
Then: does scripts/e2e.sh propagate the browser run's exit status? A harness that exits 0 regardless is
the most expensive kind of green. Read it and say which line decides the exit code.
Report every SKIP the run printed in "skips". You may run ./scripts/e2e.sh yourself — you are the only
agent doing docker this round, and that script is the one exception to gateCtx's READ-ONLY line above:
it writes and restores repo-root .env and rebuilds the image, and it changes no source file — and if you do, paste the real tail into "output".`,
  },
  {
    label: 'gate4:harness',
    model: 'sonnet',
    prompt: `${gateCtx(acceptPaths)}

VECTOR — CAN THIS HARNESS HURT SOMEONE, OR MANUFACTURE A FAILURE THAT IS NOT REAL? Read
scripts/smoke.sh first: it already solved these problems and its shape is the reference.

DO NOT RUN scripts/e2e.sh OR scripts/smoke.sh. Both write repo-root .env and run "docker compose down
-v" against a single 127.0.0.1:8080 binding; your co-reviewer is running one of them right now, and two
of them in one round is exactly the phantom failure this vector exists to prevent. Judge by reading.

  1. .env handling in scripts/e2e.sh: is a developer's real .env backed up and restored in a trap on
     EXIT, including on failure and on an interrupt? A script that clobbers it and dies leaves the
     developer's stack broken with no message.
  2. Isolation from smoke.sh is the compose PROJECT NAME (-p) and nothing else: docker-compose.yml
     pins its service env file at a fixed relative .env, so --env-file never reaches the container and
     a separate env file is impossible. e2e.sh must therefore write repo-root .env under a backup and
     an EXIT-trap restore, exactly as smoke.sh does. Do NOT report the shared .env path as a defect —
     report a MISSING backup or a missing restore, and report it if the two scripts could ever run at
     the same time (this run serialises them and only one agent per round touches docker).
  3. Does it bring the volume down with -v both BEFORE and AFTER? A leftover volume means the slug
     "alex" is taken and the URL assertion fails for a reason that has nothing to do with the UI.
  4. Does it wait for readiness by POLLING /healthz rather than sleeping? A fixed sleep is a flake
     generator on a loaded box.
  5. Does it dump container logs on failure BEFORE tearing down? Without that, a server that crashed
     mid-test leaves a curl error and no cause.
  6. Do the playwright artefacts (traces, screenshots, the HTML report) land OUTSIDE the repo? Nothing
     in this run may require a .gitignore edit it does not own.
  7. Does anything here run npm install, add a dependency, or edit package.json / package-lock.json?
     That is a blocker: the image installs with npm ci from the committed lockfile.
  8. scripts/smoke.sh: are the new UI checks ADDED without touching the existing ones? Diff carefully —
     a changed teardown or an edited check() is a blocker even when the new checks pass. A NEW helper
     beside check() is expected and is not a violation: three of the new checks assert response
     headers, which check() cannot see. Does the
     asset check discover the hashed filename from the served HTML rather than hard-coding a hash that
     changes on every build?
  9. Does either script leave anything running or any file changed after a successful run? Run
     "git status --porcelain" and report anything unexpected.`,
  },
], acceptFixCtx)

// -------------------------------------------------------------------- Final --
// The check and the security review run TOGETHER (both read-only), so their
// findings feed ONE fix round instead of two. The verify agent then re-observes
// the tree — the criterion flag must come from the last step that looked at it,
// not from the acceptance agent that ran before the fixes.
phase('Final')

const carried = [...gate1.unresolved, ...gate2.unresolved, ...gate3.unresolved, ...gate4.unresolved]
const deadReviewers = [...gate1.dead, ...gate2.dead, ...gate3.dead, ...gate4.dead]
if (deadReviewers.length) log(`WARNING: these gate vectors never ran: ${deadReviewers.join(', ')}. Those sections are unreviewed along them.`)
log(`Carried into the final round: ${carried.length} finding(s) that survived their section's gate.`)

const finalPair = await parallel([
  () => agent(`${gateCtx(P1D1_PATHS)}

FINAL CHECK — you run the repo-wide bars ONCE, at the end, and you report what they actually printed.
You are READ-ONLY: report, never fix. In particular do NOT run the npm build: it empties
internal/webui/dist, and a security reviewer is reading that directory in parallel with you right now.

Run all of these and paste the real output:
${GO_BAR}
${UI_CHECK}
  test -f internal/webui/dist/.gitkeep ; echo "placeholder present: $?"
  git status --porcelain
  git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json ; echo "golden exit: $?"
  git diff HEAD --no-renames --diff-filter=MD -- $(git ls-tree -r --name-only HEAD | grep '_test\\.go$') ; echo "pre-existing tests diff above (empty is correct — new test files never appear in it)"
  git check-ignore --no-index -q internal/webui/dist/.gitkeep ; echo "check-ignore exit: $? (want 1)"
  git status --porcelain -uall internal/webui/dist
Then judge, and raise a finding for each that is wrong:
  - Any bar not green.
  - The golden diff not empty (blocker: it is write-once and nothing in this slice is upstream of it).
  - ANY pre-existing *_test.go modified (blocker: there is no authorised exception in this run).
  - internal/webui/dist/.gitkeep missing from the WORKTREE (blocker — Vite's emptyOutDir deletes it on
    every build, so an agent that built without re-creating it is the cause, and a fresh clone would
    then fail to compile: "//go:embed all:dist" needs at least one file). git ls-files reads the index,
    not the worktree, so the check is "test -f".
  - git status --porcelain -uall internal/webui/dist listing anything besides the .gitkeep: that is a
    built asset the ignore rule failed to cover.
  - git status --porcelain showing files nothing in this run should have produced.
  - Any test the suite SKIPPED — report each with the reason it printed, in "skips".
  - A "make build" / "make test" / "make lint" / "make smoke" target that now requires node.
Do not run docker and do not run the browser acceptance: the Accept section owns those and a
concurrent stack is how a phantom failure is made.`, { label: 'final:check', phase: 'Final', model: 'sonnet', schema: REVIEW_SCHEMA }),

  () => agent(`${gateCtx(P1D1_PATHS)}

FINAL SECURITY REVIEW — the one opus agent in this run, because this is the only step whose miss is
both uncatchable downstream and expensive to undo: an unauthenticated static handler has just been
mounted in front of an authenticated plane, and nothing after you re-checks it.

Read DESIGN §15 (lines 917-982, Russian, read the RANGE) for the threat model this project actually
signed up to: shared password, no isolation between people, admin plane on its own host, mock plane
unauthenticated by design.

Attack these, with a file and a line for each finding:
  1. AUTH BYPASS. Can any request reach a protected handler without passing attachSession/enforceCSRF
     now that a wrapper sits in front of the mux? Is the wrapper INSIDE httpx.Chain or outside it?
     Does it ever call next.ServeHTTP after already writing a response?
  2. WHAT THE UNAUTHENTICATED SURFACE LEAKS. index.html and the assets are served to anyone who can
     reach the admin host. Does anything in the BUILT bundle contain configuration, a hostname, a
     token, a password or a build-time secret? Grep the built dist for the base domain, for "password",
     for "token". The server config object must arrive only through an authenticated response.
  3. PATH TRAVERSAL out of the embedded FS: ../, %2e%2e%2f, a NUL byte, a leading //, a Windows-style
     separator, a symlink-shaped name. The embedded FS is not the disk, but the lookup can still be
     wrong.
  4. THE CSP: is it on every response the UI serves? Does script-src stay 'self'? Is connect-src's
     width deliberate and documented (the panel fetches a host known only at runtime), or accidental?
     Does anything rely on 'unsafe-eval' or an inline script?
  5. THE COOKIE. Nothing in the UI may read or set it (it is httpOnly). MOCKER_DEV=1 lives in
     .env.example and drops Secure for local http — confirm no agent moved it, no agent added a second
     copy, and no code path turns it on by default.
  6. CACHING. index.html no-cache, hashed assets immutable. An immutable index.html serves a stale
     app after a deploy; a no-store on assets is merely slow. Also: does any authenticated JSON
     response now carry a cache header it did not have before?
  7. SUPPLY CHAIN. package.json pins exactly, package-lock.json is committed, the image installs with
     npm ci, and nothing is fetched at runtime (no CDN, no webfont, no remote source map). Check the
     built HTML and CSS for an external URL.
  8. THE DEV PROXY must exist only in the Vite config, never in anything the image runs, and the
     Origin rewrite must not have been "solved" by weakening internal/admin/security.go — diff that
     file and say whether originAllowed or enforceCSRF changed at all. If either did, that is a
     blocker regardless of how it is justified.
  9. DENIAL OF SERVICE, at this project's threshold only: does serving the UI add an unbounded read,
     an allocation per request proportional to a header, or a new unauthenticated path that hits the
     database? /healthz and /readyz must still answer without touching the UI code.
Report ONLY what you can point at. An empty findings array is the correct answer if the mount is
clean — this vector's value is precision, not volume.`, { label: 'review:security', phase: 'Final', model: 'opus', schema: REVIEW_SCHEMA }),
])

const [finalCheck, security] = finalPair
if (!finalCheck) log('WARNING: final:check returned nothing — the repo-wide bars were NOT observed this round. Do not read the absence of findings as green.')
if (!security) log('WARNING: review:security returned nothing — the mount was NOT security-reviewed. That vector did not run.')

const finalFindings = [
  ...(finalCheck?.findings || []),
  ...(security?.findings || []),
]
const finalBlockers = finalFindings.filter((f) => f.severity === 'blocker' || f.severity === 'major')
for (const f of finalFindings.filter((x) => x.severity === 'minor')) log(`final minor: ${f.file}:${f.line} — ${f.summary}`)
for (const s of [...(finalCheck?.skips || []), ...(e2e?.skips || []), ...(smoke?.skips || [])]) log(`SKIP reported: ${s}`)

const todo = [
  ...carried.map((f, i) => `${i + 1}. [carried from a section gate, ${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`),
  ...finalBlockers.map((f, i) => `${carried.length + i + 1}. [${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`),
].join('\n')

let finalFix = null
if (todo) {
  log(`Final fix round: ${carried.length} carried + ${finalBlockers.length} from the final pair.`)
  finalFix = await agentSafe(`${CTX_CORE}${CTX_GO}${CTX_UI}${CTX_TEST}

FINAL FIX ROUND. You own every file this run produced — web/, internal/webui/, the mount and the
config object in internal/admin/, Dockerfile, .dockerignore, .gitignore, Makefile, README.md,
scripts/e2e.sh and the UI section of scripts/smoke.sh. Fix the list below, changing as little as
possible; every edit maps to a numbered item.

${todo}

RULES, and the last two have both been violated by an end-of-run fix agent on an earlier phase:
- Verify each item against the code FIRST. Reviewers false-positive. If one is wrong, do NOT "fix" it:
  record it in "deviations" with the evidence.
- Add or extend a test for every real defect you fix.
- NEVER touch internal/gen/testdata/p1b_body_hashes.json. Regenerating a baseline keeps the assertion
  and destroys its meaning, which is worse than deleting it because it reads as a pass.
- NEVER modify a pre-existing *_test.go. There is no authorised exception in this run. If a pre-existing
  test now fails, the fix is in the code it tests, not in the test.
- Never run "npm install" and never edit package.json or package-lock.json.
- Finish with the bars clean and paste the REAL output into "verified":
${GO_BAR}
${UI_BAR}`,
    { label: 'final:fix', phase: 'Final', model: 'sonnet', schema: SCHEMA })
  if (finalFix) logDeviations('final:fix', [finalFix])
  else log('WARNING: the final fix agent returned NOTHING. Every item above is UNFIXED and unverified.')
}

const verify = await agentSafe(`${gateCtx(P1D1_PATHS)}

FINAL VERIFICATION — you are the LAST agent to look at this tree, so every end-of-run verdict comes
from you rather than from an earlier agent's snapshot. gateCtx above opens with READ-ONLY; for you the
exception is exactly this: the npm build (with the touch that restores the placeholder) and
./scripts/e2e.sh, which rebuilds the image and writes and restores .env. You change no source file.

${todo ? (finalFix
  ? `A fix agent has just addressed ${carried.length + finalBlockers.length} item(s), renumbered from 1 here:\n${todo}\n\nFor each, say whether it is ACTUALLY resolved in the tree as it stands now. A fix that silences a symptom is not resolved. Put every unresolved one in "unresolved" by its number.`
  : `The fix agent DIED and these ${carried.length + finalBlockers.length} item(s) are UNFIXED, renumbered from 1 here:\n${todo}\n\nCheck each against the tree anyway — some may never have been real — and put every one that is still open in "unresolved" by its number.`)
  : 'No blocker or major survived the section gates and the final pair, so there is no fix list. Verify the bars and the criterion below.'}

Then run, and paste the real output:
${GO_BAR}
${UI_BAR}
  git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json ; echo "golden exit: $?"
  git diff HEAD --no-renames --diff-filter=MD -- $(git ls-tree -r --name-only HEAD | grep '_test\\.go$')
  ./scripts/e2e.sh
Fill the report honestly:
  green            — true only if the Go bar and the UI bar are ALL clean.
  criterion        — "observed-passing" ONLY if you ran ./scripts/e2e.sh yourself and its browser test
                     passed in the tree as it stands NOW: a real Chromium, through the image, logging
                     in, creating "alex", and the panel rendering the workspace and revision it read
                     cross-origin under the non-default reserved prefix. "not-observed" if you did not
                     run it — never infer it from a green build. "observed-failing" if it ran and failed.
  browser          — passed / failed / skipped-no-docker / skipped-no-browser.
  goldenIntact     — the golden diff came back clean.
  testsUnmodified  — the diff command above over the pre-existing *_test.go files printed NOTHING.
  skips            — every test the suites reported as SKIP, with the reason printed.
You may run docker: the Accept section's stack is down and you are the only agent running now.`,
  { label: 'final:verify', phase: 'Final', model: 'sonnet', schema: VERIFY_SCHEMA })

if (!verify) log('WARNING: final:verify returned nothing — the run ends UNVERIFIED. The last observation of this tree is the acceptance agent\'s, which ran before the final fix round.')
else {
  log(`FINAL: green=${verify.green} criterion=${verify.criterion} browser=${verify.browser || 'n/a'} goldenIntact=${verify.goldenIntact} testsUnmodified=${verify.testsUnmodified} unresolved=${(verify.unresolved || []).length}`)
  for (const u of verify.unresolved || []) log(`UNRESOLVED ${u.n}: ${u.why}`)
  for (const s of verify.skips || []) log(`SKIP at the end of the run: ${s}`)
}

return {
  phase: 'P1d-1 — the admin UI ships inside the binary',
  // Every verdict below comes from the LAST agent that observed the tree, not
  // from a mid-run snapshot: an earlier phase reported "criterion not proven"
  // for a criterion that passed, because the flag came from an agent that ran
  // before the fix round which closed the failures it had reported.
  green: verify ? verify.green : null,
  criterion: verify ? verify.criterion : 'not-observed',
  browser: verify ? verify.browser : (e2e ? e2e.browser : null),
  goldenIntact: verify ? verify.goldenIntact : (e2e ? e2e.goldenIntact : null),
  testsUnmodified: verify ? verify.testsUnmodified : (e2e ? e2e.testsUnmodified : null),
  unresolved: verify ? (verify.unresolved || []) : carried.concat(finalBlockers),
  deadAgents: [
    ...(scaffold ? [] : ['ui:scaffold']),
    ...(webuiGo ? [] : ['go:webui']),
    ...(plumbing ? [] : ['build:plumbing']),
    ...(kernelScaffold ? [] : ['kernel:scaffold']),
    ...(kernel ? [] : ['foundation:kernel']),
    ...(apiClient ? [] : ['ui:api']),
    ...(authUI ? [] : ['ui:auth']),
    ...(workspacesUI ? [] : ['ui:workspaces']),
    ...(connect ? [] : ['ui:connect']),
    ...(e2e ? [] : ['accept:e2e']),
    ...(smoke ? [] : ['accept:smoke']),
    ...(finalCheck ? [] : ['final:check']),
    ...(security ? [] : ['review:security']),
    ...(todo && !finalFix ? ['final:fix'] : []),
    ...(verify ? [] : ['final:verify']),
    ...deadReviewers,
  ],
  files: [scaffold, webuiGo, plumbing, apiClient, authUI, workspacesUI, connect, e2e, smoke, finalFix]
    .filter(Boolean)
    .flatMap((r) => r.files || []),
}

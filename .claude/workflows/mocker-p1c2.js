export const meta = {
  name: 'mocker-p1c2',
  description: 'P1c slice 2: when-conditions, the RAM live-state layer, traffic with polling, and edits/endpoints made from observed traffic',
  phases: [
    { title: 'Engines', detail: 'the when-evaluator first (it exports what the others call), then livestate, traffic and custom endpoints in parallel', model: 'sonnet' },
    { title: 'Gate 1', detail: 'two reviewers on the four new engines, one fix agent if either raises a blocker', model: 'sonnet' },
    { title: 'Serve', detail: 'mockplane: variant choice, then traffic capture, then custom endpoints — serial, one package at a time', model: 'sonnet' },
    { title: 'Gate 2', detail: 'two reviewers on the serving path', model: 'sonnet' },
    { title: 'Admin', detail: 'live-state and traffic handlers, then endpoints and the from-traffic conversions, then the binary wiring', model: 'sonnet' },
    { title: 'Gate 3', detail: 'two reviewers on the admin surface and the wiring', model: 'sonnet' },
    { title: 'Accept', detail: 'the read-only scenario end to end in Go, then the docker stack', model: 'sonnet' },
    { title: 'Review', detail: 'correctness and security across the whole slice, both opus', model: 'opus' },
    { title: 'Final check', detail: 'the no-findings path still ends on an observation of the tree, not on a self-report', model: 'opus' },
    { title: 'Fix', detail: 'apply the findings, at most two rounds', model: 'sonnet' },
    { title: 'Verify', detail: 'check the fix semantically, not just that it compiles', model: 'opus' },
  ],
}

// ---------------------------------------------------------------------------
// Run-level decisions. Logged here, where they were made, rather than left to
// be re-derived from the diff. Every one of them survived a pre-flight review
// of THIS list (one opus agent + one independent external reviewer) that found
// six blockers in its first draft; the corrected form is what follows.
// ---------------------------------------------------------------------------

const SPEC_PATH = (typeof args !== 'undefined' && args && args.specPath) || 'output.swagger.json'

log(`Acceptance spec: ${SPEC_PATH} (gitignored, 347 KB; every test that reads it SKIPS itself when it is absent, and NO agent may read or cat it — jq only).`)

log('SCOPE — this run is SLICE 2 of P1c: the when[] conditions become executable, a RAM live-state layer gives force-status and fail-next-N, traffic is recorded and polled, and an observed request/response can be turned into an edit or into a custom endpoint. The phase criterion is DESIGN §19 line 1111 read as a whole: the frontend logs in (slice 1 proved that), walks a read-only scenario, and sees a list and a card that agree.')

log('SCOPE WAS CUT, and the cut is the most important decision here. The first draft carried the FULL session layer (pause and delay as actions), the fail_directive column and the full custom-endpoint CRUD. Both pre-flight reviewers independently pointed at DESIGN §19 line 1112: "весь session-слой", "кастомные endpoint\'ы" and "задержки и fail" are P2. What stays is what §19 line 1111 names for P1 plus what P1d (the React UI) cannot invent for itself.')

log('DECISION — the livestate "fail" action IS in this slice, and the justification is written down because DESIGN §19 line 1112 defers "задержки и fail" as one phrase. Three things outweigh the phrase: HANDOFF.md assigns fail_next to this slice by name; §14 screen 5 lists «сломать следующий запрос» as a button with no other source; and §12 spends a paragraph on WHERE the remainder must live, which is the reason the RAM layer exists at all. It costs nothing outside RAM — no column, no migration, no revision bump. "delay" and "pause" come out of that same §14 sentence and are cut anyway, for their own reasons rather than for a phase label: pause is a self-inflicted denial of service on an unauthenticated plane whose only un-pause switch is itself pausable, and delay duplicates op_overrides.delay_ms, which already ships. HANDOFF names delay for this slice explicitly (pause only implicitly, under "session-слой"), so this is a deliberate narrowing of HANDOFF, not an oversight.')

log('DEFERRED TO P2, deliberately, so nobody reads a gap as a defect. (1) livestate actions "delay" and "pause": the API accepts the names and answers 501 naming P2 rather than storing them — pause in particular is a self-inflicted denial of service on an unauthenticated plane (§15, §18: no rate limit on the mock plane), and a paused workspace whose only un-pause switch is itself paused is unrecoverable until the TTL expires. (2) op_overrides.fail_directive stays PRESERVED-ONLY: §12 puts the DIRECTIVE in the edit and only the REMAINDER in session, and the column\'s typed shape has never been written down — inventing it mid-run is how a schema gets decided by whichever agent reaches it first. The livestate "fail" action needs none of it: the whole directive lives in RAM. (3) PUT on a custom endpoint and the schema-tree editor (§14 screen 6): this slice creates, lists, serves and deletes, which is exactly what "создать endpoint из запроса" needs. (4) POST /api/workspaces/:id/preview {draft}: it is the live preview of §14 screen 5, and §19 puts the editor with the schema tree in P2.')

log('DEFERRED TO P1d (the UI slice), named because they are P1-row items and silence would read as forgetting them. (1) The «Подключить» panel and its «Проверить» button: the browser half is UI, and the SERVER half — the server dialling https://<slug>.<base>/__mocker/health by external name — would be the first outbound HTTP client in this tree. The P1c-1 security review asserts "no http.Get / http.Client.Do / net.Dial anywhere" as a standing invariant; breaking it deserves its own slice with §15\'s allowlist rules applied, not a corner of this one. Two pieces of that screen DO ship here, because a UI-only slice cannot invent either: the "N requests in the last minute" indicator, as a field of the traffic response (a COUNT(*) over rows this slice already writes), and the workspace\'s own external URL as a field of the admin workspace view — nothing exposes it today, and config forbids the admin host from sitting under the base domain, so a browser cannot derive it from window.location. (2) The «Спеки» screen: its API (import, list, report) shipped in P1a; ZIP, URL import and migrate-workspaces are a separate late slice by DESIGN §21. (3) «Первый вход» auto-creating a workspace from MOCKER_DEFAULT_SPEC: cfg.DefaultSpec is parsed at internal/config/config.go:116 and read by NOTHING (rg -n DefaultSpec --glob "*.go" returns two hits, both inside config). That is a dead knob and it is recorded as one; what MOCKER_DEFAULT_SPEC names (a file, an id, a spec name) is not pinned by DESIGN, and pinning it belongs with the screen that shows the chosen slug.')

log('ALSO NOT IN THIS SLICE and not new: schema_patch application, the faker/template/sequence recipes, WS/SSE traffic streaming, stateful resources, checkpoints and bundles, request validation (validate_req stays uninterpreted), domain.AuthSettings.RequireHeader, the SameSite=None cookie rule of §10 (nothing here makes the mock set a cookie), and the P1b MaxBytes soft-ceiling debt recorded in HANDOFF.md.')

log('DECISION — when[] evaluation is ORDERED, and the order is a correctness property, not a detail. Variant.When lives in Row.Responses, a map[string]Variant, and Go randomizes map iteration: "the first matching variant wins" over a map means the served status is nondeterministic the moment two statuses both carry a matching when[]. The rule is: iterate the response keys in ASCENDING NUMERIC order and take the first whose when[] is non-empty AND matches. A test with two matching variants pins it.')

log('DECISION — the layer order at serve time, in one place, because three agents implement pieces of it. After a route matches: (1) route_off still answers the 404 it answers today and consumes NOTHING — a disabled route is gone, and a forced status on it would let a client tell a disabled route from a missing one, which is exactly what slice 1\'s 404 shape prevents; (2) livestate.Apply runs ONCE per request and is what consumes a fail counter; (3) the delay path is untouched (settings.delayMs or the row\'s delay_ms, clamped at 30s); (4) the status is chosen: a livestate forced status wins, else the first matching when[], else active_status, else the document\'s own choice; (5) everything downstream — the declared-variant lookup, the synthetic variant for an undeclared status, responses[status], the compiled recipe set — keys off the FINAL status exactly as it does today. DESIGN §4 puts Session above Workspace and that is why (4) is ordered the way it is. §6 draws the session check BEFORE the route table; we check it after the match instead, because a forced status must still serve the declared body for that status when the document declares one — and because a directive addressed to one operation cannot be resolved before we know which operation was hit.')

log('DECISION — the request body is read ONCE per request, capped, and shared. A body predicate (in:"body") and traffic\'s req_body both need it. The plane reads at most max(cfg.TrafficMaxBody, 64 KiB), restores r.Body for everything downstream, and marks the buffer truncated when it hit the cap. A body predicate over a TRUNCATED buffer evaluates FALSE — never a match on a partial document. Parsing follows DESIGN §8: application/json strictly, text/plain and no-content-type read as text and tried as JSON with no 400 on failure, multipart untouched.')

log('DECISION — the response is captured by a write-through tee that STOPS copying at cfg.TrafficMaxBody (default 8 KiB), not by buffering the whole response. cfg.MaxResponse is 4 MB; buffering that per in-flight request is exactly the hot-path cost §18 exists to remove.')

log('DECISION — traffic never blocks the request goroutine and never INSERTs per request (§18). Record() drops on a full queue and counts the drops; one writer goroutine flushes a batch in ONE transaction on a size or interval trigger; retention is pruned every N inserts down to MOCKER_TRAFFIC_RETENTION per workspace. Because that is asynchronous, the recorder exposes Flush(ctx) — the acceptance test and DELETE /traffic both use it, and NO test may assert traffic through a time.Sleep.')

log('DECISION — bodies are suppressed by PATH, not by route. §15 keeps auth-endpoint bodies out of storage entirely; the 404s this slice records (matched_kind "none") have no route at all, and a typo\'d POST /api/login with a real password in it is precisely the request that must not be stored. Suppression is therefore computed from the normalized request path via authpreset.IsAuthPath (exported by this slice from the existing unexported isAuthPath, whole-segment matching preserved — "/journal/tokens" is NOT an auth path and a substring match would break that).')

log('DECISION — a traffic row becomes an edit or an endpoint in the ADMIN handler, never in a repo, and the admin handler is the one place that knows basePath. traffic.path is the path as requested, WITH settings.basePath glued on; op_overrides.path and custom_endpoints.path are relative, WITHOUT it. Nothing in internal/traffic or internal/overrides can strip a prefix it cannot see, and internal/overrides may not import internal/workspaces. The two conversions then resolve their key DIFFERENTLY, and that is the sharp edge: to-endpoint strips ws.Settings.BasePath from the concrete path, because a custom endpoint IS the literal route somebody asked for; to-override strips nothing — it resolves traffic.matched_id to the operations row and uses that row\'s TEMPLATE path ("/widgets/{widgetId}"), because overrides.OpKey over anything else produces a key the plane\'s own lookup can never generate. The acceptance test runs on a workspace with a NON-EMPTY basePath, or this whole class of defect passes every test.')

log('DECISION — a custom endpoint needs an identity of its own. router.Route.OpRowID is 0 for a custom route and respond.go keys declared variants as rt.variants[route.OpRowID], so every custom route would collide on key 0 and traffic\'s matched_id would have no source. router.Route gains CustomRowID (custom_endpoints.id, 0 for a spec operation), and serveRoute dispatches on route.Custom into a serving path that never touches rt.variants at all.')

log('DECISION — a custom-endpoint write bumps workspaces.revision INSIDE ITS OWN TRANSACTION, the same way overrides.Repo already does (internal/overrides/repo.go, bumpRevisionTx). The route cache is keyed (workspace_id, revision); without the bump a created endpoint never enters a route table and 404s until something else edits the workspace. It must NOT call workspaces.Repo.Update: that opens a second db.Write on a one-connection writer pool and deadlocks the process.')

log('DECISION — a workspace with no spec can still serve custom endpoints. Today routes.go returns the plain 404 whenever ws.SpecID is nil, before any runtime exists, and buildRuntime dereferences *ws.SpecID on its first line. "Create an endpoint from a request" on a fresh workspace would silently do nothing. buildRuntime gains a spec-less path: no generator, no variants, a table built from the custom rows alone.')

log('DECISION — cross-table conflicts are refused in ADMIN, not inside either repo. DESIGN §8: two custom endpoints colliding on (method, canonical_path) is a conflict, a custom endpoint canonically equal to a SPEC operation is an override and is allowed, and an op_overrides row plus a custom endpoint on the same (method, path) is forbidden outright ("иначе правка молча не действует"). The first is the UNIQUE index at migrations/0001_init.sql:210 and router.Conflicts\' own key; the third spans two tables and two repos, so it belongs to the handler that already holds both.')

log('DECISION — the new RAM package is called internal/livestate, NOT internal/session. internal/auth already owns admin sessions and store.DB has a sessions table; a second "session" in the same binary is the adjacent-same-name swap this project\'s gate has caught before.')

log('DECISION — no new environment variables. MOCKER_TRAFFIC_MAX_BODY and MOCKER_TRAFFIC_RETENTION already exist (config.go:124,126). The livestate TTL, the traffic queue depth, the batch size and the flush interval are package constants with doc comments; DESIGN\'s deployment table names none of them, and a knob nobody sets is a knob nobody tunes.')

log('DECISION — the new plane and admin dependencies are SETTERS, never constructor parameters. mockplane.New has 8 qualified call sites in 7 files plus 18 unqualified in-package ones (26 in 9 files); admin.New has 5 call sites in 5 files and its second parameter is already an *auth.Manager named "sessions". Slice 1 paid this lesson once with SetOverrides. Every setter must have a caller in cmd/mocker/main.go AND in the acceptance harness, named in a prompt that owns that file — a nil left in main.go ships a dead feature with a fully green suite, which is the P1a/P1c-1 near-miss HANDOFF.md records twice.')

log('DECISION — the byte-identity guard for THIS slice is not the golden, and saying so is the point. internal/gen/testdata/p1b_body_hashes.json hashes gen.Generator.Body directly, and no agent in this run owns a file under internal/gen: the status choice, the response tee, the merged route table and serveNoRoute all live DOWNSTREAM of gen.Body, so the 419 hashes match no matter what the serving path does. The golden stays as internal/gen own tripwire — it proves the generator was not touched, and nothing more. The guard that CAN fail is the existing, already-reviewed test corpus of internal/mockplane (respond_test.go, plane_test.go, routes_test.go, runtime_test.go), which asserts today behaviour in detail and which two Serve agents have permission to edit. So: those tests must pass UNMODIFIED, every gate diffs them for deleted or weakened assertions, and a weakened assertion is a blocker.')

log('DECISION — domain.Settings.NotFoundBody is applied by this slice. It is parsed, stored, size-limited by internal/workspaces and read by NOTHING; the mock plane\'s 404 is the one place it means anything (§6, §8). It is fifteen lines, it sits in the 404 path this slice is already editing, and a stored setting that silently does nothing is the same defect class as a nil in main.go.')

log('Agent count: 20 call sites — 23 agents on the typical path, 34 worst case (three gate fixes, six gate re-reviews, a second fix/verify round). Above the medium guideline of 15, deliberately and for the same reason P1a, P1b and P1c-1 were: the slice spans four new packages and three existing ones, the seams between them are the actual risk, and collapsing them into fewer agents means one agent holding two packages worth of context and reviewing its own seam.')

// cmd, go.mod and go.sum are in the diff path list deliberately, not for
// completeness: the wire agent puts three setters into the real binary, and
// go.mod/go.sum are the tripwire for HARD RULE 2. internal/gen is in the list
// so a reviewer can see that the golden was NOT touched.
const P1C2_PATHS = 'internal/overrides internal/livestate internal/traffic internal/customep internal/mockplane internal/router internal/admin internal/server internal/authpreset internal/workspaces internal/domain internal/config internal/httpx internal/gen internal/specs internal/store internal/recipes internal/openapi internal/auth cmd go.mod go.sum scripts Makefile README.md'

// ---------------------------------------------------------------------------
// Signature blocks. Pasted VERBATIM into the agent that writes each package AND
// into every agent that calls it. A contract left as an optional field of a
// report schema is a contract two agents will guess differently, and two
// adjacent same-typed parameters swap silently and still compile.
// ---------------------------------------------------------------------------

const SIG_WHEN = `  // NEW FILE internal/overrides/when.go, inside the EXISTING package overrides.
  // The Condition type already exists (internal/overrides/overrides.go, "type
  // Condition struct" with fields In, Name, Op, Value) and does not change;
  // this file is what finally gives it meaning. Imports: net/http, net/url,
  // strconv, strings, sort, encoding/json and the stdlib only.

  // Input is one request as the evaluator sees it. mockplane builds it ONCE per
  // request and passes the SAME value to every candidate variant.
  type Input struct {
      Query  url.Values  // r.URL.Query()
      Header http.Header // r.Header — look names up with Header.Get, which canonicalises
      Body   any         // the decoded JSON body: map[string]any, []any or a scalar; nil when there is none
      BodyOK bool        // false when the body was absent, unparsable OR TRUNCATED; every
                         // in:"body" condition then evaluates FALSE — never a match on a partial document
  }

  // Match reports whether one condition holds for in.
  func (c Condition) Match(in Input) bool

  // MatchAll reports whether EVERY condition holds (AND). An EMPTY list returns
  // FALSE, not true: a variant with no when[] is the fallback, never a candidate.
  func MatchAll(conds []Condition, in Input) bool

  // SelectWhen returns the responses key of the first variant whose when[] is
  // non-empty and matches, iterating the keys in ASCENDING NUMERIC order.
  func SelectWhen(responses map[string]Variant, in Input) (status string, ok bool)

  // ValidateConditions returns a non-nil error for a condition this build
  // cannot evaluate: an unknown In or Op, an empty Name, or a missing Value on
  // equals/contains. Put/PutMany wrap it in ErrInvalidRow.
  func ValidateConditions(conds []Condition) error

  // ValidateVariant is the EXISTING unexported validateVariant, exported
  // unchanged. Keep the unexported name as a one-line caller into the exported
  // one — do NOT rename it away: internal/overrides/repo.go:460 calls
  // validateResponses and that file is not yours. Either way there stays exactly
  // ONE implementation. internal/customep stores the
  // same Variant JSON in an adjacent table and is forbidden to write a second,
  // drifting validator; this is what it calls. It must therefore exist before
  // that package is written, which is why the when-evaluator runs first and
  // alone.
  func ValidateVariant(v Variant) error

  // ValidateResponses validates a whole responses map: every key is a 3-digit
  // status and every value passes ValidateVariant. Same reason — customep's
  // responses column is the same shape as op_overrides.responses.
  func ValidateResponses(responses map[string]Variant) error

  // SEMANTICS, fixed here so three agents cannot each pick their own:
  //   In:  "query" | "header" | "body". Anything else NEVER matches.
  //   Op:  "equals" | "contains" | "exists". Anything else NEVER matches.
  //   Name: a query parameter name (case-sensitive); a header name
  //         (case-INsensitive, via http.Header.Get); or a TOP-LEVEL field name
  //         of a JSON object body — no dot paths and no array indexes in this
  //         slice (DESIGN §19 line 1111 asks for equality on ONE field).
  //   equals:   the value rendered as a string equals Value byte for byte.
  //   contains: strings.Contains over the same rendering.
  //   exists:   the name is present at all — a query key with any value
  //             (including empty), a header that is set, a body key that is
  //             present even when its value is null.
  //   Rendering, and it is the whole of the number question: string -> itself;
  //     json.Number -> its own String(); float64 -> strconv.FormatFloat(f,'f',-1,64)
  //     so JSON 1, 1.0 and 1.00 all render "1"; bool -> "true"/"false"; null,
  //     objects and arrays NEVER match anything, not even exists=false.
  //   A query parameter with several values matches when ANY value matches.`

const SIG_LIVESTATE = `  // package livestate  (internal/livestate) — A LEAF: the stdlib only. It must
  // NEVER import internal/store, internal/workspaces or database/sql. DESIGN §4
  // and §12: everything here changes on EVERY request and must never reach
  // SQLite and never bump workspaces.revision.

  type Action string
  const (
      ActionStatus Action = "status" // answer Status instead, until cleared
      ActionFail   Action = "fail"   // answer Status for the next N requests, or exactly once
  )
  // DESIGN §14 also lists "delay" and "pause". Both are P2 (§19 line 1112) and
  // this slice refuses them at the HTTP boundary with 501 rather than storing
  // them — see the run log for why pause in particular is not a free extra.

  // Target addresses one operation, or every operation in the workspace.
  type Target struct {
      All    bool   // the "*" target
      Method string // upper case; ignored when All
      Path   string // RELATIVE path, no base path — byte-identical to op_overrides.path; ignored when All
  }

  // Directive is one stored instruction. N is the REMAINDER, not the original.
  type Directive struct {
      Target Target
      Action Action
      Status int  // 100..599; required for both actions
      Once   bool // ActionFail only: fire exactly once
      N      int  // ActionFail: how many requests are still to fail; Once implies 1
      SetAt  time.Time
  }

  // Effect is what the serving path must do for ONE request.
  type Effect struct {
      Status int // 0 means nothing is forced
  }

  type Store struct{ /* unexported; safe for concurrent use */ }

  const DefaultTTL = time.Hour
  // MaxDirectivesPerWorkspace bounds the map: POST {prefix}/state is
  // UNAUTHENTICATED by design (§12 "переключение из тестов", §15 "мок открыт"),
  // so an open endpoint that grows a map forever is a memory leak with a URL.
  const MaxDirectivesPerWorkspace = 64

  func NewStore(ttl time.Duration, now func() time.Time) *Store // ttl<=0 -> DefaultTTL; now nil -> time.Now
  func (s *Store) Set(workspaceID int64, d Directive) error     // replaces the directive with the same (Target, Action)
  func (s *Store) List(workspaceID int64) []Directive           // snapshot in a STABLE order: "*" first, then method+path; N as it is NOW
  func (s *Store) Clear(workspaceID int64) int                  // returns how many directives were dropped
  func (s *Store) Apply(workspaceID int64, method, path string) Effect // CONSUMES: decrements N and drops the directive at zero
  func (s *Store) Sweep(now time.Time) int                      // drops workspaces untouched for longer than ttl; returns how many

  var ErrInvalidDirective = errors.New("livestate: invalid directive")
  var ErrTooManyDirectives = errors.New("livestate: too many directives for this workspace")

  // THE WIRE SHAPE IS THIS PACKAGE'S, NOT THE HANDLERS'. Two handlers accept
  // the identical JSON — POST {prefix}/state on the mock plane and POST
  // /api/workspaces/{id}/session on the admin plane — and if each writes its
  // own DTO they will disagree about a field name, a default or the "*" union
  // within a week. So Directive carries json tags and Target implements
  // MarshalJSON/UnmarshalJSON for the union, here, once; both handlers decode
  // straight into livestate.Directive.
  //
  //   {"target":{"method":"POST","path":"/auth/login"} | "*",
  //    "action":"status"|"fail","status":503,"once":false,"n":0,
  //    "setAt":"2026-08-18T12:00:00Z"}
  //
  // Directive ALSO carries one field it never stores:
  //     Scenario json.RawMessage  json:"scenario,omitempty"
  // DESIGN §12 line 534's scenario switch is a TOP-LEVEL KEY, not an action, and
  // scenarios are P2. Without this field encoding/json drops the key silently and
  // both planes answer "400 unknown action" instead of the 501 that names P2 — a
  // handler cannot 501 for a key it never sees. It is decoded, answered 501, and
  // never stored. Test for PRESENCE carefully: json.RawMessage keeps the literal
  // "null", so {"action":"status","scenario":null} gives len(Scenario)==4 and a
  // bare length check would 501 an otherwise valid directive.
  //
  // "n" going OUT is the REMAINDER, never the original (DESIGN §12: the UI
  // reads the counter from the same place the router does). Target marshals as
  // the string "*" when All is true and as the object otherwise; unmarshalling
  // accepts both and rejects anything else with ErrInvalidDirective.
  // ErrTooManyDirectives is answered 409 by BOTH planes (httpx.CodeConflict on
  // the admin side) — pinned here so the two do not pick 400 and 429.

  // PRECEDENCE inside Apply, fixed here: an exact (method, path) directive
  // beats the "*" one; on the same target, a fail directive with N>0 beats a
  // status directive. Apply must be cheap on the overwhelmingly common path
  // where a workspace has no directives at all — one read-locked map lookup and
  // a zero Effect.`

const SIG_TRAFFIC = `  // package traffic  (internal/traffic) — imports internal/store, net/http and
  // the stdlib. It must NEVER import internal/mockplane, internal/authpreset or
  // internal/workspaces: the caller decides WHAT to record, this package decides
  // how to redact, batch and store it.

  // Event is one request as the plane observed it. Bodies arrive already cut to
  // whatever the plane captured; this package cuts them again to Options.MaxBody
  // and redacts them BEFORE anything is buffered (DESIGN §15).
  type Event struct {
      WorkspaceID    int64
      TS             time.Time
      Method         string
      Path           string      // as requested, query stripped, WITH the workspace base path
      PeerIP         string      // httpx.Peer's immediate peer, always recorded (§15)
      FwdIP          string      // the forwarded client, empty unless MOCKER_TRUST_PROXY says otherwise
      MatchedKind    string      // "operation" | "custom" | "none"
      MatchedID      int64       // operations.id, custom_endpoints.id, or 0 for "none"
      Status         int
      Duration       time.Duration
      ReqHeaders     http.Header
      ReqBody        []byte
      RespBody       []byte
      SuppressBodies bool        // an auth path: neither body is stored at all (§15)
      Truncated      bool        // the CALLER already cut one of the bodies
      Notes          string
  }

  type Options struct {
      MaxBody    int64         // cfg.TrafficMaxBody; <=0 -> DefaultMaxBody
      Retention  int           // cfg.TrafficRetention; <=0 -> DefaultRetention
      Queue      int           // <=0 -> DefaultQueue
      Batch      int           // <=0 -> DefaultBatch
      FlushEvery time.Duration // <=0 -> DefaultFlushEvery
  }

  type Recorder struct{ /* unexported */ }
  func NewRecorder(db *store.DB, log *slog.Logger, opts Options) *Recorder
  func (rec *Recorder) Record(ev Event)                 // NEVER blocks; drops and counts when the queue is full
  func (rec *Recorder) Run(ctx context.Context)         // the single writer goroutine; flushes once more and returns when ctx ends
  func (rec *Recorder) Flush(ctx context.Context) error // forces everything queued out and waits for it
  func (rec *Recorder) Dropped() int64

  // Row is one stored request, as the admin API hands it out.
  type Row struct {
      ID          int64             \`json:"id"\`
      TS          time.Time         \`json:"ts"\`
      Method      string            \`json:"method"\`
      Path        string            \`json:"path"\`
      PeerIP      string            \`json:"peerIp,omitempty"\`
      FwdIP       string            \`json:"fwdIp,omitempty"\`
      MatchedKind string            \`json:"matchedKind"\`
      MatchedID   *int64            \`json:"matchedId,omitempty"\`
      Status      int               \`json:"status"\`
      DurationMS  float64           \`json:"durationMs"\`
      ReqHeaders  map[string]string \`json:"reqHeaders,omitempty"\`
      ReqBody     string            \`json:"reqBody,omitempty"\`
      RespBody    string            \`json:"respBody,omitempty"\`
      Notes       string            \`json:"notes,omitempty"\`
      Truncated   bool              \`json:"truncated"\`
  }

  type Repo struct{ /* unexported */ }
  func NewRepo(db *store.DB) *Repo
  func (r *Repo) List(ctx context.Context, workspaceID int64, limit int) ([]Row, error)                  // NEWEST first
  func (r *Repo) Since(ctx context.Context, workspaceID, sinceID int64, limit int) ([]Row, error)        // id > sinceID, OLDEST first
  func (r *Repo) Get(ctx context.Context, workspaceID, id int64) (*Row, error)
  func (r *Repo) Clear(ctx context.Context, workspaceID int64) (int64, error)
  func (r *Repo) Rate1m(ctx context.Context, workspaceID int64, now time.Time) (int, error)
  var ErrNotFound = errors.New("traffic: row not found")

  // Redaction, exported because it is the security property and deserves its own tests:
  const RedactedValue = "[redacted]"
  func RedactHeaders(h http.Header) map[string]string      // authorization, cookie, set-cookie, x-api-key, proxy-authorization -> RedactedValue
  func RedactJSONBody(body []byte) (out []byte, changed bool) // password/token/secret/*_key/*_token field VALUES -> RedactedValue, at any depth

  // truncated is ONE column for TWO bodies (migrations/0001_init.sql). It means
  // "at least one body was cut", and Notes says which.
  //
  // NOTES IS A PINNED TOKEN SET, not free text, because a reader one package
  // away has to act on it: the admin to-override conversion must REFUSE a row
  // whose body was cut or redacted, and the frozen schema has no column for
  // either. Notes is a comma-separated list: the recorder's own tokens FIRST,
  // then any caller free text from Event.Notes. The tokens are:
  //   const (
  //       NoteRedacted     = "redacted"      // a secret was replaced in at least one body
  //       NoteTruncatedReq = "truncated:req"
  //       NoteTruncatedRsp = "truncated:resp"
  //       NoteSuppressed   = "suppressed"    // an auth path: no body stored at all
  //       NoteDroppedPrefix = "dropped:"   // + a decimal count: "dropped:12"
  //   )
  //   func (r Row) HasNote(token string) bool  // exact token match, never a substring scan
  //   func (r Row) Redacted() bool             // HasNote(NoteRedacted) || HasNote(NoteSuppressed)
  //   func (r Row) DroppedBefore() int         // 0 when there is no dropped: token
  // RedactJSONBody's "changed" return is what sets NoteRedacted; discarding it
  // at record time is what makes the conversion's refusal impossible.

  // The defaults, pinned rather than left to taste, because a test and a shell
  // script both have to wait for them:
  //   DefaultQueue = 1024, DefaultBatch = 64, DefaultFlushEvery = 500ms,
  //   DefaultMaxBody = 8 KiB, DefaultRetention = 1000, retention pruned every
  //   256 inserts.
  // DefaultFlushEvery must stay at or under one second: scripts/smoke.sh polls
  // for traffic from shell, where Flush is not reachable, and its bounded
  // retry ceiling is written against this number.
  //
  // Event.TS's own doc comment states the epoch UNIT (seconds or milliseconds)
  // chosen for the traffic.ts column, and insert, Since and Rate1m all use it.
  // Do NOT put that note in the migration file: the schema is frozen and no
  // agent in this run owns it.`

const SIG_CUSTOMEP = `  // package customep  (internal/customep) — imports internal/store,
  // internal/overrides (for the Variant type — the responses JSON is the SAME
  // shape as op_overrides.responses and must not be parsed twice by two
  // packages) and internal/router (for CanonicalPath). internal/overrides must
  // NEVER import this package: the dependency is one-way, or it is a cycle.

  type Row struct {
      ID            int64
      WorkspaceID   int64
      Method        string // upper case
      Path          string // RELATIVE, leading slash, no base path
      CanonicalPath string // router.CanonicalPath(Path) — computed here, stored, never recomputed at match time
      SourceOrder   int64
      OverrideOn    bool
      RouteOff      bool
      ActiveStatus  int                           // NOT NULL in the schema, default 200
      Responses     map[string]overrides.Variant  // key: the status as a decimal string
      ReqSchema     json.RawMessage               // PRESERVED ONLY — request validation is P2
      ListSize      *overrides.ListSize
      DelayMs       *int
      FailDirective json.RawMessage               // PRESERVED ONLY — P2
      ValidateReq   *bool
      CreatedAt     time.Time
      UpdatedAt     time.Time
  }

  type Repo struct{ /* unexported */ }
  func NewRepo(db *store.DB) *Repo
  func (r *Repo) ForWorkspace(ctx context.Context, workspaceID int64) ([]*Row, error) // ordered by source_order, then id
  func (r *Repo) Get(ctx context.Context, workspaceID, id int64) (*Row, error)
  func (r *Repo) Create(ctx context.Context, workspaceID int64, row *Row) (*Row, error)
  func (r *Repo) Delete(ctx context.Context, workspaceID, id int64) error

  var ErrNotFound = errors.New("customep: endpoint not found")
  var ErrConflict = errors.New("customep: an endpoint already exists for this method and canonical path")
  var ErrInvalidRow = errors.New("customep: invalid endpoint")
  var ErrWorkspaceNotFound = errors.New("customep: workspace not found")

  // Create and Delete BOTH bump workspaces.revision inside their OWN
  // transaction, exactly the way internal/overrides/repo.go does it (find
  // bumpRevisionTx there and follow it). They must NOT call
  // workspaces.Repo.Update: that opens a second db.Write on a one-connection
  // writer pool and deadlocks the process. Without the bump the route cache,
  // keyed (workspace_id, revision), never rebuilds and a created endpoint 404s.
  //
  // SourceOrder is assigned by Create as max(source_order)+1 for the workspace,
  // inside the same transaction: SQLite row order is not stable across scans and
  // DESIGN §8 rule 4 makes source_order the final tie-break.
  //
  // Create refuses (ErrConflict) another row with the same (method,
  // canonical_path) — the UNIQUE index at migrations/0001_init.sql:211 (:210 is
  // the literal-path one). A custom endpoint canonically equal to a SPEC
  // operation is NOT a conflict: DESIGN §8 calls that an override and it is the
  // point of the feature. The cross-table rule — an op_overrides row and a
  // custom endpoint on the same (method, path) is forbidden — is checked in
  // internal/admin, which is the only layer that holds both repos.
  //
  // Every Variant goes through overrides.ValidateVariant / ValidateResponses —
  // the same single implementation op_overrides writes through. This package
  // does not re-implement any part of it.
  //
  // OverrideOn has the same meaning it has for op_overrides: false means the
  // row exists and is INERT. An override_on=false custom endpoint is left out
  // of the route table entirely, so the route 404s (or falls through to the
  // spec operation it was shadowing) exactly as if the row were not there.`

const SIG_PLANE = `  // internal/mockplane — the three new setters and the ONE new router field.
  // Every setter follows SetOverrides exactly (plane.go): call it ONCE at
  // startup, after New and before the first request; the field is written once
  // and only read afterwards, with no lock, and calling it concurrently with
  // ServeHTTP is a data race that go test -race will correctly catch.

  func (p *Plane) SetLiveState(src LiveStateSource)
  func (p *Plane) SetTraffic(sink TrafficSink)
  func (p *Plane) SetCustomEndpoints(src CustomSource)

  // LiveStateSource is *livestate.Store as the plane needs it. The plane calls
  // Apply on the serving path and the other three only from {prefix}/state.
  type LiveStateSource interface {
      Apply(workspaceID int64, method, path string) livestate.Effect
      Set(workspaceID int64, d livestate.Directive) error
      List(workspaceID int64) []livestate.Directive
      Clear(workspaceID int64) int
  }

  // TrafficSink is *traffic.Recorder as the plane needs it: fire and forget.
  type TrafficSink interface{ Record(ev traffic.Event) }

  // CustomSource is *customep.Repo as the plane needs it.
  type CustomSource interface {
      ForWorkspace(ctx context.Context, workspaceID int64) ([]*customep.Row, error)
  }

  // A nil source means exactly what a nil OverrideSource means today: the
  // feature is absent and every request is served precisely as HEAD serves it.

  // internal/router/router.go, Route gains ONE field — paste this comment with it:
  //   // CustomRowID is custom_endpoints.id for a custom endpoint and 0 for a
  //   // spec operation. OpRowID is the mirror image (0 for a custom route), and
  //   // respond.go keys the declared response variants by OpRowID — so without
  //   // a separate id every custom route in a workspace would collide on key 0
  //   // and traffic's matched_id would have nothing to record.
  //   CustomRowID int64`

const SIG_WIRE = `  // THE HTTP SURFACE THIS SLICE ADDS. Written out once, here, because three
  // agents implement halves of it and the acceptance test asserts on it.
  //
  // ON THE WORKSPACE HOST, under MOCKER_RESERVED_PREFIX (default /__mocker),
  // UNAUTHENTICATED like the rest of the mock plane (DESIGN §12: "переключение
  // доступно из UI и из тестов"). None of these three are recorded in traffic.
  //
  //   POST {prefix}/state
  //     {"target": {"method":"POST","path":"/auth/login"}, "action":"status", "status":503}
  //     {"target": "*", "action":"fail", "status":500, "n":2}
  //     {"target": "*", "action":"fail", "status":500, "once":true}
  //     -> 200 {"workspace":"<slug>","directives":[...]}          the FULL list after the write
  //     -> 501 {"error":{"code":"not_implemented_yet",...}}       for "scenario" (P2), "delay" or "pause" (P2)
  //     -> 409 {"error":{"code":"conflict",...}}                  for livestate.ErrTooManyDirectives
  //     Two error codes are NOT in httpx's Code* set and are spelled here once so
  //     the two planes cannot diverge: "not_implemented_yet" (already used at
  //     internal/mockplane/plane.go:226) and "service_unavailable" (the admin
  //     503 when no live-state store was wired). Everything else uses a Code*.
  //     -> 400 for an unknown action, a status outside 100..599, n<=0 with action "fail" and no "once"
  //     -> 429-free: there is no rate limit on this plane by §18; MaxDirectivesPerWorkspace is the bound
  //   GET    {prefix}/state -> 200 {"workspace":"<slug>","directives":[...]}
  //   DELETE {prefix}/state -> 200 {"workspace":"<slug>","cleared":<n>}
  //   GET/DELETE are an addition to §14's single POST, logged as one: a test that
  //   can force a status but not clear it leaves the next test running against it.
  //
  //   A directive on the wire, in both directions:
  //     {"target":{"method":"POST","path":"/auth/login"}|"*","action":"status"|"fail",
  //      "status":503,"once":false,"n":0,"setAt":"2026-08-18T12:00:00Z"}
  //   "n" going OUT is the REMAINDER (DESIGN §12: the UI reads the counter from
  //   the same place the router does).
  //
  // ON THE ADMIN PLANE (session cookie + CSRF on every state-changing method,
  // exactly like every existing /api route):
  //
  //   GET    /api/workspaces/{id}/session            -> 200 {"directives":[...]}
  //   POST   /api/workspaces/{id}/session            same body as {prefix}/state -> 200 {"directives":[...]}
  //   DELETE /api/workspaces/{id}/session            -> 200 {"cleared":<n>}
  //
  //   GET    /api/workspaces/{id}/traffic?limit=     -> 200 {"rows":[...newest first...],"rate1m":<n>,"dropped":<n>}
  //   GET    /api/workspaces/{id}/traffic/poll?since=<id>&limit=
  //                                                  -> 200 {"rows":[...oldest first...],"lastId":<n>,"dropped":<n>}
  //          "since" is a traffic ROW ID, never a timestamp: traffic_ws is indexed
  //          (workspace_id, id DESC) and ts ties inside a millisecond. When there
  //          are no new rows, lastId is the "since" it was GIVEN — returning 0
  //          would make a chained poller replay the whole table on every quiet
  //          tick. A missing or negative "since" is 0; limit has a default (100)
  //          and a ceiling (500), and a caller asking for more gets the ceiling,
  //          not an error. rate1m is reported by GET /traffic only, not by poll.
  //   DELETE /api/workspaces/{id}/traffic            -> 200 {"deleted":<n>}
  //          It FLUSHES the recorder first, or the next flush resurrects rows the
  //          operator just cleared.
  //
  //   GET    /api/workspaces/{id}/endpoints          -> 200 {"endpoints":[...]}
  //   POST   /api/workspaces/{id}/endpoints          {"method","path","status","body","mediaType"} -> 201 {endpoint}
  //   DELETE /api/workspaces/{id}/endpoints/{eid}    -> 204
  //          PUT is P2 (§14 screen 6, the editor).
  //
  //   POST   /api/workspaces/{id}/traffic/{tid}/to-override
  //     -> 200 {"opKey":"<method>%20<encoded path>","status":<n>,"revision":<n>}
  //     Turns one observed RESPONSE into a pinned edit on the operation it hit.
  //     THE KEY IS THE OPERATION'S OWN TEMPLATE PATH, NOT THE REQUESTED PATH,
  //     and this is the single most dangerous line in this file. The mock plane
  //     looks an override up as overrides.OpKey(route.Method, route.Path), and
  //     router.Route.Path is "exactly as stored in operations.path" — the
  //     TEMPLATE, "/widgets/{widgetId}". A traffic row holds the CONCRETE path
  //     the client asked for, with the base path glued on: "/api/v1/widgets/7".
  //     Stripping the base path off that yields "/widgets/7", which no route
  //     will ever produce as a key: the row lands orphaned, the merged
  //     operations view never shows it, and the operator's click does nothing —
  //     while a test written against "/widgets/7" passes, and passes VACUOUSLY
  //     on any path with no {param} at all. So: resolve traffic.matched_id ->
  //     the operations row -> its Path, and key on that. No base-path stripping
  //     is involved on this route, because operations.path is already relative.
  //     -> 409 when the row's matched_kind is not "operation" (nothing to pin
  //        to), when the operation is gone or belongs to a different spec than
  //        the workspace's own (a re-import orphaned matched_id), or when
  //        Row.Redacted() or Row.Truncated is true: a pinned body assembled
  //        from a cut or redacted body ships the operator a lie, and this
  //        handler is the only place that can tell.
  //     The write sets OverrideOn=true explicitly — Repo.Put defaults it true
  //     only for a NEW row, so pinning onto an existing switched-off row would
  //     otherwise store an inert body.
  //   POST   /api/workspaces/{id}/traffic/{tid}/to-endpoint
  //     -> 201 {"id":<n>,"method":"...","path":"...","revision":<n>}
  //     Turns one observed REQUEST (typically a 404, matched_kind "none") into a
  //     custom endpoint. THE STATUS RULE IS PINNED: an observed 404 becomes a 200
  //     carrying the observed body, or {} when there was none — the operator is
  //     creating the endpoint precisely because it was missing, so re-serving the
  //     404 would be a no-op; ANY OTHER observed status is preserved as it was.
  //     -> 409 when an op_overrides row already exists for that (method, path)
  //        (DESIGN §8) or a custom endpoint already holds that canonical path.
  //
  //   THE TWO CONVERSIONS RESOLVE THEIR KEY DIFFERENTLY, and this is the single
  //   seam where the two path spellings meet:
  //     to-endpoint  STRIPS ws.Settings.BasePath from traffic.path — a custom
  //                  endpoint is the literal route somebody asked for, so
  //                  "/api/v1/legacy/ping" becomes "/legacy/ping". If the row's
  //                  path does not start with the CURRENT base path (it changed
  //                  after the request was recorded), refuse with 409 rather than
  //                  guess.
  //     to-override  STRIPS NOTHING. It resolves traffic.matched_id to the
  //                  operations row and uses THAT row's Path — the template
  //                  "/widgets/{widgetId}", which is the only key the mock
  //                  plane's lookup can ever produce.
  //   Both live in the admin handler, the only layer holding ws.Settings and both
  //   repos.`

// ---------------------------------------------------------------------------
// Shared context blocks.
// ---------------------------------------------------------------------------

const CTX_CORE = `You are building phase P1c (SLICE 2) of "mocker", a self-hosted mock-backend service written in Go.
Repo root is your CWD: /home/yakov/projects/mocker. Module path git.sumka.site/yakov/mocker, Go 1.26.
HEAD is commit 5cc357f — P0, P1a, P1b and P1c slice 1, all committed, reviewed and green. Everything you
see in "git diff HEAD" is this run's own work.

WHAT ALREADY EXISTS, so you do not rebuild it: HTTP skeleton, SQLite store with two pools and migrations,
shared-password login with sessions and CSRF, workspaces, host and path routing, spec import with a lazy
$ref resolver, the response generator with a layered seed and the list contract, CORS and preflight, the
recipes engine, op_overrides (route_off, active_status, pinned bodies, per-status recipes) and the auth
preset. A frontend can already log in against a mock.

RUNTIME DEPENDENCIES, all of them: modernc.org/sqlite (pure-Go driver), golang.org/x/sync (singleflight),
golang.org/x/crypto (argon2 for the admin password). github.com/santhosh-tekuri/jsonschema/v6 is TEST-ONLY
and must stay that way — "go list -deps ./cmd/mocker | grep santhosh" must print nothing.

HARD RULES — all six, all the time:
1. FILE OWNERSHIP. Write only the files your own task lists. Another agent owns everything else, and two
   agents editing one file is how this project loses an hour.
2. NO NEW RUNTIME DEPENDENCY. go.mod and go.sum must come out of this run unchanged. No JSON-diff library,
   no router, no logging framework, no JWT library.
3. THE SQLITE SCHEMA IS FROZEN. internal/store/migrations/0001_init.sql already creates every table this
   slice needs — traffic at line 228, custom_endpoints at 191, scenarios at 120. No new migration, no
   ALTER, no column added, no index added. If a column seems missing, you have misread the schema.
4. NEVER READ THE BIG FILES WHOLE. DESIGN.md is 87 KB of Russian — read the section you need by line range
   (index below). ${SPEC_PATH} is 347 KB and gitignored — never Read or cat it; query it with jq only.
5. NO NESTED db.Write. store.DB's writer pool is SetMaxOpenConns(1). Calling a repo method that opens its
   own db.Write from inside another db.Write deadlocks the whole process, not just that request. Anything
   that must happen with a write goes into the SAME transaction (see bumpRevisionTx in
   internal/overrides/repo.go for the pattern this project already uses).
6. NO DIRECTIVE, NO CONDITION, NO CUSTOM ENDPOINT MUST MEAN BYTE-IDENTICAL OUTPUT. A workspace with no
   livestate directive, no when[] on any variant and no custom endpoint must serve exactly what HEAD
   serves today, byte for byte. THE GUARD THAT CAN CATCH YOU IS THE EXISTING TEST CORPUS, not the golden:
   internal/mockplane/{respond,plane,routes,runtime}_test.go assert today's serving behaviour in detail,
   they were reviewed one slice ago, and they must keep passing UNMODIFIED. Deleting an assertion,
   loosening a comparison, or "adapting a fixture" so a new branch fits is the failure this rule exists to
   prevent, and every gate diffs those four files for exactly that. Adding a test is always fine.
   (internal/gen/testdata/p1b_body_hashes.json — 419 hashes under its "bodies" key — remains internal/gen's
   own tripwire: it hashes gen.Body directly, upstream of everything this slice touches, so it proves the
   generator was not edited and nothing more. It is WRITE-ONCE: never run MOCKER_REGENERATE_GOLDEN=1, never
   edit it, and "git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json" must stay clean.)

DESIGN.md section index (line ranges, so you never open it whole):
  §4 layers 98-135 · §5 reimport 113-135 · §6 request path 136-174 · §7 import 175-245 · §8 router 246-296
  §9 generator 297-411 · §10 auth 412-471 · §11 resources 472-508 · §12 runtime/session/when 509-556
  §13 SQLite schema 557-820 · §14 admin API and screens 821-916 · §15 threat model 917-982
  §16 deployment 983-1062 · §18 load 1088-1105 · §19 phases 1106-1128
DESIGN.md is the authority. Where this prompt and DESIGN disagree, say so in "deviations" rather than
silently picking one.

STYLE, non-negotiable because the reviewers read for it: this codebase explains WHY in comments, not what.
Match the density and the voice of the file you are editing. Every non-obvious decision gets a comment
that would stop the next reader from "fixing" it. Table-driven tests, t.Parallel where it is safe,
never a sleep in a test.

FINISH GREEN, and paste the real output into "verified":
    test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
IF YOUR TASK SAYS YOU RUN CONCURRENTLY WITH OTHER AGENTS, scope those four to the directories you own
(test -z "$(gofmt -l internal/yours)", go build/test ./internal/yours/...) and run the repo-wide four ONCE
at the end: a sibling's half-written package makes the repo-wide bar fail for a reason you cannot fix, and
"gofmt -w ." over another agent's file is how a parallel phase corrupts itself. A repo-wide failure in a
package you do not own goes into "deviations" — never into an edit.
Never weaken, skip or delete a test to get there.`

const CTX_SERVE = `
THE SERVING PATH AS IT IS TODAY — read these before you touch anything, they are short:
  internal/mockplane/plane.go     — ServeHTTP/ServeSlug/serveResolved, the reserved prefix, NormalizeSegments
  internal/mockplane/routes.go    — the runtime cache (keyed workspace_id + revision), serveRoute, serveNoRoute
  internal/mockplane/runtime.go   — buildRuntime: normalized document, resolver, generator, variants, routes, overrides
  internal/mockplane/respond.go   — serveGenerated, the whole response assembly
  internal/mockplane/overrides.go — lookupOverride and lookupRecipes, and WHY the key is route.Path
  internal/router/router.go       — Route, Build, Match, CanonicalPath, Conflicts

THE ORDER, WHICH THREE AGENTS IMPLEMENT PIECES OF. serveResolved runs: workspace resolve, CORS, preflight,
reserved prefix (step 4), route table (step 5). Inside serveGenerated, after the override row is looked up:
  1. route_off -> the existing 404 via serveNoRoute. It consumes NOTHING: no livestate counter, no
     directive. A disabled route is GONE, and a forced status on it would let a client distinguish a
     disabled route from a missing one — exactly what that 404's shape exists to prevent.
  2. livestate.Apply(ws.ID, route.Method, route.Path) — ONCE per request, and the only place a fail
     counter is consumed. Skip the call entirely when no LiveStateSource is wired.
  3. the delay, unchanged: effectiveDelayMs(...) through awaitDelay/clampedDelay, capped at 30s.
  4. THE STATUS, in this order and no other:
       a. the livestate Effect.Status, when non-zero — DESIGN §4 puts Session above Workspace;
       b. else, ONLY WHEN overrideActive, overrides.SelectWhen(row.Responses, in) — the first matching
          when[] by ascending status. The gate is not optional: respond.go's own comment establishes that
          "every P1c branch below gates on overrideActive (never merely hasRow) so an OverrideOn=false row
          is inert end to end", and a when[] firing from a switched-off row breaks that silently —
          TestServeGenerated_OverrideOnFalse_IsInert has no When in its fixture, so nothing catches it;
       c. else row.ActiveStatus;
       d. else chooseVariant(rt.variants[route.OpRowID]) — the document's own choice, untouched.
     Then the EXISTING variant resolution runs on whatever status came out: variantForStatus, and the
     synthetic no-body variant when the document declares no response for it (that code is already there
     and already logs a warning — reuse it, do not write a second one).
  5. everything downstream keys off the FINAL status exactly as it does now: row.Responses[status],
     lookupRecipes(route, status), pinned bodies, media type, headers, envelope. Get this backwards and
     "pinned" silently stops firing on every operation whose status was forced.

TRAPS THIS PATH HAS ALREADY SPRUNG ON SOMEBODY:
- rt.overrides and rt.recipeSets are nil when no OverrideSource was wired, and every branch checks
  overrideActive (not merely "a row exists") so an OverrideOn=false row is inert end to end. New sources
  follow the same shape.
- The runtime is cached by (workspace_id, revision). Anything that must change what is SERVED has to bump
  workspaces.revision, or it will not be seen until something else does.
- HEAD is matched as GET (router.Match resolves it) and answers with no body.
- setSafeHeader is the ONLY way a header value reaches the wire; pinned values are user input.`

const CTX_ADMIN = `
THE ADMIN PLANE AS IT IS TODAY:
  internal/admin/server.go            — Server, New(cfg, sessions, ws, db, log), Handler() and its middleware
                                        chain, decodeJSON, parseWorkspaceID
  internal/admin/security.go          — requireUser, attachSession, enforceCSRF, securityHeaders, the login
                                        rate limiter (login only — DESIGN §18 keeps rate limits off the mock plane)
  internal/admin/override_handlers.go — loadWorkspace, the merged operations view, GET/PUT/DELETE by opKey
  internal/admin/preset_handlers.go   — the auth-preset preview and apply
Server builds specsRepo and overridesRepo INTERNALLY from db, precisely because New's signature is shared
with cmd/mocker/main.go, which this package does not own. Repos over the database follow that rule. The
live-state store does NOT: it is RAM shared with the mock plane, so it arrives through a setter and the
same instance must reach both planes.
Routes are registered with Go 1.22 mux patterns ("GET /api/workspaces/{id}/traffic"). Every state-changing
method already goes through CSRF; nothing new may live outside /api/.
Errors go out through httpx.Err with an httpx.Code* constant; bodies through httpx.JSON.`

const CTX_TEST = `
TESTS — the conventions this repo already holds:
- internal/testspec resolves the real acceptance document and SKIPS (never fails) when it is absent. A test
  that needs it uses testspec.Bytes(t). Report every SKIP your run prints.
- internal/server/*_test.go is package server_test and builds the whole stack over a temp SQLite file. It
  ALREADY declares: testConfig, testLogger, buildStack, buildStackWithSpecs, buildP1cStack, do, jsonRequest,
  login, fakePlane, p1cInlineSpec, p1cWorkspaceView, p1cLoginResponse, p1cRequestLogin, p1cDecodeJWT,
  p1cAssertToken, TestP1c_FrontendLogsIn. Anything you add to that package MUST carry a p1c2 prefix, and you
  may not redeclare any of the above.
- Nothing in a test may sleep to wait for asynchronous work. The traffic recorder exposes Flush(ctx) for
  exactly that reason; a bounded retry loop with a stated ceiling is acceptable only where Flush cannot
  reach, and a bare time.Sleep is not.
- Table-driven where there is more than one case. t.Parallel where the test owns its own database.
- internal/mockplane holds TWO test packages: "package mockplane" (respond_test.go, runtime_test.go, for
  unexported helpers) and "package mockplane_test" (plane_test.go, routes_test.go). A NEW test file goes
  in mockplane_test unless it must reach an unexported symbol. The directory already declares ~30 helpers
  including testConfig, newPlane, decodeJSON, runtimeTestConfig and widgetsSource — run
  "rg -n '^func [a-z]' internal/mockplane/*_test.go" before you name anything, and prefix what you add.
- internal/admin tests are "package admin_test". Its harness (newTestServer in admin_test.go) returns a
  handler and does NOT expose the *admin.Server, so anything needing a setter writes its own p1c2-prefixed
  builder rather than editing admin_test.go.
- THE EXISTING TESTS OF internal/mockplane ARE THIS SLICE'S REGRESSION GUARD (HARD RULE 6). Adding tests
  is always fine; changing an existing assertion, fixture expectation or comparison in respond_test.go,
  plane_test.go, routes_test.go or runtime_test.go is a blocker unless your own task explicitly names that
  test as yours to update. If one of them fails, the code is wrong, not the test.`

const INTENDED = `
WHAT IS DELIBERATELY NOT HERE (so you do not report a gap as a defect):
- livestate actions "delay" and "pause" — P2 (DESIGN §19 line 1112). The API answers 501 and names P2.
- op_overrides.fail_directive and Variant.SchemaPatch — stored, round-tripped, never interpreted. P2.
- PUT on a custom endpoint, the schema-tree editor and POST /api/workspaces/:id/preview — P2.
- Scenarios: POST {prefix}/state with a "scenario" key answers 501. The scenarios table stays empty.
- The «Подключить» panel's server-side health probe — P1d, and it would be the first outbound HTTP client
  in this tree, which needs §15's allowlist rules and its own slice. The workspace's external URL IS
  exposed by this slice, so the browser half of that screen is buildable.
- The «Спеки» screen — P1d. Its API shipped in P1a (import, list, report); ZIP, URL import and
  migrate-workspaces are a separate late slice.
- MOCKER_DEFAULT_SPEC auto-creating a first workspace — P1d. cfg.DefaultSpec is parsed and read by nothing;
  that is known and recorded, not overlooked.
- Request validation (validate_req), stateful resources, checkpoints, bundles, WS/SSE, the faker/template/
  sequence recipes, RequireHeader, the SameSite=None cookie rule.
- The P1b MaxBytes soft-ceiling debt (HANDOFF.md records the measured numbers).`

// ---------------------------------------------------------------------------
// Report schemas.
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
      description: 'every signature another agent must call — exported OR unexported: funcs, methods, types AND error sentinels, one per line, as written in the code. An in-package seam a later stage will call (a helper, a context accessor) belongs here too — unexported does not mean private to the next agent.',
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
    output: { type: 'string', description: 'the real output of any command this vector was told to run and report (a test run, EXPLAIN QUERY PLAN, go list -deps)' },
    skips: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'every test the suite reported as SKIP, with the reason it printed' },
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
      description: 'the real numbers your test printed: rows polled, statuses forced, list/card fields compared, golden hashes still matching, SKIPs printed',
    },
    contracts: { type: 'array', maxItems: 20, items: { type: 'string' } },
    goldenIntact: { type: 'boolean', description: 'git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json came back clean (true) or dirty (false)' },
    testsUnmodified: { type: 'boolean', description: 'git diff HEAD -- internal/mockplane/respond_test.go internal/mockplane/plane_test.go internal/mockplane/routes_test.go internal/mockplane/runtime_test.go shows ADDED tests plus at most the three edits this phase authorises: TestServeHTTP_NotImplementedYet (repointed at another unimplemented path), TestServeGenerated_OverrideOnFalse_IsInert (its fixture extended with a when[]), and the serveNoRoute call site in TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute (adapted to the slug -> *workspaces.Workspace parameter change). Anything else — a deleted assertion, a loosened comparison, an adapted expectation — is false' },
    skips: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'every test the suite reported as SKIP, with the reason it printed' },
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
    criterion: {
      type: 'string',
      enum: ['observed-passing', 'observed-failing', 'not-observed'],
      description: 'did YOU observe the phase criterion pass in the tree as it stands NOW: the read-only scenario end to end, the forced status, the traffic poll, the conversions. "not-observed" if you did not run it — never infer it from a green build',
    },
    goldenIntact: { type: 'boolean', description: 'git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json came back clean' },
    testsUnmodified: { type: 'boolean', description: 'the diff of the four internal/mockplane test files shows added tests plus at most the three authorised edits — no assertion deleted or loosened' },
    skips: { type: 'array', maxItems: 20, items: { type: 'string' }, description: 'every test the suite reported as SKIP, with the reason it printed' },
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

// agentSafe is agent() with the one behaviour this script always assumed it
// had. agent() returns null for some deaths, but a subagent that fails to emit
// a valid structured report five times makes the call THROW, and an unhandled
// throw at any serial step aborts the whole run — losing every completed phase's
// orchestration even though its work is on disk. Every serial await goes through
// this; calls inside parallel() are already null-on-throw by the harness.
const agentSafe = async (prompt, opts) => {
  try {
    return await agent(prompt, opts)
  } catch (err) {
    log(`AGENT DIED: ${opts && opts.label} threw (${String(err && err.message || err).slice(0, 200)}). Treating it as a dead agent — its work may still be on disk, and every downstream step is told to read the tree rather than its report.`)
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
TO SEE THE SECTION'S DIFF — most of these files are NEW and plain "git diff HEAD" does not show untracked
files at all:
    git add -N . ; git diff HEAD -- ${paths}
Intent-to-add stages nothing for commit. Your co-reviewer runs the same command concurrently: on
".git/index.lock: File exists", wait a second and retry.
Do NOT widen the path list: .claude/workflows and .wf together hold ~300 KB of untracked workflow script,
and diffing that costs more than the code you are here for.
HEAD is 5cc357f — P1c slice 1, committed, reviewed and green — so this diff is exactly what this run has
produced so far.
DESIGN.md is Russian and 87 KB; never read it whole. The index: §4 98-135, §6 136-174, §8 246-296,
§9 297-411, §12 509-556, §13 557-820, §14 821-916, §15 917-982, §18 1088-1105, §19 1106-1128.
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
    fix = await agentSafe(`${fixCtx}

${name} found ${blockers.length} BLOCKER finding(s) in the section just written. Fix them, changing as
little as possible — every edit must map to a numbered finding. You own every file the findings name.

${list}

RULES
- Verify each finding against the code FIRST. Reviewers false-positive. If one is wrong, do NOT "fix" it:
  record it in "deviations" with the evidence that it is wrong.
- Add or extend a test for every real defect you fix, so it cannot come back silently.
- Never weaken, skip or delete a test to reach green, and never touch
  internal/gen/testdata/p1b_body_hashes.json (HARD RULE 6).
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
  // findings: [] and blockers: [] — structurally identical to a clean gate.
  return { findings, fix, unresolved, dead }
}

// ----------------------------------------------------------------- Engines --
// The when-evaluator runs FIRST and ALONE, then three independent packages in
// parallel. The first draft ran all four together and the pre-flight gate caught
// why that could not work: internal/customep is required to validate the same
// Variant JSON op_overrides validates, every one of those validators is
// unexported inside internal/overrides, and the only agent allowed to export
// them is the one writing when.go — in the same parallel batch. That is the
// recorded deadlock ("two agents writing different files of the same package in
// parallel, referencing each other's symbols"), and its three outcomes are all
// bad: an unowned edit, a second drifting validator, or dropped validation.
// Serializing one small agent costs minutes and removes the whole class.
//
// Model: sonnet for all four. None clears the three-prong opus test — every
// risky property here (the map-order rule, the atomic fail decrement, the batch
// transaction, the revision bump) is checked by Gate 1 immediately after, which
// is prong 2's definition of cheaply caught.
phase('Engines')

const enginesWhen = await agentSafe(`${CTX_CORE}${CTX_TEST}

YOU OWN, AND NOTHING ELSE:
  internal/overrides/when.go — NEW, the evaluator
  internal/overrides/when_test.go — NEW, its tests
  internal/overrides/overrides.go — MODIFY, one call into the existing variant validation
  internal/overrides/repo_test.go — MODIFY, ONLY if an existing when[] fixture stops round-tripping

YOU RUN FIRST AND ALONE in this phase, so the repo-wide green bar applies to you normally, and three
later agents are written against what you export here.

YOUR TASK — make Variant.When executable, and export the variant validation that internal/customep must
share rather than re-implement. DESIGN §12 lines 546-556 ("Условия срабатывания (when)"):
a response variant carries an optional list of simple predicates over the query, a header or a body field;
the first matching variant wins, otherwise active_status. Slice 1 stored and round-tripped these
conditions and never evaluated one. You write the evaluator; internal/mockplane calls it in the next phase.

${SIG_WHEN}

WHAT IS ALREADY THERE, verified — read it before writing:
- internal/overrides/overrides.go declares "type Condition struct" with In, Name, Op, Value (all string)
  and "type Variant struct" whose When field is []Condition. Neither type changes.
- The same file's validateVariant (find it by name) deliberately does NOT shape-check When: its comment
  says When, SchemaPatch and FailDirective are "PRESERVED ONLY … deliberately not interpreted or
  shape-checked here". That comment is now half-obsolete for When, and updating it is part of your job.
- Three conditions already live in two existing fixtures, and all three must keep round-tripping:
  internal/overrides/repo_test.go:107-108 holds TWO — {In:"query",Name:"verbose",Op:"equals",Value:"true"}
  and {In:"header",Name:"X-Debug",Op:"exists"} — and internal/admin/override_handlers_test.go:226 holds
  {In:"header",Name:"X-Test",Op:"exists"}. Both tests must keep passing. READ THEM FIRST and check every field they use against your validation rules before you
  write the validation — if a fixture would now be rejected, that is a behaviour change to an earlier
  phase's test and it goes in "deviations" with the reason, not into a silently relaxed rule.

VALIDATION, and the asymmetry is deliberate:
- Put/PutMany (the WRITE path) begin rejecting a condition this build cannot evaluate — unknown In,
  unknown Op, empty Name, missing Value on equals/contains — wrapped in the existing ErrInvalidRow so the
  admin handler's 400 shape does not change. Call ValidateConditions from validateVariant.
- The READ path tolerates everything. A row written by an older build, or by hand, with in:"cookie" must
  NOT break the workspace: the condition simply never matches. Nothing in the evaluator returns an error
  and nothing panics on a shape it does not know.

THE ONE PROPERTY THAT IS EASY TO GET WRONG AND IMPOSSIBLE TO SPOT LATER: Row.Responses is a Go map, and
Go randomizes map iteration. "The first matching variant wins" over a map is nondeterministic the moment
two statuses both carry a matching when[]. SelectWhen sorts the keys ASCENDING NUMERICALLY (parse them —
they are decimal status strings; a key that does not parse sorts last and is skipped) and returns the
first match in that order. Write the test that pins it: two variants, both matching, assert the lower
status wins, and run it enough times that a random-order implementation would fail (a t.Run loop of 50 is
plenty — map order is re-randomized per iteration).

TESTS — table-driven, and these cases specifically:
- each In x each Op, matching and not matching;
- an unknown In and an unknown Op: never match, no error, no panic;
- the number rendering: a body field holding JSON 1, 1.0 and 1.00 all equal "1"; a json.Number and a
  float64 both render the same; 1e3 renders "1000" not "1e+03";
- true/false; a null value never matches, not even exists;
- an object and an array never match, not even exists;
- BodyOK=false (a truncated or unparsable body): every in:"body" condition is false, including exists;
- a query parameter with two values where only the second matches;
- header lookup is case-insensitive ("x-test" finds "X-Test");
- MatchAll over an empty list returns FALSE;
- MatchAll over two conditions is AND: both must hold;
- SelectWhen skips variants whose When is empty;
- ValidateConditions accepts exactly what the evaluator can evaluate — assert all THREE existing fixture
  conditions listed above are accepted;
- ValidateVariant and ValidateResponses behave exactly as the unexported validation did before you
  exported it: run the existing internal/overrides and internal/admin suites unchanged and paste the
  result. There must be exactly ONE implementation afterwards — if you keep the unexported name, it is a
  one-line call into the exported one, never a copy.`,
  { label: 'engines:when', phase: 'Engines', model: 'sonnet', schema: SCHEMA })

if (enginesWhen) logDeviations('engines:when', [enginesWhen])
else log('WARNING: engines:when returned NOTHING. Three later agents call overrides.ValidateVariant/ValidateResponses and SelectWhen; if when.go is not on disk they cannot build. The gate below reads the tree, not this report.')

const engines = await parallel([
  () => agent(`${CTX_CORE}${CTX_TEST}
YOU RUN CONCURRENTLY with two other agents writing two other new packages — see the green-bar rule above.

YOU OWN, AND NOTHING ELSE:
  internal/livestate/livestate.go — NEW
  internal/livestate/store.go — NEW
  internal/livestate/livestate_test.go — NEW
  internal/livestate/store_test.go — NEW
(Split the code across those two files however is natural: the types and validation in one, the Store and
its locking in the other. Do not create other files in the package.)

YOUR TASK — the RAM layer DESIGN §4 calls "Session" and §12 calls the session-слой: "вне рантайма, ключ
workspace_id, свой TTL, переживает пересборку и вытеснение". Read §4 (98-135) and §12 (509-556). The
sentence that matters most: the REMAINDER of a fail counter lives ONLY here, never in SQLite, because the
three alternatives §12 lists are all worse — a synchronous INSERT per request (which §18 exists to
remove), the runtime cache (where any edit resurrects the original N mid-run), or the admin API (bumping
revision hundreds of times a second).

${SIG_LIVESTATE}

SCOPE, and it is smaller than §14's action list on purpose: this slice implements "status" and "fail"
only. "delay" and "pause" are P2 (§19 line 1112) and are refused at the HTTP boundary by the mockplane and
admin agents in a later phase — this package simply has no such actions, and Set rejects anything that is
not one of the two constants with ErrInvalidDirective.

WHAT THIS PACKAGE MUST NOT DO: import internal/store, internal/workspaces, database/sql or net/http. It
holds no database handle, writes nothing, and knows nothing about HTTP. It also never bumps a revision —
it has no way to, and that is the point (§12: "счётчики session никогда не пишутся в SQLite и не трогают
revision").

CONCURRENCY IS THE WHOLE JOB. Apply runs on EVERY request of every workspace, concurrently, and it
MUTATES (it decrements a counter and drops a spent directive). Requirements:
- Apply on a workspace with no directives is one map read under a read lock and a zero Effect. That is the
  overwhelmingly common path and it must not take a write lock.
- The decrement is atomic with respect to concurrent Apply calls: a fail directive with N=100 hit by 200
  concurrent requests forces the status EXACTLY 100 times and never 101, and the 101st sees no directive.
  Write that test with a real goroutine fan-out and run it under -race.
- Sweep, Set, List and Clear are all safe against concurrent Apply.
- List returns a SNAPSHOT (a copy) — a caller must not be able to mutate the store through the slice it
  gets back, and the JSON encoder must not read a map while Apply writes it.

TESTS:
- a status directive forces until cleared; a fail directive with N=3 forces three times then stops;
- once:true fires exactly once, regardless of N;
- an exact (method, path) directive beats a "*" directive on the same request;
- a fail directive with N>0 beats a status directive on the same target;
- Set replaces a directive with the same (Target, Action) rather than appending a second one;
- MaxDirectivesPerWorkspace: the 65th distinct directive returns ErrTooManyDirectives, and replacing an
  existing one still works at the cap;
- Sweep drops a workspace whose newest SetAt is older than the TTL and keeps a fresher one; the injected
  clock makes this instant — no test sleeps;
- ErrInvalidDirective for: an unknown action, a status below 100 or above 599, action "fail" with n<=0 and
  once=false, a non-All target with an empty method or a path that does not start with "/";
- Clear returns the number dropped and leaves other workspaces alone;
- the concurrency test above, under -race.`,
    { label: 'engines:livestate', phase: 'Engines', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${CTX_CORE}${CTX_TEST}

YOU OWN, AND NOTHING ELSE:
  internal/traffic/traffic.go — NEW, the Event/Row types and Options
  internal/traffic/redact.go — NEW, redaction
  internal/traffic/recorder.go — NEW, the queue and the single writer goroutine
  internal/traffic/repo.go — NEW, the read/clear queries
  internal/traffic/redact_test.go — NEW
  internal/traffic/recorder_test.go — NEW
  internal/traffic/repo_test.go — NEW
YOU RUN CONCURRENTLY with two other agents writing two other new packages — see the green-bar rule above.

YOUR TASK — record what the mock plane answered, cheaply enough that an e2e run does not notice.
Read DESIGN §18 (1088-1105) and §15 lines 950-968 before writing a line. The four rules §18 states for
traffic are the spec for this package: batched in one transaction rather than an INSERT per request;
retention cleaned every N inserts; bodies cut to MOCKER_TRAFFIC_MAX_BODY BEFORE storage; nothing on the
hot path that blocks. §15: "Traffic редактирует секреты до попадания в буфер" — redaction happens before
anything is queued, not on the way out.

${SIG_TRAFFIC}

THE TABLE IS ALREADY THERE — internal/store/migrations/0001_init.sql line 228 (HARD RULE 3, no migration).
Its columns: id, workspace_id, ts, method, path, peer_ip, fwd_ip, matched_kind, matched_id, status,
duration_ms, req_headers, req_body, resp_body, notes, truncated. matched_id is nullable; store NULL for
matched_kind "none", never 0. req_headers is TEXT — store the redacted map as JSON. ts is INTEGER: store
epoch, pick seconds or milliseconds ONCE, and document the choice in the Go doc comment on Event.TS —
NOT in the migration file, which HARD RULE 3 freezes and which no agent in this run owns. Insert, Since
and Rate1m must all use that one unit.
There is one index: traffic_ws on (workspace_id, id DESC). Every query you write must be servable by it —
that is also why "since" is a row id and never a timestamp.

HOW TO OPEN A DATABASE IN A TEST: follow internal/overrides/repo_test.go — it opens a real store.DB over a
temp file, migrates it and inserts a workspace row. Do the same; do not invent a second helper shape. A
traffic row needs a real workspaces row to exist (foreign key).

THE WRITER, precisely:
- Record() puts the event on a buffered channel and returns immediately. Full channel: drop it, increment
  a counter, and NEVER block, NEVER spawn a goroutine per event, NEVER grow the queue.
- Run(ctx) is the single consumer: it accumulates up to Options.Batch events or until Options.FlushEvery
  elapses, then writes them in ONE db.Write transaction. On ctx cancellation it drains what is queued,
  writes it, and returns — a shutdown must not lose the last batch. THAT FINAL DRAIN MUST NOT USE THE
  CANCELLED CONTEXT: database/sql fails immediately on one, so the drain that exists to save the last
  batch would write nothing while looking like it worked. Use context.WithoutCancel(ctx) with a bounded
  timeout for it, and assert in a test that after cancellation the rows are actually IN THE DATABASE, not
  merely that Run returned.
- Flush(ctx) forces the current buffer out and returns only when it has been committed. It is what makes
  this testable and what DELETE /traffic calls; it must be safe to call concurrently with Record and with
  Run, and must not deadlock if Run is not running (return an error or write synchronously — your choice,
  documented, but a Flush that hangs forever when nobody consumes the queue is a bug the acceptance test
  will hit).
- Retention: every N inserts (N a documented constant), delete the workspace's rows beyond
  Options.Retention newest. Do it INSIDE the same transaction as the insert batch, keyed by id (the index
  order), and never with a full-table scan across workspaces.
- The number of dropped events is reported: Dropped() for the API, and the drop is also written into the
  next stored row's notes so an operator reading the traffic screen sees the gap rather than a clean lie.
- NOTES IS COMPOSED BY THE RECORDER, NEVER BY THE CALLER, and it is a contract another package reads: the
  admin to-override conversion REFUSES a row whose body was cut or redacted, and the frozen schema has no
  column for either, so these tokens are the only carrier. Write NoteRedacted whenever RedactJSONBody
  reported a change for either body, NoteSuppressed when SuppressBodies was set, NoteTruncatedReq and/or
  NoteTruncatedRsp for each body you actually cut, and the dropped counter as NoteDroppedPrefix+N.
  Event.Notes is the caller's free text and is appended AFTER your tokens, never merged into them.
  Provide Row.HasNote (exact token match, never a substring scan), Row.Redacted() and Row.DroppedBefore(),
  and test that every token survives a round trip through the database — a token nobody writes is a
  refusal that never fires, and a token read by substring is a refusal that fires on the wrong row.

REDACTION, and it is the security property this package exists to hold:
- Headers: authorization, cookie, set-cookie, x-api-key, proxy-authorization -> RedactedValue, matched
  case-insensitively. Everything else is stored as sent. A multi-valued header is joined the way
  http.Header.Get sees it (first value) or comma-joined — document which.
- Bodies: for JSON, walk the decoded value and replace the VALUE of any field whose name (lower-cased)
  is exactly password, token, secret, passwd, pwd, or ends in _key, _token, _secret — at ANY depth,
  inside arrays too. Non-JSON bodies are stored as-is (cut to MaxBody); do not attempt regex redaction of
  text.
- Event.SuppressBodies=true means NEITHER body is stored at all — not redacted, absent. The caller sets it
  for auth paths (§15: "Для auth-ручек тела по умолчанию не хранятся вовсе").
- Cutting to MaxBody happens after redaction (redacting a cut JSON body would fail to parse and store the
  secret) and sets Truncated.

TESTS:
- redaction of each header name, mixed case, and that an unlisted header survives;
- nested and in-array secret fields; a *_key suffix; a field named "tokens" (plural) is NOT redacted, and
  say why in the test name;
- a body that is not JSON is stored unchanged (cut), not mangled;
- SuppressBodies stores neither body;
- MaxBody cuts and sets truncated; the redaction still happened;
- Record() never blocks: fill the queue past capacity from one goroutine with no consumer running and
  assert it returns and Dropped() counts;
- one batch of N events lands in ONE transaction (assert by counting rows after a single Flush);
- retention: write Retention+20 events, flush, assert exactly Retention remain and that they are the
  NEWEST ones;
- Since(): ids strictly greater than the cursor, oldest first, limit honoured;
- List(): newest first;
- Clear(): returns the count and touches only that workspace;
- Rate1m with an injected "now";
- ctx cancellation flushes the tail;
- everything under -race.`,
    { label: 'engines:traffic', phase: 'Engines', model: 'sonnet', schema: SCHEMA }),

  () => agent(`${CTX_CORE}${CTX_TEST}

YOU OWN, AND NOTHING ELSE:
  internal/customep/customep.go — NEW, the Row type, validation, JSON encode/decode of responses
  internal/customep/repo.go — NEW, the queries
  internal/customep/customep_test.go — NEW
  internal/customep/repo_test.go — NEW
  internal/router/router.go — MODIFY, exactly one new field on Route
  internal/router/router_test.go — MODIFY, one test that the field survives Build and Match
YOU RUN CONCURRENTLY with two other agents writing two other new packages — see the green-bar rule above.
internal/overrides already has the exported validation you need — written by the agent that ran alone
before this batch:
    func ValidateVariant(v Variant) error
    func ValidateResponses(responses map[string]Variant) error
If they are NOT on disk (that agent can have died), report it in "deviations" and leave the call sites as
TODO comments: do NOT write a second validator and do NOT edit internal/overrides. It is not yours, and a
drifting second copy is worse than a missing call.

YOUR TASK — the storage half of custom endpoints (DESIGN §8, §13's custom_endpoints table at
internal/store/migrations/0001_init.sql line 191, §14's endpoints routes). The serving half and the admin
handlers are other agents' work in later phases; you give them a repo and an identity to key on.

${SIG_CUSTOMEP}

${SIG_PLANE}
Of that block, YOU implement only the router.Route field (CustomRowID) and its test. The three Plane
setters belong to the mockplane agents in the next phase — they are pasted here so the field you add is
the field they will read, spelled the same way.

WHAT TO COPY RATHER THAN INVENT: internal/overrides/repo.go is the same shape of repository over an
adjacent table, written and reviewed one slice ago. Read it whole (about 500 lines) and follow it —
especially bumpRevisionTx (the revision bump inside the SAME transaction, and its comment explaining why
calling workspaces.Repo.Update instead would deadlock the one-connection writer pool), the scan helpers,
the ErrNotFound/ErrWorkspaceNotFound split, and how it validates a Variant before writing. Reuse
overrides.Variant and call overrides.ValidateVariant / overrides.ValidateResponses rather than writing a
second decoder or a second validator for the same JSON — that is exactly why this package imports
internal/overrides, and a drifting copy is the defect this instruction exists to prevent.

DETAILS THE SCHEMA FIXES FOR YOU, so do not "improve" them:
- UNIQUE (workspace_id, method, path) and UNIQUE (workspace_id, method, canonical_path). The second is the
  DESIGN §8 conflict rule; map its constraint violation to ErrConflict, and do not implement the check as
  a SELECT-then-INSERT race.
- active_status is INTEGER NOT NULL DEFAULT 200 — not nullable like op_overrides.active_status.
- source_order is NOT NULL: assign max+1 inside the insert transaction.
- canonical_path is a stored column: compute it with router.CanonicalPath(Path) on write, never at match
  time.

VALIDATION: method must be a known HTTP method, upper-cased on the way in; path must start with "/" and
carry no query, no fragment and no "//"; every responses key is a 3-digit status; every Variant goes
through the same validation overrides already applies (mode, bodyEncoding, base64 decodability, the
pinned-body size ceiling, recipes). Reject with ErrInvalidRow.

THE ROUTER FIELD: add CustomRowID to Route with the comment given in the block above, and prove with a
test that Build copies it and Match returns it — Build compiles a copy of each Route and Match returns a
pointer to that copy, so a field that Build forgot to carry would be silently zero at serve time and every
custom endpoint would record matched_id 0.

TESTS:
- create, read back, list ordering by source_order then id;
- Create bumps workspaces.revision by exactly one, in one transaction (assert the revision before/after);
- Delete bumps it too — a deleted endpoint that keeps serving until the next unrelated edit is the same
  bug in reverse;
- ErrConflict on a second endpoint with the same (method, canonical_path), including when the literal
  paths differ ("/a/{id}" vs "/a/{name}");
- the same canonical path under a DIFFERENT method is allowed;
- ErrWorkspaceNotFound for a workspace that does not exist;
- ErrInvalidRow for a bad method, a relative path, a bad status key, an undecodable base64 body;
- a responses map carrying when[] and schemaPatch round-trips unchanged (this slice's evaluator reads
  when[] at serve time, but the repo must not reshape it);
- fail_directive and req_schema round-trip byte-for-byte as raw JSON;
- concurrent creates from several goroutines do not lose a source_order or deadlock (-race).`,
    { label: 'engines:customep', phase: 'Engines', model: 'sonnet', schema: SCHEMA }),
])

const enginesOK = [enginesWhen, ...engines].filter(Boolean)
logDeviations('Engines', engines.filter(Boolean))
for (const [i, r] of engines.entries()) {
  if (!r) log(`WARNING: engines agent #${i + 1} returned NOTHING. Its work may still be on disk — the gate below reads the tree, not this report.`)
}
if (!enginesWhen && enginesOK.length === 0) {
  log('FATAL: every Engines agent died. Nothing downstream can be built against contracts that were never reported; stopping.')
  return { phase: 'P1c-2', fatal: 'all Engines agents died', findings: 0, actionable: 0, rounds: [], stillOpen: [], unreviewed: ['everything'] }
}

// ------------------------------------------------------------------ Gate 1 --
phase('Gate 1')

const gate1Paths = 'internal/overrides internal/livestate internal/traffic internal/customep internal/router internal/store'
const gate1Ctx = gateCtx(gate1Paths)
const enginesContracts = contractsOf(enginesOK)

const gate1 = await runGate('Gate 1', [
  {
    label: 'gate1:contracts',
    prompt: `${gate1Ctx}

Four new engines were just written — the evaluator first and alone, then three in parallel: the when[] evaluator inside internal/overrides, the RAM
live-state store, the traffic recorder and repository, and the custom-endpoint repository plus one new
router field. Nothing calls them yet — the serving path and the admin API are written against them next,
by agents that will have ONLY the signatures below.

The contracts they reported:
${enginesContracts}

YOUR VECTOR: do these packages actually deliver what the next phase will assume, and does each match
DESIGN?
- Compare every exported symbol against the intended contract below. A name, an argument order or a return
  shape that drifted is a blocker: the callers are written from this text, not from the code.
${SIG_WHEN}
${SIG_LIVESTATE}
${SIG_TRAFFIC}
${SIG_CUSTOMEP}
- The when[] rules: is SelectWhen's order genuinely deterministic (sorted, not map order)? Does an
  unparsable status key crash or sort sanely? Does a truncated body really make every body predicate
  false? Does the number rendering make JSON 1 and 1.0 equal? Is MatchAll on an empty list false?
- Is there EXACTLY ONE implementation of the variant validation? engines:when exported ValidateVariant/
  ValidateResponses and engines:customep was told to call them; a second copy inside internal/customep, or
  an "adapted" variant of it, is a blocker — the two tables store the same JSON, and a drifting validator
  means one accepts what the other rejects. Confirm too that engines:customep did not edit
  internal/overrides.
- Does the write-path validation reject anything the two existing fixtures use
  (internal/overrides/repo_test.go, internal/admin/override_handlers_test.go)? Run the whole suite, not
  just the new packages.
- livestate: is Apply the only place a counter is consumed? Does a "*" directive lose to an exact one?
  Is MaxDirectivesPerWorkspace actually enforced on the path an unauthenticated POST reaches?
- traffic: are the SQL statements servable by the ONE index (workspace_id, id DESC)? Is matched_id NULL
  for "none" rather than 0? Is the ts unit used consistently by insert, Since and Rate1m?
- customep: does Create bump workspaces.revision in the SAME transaction, with no nested db.Write? Is
  ErrConflict driven by the UNIQUE constraint rather than a racy pre-SELECT? Does router.Build carry
  CustomRowID through to Match?
- HARD RULE 3: any migration, ALTER, CREATE INDEX or schema edit is a blocker. HARD RULE 2: go.mod/go.sum
  must be untouched — check "git diff HEAD -- go.mod go.sum".
- Tests that assert nothing, or would still pass with the implementation deleted.`,
  },
  {
    label: 'gate1:concurrency',
    prompt: `${gate1Ctx}

YOUR VECTOR: concurrency, cost and data safety in the two packages that carry state — internal/livestate
(a mutable map read on every request) and internal/traffic (a queue, a goroutine and a transaction) — plus
the SQL in internal/customep and internal/traffic.
- Run "go test ./internal/livestate/... ./internal/traffic/... ./internal/customep/... -race -count=4".
  Report the real output. Then read for the races a test would not catch: a map read outside a lock, a
  slice returned from List that aliases stored state, a counter decremented non-atomically, a
  read-lock upgraded to a write lock by hand, a channel closed twice, a goroutine leaked per request.
- livestate.Apply is on the mock plane's hot path (DESIGN §18 forbids a rate limit there, so per-request
  cost IS the defence). What does it cost for a workspace with NO directives? A write lock, an allocation
  or a map iteration on that path is a blocker, not a nit.
- traffic.Record must never block and never spawn. Check what happens when the queue is full, when Run was
  never started at all (which is exactly what every existing test that builds a Plane without a recorder
  produces), and when ctx is cancelled mid-batch. A Flush that can hang forever is a blocker: the
  acceptance test calls it.
- The batch write: is it ONE transaction? Does a single malformed event abort the whole batch and lose 63
  good rows? Is retention pruning bounded, or does it scan the table? Is it inside the same transaction?
- store.DB's writer pool is SetMaxOpenConns(1): find any path where a db.Write can be entered while
  another is open — a repo method called from inside a transaction, a Flush called from a handler that is
  itself inside db.Write. That deadlocks the process, not the request.
- Memory: anything unbounded that an UNAUTHENTICATED request can grow — the directive map, the queue, a
  per-workspace map that is never swept, a slice appended per request.
- Cost of the queries: an EXPLAIN QUERY PLAN on the traffic reads, and whether the retention delete uses
  the index.`,
  },
], `${CTX_CORE}${CTX_TEST}

You are fixing blockers in the four engine packages just written: internal/overrides (when.go),
internal/livestate, internal/traffic, internal/customep and the one new field in internal/router.
${SIG_WHEN}
${SIG_LIVESTATE}
${SIG_TRAFFIC}
${SIG_CUSTOMEP}`)

// -------------------------------------------------------------------- Serve --
// SERIAL, and that is the whole reason this phase is shaped this way: all three
// agents write files of the SAME Go package (internal/mockplane) and each one's
// definition of done is "go build ./... is clean". Two of them in parallel means
// neither can reach it — P1b's pre-flight gate caught exactly this and the fix
// was to serialize.
phase('Serve')

const serveCtx = `${CTX_CORE}${CTX_SERVE}${CTX_TEST}
THE CONTRACTS THE ENGINE PACKAGES REPORTED (later lines supersede earlier ones):
${enginesContracts}
${gate1.fix ? `\nA gate fix agent then changed: ${(gate1.fix.files || []).join(', ')}. Read those files as they are NOW, not as the list above describes them.\n` : ''}`

const serveChoose = await agentSafe(`${serveCtx}${SIG_WHEN}
${SIG_LIVESTATE}
${SIG_PLANE}
${SIG_WIRE}

YOU OWN, AND NOTHING ELSE:
  internal/mockplane/reqbody.go — NEW, the one-read capped request-body capture
  internal/mockplane/livestate.go — NEW, SetLiveState, the LiveStateSource interface, the {prefix}/state handler
  internal/mockplane/reqbody_test.go — NEW
  internal/mockplane/livestate_test.go — NEW
  internal/mockplane/respond.go — MODIFY, the status-choice order
  internal/mockplane/plane.go — MODIFY, the body capture and the state endpoint under the reserved prefix
  internal/mockplane/plane_test.go — MODIFY, TestServeHTTP_NotImplementedYet asserts the very 404 you are removing
  internal/mockplane/respond_test.go — MODIFY, add cases

YOUR TASK, THREE THINGS THAT SHARE ONE REQUEST:

1. THE REQUEST BODY, READ ONCE — and the restore semantics are prescribed, because getting them subtly
   wrong stays invisible until something downstream reads a body:
   - read at most cap+1 bytes where cap = max(cfg.TrafficMaxBody, 64 KiB); keep cap of them, and let that
     one extra byte be how you know it overflowed (never compare against Content-Length, which lies);
   - restore r.Body as an io.ReadCloser over io.MultiReader(bytes.NewReader(consumed), original) whose
     Close closes the ORIGINAL — not a NopCloser over the prefix, which silently truncates every
     downstream reader, and not a fresh reader that leaks the original;
   - do NOT touch r.ContentLength and do not rewrite the header: they still describe the real body;
   - httpx.MaxBody wraps the whole dispatcher in http.MaxBytesReader, so YOUR read is the one that hits
     that limit first. Propagate its error rather than treating a read failure as "no body": a request
     over MOCKER_MAX_BODY must keep behaving exactly as it does today;
   - GET/HEAD/DELETE with no body must cost nothing — no allocation, no read.
   Parsing follows DESIGN §8 (lines 289-294): application/json strictly; text/plain and a missing
   Content-Type are read as text and tried as JSON, and a parse failure is NOT a 400 — clients routinely
   send JSON with no type; multipart is not touched at all. Over the cap the buffer is marked truncated,
   and a truncated buffer makes every in:"body" predicate FALSE (overrides.Input.BodyOK=false) rather than
   matching on a partial document.
   The next stage (serve:traffic) reads these same bytes, so whatever holds them is a seam: name it in
   your "contracts" list even though it is unexported.

2. THE STATUS CHOICE. Implement exactly the order written out in the CONTEXT block above (route_off, then
   livestate.Apply, then the delay, then status: forced -> when -> active_status -> document). Keep the
   existing variantForStatus / synthetic-variant / "no variant at all" branches exactly as they are — you
   are inserting two new sources of a status, not rewriting response assembly. Everything after the status
   is chosen must keep keying off the FINAL status: row.Responses[status], lookupRecipes(route, status),
   pinned bodies, media type, headers.

3. POST/GET/DELETE {prefix}/state, per the wire block above. serveReserved (plane.go) currently answers
   404 "not_implemented_yet" for it — that is the branch you replace, and
   internal/mockplane/plane_test.go:168 TestServeHTTP_NotImplementedYet asserts precisely that 404, so it
   is yours to update (do not delete it: point it at another unimplemented path under the prefix so the
   "everything else is 404" rule stays tested). The endpoint is UNAUTHENTICATED like the rest of this
   plane — DESIGN §12 says switching must work from tests, §15 already says the mock plane is open — so:
   it is bounded (MaxDirectivesPerWorkspace), it logs each directive with the peer address
   (httpx.ResolvePeer, as the rest of the plane does), it is NOT recorded in traffic (the next agent
   handles that; just leave the reserved-prefix branch outside whatever it wraps), and it answers 501 for
   "scenario", "delay" and "pause" with a body that names P2 rather than pretending to accept them.

HARD RULE 6 IS YOURS TO PROTECT, and you own two of the four files it is enforced through
(respond_test.go and plane_test.go). With no LiveStateSource wired and no when[] on any variant, every
branch you add must be provably inert: every existing mockplane test builds a Plane with no live-state
source, and they must all still pass with their assertions UNCHANGED. The one exception you are given is
TestServeHTTP_NotImplementedYet, named below. If any other existing assertion fails, the code is wrong —
do not adjust the test. One existing test you must EXTEND rather than change:
TestServeGenerated_OverrideOnFalse_IsInert (respond_test.go) has no When in its fixture, so add one that
would match and assert the switched-off row still fires nothing.

TESTS:
- the order, as a table: given a route with active_status=418, a when[] on 409 that matches, and a
  livestate force of 503, the answer is 503; remove the force and it is 409; remove the condition and it
  is 418; remove active_status and it is the document's choice;
- a when[] that matches selects a status whose pinned body and recipes then apply (the "keys off the FINAL
  status" property, which is the one that silently breaks);
- route_off wins over a livestate force, and consumes no counter (assert the counter afterwards);
- a fail directive with n=2 answers the forced status twice and the third request is normal;
- a body predicate on a truncated body does not match;
- a body predicate on a JSON body sent with no Content-Type DOES match (the §8 tolerance);
- POST /state with each valid shape, then GET it back, then DELETE; 501 for scenario/delay/pause; 400 for
  a bad status and for n<=0; the directive list in the response carries the REMAINDER;
- a request with a 1 MB body against a workspace with no when[] anywhere: the plane must not have buffered
  more than the cap (assert the cap, not the timing).`,
  { label: 'serve:choose', phase: 'Serve', model: 'sonnet', schema: SCHEMA })

if (serveChoose) logDeviations('serve:choose', [serveChoose])
else log('WARNING: serve:choose returned nothing. The next two Serve agents edit the same files; they are told to read the tree as it is.')

const serveTraffic = await agentSafe(`${serveCtx}${SIG_TRAFFIC}
${SIG_PLANE}
${serveChoose ? `The previous stage of this phase (serve:choose) changed: ${(serveChoose.files || []).join(', ')} and reported these contracts:\n${(serveChoose.contracts || []).join('\n')}` : 'The previous stage of this phase (serve:choose) reported nothing — read internal/mockplane as it actually is on disk before you plan anything.'}

YOU OWN, AND NOTHING ELSE:
  internal/mockplane/traffic.go — NEW, SetTraffic, the TrafficSink interface, the capturing writer, the record call
  internal/mockplane/traffic_test.go — NEW
  internal/authpreset/names.go — MODIFY, export isAuthPath as IsAuthPath
  internal/authpreset/authpreset.go — MODIFY, the call sites of the renamed function
  internal/authpreset/authpreset_test.go — MODIFY, the call sites in TestIsAuthPath

SERIAL EDITS — files the previous stage of this same phase already changed. You extend them; read them as
they are on disk:
  - internal/mockplane/plane.go: wrap the route-table branch (step 5) so the response is captured and the
    event recorded. NOT the reserved prefix (step 4) and NOT the preflight branch.
  - internal/mockplane/routes.go and internal/mockplane/respond.go: two or three lines that record which
    route was matched.

YOUR TASK — every request the mock plane answers becomes one traffic event, and it costs almost nothing.

WHAT MUST BE RECORDED: the requests that reach the route table — matched to a spec operation
(matched_kind "operation", matched_id = route.OpRowID), matched to a custom endpoint ("custom",
route.CustomRowID — the custom serving path itself arrives in the next stage, so leave the field where it
will be filled and do not fake it), or matched to nothing at all ("none", matched_id NULL). The 404 case
is not an afterthought: DESIGN §6 draws it as "404 + traffic + создать endpoint" and it is the whole input
to "создать endpoint из запроса".
WHAT MUST NOT BE RECORDED: anything under the reserved prefix (health and state are control traffic, and
recording POST /state would let the traffic screen echo directives back), and CORS preflights.

HOW THE MATCH GETS BACK TO THE RECORDER: put a small mutable capture struct in the request context in
serveResolved and have serveRoute/serveGenerated mark it. That is deliberate and worth its comment: the
alternative — threading a parameter through serveRoute and serveGenerated — changes two functions another
agent owns, for a value that is only ever written once per request.

THE CAPTURING ResponseWriter is a write-through TEE, not a buffer: it forwards every Write to the real
writer immediately and copies at most cfg.TrafficMaxBody bytes aside, setting truncated when it stops
copying. cfg.MaxResponse is 4 MB and buffering that per in-flight request is exactly the hot-path cost
DESIGN §18 exists to remove. Four specifics, each with a wrong answer that compiles:
- HEAD. plane.go:170 already wraps w in a headWriter whose Write REPORTS SUCCESS AND DISCARDS THE BYTES
  (DESIGN §8: HEAD matches GET with an empty body). Your tee sits outside it, so a naive copy records a
  response body that was never sent. For HEAD: record the status and the headers, copy no body.
- Unwrap. This repo's house contract is Unwrap() http.ResponseWriter — httpx.StatusRecorder
  (internal/httpx/middleware.go:102) and headWriter (plane.go:259) both implement it so
  http.ResponseController can still reach the real writer for flushing and deadlines. Yours does too.
- ReadFrom. If you expose one, or embed something that does, route it through your own Write or the copy
  is bypassed entirely for exactly the large bodies it exists to cap. Prefer not exposing it.
- Status. Record the implicit 200 net/http writes when a handler never calls WriteHeader. Reuse
  httpx.StatusRecorder for the status and the byte count rather than reimplementing them, and add only
  the capped body copy on top.

BODY SUPPRESSION IS COMPUTED FROM THE PATH, NOT FROM THE ROUTE: an unmatched request has no route, and a
typo'd POST to an auth path with a real password in it is exactly the request that must not be stored
(DESIGN §15). Export authpreset.IsAuthPath (today it is unexported at internal/authpreset/names.go:23)
and call it with the normalized request path, with ws.Settings.BasePath stripped first when it is a
prefix — otherwise a workspace whose base path itself contains a trigger segment (basePath "/api/auth")
suppresses every body in the whole workspace. Put that in a comment; it is a decision, not an accident. Keep its whole-segment matching exactly as it is: it is
segment-based on purpose, so "/journal/tokens" is NOT an auth path, and a substring match would break
that. Renaming a function used inside internal/authpreset means updating its call sites there and in its
test — those three files are yours for that rename and nothing else.

THE PEER: httpx.ResolvePeer(r, cfg.TrustProxy) already exists and already implements DESIGN §15's
"MOCKER_TRUST_PROXY off by default, the immediate peer is always recorded". Use it; do not read
X-Forwarded-For yourself.

A NIL TrafficSink MEANS NOTHING IS RECORDED and the plane behaves exactly as it does today — every
existing mockplane test builds a Plane with no sink and must keep passing unchanged.

TESTS (with a fake sink that collects events — no database in these tests):
- a matched request produces one event with the right kind, id, status, duration>0 and the peer set;
- an unmatched request produces one event with kind "none" and a NULL id;
- a request under the reserved prefix produces NO event, and neither does a preflight;
- the response body is captured up to the cap and truncated beyond it, and the CLIENT still receives the
  full body (assert both sides — a tee that eats bytes is the worst possible bug here);
- an auth path suppresses both bodies;
- a handler that never calls WriteHeader still records 200;
- with no sink wired, nothing panics and nothing changes;
- -race with concurrent requests against one plane.`,
  { label: 'serve:traffic', phase: 'Serve', model: 'sonnet', schema: SCHEMA })

if (serveTraffic) logDeviations('serve:traffic', [serveTraffic])
else log('WARNING: serve:traffic returned nothing.')

const serveCustom = await agentSafe(`${serveCtx}${SIG_CUSTOMEP}
${SIG_PLANE}
${[serveChoose, serveTraffic].filter(Boolean).map((r) => `An earlier stage of this phase changed: ${(r.files || []).join(', ')}`).join('\n') || 'Both earlier stages of this phase reported nothing — read internal/mockplane as it actually is on disk.'}

YOU OWN, AND NOTHING ELSE:
  internal/mockplane/custom.go — NEW, SetCustomEndpoints, the CustomSource interface, serveCustom
  internal/mockplane/custom_test.go — NEW

SERIAL EDITS — files earlier stages of this same phase already changed; read them as they are on disk:
  - internal/mockplane/runtime.go: load the workspace's custom rows, merge them into the ONE sorted route
    table, and build a runtime even when the workspace has NO spec.
  - internal/mockplane/routes.go: dispatch a matched custom route into serveCustom, stop short-circuiting
    the no-spec case when custom endpoints exist, and apply settings.NotFoundBody in serveNoRoute.
  - internal/mockplane/plane.go: SetCustomEndpoints.
  - internal/mockplane/respond.go and internal/mockplane/respond_test.go, FOR THE serveNoRoute CALL SITE
    ONLY: serveNoRoute(w, r, slug string, segments []string) has three call sites — routes.go:187,
    routes.go:200 and respond.go:105 (the route_off branch) — plus respond_test.go:1325-1356, which
    asserts the two 404s are byte-identical. To reach settings it needs the workspace, so change that
    parameter from slug to ws *workspaces.Workspace and update ALL THREE call sites in one edit. Do NOT
    write a second 404 writer: the two answers being identical is what keeps a disabled route
    indistinguishable from a missing one (DESIGN §8), which is the property that test protects.

YOUR TASK, THREE THINGS:

1. CUSTOM ROUTES JOIN THE SAME TABLE. DESIGN §8: "Одна отсортированная таблица для операций спеки и
   кастомных endpoint'ов", and rule 3 — at equal specificity the custom endpoint wins. router.Route.Custom
   and the comparator branch for it already exist and were written for this day; set Custom=true and
   CustomRowID, and let the existing comparator do its job. Build the merged slice in buildRuntime, from
   CustomSource.ForWorkspace, and keep the loading shape overrides already uses (nil source -> no rows ->
   nothing changes).

2. A WORKSPACE WITH NO SPEC MUST STILL SERVE THEM. Today routes.go returns the plain 404 whenever
   ws.SpecID is nil, before any runtime is built; buildRuntime dereferences *ws.SpecID on its first line;
   and routes.go:193 dereferences it AGAIN inside the build-error log line — which, the moment a
   spec-less workspace can reach that code, is a nil dereference on an unauthenticated request. Fix that
   log line too. "Create an endpoint from a request" on a fresh workspace would silently do nothing. Give
   buildRuntime a spec-less path: no document, no resolver, no generator, no variants — a table built from
   the custom rows alone. Everything that would dereference the missing generator must be unreachable by
   construction on that path, and a test must prove a spec-less workspace serves a custom endpoint and
   still 404s everything else.

3. SERVING A CUSTOM ENDPOINT. It has no operations row and therefore no declared variants: responses[status]
   is the only source of truth. Reuse the helpers respond.go already exports to the package —
   pinnedBody, setSafeHeader, acceptable, wrapEnvelope, awaitDelay/clampedDelay, dangerousResolvedMediaType
   — do NOT copy their logic into a second implementation, and do not touch serveGenerated beyond the
   dispatch. One gate is NOT a helper and is the easiest to lose: the pinned-body re-check against
   p.cfg.MaxResponse is written inline at respond.go:232, and a custom endpoint that serves a pinned body
   without it lets a stored body bypass the live ceiling every generated body is held to. The order for a custom route is the same as for a spec route and is written out in the
   context block above: route_off, livestate.Apply, delay, then the status (forced -> when -> active_status,
   with no "document's choice" step because there is no document). mode "generated" on a custom endpoint
   with no schema answers that status with an empty body — honest silence, not an error.
   settings.Envelope, the media type and the header rules apply exactly as they do for a spec route.

ALSO YOURS, and it is small: domain.Settings.NotFoundBody. It is parsed, stored and size-limited by
internal/workspaces and read by NOTHING. serveNoRoute is the one place it means anything (DESIGN §6, §8):
when it is set, the 404 answers that JSON body instead of the default error shape, with the same 404
status and the same CORS headers. While you are there, the default message still says the route table
"arrives in a later phase" — it arrived two phases ago. No test asserts that string (verified: rg over
*_test.go finds none), so fix the text.

TESTS:
- a custom endpoint answers on a path the spec does not have;
- a custom endpoint canonically equal to a spec operation OVERRIDES it (§8 rule 3), and removing the
  custom row gives the spec operation back;
- a spec-less workspace serves its custom endpoint and 404s everything else;
- route_off on a custom endpoint answers the same 404 as a missing route;
- a when[] on a custom endpoint selects the status;
- NotFoundBody replaces the 404 body and only the body;
- with no CustomSource wired, every existing behaviour is unchanged (this is HARD RULE 6 for this stage);
- -race.`,
  { label: 'serve:custom', phase: 'Serve', model: 'sonnet', schema: SCHEMA })

if (serveCustom) logDeviations('serve:custom', [serveCustom])
else log('WARNING: serve:custom returned nothing.')

const serveOK = [serveChoose, serveTraffic, serveCustom].filter(Boolean)

// ------------------------------------------------------------------ Gate 2 --
phase('Gate 2')

const gate2Paths = 'internal/mockplane internal/router internal/authpreset internal/gen'
const gate2Ctx = gateCtx(gate2Paths)

const gate2 = await runGate('Gate 2', [
  {
    label: 'gate2:correctness',
    prompt: `${gate2Ctx}

The mock plane just gained three things, written by three agents one after another: the request-body
capture plus the new status-choice order plus {prefix}/state; the traffic capture; and custom endpoints
including a spec-less runtime.
${CTX_SERVE}

YOUR VECTOR: correctness of the serving path, and above all the regression nobody would notice.
- HARD RULE 6 first, and look in the right place: the golden CANNOT catch this slice (it hashes
  gen.Generator.Body directly, upstream of everything here), so the guard is the existing mockplane test
  corpus. Run:
      git diff HEAD -- internal/mockplane/respond_test.go internal/mockplane/plane_test.go internal/mockplane/routes_test.go internal/mockplane/runtime_test.go
  That diff must contain ADDED tests only. A deleted assertion, a loosened comparison, an "adapted"
  fixture expectation or a renamed-away test is a BLOCKER — it is the regression guard being dismantled to
  fit new behaviour, and it looks exactly like a passing run. THREE edits are authorised by this phase and
  are NOT findings: TestServeHTTP_NotImplementedYet (repointed at another unimplemented path, not
  deleted), TestServeGenerated_OverrideOnFalse_IsInert (its fixture EXTENDED with a when[] that would
  match), and the serveNoRoute call site inside TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute
  (a mechanical adaptation to the slug -> *workspaces.Workspace parameter change, which cannot compile
  otherwise). The rule is: those three edits plus added tests, nothing else. Then run "git diff --exit-code --
  internal/gen/testdata/p1b_body_hashes.json" — non-empty is a blocker — and read the respond.go diff
  asking whether any new branch can change a body, a header or a status when none of the three features
  is in play.
- The status order, against DESIGN §4 and §12: does a live-state force really beat when[], does when[]
  really beat active_status, and does everything AFTER the choice (row.Responses[status], the compiled
  recipe set, the pinned body, the media type) key off the FINAL status? Prove it with a case where the
  forced status has a pinned body of its own.
- Does route_off still answer the same 404 as a missing route, and consume no counter?
- The body capture: is r.Body genuinely restored (a handler downstream reading it must see the whole
  body)? Is the cap enforced? Is a GET with no body free? Does a multipart request go untouched?
- The tee: does the client still receive the complete response when the copy stops at the cap? Does it
  record the implicit 200? Does anything downstream type-assert the ResponseWriter (Flusher, Hijacker) and
  now get the wrapper instead?
- The spec-less runtime: is every generator/variant dereference genuinely unreachable on that path? A nil
  map read is fine in Go; a nil *gen.Generator method call is a panic on an unauthenticated request.
- The merged route table: does a custom route still lose to a MORE specific spec route, and win only at
  equal specificity (§8 rules 1-3)? Does CustomRowID survive Build and Match?
- {prefix}/state: 501 for scenario/delay/pause, 400 for the malformed shapes, and the remainder reported
  as the remainder. Is it excluded from traffic?
- Run the full suite: "go test ./... -race -count=1". Report every SKIP with its reason.`,
  },
  {
    label: 'gate2:security',
    prompt: `${gate2Ctx}

YOUR VECTOR: security and cost on a plane that DESIGN §15 says everyone in the contour can reach and §18
says must never be rate-limited. This slice added an unauthenticated write endpoint, a per-request buffer
and a per-request record.
- POST {prefix}/state is unauthenticated by design. What can an attacker do with it? Bound the damage:
  how much memory can they make the process hold (directives per workspace, workspaces per process, TTL),
  can they force a status on a workspace they do not own (they can — that is the design; say whether the
  blast radius is bounded and logged), can they crash the plane with a malformed body, can they make it
  allocate proportionally to input?
- The request-body capture: a request with a huge body, a chunked body with no length, a Content-Length
  that lies, a body that is 64 KiB of nested JSON arrays. What is the worst case per in-flight request,
  measured, and what bounds it? httpx.MaxBody wraps the whole dispatcher — check what it is set to and
  whether the capture respects it.
- Traffic redaction reaching the wire: trace what actually gets stored for a request carrying an
  Authorization header and a password in the body, and for the SAME request against an auth path. A leak
  here is a credential store with an admin UI in front of it.
- The tee: can a slow or malicious client make the plane hold the copied body longer than the response?
  Is the copy bounded by cfg.TrafficMaxBody in BYTES, or by something an attacker chooses?
- Response splitting and content sniffing through a custom endpoint's pinned headers, pinned media type
  and pinned body — the same three gates a spec-route override goes through (setSafeHeader,
  dangerousResolvedMediaType, the MaxResponse recheck). A custom endpoint that skips any of them is a
  blocker; find the path or confirm there is none.
- NotFoundBody now reaches the wire: it is operator JSON on an open plane. What is its size ceiling
  (internal/workspaces enforces one — check it), and can it carry a media type or a header?
- Panic reachability from an unauthenticated request through any of the new code: a type assertion on
  decoded JSON, a slice index from a path segment, a nil map write, a context value asserted to the wrong
  type.
- "go list -deps ./cmd/mocker | grep santhosh" must print nothing; "git diff HEAD -- go.mod go.sum" must
  be empty.
Report each finding as a concrete attack: what the attacker sends, what they get.`,
  },
], `${serveCtx}${SIG_LIVESTATE}
${SIG_TRAFFIC}
${SIG_CUSTOMEP}
${SIG_PLANE}

You are fixing blockers in internal/mockplane (and, only if a finding names them, internal/router,
internal/authpreset). HARD RULE 6 is the one to be most careful about: never "fix" a hash mismatch by
touching internal/gen/testdata/p1b_body_hashes.json.`)

// -------------------------------------------------------------------- Admin --
// SERIAL for the same reason Serve is: all three write into packages whose other
// files an adjacent agent is also editing (internal/admin/server.go carries both
// route sets), and the wiring agent must see the final setter names.
phase('Admin')

const adminCtx = `${CTX_CORE}${CTX_ADMIN}${CTX_TEST}
THE CONTRACTS THE ENGINE PACKAGES REPORTED (later lines supersede earlier ones):
${enginesContracts}
THE SERVING PATH REPORTED:
${contractsOf(serveOK)}`

const adminLive = await agentSafe(`${adminCtx}${SIG_LIVESTATE}
${SIG_TRAFFIC}
${SIG_WIRE}

YOU OWN, AND NOTHING ELSE:
  internal/admin/livestate_handlers.go — NEW
  internal/admin/traffic_handlers.go — NEW
  internal/admin/livestate_handlers_test.go — NEW
  internal/admin/traffic_handlers_test.go — NEW
  internal/admin/server.go — MODIFY, two fields, two setters and your six routes
  internal/admin/workspace_handlers.go — MODIFY, ONE field on workspaceView (see below)
(There is no workspace_handlers_test.go. The view's shape is asserted from internal/admin/admin_test.go
and spec_handlers_test.go, which belong to earlier phases and are NOT yours: adding a field must not
break them, and if one of them pins the exact field set, say so in "deviations" instead of editing it.)

YOUR TASK — the admin half of the live-state layer and of traffic, per the wire block above (DESIGN §14
lines 852-861).

THE TWO SETTERS, and why they are setters: admin.New(cfg, sessions, ws, db, log) has 5 call sites in 5
files this task does not own, and its second parameter is ALREADY an *auth.Manager named "sessions" —
adding a second session-shaped dependency to that signature is the adjacent-same-name swap this project's
gate has caught before. So:
    func (s *Server) SetLiveState(src LiveStateSource)   // the SAME *livestate.Store the mock plane holds
    func (s *Server) SetTraffic(rec TrafficControl)      // the SAME *traffic.Recorder the mock plane holds
Define both interfaces in this package, narrow, over what you actually call. The traffic REPOSITORY is
different: it is a stateless reader over db, so build it internally exactly as Server already builds
specsRepo and overridesRepo (see the comment on those fields for the rule).
A nil source is not an excuse to panic: with no live-state store wired, the three session routes answer
503 with an honest message; with no recorder, DELETE /traffic still deletes rows but cannot flush, and
says so. cmd/mocker and the acceptance harness both wire them (other agents), so 503 is a test-shape
guard, not the normal path.

THE SESSION ROUTES map one-to-one onto livestate.Store: GET -> List, POST -> Set, DELETE -> Clear. The
POST body is IDENTICAL to the mock plane's {prefix}/state body — and it is identical because BOTH decode
into livestate.Directive itself, which owns its own json tags, its own "*" union and the Scenario field
that exists only so both planes answer 501 for §12's {scenario} KEY rather than "400 unknown action"
(SIG_LIVESTATE). Do NOT declare a second DIRECTIVE struct in this package and do not re-derive the shape
from the wire block:
two hand-written decoders for one shape is the seam this project has been bitten by, and the fix was to
give the type an owner. The 501 for "scenario"/"delay"/"pause" and the 400s are yours to answer the same
way the mock plane's handler does — read it, and report any divergence you cannot remove in "deviations".

THE TRAFFIC ROUTES: GET /traffic (newest first, limit with a sane default and ceiling — say 100 and 500),
GET /traffic/poll?since=<row id>&limit= (oldest first, strictly greater than the cursor, plus lastId so a
poller can chain), DELETE /traffic (FLUSH the recorder first, then delete — otherwise the next flush
resurrects rows the operator just cleared, which is a bug an operator would never be able to explain).
"since" is a row id, never a timestamp: the only index is (workspace_id, id DESC) and ts ties inside a
millisecond. GET /traffic — and only it, never the poll, which a UI hits several times a
minute — also reports rate1m (DESIGN §14 screen 4's "сюда пришло N запросов за минуту"). BOTH report
dropped (the recorder's own counter: a traffic screen that silently omits dropped events lies about what
happened).

ALSO YOURS, one field, and it is what keeps a P1 screen buildable: workspaceView
(internal/admin/workspace_handlers.go) carries the slug but no address, /api/me returns only the user and
the CSRF token, and config forbids MOCKER_ADMIN_HOST from sitting under MOCKER_BASE_DOMAIN — so a browser
cannot derive a workspace's own URL from window.location, and DESIGN §14 screen 4 («Подключить», lines
878-889) is unbuildable by a UI-only slice. Add the workspace's external URL to workspaceView. Everything
you need is spelled out here because internal/config has NO url accessor and NO scheme field — only
IsWorkspaceHost/IsAdminHost, which parse in the other direction:
    scheme := httpx.ForwardedProto(r, cfg.TrustProxy)   // per REQUEST, not per config
    host mode (cfg.Routing == config.RoutingHost):  <scheme>://<slug>.<cfg.BaseDomain>
    path mode (cfg.Routing == config.RoutingPath):  <scheme>://<cfg.AdminHost>/w/<slug>
NO trailing slash in either mode — a UI concatenating url+"/api/v1/widgets" must not produce a double
slash — and carry the PORT from r.Host when it has one, or every dev deployment on :8080 gets an
unreachable address (cfg.BaseDomain cannot hold a port: IsWorkspaceHost strips it before matching).
"/w/" is workspacePathPrefix at internal/server/server.go:26, and it is unexported: declare your own const
with a comment pointing at that line and saying the duplication is deliberate. (internal/admin COULD
import internal/server without a cycle — the reason not to is that the constant is unexported and you own
neither file, not that the import is impossible.) newWorkspaceView is currently a free function with no cfg and four call sites inside the
file you own: making it a method on *Server (or passing cfg and the request) is a signature change you own
end to end. No existing test pins the view's field set — admin_test.go and spec_handlers_test.go both
decode into partial structs — so adding a field is safe, but ADD a test that the URL is right in BOTH
routing modes.

EVERY ROUTE: session cookie + CSRF exactly like the existing ones (read security.go and
override_handlers.go), loadWorkspace for {id}, httpx.Err with an httpx.Code* constant, httpx.JSON for
bodies. Nothing outside /api/.

TESTS, in package admin_test (every existing admin test is; follow internal/admin/override_handlers_test.go
for the stack it builds). Its shared harness, newTestServer in admin_test.go, returns only a handler and
never exposes the *admin.Server, so you cannot call your own setters through it: write your own
p1c2-prefixed builder that constructs admin.New(...) and calls SetLiveState/SetTraffic. Do NOT edit
admin_test.go — it belongs to an earlier phase.
- unauthenticated 401 and missing-CSRF 403 on every state-changing route;
- POST a status directive, GET it back with the remainder, DELETE it and get an empty list;
- 501 for scenario/delay/pause, 400 for a bad status;
- traffic list ordering (newest first) and poll ordering (oldest first) over rows inserted directly;
- poll with since = the last id returns nothing;
- DELETE clears only the addressed workspace;
- a 404 for an id that parses but names no workspace;
- with no live-state store wired: 503, not a panic.`,
  { label: 'admin:live+traffic', phase: 'Admin', model: 'sonnet', schema: SCHEMA })

if (adminLive) logDeviations('admin:live+traffic', [adminLive])
else log('WARNING: admin:live+traffic returned nothing — the next admin agent edits the same server.go and is told to read it from disk.')

const adminEndpoints = await agentSafe(`${adminCtx}${SIG_CUSTOMEP}
${SIG_TRAFFIC}
${SIG_WIRE}
${adminLive ? `The previous stage changed: ${(adminLive.files || []).join(', ')} and reported:\n${(adminLive.contracts || []).join('\n')}` : 'The previous stage reported nothing — read internal/admin/server.go from disk before adding routes to it.'}

YOU OWN, AND NOTHING ELSE:
  internal/admin/endpoint_handlers.go — NEW, the custom-endpoint CRUD
  internal/admin/from_traffic.go — NEW, the two conversions
  internal/admin/endpoint_handlers_test.go — NEW
  internal/admin/from_traffic_test.go — NEW
  internal/specs/repo.go — MODIFY, ONE new method (see below)
  internal/specs/repo_test.go — MODIFY, its test

SERIAL EDIT — a file the previous stage already changed; read it as it is on disk:
  - internal/admin/server.go: your five routes, and the customep repo built internally from db the way
    specsRepo and overridesRepo already are.

YOUR TASK — DESIGN §14's endpoints routes plus the two conversions §14 screen 8 calls «создать правку из
этого ответа» and «создать endpoint из этого запроса». The wire block above is the contract.

THE CONVERSIONS ARE THE POINT OF THIS AGENT, and the two of them resolve their key in DIFFERENT ways.
Getting that backwards produces rows nothing ever reads, with tests that pass.

A traffic row holds the path AS REQUESTED — concrete, with the workspace's settings.basePath glued on:
"/api/v1/widgets/7". Neither target table stores paths that way.

to-override keys on the OPERATION'S OWN TEMPLATE PATH. The mock plane looks an override up as
overrides.OpKey(route.Method, route.Path), and router.Route.Path is documented as "relative — WITHOUT the
base path — exactly as stored in operations.path": the TEMPLATE, "/widgets/{widgetId}". Stripping the
base path off the traffic path gives "/widgets/7", which no route will ever produce as a key — the row
lands orphaned, the merged operations view never shows it, the operator's click does nothing, and a unit
test written against "/widgets/7" passes anyway (and passes VACUOUSLY on any path with no {param} at all,
which is how it would ship green). So: take the row's matched_id, resolve the operation, use ITS Path.
That method does not exist yet — add it, and it is the only reason internal/specs is in your file list:
    // OperationByID returns one operations row by its primary key. The
    // traffic-to-override conversion needs the operation's TEMPLATE path, which
    // is the only key the mock plane's override lookup will ever produce.
    func (r *Repo) OperationByID(ctx context.Context, id int64) (*Operation, error)
Follow the package's existing scan helpers and its ErrNotFound convention (see Repo.Operations and
Repo.ByID). specs.Operation already carries ID, SpecID, Method and Path — everything the conversion needs,
including the SpecID it must compare against ws.SpecID.

to-endpoint keys on the CONCRETE path with the base path stripped, because a custom endpoint is exactly
the literal route somebody asked for: "/api/v1/legacy/ping" becomes "/legacy/ping". internal/traffic
cannot strip a prefix it does not know and internal/overrides may not import internal/workspaces, so the
handler is the only place that can: it reads ws.Settings.BasePath. If the row's path does NOT start with
the current base path (it was changed after the request was recorded), refuse with 409 rather than
guessing — a wrong strip writes a row under a key nobody will look up, or under someone else's.

Write both tests on a workspace whose basePath is NON-EMPTY, or this entire class of defect passes.

to-override refuses (409) when: the row's matched_kind is not "operation" (there is nothing to pin to);
the operation is gone, or its SpecID is not the workspace's own (a re-import orphaned matched_id);
Row.Truncated or Row.Redacted() is true (a pinned body assembled from a cut or redacted body ships the
operator a lie, and this handler is the only place that can tell — internal/traffic pins those as Notes
tokens precisely so you can); or the row's status is one that carries no body. Otherwise it writes a
pinned variant for that status through overrides.Repo — which already bumps the revision inside its own
transaction; do NOT bump it yourself and do NOT call workspaces.Repo.Update (HARD RULE 5). Set
OverrideOn=true EXPLICITLY in what you write: Repo.Put defaults it true only for a NEW row, so pinning
onto an existing switched-off row would otherwise store a body that never serves.

to-endpoint refuses (409) when: an op_overrides row already exists for that (method, relative path) —
DESIGN §8, "правка и кастомный endpoint на один и тот же ключ запрещены на уровне API: иначе правка молча
не действует" — or when customep.Create reports ErrConflict for the canonical path. This cross-table rule
lives HERE and nowhere else: it is the only layer holding both repos. The status rule is PINNED, not yours to choose (two agents
picking differently here is why it is written down): an observed 404 becomes a 200 endpoint carrying the
observed body, or {} when there was none — the operator is creating the endpoint precisely because it was
missing, so re-serving the 404 would be a no-op; ANY OTHER observed status is preserved as it was.

CRUD: GET lists the workspace's endpoints, POST creates one from an explicit {method, path, status, body,
mediaType}, DELETE removes one by id. PUT is P2 (§14 screen 6's editor) — do not add it.

TESTS:
- to-endpoint on a workspace whose basePath is "/api/v1": record a traffic row for "/api/v1/legacy/ping"
  and assert the endpoint is created with the RELATIVE path "/legacy/ping";
- to-override on a truncated row: 409; on a redacted row: 409;
- to-override on a "none" row: 409; on a row whose operation belongs to another spec: 409;
- to-override onto an EXISTING row whose override_on is false: the result is on and serving;
- to-override writes the TEMPLATE key: record a traffic row for "/api/v1/widgets/7" whose matched_id is
  the operation for "/widgets/{widgetId}", convert, and assert the stored key is
  overrides.OpKey("GET", "/widgets/{widgetId}") — then read it back through the operations API and prove
  the merged view shows it;
- to-endpoint on a 404 row creates an endpoint whose path is relative and whose canonical path is right;
- to-endpoint when an op_overrides row already holds that key: 409, and nothing written (assert the
  revision did not move);
- create/list/delete, and 404 for an unknown endpoint id;
- 409 for a duplicate canonical path;
- auth and CSRF on every state-changing route.`,
  { label: 'admin:endpoints', phase: 'Admin', model: 'sonnet', schema: SCHEMA })

if (adminEndpoints) logDeviations('admin:endpoints', [adminEndpoints])
else log('WARNING: admin:endpoints returned nothing.')

const wire = await agentSafe(`${adminCtx}${SIG_LIVESTATE}
${SIG_TRAFFIC}
${SIG_CUSTOMEP}
${SIG_PLANE}
${[adminLive, adminEndpoints].filter(Boolean).map((r) => `An earlier Admin stage changed ${(r.files || []).join(', ')} and reported:\n${(r.contracts || []).join('\n')}`).join('\n') || 'Both earlier Admin stages reported nothing — read internal/admin/server.go from disk for the real setter names.'}

YOU OWN, AND NOTHING ELSE:
  cmd/mocker/main.go — MODIFY

YOUR TASK — put this slice into the real binary. Nothing else in this run does, and a nil left here ships
a dead feature with a fully green suite: HANDOFF.md records that near-miss twice (P1a's specs repo, P1c-1's
SetOverrides), and both times the tests passed because the TESTS wired it and main.go did not.

WHAT MUST HAPPEN IN run(), in cmd/mocker/main.go, in this order:
1. After the store is open and migrated and BEFORE the server starts serving, construct:
     - the live-state store: livestate.NewStore(livestate.DefaultTTL, nil)
     - the traffic recorder: traffic.NewRecorder(db, log, traffic.Options{MaxBody: cfg.TrafficMaxBody,
       Retention: cfg.TrafficRetention})
     - the custom-endpoint repo: customep.NewRepo(db)
   Use whatever the reported contracts actually say if they differ from this line — the code wins.
2. Wire ALL FIVE: mockPlane.SetLiveState(...), mockPlane.SetTraffic(...),
   mockPlane.SetCustomEndpoints(...), adminSrv.SetLiveState(...), adminSrv.SetTraffic(...). The live-state
   store and the recorder must be the SAME INSTANCE on both planes — two stores means the admin UI shows
   directives the router never sees, and two recorders means the admin DELETE flushes a queue that is not
   the one filling up. Put a comment on that line saying so.
3. Start the recorder's writer goroutine and make shutdown wait for it — but NOT by copying the janitor's
   lifetime, which would be wrong in a way that only shows in production. The janitor dies with the
   signal context; that context is Done at the START of shutdown, while httpServer.Shutdown is still
   draining in-flight requests for up to shutdownDrain. A recorder bound to the same context has already
   returned while requests are still being answered, so their Record calls land in a channel nobody
   reads — exactly the "the last few requests never appeared" bug. Give the recorder its OWN
   cancellation, and cancel it on BOTH of run()'s exit paths — after httpServer.Shutdown returns on the
   signal path, and immediately in the "<-serveErr" branch, where Shutdown is never called at all. Join it
   next to <-janitorDone. Without that second cancel, a listener error hangs the process forever on that
   join, waiting for a goroutine nothing will ever stop. The final flush must
   run on a context that is NOT the cancelled one (context.WithoutCancel with a timeout) — database/sql
   fails immediately on a cancelled context, so a flush issued on it writes nothing at all.
4. Add livestate sweeping to the existing janitor loop (runJanitor) so an abandoned workspace's directives
   do not live forever. Log what it dropped the same way the session purge next to it does (that one logs
   at Info — match it rather than guessing a level).

WHAT YOU MUST NOT DO: change any signature (mockplane.New has 26 call sites in 9 files, admin.New has 5 in
5 — the setters exist precisely so neither changes), add a flag, add an environment variable, or touch any
package other than cmd.

THE ONE FAILURE THIS TASK ACTUALLY HAS, and it has happened on this very run: an agent reported this task
complete while cmd/mocker/main.go was never opened. Every setter existed, every test passed, and the
binary shipped with three nil sources. So before you report anything, run this and PASTE ITS OUTPUT
VERBATIM into "verified" as the first line:
    rg --max-columns=200 --max-columns-preview -n 'SetLiveState|SetTraffic|SetCustomEndpoints|NewRecorder|livestate.NewStore|customep.NewRepo' cmd/mocker/main.go
If it prints nothing, you have not done the task, regardless of what else you did. If it prints fewer than
five wirings plus the constructors, you have done part of it.

VERIFY LIKE THIS, and paste the output:
- "go build ./... && go vet ./..." clean;
- "go test ./... -race -count=1" clean;
- "go list -deps ./cmd/mocker | grep santhosh" prints NOTHING;
- grep the wiring back out of the binary's source: "rg -n 'SetLiveState|SetTraffic|SetCustomEndpoints|NewRecorder|NewStore' cmd/mocker/main.go" must show all five wirings and the two constructors;
- run the binary against a temp database and hit it: start it with MOCKER_DEV=1 and a temp MOCKER_DB_PATH
  on a free port, create nothing, and confirm it starts, answers /healthz, and shuts down cleanly on
  SIGTERM with no goroutine complaining. Paste what you saw. If that is impractical in your environment,
  say so plainly rather than reporting a start you did not observe.`,
  { label: 'wire', phase: 'Admin', model: 'sonnet', schema: SCHEMA })

if (wire) logDeviations('wire', [wire])
else log('WARNING: the wiring agent returned nothing — assume the binary may still hold nil sources and check cmd/mocker/main.go in the review.')

const adminOK = [adminLive, adminEndpoints, wire].filter(Boolean)

// ------------------------------------------------------------------ Gate 3 --
phase('Gate 3')

const gate3Paths = 'internal/admin internal/specs internal/workspaces cmd internal/mockplane go.mod go.sum'
const gate3Ctx = gateCtx(gate3Paths)

const gate3 = await runGate('Gate 3', [
  {
    label: 'gate3:correctness',
    prompt: `${gate3Ctx}

The admin surface just gained the live-state routes, the traffic routes, the custom-endpoint CRUD and the
two "create from a traffic row" conversions; cmd/mocker/main.go was wired.
${SIG_WIRE}

YOUR VECTOR: does the admin plane do what it says, and is the binary actually wired?
- THE WIRING FIRST, because it is the cheapest thing to get wrong and the most expensive to ship:
  "rg -n 'SetLiveState|SetTraffic|SetCustomEndpoints' cmd/mocker/main.go" — all five calls present? Is it
  the SAME live-state store and the SAME recorder on both planes (two instances is a silent, fully-green
  failure)? Is the recorder's Run goroutine started, and does shutdown wait for its final flush? Is
  livestate swept anywhere?
- The two key resolutions, which are DIFFERENT and are the likeliest thing to be wrong: to-override keys
  on the operation's TEMPLATE path (resolved through traffic.matched_id — "/widgets/{widgetId}") and
  strips nothing; to-endpoint strips ws.Settings.BasePath from the concrete path ("/api/v1/legacy/ping" ->
  "/legacy/ping"). Build a workspace with basePath "/api/v1" and check what each one actually writes. A
  to-override keyed "/widgets/7" is dead on arrival while its own test passes; and if either test uses an
  EMPTY basePath, that alone is a blocker.
- to-override's refusals: truncated, redacted, matched_kind != "operation". Does the handler actually know
  the body was redacted, or does it only check the truncated flag?
- to-endpoint's cross-table refusal (an op_overrides row on the same key) — DESIGN §8 requires it, and it
  cannot live in either repo.
- Does DELETE /traffic flush before deleting?
- Does the poll cursor work as a chain: poll, use lastId, poll again, no duplicates and no gaps?
- Two decoders for one wire shape: does the admin POST /session accept exactly what the mock plane's
  {prefix}/state accepts? Diff the two decoders and report any field, default or error code that differs.
- Every state-changing route: session + CSRF, and a 404 (not a 500) for an id that names nothing.
- Run "go test ./... -race -count=1" and report every SKIP.`,
  },
  {
    label: 'gate3:security',
    prompt: `${gate3Ctx}

YOUR VECTOR: security of the admin surface and of what the conversions can be made to write. DESIGN §15:
the admin password is SHARED, so "an admin did it" is a low bar; any logged-in user can already reach any
workspace, which is accepted — what is NOT accepted is a route that escapes that model or writes something
nobody could have written directly.
- The conversions write op_overrides and custom_endpoints rows from data an UNAUTHENTICATED party
  controls: a traffic row is whatever some caller sent to the mock plane. Trace what an attacker who can
  hit the mock plane (everyone in the contour) can get an admin to pin with one click: a body with a
  script tag, a media type that makes it browser-executable on a workspace host, a header that splits the
  response, a body larger than MOCKER_MAX_RESPONSE, a path with "../" or "%2F" in it, a path that
  canonically collides with an existing route. Which of those does the write path already reject
  (overrides.validateVariant, customep validation, dangerousPinnedMediaType), and which reaches storage?
- The path stripping, which happens in to-endpoint ONLY (to-override strips nothing — it keys on the
  operation's template path, resolved through matched_id): what happens when the traffic path does NOT
  start with the current base path, because basePath was changed after the request was recorded? A wrong
  strip writes a custom endpoint under a path nobody will ever request, or one that shadows an existing
  route.
- Traffic exposure: the admin API now hands out stored request headers and bodies. Confirm by reading the
  actual stored bytes in a test that an Authorization header and a password field cannot come back out.
  §15's rule is that auth-endpoint bodies are never stored at all — verify that end to end, not by reading
  the redaction unit test.
- The polling endpoints: can they be made expensive? A limit an attacker chooses, an offset scan, a
  rate1m query without the index, a poll that returns the whole table when "since" is negative or absent.
- CSRF and Origin: does every new state-changing route go through enforceCSRF? Is any new route outside
  /api/? Does any of them accept a simple-CORS content type that would let a cross-site form post reach it
  (see security.go's own note on the strict admin parser)?
- cmd/mocker: does the new wiring log anything sensitive at startup (a signing key, a password hash, a
  database path with credentials)?
- "go list -deps ./cmd/mocker | grep santhosh" prints nothing; "git diff HEAD -- go.mod go.sum" is empty.
Report each finding as a concrete attack: what is sent, what is stored, what comes back.`,
  },
], `${adminCtx}${SIG_WIRE}
${SIG_CUSTOMEP}
${SIG_TRAFFIC}
${SIG_LIVESTATE}

You are fixing blockers in internal/admin and cmd/mocker/main.go.`)

// ------------------------------------------------------------------- Accept --
phase('Accept')

const acceptE2E = await agentSafe(`${CTX_CORE}${CTX_ADMIN}${CTX_TEST}${SIG_WIRE}
${SIG_LIVESTATE}
THE CONTRACTS THIS RUN PRODUCED (later lines supersede earlier ones):
${contractsOf([...enginesOK, gate1.fix, ...serveOK, gate2.fix, ...adminOK, gate3.fix].filter(Boolean))}

YOU OWN, AND NOTHING ELSE:
  internal/server/p1c2_test.go — NEW

YOUR TASK — prove DESIGN §19 line 1111's criterion for this slice, end to end, through the real
dispatcher, in one process: "фронт логинится, проходит read-only сценарий и видит согласованные список и
карточку". Slice 1 proved the login half (internal/server/p1c_test.go, TestP1c_FrontendLogsIn — read it
first: it is the shape you are extending, and buildP1cStack is the harness you are copying).

THE HARNESS: write buildP1c2Stack, a copy of buildP1cStack that ALSO wires the three plane setters and
the two admin setters with the SAME live-state store and the SAME traffic recorder — exactly what
cmd/mocker/main.go does. Start the recorder's Run goroutine on t.Context() and register a t.Cleanup that
flushes it. Every helper you add carries a p1c2 prefix; the package already declares testConfig,
testLogger, buildStack, buildStackWithSpecs, buildP1cStack, do, jsonRequest, login, fakePlane and the
whole p1c* family, and you may not redeclare any of them.

THE FIXTURE — an inline OAS 3.0 document in this file (NOT ${SPEC_PATH}: internal/testspec SKIPS when the
real document is absent, and a criterion that vanishes in a fresh clone is not a criterion). It must
declare, at minimum:
  GET  /widgets            with query parameters "limit", "offset" AND "status"; "status" must ALSO be a
                           property of the item schema, or internal/gen's postfilter (list.go:419, rule 6)
                           ignores it and your filter assertion silently tests nothing. Give that property
                           an enum of at least THREE values and NO "example": a single-valued enum or an
                           example makes every generated item identical, and then "the filtered set
                           differs from the unfiltered one" fails for a reason that has nothing to do
                           with filtering;
  GET  /widgets/{widgetId} the detail route of the same family, same item schema;
  POST /widgets            a 200 and a 409, so a when[] has two statuses to choose between;
  GET  /auth/me            on an auth trigger path, for the body-suppression check.
Set the workspace's basePath to "/api/v1" (PATCH the workspace settings — check what
internal/admin/workspace_handlers.go accepts). A NON-EMPTY base path is not decoration: the traffic-to-
override conversion is the one seam where the requested path and the stored relative path meet, and with
an empty base path a broken implementation passes.

WHAT TO ASSERT — each one OBSERVED, never inferred:
1. list/card agreement: GET /api/v1/widgets returns N items; take the third; GET
   /api/v1/widgets/{its id} returns a card whose fields EQUAL that row's (compare the decoded values,
   field by field, not a substring).
2. pagination: ?offset=2&limit=2 returns exactly the items at global positions 2 and 3 of the unpaged list
   — same ids, same values.
3. filtering: ?status=<a value that appears in some items> returns only matching items AND a DIFFERENT set
   from the unfiltered call. Assert both halves: "only matching" alone passes when the filter is ignored
   and every item happens to match.
4. when[]: PUT an override on POST /widgets binding a 409 variant whose when[] is
   [{"in":"body","name":"name","op":"equals","value":"taken"}] with a pinned body. Posting {"name":"taken"}
   answers 409 with that body; posting {"name":"free"} answers the 200. Send the second request with NO
   Content-Type as well, to pin DESIGN §8's tolerance.
5. live state: POST http://<host>/__mocker/state forcing 503 on GET /widgets. The next GET is 503. The
   workspace's revision is IDENTICAL before and after (read it through the admin API both times) — DESIGN
   §12: session counters never touch revision. DELETE /__mocker/state and the next GET is 200 again.
6. fail: a directive with n=2 answers the forced status exactly twice; the third request is normal. Assert
   the remainder reported by GET /__mocker/state between the two.
7. traffic: flush the recorder (never a sleep), then GET /api/workspaces/{id}/traffic/poll?since=0. The
   requests above appear, in id order, with the right statuses and matched kinds. A request carrying
   "Authorization: Bearer secret-value" comes back with that header REDACTED and the literal
   "secret-value" appears nowhere in the response bytes. A POST to /api/v1/auth/me comes back with NO
   request body stored at all.
8. from-traffic: take the traffic row of the GET /api/v1/widgets/{id} call and POST it to
   .../traffic/{tid}/to-override. Afterwards that same GET returns the pinned body BYTE-IDENTICAL to what
   traffic recorded. Assert the KEY too, not only the effect: the override must be stored under the
   operation's TEMPLATE path ("/widgets/{widgetId}"), which is the only key the plane's lookup can
   produce — a row written under the concrete "/widgets/7", or under "/api/v1/widgets/7", is orphaned,
   and the "byte-identical" half of this assertion is what would otherwise catch it only by luck.
9. from-request: GET /api/v1/legacy/ping (a path the document does not have) answers 404 and is recorded
   with matched_kind "none"; POST that row to .../traffic/{tid}/to-endpoint; afterwards the same GET
   answers 200 (the pinned status rule for an observed 404), and the NEXT traffic row for that path
   records matched_kind "custom" with the new endpoint's id. Both halves matter: without the status
   change and the matched_kind, this assertion passes with the conversion deleted, because a 404 before
   and a 404 after look the same. Also assert the 404 body honoured settings.notFoundBody if you set one.
10. ISOLATION, which is what this assertion actually proves — name it that in the test and its comment
    rather than calling it byte-identity: as the FIRST statements of the test, before anything below has
    run, record the bytes of GET /api/v1/widgets on a SECOND workspace that has no directives, no when[]
    and no custom endpoints; at the end, request it again and compare byte for byte. Both captures come
    from the same build, so this cannot see a shift that moved every output consistently — what it CAN
    see, and what it is for, is workspace 1's directives, overrides and endpoints leaking into workspace
    2. The byte-identity-against-HEAD guard is not a test you write: it is the existing internal/mockplane
    corpus continuing to pass unmodified, which is why the two diffs below matter more than this
    assertion.
    Then report goldenIntact from "git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json"
    and testsUnmodified from
    "git diff HEAD -- internal/mockplane/respond_test.go internal/mockplane/plane_test.go internal/mockplane/routes_test.go internal/mockplane/runtime_test.go"
    — that diff must contain ADDED tests plus at most the three edits this phase authorises: TestServeHTTP_NotImplementedYet (repointed at another unimplemented path), TestServeGenerated_OverrideOnFalse_IsInert (its fixture extended with a when[]), and the serveNoRoute call site in TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute (adapted to the slug -> *workspaces.Workspace parameter change). Anything else — a deleted assertion, a loosened comparison, an adapted expectation — is false, and false is a failure, not a note. THE THREE ARE MANDATED
    BY THIS PHASE, so seeing them is the expected state: reporting testsUnmodified=false because they are
    present would send the end-of-run fix agent to revert a signature change the tree cannot compile
    without.

STRUCTURE: give each numbered assertion its own t.Run with a name that says which one it is
("08_to_override_uses_template_key"). A later opus agent verifies the criterion by reading "go test -v"
output, which prints subtest names and nothing else — one flat test function tells it only that something
passed. TWO EXCEPTIONS, both load-bearing:
- assertion 10's BASELINE capture runs BEFORE the first subtest, in the parent function; only its
  comparison is a subtest, and it is the last one. Inside a t.Run it would execute after 1-9, both reads
  would already carry any leakage, and the guard would pass while proving nothing;
- the subtests run SEQUENTIALLY. No t.Parallel here: 5 through 9 share one workspace and depend on each
  other's order (a directive set, then observed, then cleared; a request recorded, then converted).

REPORT, in "measurements", the real numbers your test printed: items in the list, traffic rows polled,
the forced statuses observed, the remainder after each failing request, and how many SKIPs the suite
printed. In "passed", true only if every assertion you were able to RUN passed.
Then run the WHOLE suite: "go test ./... -race -count=1" and report every SKIP it printed in "skips".`,
  { label: 'accept:e2e', phase: 'Accept', model: 'sonnet', schema: ACCEPT_SCHEMA })

if (acceptE2E) logDeviations('accept:e2e', [acceptE2E])
else log('WARNING: accept:e2e returned nothing — the phase criterion has not been observed by anyone yet.')

const acceptDocker = await agentSafe(`${CTX_CORE}${SIG_WIRE}
${acceptE2E ? `The Go acceptance test just written is internal/server/p1c2_test.go; it reported: ${acceptE2E.summary || '(no summary)'}` : 'The Go acceptance test agent reported nothing; read internal/server/p1c2_test.go from disk if it exists.'}

YOU OWN, AND NOTHING ELSE:
  scripts/smoke.sh — MODIFY
  README.md — MODIFY

YOUR TASK — the same criterion through the DEPLOYED artifact. HANDOFF.md's own conclusion from P0: "go
test зелёный ≠ фаза готова", and the docker path found two P0 defects nothing else could. scripts/smoke.sh
already runs 15 checks against a real container (read it whole — it is 554 lines and it is the contract
for how this project checks itself: it builds, waits for health, logs in, imports a spec, attaches it,
hits the mock plane, applies the auth preset, decodes a JWT in shell with base64 -d, pins and unpins an
override, and turns a route off).

ADD checks for this slice, in the same style (same helper functions, same PASS/FAIL lines, same exit
behaviour, and it must still tear down cleanly):
- force a status through POST /__mocker/state and see it; clear it and see the original again;
- a fail directive with n=2: two forced answers, then a normal one;
- a when[] condition: PUT an override with a body-equality condition, then two requests that differ only
  in that field, answering with two different statuses;
- traffic: after the requests above, poll GET /api/workspaces/{id}/traffic/poll?since=0 and assert the
  rows are there, in order, and that an Authorization header sent in one of them comes back redacted;
- create an endpoint from a recorded 404 and then hit it successfully;
- create an override from a recorded response and see the pinned body come back.
Every check must be deterministic. Flush() is not reachable from shell, and DELETE /traffic is no help
because it deletes the very rows the check must see — so poll with a BOUNDED RETRY: the recorder's
DefaultFlushEvery is 500ms, so retry the poll for up to 5 seconds (10 tries) and FAIL with the real
output if the rows never arrive. Never a bare sleep, and never an unbounded loop.

ALSO REPORT THE TWO WRITE-ONCE GUARDS — you share a report schema with the Go acceptance agent, and a
second independent reading of these is worth the two commands:
    git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json                      -> goldenIntact
    git diff HEAD -- internal/mockplane/respond_test.go internal/mockplane/plane_test.go internal/mockplane/routes_test.go internal/mockplane/runtime_test.go
The second must show added tests plus at most the three edits this phase authorises
(TestServeHTTP_NotImplementedYet repointed, TestServeGenerated_OverrideOnFalse_IsInert's fixture extended,
and the serveNoRoute call site in TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute adapted to the
slug -> *workspaces.Workspace parameter) -> testsUnmodified.

Run it: "make smoke". If docker is unavailable, say so and set smoke to "skipped-no-docker" — that is
honest; reporting a pass you did not see is not. If it FAILS, that outranks everything: report the exact
failing check and its output.

README.md: update the sections this slice changes — what the mock plane now does with when[], the
{prefix}/state endpoint (with a curl example that works), the traffic endpoints, custom endpoints, and the
"what is not implemented yet" list. Keep the existing voice and do not rewrite sections this slice did not
touch.`,
  { label: 'accept:docker', phase: 'Accept', model: 'sonnet', schema: ACCEPT_SCHEMA })

if (acceptDocker) logDeviations('accept:docker', [acceptDocker])
else log('WARNING: accept:docker returned nothing — make smoke was not observed.')

// ------------------------------------------------------------------- Review --
phase('Review')

const acceptance = [acceptE2E, acceptDocker].filter(Boolean)
const touched = [...enginesOK, gate1.fix, ...serveOK, gate2.fix, ...adminOK, gate3.fix, ...acceptance]
  .filter(Boolean)
  .flatMap((r) => r.files || [])
  .join(', ')

const criterionState = [
  acceptE2E
    ? `The end-to-end acceptance agent reports passed=${acceptE2E.passed}. Its measurements: ${(acceptE2E.measurements || []).join(' | ') || '(none reported)'}`
    : 'The end-to-end acceptance agent DIED without reporting. Treat the phase criterion as unproven and verify it yourself.',
  acceptDocker
    ? `The docker stack check reports passed=${acceptDocker.passed}, smoke=${acceptDocker.smoke || '(not reported)'}. ${acceptDocker.smoke === 'failed' ? 'A FAILING make smoke outranks every code-level finding — explain it first.' : acceptDocker.smoke === 'skipped-no-docker' ? 'Docker was ABSENT, so the end-to-end criterion is unproven through the deployed artifact rather than broken — do not read this as a failure, and do not read it as a pass either.' : ''}`
    : 'The docker stack check DIED without reporting: make smoke was not observed to pass. Run it yourself if docker is available.',
].join('\n')

const reviewCtx = `Repo root is your CWD. Do not modify any file — you are auditing, not fixing. Running the
test suite is expected.
TO SEE THE DIFF, SCOPED TO THIS SLICE — most files it produced are NEW and plain "git diff HEAD" does not
show untracked files at all:
    git add -N . ; git diff HEAD -- ${P1C2_PATHS}
The ";" and the "." are both deliberate: with "&&" and a path list, one path nobody created makes
"git add -N" exit 128 having added nothing, and you would review an EMPTY diff believing the phase produced
nothing. Intent-to-add stages nothing for commit. Your co-reviewer runs the same command concurrently: on
".git/index.lock: File exists", wait a second and retry.
Do NOT widen it: .claude/workflows and .wf together hold ~300 KB of untracked workflow script.
HEAD is 5cc357f — P1c slice 1, committed, reviewed and green — so this diff is exactly what this slice
produced.
DESIGN.md is Russian and 87 KB — never read it whole. The index: §4 98-135, §6 136-174, §8 246-296,
§9 297-411, §12 509-556, §13 557-820, §14 821-916, §15 917-982, §18 1088-1105, §19 1106-1128.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it, query it with jq only.

P1c slice 2 of mocker was just written: the when[] evaluator (internal/overrides/when.go), the RAM
live-state layer (internal/livestate), traffic recording and polling (internal/traffic), custom endpoints
(internal/customep), all of it wired into the mock plane, the admin API and cmd/mocker.
Files written or changed by this run: ${touched}
${criterionState}
${INTENDED}
Report ONLY defects you can point at with a file and a line. No praise, no style nits that do not change
behaviour. Empty findings array if your vector is clean.`

const reviews = await parallel([
  () => agent(`${reviewCtx}

YOUR VECTOR: correctness across the whole slice, and especially what falls BETWEEN the sections the three
gates each looked at separately.
- Verify the phase criterion YOURSELF rather than trusting the acceptance agent's report. Run
  internal/server/p1c2_test.go and read what it actually asserts: does the list/card comparison compare
  VALUES field by field, does the filter assertion also prove the filtered set DIFFERS from the unfiltered
  one, does the traffic assertion avoid sleeping, and does the from-traffic conversion run on a workspace
  with a NON-EMPTY basePath? An assertion that would pass with the feature deleted is worth a blocker.
- HARD RULE 6, the regression that would be worst to ship. The golden is NOT the guard here — it hashes
  gen.Generator.Body, upstream of every seam this slice touched, so it matches no matter what the serving
  path does; it only proves internal/gen was not edited (check that too: the diff must be empty and
  "git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json" clean). The real guard is the
  existing mockplane corpus:
      git diff HEAD -- internal/mockplane/respond_test.go internal/mockplane/plane_test.go internal/mockplane/routes_test.go internal/mockplane/runtime_test.go
  Only ADDED tests are acceptable. Read every deletion and every changed expectation and decide whether
  the code or the test was wrong; a weakened assertion is a blocker. THREE edits are authorised and are not
  findings: TestServeHTTP_NotImplementedYet (repointed), TestServeGenerated_OverrideOnFalse_IsInert (its
  fixture extended with a when[]), and the serveNoRoute call site in
  TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute (a mechanical signature adaptation). Those
  three plus added tests, nothing else.
- The status-choice order, which three agents touched: a live-state force beats when[], when[] beats
  active_status, and everything after the choice keys off the FINAL status. Construct the case where a
  forced status has its own pinned body and its own recipes and prove both fire.
- The fail counter: exactly N, never N+1, under concurrent requests. Run it with -race and a goroutine
  fan-out if the tests do not already.
- The two decoders of the same wire shape (mock plane {prefix}/state and admin POST /session): diff them.
- Traffic end to end: does a request actually reach the traffic table through the real plane (not a fake
  sink), with the right matched_kind and matched_id for all three cases — operation, custom, none?
- The spec-less runtime path: can any request reach a nil generator or nil variants?
- errors compared with == instead of errors.Is; nil map writes; unchecked type assertions on decoded JSON
  (both traffic rows and override data are user input, and the mock plane is unauthenticated); a context
  value asserted to the wrong type.
- Tests that assert nothing, are skipped in the normal run, or would still pass with the implementation
  deleted. Report every SKIP the suite prints and why.
- "go test ./... -race -count=1".
If docker is available ("docker compose version"), run "make smoke" and report it if it fails. If docker is
absent, say so and move on — it is not a finding.`,
    { label: 'review:correctness', phase: 'Review', model: 'opus', schema: REVIEW_SCHEMA }),

  () => agent(`${reviewCtx}

YOUR VECTOR: security, across the whole slice. Three new surfaces open at once: an UNAUTHENTICATED write
endpoint on the mock plane ({prefix}/state), a store of everything every caller sent (traffic), and a path
from attacker-controlled traffic to stored, permanently-served configuration (the two conversions).
DESIGN §15 (917-982): the mock plane is reachable by everyone in the contour and the admin password is
SHARED; §18 (1088-1105) forbids rate limiting the mock plane, so per-request cost IS the defence.
- {prefix}/state: bound the damage precisely. Memory an anonymous caller can make the process hold
  (directives per workspace x workspaces x TTL), whether a malformed body can panic or allocate
  proportionally to input, whether it can be used to enumerate which workspaces exist, and whether it is
  excluded from traffic (recording it would echo directives into the traffic screen).
- Credentials in traffic: read the ACTUAL stored bytes in a test — an Authorization header, a Cookie
  header, a password field nested inside an array, and any request to an auth path (bodies must not be
  stored at all, §15). Then check the way OUT: the admin list/poll responses, the log lines, the error
  messages, and what a conversion writes into op_overrides. A redaction that happens on read and not on
  write is a database full of secrets.
- The conversions as an attack: an anonymous caller controls the request AND the response body a
  conversion later pins (through a generated route they can shape with query parameters, or through a 404
  they invent). What can they get pinned? A text/html body served from a workspace host; a header that
  splits the response; a body over MOCKER_MAX_RESPONSE; a path traversing with ../ or %2F; a canonical
  path that shadows a real operation. Which write-path validation stops each one — name it — and which
  reaches storage.
- The signing key (domain.AuthSettings.SigningKey). Be precise about the invariant, because it is
  narrower than it sounds: the key IS already returned to an authenticated admin inside workspaceView
  (internal/admin/workspace_handlers.go:41 embeds domain.Settings, and settings.go declares
  SigningKey with a plain json tag). That is pre-existing, out of this slice's scope, and NOT a finding —
  and do not propose adding json:"-", which MarshalJSONStable also honours and which would silently
  destroy the stored key on the next settings write. What this slice must not do is leak it anywhere NEW:
  into a traffic row, a directive list, a custom-endpoint body, a log line, an error message, or anything
  the UNAUTHENTICATED mock plane returns. Trace those paths.
- Cost: the largest single response, the largest single traffic row, and the largest total memory one
  in-flight request can hold, each measured, not guessed. The tee, the request buffer and the recorder
  queue all add per-request bytes.
- Panic reachability from an unauthenticated request through any new code path.
- The admin plane: CSRF on every state-changing route, no route outside /api/, no new bypass in the
  middleware chain, no unbounded limit parameter.
- "go list -deps ./cmd/mocker | grep santhosh" must print nothing. "git diff HEAD -- go.mod go.sum" must
  be empty. Still no http.Get / http.Client.Do / net.Dial anywhere in the tree — this slice was explicitly
  forbidden to add the first outbound client.
Do NOT run "make smoke" — the correctness reviewer may run it, you two run concurrently, and it rewrites
.env and runs "docker compose down -v" on entry and in its exit trap, so a second run manufactures a
phantom failure.
Report each finding as a concrete attack: what the attacker sends, what they get.`,
    { label: 'review:security', phase: 'Review', model: 'opus', schema: REVIEW_SCHEMA }),
])

const findings = reviews.filter(Boolean).flatMap((r) => r.findings || [])
for (const [i, r] of reviews.entries()) {
  if ((r?.findings || []).length >= 40) log(`WARNING: ${['review:correctness', 'review:security'][i]} returned the schema maximum of 40 findings — probably TRUNCATED.`)
  if (!r) log(`WARNING: ${['review:correctness', 'review:security'][i]} returned nothing — that vector did NOT run and the slice is unreviewed along it.`)
}
const minors = findings.filter((f) => f.severity === 'minor')
for (const m of minors) log(`minor: ${m.file}:${m.line} — ${m.summary}`)

// The acceptance agent reports both write-once guards. Declared-but-never-read
// is how a guard becomes decoration: log them, and turn a broken one into a
// finding the fix round actually receives.
const guardFindings = []
if (acceptE2E) {
  log(`Acceptance guards: goldenIntact=${acceptE2E.goldenIntact}, testsUnmodified=${acceptE2E.testsUnmodified}`)
  for (const s_ of acceptE2E.skips || []) log(`accept:e2e SKIP: ${s_}`)
  if (acceptE2E.goldenIntact === false) guardFindings.push({ file: 'internal/gen/testdata/p1b_body_hashes.json', line: 1, severity: 'blocker', summary: 'the write-once 419-hash golden was modified during the run', failure: 'a regenerated golden keeps the assertion and destroys its meaning — it reads as a pass', fix: 'restore it from HEAD (git checkout HEAD -- internal/gen/testdata/p1b_body_hashes.json) and fix whatever the hashes disagreed with' })
  if (acceptE2E.testsUnmodified === false) guardFindings.push({ file: 'internal/mockplane', line: 1, severity: 'blocker', summary: "an existing mockplane test assertion was deleted or loosened — this slice's actual regression guard", failure: 'HARD RULE 6 is enforced by that corpus; an adapted expectation makes the guard fit the new behaviour instead of checking it', fix: 'restore the assertion and fix the code, keeping only the three authorised edits' })
}
// Separate block on purpose: the two agents die independently, and reading
// acceptE2E's fields from inside a check on acceptDocker is a TypeError the
// moment one survives and the other does not.
if (acceptDocker) {
  for (const s_ of acceptDocker.skips || []) log(`accept:docker SKIP: ${s_}`)
}
if (guardFindings.length) log(`${guardFindings.length} guard violation(s) carried into the fix list as blockers.`)

const gateMajors = [...gate1.findings, ...gate2.findings, ...gate3.findings].filter((f) => f.severity === 'major')
const gateSurvivors = [...gate1.unresolved, ...gate2.unresolved, ...gate3.unresolved]
// Deduped: a gate blocker that survived its re-review and was then independently
// re-raised by the whole-slice review would otherwise reach the fix agent twice,
// as two numbered items describing one defect.
const seenFinding = new Set()
const actionable = [...guardFindings, ...gateSurvivors, ...gateMajors, ...findings.filter((f) => f.severity === 'blocker' || f.severity === 'major')]
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

const unreviewed = [
  ...(gate1.dead || []), ...(gate2.dead || []), ...(gate3.dead || []),
  ...['review:correctness', 'review:security'].filter((_, i) => !reviews[i]),
]
// A dead PRODUCER is not the same as a dead reviewer and must not leave the run
// as a log line nobody reads: a run where serve:traffic or wire died returns a
// structurally normal object with a slightly shorter file list.
const deadProducers = [
  ...(enginesWhen ? [] : ['engines:when']),
  ...['engines:livestate', 'engines:traffic', 'engines:customep'].filter((_, i) => !engines[i]),
  ...[['serve:choose', serveChoose], ['serve:traffic', serveTraffic], ['serve:custom', serveCustom],
      ['admin:live+traffic', adminLive], ['admin:endpoints', adminEndpoints], ['wire', wire],
      ['accept:e2e', acceptE2E], ['accept:docker', acceptDocker]].filter(([, r]) => !r).map(([n]) => n),
]
if (deadProducers.length) log(`WARNING: ${deadProducers.length} PRODUCER agent(s) returned nothing: ${deadProducers.join(', ')}. Their work may be on disk — the tree is the witness, not the report — but nothing downstream was written against their reported contracts.`)
if (unreviewed.length) log(`WARNING: ${unreviewed.length} review vector(s) NEVER RAN: ${unreviewed.join(', ')}. Whatever they cover is unaudited — an empty findings list from this run is not evidence along those vectors.`)

// criterionAtAcceptance is a SNAPSHOT taken BEFORE any fix round, and its name
// says so. P1c-1 returned criterionProven:false on a run whose criterion passed,
// because the flag was derived from the acceptance agent that ran before the fix
// round which closed the failures it had reported. The end-of-run verdict is
// computed from the LAST step that observed the tree — the verifier — further
// down.
const criterionAtAcceptance = acceptE2E ? Boolean(acceptE2E.passed) : null
if (criterionAtAcceptance !== true) log('NOTE: at ACCEPTANCE time the criterion was not observed to pass (the agent died, or reported passed=false). This is a snapshot from before any fix round — the end-of-run verdict comes from the verifier.')

if (outstanding.length === 0) {
  // Nothing to fix — but the run must still END with somebody looking at the
  // tree. Without this, criterionAtEnd would be inherited from the acceptance
  // agent, i.e. from the same sonnet that wrote the acceptance test grading its
  // own work, and the two write-once guards would leave the run as free text.
  log('Nothing actionable: skipping Fix. Running ONE final verification pass so the run ends on an observation rather than on a self-report.')
  phase('Final check')
  const finalCheck = await agentSafe(`Repo root is your CWD. Do not modify any file.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it; query it with jq only.
DESIGN.md is 87 KB — never read it whole (§4 98-135, §12 509-556, §14 821-916, §19 1106-1128).
${INTENDED}

Two reviewers audited P1c slice 2 of mocker and raised nothing actionable. Your job is to confirm that
independently, against the tree as it stands — not to re-review it.

1. Run and report the REAL output:
     test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
   "green" is true only if all four are clean. List every SKIP with its printed reason in "skips".
2. "docker compose version"; if it succeeds, run "make smoke" and set "smoke" accordingly, else
   "skipped-no-docker". A failing smoke means green is false.
3. THE PHASE CRITERION, observed by you: run internal/server/p1c2_test.go with -v and read what actually
   passed — the list and the card agreeing, the filtered list DIFFERING from the unfiltered one, a forced
   status arriving and clearing with NO revision bump, a fail counter that stops after exactly N, the
   traffic poll returning the requests with the Authorization header redacted, and both from-traffic
   conversions on a workspace whose basePath is not empty (the override must be keyed by the operation's
   TEMPLATE path). Set "criterion" to observed-passing, observed-failing or not-observed.
4. The two write-once guards:
     git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json          -> goldenIntact
     git diff HEAD -- internal/mockplane/respond_test.go internal/mockplane/plane_test.go internal/mockplane/routes_test.go internal/mockplane/runtime_test.go
   The second must show ADDED tests plus AT MOST the three authorised edits (fewer is fine — they are a
   ceiling, not a checklist): TestServeHTTP_NotImplementedYet
   (repointed at another unimplemented path), TestServeGenerated_OverrideOnFalse_IsInert (its fixture
   extended with a when[]), and the serveNoRoute call site in
   TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute (adapted to the slug -> *workspaces.Workspace
   parameter change). Anything else — a deleted assertion, a loosened comparison, an "adapted"
   expectation — means the slice's own regression guard was dismantled: report it in "unresolved" as item
   1 and set green false.

"unresolved" is empty unless step 4 or step 3 found something.`,
    { label: 'verify:final', phase: 'Final check', model: 'opus', schema: VERIFY_SCHEMA })

  if (!finalCheck) log('WARNING: the final verification returned nothing. The run ends unobserved: treat green and the criterion as unknown, not as passing.')
  else log(`Final check: green=${finalCheck.green}, smoke=${finalCheck.smoke}, criterion=${finalCheck.criterion}, goldenIntact=${finalCheck.goldenIntact}`)
  for (const s_ of (finalCheck && finalCheck.skips) || []) log(`Final check SKIP: ${s_}`)

  return {
    phase: 'P1c-2',
    acceptance: acceptance.map((a) => ({ files: a.files, passed: a.passed, smoke: a.smoke, measurements: a.measurements })),
    findings: findings.length,
    actionable: 0,
    rounds: finalCheck ? [{ round: 0, green: finalCheck.green, smoke: finalCheck.smoke, criterion: finalCheck.criterion, goldenIntact: finalCheck.goldenIntact, skips: finalCheck.skips, unresolved: finalCheck.unresolved }] : [],
    stillOpen: (finalCheck && (finalCheck.unresolved || []).map((u) => u.why)) || [],
    unreviewed,
    deadProducers,
    criterionAtAcceptance,
    criterionAtEnd: finalCheck ? finalCheck.criterion : 'not-observed',
    goldenIntact: finalCheck ? finalCheck.goldenIntact : null,
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
// duplicate. This is the shape P0, P1a, P1b and P1c-1 all shipped.
const rounds = []
let lastOutput = ''
let lastVerdict = null
for (let round = 1; round <= 2 && outstanding.length > 0; round++) {
  phase('Fix')
  const list_ = outstanding
    .map((f, i) => `${i + 1}. [${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}\n   fix: ${f.fix}`)
    .join('\n')

  const fixed = await agentSafe(`${CTX_CORE}${CTX_SERVE}${INTENDED}

HARD RULE 1 DOES NOT APPLY TO YOU: you run alone and own every file this slice touched. HARD RULES 2 (no
new runtime dependency), 3 (no migration), 4 (never read the big files whole), 5 (no nested db.Write) and
6 (no directive, no condition, no custom endpoint must mean byte-identical output — and the golden is
WRITE-ONCE) STILL HOLD.

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
  never relax the byte-identity assertion, never turn a traffic assertion into a sleep, and NEVER
  regenerate internal/gen/testdata/p1b_body_hashes.json (MOCKER_REGENERATE_GOLDEN=1 is forbidden to you).
- Finish with all four clean and paste the real output of the last one into "verified":
    test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
  Then, only if "docker compose version" succeeds, run "make smoke" and paste its result too.`,
    { label: `fix:round${round}`, phase: 'Fix', model: 'sonnet', schema: SCHEMA })

  if (fixed) logDeviations(`Fix round ${round}`, [fixed])
  else log(`Fix round ${round}: the fix agent returned NOTHING. Its work may be on disk, but nothing was reported — the verifier below is checking a tree that may be untouched, and every finding it calls "still open" should be read that way.`)

  phase('Verify')
  const verdict = await agentSafe(`Repo root is your CWD. Do not modify any file.
The acceptance document ${SPEC_PATH} is 347 KB — never Read or cat it; query it with jq only.
DESIGN.md is 87 KB — never read it whole (§4 98-135, §12 509-556, §13 557-820, §14 821-916, §19 1106-1128).
${INTENDED}

A fix agent just claimed to have addressed the findings below. Its own report is self-assessed, so check it.
FOUR things, in this order:

1. Run, and report the REAL output:
     test -z "$(gofmt -l .)"   /   go build ./...   /   go vet ./...   /   go test ./... -race -count=1
   "green" is true only if all FOUR are clean. List every test that SKIPPED and why, in "skips" — a phase
   criterion that only runs under an environment variable is not a criterion.

2. Run "docker compose version". If it succeeds, run "make smoke" and set "smoke" to passed or failed; if
   docker is absent, set it to "skipped-no-docker". A skipped smoke does NOT make green false; a FAILING
   smoke means the phase criterion or an earlier phase's check broke and green must be false.

3. THE PHASE CRITERION, observed by YOU in the tree as it stands now — this is the field the run's summary
   is computed from, so do not infer it from a green build. Run internal/server/p1c2_test.go with -v and
   read what passed: the list and the card agreeing, the filtered list differing from the unfiltered one,
   a forced status arriving and clearing WITHOUT a revision bump, a fail counter that stops after exactly
   N, the traffic poll returning the requests with the Authorization header redacted, and the two
   from-traffic conversions working on a workspace whose basePath is NOT empty. Set "criterion" to
   observed-passing, observed-failing or not-observed. Also set the two guard fields, and the
   SECOND is the one that can actually catch this slice:
     goldenIntact    <- git diff --exit-code -- internal/gen/testdata/p1b_body_hashes.json
                        A non-empty diff means the 419-hash guard was regenerated: that reads as a pass
                        and proves nothing. goldenIntact=false and green=false.
     testsUnmodified <- git diff HEAD -- internal/mockplane/respond_test.go internal/mockplane/plane_test.go internal/mockplane/routes_test.go internal/mockplane/runtime_test.go
                        Must be ADDED tests plus at most three authorised edits:
                        TestServeHTTP_NotImplementedYet (repointed), TestServeGenerated_OverrideOnFalse_IsInert
                        (fixture extended with a when[]), and the serveNoRoute call site in
                        TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute (adapted to the
                        slug -> *workspaces.Workspace parameter). Any other deleted assertion, loosened
                        comparison or "adapted" expectation is this slice's regression guard being
                        dismantled to fit new behaviour: testsUnmodified=false and green=false.

4. For EACH numbered finding below, decide: fixed, credibly rebutted, or still open. A fix that compiles is
   not a fix — check the semantics. Specifically distrust: a byte-identity assertion "fixed" by deleting
   it; a traffic assertion "fixed" with a sleep; a conversion test "fixed" by emptying the basePath; a
   concurrency finding "fixed" by a mutex on the hot path; a panic "fixed" by a recover; an unauthenticated
   endpoint "fixed" by adding a check the test bypasses; the test-only JSON Schema dependency leaking into
   ./cmd/mocker ("go list -deps ./cmd/mocker | grep santhosh" must print nothing).

${outstanding.map((f, i) => `${i + 1}. [${f.severity}] ${f.file}:${f.line} — ${f.summary}\n   fails when: ${f.failure}`).join('\n')}

List in "unresolved" ONLY the numbers that are still open, with why. The list is renumbered from 1 every
round: use the numbers as given above.`,
    { label: `verify:round${round}`, phase: 'Verify', model: 'opus', schema: VERIFY_SCHEMA })

  if (!verdict) {
    log(`Verify round ${round} returned nothing. Treating the round as unverified and stopping the loop rather than declaring a pass nobody checked.`)
    rounds.push({ round, green: null, smoke: null, criterion: 'not-observed', unresolved: 'verifier died' })
    // Clear it: a round-1 verdict describes a tree that round ${round}'s fix
    // agent has since edited, and reporting it as the end-of-run observation is
    // the stale-snapshot defect this script fixed everywhere else.
    lastVerdict = null
    break
  }

  lastVerdict = verdict
  rounds.push({ round, green: verdict.green, smoke: verdict.smoke, criterion: verdict.criterion, goldenIntact: verdict.goldenIntact, skips: verdict.skips, unresolved: verdict.unresolved })
  lastOutput = verdict.green ? '' : verdict.output || ''
  log(`Verify round ${round}: green=${verdict.green}, smoke=${verdict.smoke}, criterion=${verdict.criterion}, goldenIntact=${verdict.goldenIntact}, still open=${(verdict.unresolved || []).length}`)
  for (const s of verdict.skips || []) log(`Verify round ${round} SKIP: ${s}`)
  if (verdict.goldenIntact === false) log('BLOCKER AT THE END OF THE RUN: internal/gen/testdata/p1b_body_hashes.json was modified. The regression guard is now vacuous — restore it from HEAD before anything is committed.')

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
  phase: 'P1c-2',
  acceptance: acceptance.map((a) => ({ files: a.files, passed: a.passed, smoke: a.smoke, measurements: a.measurements })),
  findings: findings.length,
  actionable: actionable.length,
  rounds,
  stillOpen: outstanding.map((f) => `${f.file}:${f.line} — ${f.summary}`),
  unreviewed,
  deadProducers,
  // Two fields, deliberately: the first is a snapshot from before any fix round,
  // the second is what the LAST step that actually looked at the tree reported.
  criterionAtAcceptance,
  criterionAtEnd: lastVerdict ? lastVerdict.criterion : 'not-observed',
  goldenIntact: lastVerdict ? lastVerdict.goldenIntact : null,
}

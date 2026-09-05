# Streaming: the admin feed, SSE and WebSocket endpoints, connections, screens — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

`internal/stream` (`P6a`, 2026-09-02) is the first line of streaming code in
this tree and the admin plane's traffic feed over Server-Sent Events —
`GET /api/workspaces/{id}/traffic/stream`, with `GET .../traffic` (the tail)
and `GET .../traffic/poll` (the cursor) left exactly as they were: the three
are three halves of one feed, and the poll is what a client falls back to
whenever the stream refuses or dies. The package owns the CONNECTION
REGISTRY (a process-wide cap of `maxStreamConns = 64`, a constant and not a
variable — file descriptors and memory are properties of the process, and a
per-workspace cap would bound the process by nothing since a workspace costs
one row), the SSE wire (`id: <lastId>`, `event: traffic`, `data:` the SAME
`trafficPollView` JSON the poll route answers with, at most 200 rows per
frame — the screen's own `POLL_LIMIT`, not the poll route's default or
ceiling), the write-deadline exemption through `http.ResponseController`,
the per-frame deadline, the ping ticker, the lifetime and the refusal path —
and knows nothing about sessions, workspaces or repositories: `internal/admin`'s
`stream_handlers.go` resolves those and hands the package two callbacks. The
transport is SSE and not WebSocket permanently for this route (`DESIGN.md`
§30.10): the CSRF guard runs only on state-changing methods, a WebSocket
handshake is a `GET` that CORS does not cover, and a WebSocket feed would be
readable, session-authenticated, by any page in the contour; `EventSource` is
an ordinary `GET` and the admin plane emits no cross-origin allowance, so the
browser blocks the read.

Six things about it cannot be guessed from the code. **Delivery is a NUDGE
plus a read, never a push of rows**: `traffic.Recorder.SetNotifier` hands the
registry to the recorder as a `Notifier`, which is told the distinct workspace
ids of a batch AFTER `writeBatch` committed (a row's identity is the SQLite
id the INSERT assigns, so a row pushed before the batch landed could not be
merged with what the poll and the tail already produced); `internal/traffic`
does not import `internal/stream`. A connection wakes on a nudge for its own
workspace, reads `Repo.Since(cursor)`, and reads AGAIN while the page it just
wrote came back FULL, every iteration passing through the same `select` the
outer wait does — a bare inner loop would hold a connection past its lifetime
and its recheck under steady traffic. The wakeup is a ONE-SLOT channel per
connection, drained BEFORE the read (drained after, a nudge landing between
the read's snapshot and the wait is lost with its row unseen), sent into
without blocking and DROPPED AND COUNTED (`coalescedNudges`) when the slot is
full — that count is the whole of §30's "drop-and-count" here, because no row
is lost by a coalesced nudge, and the channel is never CLOSED, only left to
be collected, because closing it races a `Notify` in flight and panics the
recorder's goroutine. A failed `Repo.Since` does NOT re-arm the slot: the
connection waits for the next nudge, the lifetime being the backstop.
**The cursor is `Last-Event-ID` when present and positive, `?since=`
otherwise** — the browser's own reconnect replays the URL and adds the
header; honouring only one of the two would re-deliver, skip, or leave
`curl` unable to resume. **`maxStreamLifetime` (900 s) is a package `var`,
never a literal in the loop**, so the package's own test can shorten it — and
that test does not run in parallel and restores the value only after the
connection goroutine is JOINED, because a mutable package var read by a
goroutine belonging to another test is exactly what `-race` would only
probably catch. **The session AND the workspace are re-validated on
`MOCKER_STREAM_SESSION_RECHECK`**, the session by a store read the SPA's own
route guard already makes, the workspace by `(created_at, slug)` and never by
id — `workspaces.id` is a reusable rowid, deleting a workspace cascades its
traffic away, and an id-only recheck would keep serving a DELETED
workspace's connection with whatever workspace a later `POST /api/workspaces`
was handed that id; the pair is an identity only to within a second, the
identical residual `internal/checkpoints` has fenced on since `P2c`, accepted
rather than hidden. And the identity check ALSO runs before every read, not
only on the timer: the package's own reissued-id test delivered the
impostor's first batch on the timer-only draft, because a nudge for the new
workspace arrives inside the recheck interval — a read that refuses answers
`stream.ErrRefused` and closes the connection. **The 501 and the 503 are
different statuses on purpose**: `501 streaming_unsupported` is a response
writer that reports `http.ErrNotSupported` on `SetWriteDeadline` (a test
recorder, an in-process loopback, a proxy that unwraps into something that
cannot flush), refused BEFORE a single frame and never a buffered
fallthrough; `503 service_unavailable` is the cap, or the registry closing
for shutdown, or no registry wired — two checks sharing one sentinel both
pass when the other feature is deleted. **The shutdown order is
`liveState.Close()` → `registry.Close()` → the drain → `recorderCancel()`, on
BOTH exit paths of `cmd/mocker/main.go`**, and the listener-error path was
REORDERED to say so — before `P6a` it cancelled the recorder first and
released the live state after, the reverse of the signal path. Live state
first because a handler parked on a pause must be released before its
connection is cancelled; registry before the drain because `Shutdown` waits
for a live SSE connection for the whole drain window; recorder last because
a mock request answered during the drain leaves an event in the recorder's
QUEUE that a cancel before the drain ends would discard — the admin stream's
own request is recorded by nothing, so what the recorder must outlive is
mock traffic, never this slice's connections. `Close()` itself is the
three-step shape §30.7 prescribes — closed flag first (a handshake racing
shutdown is refused, not registered), cancel every connection, WAIT for
their handlers to return — and `internal/testleak`'s ignore list did not
grow for it.

`GET /api/stream/stats` (`{open, cap, refusedCap, refusedUnsupported,
coalescedNudges, byWorkspace}`) is process-wide because the cap is; it is
agent-only (`get_stream_stats`, tool 47) and `coverage.test.ts`'s THIRD
non-probe `EXEMPT` entry. The stream route is NOT exempt: the screen calls
it through the browser's own `EventSource` (`web/src/api/stream.ts`), and
the scanner learned that fifth consumption shape by turning each `{param}`
of the contract's template into a one-segment wildcard, so the natural
template literal matches without the screen being written around the guard.
The screen (`TrafficPage.tsx`) opens the stream from the tail's cursor,
goes live on the FIRST frame (badge «живой поток» — a Russian UI string),
and on ANY `EventSource` error — before or after that frame, including the
server's own 900-second expiry — closes the stream, enters the poll at its
2 s interval (badge «опрос каждые 2 с», with the server's own envelope message
beside it where there is one), retries a replacement stream every 60 s and
leaves the fallback only when THAT stream delivers a frame. The reason
beside the badge comes from ONE raw `fetch` of the same URL that reads the
status and aborts on a success — deliberately not the orval mutator, whose
`res.text()` would await a stream that never ends and hold one of the 64
slots for fifteen minutes on every fallback; a `401` there goes where every
other dead session goes (`notifyUnauthorized`, the same handler `client.ts`
already fires). Clearing the traffic (`DELETE .../traffic`) reopens the
stream with no cursor and the server learns nothing about it — but a cursor
had to SURVIVE a clear for every other client, so `traffic.id` is now
`INTEGER PRIMARY KEY AUTOINCREMENT` (migration `0004_traffic_autoincrement.sql`,
the tree's first REBUILD migration: create, `INSERT … SELECT` every row with
its id, drop, rename, recreate `traffic_ws`; `sqlite_sequence` then sits at
the highest id carried across). Without it SQLite reissues a deleted
highest id, and a second tab, a `curl`, or the browser's own reconnect racing
the clear sits on a connection that looks alive and is permanently empty.
`scripts/migration-check.sh` is the observation `make smoke` cannot make —
it starts from an empty volume — a parent-commit binary's data directory
opened by the current one, every row still there with its id.

**`P6b` (2026-09-02) is the first mock-plane stream: a custom endpoint of
`kind: "sse"`.** A stream is a custom endpoint and nothing else (§30.2) —
never derived from a spec, no fifth response layer — so it inherits the
edit-version compare-and-swap, the auto checkpoint, `routeOff`/`overrideOn`,
the bundle's `endpoints[]` and the checkpoint restore unchanged. Migration
`0005_custom_endpoints_stream.sql` adds `kind` (`'http'` by default, so no
row moves) and `stream` (the whole definition as one JSON document, `NULL`
exactly when `kind = 'http'`, a SQL `CHECK` because that is the one invariant
a hand-run `UPDATE` or a restore could break silently); it is the tree's
second REBUILD migration, and neither UNIQUE index admits `kind` — one path
cannot be both an ordinary `GET` and a stream. The document
(`internal/customep/stream.go`) is `{timeline: {frames: [{delayMs, event,
data}], loop}, tick: {intervalMs, event, schema}, closeWhenDone}`, at least
one of timeline/tick, validated in ONE place both writers and the preview
route reach (`customep.ValidateDraft` is the same function `Create` and
`UpdateExpecting` run) and refused BY NAME, never clamped: the tick floor
is 100 ms, a timeline holds at most 500 frames, a frame waits at most
30 000 ms (the same ceiling a delay directive has), a payload is at most
`MOCKER_MAX_RESPONSE` (`customep.Repo.MaxFrameBytes`, set from config by
both constructors), an `event:` fits an SSE line, and an inline schema
carries no `$ref` because there is no document to resolve one against. An
`sse` row is STRICT — `GET`, no `responses`, `activeStatus` 200 — because a
response map on a stream would quietly never fire, the shape §30.3 forbids
for `when[]`; `kind: "ws"` is refused by name until `P6d`; and a `PUT` that
resends everything but the kind is refused for carrying a stream on an
http row, never silently downgraded.

Serving is a BRANCH in `serveCustom` (`internal/mockplane/stream.go`),
after the session layer and only when no status was forced — a forced 503
answers 503 with no stream, a pause parks the handshake, a delay delays the
handshake and never each frame (§30.4), all of which the existing custom
path already did before the branch. The loop is ONE `select` on the
handler's own goroutine (§30.8: a timeline and a tick start nothing): the
timeline's timer re-armed per frame with that frame's own delay, the tick's
ticker, the ping (`MOCKER_STREAM_PING`, shared with the admin feed), the
lifetime (`MOCKER_STREAM_MAX_LIFETIME`, the mock plane's own), the request
context and the registry's cancel; frames carry `id: <ordinal>` and
`Last-Event-ID` is ignored — a connection copies its definition out at the
handshake and has no server-side state (§30.5), so an edit bumps `revision`
and takes effect on the NEXT connection while an open one runs to
completion on what it opened with. **A tick body is `gen.Body` over the
inline schema**, seeded by the workspace seed, the endpoint id and the tick
ordinal folded through `PathParams` (no new `gen.Request` field) — the same
body at the same ordinal on every connection, and the SECOND `gen.Body`
site in the tree beside `assembleResponse`: `TestAssembleResponseIsTheOnlySeam`
names it, because the seam it guards is a RESPONSE's assembly (recipes,
schemaPatch, pinned-versus-generated, `ref`) and a tick frame has none of it
to duplicate. A frame over `MOCKER_MAX_RESPONSE` is skipped and counted.
The registry is `stream.NewWorkspaceRegistry(cfg.StreamMaxConns)` — a
SECOND instance, capped per workspace, closed by `main.go` in the same step
as the admin feed's, because an unauthenticated plane must not be able to
exhaust the authenticated one's feed by sharing a counter; `0` refuses every
handshake outright; `GET /api/stream/stats` reports it as `mock`. **The
traffic recorder writes ONE row per connection**, when it closes: status
200 (or the forced/refused one), `durationMs` the connection's whole life,
notes `stream:sse,frames:N` (`frames_skipped:M` when any), and `respBody`
NULL under `MOCKER_STREAM_TRAFFIC_FRAMES=off`, the first frame's wire
bytes under `first`, or every frame's under `all` (`A14`, below) — never
the writer's own capture, which would be pings and whatever fit in 8 KiB. The bundle is **v4** (`kind` always, `stream`
`null` for http) and **v3 is refused**, on the owner's own decision that no
deployment exists: a database created before this slice holds checkpoints
and scenarios nothing can decode, and `migration-check.sh` does not exercise
a rollback across that boundary. `POST /api/workspaces/{id}/endpoints/preview`
(`preview_endpoint`, tool 48, the fourth non-probe `EXEMPT` entry) lays the
first ≤ 50 frames of a DRAFT out on one time axis with tick bodies seeded by
endpoint id 0, writes nothing, and carries `maxBytesPerSec`, the estimate of
the amplifier §30.12 wants shown. No screen: the A4 rule ("UI вообще не
нужен делай только MCP", a Russian string quoted as data) applied to §30.14,
recorded in `CARVE-OUTS.md`.

**`P6c` (2026-09-02) is the live-connection surface: three routes under
`/api/workspaces/{id}/connections` — list, close one, push one frame — over
the MOCK plane's registry only, with three MCP tools
(`list_stream_connections`, `close_stream_connection`, `push_stream_frame`,
tools 49–51) and no screen.** Contract 58 → 61, `EXEMPT` six → nine, no
variable, no migration, no bundle change. Five things about it cannot be
guessed from the code. **A connection has an identity now, minted by its
registry**: `stream.Conn.id` is an `int64` counter from 1 per registry,
never reused while the process runs and restarting at 1 after a restart —
an id held across a restart can name a NEW connection, recorded rather than
hidden behind a token; `Conn.SetInfo` (endpoint id, path, kind, the peer as
`httpx.ResolvePeer` renders it) is called by the mock plane between `Open`
and `Handshake`, the admin feed never calls it and is never listed, and
`Registry.Snapshot`/`Lookup` are workspace-scoped and SKIP a connection whose
context is already done. **Where a pushed frame lives is §30.16's first open
question, answered: in the connection, in RAM, nowhere else** — a bounded
channel on the `Conn` (`inboxDepth = 16`, a constant: 16 × 4 MiB is the
worst case and only an authenticated operator can fill it), never
`internal/livestate` (keyed by operation; a frame's target is a connection),
never SQLite, a checkpoint or a bundle, never replayed to a later connection.
**Push is delivered by the connection's OWN loop and the caller waits for the
ordinal**: `Conn.Push` enqueues without blocking (`ErrInboxFull`), the loop
gains one `select` case that writes the frame under the next `id:` with the
same per-frame deadline every other frame has (§30.8: the handler goroutine
stays the only writer), and the pusher waits on a ONE-SLOT buffered reply
with a second non-blocking look before it returns `ErrPushTimeout` (the
frame STAYS queued) or `ErrConnClosed` — `deregister` cancels the connection
context on every exit so no pusher parks behind a returned loop. The admin
handler waits `2 × FrameTimeout` and answers `200 {connectionId, frameId}`,
`409 inbox_full`, `409 connection_closed` or `504 push_timeout`; the MCP tool
uses `lb.do` for exactly that 504 because `toolErr` drops every 5xx message
and this one says "do not resend blindly". The frame body is a timeline
frame's `{event?, data}`, validated by `customep.ValidatePushFrame` — the
same private checks the timeline validator calls, the same words after the
field name. **Close is a compare-and-swap cancel and nothing more, and it is ONE
registry operation** (`Registry.CloseByAdmin(workspaceID, id)` finds, checks
liveness and flips the flag under the registry's own lock — never a
`Lookup` followed by a close, which the diff review showed could answer 204
for a connection that deregistered in between): no final frame (SSE has
none; the client's `EventSource` reconnects as a NEW id), the loser of two
racing DELETEs answers `404 connection_not_found` like a DELETE after
deregistration, no `confirmSlug` on any of the three (a connection is not
workspace data). **The traffic row learns two conditional tokens**:
`pushed:M` (M > 0) and `closed:admin`, beside `stream:sse,frames:N` —
`frames:N` counts pushed frames, since they carry ordinals like every other.
The list envelope is `{open, cap, connections[]}` — the ceiling beside who
holds it, `cap` the same number `stats.mock.cap` reports — with an optional
`?endpointId=` filter that never changes `open`. Both mutating routes joined
`autoCheckpointExcludedNeverTouchesLayer` (12 → 14): a cancel and a RAM
inbox write no row and bump no `revision`.

**`P6d` (2026-09-02) is WebSocket: a custom endpoint of `kind: "ws"`, served
by `github.com/coder/websocket` behind `internal/wsmock` (the one importer,
held by a boundary test), with all four behaviours of §30.3 — timeline and
tick reused from `P6b`, reactive and echo new — three variables (29 → 32:
`MOCKER_STREAM_MAX_FRAME`, `_SEND_BUDGET`, `_ORIGINS`), the CSRF predicate
of §30.10, the CSP sources of §30.14, and the first inbound data this plane
records.** No route, no tool, no migration, no bundle change: contract 61,
tools 51, `EXEMPT` 9. Seven things about it cannot be guessed from the code.
**The document grows two fields, refused by name on `sse`**:
`stream.reactive: [{when, data?, close?: {code, reason?}}]` (at most 100
rules; `when` is `overrides.Condition[]` through the SAME
`ValidateConditions`/`MatchAll` — the inbound frame is the `body`, the
handshake's query and headers are captured once and constant; only a TEXT
frame carrying a JSON OBJECT can match; first match wins; `close.code` is
1000 or 4000–4999, the owner's own addition against the recommendation)
and `stream.echo` (the fallback for an unmatched frame, mirroring the
opcode, never a parallel producer). `customep.ValidateStreamFor(kind, …)` is
the one door; a `ws` row is strict exactly like `sse`. **One extra goroutine
per connection, the reader, and the handler loop stays the ONLY writer**
(§30.8): the reader reads, counts `framesIn`, matches and hands replies to
a per-connection queue bounded in BYTES by `SEND_BUDGET`; a reply over it
is dropped and counted and the loop writes one `{"$gap": N}` text frame
before its next write from that queue (§30.11); a rule's `close` is a
terminal item outside the budget, after which the reader keeps DRAINING
(the peer's half of the closing handshake arrives on that same read) but
stops matching. The reader's context is its OWN, cancelled only after the
closing handshake — the library tears the socket down the instant a Read's
context expires, which would defeat the 1001 close frame an operator's
close or a shutdown promises — while every Write/Ping/Close runs under the
connection context plus one frame timeout, so `Registry.Close` aborts a
blocked write within that. The exit order is: loop returns → `Close(code,
reason)` (peer half read by the reader) → reader context cancelled → reader
JOINED → row → `Release`; goleak's ignore list did not grow. **Close codes
are D8's**: 1000 `done`/`lifetime`, 1001 `shutting down`/`closed by
operator`/`no pong`, the rule's own, 1009 from the library's read limit
(recognised by its error text inside `wsmock.CloseStatus`, the one place
the library's wording is depended on), the peer's mirrored. **Origins are
checked BEFORE the upgrade and before the cap** (`403 origin_refused`; a
missing `Origin` is always allowed; the library's own origin check is
disabled by name at the call site because this one is the owner), and a
writer that cannot be hijacked answers `501 streaming_unsupported` before
the library touches it — `wsmock.CanHijack` walks `Unwrap` exactly as the
library and `http.ResponseController` do, and every wrapper on the mock
path (`httpx.StatusRecorder`, the traffic tee, `headWriter`) implements it.
**The traffic row learns the inbound half**: `stream:ws`, `frames_in:M`,
`replies_dropped:K` (K > 0), `close:<code>` always, `first_in:binary|text`
when the first inbound frame was kept as nothing; under
`MOCKER_STREAM_TRAFFIC_FRAMES=first` the row's `reqBody` is the first
inbound TEXT frame that is a JSON object, run through
`traffic.RedactJSONBody` directly (there is no content type to dispatch on,
§30.13's first collision, answered by dispatching on the frame's opcode and
shape). **The CSRF predicate takes the request**: a `GET` with `Connection:
upgrade` and `Upgrade: websocket` is state-changing, so on the admin host
it meets the chain's first check and is refused `415` (a handshake carries
no JSON content type) — the door is locked before any admin route upgrades.
**The P6c surface works unchanged**: `kind` `ws` and `framesIn` on the row,
a push writes `data` as one text frame and refuses a non-empty `event` by
name, a close ends the connection with 1001. The smoke's WebSocket client is
`scripts/wsclient.py`, python3 standard library only, because curl's CLI can
only receive frames and the runtime image is distroless; "Requires:" at the
top of `scripts/smoke.sh` says so.

**`P6e` (2026-09-02) is the last streaming slice and the one whose whole
deliverable is a screen — DESIGN §30.14, built after the owner said
«сделай P6e» (a Russian string quoted as data), which is the lifting of
the A4 rule for exactly this slice and nothing else.** Three pieces, all
under `web/src/components/`. **Authoring** on the custom-endpoints screen:
a «Тип» selector (`http` \| `sse` \| `ws`) that pins the method to GET and
swaps the body editor for `StreamEditor.tsx` — the four behaviours phrased
as TASKS («Отправлять кадры по расписанию», «Генерировать кадр по
интервалу», «Отвечать на входящие сообщения», «Возвращать входящие
сообщения как есть»), because §14 forbids "recipe"/"JSON patch"/"matcher"
in the interface and §30.14 puts "timeline"/"reactive"/"tick" under the
same rule; a test asserts none of the six words renders. The reply rules
reuse `OperationEditor.tsx`'s `when[]` row and labels verbatim — one
condition language. `draftToDefinition` mirrors the server's refusals a
form can answer before a round trip (floor, ceilings, JSON, close codes)
and never clamps; `StreamCapsStrip` is the read-only strip — the caps as
constants (`STREAM_CAPS`, a copy of `internal/customep/stream.go`'s, the
one deliberate duplication) plus «Рассчитать кадры», which is
`POST .../endpoints/preview` and shows `maxBytesPerSec`, the amplifier
§30.12 wants seen before a loop is saved. A stream row edits through
`EditStreamForm` (PUT resends `kind` and `stream`, `activeStatus` 200).
**"Try it"** is `StreamTestClient.tsx`, a BROWSER-side client and never a
server-side probe (§30.14's own argument: `internal/probe` stays the tree's
only outgoing client, and the proxy §16 warns about sits between THIS
browser and the mock host): «Проверить» on a stream row opens
`EventSource` or `WebSocket` to `${workspace.url}${path}` (the ws URL is
the http one with the scheme swapped, never rebuilt), listens for the
named events the definition declares plus `message`, logs frames, sends a
text frame on ws, and reports what the browser will not say — an
EventSource `error` carries no status, so the wording says "the browser
does not report the reason" rather than guessing, and the source is
CLOSED on the first error (one attempt, one verdict) instead of left to
its silent reconnect loop. **The connections panel** is an eighth tab,
«Соединения» (`/workspaces/$id/connections`, `StreamConnectionsPage.tsx`),
polling `GET .../connections` every 2 s (the registry has no feed of its
own, §30.16), with «Закрыть» and an inline «Отправить кадр» form whose
error alert carries the server's own sentence — the 504 `push_timeout`
one says the frame stays queued. Two contract repairs rode along, both
drifts the screens exposed: `StreamPreviewRequest.kind` said `sse` alone
while the handler accepted `ws` since `P6d`; and `EndpointConflictDetails`
lacked `kind`/`stream`, so a stream row's 409 could not seed the editor
with the document the other writer saved — the ONE server change of the
slice (`endpointConflictDetails` in `internal/admin/endpoint_handlers.go`).
Contract stays 64 operations; `EXEMPT` 12 → 8 (preview and the three
connection operations are called by screens now); no tool, no variable,
no migration.

**`A14` (2026-09-02) is `MOCKER_STREAM_TRAFFIC_FRAMES=all` — §30.13's
"own retention budget", built as a budget per ROW and not as more rows:**
a connection is still ONE traffic row, so frames can never evict ordinary
rows, and `MOCKER_STREAM_TRAFFIC_MAX_FRAMES` (200) and
`MOCKER_STREAM_TRAFFIC_MAX_BYTES` (`64kb`), each way, bound what that row
holds. `internal/mockplane/traffic.go`'s `frameLog` is the one type
behind all three modes: nil under `off`; one frame CUT at
`MOCKER_TRAFFIC_MAX_BODY` under `first` (the pre-A14 bytes, and a later
frame is neither kept nor a truncation); whole frames only under `all`,
the first frame that does not fit — by count or by size — marking the log
truncated and closing it, because half a JSON object in an NDJSON body is
worse than a flag. SSE frames concatenate on the wire's own blank line;
WebSocket frames go one per line each way, the inbound side still only a
TEXT frame holding a JSON object, redacted per frame (`reqContentType`
`application/x-ndjson` under `all`). The row's `truncated` now also means
"a frame budget stopped the log", and the notes gain
`frames_recorded:N` (`frames_in_recorded:M` for WebSocket) under `all`
beside the unchanged `frames:N`/`frames_in:M` counts. `config.Limits`
carries both budgets (a schema change on `ServerConfigView.limits`, and
the enum gains `all`); the two P6b/P6d carve-outs that refused `all` are
closed. No route, no tool, no migration, no screen.


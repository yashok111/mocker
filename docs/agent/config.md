# Configuration: the MOCKER_* variables — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

**Configuration — through the environment only: 36 `MOCKER_*` variables, all with
defaults in `internal/config`, the full list with comments — `.env.example`.**
`MOCKER_MAX_ASSET` (`8mb`, one uploaded file; floor `1kb`; may not exceed
`MOCKER_MAX_BODY`, whose limit would otherwise refuse the upload first with
a 413 naming the wrong knob) and `MOCKER_MAX_ASSETS_TOTAL` (`64mb`, a
workspace's stored sum; may not be below the per-file cap) are `A6`'s two,
read by `assets.Repo` inside the upload's own transaction — `loadAssets`
in `internal/config`, the same sub-function shape `loadStreamWS` has.
`MOCKER_STREAM_MAX_CONNS` (200, per WORKSPACE, `0` refuses every stream
handshake — the one `MOCKER_STREAM_*` zero that means something),
`MOCKER_STREAM_MAX_LIFETIME` (900, ≥ 1; the mock plane's, the admin feed keeps
its constant) and `MOCKER_STREAM_TRAFFIC_FRAMES` (`off` \| `first` \| `all`
since `A14`) are `P6b`'s three, the mock plane's;
`MOCKER_STREAM_TRAFFIC_MAX_FRAMES` (200, ≥ 1) and
`MOCKER_STREAM_TRAFFIC_MAX_BYTES` (`64kb`, ≥ 1kb) are `A14`'s two, `all`'s
per-row budget each way, read nowhere but under `all`.
`MOCKER_STREAM_MAX_FRAME` (`64kb`, the INBOUND WebSocket frame cap, a
bigger frame closes with 1009), `MOCKER_STREAM_SEND_BUDGET` (`256kb`, the
per-connection byte bound of the reactive/echo reply queue — over it a
reply is dropped and counted, never blocks) and `MOCKER_STREAM_ORIGINS`
(empty = any; a list of `scheme://host[:port]`, parsed like
`MOCKER_URL_IMPORT_ALLOWLIST`, each element validated at startup; a request
with no `Origin` is always allowed) are `P6d`'s three; both sizes are
floored at `1kb`.
`MOCKER_STREAM_PING` (15), `MOCKER_STREAM_FRAME_TIMEOUT` (5) and
`MOCKER_STREAM_SESSION_RECHECK` (60) — `P6a`'s three, integer SECONDS read by
the same `count()` as `MOCKER_CHECKPOINT_DEBOUNCE` and converted to a
`time.Duration` in `cmd/mocker/main.go`, never inside `internal/stream`; unlike
every other `count()` value, **`0` fails startup for all three** — a zero ping
or recheck interval is a ticker that fires continuously and a zero frame
deadline expires every write before it is attempted, and none of them has a
"disabled" reading.
`MOCKER_MCP_KEY` — the bearer key of the MCP endpoint (see "Architecture" and `README.md`);
empty (the default) — `/mcp` is not mounted at all, a non-empty value shorter than 32
bytes fails startup, as with every other variable here.
`MOCKER_DEFAULT_SPEC` — **the numeric id of an already imported spec**, not a name (not
unique) and not a path (nothing at startup reads a document from disk or from the network).
Set — a user's first login with zero workspaces creates one for them from that
spec (§14 screen 2); pointing at nothing — **a crash at startup**, not silent
inaction.
Four decide routing: `MOCKER_ROUTING` (`host`/`path`),
`MOCKER_ADMIN_HOST`, `MOCKER_BASE_DOMAIN` and `MOCKER_RESERVED_PREFIX`.
The admin host is **forbidden** to sit under the base domain — the config crashes at startup
with `MOCKER_ADMIN_HOST (…) must not sit under MOCKER_BASE_DOMAIN (…)`, and that is exactly
why the browser cannot derive a workspace address from `window.location`.


# Assets (A6, A10) — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

**`A6` (2026-09-02) is assets: uploaded files a mock can serve — DESIGN
§32, the second version (v11) to add intent before code, inserted by the
agent at the owner's explicit instruction from four answered questions.**
`internal/assets` is a leaf repository over ONE new table (migration
`0006_assets.sql`, the first table since P0): one row per file, the bytes
as a BLOB, the natural key `(workspace_id, name)` — a `bodyRef` and an
`asset_url` recipe address an asset by NAME and never by id, because a
name survives the delete-and-reupload repair this product prescribes for a
wrong object everywhere else. `assets.ValidName` is the ONE owner of what
a name may be (`[A-Za-z0-9._-]{1,128}`, no dot-segment); `internal/recipes`
carries its own copy because it is a leaf that may not import
`internal/store`, and `internal/assets`' corpus test feeds both. Seven
things about it cannot be guessed from the code. **The upload is a raw-body
`PUT`** (`PUT /api/workspaces/{id}/assets/{name}`, `Content-Type` = the
file's type): §8's "multipart we do not touch" stands, and the CSRF chain's
content-type check (`enforceCSRF`) is swapped for "parseable and not
browser-executable" on exactly ONE predicate, `rawBodyRoute` — the origin
check and `X-CSRF-Token` run unchanged, the header keeps the request
non-simple and preflighted, and the handler repeats the media-type refusal
because the MCP loopback bypasses the chain by construction. The read is
`http.MaxBytesReader` at `MOCKER_MAX_ASSET + 1`, never a bare `io.ReadAll`
under the dispatcher's 10 MB `MaxBody`. **The `revision` bump is the
package's own `bumpRevisionTx`**, HARD RULE 5's sixth copy —
`workspaces.Repo.Update` inside a `db.Write` callback deadlocks the single
writer connection, a hang and not a red test, which the first draft had
backwards until a reader caught it. **The mock route is dispatch step 4**
(`serveReserved`, beside `/health` and `/state`): CORS already set at step
2, preflight answered at step 3, the session layer (step 5) never reached —
no forced status, delay or pause applies to a picture — and not recorded in
traffic; `Meta` first and the BLOB only on a miss, so `If-None-Match`
answers 304 without moving bytes through the reader pool; a strong `ETag`
(the sha256), `nosniff`, and NO `Cache-Control` (§32.3: nothing beyond the
tag — with no `Last-Modified` either a browser has no heuristic freshness to
apply). **The executable-type refusal runs TWICE** — at the upload and
again on the stored type at serve, on the route and inside the `bodyRef`
lookup — because a row written by an older build or a hand-run `UPDATE`
must not serve script under the admin origin in path mode (§32.6).
**`bodyRef` on a pinned variant is exclusive with `body`, `bodyEncoding` AND
`mediaType`** — refused, where §32.3 said "must agree", a declared narrowing
(`CARVE-OUTS.md`): agreement is unknowable at write time, so the asset's
stored type is the only type such a variant has, and it reaches the wire
through ONE place — a local `assetType` in `assembleResponse` that
overrides `resp.MediaType` after the switch and skips the envelope wrap,
because the tail sets the type from `rv.MediaType` unconditionally and a
switch arm alone could not. The arm sits AFTER the executable-type refusal
and AFTER `PreBuilt` (a pinned variant already exits resource takeover, so
the two never coexist; the order is stated so a later caller cannot bypass
either) and BEFORE `pinned`; the lookup it resolves through is the `ref`
shape twice over — `assetLookup`, CONSTRUCTED by `serveGenerated` from the
real request (it is what MARKS `asset_missing` in the traffic, because
`assembleResponse` holds no `*http.Request`), RECEIVED by
`assembleResponse`, `nil` from Preview and handled like a nil `Ref`. A
missing asset answers the variant's status with an EMPTY body and the note;
Preview answers `noBody` with `PreviewResult.Notes`. **The `asset_url`
recipe writes `Env.AssetBase + PathEscape(name)`** and DECLINES on an empty
base (population, a tick frame, a `Request` built by hand) rather than emit
a relative URL; the base is per REQUEST — `gen.Request.AssetBase`, copied
into `recipes.Env` from `w.req`, never `Options`, the precedent being `Ref`
and the reason being the runtime cache under `(workspace_id, revision)` —
and it is built by `httpx.WorkspaceURL(r, cfg, slug) + ReservedPrefix +
"/assets/"` at both call sites (the mock request in `serveGenerated`, the
admin request in the preview handler, carried in on
`domain.PreviewRequest.AssetBase`). **`httpx.WorkspaceURL` is the ONE
construction of a workspace's public URL**: `admin.workspaceURL` delegates
to it, `httpx.WorkspacePathPrefix` replaced the two identical unexported
consts in `internal/server` and `internal/admin`, and the two guarded reads
(scheme through `ForwardedProto`, port through `RequestPort` — the SSRF
pivot `TestP1c2WorkspaceView_urlRefusesAnInjectedPort` pins) moved with it.
**`upload_asset` reaches the admin plane through `CallAsMCPRaw`**, a second
method on `admin.Server` with a content type, asserted by `internal/mcp`
through its own one-method `rawCaller` interface only where it is needed —
`mcp.Caller` is the seam all fifty-one earlier tools and every test double
dispatch through and is not widened; the tool's description names the REAL
ceiling (about 7 MB at the default `MOCKER_MAX_BODY`, the base64 travels
under it), and `list_assets`/`delete_asset` (`confirmSlug`, checked inside
the delete's transaction) complete the three. No checkpoint, scenario or
bundle carries asset bytes (bundle stays v4: `bundle.Decode` is a plain
`jsonx.Unmarshal` and the deep gate re-enters `ValidateVariant`, so a
`bodyRef` round-trips and a v4 document without one decodes byte for
byte); a rollback restores a `bodyRef` whose asset may be gone, and
`asset_missing` is the whole of the answer. `PUT`/`DELETE` bump `revision`
for §32.5's reason (`{prefix}/health`'s `revision` is the one signal an
external test has that something changed) at a named cost — `routeCache`
is keyed by it, so every upload discards the compiled runtime — and both
joined `autoCheckpointExcludedNeverTouchesLayer` (14 → 16), because bytes
are not configuration.

**`A10` (2026-09-02) is the assets screen: the «Файлы» tab
(`AssetsPage.tsx`, the ninth) over `A6`'s three routes, on the owner's
word like `P6e`.** List with the workspace's usage against its two caps, a
dropzone upload whose name is pre-repaired to `assets.ValidName`'s
alphabet and editable, and a delete behind the typed workspace slug. One
thing in it is not a screen: `api/client.ts`'s `customFetch` used to set
`Content-Type: application/json` on EVERY non-GET, which on the raw-body
`PUT .../assets/{name}` would have stored a JPEG under the JSON type (the
server takes the asset's type from that header); a `Blob` body now keeps
its own type, orval's `*/*` placeholder overridden the same way, and the
CSRF chain's `rawBodyRoute` predicate admits it. `EXEMPT` 8 → 5 (the
three asset operations withdrawn); no route, no tool, no migration, no
variable.


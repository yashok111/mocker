# The test plugin @yashok111/mocker-test and the per-target clear (A12, A13) — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

**`A12` (2026-09-02) is `@yashok111/mocker-test` — `packages/mocker-test/`, the
mock as a fixture a test suite owns: a zero-dependency npm package over
`{prefix}/health` and `{prefix}/state`, with a Playwright fixture factory
and a Cypress command registration that import neither framework.** Not a
Go change: no route, no tool, no migration, no variable. Its own yarn 4
install (same TypeScript/vitest/oxlint/oxfmt versions as `web/`, its own
`.oxlintrc.json` without the react plugin — that plugin reads Playwright's
`use` as a hook), its suite spawns `./bin/mocker` in PATH routing on a
loopback port (`MOCKER_ADMIN_HOST=localhost`; Node's `fetch` cannot set
`Host`), and `make plugin-test` depends on `build`. Since 2026-09-03 it
is built by **tsdown** (`tsdown.config.ts`: three entries, ESM only with
declarations, target node24, `engines.node >= 24` — the owner's call,
Node 24 `require()`s ESM natively so a CJS twin would only double the
files) and published on npm as `@yashok111/mocker-test` (`A17`, below;
`make plugin-pack` still cuts a tarball for a registry-less contour —
`npm pack`'s `prepack` script IS the build, so neither the tarball nor the
published tarball ever ships a stale `dist/`); `package.json`'s `exports`
name the tsdown file names, not tsc's. `reset()` is the only
clear and clears everything; `fail` collapses the server's `once`/`n` to
one `times`.

**`A13` (2026-09-02) is the per-target clear the plugin exposed the lack
of**: `livestate.Store.Delete(workspaceID, target, action)` — `Clear`
narrowed by key (every action on the target, or one), the same broadcast,
so a request parked on the deleted pause is released and one parked on
another target stays — reached by an OPTIONAL body on BOTH planes'
existing DELETE (`DELETE {prefix}/state` and `DELETE /api/workspaces/{id}/session`,
`{target, action?}`; no body is the pre-A13 clear-all; a body naming no
target is refused, never read as "everything"), by `clear: true` on
`set_session_directive` (which DELETEs then GETs, so `directives[]` is
what remains — the tool's `toolRoutes` row gained the GET), and by
`mock.clear(target, action?)` in `@yashok111/mocker-test`. Contract: a schema change
on an existing route (an optional `SessionClearRequest`), no count
change; no migration, no variable, no screen.


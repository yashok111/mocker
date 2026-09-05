# The A15 audit: invariants fixed with a test each — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

**`A15` (2026-09-03) is the audit: five monolith files split by pure
text moves, then a five-reviewer pass (hot path, concurrency, SQL,
admin/MCP/config, generator) whose findings were fixed with a test each.**
The splits changed no text: `internal/resources/repo.go` →
`scope.go`/`confirm.go`/`decline.go`/`entity_read.go`/`entity_write.go`/
`workspace_tx.go`, `internal/checkpoints/repo.go` →
`capture.go`/`apply.go`/`write_tx.go`/`rollback.go`/`auto.go`,
`internal/specs/repo.go` → `suggestions.go`/`read.go`/`write.go`,
`internal/mockplane/respond.go` → `negotiate.go`/`delay.go`/`variant.go`,
`internal/admin/server.go` → `route_table.go`. Eight things the fixes
established that cannot be guessed from the code. **A document value
leaves the spec through ONE door, `gen.cloneDocValue`**: `leafValue`,
`depthLimitValue`, `hardCeilingValue` and `minimalScalarValue` used to
return a schema-level `example`/`const`/`default`/enum member BY
REFERENCE, and the list walker wrote the row's id INTO it — every row of
a page was the same document map, and two concurrent anonymous requests
wrote it together, which is a fatal `concurrent map writes` that kills
the process, not a panic the middleware recovers. **`gen.jsonSize` is the
budget's sizer and must stay byte-exact**: `estimateJSONSize` was a
reflective `Marshal` per scalar, per item and per required floor, the
single largest cost of a generated body; the sizer reproduces
`encoding/json`'s escaping and float rules and `jsonsize_test.go` holds
it to the byte, because the byte-budget tests and the 419-body golden
both move on a one-byte disagreement (measured, `BenchmarkBody_fullCorpus`:
1.34 s → 0.85 s, 3.9 M → 1.6 M allocations, with the floor pass reused
in `walkObject`, the resolver memo on a `sync.Map` and `valuesEqual`'s
scalar fast path). **The generator's cost model now counts what it
prices**: `hardCeilingValue`'s required-subtree recursion charges
`visitNode`, a oneOf/anyOf branch hop is bounded by `maxCompositionHops`
(a self-referencing `oneOf` recursed ~10^5 frames at the SAME depth
before `maxWalkNodes` tripped), and `toFloat64` clamps a schema number to
±2^53 (a `maximum: 1e308` gave a span of `+Inf`, a modulo by zero on
arm64). **The login limiter has two buckets**: the `(address, name)` one
that keeps a flood under one name from locking a colleague out, and a
per-address one at `addrLimitMultiplier` × the limit — without it a fresh
name per attempt was a fresh 10/minute budget against the ONE shared
password; the map is capped at `maxLoginBuckets` and the name is keyed at
64 runes. **A stored status is 100..599** (`overrides.ValidHTTPStatus`,
shared by `internal/customep`): `WriteHeader` panics outside it, and a
pinned `activeStatus: 999` was a 500 with a stack trace on every request
to that operation. **`entities`' UNIQUE does not span `base_scope_key`**
(0003's premise, broken by A11's operator-chosen key): `resources.Repo.Set`
answers `ErrEntityKeyConflict` (409) for the same key under another base
value, and `ErrEntityKeyNotCanonical` (400) for a key that does not
round-trip through the family's id type — `gen.CoerceIDValue` HASHES an
unparsable key rather than failing, so `PUT .../entities/abc` on an
integer family stored a row whose key and id disagreed. **`forked_from`
is `NO ACTION`, not dangling**: `workspaces.Repo.Delete` detaches every
copy before the delete (`CARVE-OUTS.md`'s P4b entry is corrected in
place). **The admin feed's lifetime is a one-shot timer**: `drain`'s
non-blocking select used to consume it and return nil, so under exactly
the steady traffic that drains, D10's 900 s was lost; `errLifetimeExpired`
carries the fact out. Beside those: the import (`POST
/api/workspaces/import`) refuses a browser-executable media type and
runs the bound-spec base-path check like `PATCH` does (which now runs it
on a `specId`-only body too); `bundle.Validate` checks
`resources[]`/`decisions[]` and `ValidateData` requires an object body;
`config.Load` refuses a reserved prefix that trims to empty, a host with
a port, a body size under 1kb and a size that overflows, and `main`
refuses an argon2 hash the library would panic on; `internal/yamlx`
walks `yaml.Node` so `1.0` stays `1.0` and an unquoted date stays a
string; the recorder caps stored headers at the body cap
(`truncated:headers`), copies a cut body, counts a failed batch as
dropped, writes under a detached context and keeps writing while a full
batch waits (one batch per tick capped it at 128 rows/s); `http.Server`
gets `MaxHeaderBytes` 64 KiB; the runtime build under singleflight runs
under `context.WithoutCancel` (one client aborting failed every joiner
with 500); `routeCache.get` takes a read lock on the hot path; the
envelope and a collection body are built with `jsonx.Compact`, never a
reflective marshal; a rollback decodes `data_snap` before the write
transaction; `CAST(entity_key AS INTEGER)` is guarded against a
19-digit key saturating `seq`; migration `0007_fk_indexes.sql` indexes the
seven foreign-key columns that had none (a `DELETE FROM resources`
scanned three tables whole). Refused on policy and left for the owner:
treating `+xml` media types as browser-executable (it would break XML
mocking) and the probe route's residual port choice.


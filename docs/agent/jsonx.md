# JSON through internal/jsonx: the sonic measurement — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

## JSON — only through `internal/jsonx`

The backend is **`encoding/json`**, but **not a single production file imports it
directly** (`internal/jsonx/boundary_test.go` checks by walking the AST and fails the
build on a violation). The wrapper is a seam that makes the backend choice a decision
rather than a rewrite: one `var api` line changes, and the tests next to it are what
makes such a swap safe.

**bytedance/sonic was integrated in full and rejected by measurement, not by taste:**

```
marshal, microbenchmark     4771 → 2692 ns/op (29 → 8 allocations)
unmarshal, microbenchmark  10261 → 4320 ns/op
generating all 419 bodies   0.524 → 0.452 s   (6 runs each, ~14%)
```

The one-and-a-half-fold microbenchmark win collapses to 14% through the real
path: generation is dominated by schema traversal, the PRNG and validation, not marshaling.
Against it — five extra modules, including a JIT that writes executable memory at
runtime (a strange addition to a product whose entire delivery story is one
static CGO-free binary into a closed network), `-race` on `internal/gen`
28 s → 86 s, and a hard dependency on whether sonic keeps up with Go releases.

**Two traps that attempt exposed** — because of them the wrapper is worth its own
file even today:

1. **Output stability.** Default sonic does not sort map keys and does not
   escape HTML (only `sonic.ConfigStd` matches stdlib). Go
   randomizes map traversal on every run, and the whole point of the project is that one
   seed and one spec give byte-identical bodies. A backend taken for speed
   without checking that property would break the golden **every other time** — the worst
   possible form of failure. `TestMarshal_isStableAcrossRuns` holds the line.
2. **Error taxonomy.** sonic returns `*decoder.MismatchTypeError` where
   stdlib returns `*json.UnmarshalTypeError`: an `errors.As` on the standard type
   keeps **compiling** and silently stops matching — a malformed login
   body starts getting 500 instead of 400. Therefore one must ask
   `jsonx.Malformed(err)`, and a new backend is obliged to extend it. The requirement is
   held not by a comment but by a test: it decodes through `jsonx.NewDecoder` and
   checks through `jsonx.Malformed`, i.e. it goes red on any backend.

The types `RawMessage`, `Number`, `Marshaler`, `Unmarshaler` are aliases to stdlib.
**The tests are deliberately left on `encoding/json` directly**: a test that builds
the expectation with the standard library and compares it with `jsonx`'s output is a continuous
cross-check of whichever backend is configured right now.


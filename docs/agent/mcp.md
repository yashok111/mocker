# MCP adapter, embedded guide, spec import, limits — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

`internal/mcp` — a Model Context Protocol server, `POST /mcp` on
`MOCKER_ADMIN_HOST`, mounted only when `MOCKER_MCP_KEY` is set. Not a
third plane, but a thin adapter over the admin plane. **Since v4 it is described in
DESIGN.md — §14.2, and the threat model around it in §15**; before that it was not
there at all. Three things about it cannot be
guessed from the code of the two planes above: it is mounted **outside the
CSRF/session chain** of the admin plane (`Server.Handler()` gives it a
separate branch before `webui.Handler`; authorization is a bearer key by constant-time
comparison, `/mcp` does not read cookies at all); it is **deliberately not part** of
`api/openapi.json` (that stays at 61 routes — there is nothing to type in JSON-RPC,
and `coverage.test.ts` and the CSRF contract test both exist precisely
for the routes FROM that contract, `/mcp` is not in their perimeter and must
not be); and fifty-four of its fifty-five tools reach the domain only through
`admin.Server.CallAsMCP` — an in-process call into the very same admin route table
under an MCP identity, never straight into `overrides.Repo`/`workspaces.Repo`
and so on, so as not to create a second validation path bypassing the handlers.
The fifty-fifth, `get_guide` (`A7`), reaches nothing: it returns the embedded
usage guide (`internal/guide`, below), and its `toolRoutes` row is empty.
The fifty-seventh, `get_server_config` (`A9`), reads `*config.Config`
directly — `config.Limits`, the same projection `ServerConfigView.limits`
carries to the panel through login and `GET /api/me`, so the two readers
cannot disagree — and its `toolRoutes` row is empty like `get_guide`'s.
The fifty-sixth, `import_spec` (`A8`), is the reversal of the one exclusion
`mcpAllowedRoutes` kept on a policy rather than a hazard — `POST /api/specs`,
which mocker-a4-mcp-reach D3 left to the `/specs` screen; the owner let it
in so an agent holding the spec file in its own repository needs no human
to paste it. `DELETE /api/specs/{id}` stays out: it cascades across every
bound workspace.
Tool details — `internal/mcp`, client config — `README.md`
("MCP").

**`A7` (2026-09-02) is the documentation slice: how to use the product,
for a human and for an agent, with the agent's copy served by the server
itself.** Three artefacts and one owner each. `docs/USER-GUIDE.md` is the
operator's manual in Russian (the product's language, like every other
operator-facing string), rendered inside the panel at `/guide` by
`GuidePage.tsx` through `marked` (the SPA's first markdown renderer and
its one new dependency, zero transitive) from a `?raw` import — a screen
added AFTER the A4 "no new screens" rule, on the owner's explicit request
(«для людей прям отдельную страницу можно заверстать в ui», a Russian
string quoted as data), and admissible under it because the screen calls
NO admin route: the contract stays at 64, `coverage.test.ts` learns
nothing, and `routes.test.tsx` asserts the mount makes no call past the
guard's own `/api/me`. `skills/mocker/` is the agent's guide as an
installable skill — `SKILL.md` (the mental model, the order of calls, the
rules that bite) plus four references (`tools.md`, every tool with inputs,
outputs and gotchas; `shapes.md`, every document an agent writes;
`cookbook.md`, twelve ordered recipes; `http.md`, the same over curl and
the MCP client config) — and it is the ONE OWNER of that text.
`internal/guide` embeds byte copies of those five files (go:embed cannot
reach above its package, and a skill is discovered by the path
`skills/<name>/SKILL.md`, so neither can move), `make guide-sync`
refreshes them, and `internal/guide`'s own test fails on any drift. Two
things reach an agent that has no repository: `initialize` now returns
`instructions` (`internal/guide/instructions.md`, a few paragraphs, the
one file with no skill counterpart, passed as `sdk.ServerOptions` — a
FIELD of the initialize result, not a capability, so DESIGN §14.2's "only
`tools`, without `resources` and `prompts`" still holds and no divergence
is recorded), and `get_guide {topic}` (tool 55, `internal/mcp/tools_guide.go`)
returns one of the five files, `overview` with SKILL.md's frontmatter
stripped. It is the first tool whose `toolRoutes` row is EMPTY — it calls
no handler — which `TestToolRoutesPopulation` counts and
`TestToolRoutesAgreeWithAdminAllowlist` has nothing to check for. No
migration, no variable, no contract change.

**`A8` (2026-09-02) is spec import for the agent, and YAML.** Two things.
`import_spec` (tool 56, `internal/mcp/tools_specs.go`) wraps `POST
/api/specs` — the document as one string inside the arguments, exactly the
shape the screen sends, deduplicated by byte hash so a retry answers
`duplicate: true` — and `POST /api/specs` joins `mcpAllowedRoutes`, the
reversal described under `/mcp` above. And `internal/yamlx` (`ToJSON`) is
the tree's second isolated library: `internal/openapi`'s `decodeDocument`
tries JSON first (JSON is valid YAML, so the other order would route every
document through the converter), and only an input whose first CONTENT
line — after blank lines, `#` comments and a `---` marker — names
`openapi:`/`swagger:` goes through the converter and back into the SAME
`decodeJSON`; so the pipeline keeps one root type, one number handling
(`json.Number`) and one error set, and a YAML parse failure is
`ErrNotADocument` with the decoder's words beside it, never
`ErrUnsupportedFormat` (that is for a document we recognise and decline —
Swagger 2.0 still is). Integer mapping keys (`200:` under `responses:`)
become the string keys JSON needs; a sequence key or a multi-document
stream is refused by name. The `/specs` screen dropped its client-side
`.yaml` refusal. No route, no migration, no variable; the stored hash is
over the bytes as uploaded, so a YAML and a JSON rendering of the same spec
are two specs, as two JSON serialisations already were.

**`A9` (2026-09-02) is the limits, readable.** `config.Limits`
(`internal/config`) is the one projection of every ceiling a caller can
hit — bytes as bytes, seconds as seconds — and two readers share it:
`ServerConfigView.limits` (login and `GET /api/me`, a schema change on an
existing route, so no contract count, no `EXEMPT` entry) feeds the stream
caps strip through the endpoints route's session context, replacing the
`MOCKER_*` variable NAMES it showed under `P6e`; and `get_server_config`
(tool 57) hands the same struct to an agent from `mcp.New`'s own `cfg`,
calling no route. The validator's constants (`STREAM_CAPS`: frames,
delays, rules) stay constants on the strip because they are not
configuration. No route, no migration, no variable.


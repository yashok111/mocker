# Documentation index

| document | audience | language |
|---|---|---|
| [`USER-GUIDE.md`](USER-GUIDE.md) | the operator at the admin panel: concepts, first steps, every screen, controlling the mock from tests, recipes, troubleshooting. Rendered inside the panel at `/guide`. | Russian — the product's own language |
| [`../skills/mocker/SKILL.md`](../skills/mocker/SKILL.md) | an agent (Claude Code, Cursor, any MCP host) driving mocker: the mental model, the order of calls, the rules that bite. Served by the running server as `get_guide {topic: "overview"}` and, in short, in `initialize`'s `instructions`. | English |
| [`../skills/mocker/references/tools.md`](../skills/mocker/references/tools.md) | every MCP tool: inputs, outputs, gotchas, `editVersion` and `confirmSlug`. `get_guide {topic: "tools"}` | English |
| [`../skills/mocker/references/shapes.md`](../skills/mocker/references/shapes.md) | every document an agent writes: override, `when[]`, recipes, custom endpoint, stream, session directive, settings, resources, assets, errors. `get_guide {topic: "shapes"}` | English |
| [`../skills/mocker/references/cookbook.md`](../skills/mocker/references/cookbook.md) | twelve ordered recipes, from "stand up a workspace" to "debug why the mock answered that". `get_guide {topic: "cookbook"}` | English |
| [`../skills/mocker/references/http.md`](../skills/mocker/references/http.md) | the same over curl: login and CSRF, spec import, raw asset upload, the `/__mocker/state` calls a test suite makes, MCP client config. `get_guide {topic: "http"}` | English |
| [`../README.md`](../README.md) | running it: docker, HTTPS, environment variables, tests | English |
| [`../DESIGN.md`](../DESIGN.md), [`../CLAUDE.md`](../CLAUDE.md), [`../HISTORY.md`](../HISTORY.md), [`../CARVE-OUTS.md`](../CARVE-OUTS.md) | changing mocker itself: the intent, the state as built, how each slice arrived, what is deliberately absent | English |

## Installing the skill into another project

The skill is what makes an agent in a FRONTEND repository know mocker before
its first MCP call. From that repository:

```bash
npx -y -p skills skills add https://github.com/yashok111/mocker --skill mocker -a claude-code
# or, from a local checkout:
npx -y -p skills skills add /path/to/mocker --skill mocker -a claude-code
# or by hand:
cp -r /path/to/mocker/skills/mocker .claude/skills/mocker
```

Then add the MCP server (`http.md`, last section). An agent WITHOUT the skill
still finds its way: the server's `initialize` answer names `get_guide`.

## Keeping the copies equal

`skills/mocker/` is the one owner of the agent guide. `internal/guide/` holds
byte copies for `go:embed`; `make guide-sync` refreshes them and
`internal/guide`'s test fails the build when they drift. `docs/USER-GUIDE.md`
has no copy: the SPA imports the file itself.

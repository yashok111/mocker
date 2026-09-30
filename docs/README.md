# Documentation index

| document | audience | language |
|---|---|---|
| [`USER-GUIDE.md`](USER-GUIDE.md) | the operator at the admin panel: concepts, first steps, every screen, controlling the mock from tests, recipes, troubleshooting. Rendered inside the panel at `/guide`. | Russian — the product's own language |
| [`../skills/mocker/SKILL.md`](../skills/mocker/SKILL.md) | an agent (Claude Code, Cursor, any MCP host) driving mocker: the mental model, the order of calls, the rules that bite. Served by the running server as `get_guide {topic: "overview"}` and, in short, in `initialize`'s `instructions`. | English |
| [`../skills/mocker-backend-import/SKILL.md`](../skills/mocker-backend-import/SKILL.md) | independently installable foundation repository import, safe repeat import and pinned comparison. Served as `backend-import`; shared details are `backend-model`, `backend-import-protocol`, `backend-recovery`, `backend-examples` in the same selected guide set. | English |
| [`../skills/mocker/references/tools.md`](../skills/mocker/references/tools.md) | every MCP tool: inputs, outputs, gotchas, `editVersion` and `confirmSlug`. `get_guide {topic: "tools"}` | English |
| [`../skills/mocker/references/shapes.md`](../skills/mocker/references/shapes.md) | every document an agent writes: override, `when[]`, recipes, custom endpoint, stream, session directive, settings, resources, assets, errors. `get_guide {topic: "shapes"}` | English |
| [`../skills/mocker/references/cookbook.md`](../skills/mocker/references/cookbook.md) | twelve ordered recipes, from "stand up a workspace" to "debug why the mock answered that". `get_guide {topic: "cookbook"}` | English |
| [`../skills/mocker/references/http.md`](../skills/mocker/references/http.md) | the same over curl: login and CSRF, spec import, raw asset upload, the `/__mocker/state` calls a test suite makes, MCP client config. `get_guide {topic: "http"}` | English |
| [`../README.md`](../README.md) | running it: docker, HTTPS, environment variables, tests | English |
| [`../DESIGN.md`](../DESIGN.md), [`../CLAUDE.md`](../CLAUDE.md), [`../HISTORY.md`](../HISTORY.md), [`../CARVE-OUTS.md`](../CARVE-OUTS.md) | changing mocker itself: the intent, the state as built, how each slice arrived, what is deliberately absent | English |
| [`agent/`](agent/) | the per-subsystem context an agent reads on demand — the paragraphs cut out of `CLAUDE.md` on 2026-09-05 (resources, streaming, checkpoints, MCP, ops, …); `CLAUDE.md` holds the index saying which file to open for which task | English |

## Installing skills into another project

Choose root-only for mock configuration/API design and backend task routing,
import-only for repository reconstruction/reconciliation, or both. From the
repository where the agent works:

```bash
npx -y -p skills skills add https://github.com/yashok111/mocker --skill mocker -a claude-code
# or, from a local checkout:
npx -y -p skills skills add /path/to/mocker --skill mocker -a claude-code
# Import-only from the same checkout:
npx -y -p skills skills add /path/to/mocker --skill mocker-backend-import -a claude-code
# For both packages, run both local-checkout commands above.
# Or copy the selected folders into a recognized skill directory:
mkdir -p .claude/skills
cp -r /path/to/mocker/skills/mocker .claude/skills/mocker
cp -r /path/to/mocker/skills/mocker-backend-import .claude/skills/mocker-backend-import
```

The copy fallback works with any host's recognized skill directory; `.claude/skills`
is one example. Import-only requires only its own SKILL.md, without a neighboring
root package. It reads model/protocol/recovery/examples from pinned server topics.
Root-only keeps the generated compatible import procedure. Both packages select
the same server workflow.

Then add the MCP server ([HTTP onboarding](../skills/mocker/references/http.md)).
Skill installation does not install server capabilities. An agent without either
skill still discovers procedures from `initialize` and `get_guide`.

## Keeping the copies equal

`skills/mocker/guide-sources.json` declares canonical owners. The import entrypoint
is owned by `skills/mocker-backend-import/SKILL.md`; its root-package
`references/backend/import.md` is a generated compatibility copy. Root text and
shared references are owned by their declared files under `skills/mocker/`.
Edit canonical files, then run `make guide-sync` to refresh embedded topics,
compatibility copies, metadata and immutable manifests. `python3 scripts/guide-sync.py --check`
and guide tests reject drift. `docs/USER-GUIDE.md` has no copy: the SPA
imports the file itself.

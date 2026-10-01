# Documentation index

| document | audience | language |
|---|---|---|
| [`USER-GUIDE.md`](USER-GUIDE.md) | the operator at the admin panel: concepts, first steps, every screen, controlling the mock from tests, recipes, troubleshooting. Rendered inside the panel at `/guide`. | Russian — the product's own language |
| [`../skills/mocker/SKILL.md`](../skills/mocker/SKILL.md) | an agent (Claude Code, Cursor, any MCP host) driving mocker: the mental model, the order of calls, the rules that bite. Served by the running server as `get_guide {topic: "overview"}` and, in short, in `initialize`'s `instructions`. | English |
| [`../skills/mocker-backend-import/SKILL.md`](../skills/mocker-backend-import/SKILL.md) | independently installable foundation or PostgreSQL/SQLite relational source import, safe repeat import and pinned comparison. Served as `backend-import`; shared details are `backend-model`, `backend-import-protocol`, `backend-recovery`, `backend-examples` in the same selected guide set. | English |
| [`../skills/mocker-backend-database/SKILL.md`](../skills/mocker-backend-database/SKILL.md) | independently installable pinned database/ER, facet drift, coverage and evidence inspection. Served as `backend-database`; `backend-database-reference` keeps database ownership, while model/recovery topics keep import ownership in the same global set. | English |
| [`../skills/mocker/references/tools.md`](../skills/mocker/references/tools.md) | every MCP tool: inputs, outputs, gotchas, `editVersion` and `confirmSlug`. `get_guide {topic: "tools"}` | English |
| [`../skills/mocker/references/shapes.md`](../skills/mocker/references/shapes.md) | every document an agent writes: override, `when[]`, recipes, custom endpoint, stream, session directive, settings, resources, assets, errors. `get_guide {topic: "shapes"}` | English |
| [`../skills/mocker/references/cookbook.md`](../skills/mocker/references/cookbook.md) | twelve ordered recipes, from "stand up a workspace" to "debug why the mock answered that". `get_guide {topic: "cookbook"}` | English |
| [`../skills/mocker/references/http.md`](../skills/mocker/references/http.md) | the same over curl: login and CSRF, spec import, raw asset upload, the `/__mocker/state` calls a test suite makes, MCP client config. `get_guide {topic: "http"}` | English |
| [`../README.md`](../README.md) | running it: docker, HTTPS, environment variables, tests | English |
| [`../DESIGN.md`](../DESIGN.md), [`../CLAUDE.md`](../CLAUDE.md), [`../HISTORY.md`](../HISTORY.md), [`../CARVE-OUTS.md`](../CARVE-OUTS.md) | changing mocker itself: the intent, the state as built, how each slice arrived, what is deliberately absent | English |
| [`agent/`](agent/) | the per-subsystem context an agent reads on demand — the paragraphs cut out of `CLAUDE.md` on 2026-09-05 (resources, streaming, checkpoints, MCP, ops, …); `CLAUDE.md` holds the index saying which file to open for which task | English |

## Installing skills into another project

Choose root-only for mock configuration/API design and backend task routing,
import-only for repository reconstruction/reconciliation, database-only for
pinned inspection, or all three as a bundle. From the
repository where the agent works:

```bash
npx -y -p skills skills add https://github.com/yashok111/mocker --skill mocker -a claude-code
# or, from a local checkout:
npx -y -p skills skills add /path/to/mocker --skill mocker -a claude-code
# Import-only from the same checkout:
npx -y -p skills skills add /path/to/mocker --skill mocker-backend-import -a claude-code
# Database-only from the same checkout:
npx -y -p skills skills add /path/to/mocker --skill mocker-backend-database -a claude-code
# For the bundle, run all three local-checkout commands above.
# Or copy the selected folders into a recognized skill directory:
mkdir -p .claude/skills
cp -r /path/to/mocker/skills/mocker .claude/skills/mocker
cp -r /path/to/mocker/skills/mocker-backend-import .claude/skills/mocker-backend-import
cp -r /path/to/mocker/skills/mocker-backend-database .claude/skills/mocker-backend-database
```

The copy fallback works with any host's recognized skill directory; `.claude/skills`
is one example. Either leaf requires only its own SKILL.md, without a neighboring
root package. Import reads focused model/protocol/recovery/database/profile/examples
topics as needed; inspection reads database/model/recovery as needed. All reads
use one selected immutable global guide set, with each actual canonical owner
identity/contentHash verified. Root-only keeps generated import and database
workflow aliases. All install modes select the same compatible server procedures.
A relational task cannot fall back to a schema1-only foundation workflow.

Then add the MCP server ([HTTP onboarding](../skills/mocker/references/http.md)).
Skill installation does not install server capabilities. An agent without any local
skill still discovers procedures from `initialize` and `get_guide`. Source SQL/ORM/
migration facets provide declarations and evidence, not live runtime observation;
no modeled code or DDL executes. Database typed proposals/edits remain B1.2.

## Keeping the copies equal

`skills/mocker/guide-sources.json` declares canonical owners. The import entrypoint
is owned by `skills/mocker-backend-import/SKILL.md`; its root-package
`references/backend/import.md` is a generated compatibility copy. The database
entrypoint is owned by `skills/mocker-backend-database/SKILL.md`; its generated
root alias is `references/backend/database-workflow.md`. The separate canonical
`references/backend/database.md` owns only `backend-database-reference`. No topic
has two owners. Root text and
shared references are owned by their declared files under `skills/mocker/`.
Edit canonical files, then run `make guide-sync` to refresh embedded topics,
compatibility copies, metadata and immutable manifests. `python3 scripts/guide-sync.py --check`
and guide tests reject drift. `docs/USER-GUIDE.md` has no copy: the SPA
imports the file itself.

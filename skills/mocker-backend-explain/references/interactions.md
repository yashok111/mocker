# Static dynamic interactions

Require `backend-interactions`, admitted `interactions`, `backend-diagram-v1`
and `diagram-view-v1` from the selected immutable guide set. Source schemas1–6,
legacy Flow/Database saved views and existing Sequence artifacts are unchanged.
B5 export/check/replay and B6 observed-run acceptance remain
open. This view never applies or runs a scenario.

## Read, then explicitly accept

1. Read an exact runtime/event entrypoint on `{revisionId}` or
   `{changeProposal:{proposalId,proposalRevisionId}}`. Legacy DB proposals and
   import candidates are unsupported. Never substitute a current head.
2. `build_backend_interactions {projectId,target,entrypointId,maxSteps:200}`
   (REST POST `/api/backend-projects/{id}/diagrams/interactions/build`) returns
   `{document,targetHash,gaps,visitedObjects,truncated}` without writing a project,
   diagram or analysis job. Optional `architecture:{id,version,contentHash}` must
   share the exact target. Bounds are maxSteps1–1000 and250000 visited objects;
   document limits include participants/steps/branches together. Inspect frontier
   and gaps before accepting. A source assertion is a static claim, not execution.
3. Supply authored declarations when business meaning cannot be established from
   source. A participant has id/label/origin/refs and optional architectureElementId.
   A step has id/label/origin/refs/from, optional to/replyTo, kind
   request/response/send/receive/error/boundary and required branchPath.
   Branches have id, optional parentId, groupId, label, kind
   alternative/parallel/loop, guardText and origin. Order links have stable id,
   from/to step IDs and origin. Payload arrays scopeRefs/participants/steps/order/
   branches are required and nonnull. Missing to is only legal for send/boundary.
4. Explicit `create_backend_diagram {projectId,document,idempotencyKey}` accepts
   the candidate through the normal validator. Its normalized document and
   targetHash must equal the accepted candidate. Never save implicitly on query.
5. `query_backend_diagram {projectId,pin,search:"",origin:"all",section:"elements",
   limit:100}` returns participants/steps/branches. `section:"links"` returns order
   IDs; members requires subjectId; gaps returns boundaries. Omit architecture
   level/root. Page all results using the same pin/filters; cursor mismatch409
   requires restarting the exact query. Canvas limits do not limit search.

## Semantics and history

B/C alternatives never imply B→C. Parallel peers stay unordered even when display
sorts IDs. Missing delivery does not create a receiver, response or retry.
Order is an acyclic partial order; duplicate endpoint pairs and cross-kind ID
collisions reject save. replyTo must address the corresponding request.
Branch paths follow the full parent chain. Unsafe sequence fragments use an
explained accessible list; the static canvas has no execution controls.

`save_backend_diagram` requires expectedVersion and a new idempotencyKey. Adding
or removing an order edge while steps remain unchanged appears in compare by
that edge's ID. Changing its origin keeps the ID. Reordering arrays is a no-op.
`compare_backend_diagrams {projectId,before,after,limit:100}` retains before-side
inspection for removed messages/order/evidence. After a version conflict, retain
local edits and explicitly reread or fork. Unknown result: replay the identical
saved request/key, never overwrite it with another body.

`create_backend_diagram_view {projectId,name,state:{diagram:pin,search:"",
origin:"all",selection:{type:"link",id:orderId},positions:[],collapsedIds:[]},
idempotencyKey}` creates an exact bookmark. Layout-only save uses
`save_backend_diagram_view` with the view's expectedVersion; it cannot change its
semantic pin. A newer diagram needs a new view. Historical selection remains
readable after edge removal, including after restart.

`fork_backend_diagram` takes source pin, explicit target, reason and key. If a
dependent document moves targets, supply architecture on that target. Evidence
from the former target remains historical, with gaps, never upgraded to current
proof by name. C4 → interactions → exact Flow/API/event evidence → Back retains
pins and selection. Inspect `backend-architecture` for C4 projection semantics.

## Human-readable modeling contract

This contract applies when importing or remodeling a backend **for understanding**.
It adds a human-facing deliverable to accurate capture; it does not change import
schemas, source fidelity or mutation authority. A request limited to raw extraction
can end after capture, explicitly described as such. Source-backed explanations
are static interpretations of the inspected revision, never proof of execution.

### Two deliverables with different purposes

- **Evidence layer:** exact declarations, source spans, native text, identities,
  call/control/data-access facts, contradictions and extraction gaps. Follow the
  selected import/sync protocol unchanged. Preserve low-level detail where that
  contract requires it; do not rename identifiers or drop proof to simplify UI.
- **Explanation layer:** authored capabilities, meaningful scenarios, participants,
  decisions, effects and outcomes, mapped to exact evidence. A commit receipt,
  successful builder, translated function name or complete source inventory does
  not establish that this layer exists or is useful.

Keep semantic names separate from source symbols. Where the format supports it,
use authored origin with a reason explaining the interpretation and exact refs
for verification. Use source_assertion only for the actual evidenced assertion,
not to make an inferred business interpretation look mechanically extracted.

### Decide what to import and explain

Before bulk work, inventory capabilities and externally meaningful entrypoints:
user/API requests, scheduled work, incoming events, external callbacks and manual
operations. For each, capture its actor/trigger, goal, observable result, data
read/changed, external systems, consequential branches and evidence availability.
The import protocol's source inventory is still required; this semantic inventory
is additional. Several functions may implement one scenario; one shared function
may support several scenarios. Neither function count nor endpoint count defines
scenario coverage. Mark unsupported or unexamined areas explicitly.

Organize the normal reading path around:

1. System purpose, participants and capability boundaries.
2. A capability's scenarios, named by their goals.
3. One scenario's meaningful algorithm, decisions and outcomes.
4. Optional exact implementation/proof drill-down.

Do not generate one architecture component per handler/helper, one scenario per
statement, or a separate top-level diagram per traversal chunk. Name saved views
by scope and purpose; retain exact navigation pins. Describe what each diagram
adds. Repeated system titles and boilerplate responsibilities fail review. Source
inspection remains available but must not be presented as the completed semantic
scenario. A saved view's layout alone cannot turn a raw Flow into an abstraction.

### Write actions and decisions in the reader's vocabulary

Use the product's language and a consistent glossary. Mocker display text defaults
to Russian; identifiers belong in evidence/details. Name scenarios as goals such
as «Получить ссылку на Литрес», actions as verbs with objects, and branches as
questions with explicit outcomes. Include who acts when responsibility matters.
Descriptions explain purpose, conditions and effects rather than repeating names
or the same caution on every card. Put shared static-source limitations once in
the scenario summary; keep specific uncertainties next to affected actions.

Never guess a domain meaning from a symbol alone. Inspect callers, data, tests,
configuration and available product documentation. Distinguish what code declares,
what tests expect and what an author interprets; none proves production execution.
If terminology or intent remains unclear, use the narrow supported statement and
record the question. A confident invented story is not an improvement.

### Collapse implementation, preserve behavior

Group a contiguous set of operations when it serves one goal and shares an actor,
preconditions and outcome. Keep a mapping to every supporting source span and
record where omitted mechanics are accounted for. Typical internal mechanics:
assignments, argument preparation, entering/exiting functions, DTO conversion,
ordinary diagnostic logging and response serialization. Retain their resulting
observable output, state effect or failure in the enclosing action.

Do not collapse across distinctions that change understanding: authorization,
validation, domain decisions, persistent state changes, external interactions,
transaction boundaries, retries/timeouts, asynchronous handoff, irreversible
side effects, materially different failures or audit obligations. Logging that
constitutes a business/audit requirement is meaningful; ordinary debugging output
usually is not. A nil/empty check may encode a real policy: translate the condition
and its outcome rather than automatically deleting it.

Preserve alternatives, guard conditions, parallel work, loops, ordering and error
propagation. Separate branches must not become a false sequential chain. Do not
invent a receiver, response, transaction, retry, or exactly-once guarantee from
an opaque call. If a nested procedure is independently useful, give it a named
subscenario with explicit input/output instead of inlining its body.

Aim for roughly 5–12 meaningful actions in an overview when the behavior warrants
it, not a required count or publication limit. A three-action scenario needs no
padding. A larger scenario needs justified decomposition, not omitted decisions.
Translating each statement into Russian without reducing implementation detail
still fails this contract.

### Before and after examples

These are illustrative transformations, not verified claims about a live project.
Validate the actual implementation before applying them.

| Implementation fragment | Human-facing meaning | What must remain verifiable |
| --- | --- | --- |
| Enter `LitresHandler.GetLink` | «Получить ссылку на Литрес» as the scenario goal; no separate enter-function action | Actual trigger and caller |
| `h.linkURL == ""` | «Ссылка на Литрес настроена?» | Exact setting and branch condition |
| Construct an error, log it, return an error response | «Сообщить, что ссылка на Литрес не настроена» | Error response, absence/presence of side effects; do not invent a status |
| Convert URL to a string and serialize the response | «Вернуть настроенную ссылку» | Actual returned value; not an invented external authorization call |
| Check access, then update a record | «Проверить право на изменение» → «Сохранить изменения» | Denied branch and the write remain distinct |
| Call an unavailable SDK and return its result | «Запросить результат у внешнего сервиса» with an explicit unknown behavior boundary | Known inputs/output and unknown retry/delivery behavior |

A verified GetLink-like implementation might produce a decision and two outcomes:
«Ссылка настроена?» → «Вернуть ссылку» / «Сообщить о недоступности». It does not
justify inventing login, token generation or data storage. Name the entry scenario
once instead of adding duplicated “start”, “handler”, and “enter function” cards.

### Choose an honest representation

Use the actual `backend-architecture` contract for responsibility boundaries;
`backend-interactions` for messages, participants, branches and partial ordering;
`backend-business-map` for commands/events/policies; `backend-lifecycle` for entity
states. Negotiate the relevant owner and capabilities before publication.

An interactions builder returns a candidate, not a finished explanation. Review
and, where supported, author a semantic companion before saving it. Its allowed
step kinds and branches remain exactly those documented above. Do not label a
local assignment as a network request or misuse a boundary step as a generic
action just to fit the schema. Business-map links also have a closed role/relation
matrix; do not fake domain events to draw arbitrary control flow.

If no available format preserves a needed local algorithm, produce the readable
scenario outline plus evidence map and record the missing representation. Do not
invent API fields, silently edit source-flow semantics, or claim that the UI
supports a new semantic scenario mode. Existing saved pins remain immutable; new
semantic documents/views and any navigation updates follow their real contracts.

### Working deliverable template

Keep this as an agent artifact, not extra unrecognized API fields. Translate
reader-facing content to the selected language. Use actual schema fields when
publishing and retain the richer ledger locally when no field represents it.

```text
Scenario: human goal; scope and intended reader
Basis: exact project/revision or other admitted target; inspected source scope
Actor and trigger:
Preconditions and inputs:
Success outcome; alternative/failure outcomes:
Steps:
  semantic ID | actor | action/decision | input -> effect/output | next/branch
Evidence:
  semantic ID | exact node/evidence/artifact refs and file spans | confidence/gap
Abstraction ledger:
  source block(s) | represented by semantic ID | mechanics grouped and why
Unknowns and conflicting evidence:
Related capability/subscenarios; exact navigation destinations if published:
Coverage: explained / unexamined / blocked scenarios, with reasons
Verification: semantic findings; readback/render results when published
```

Identifiers remain stable across wording edits within the chosen document
contract. Keep naming consistent across overview, scenario and navigation labels.
On a source change, review affected semantic statements against the new evidence;
do not relabel old evidence or advance saved pins silently.

### Acceptance before claiming a human-readable import

Check a representative pilot before bulk authoring, then review each delivered
scenario. These are semantic checks, not word-count or translation checks:

- A reader unfamiliar with the implementation can explain the trigger, goal,
  responsible actors, main outcome and consequential alternatives from the normal
  diagram entry path without opening code or decoding identifiers.
- Every displayed action adds meaning. No boilerplate entry/exit/assignment/logging
  ladder or duplicated endpoint→handler wrappers stands in for an algorithm.
- Every material branch, failure, side effect and external boundary in scope is
  represented, including cases hidden by the happy path. Grouping preserves order
  and dependencies; a short picture does not earn credit by losing behavior.
- Every claim can be traced to exact evidence or explicitly marked authored
  interpretation/unknown. Many source operations may support one semantic step;
  a one-to-one statement-to-card translation is a warning, not a coverage goal.
- Architecture/view titles distinguish responsibilities and scope. No unexplained
  “Platform” duplicates, source-directory catalog as business architecture, or
  top-level technical fragments presented as independent user scenarios.
- Readback matches the intended pin and content; desktop inspection verifies
  readable text, meaningful navigation and access to evidence. Search and a
  prettier layout alone do not count as a semantic improvement.

Report source capture and semantic explanation separately: scope, evidence gaps,
number of meaningful scenarios delivered, illustrative before/after, and remaining
format/navigation work. If only the source graph is ready, say “source captured;
human-readable scenarios incomplete”. Do not call a large raw Flow a successful
human explanation merely because import validation passed.

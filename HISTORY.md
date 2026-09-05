# mocker — the project log

**Not auto-loaded.** `CLAUDE.md` carries only what a session needs on every run:
the architecture, the bars, the hard rules, the shipped chain in one paragraph and
what is next in five lines. This file is the RECORD behind that paragraph — how
each slice arrived, what each gate cost, what each fleet run measured, and which
lesson was paid for rather than argued. Read it when you need the WHY of a slice,
when a gate or a fleet run is being planned, or when something in the tree looks
like an oversight and you want to know whether it was a decision.

The companion file is `CARVE-OUTS.md` — the list of holes, per slice. `DESIGN.md`
§23 is the state as built. `git log` WAS one commit per slice: the history this
file narrates (144 commits, 2026-08-16 to 2026-09-03) was left behind when the
repository was published and restarted from one commit on 2026-09-03, so every
commit hash cited below belongs to that history and resolves to nothing here.


This section exists because three neighbouring documents answer three different
questions and none answers the fourth. `DESIGN.md` says what is DESIGNED, and does not know
what of it is built. `CARVE-OUTS.md` says what is CARVED OUT, and that is
a list of holes, not a plan. `git log` says what is DONE, one commit per slice. What
nobody said is what is NEXT and why exactly that. That role was carried by `HANDOFF.md`,
deleted in `a5818e2` as spent, and it moved nowhere: the next slice
was designed outside the repository and did not appear in the repository at all.

## The slice log

**Shipped.** `P0` (authentication, workspaces, both planes, admin API) →
`P1a` (spec import, lazy `$ref` resolver, route table) → `P1b`
(body generator) → `P1c-1` (recipes, `op_overrides`, the auth preset) → `P1c-2`
(`when[]`, live state in RAM, traffic, custom endpoints) → `P1d-1` (the SPA inside
the binary) → `P1d-2` (the admin API contract and the test that does not let it lie) → `P1e`
(the six screens of §14). Then outside the phase numbering: `delay`/`pause`, Go 1.27,
`/mcp` with nine tools. Then `P2b` (the `Scenario` layer), `P2c` (checkpoints and
rollback), `P2d` (scenario clone and rename, debounce, checkpoint deletion),
`P2e` (`schema_patch` and three recipes), `P2f` (`POST .../preview` and splitting
`serveGenerated`), `A1` (`PUT .../endpoints/{eid}`), `A2` (twenty-nine
MCP tools; surface 9 → 38, allowlist 12 → 40), `A3` (per-component
compare-and-swap on write), `P3a` (the head of `P3`: a mock REMEMBERS a write —
resources derived from the spec, confirmed per workspace, populated
deterministically, and four verbs served out of SQLite), `P3b` (a checkpoint carries
a resource's configuration UPSERT-only so a rollback can never cascade its entity
rows away, `POST .../reset-data`'s `reseed`/`clear`, and the four resource verbs on
the MCP surface), `P3c` (the `ref` recipe: a generated body can carry a value a
confirmed resource really holds, resolved per request against that workspace's own
confirmed families, never failing the response it declines to resolve), `P3d`
(`checkpoints.data_snap` goes from an explicit NULL to real bytes: every checkpoint
capture now snapshots the workspace's confirmed entity rows alongside its
configuration, `POST .../rollback/{cid}` accepts `restoreData: true` and a
`confirmSlug` and restores them instead of refusing with a 400, and the history
screen renders the checkbox and slug control it never rendered before), `P3e`
(one level of nested resource families: `/orgs/{}/users` confirms only while
`/orgs` stays confirmed, is populated one row set per live parent row, is
served per scope with a `404 entity_not_found` for a scope no live parent
row anchors, and a `reset-data` reseed treats a parent and its confirmed
children as one group — `entities.parent_entity_id` and `resources.parent_id`
stay NULL by decision, not deferral), `P3g` (nesting raised from one level to
a ceiling of three: derivation loops to `maxNestingDepth`, confirming a family
still checks only its immediate parent — by induction that already proves the
whole chain confirmed — population is one row set per live ANCESTOR TUPLE
walked top down from the root, serving walks every ancestor in a nested
family's scope and answers `404 entity_not_found` naming the OUTERMOST missing
one, `reset-data`'s reseed groups a whole confirmed SUBTREE rather than a
parent and its direct children, and both cascade columns stay NULL — the same
decision `P3e` made, re-argued at the deeper chain rather than carried over
unexamined), `P3h` (a base-path parameter becomes a supported TENANT
dimension rather than a silent collision: `settings.basePathValues` declares
which values `basePath`'s `{param}` may take, every entity row carries the
base scope it belongs to (`entities.base_scope_key`, migration `0003`, the
first since `0002`), a confirm populates one row set per declared base value
× live ancestor tuple, a request resolves its own base scope POSITIONALLY
off its own path segments and refuses an undeclared value with the family's
own `404 entity_not_found` on all four verbs before touching storage, a
checkpoint captures and restores the base scope at `DataVersion 2` while a
`DataVersion 1` document still restores into the empty base scope, a `ref`
resolves within the serving request's own base scope, `MOCKER_MAX_ENTITIES`
becomes the knob actually behind the cap the code already enforced at 1000,
and the two consumers that used to literal-match `basePath` against a real
request path — `stripBasePath`, `authCheckPath` — read it segment-wise
instead), `P3f` (`resource_suggestions.gen` finally carries a value above 1:
`POST /api/specs/{id}/rederive` re-runs family derivation over an ALREADY
IMPORTED spec's stored bytes and writes the result as a new generation
exactly when it differs from the newest one, answering `changed` plus the
`added`/`removed` family names; derivation runs outside the write
transaction and the newest generation is read once before the write and
again inside it, a difference answering `ErrStaleGeneration`; every read
that resolves a family — `EnsureSuggestions`, and `internal/resources`'
`findSuggestion` THROUGH it rather than beside it — sees the newest
generation only, and no second query with its own `gen` predicate exists
anywhere; a spec with no suggestions row at all still gets generation 1
written unconditionally, which is the lazy backfill arriving through the
verb; and no migration was needed, because all four columns have been
`0001_init.sql`'s since P0).

## A3 — compare-and-swap on write

**`A3` closed the hole that `A2` shipped KNOWING about it**: each of the six
MCP write tools read an object whole and wrote it whole, so an override
made in the admin panel between the tool's read and its write was overwritten
without warning — that was right there in those tools' `Description`,
a documented limit, not a silent one. The slice added an `edit_version` column
PER ROW
(not `workspaces.revision` — that token's granularity and atomicity were
measured and rejected, details in the A3 decisions workspace) to all the tables
whose objects are edited whole: `op_overrides`, `custom_endpoints`, `workspaces`,
`scenarios`, allocated from the new `workspaces.edit_seq`
(`internal/store.AllocateEditVersion`). An `editVersion` expectation is now MANDATORY
on all five population routes (`PUT .../operations/{opKey}`,
`POST .../auth-preset`, `PUT .../endpoints/{eid}`, `PATCH /api/workspaces/{id}`,
`PUT .../scenarios/{sid}`), on all six MCP write tools and in the admin UI;
a conflict answers `409 edit_conflict` with `details` that carry what
the caller needs for a retry — the current document for a single object,
a tombstone for a deleted row, `staleVersions` for the preset. Idempotency
the slice does not solve and it is not in its perimeter.

## P3a — the first fleet-built slice

**`P3a` is the head of `P3` and the first slice this repository BUILT with a fleet
rather than by hand.** What it ships is in the `Architecture` section above; what
is worth knowing here is how it arrived and what that costs the next slice.

The decisions were gated first, alone, to exhaustion — sixteen rounds, and round 1
killed the premise: the first draft justified the slice by a defect this tree does
not have (the generator's list row and detail card already agree, and two tests
pin it). What was left after the rescope is the thing the generator genuinely
cannot do, which is remember. Only then was the orchestration script written, and
gated in its own right for five more rounds: blockers 10 → 6 → 3 → 0 → 0. The
manual's claim that these are two different gates asking two different questions —
is the design RIGHT, versus will the REPOSITORY let you build it — held: the
script gate's first round found a hand-pinned total in
`internal/admin/autocheckpoint_test.go` that sixteen rounds over the document had
not, because its reviewer read the tree with the implementing run's file-ownership
list in hand.

**The run itself took three launches to produce one measurement**, and the two
failures are worth more than the success. The first died on an API 403 at section
five; the abort guard the script had gained in gate round 18 ended it at 7 agents
of 24 rather than burning eight more sections, four gates and a docker build
against a tree nobody could finish. The second completed 28 of 32 and blocked four
on `output schema too large to classify safely` — the guard this operator was
proudest of, required KEYS instead of a free array, at fifty clause keys and
23,756 bytes. **A round-21 lens had MEASURED that number and the answer weighed
turns and tokens and never asked whether a schema that size would be accepted.**
The fix keeps the guarantee and bounds the width to seven keys a report; the
widest schema is now 3,378 bytes. The third launch returned 44 of 44 with
`goalMet: true` and named three of the fifty acceptance clauses as held by no
test — the first verdict of the whole gate that described the TREE rather than
which agent had died.

**Three clauses were closed by hand afterwards, each checked by MUTATION**, and
the shape of all three is the same and is the one worth carrying forward: a test
that passes for the wrong reason. Clause 23's own new test forced its cache to
miss by incrementing a field in test code rather than re-reading the column, so
deleting `bumpRevisionTx` from `Decline` turned nothing red. Clause 26's
`data_snap` test predated P3a and declared no resource, so it could not tell a
codec that would start filling that column the moment entity rows exist from one
that would not. Clause 48 compared two families that both declared an integer id.
Each fix was verified by breaking the code and watching the test go red, which is
the strongest form of the evidence an acceptance clause asks for — a walker
reasons about whether a test WOULD go red; a mutation makes it.

**And a fourth launch measured nothing.** After those fixes,
`resumeFromRunId` returned in 105 ms with zero tokens: resume keys on
`(prompt, opts)`, the fixes were to the TREE, so all 44 agents replayed and the
report was a verbatim copy describing tests that no longer exist in that form. **A
cached red is indistinguishable from a measured red.** If a future session
resumes a run after editing the repository rather than the script, that is what it
will get.

**The debt that lived outside the repository is closed: `DESIGN.md` is brought up to the code
(v4).** The document no longer describes a product driven by hands only:
`POST /mcp` appeared in §14.2, the second subject — an agent with a bearer key holding the
same power as the operator at the screen, up to irreversible deletion of a
workspace — in the threat model §15, and compare-and-swap on write in §14.1. In the same place,
in §23, are collected the four places where the document before v4 diverged from the code SILENTLY
(the missing `/mcp`, a threat model without the agent, `DELETE .../checkpoints/{cid}`
outside the route table, the unnamed `MOCKER_CHECKPOINT_DEBOUNCE`), and separately —
what is designed and not built. **The rule did not change in the process: `DESIGN.md`
is not edited by an agent.** v4 was made by a human on an explicit request; close the next
divergence the same way.

**Where the decisions live.** `A1` and `A2` were designed in a separate gate workspace
(`mocker-a-mcp`, 25 rounds, 196 findings, 58 blockers), `A3` —
in its own (`mocker-a3-cas`, thirteen rounds, 267
findings, 119 blockers, of them 151 findings introduced by the previous round's edits), and
`P3a` — in its own again (`mocker-p3a-resources`, twenty-two
rounds over TWO artifacts: sixteen on the decision document, then five more plus a
tooling round on the orchestration script that implements it, 441 findings and 142
blockers), and `P3b` — in its own again
(`mocker-p3b-resources`, twenty-two rounds over TWO artifacts as
well: seventeen on the decision document, closed on the stop rule at 19 → 0
blockers, then four on the script, 485 findings and 155 blockers), and `P3c` — in
its own again (`mocker-p3c-ref-recipe`, ten rounds over TWO
artifacts, five on the decision document and four on the script, both closed on the
stop rule, plus a tenth of kind `run` holding the launch itself; 146 findings, 59
blockers, and a filled `close/feedback.md` that is the first in this corpus to name
what the TOOL got wrong as well as what the gate did), and `P3h` — in its own
again (`mocker-p3h-basepath`, ELEVEN rounds over two artifacts,
86 findings and 20 blockers, closing on the stop rule at rounds 9 and 10 with the
external lens present, the eleventh of kind `run` holding the launch and the
post-run audit), and `P3f` — in its own again
(`mocker-p3f-rederive`, SEVENTEEN rounds over two
artifacts, the longest gate in the corpus by rounds; 128 findings and 58
blockers, closing on the stop rule at round 8 for the document and round 17
for the script), and
in each case the decisions document is in place — the authority on WHY the slice is
the way it is. **There are TEN such workspaces now, not the five this sentence
counted for three slices** (`mocker-a-mcp`, `mocker-a3-cas`, and one per `P3a`,
`P3b`, `P3c`, `P3d`, `P3e`, `P3g`, `P3h`, `P3f`); every one is outside this repository
and will outlive it only while those workspaces are intact, so everything that
must outlive it for certain is written over here.

## P3b — the first fleet run that launched

**`P3b` is the first slice this repository built with a fleet that RAN.** P3a was
gated with a fleet and its script never launched; this one did. What it ships is in
CLAUDE.md "Architecture"; what belongs here is how it arrived and what that cost.

**Two gates, and only the first closed on its own rule.** The decision document took
seventeen rounds, 19 → 0 blockers, closing on a reviewer round with zero and the
external lens present — the rule this workspace banked at round 8 after four
quota-blocked rounds, firing favourably for the first time. It made FIVE cuts, and the
distinction between them is new and worth carrying: three removed SCOPE (nested
families and `rederive`; the `ref` recipe; `data_snap` and everything downstream),
while two removed RULES THAT DEFENDED NOTHING and cost no capability at all. Round 12
cut an auto-checkpoint label whose row could not undo the confirm it was named for —
the wrapper snapshots BEFORE the handler, and the UPSERT-only restore makes that
permanent. Round 13 cut a `bundle.ValidateScenario` that defended a population of
zero: `b.Resources` is dereferenced in exactly two production lines in the whole tree
and both are the refusals themselves. **Both shared one signature — a rule that
reverses a previously recorded decision and cannot name what it defends against** —
and both were found by running the rule against the tree instead of reading it.

**The script gate did NOT close; it was stopped, and the numbers say why.** Four
rounds: 35/9 → 37/3 → 30/5 → 35/5 findings over blockers, with `fix-induced` at 16,
14, 16 — close to half of every round produced by the round before it, and four of
round 21's five blockers introduced by round 20's fixes. The oscillation lived in the
HARNESS, not the prompts: the producer schema made a full round trip (literal → call →
three hand-expanded 14 KB literals → call) and `abortIfDead` came back byte-identical
to its round-18 form. **Cause: the harness was being tuned against TOOL OUTPUT, and
the two tools pull opposite ways** — `wfcheck` rewards schemas it can read statically,
`wfdry` rewards runs that go deep, and one round's red-bar abort satisfied a rigour
argument while taking `wfdry` from 25 agents to ONE in six of seven modes, every
failure branch covered by nothing while all seven still printed OK. The harness is now
frozen in the script itself with its three settled trades and their numbers. Full
record and six tooling proposals: `~/projects/wfgate/FEEDBACK-p3b.md`.

**The stopping signal was not the blocker count, and that is the transferable part.**
Not one finding after round 19 was a defect in the SPECIFICATION — the fleet would
have built the right slice; the run would have misreported it, aborted, or destroyed
itself. The last blocker of the other kind was round 19's: `internal/mcp/tools.go` —
the one place any tool is registered, by its own doc comment — owned by no section,
leaving four new tools with nowhere to go. **What discriminates is a finding's
SUBJECT, not its LEVEL:** would a fleet build the wrong thing, or would it merely be
told something stale about a file it is about to read anyway.

**The run: 22 agents, 0 errors, 3 h 42 min, 3.7M tokens, and it returned
`green: false` HONESTLY.** Every gate closed clean, every bar green including
`make smoke`, `HEAD` unmoved, all six acceptance mutations restored. What made it
false was `goalMet`: of forty-eight acceptance clauses, **forty-seven were held by a
test and one was not** — and the acceptance step found that by MUTATION rather than by
reasoning, deleting the code and watching nothing go red.

**The clause it found, and the shape is one to recognise.** `ResetData` compares
`confirmSlug` twice (D3 R9): once before the transaction against the slug it just
read, once inside against a fresh `SELECT`. Only the second is authoritative — the
window between the two reads is exactly where another request renames the workspace.
Both comparisons call the same `compareConfirmSlug` and both answer
`ErrConfirmSlugMismatch`, **so an error code cannot say which of them fired** — and
the test's own comment reasoned correctly that `confirm_slug_mismatch` rather than
`stale_config` proves a slug check caught it, which is true and does not identify
WHICH. Its fixture renamed BEFORE the call, so the pre-check refused first and the
in-transaction read never executed. Closed by hand afterwards with
`TestResetData_RenameBetweenTheReadAndTheWrite_CaughtInsideTheTransaction`, which
lands the rename INSIDE the window through `resetPreWriteHook` — the seam the run
itself built and used only for the fence. **Verified by mutation in both directions**:
deleting step 3 reds that test and only that test, and `internal/admin` stays green,
confirming the acceptance step's own measurement.

**Four MINOR findings the run reported were closed by hand with this section** — three
in this file (a checkpoint aside spliced into the `Scenario` layer's one thought, "All
three" gate workspaces where there are now four, and a chunking cross-reference
pointing at a bullet scoped to `Confirm` alone, which now has its own `reset-data`
carve-out) and one in `api/openapi.json`, whose `confirmSlug` description called an
empty string a "mismatch" where the route distinguishes `confirm_slug_required` from
`confirm_slug_mismatch` two fields away.

## P3c — the `ref` recipe, both gates closed on their own rule

**`P3c` is the first slice whose BOTH gates closed on their own rule**, and the
first where the human broke a loop no reviewer could have. P3a's script never
launched; P3b's ran but its script gate was STOPPED rather than closed. This one:
decision gate five rounds, 15 → 7 → 10 → 6 → 0 blockers; script gate four rounds,
11 → 9 → 1 → 0; 146 findings and 59 blockers across both. The run itself was 15
agents, every bar green including `make smoke`, and nine of ten acceptance
properties held by an OBSERVED red under mutation.

**The design inverted TWICE, and both times on a fact from the code rather than on
taste.** Round 1 moved addressing from a family path to `resource_id`, because
`DESIGN.md` §9:416 says so and §9's own v3 change list records that choice being
made and reversed once already. Round 2 moved it back, on two facts round 1 did not
have: a `resources.id` does not survive `decline → confirm again`, which is the
repair P3a itself prescribes for a wrong resource (the decline DELETEs the row, the
re-confirm mints a fresh rowid, and a rollback over a decline does the same because
`bundle.ResourceEntry` deliberately carries no id); and `EntityStore.List` is not
workspace-scoped, so an id-addressed `ref` is one forgotten roster lookup away from
serving another workspace's rows on a plane that is unauthenticated by design,
while a PATH must go through the request's own roster to become an id at all. A
reversal justified by new evidence is not oscillation; the same reversal argued
from taste would have been.

**The most expensive lesson is about the ACCEPTANCE SECTION, and it is a lesson
about FORM.** Four rounds running, reviewers found clauses a wrong implementation
could still satisfy; four rounds running, the fix made the clause more specific;
and every added specific handed the next round a new surface. Clause kills went 8,
2, 5, 9 while the document around them settled. `gate-checks.md` section D already
described that exact trajectory, with its measurement — and section D is marked
AUTHOR-side while the skill's activity table routed only REVIEWERS to it, so it was
never read. Fixed upstream in `wfgate@5b01efb` as one table row.

**The loop broke on one sentence from the owner, and no reviewer could have
produced it.** The plan at that moment was to add a mutation column to all
twenty-eight clauses — more machinery, in the direction that had already failed
four times. "Simplify the acceptance part and stop trying to pre-think every
detail" reframed the defect from "these clauses are imprecise" to "this FORM
cannot work". The cut that followed — 28 clauses to 10 properties, each verified by
MUTATION and the fine work moved into the run where it is measured — closed the
gate two rounds later. **A gate optimises within a form; it does not question the
form.** Worth knowing when a count stops descending.

**And the run died on its last agent for a reason the gate had already measured.**
`accept` was refused with `output schema too large to classify safely` at 4,563
serialized bytes — the same refusal that killed four agents of the P3a run at
23,756. The gate had MEASURED 4,563, recorded that it sat above the corpus's only
known-accepted point of 3,378, and accepted the risk on the written grounds that
"what cannot be measured from here is acceptance itself, only size". That was
false. **One throwaway agent carrying the schema answers it in sixteen seconds and
49k tokens**, which is what settled it afterwards — declaring the per-property leaf
once instead of ten times brought it to 3,877, the probe accepted it, and
`resumeFromRunId` re-ran only that one agent because it is the script's last call.
The rule for next time is one line: **schema width has a test, it costs one cheap
agent, and it is run BEFORE the fan-out.**

**One acceptance property was mis-specified and the run proved it.** Property 10 —
"the 419-body golden is unchanged" — asks for a mutation the fixture cannot
express: the golden binds no recipes, so `recipeValue` returns before it reads the
resolver. The acceptance step demonstrated it by replacing the resolver with a
hardcoded one returning a very different value and getting 0 mismatches over all
419 variants. Round 3 of the decision gate had SPLIT that clause in two for exactly
this reason — the golden as a regression check, and a recipe-bearing request for
the priority chain — and the 28-to-10 cut kept the first, dropped the second, and
left the mutation requirement on the survivor. The document now says property 10 is
a regression check exempt from the rule, with the measurement beside it. **A cut is
an edit like any other and its DELETIONS are reviewed by nothing.**

## P3d — `data_snap`, and the re-measurement mechanism

**`P3d` is the first slice whose RUN validated a gate decision on its first
launch** — a failure mode the gate had argued for on reasoning alone turned out
to be the first thing the run did. Fifteen rounds over two artifacts, 314
findings, 85 blockers; the script
gate's own descent was 14 → 7 → 6 → 1 → 0 and it closed on the stop rule. The run
was 32 agents, 0 errors, 4 h 31 min, 4.95M tokens and 1,429 tool calls, and it
returned `green: true` with every bar measured AFTER the last edit — verified
afterwards by hand: `make test`, `make lint`, `make ui-test`, `make ui-lint` and
`make smoke`, the last at 225 checks.

**What decided that verdict is the mechanism three gate rounds were spent
building.** The acceptance step ran all five bars green and returned
`criterionMet: FALSE`: it had found BY MUTATION that acceptance property 3's
subject — a live confirmed family ABSENT from the checkpoint's document is
untouched under `restoreData: true` — was observed by no test, and it proved it
by making `restoreEntitiesTx` delete those rows too and watching the whole
`internal/checkpoints` suite and `internal/admin`'s handler tests stay green. The
accept gate raised it, its fix agent wrote
`TestRollback_dataLeavesALiveFamilyAbsentFromTheDocumentUntouched`, the recheck
passed, and a conditional re-measurement then re-verified all nine properties
over the edited tree and returned `criterionMet: TRUE`. **Before that mechanism
existed every acceptance term was computed BEFORE the accept gate, so this run
would have returned not-green on a `criterionMet` frozen at its pre-fix value —
red over a tree in which the hole had just been closed.** The gate filed that as
a blocker on reasoning alone; it was the first thing the run did.

**The per-section bar populations fired in the same direction and are worth
recognising.** The run's log carries `BAR FAILURES: guards golangciCheckpoints
fail` — an honest red from a section whose package was mid-ripple, banked as a
fact about the RUN and deliberately not gating the verdict, which is taken from
the last observation instead. Under the shape this gate started with, a CORRECT
run could not go green: the first section ordered to report a staged red would
have decided the verdict at agent 4 of 24, hours and a docker build later.

**Three lessons the gate paid for, all measured rather than argued.** The
launch's schema-width refusal floor is **4,233 bytes, not the 4,563 this corpus
carried** — one throwaway agent answered it in 82 ms at zero tokens, after two
rounds had argued the band from measurements of size alone; the same probe showed
the launch DOES resolve `$defs`/`$ref` and enforce bounds through one, while
`wfdry` does not, which is why a `$ref` is safe exactly where no verdict term
reads the stubbed value. A dry-run fixture fills a REQUIRED array and leaves an
optional one empty — requiredness, not item type, is what decides whether an
array can be a verdict term, and that killed a reviewer's proposal on the second
attempt at the same idea. And a cheap guard can suppress the coverage of the
expensive path it guards: skipping the re-measurement when the verdict was
already red made both conditional agents unreachable in every dry-run mode while
all seven still printed OK. Reverted, with its measurement.

**And the shape worth carrying past this repository: what closed the gate was
telling the reviewers that closing was ALLOWED.** Four rounds carried no such
clause and every one returned blockers. The fifth said, in all three prompts,
that an empty findings list is the expected and legitimate result and that
manufacturing a finding and downgrading a real one are both failure modes — and
all three lenses returned zero blockers while still filing fifteen real defects.
They did not go quiet; they reclassified, and every reclassification held when
checked against the tree. The uncomfortable half is that for four rounds the
blocker count was being read as a measurement of the ARTIFACT while some fraction
of it was a measurement of the PROMPT. Full record and the tool-side findings:
`~/projects/wfgate/FEEDBACK-p3d.md`.

**What the run carried OUT, and it is a to-do for a human rather than a slice.**
Seven findings, none blocking, four of them one shape — a comment that survived
the change it describes. `internal/bundle/bundle_test.go`'s doc still frames
`Bundle.Entities` as "a phase away" when D3 makes it permanently null BY DESIGN;
`internal/checkpoints/repo.go:399-402` still says the whole decode happens
outside the transaction when the `data_snap` half is deliberately inside
`rollbackTx`; `web/src/components/HistoryPage.tsx:387-392` still gives the
pre-`P3d` reason for `reset-data` having no undo and ends on a sentence the
Russian string beside it no longer says. The one with a rule behind it is
`internal/mcp/tools_history.go:260`: `rollback_workspace`'s `ConfirmSlug`
description now reads "required when restoreData is true" while the TOOL has
required it unconditionally since A2 — and `internal/mcp/confirm.go`'s own doc
requires all eight confirm-slug fields to read verbatim the same, "so the eight
read as one rule rather than eight paraphrases". It is now the only one that
differs.

## P3e — one level of nesting, and the cascade divergence

**`P3e` ships one level of nested resource families** (`/orgs/{}/users`
scoped under `/orgs`) and its own decision document — `mocker-p3e-nested`
— is the first in this corpus whose central call is a DIVERGENCE argued from a
trade rather than a carve-out: `DESIGN.md:505-508` asks for a physical
`entities.parent_entity_id` cascade, and this slice keeps both cascade columns
NULL on evidence rather than deferral (D9, CLAUDE.md "Architecture" gives the
argument in full). The short form: closing the one live sequence where a
`restoreData: true` rollback can make an orphaned child scope reachable again
against a different parent of the same id would mean reopening the LARGER
guarantee P3d shipped four days earlier
(`TestRollback_dataLeavesALiveFamilyAbsentFromTheDocumentUntouched`) — a live
`parent_entity_id` would let a config rollback's own entity DELETE cascade into
a child family the call never named. The read-side anchor check (D6.3) closes
the observable half — deleting a parent's row 404s every request into its
children's scope — and accepts the narrower hole in trade.

**The gate ran two artifacts, thirteen review rounds, closing both on the stop
rule: the decision document at nine rounds (blockers 10 → 8 → 7 → 2 → 2 → 1 →
3 → 1 → 0), the orchestration script at four (8 → 3 → 1 → 0) — 186 findings and
46 blockers total.** Round 1 alone filed ten blockers against the first full
draft, and the one worth naming is the shape every later round's own
positional-read rule traces back to: a `ref`-recipe-adjacent finding that
`resources.scope_params`, read by NAME at serving time, would silently serve a
nested family HALF the moment its collection and detail routes spelled the same
outer parameter differently (`/orgs/{orgId}/users` beside
`/orgs/{organizationId}/users/{id}` — both one family, since
`router.CanonicalPath` erases parameter names). The fix — read the scope
POSITIONALLY off the route that actually matched, the identical discipline
`router.DetailIDParam` already holds for the entity-key segment — became D5.6
and D6.1, and the second derivation fixture this slice adds to `internal/testspec`
deliberately spells its two routes' outer parameter differently for exactly
this reason, so the property has something to fail against. A later round (C2)
caught the same class of bug one layer up: `reset-data`'s reseed repopulating a
parent alone, while its child skipped for the child's OWN reason (population is
`parents × listSize`, so `over_caps` is likelier for the child than the
parent) — the identical resurrection `DESIGN.md:505-508` warns against,
arriving through the reset path instead of a live decline. D8.2's group rule
(a parent and its confirmed children reseed together or not at all) closes it.

**The acceptance step ran all five bars green and returned `goalMet: false`.**
`make test` (every package, `-race -count=1`), `make lint` (`go vet`), `make
ui-test` (25 files, 317 tests), `make ui-lint` and `make smoke` (the full
docker end-to-end run, including the new P3e block: import, confirm
parent+child, two disjoint scopes, a scoped POST, the unanchored-scope 404)
all passed, both before any mutation and again after the last revert.
Sixteen of D13's twenty-two properties held under their own named mutation.
**Six did not, and the shape is identical across all six: the mutation
applied, then `go test ./cmd/... ./internal/... -count=1` stayed FULLY
GREEN** — no test anywhere in the tree would have caught the wrong
implementation the property exists to fail. Three were blocker-severity in
the acceptance step's own findings — **P6**: D6.3's anchor check gated on
`isDetail` (armed on the detail route only) leaves the suite green; `make
smoke`'s own P3e block catches the GET half of it (a flip from 404 to `200
[]`) but never POSTs into an orphaned scope, so the "and writes nothing"
half of the property was observed by nothing. **P14**: deleting the arity
check from `scopeOf` leaves the suite green, because no
`resource_test.go` fixture ever sets a mismatched `ScopeParams` the way
decisions.md's own pinned fixture — the existing `/items` resource with
`ScopeParams: ["orgId"]` — describes. **P15**: reading the outer path
value by NAME instead of positionally leaves the suite green, because
`testspec.NestedDerivationDoc`'s own mismatched-spelling fixture (built for
exactly this property) was wired into `internal/specs/derive_test.go` for
derivation and nowhere else — not into a single `internal/mockplane`
serving test — and `scripts/smoke.sh`'s own fixture spells its outer
parameter identically on both routes, so it cannot discriminate this
either. Three were major-severity — **P2**: `EncodeScope`'s injectivity
over a value containing its own `/` delimiter has no unit test anywhere;
disabling `url.PathEscape` leaves the suite green. **P8a**: a reseed
re-scoping a child to the parent's NEW keys is unverified, because the
tree's one nested-reseed test never POSTs an extra parent row before
reseeding, so reading the parent's LIVE rows instead of its prepared set
is indistinguishable to it. **P8b**: a reseed group repopulated or skipped
ATOMICALLY is unverified, because no fixture builds a group where the
child exceeds caps while the parent does not, so leaving a successful
sibling repopulated beside a failed one goes undetected. **All six were
closed by hand afterward, each verified the way the acceptance step itself
verifies a property: the named mutation applied, the new test observed
red, then reverted** — new cases in `internal/mockplane/resource_test.go`
(P6, P14) and `internal/mockplane/resource_integration_test.go` (P15, on
`NestedDerivationDoc` at last), `internal/resources/nested_test.go` (P2,
P8a, P8b), a POST-to-an-orphaned-scope check added to `scripts/smoke.sh`'s
own P3e block, and a correction to `NestedDerivationDoc`'s own doc comment,
which had claimed `internal/mockplane` and `internal/checkpoints` both
imported it when, before this pass, neither did. **The closing was the RUN's
own**, not a later human pass: the accept gate raised all six, its fix agent
wrote the tests, its recheck passed, and the conditional re-measurement — the
mechanism P3d's gate argued into existence against no evidence but the corpus —
then re-verified all twenty-two properties over the EDITED tree and returned
`goalMet: true`, eighteen of them by an observed mutation naming the red test.
**Load-bearing on its second launch running**: without it the verdict's
acceptance terms would have been frozen at the first accept's `goalMet: false`
over a tree in which the holes had just been closed. The re-measurement then
carried two smaller findings of its own out of the run, and both were closed by
hand afterwards the same way: `internal/resources/nested_test.go`'s
`TestConfirm_ParentDeclinedBetweenTheReadAndTheWrite_CaughtInsideTheTransaction`
(D5.1's parent check exists TWICE, in `prepareConfirm` and again in
`fenceParentTx`, both answering the same `ErrParentNotConfirmed`, so no error
code says which fired — every earlier fixture arrived with the parent already
gone, so only the FIRST was ever exercised; `confirmPreWriteHook` lands the
decline inside the window, and under a mutation that makes the in-transaction
check return nil this is the ONLY test in the package that reds) and
`TestCreate_NestedFamilyMintsAfterTheFamilyWideTotal` (P8d's literal symptom —
the first POST after a P=2 x L=2 confirm mints `"5"`; the wrong `seq` was
previously caught only incidentally, by P8c's test, and only because the
collision happened to cross a scope boundary).

**The run itself: 29 agents, 0 errors, 4 h 40 min, 5.04M tokens, 1,629 tool
calls — and it returned `green: false` on exactly ONE of ten verdict terms.**
`dead`, `openBlockers`, `acceptBlockers`, `propertiesNotHeld` and `barFailures`
all empty; `finalBarsGreen`, `goalMet` and `remeasured` true; `propertyCount`
22. The tenth was `inconsistent: ["gate1"]` — a reviewer that returned
`verdict: "blockers"` while filing exactly one finding at MINOR, so the
self-contradiction guard refused the cheap reading and failed closed. Read by
hand, the finding is honestly minor and the `verdict` field is the slip. Worth
recording because of what it says about the verdict itself: nine of the ten
terms are computed from the TREE and the tenth from an agent's own metadata, so
a reviewer's slip is indistinguishable, in one boolean, from a defect in the
code. `HEAD` never moved; every bar was re-run by hand afterwards and all five
are green, `make smoke` at 236 PASS / 0 FAIL.

## P3g — nesting to a ceiling of three

**`P3g` raises nesting from the one level `P3e` shipped to a ceiling of
three**, and its own decision document —
`mocker-p3g-deep-nesting` — is the first in this corpus whose
central call is a divergence this project had already made once (`P3e`'s D9)
being RE-ARGUED rather than merely carried forward: the same NULL cascade
columns are re-examined at the deeper chain, and D9.2 finds the trade gets
STRONGER, not weaker, as depth grows — the blast radius a live cascade would
open grows with the chain, while the anchor walk closes the orphan half
observably at every level rather than one. The ceiling itself is
`maxNestingDepth = 3` (`internal/specs`), the smallest depth at which
derivation, the confirm walk, the anchor walk and the reseed group are all
LOOPS rather than a second hard-coded special case exercised once (D3.1), and
every rule `P3e` wrote for one level becomes a rule about a CHAIN without
widening what it checks: confirm and decline stay SINGLE-HOP at both ends,
because that single hop is what makes the whole-chain invariant hold by
induction rather than by a second, possibly disagreeing enforcement (D5.2);
population is one row set per live ANCESTOR TUPLE, walked top down from the
root rather than read off the immediate parent alone (D5.3); serving walks
every ancestor before touching storage and refuses at the FIRST miss, naming
the OUTERMOST one — the true cause, not merely the closest to the request
(D6.2); and `reset-data`'s reseed groups a whole confirmed SUBTREE, not a
parent and its direct children, because the identical resurrection risk `P3e`
closed one hop down reappears one hop further at depth (D8.2). `scripts/smoke.sh`
carries the one proof none of `internal/mockplane`'s or `internal/resources`'s
own tests can give — every one of them wires a fake store — that this whole
chain of rules holds through the real `cmd/mocker/main.go` wiring: a
three-level `/orgs/{}/teams/{}/users` import, all three families confirmed in
order, four pairwise-disjoint leaf scopes under two roots, a scoped write, and
a ROOT delete that 404s a whole leaf scope two hops down on both `GET` and
`POST` while its siblings under the other root stay byte-identical.

## P3h — a parameterised base path, and the post-run audit

**`P3h` is the first slice in this repository whose gate closed, whose fleet
ran, and whose RESULT was then audited decision by decision against the tree —
and the audit is what earned its place here.** The gate was eleven rounds over
two artifacts (86 findings, 20 blockers), closing on the stop rule at rounds 9
and 10 with the external lens present. The run was 30 agents, 0 errors,
6 h 47 min, 5.99M tokens, and it returned `green: false` on ONE of ten verdict
terms — `openBlockers`, two of them, both on `fenceResetTx`. Every other term
was green: all twenty-five acceptance properties held under their own named
mutations, every bar passed, the re-measurement ran. Bars re-run by hand
afterward: `make test`, `make lint`, `make ui-test` (319), `make ui-lint`, and
`make smoke` at 268 PASS / 0 FAIL with the P3h block's own 17 checks.

**Four holes were closed by hand after the run, each verified by MUTATION, and
two of the four share a shape worth recognising.** `fenceResetTx`'s own
`basePath`/`basePathValues` comparison — which the run's fix agent WROTE
correctly — was observed by no test at all: disabling it left the entire
`internal/resources` package green. `resources.ErrBaseScopeUndeclared` reached
`internal/admin`'s `answerResourceDecisionError` with no `case`, so D6.4's
refusal shipped **500 internal instead of 409 `base_scope_undeclared`** to both
the admin API and MCP; the wire-code constant did not exist anywhere in the
tree, and the function's own doc comment said "nine wire codes" without
noticing the tenth. The configured entity cap was observed at neither of its
two constructor doors. And `scripts/smoke.sh`'s own `byBaseScope` check passed
its `-H` flag as the request BODY, so it was not testing what it claimed.

**The shape: a decision implemented in the layer where it is easy to test, and
absent from the layer where it is used.** D6.4 lives in `internal/resources`
and never reached the dispatcher; the cap's wiring lives in two constructors
and was observed at neither. Both acceptance properties PASSED, because both
are written against the layer the decision NAMES — P9 discharges "the confirm
refuses with 409 `base_scope_undeclared`" by calling `Repo.Confirm` directly,
which cannot observe a wire code at all. A property that names a status code
and is proved through the repository has not tested the status code. The
post-run audit found 68 of 70 numbered decisions implemented, and both misses
were this.

**One thing the audit could not close, and it is named rather than left to be
found.** `MOCKER_MAX_ENTITIES` reaching `cmd/mocker/main.go`'s own
`resources.NewRepo` is observed by nothing: that package has no tests, and
`scripts/smoke.sh` runs the stack at the default value. The admin door is now
covered (`newTestServerCfg` takes a config hook), and the mock-plane door's
test says in its own doc comment that it is not, rather than reading as
covered.

## P3f — rederive, and the acceptance step earning its whole cost

**`P3f` is the longest gate in the corpus and the first slice whose fleet run
reported `green: false` about defects that were all REAL.** Seventeen rounds
over two artifacts — the decision document at rounds 1-8, the orchestration
script at 9-17 — 128 findings and 58 blockers, 212 edits over 1962 changed
lines, 9.1 hours. Both artifacts closed on the stop rule. The full write-up,
including what the tooling got wrong, is `~/projects/wfgate/FEEDBACK-p3f.md`.

Blocker descent, document: 11, 4, 2, 5, 2, 1, 1, 0.
Blocker descent, script: 5, 5, 7, 3, 4, 2, 5, 1, 0.

**The gate's own headline defect cost five rounds and was one defect.** The
script placed a MUTATION after a MEASUREMENT: Gate 4 — the last fix-bearing
gate, which spawns agents that write production code — ran AFTER the
acceptance phase. Every quantity the acceptance phase measured was therefore
stale-able, and there were eight of them. Rounds 9 through 13 each found ONE
of those accumulators, and each round's fix was locally correct, which is
exactly why the harness's existing "stop patching the guard" signal never
fired: nothing was being weakened, one array was being repaired per round,
forever. Reordering the run — Gate 4 before Accept, Accept the last measuring
phase, a read-only `Verify` last — deleted 149 lines and all five defect
classes at once.

**What ended the gate was a change of METHOD, not of artifact quality.** Every
one of round 15's five blockers was a MISSING MEMBER of a set the script
already had: two spawn sites without a guard ten siblings carried, one
property with no route into the unanswered bucket that twenty-four siblings
had, two commands with an unfilled `<placeholder>` where every other command
was complete. Five rounds had found that shape by SAMPLING, one per round.
Round 16's lens was told to stop sampling and build three COMPLETE tables by
command — every spawn against its guard, every command against whether it
actually runs, every verdict term against the phase that last writes it — and
it found the sixth instance, which five judgement rounds had walked past.
Round 17 generalised the shape ("a guard written for a CLASS, applied to a
SUBSET"), swept the whole file, and returned three classes complete with member
counts plus one gap. Those two rounds were the cheapest in the gate per defect
closed.

**The run: 24 agents, 9 phases, 3.3 hours, zero dead.** It returned
`green: false`, and every red term was a real defect rather than a
mis-measurement — the first time in this repository that is true of a whole
run.

**The acceptance step paid for itself on ONE property, and it is the strongest
evidence this repository has for mutation-based acceptance.** P4 asks that
`internal/resources` inherit the newest-generation predicate rather than own a
second one. Its named test passed. `make test` was green. The acceptance agent
applied P4's own mutation — a second, `gen`-blind `SELECT` inside
`findSuggestion` — and the test STAYED GREEN, which is the finding the
procedure exists to produce. The cause was the fixture: it narrowed the newest
generation by DELETING generation 1's rows, so a read with the `gen` predicate
and a read without it both returned nothing and the two implementations were
indistinguishable. The repair mints generation 2 without the two families and
LEAVES generation 1 standing; under the same mutation the test now fails with
`Confirm dropped family = <nil>, want ErrUnknownFamily`, verified by applying
the mutation, running, reverting and running again. A property that cannot
fail is worth nothing, and only running its mutation says which kind it is.

**Three defects the run found in its own work, and one it could not close.**
`scripts/smoke.sh`'s new rederive calls posted an empty body, which makes the
smoke helper skip the `Content-Type: application/json` header `enforceCSRF`
requires unconditionally — 415 on the first call, and the script aborted
before any later assertion. The smoke MCP block still asserted 42 tools
against a `toolCount` of 43, and `README.md` still said "forty-two". All
three are fixed. What could NOT be closed is that the whole P3f smoke block is
satisfiable by a handler hardcoded to return the canned no-op response: over
HTTP a spec's derivation output can never widen, because `Import` dedupes by
sha256 and mints a new `spec_id` for different bytes — the same fact that
makes a re-import unable to produce `gen > 1`. Recorded in `CARVE-OUTS.md`
with its measurement rather than papered over.

**Two things about reading a run's own report.** An LSP diagnostic snapshot
taken while agents were still editing reported a wall of compile errors that
did not exist — `go build`, `go vet` and `make test` were all clean on the
same tree, and a mid-run snapshot is not evidence about a finished one.
And a number outside a producer's named ownership drifts silently: the slice
added one `//nolint:gosec` and moved the contract to 53 operations, while
`CLAUDE.md` kept saying 24 nolints and, in one of its two places, 52 routes.
The docs producer owns paragraphs, not every sentence that happens to carry a
count, and re-deriving all eight of D2's numbers by command after the run is
what caught both.

## P4a — the report half of `P4` part one, and why the repair half never opened

`P4a` is `P4` part one as `HISTORY.md`'s own "Scoped down on 2026-08-31"
entry (above) described it, built out against `decisions.md`
(mocker-p4a-triage, D1-D12): one route, `GET /api/workspaces/{id}/drift`,
naming the three things §5 named as silently breaking — an orphaned
operation override, an orphaned confirmed resource family, a shadowing
custom endpoint — plus the read-only MCP tool that wraps it,
`get_workspace_drift`. Both are described in full in `CLAUDE.md`
"Architecture" and this file's own `CARVE-OUTS.md` companion carries every
line D9 declared out of scope, each with the argument rather than left to
read as an oversight.

**D1's own probe run is why this slice trusted its own scope going in.**
Before a line of decision text was written, three throwaway Go tests drove
the real handlers against the tree at `ca58f56` and were deleted afterwards:
an override on `POST /auth/login` surviving a re-bind to a spec that drops
it (still readable by its own singular route, silently absent from
`GET .../operations`'s list); a confirmed `/widgets` family surviving a
re-bind to a spec suggesting nothing, field-for-field identical in shape to
a live family, `buildFamiliesView`'s own comment already calling it "an
orphan left behind by a re-bind" without the view type carrying a field that
says so; and a custom `GET /gadgets` still winning over a spec operation of
the same canonical path after the spec was imported, both sides stored, no
cross-reference between the two admin lists. None of the three failures is
a missing REFUSAL — router rule 3 giving a custom route priority at equal
specificity is documented behaviour, `lookupOverride`'s literal-key
tolerance for a stale row is deliberate. What was missing in every case was
a REPORT, which is the whole of what this slice built.

**The predicate that mattered most was not new — `resources.Repo.
OrphanedFamilies` (D5) is an EXTRACTION.** "A confirmed family the bound
spec's newest suggestion generation does not name" was already written
twice, in two packages: `buildFamiliesView`'s own leftover branch and
`reset-data`'s `stranded` classification. Drift would have been a third
copy of a predicate whose two existing copies already sit in different
packages, which is exactly how a `MAX(gen)` filter starts disagreeing with
itself. `resources.OrphanedIn` is now the one function it lives in — pure, over a
suggestion list the caller already holds — and `OrphanedFamilies` is the
fetching wrapper that delegates to it, SET-wise: one `specID`, the whole
roster, one round trip. `reset.go`'s own per-row loop was rewritten onto the
wrapper rather than the reverse, closing the N-round-trip regression a
per-family signature would have reopened while still satisfying every naive
call-count check.

**The two doors are the run's own correction, and the document was what was
wrong.** `D5` first required `buildFamiliesView`'s `EnsureSuggestions` call to
be GONE, replaced by the wrapper, and pinned the post-slice count of that
symbol at six. The launch proved that unreachable: `buildFamiliesView` emits
one row per SUGGESTION, so it needs the list, and the wrapper returns a map.
A producer obeying the letter kept its own call and added the wrapper's —
two reads of one table per request, the second a later snapshot, and a
`rederive` between them able to show one family as both suggested and
orphaned in one response. The pure door removes the second read rather than
the first; the baseline moved to seven. A baseline no correct implementation
can meet is the false-failure direction `D8`'s own head warns about, and six
gate rounds over the document did not catch it — the run did, on its first
try, which is the argument for running one at all.

**Two named vectors could not be closed by a grep, and the decisions
document says so rather than pretending otherwise.** Whether `hasDrift` is
computed only as the disjunction of the three response arrays, and whether
`precededSpec` reads only the two stored `created_at` values, are both
properties a sufficiently motivated implementation can defeat by
construction — a helper, a reflection write, a tie-break on row ids. D8
assigns the question to the correctness reviewer holding the diff, in a
sentence quoted VERBATIM into that reviewer's own prompt rather than
paraphrased, specifically so a later reader (or a script gate reviewing the
orchestration script itself) can grep the prompt for the sentence and
confirm the vector was actually asked about, not merely gestured at.

**The two-section split (D11) put the predicate and the route in section 1,
the tool and the record in section 2** — not because `internal/mcp` and the
docs are a smaller subject, but because `api/openapi.json` is a COMPILE
boundary: `openapi_contract_test.go` checks the contract against
`Server.routes()` in both directions inside `make test`, so the route and
its contract entry have to land together or section 1's own bars go red.
Section 1 runs the Go pair only; section 2 runs `make ui-gen` before all
four bars plus `make smoke`, because the orval client is gitignored and a
tree that has never regenerated it type-checks against the PREVIOUS
contract — the one section 1 just changed.

**The numbers, all of them produced by a command against the tree at
`ca58f56` before any of D8's clauses were written (`decisions.md`'s own D2
table):** contract operations 53 → 54, MCP tools 43 → 44, `paths` keys 36 →
37 (a new key, not a new method on an existing one), coverage population
literal 53 → 54 with a THIRD `EXEMPT` entry — the first one in that map that
is not an infrastructure probe. Ten other numbers — the mutating-route
population, the auto-checkpoint label count, the migration count, the
`//nolint` total, every bar's own baseline result — are pinned to stay
BYTE-IDENTICAL, because this slice adds exactly one route and it is a GET.

**Why the ranked "What is NEXT" list drops `P4` part one entirely rather
than marking it done.** §5 also names a migration verb, two preserving
remedies, and a full schema diff; none of those is `P4a`'s, and none of them
is queued as a future slice either — each is a line item D9 turned down on
its own evidence (`CARVE-OUTS.md`'s own new "P4a" section carries the
argument for every one). What triage NEEDED — the three signals, readable
by an agent, pointing at verbs that already exist — is what shipped.

## A4 — the agent's reach, and the gate that carried two artifacts

Shipped 2026-09-01 at `bcca57d`, 21 files, +1124. Three verbs:
`GET /api/workspaces/{id}/resources/{family}/entities` with
`resources.Repo.ListFiltered` behind it, an MCP tool over the existing
`POST .../probe`, and the `since`/`lastId` cursor `list_traffic` had underneath
and never exposed. Contract 54 → 55, `toolCount` 44 → 46, `mcpAllowedRoutes`
46 → 48, no migration.

**It is named `A4` and not `P6` on a checkable ground**: it adds no streaming, no
transport change and no `DESIGN.md` §30 line. Do NOT read the `A` series as "the
agent-surface series" — a round-1 lens killed that framing with `DESIGN.md:1480`,
which calls `A1`–`A3` slices that "came out of the work, not out of the plan";
`A1` is the custom-endpoint full-replace write and reads as a screen usability
change, and `A3`'s compare-and-swap applies to admin-UI and MCP writes alike.
Only `A2` was ever MCP-specific.

**What the measurement found, and what it did not.** Three read-only agents
priced the surface before a single decision was written: 8 of 54 admin routes
had no MCP tool, five of them deliberately (two probes, and login/logout/`me`,
whose exclusion `loopback.go`'s own D12 comment already argues — a tool over
login hands an unthrottled credential oracle to anything holding the bearer
key). The three open ones were spec import, spec delete and probe. A fourth gap
was not a route at all and was found only by reading tool bodies: every admin
handler over the resource tables does `COUNT(*)`.

That fourth claim was overstated and a round-1 lens caught it. Entity ROWS were
never invisible — `internal/mockplane/traffic.go:222` records `ev.RespBody` for
a `resourceBranch` response like any other, so an agent has read row bodies
through `list_traffic` since `P3a`. The gap is a STRUCTURED read, not first
access, and the document was reworded to say so. A slice whose premise is wrong
by a third is a slice that gets built to the wrong size.

**Spec import stays human-only, and the warrant changed.** The original carve-out
argued cost — hundreds of kilobytes through the model's context. That invited
three workarounds and each bought a new threat: a path would be the FIRST
filesystem read by request anywhere in this tree, a URL would be the second
outgoing HTTP client (`internal/probe` is a separate package to keep it
singular), and an inline document pays the context anyway. The load-bearing
warrant is now an observation: `web/src/components/SpecsPage.tsx:88` renders
`<ImportSpecForm />` and it works. A human imports; the agent drives everything
downstream. The owner's decision, and the reason the UI half of this slice is
zero.

**Push from the server was investigated before it was refused, and the refusal
is the client's, not ours.** Measured against Claude Code's own documentation and
issue tracker: `notifications/resources/updated`, resource subscriptions,
`sampling/createMessage` and `elicitation/create` are each unimplemented and each
closed "not planned"; the Streamable HTTP `GET /mcp` server→client channel is not
documented as supported; the one channel that works,
`notifications/claude/channel`, is stdio-only, research preview, and reported to
drop messages, while mocker's endpoint is HTTP with a bearer key. So
`Stateless: true`, `JSONResponse: true` and the `subscriptions/listen` refusal all
STAY. An earlier draft argued the obstacle was per-session goroutines under
goleak; that was wrong and is recorded in the decision document so no later round
re-derives it — with the SDK's defaults a stateful session costs exactly ONE
goroutine, `jsonrpc2.(*Connection).readIncoming`, torn down synchronously by
`Close()`. The cost was never the problem.

The reachable form of real-time for an agent — a tool call that PARKS and returns
at the event, holding itself alive with `notifications/progress` — is real and is
deferred with its price written out: a per-handler write-deadline exemption,
`JSONResponse: false` (a whole-transport setting, so all tools move at once), a
registry of parked calls closed before `Shutdown` because `WriteTimeout` firing
does NOT cancel the request context, `PropagateRequestCancellation: true`, and a
client-side IDLE timeout of 5 minutes for HTTP that a progress notification
resets. That is the successor slice, not this one.

**The gate is the first in the corpus to carry two artifacts in one workspace**
(`mocker-a4-mcp-reach`), with independent descents: 9-6-0-0 over
rounds 1-4 on the decision document, then 2-2-1-0 over rounds 5-8 after `wf add`
brought the orchestration script in. Eight rounds, 70 findings, 20 blockers,
2.7 hours. Then an eleven-agent run: 13 agents in fact, 1.5 hours, `green: true`,
no dead agent, no skipped step, no open blocker, no bar failure, and the four
bars re-run by hand afterwards agreeing with it.

**Three of the gate's blockers were load-bearing, demonstrably.** The `after`
cursor was keyed on `entity_key` — `TEXT`, unpadded decimal, BINARY collation, so
`'10' < '9'` and pagination silently skips and reorders past the ninth row.
The `opKey` precedent was stated BACKWARDS: `opKeyFromPath` re-escapes an
already-decoded value, and an implementer copying it 404s on every real family.
And round 5 predicted that the probe tool fits none of the existing per-theme
tool files and would land in a new one invisible to `git diff HEAD`, so every
gate brief was made to carry `git status --porcelain`; the run created
`internal/mcp/tools_probe.go`, exactly there.

**And one clause the gate got wrong, in the direction section D calls worse.**
The acceptance baseline recorded "29 passing packages"; there are 28, at the
baseline commit and after. The clause "fails if `make test` reports fewer than
§3's 29" would have gone RED against a correct implementation. Four rounds passed
it, including two criterion lenses instructed to test every clause against a
correct implementation as well as a wrong one. The mechanism generalises: the
baseline was measured ONCE, early, by an agent, and every later round read it as
an OBSERVATION rather than as a claim. The acceptance agent then found the
discrepancy, classified it as a documentation error, filed a NOTE and returned
`goalMet: true` — right outcome, wrong route. A criterion an agent can argue with
is weaker than one it cannot.

**The number worth carrying to the next gate.** The corpus records 38 findings
and 12 blockers on a script added to a quiet gate; this one produced 7 and 2. The
script was authored against a document that had already spent four rounds pinning
the method signature, the SQL predicate, the error taxonomy, the fixture size and
every Fails-if clause — materially less to get wrong. If it holds again, rounds
spent on decisions are cheaper than rounds spent on a script. The other half of
the same split: fix-induced blockers are 4 of 20 across the gate but 3 of the
script's 5, and two script rounds were 100% fix-induced — two fixes from one
batch cancelling each other, invisible in the DIFF and immediate in the composed
RESULT. Full record: `wfgate`'s `docs/feedbacks/FEEDBACK-a4.md`.

## P6a — the first streaming slice, built inline after its gate

Shipped 2026-09-02. One new package (`internal/stream`), two admin routes
(`GET /api/workspaces/{id}/traffic/stream`, `GET /api/stream/stats`), one MCP
tool (`get_stream_stats`, `toolCount` 46 → 47, `mcpAllowedRoutes` 48 → 49),
three variables (23 → 26), the tree's first REBUILD migration
(`0004_traffic_autoincrement.sql`), a recorder that nudges, a shutdown order
that agrees with itself on both exit paths, a screen with a transport badge,
and `scripts/migration-check.sh`. Contract 55 → 57. `//nolint` count 25,
unchanged — three the first draft carried (`nilerr` on "a write failure is an
ordinary close") were designed away with an `ErrPeerGone` sentinel rather than
suppressed.

**How it arrived, and it is not how the twelve before it arrived.** The gate
workspace (`mocker-p6a-sse`) ran the full procedure on the
DECISIONS document — an interview of nine rounds and twenty-four questions on
2026-09-01, then a decisions gate that reached fourteen rounds — and then on
the orchestration SCRIPT: nineteen rounds over 12.8 hours, 124 findings, 63 at
blocker level, before the script measured zero on round 19 and the workflow
launched. The owner stopped that run during its first phase and asked for the
slice to be built inline from the frozen document instead, for time: the
fleet's five build phases each carried a review gate and a fix round, and the
estimate for that was longer than the estimate for one session implementing a
document it had itself written. So `P6a` is the first slice since `A1` whose
CODE went through no fleet and no per-section gate — the gate's whole output
was spent on the document and the script, and the code was written by one
agent against 25 decisions and 21 acceptance clauses, each of which it could
check as it went.

**What the decisions document got right, measured by what the code did not
have to invent.** Every "cannot be guessed from the code" fact in CLAUDE.md's
`internal/stream` paragraph was in D5 through D13 before a line existed: the
one-slot channel drained BEFORE the read, the channel never closed, a failed
read not re-arming the slot, the inner loop passing through the outer
`select`, `Last-Event-ID` over `?since=`, the 501/503 split, the shutdown
order and the listener-error path's reversal. The producer the stopped fleet
run had begun (`internal/stream`, eight files, one compile error) was taken
as the base rather than discarded: it was three `//nolint:nilerr` and a
`bodyclose` false positive away from the spec, and the fix for the first was
a sentinel error, not a suppression.

**What the document got wrong, and the test that found it.** D11 said the
session and the workspace are re-validated "on a timer". The package's own
reissued-id test — W created last, a stream on W, W deleted, a new workspace
created and asserted to carry W's id, traffic recorded on the new one
IMMEDIATELY — delivered the impostor's first frame on the first draft, because
the nudge for the new workspace's batch arrives INSIDE the recheck interval
and the read trusted the handshake. A18 in the acceptance would have caught
the same thing on the smoke stack seven seconds later. The repair is a
per-read identity check (one primary-key read on the reader pool) and a new
seam in the package, `stream.ErrRefused`, which lets a `ReadFunc` close the
connection rather than merely skip a page; the timer keeps the session half
and the idle case. The document's own text is left as written; CLAUDE.md
records the corrected shape.

**Two clauses the run could not observe and what stands for them.** A17 (the
migration preserves rows) cannot be seen by `make smoke`, which starts from an
empty volume; `scripts/migration-check.sh` builds the parent commit in a
scratch `git worktree` and the current tree in place, runs the first over a
data directory, the second over the same one, and compares every traffic row
by id and order — five rows, `user_version` 3 → 4, and the first row after a
clear at id 6 over a highest of 5 on its first run. A21 (the lifetime is a
`var` a test shortens, restored only after the goroutine is joined) is static
plus the package's own `TestServe_lifetimeExpiry`, whose join point is the
handler's `done` channel and not the client's EOF.

**Smoke, and the assertion it had to touch.** `scripts/smoke.sh`'s MCP
`tools/list` check read 44 against a server answering 46 since `A4` — the
suite was red on that one line for two slices. D25 brings it to 47 because the
slice must edit it anyway, and repairs nothing else red. The `P6a` block itself
is fourteen checks on the MCP stack: the container's own environment read
back through `docker inspect` (A8), the poll unchanged and no CORS allowance
on a bounded probe of the stream (A12), one connection held 35 s+ with pings
counted in a ten-second window and a frame after the thirty-second mark (A9,
A10), that same connection closed by a logout within 15 s (A11), stats read
three times around it (A13), a raw socket that never reads cut by the frame
deadline while a `curl -N` beside it receives every row (A14) with
`coalescedNudges` rising across the burst and standing still under the same
waves against a draining client (A20), 64 streams and a refused 65th (A19), a
clear that does not reissue ids (A16), a deleted workspace whose reissued id
serves nothing to the old connection (A18), and `get_stream_stats` (A6).

**Where the debt is, and what the one second reader found.** The fleet's
per-section review gates did not run on this code; the whole diff had one
out-of-band second reader instead (the Codex CLI, `gpt-5.6-luna` at `high`,
read-only, over the diff plus the decisions document). Nine findings, all
filed MAJOR, triaged against the code: six real and fixed in the same
session — the workspace identity check and `Repo.Since` are two statements
on the reader pool, not one snapshot, so the identity is now checked AFTER
the read as well as before it; the flush-support check ran after the 200
header had gone out, so a writer that took a deadline but could not flush
would have been refused one line too late — `supportsFlush` now walks the
Unwrap chain before `WriteHeader`; `Notify` ran under `writeMu`, exactly
what D5 forbade — `drainAndWrite` now releases the lock and announces the
union of what committed; the screen's retry timer could open a stream into
an unmounted screen — `open()` now returns on `disposed`; the migration
check's default parent was `HEAD`, which compares schema 4 against itself
once the slice is committed — it now defaults to the parent of the commit
that added `0004`; and smoke's A9 held 34.5 s and measured in whole seconds
against a 35-second clause — 35.5 s, in milliseconds. One was already fixed
before the review returned (the block's own check count), one was
strengthened beyond what it asked (A14 now demands the healthy client's
received ids form ONE contiguous range up to the newest, not merely the
right last id), and one refuted by the run itself (`kill -0` on a reaped
curl: bash reaps background children on SIGCHLD, and A11 and A18 both
observed the close through exactly that call). A tenth defect the reader
did not see, the smoke run did: `http_json` rewrites the cookie jar with
`-c` on every call, and 64 background `curl -b` readers racing that rewrite
lost one handshake to an empty jar — the block now snapshots the jar once.
That is a weaker review than every slice since `P3a` had, and it is the
price the owner chose for the time; the compensation is that the acceptance
clauses were written as observations before the code, and every one of them
ran.

## P6b — SSE mock endpoints, the first slice interviewed and built in one day

Shipped 2026-09-02, the same day as `P6a`. One migration
(`0005_custom_endpoints_stream.sql`, the second rebuild), two columns, a
stream document with its validator (`internal/customep/stream.go`), the
serving branch and the loop (`internal/mockplane/stream.go`), a
per-workspace-capped second `stream.Registry`, one traffic row per
connection, bundle v4, one route (`POST .../endpoints/preview`), one tool
(`preview_endpoint`, 47 → 48), three variables (26 → 29). Contract 57 → 58.
`//nolint` 25, unchanged.

**How it arrived.** Step −2 of the gate manual — the interview — and nothing
after it. `wf init` opened `mocker-p6b-sse-mock` before the
first question; four rounds and fifteen questions, each asked through the
question tool with the recommended answer first, settled the criterion
first (MCP-only, what "done" means, ten smoke observations, the preview
route, three variables, `stats.mock`) and then the design (tick body from
an inline schema, the constants, a second registry, bundle v4, Accept,
recording, the preview's shape, `closeWhenDone`, the frame `id:`, strict
`sse` rows, extending the two endpoint tools). The owner took the
recommendation on fourteen and overruled one — bundle v4 refuses v3 outright,
"мокером никто еще никогда не пользовался" — and the document was written,
`wf add`ed, and built inline the same afternoon. No decision gate, no
script gate, no fleet: the acceptance section (§3) is what stood in for
them, and every clause of it ran.

**What the build found that the document did not say.** Three things, each
recorded where the next reader looks. `TestAssembleResponseIsTheOnlySeam`
went red on the tick generator — the guard pins ONE `gen.Body` site — and
the answer was to widen the guard by name rather than route a stream frame
through a response's assembly it has nothing of (D4, `CARVE-OUTS.md`). The
`rtk` rewrite hook mangled a Go receiver named `rg` inside a heredoc into a
ripgrep invocation, which cost one build and is now a memory. And the
contract's `EndpointView.kind` becoming required broke one test fixture in
`web/src/test/fixtures.ts` — the only change under `web/` besides the
coverage guard's count and `EXEMPT` entry.

**What the smoke run observed**, on the MCP stack with
`MOCKER_STREAM_MAX_CONNS=3`, `MAX_LIFETIME=20` and `TRAFFIC_FRAMES=first`
read back from the container: the timeline in order at its delays and a
self-closing connection; a tick byte-identical across two connections; a
forced 503 aborting the handshake and a 300 ms delay moving the handshake
and not the frames; three streams open and the fourth refused with
`refusedCap` up by one while another workspace still opens; a 20-second
lifetime; one traffic row with `frames:3` and the first frame as its body;
an edit that bumped `revision`, left the open stream on its cadence and gave
a new one the edit; a rollback that brought `intervalMs` back through the
v4 snapshot; the tick floor and the frame cap refused by name on both
writers; and an http endpoint unchanged. `scripts/migration-check.sh`,
generalised to the newest migration's parent, carried five traffic rows and
an http endpoint across 4 → 5. The smoke run earned its place twice on the
way: its second run found `list_endpoints` answering no `kind`/`stream` at
all — the tool built its own copy of the endpoint projection instead of
sharing `toEndpointLine` with create/update, and the rollback clause read
`kind ''` off a row the rollback had in fact restored correctly; its first
run had died at the session-layer clause under `set -o pipefail`, because a
forced 503 answers with no `Content-Type` and the `grep` that read it
failed the pipeline. Three runs: 291/1, 312/1, 313/0.

**Where the debt is, and what the one second reader found.** As for
`P6a`: no per-section gate ran on this code; one out-of-band second reader
(the Codex CLI, `gpt-5.6-luna` at `high`, over the diff plus the decisions
document) read the whole of it. Ten findings — three BLOCKER, five MAJOR,
two MINOR — triaged against the code: six real and fixed in the session (a
`schema: null` tick decoded to a nil map and would have sent the generator
to a resolver a stream does not have — refused by name now; the preview's
loop never reached its frame limit when every tick body was over the cap —
bounded in steps; the smoke block counted its own checks wrong, twelve
against an expected eleven; the cap segment opened its three streams
before the previous segment's handlers had deregistered — it waits for
`mock.open` to read zero first; the first frame was copied for the traffic
row under `off` too and without a cap — copied only under `first`, cut at
`MOCKER_TRAFFIC_MAX_BODY`; and a stream request's `Truncated` cleared the
request half along with the response half — the request half is kept); two
accepted as notes (a `select` with several ready cases can write one frame
after the client left — every write now checks the context first; the
delay window in the smoke's session-layer clause is narrow by design,
because widening it would stop separating a handshake delay from a
per-frame one); one refuted by the code (the timeline IS validated before
the tick returns — the function validates both in order); and one recorded
in `CARVE-OUTS.md` rather than fixed (a recorded first frame is not run
through JSON redaction, which matters the day a frame can carry inbound
data — `P6d`'s, since every frame this slice records is operator-authored
or generated from an inline schema).

## P6c — the live-connection surface, interviewed and built in one afternoon

Shipped 2026-09-02, the third streaming slice of the day. No migration, no
variable, no bundle change: a connection identity and a per-connection inbox
in `internal/stream` (`conn.go`, `registry.go`, `push.go`), one new `select`
case in the mock loop (`internal/mockplane/stream.go`), two traffic-note
tokens, three routes under `/api/workspaces/{id}/connections`
(`internal/admin/connection_handlers.go`), three tools
(`internal/mcp/tools_stream_conns.go`, 48 → 51). Contract 58 → 61, `EXEMPT`
6 → 9, `//nolint` 25 unchanged, goleak 29 unchanged.

**How it arrived.** Step −2 again — `wf init mocker-p6c-live-conns` before
the first question — and this time the interview was two rounds and
fourteen questions through the question tool, criterion first (all three
verbs, mock plane only, push by connection id, smoke + numbers as the
"done", the two traffic tokens, no `confirmSlug`), then the design (a
counter id, the `connections` noun, the inbox in `stream.Conn` bounded in
frames, a synchronous push that returns the ordinal, a cancel with no close
frame, the full row, the `{open, cap, connections}` envelope, depth 16 and a
wait of two frame timeouts). The owner took every recommendation. The
document was written, `wf add`ed, frozen, and — the one thing P6b did not
do — read once by the external lens (`gpt-5.6-luna`, effort high) BEFORE
the build. It returned eight rows: two BLOCKERs that were real
(the reply-channel discipline and who cancels the connection context were
implemented but not written down), four MAJORs that were real (a
close/second-close race, an arithmetic slip on the `EXEMPT` count, an
acceptance clause that a tick landing before the push would fail against
correct code, a close bound tighter than one frame timeout), one MAJOR
asking for the middleware chain and the validator door to be named, and one
BLOCKER that was the round's own cost: the lens read the target's WORKING
TREE while the inline build had already started in it, and reported the
document's baseline as false because half of P6c already existed. Recorded
as a NOTE with the lesson — a lens that reads the tree at HEAD needs a tree
that IS at HEAD, so either the build waits for the barrier or the lens gets a
clean worktree. Every real finding was applied through `wf apply` in one
batch and closed by that commit; the code changes the two MAJORs needed
(`CloseByAdmin` as a compare-and-swap, `Lookup`/`Snapshot` skipping a
closing connection) landed in the same build.

**What the build found that the document did not say.** Four things. The
package test for the inbox deadlocked on its own cleanup order — `defer
reg.Close()` runs before `t.Cleanup(c.Release)`, and `Close` waits for the
connection `Release` would have released — which is why `deregister` is now
idempotent (a second call is a no-op, never a negative WaitGroup) and the
tests register `reg.Close` as a cleanup before opening anything. Two count
pins moved that no document names: `autoCheckpointExcludedNeverTouchesLayer`
12 → 14 and the mutating-route total 33 → 35. `toolErr` drops the envelope
message on every 5xx, which would have thrown away the one sentence a 504
`push_timeout` exists to carry ("it stays queued; do not resend blindly"),
so `push_stream_frame` uses `lb.do` and formats that one status itself. And
the bars themselves: an uncapped `golangci-lint` run right after a capped
`make test` took the tmux scope to a 5.8 GB peak and the kernel OOM killer
took the scope — the agent session included — twice in one afternoon
(`journalctl --user`, 10:54 and 11:10); `make lint` now runs under the same
`$(CAP)` as `test` and `ui`, and CLAUDE.md's Environment section says so.

**The bars after `P6c`** (`462e710`): `make test` 29 `ok` / 0 `FAIL`, `make
lint` `0 issues.`, `make ui-test` 328 passed, `make ui-lint` clean, `make
smoke` 320 `PASS` / 0 `FAIL` — the first smoke run was 304/1, the one FAIL a
pin of the tool count at 48 in the MCP block, and the run also stopped inside
P6c's own A3(e): a `grep -c | awk` over two reader files with no match exits
1 under `set -euo pipefail` and ended the script with nothing printed, which
is exactly the shape the smoke script's own comments warn about. Contract
61, tools 51, variables 29, migrations 5, bundle v4, `//nolint` 25, goleak
29. What P6c does NOT do — no broadcast, no close frame, an inbox in frames
not bytes, ids that do not survive a restart — is its `CARVE-OUTS.md`
section.

**The second external read, over the finished diff**, returned
`VERDICT: pass` with one MAJOR and one MINOR, both real and both fixed in
the follow-up commit: the close was `Lookup` then `CloseByAdmin`, two
registry operations, so a connection that deregistered in between could
still be CAS-closed and answer 204 — it is now ONE operation under the
registry's lock (`Registry.CloseByAdmin`); and the three contract
operations omitted the `500` every workspace-loading route can emit and the
`415` the CSRF middleware answers on a `DELETE`. Round 2 of the workspace
records both; neither had a clause in §3 that would have observed it, which
is the usual shape of what a diff reader finds that a document reader
cannot.

## P6d — WebSocket, the slice that added a module

Shipped 2026-09-02, the fourth streaming slice of one day and the one §30.15
put last because it is the only one that adds a module and lifts a §2
non-goal. `github.com/coder/websocket` v1.8.15 behind `internal/wsmock` (one
importer, held by `boundary_test.go`), `kind: "ws"` on a custom endpoint
with all four behaviours of §30.3, three variables (29 → 32), the CSRF
predicate of §30.10, the CSP sources of §30.14, the first inbound data this
plane records. No route, no tool, no migration, no bundle change: contract
61, tools 51, `EXEMPT` 9, migrations 5, bundle v4; goleak 29 → 30.

**How it arrived.** Step −2 again, and this time the round-1 lesson of P6c
was applied: `wf init mocker-p6d-websocket`, a two-round interview (eight
questions through the question tool; the owner took seven recommendations
and overruled one — a reactive rule may CLOSE the connection, "a server
that kicks you" being a reconnect scenario a client builder needs), the
document written and `wf add`ed, and the external lens (`gpt-5.6-luna`,
high) read it on a tree at HEAD with nothing built. It returned ten rows
and `VERDICT: blockers`: one BLOCKER that was wrong on the fact and right
on the gap — "StatusRecorder only exposes Unwrap, so the middleware chain
cannot hijack" — the library's own `hijack.go` walks `Unwrap` exactly as
`http.ResponseController` does and every wrapper on the mock path
implements it, but no clause observed the upgrade through the REAL chain,
so A5 now mounts the fixture under `httpx.RequestLog`; and nine MAJORs,
every one real: the reply-channel discipline of a reader that exits, the
connection context after `Hijack` (net/http stops cancelling it on peer
disconnect), a terminal close that a full byte-bounded queue could drop,
`Write`/`Ping`/`Close` unbound by the connection context, an A3(f) that a
correct implementation would pass by writing 13 KiB into kernel buffers,
a path-mode CSP with no host to name, a python client with no invocation
or self-check, and an ordinal policy that did not say which frames consume
one. All ten were applied through one `wf apply` batch and closed by that
commit before the first line of code.

**What the build found that the document did not say.** Three things, each
recorded in `CARVE-OUTS.md`. The reader's context cannot be the
connection's: coder/websocket tears the socket down the instant a Read's
context expires, so a reader under `wsCtx` defeated the 1001 close frame an
operator's close promises — the client saw an abrupt drop — and the reader
now reads under its OWN context, cancelled only after the closing handshake
(whose peer half that same reader is what reads). For the same reason the
reader keeps DRAINING after a rule's terminal close rather than stopping,
as D7 said: a stopped reader leaves every rule-close waiting out the
library's handshake timeout. And the admin predicate refuses an upgrade
`GET` with 415, not the document's 403: a handshake carries no JSON content
type, and that is the CSRF chain's first check — the smoke accepts either
and the carve-out says which one it is. One more fact the seam absorbed:
the library's read limit is the one close it performs without returning a
`CloseError`, so `wsmock.CloseStatus` recognises "read limited at" by its
text to record the 1009 the peer saw — the one place the library's wording
is depended on, inside the package that exists for that. And a white-box
test that ordered the gap marker after "the frame the loop was blocked on"
was wrong under load: a reader that outruns the loop's first `select` drops
before the first take, so the marker legitimately goes first; the test now
synchronises on the blocked write instead of assuming the order. The
smoke's own budget clause taught the last lesson twice: its first draft
sent 3000-byte echoes under a 1kb budget and saw nothing — correctly,
because a single reply larger than the whole budget can never be queued —
and the python client's byte-wise XOR masking took twenty seconds over
24 MB, long enough to hit the lifetime before the first read; the clause
now sends 20000 sub-budget frames with a zero mask key, and drops that
happen after the last write reach the row's `replies_dropped` on the way
out, where before they were reported only by the next write's marker.

**The second external read, over the finished diff**, returned
`VERDICT: pass` with four MAJORs and one MINOR, all real, all fixed in the
follow-up commit: the closing handshake ran under the library's own fixed
five-second wait, which a registry shutdown could not cancel (now a helper
goroutine, joined, cut off after one frame timeout by `CloseNow`); a peer
close racing another ready `select` case made the NEXT write fail and the
row said `write failed` instead of the peer's code (`readerDone` is now
checked before that answer); a push landing between `Open` and `SetInfo`
saw an empty `Kind` and could accept an `event` a WebSocket loop then
dropped (an unlabelled connection is neither listed nor addressable now);
the path-mode CSP named `ws://<admin host>` without `:*`, which a browser
would have refused on port 8080; and the smoke client's mask key was all
zeros, legal on the wire but not the unpredictable key RFC 6455 asks for
(a random key, XORed as two big integers so the burst stays fast). Neither
document reader found any of the five; every one is a fact about the
library's behaviour or a race the diff shows and prose does not.

**The bars after `P6d`**: `make test` 30 `ok` / 0 `FAIL` (29 + `wsmock`),
`make lint` `0 issues.`, `make ui-test` 328 passed, `make ui-lint` clean,
`make smoke` 332 `PASS` / 0 `FAIL` — the third run; the first two failed
the budget clause for the two reasons above and once for the MCP row
lacking `framesIn`. Contract 61, tools 51, `EXEMPT` 9, variables 32,
migrations 5, bundle v4, `//nolint` 28, goleak 30, one direct module more
in `go.mod` with zero transitive requirements. What P6d does NOT do is its
`CARVE-OUTS.md` section.

## A5 — the stack as one command, and the proxy contract as a test

Shipped 2026-09-02, the same day as the four streaming slices, and built
INLINE with no gate: the owner asked for it in two words («сделай пока что 1
и 2», a Russian string quoted as data — items 1 and 2 of a list the agent
had ranked a minute earlier), it adds no domain verb, no route, no tool, no
migration and no `DESIGN.md` divergence, and its whole deliverable is
operations — a Makefile, two scripts, a compose overlay, a Caddyfile, one Go
subcommand. What it answers is the question the owner opened with: can the
whole thing be brought up with literally one command, and does it serve
HTTPS out of the box. Before this slice: almost, and no. `make up` needed
`cp .env.example .env`, `make hash-password`, and a hand paste of the hash;
the container had no healthcheck (distroless, no curl — the compose file's
own comment said so); and `deploy/Caddyfile.example` opened with
"ILLUSTRATIVE ONLY — not exercised by P0", which was true of every slice
since — DESIGN §15/§16's reverse-proxy contract (`MOCKER_TRUST_PROXY`,
`X-Forwarded-Proto`, the `Secure` cookie, the forwarded client in the
traffic row) was code with unit tests and no live observation.

**What shipped.** `make up` depends on the FILE `.env`; on a bare clone it
runs `scripts/init-env.sh`, which copies the example, builds the image,
mints the hash through the image's own `hash-password` and prints the
generated password once — measured on this box by moving `.env` aside,
running `make up`, and logging in over http with the printed password.
`mocker healthcheck` is the second caller of `internal/probe` and the
reason that package promised to exist as one place: `probe.Readiness` is
the same client with a `Host` override, dialling `/readyz` on loopback with
`MOCKER_ADMIN_HOST`, after `config.Load` so a broken environment fails the
probe the same way it fails the server. `docker-compose.tls.yml` is an
overlay applied through `scripts/compose-tls.sh`: Caddy 2.11 with `tls
internal` for `*.<base>` and `<admin>`, `MOCKER_DEV=0`, Caddy on a static
`.254` of a fixed `MOCKER_TLS_SUBNET` and `MOCKER_TRUST_PROXY` set to that one
address, mocker's `:8080` emptied with `!reset`.
`scripts/smoke-tls.sh` makes nine observations of that stack; all nine
pass, and `make smoke` gained two of its own (the subcommand exits 0 exec'd
in the container; compose reports `healthy`).

**What the build found, in the order it was paid for.**

- **The dev box's egress proxy took the first run.** `HTTPS_PROXY` (a
  local egress proxy) is exported in every session on the dev box, and
  `curl --resolve mocker.local:8443:127.0.0.1` still sent a CONNECT for
  `mocker.local:8443` THROUGH it — the stack was up and healthy, Caddy had
  issued both certificates, and the wait loop timed out on a connection
  reset from the proxy. Plain-http `smoke.sh` never met this because
  `127.0.0.1` is in the proxy's own no-proxy list and a `Host` header is not
  a URL. Every curl in `smoke-tls.sh` carries `--noproxy '*'` now, and the
  comment beside the array says why.
- **`docker compose port` lies about an unpublished port.** Check 6 asks
  whether `!reset` held; `compose port mocker 8080` answered `invalid IP:0`
  with exit 0, which read as a binding and failed the check on a stack whose
  `ps` plainly showed `8080/tcp` unpublished. `docker port <cid>` prints one
  line per binding and nothing for none; the check reads that.
- **Caddy's internal CA issues a wildcard without being asked twice.** One
  site block `*.mock.local { tls internal }` obtained `*.mock.local` from
  the local issuer at startup, no DNS, no on-demand directive — check 4
  verifies the workspace host's chain against the exported root, and
  check 8's `wss://` handshake sends SNI for the same name.
- **The proxy contract holds end to end.** Check 3: the workspace record's
  `url` came back `https://alex.mock.local:8443` — scheme from a BELIEVED
  `X-Forwarded-Proto`, port from `r.Host` passed through unchanged. Check 5:
  the traffic row for a request through Caddy carried `fwdIp=172.30.10.1`
  (the bridge gateway, the host's own address on the stack) and
  `peerIp=172.30.10.3` (Caddy), the exact split §15 wants and nothing had
  ever observed live.
- **Streaming survives the proxy with no directives.** Check 7 counted 5
  tick frames inside a 3-second `--max-time` through `reverse_proxy` with no
  `flush_interval` — Caddy flushes `text/event-stream` on its own; a
  buffering proxy would have delivered none before curl hung up. Check 8 is
  P6d's reactive rule over `wss://` through the same block.
- **The review round moved the trust from a subnet to one address.** The
  first draft set `MOCKER_TRUST_PROXY` to the whole `/24`, and check 5
  passed on it. Five reviewers read the diff (three Opus lenses — security,
  shell/compose, Go and docs — and two the Codex CLI runs); the security lens'
  one major was that the subnet's `.1` is the docker HOST, from which
  mocker's unpublished `:8080` is still reachable on the bridge, so a local
  process could forge `X-Forwarded-For` into the traffic log and rotate the
  login limiter's per-address key on every attempt. Caddy now has a static
  `.254` (the first draft said `.2`, and mocker — up first — had already
  taken `.2` dynamically, so Caddy failed to start with "Address already
  in use") and the variable names exactly it; check 5 pins `peerIp` to that
  address rather than to "somewhere on the subnet". The same round moved
  the generated password off `argv` onto stdin (`ps` on the host showed it
  for the whole argon2 run), gave `.env` `umask 077`, made `init-env.sh`
  remove a half-made `.env` on failure (compose needs the file to exist
  before `run` can mint the hash, so a failed build used to leave the
  placeholder behind with `make up` then considering the file done),
  quoted `PASSWORD` through the environment in the Makefile (a password
  with a space hashed only its first word), pinned the two forwarding
  headers in the Caddyfile, and required `MOCKER_ADDR`'s port to be a bare
  decimal in the healthcheck — the tree's one hard rule about URL parts,
  applied to an environment value for consistency, not as a boundary. The
  adversarial the Codex CLI lens asked what the smoke does NOT prove, and three
  of its five answers became stronger checks: check 2 reads
  `MOCKER_DEV=0` off the container's own environment (a trusted
  `X-Forwarded-Proto` alone turns the cookie flag on, so the flag could
  not prove the overlay's override), check 4 reads the leaf's SAN through
  `openssl s_client` (`*.mock.local`, not a per-name leaf), check 7 runs
  once over HTTP/1.1 and once over HTTP/2, and check 9 reads the
  `service_healthy` condition off the RENDERED overlay rather than trusting
  a healthy container. The shell lens closed a vacuous pass too: an
  absence check (":8080 not published") that would have passed on a
  missing container now fails on one. The first the Codex CLI run — the broad
  security/shell/Go lens — timed out at 25 minutes with no verdict, still
  reading; its ground was covered by the three Opus lenses, and it was not
  rerun. What each reviewer asked for and was REFUSED is in
  `CARVE-OUTS.md`: HSTS, HTTP/3, renewal, idle WebSocket survival, active
  upstream health checks.
- **`/readyz`, not `/healthz`, and it mattered the same afternoon.** Caddy's
  `depends_on: condition: service_healthy` is what let the overlay skip a
  retry loop of its own; readiness pings the database, liveness does not,
  and a "healthy" that a not-yet-open database could earn is the one the
  sidecar must not be given.

**What it cost and what it did not.** Bars: `make test`, `make lint` at zero,
`make smoke-tls` 9/9, `make smoke` green with its two new checks; goleak
30 → 31 (`cmd/mocker` had no test file before its healthcheck). Variables
32 → 32 — the overlay's two knobs (`MOCKER_TLS_SUBNET`, `MOCKER_TLS_PORT`)
are compose-side, read by nothing in `internal/config`. Contract 61, tools
51, `EXEMPT` 9, migrations 5, bundle v4 — untouched. What was deliberately
not built is in `CARVE-OUTS.md` under `A5`: no ACME, no port 80, no Caddy
healthcheck of its own, no DNS sidecar for the wildcard, no path-mode run
of the TLS smoke.

**Raised the same hour and deferred, for the record — and built the same
day as `A6`, below.** The owner's other idea in the same message: a local
S3 (MinIO) in the stack so images can be uploaded and served as mock data. The agent's read, given in reply and not
yet decided: images as mock data already work through a pinned body with
`bodyEncoding: "base64"` and a `mediaType` (capped by `MOCKER_MAX_RESPONSE`);
what is missing is a multipart upload on the admin plane and a blob store
outside the JSON column — an `assets` route inside mocker keeps §16's one
image, one process, one volume, where a MinIO sidecar would not. Either
shape is a new intent `DESIGN.md` does not describe and is v11 by the
owner's hand, the way §30 arrived, before any slice opens.

## A6 — assets, the slice that made DESIGN v11 and was read twice before it was built

Shipped 2026-09-02, the last slice of a day that also shipped `P6a`–`P6d`
and `A5`. The owner raised it in the same message as the HTTPS work («надо бы
расширить форматы картинок, чтобы не только png принемал и дать возмодность
грузить файлы и использовать их в ответах» — a Russian string quoted as data),
and the first fact the slice established was that the "png only" half was
not true of the server: a pinned body with `bodyEncoding: "base64"` under any
non-executable `mediaType` already served a JPEG, a WebP or a PDF. What did
not exist was the file AS A FILE — one copy several responses share, an
upload that is not base64-in-JSON, and a working URL in a generated field.

**DESIGN v11 came first, and the agent wrote it at the owner's instruction.**
Assets are a new intent no section described, so the slice followed §30's
order: four questions to the owner (storage — SQLite BLOB; addressing —
`bodyRef` on a pinned variant plus an `asset_url` recipe; transport — a
raw-body `PUT`, never multipart; screen — none), a §32/§33 draft in the gate
workspace, and one more question: who inserts it. The owner chose «Вставь сам
как v11 от моего имени», so `65508b0` is the first DESIGN commit made by the
agent — inside the rule, not against it, because the decision on every line
is the owner's and the header says so.

**Two readers before the build, and the build waited for them** (P6c's own
lesson). The decisions document went to one Opus lens and one the Codex CLI run
(`high`, capped at 2 GB after the morning's OOM — see `A5`). Opus returned
nineteen findings and DO NOT BUILD YET; two were blockers that a green test
suite would never have shown: the `revision` bump was specified as
`workspaces.Repo.Update` inside the write transaction — a deadlock of the
single writer connection, a HANG, where HARD RULE 5 already had five private
copies of `bumpRevisionTx` for exactly that reason — and the mock route
built only the upload-side executable-type gate where §32.6 promises two.
The other majors were all in the one seam the slice depends on,
`assembleResponse`: a nil-deref on the literal case predicate (`rv.Override`
is nil for every request with no row), a media type that could not reach
the wire because the tail overwrites it unconditionally, a traffic note
markable only from a closure that holds the request, a `PreviewResult`
"note field" that did not exist, a wrong precedent (`Identity`/`Auth` live
on cached `Options`; `Ref` is the per-request hop), and "two constructions
of the workspace URL and a test that they agree" — the shape
`mediatype.go` was written to remove, replaced by `httpx.WorkspaceURL`. The
the Codex CLI reader, reading the round-0 text, found ten; eight were already
closed and two were new — a nil `assetLookup` must be handled, not called,
and "PreBuilt wins" was unreachable because a pinned variant already leaves
resource takeover. Every finding is folded into the decisions document by
number; the build started from that text.

**What the build itself found.** `mime.ParseMediaType` on the raw-body
route needed its own 415 sentence (the JSON one would have been wrong
advice on the one route where JSON is not wanted); Go's `ServeMux` cleans a
`..` segment into a 307 before any handler sees it, so the "bad name" case
is a space, not a dot-segment; the MCP `set_operation_variant` input mirrors
`overrides.Variant` field for field and had to learn `bodyRef` or no agent
could set one; the smoke's own first draft called that tool without the
`editVersion` A3 requires and was rewritten onto the same `PUT`-with-CAS
shape every other block uses; and `golangci-lint`'s `contextcheck` wanted
the lookup closure built from an explicit `ctx`, the shape `newRefResolver`
already had.

**What it cost.** Contract 61 → 64, tools 51 → 54, `EXEMPT` 9 → 12,
migrations 5 → 6 (the first new TABLE since P0), variables 32 → 34, goleak
31 → 32, `nolint` 28 → 29 (one `gosec` on the route's write), bundle v4
unchanged. `make test`, `make lint` at zero, `make ui-test`, `make smoke`
with a fifteen-check A6 block, `make smoke-tls` with one added check (an
upload through Caddy reports an `https://…:8443` url that fetches with its
ETag). Refused, with reasons, in `CARVE-OUTS.md`.

## What is NEXT, and why exactly that

**What is NEXT, and why exactly that.** This is the question the whole section
exists for. `P3b` closed the cascade trap (`config_snap` now carries
`resources` and `resource_decisions`, restored UPSERT-only, never DELETE — see
`internal/checkpoints` in CLAUDE.md "Architecture") and shipped `POST .../reset-data`;
`P3c` closed the `ref` recipe (see CLAUDE.md "Architecture" and its own carve-out list in `CARVE-OUTS.md`); `P3d` closed `data_snap` and the checkbox (see CLAUDE.md "Architecture" and its
own carve-out list in `CARVE-OUTS.md`); `P3e` closed one level of nested families, `P3g`
raised that to three, and `P3h` closed the parameterised-`basePath` hole
those five slices all accepted on the same terms (see CLAUDE.md "Architecture"
and all three slices' own carve-out lists in `CARVE-OUTS.md`). What is left is the rest
of what P3a's own carve-out list named, still unclaimed by any slice letter:

- **Nesting deeper than three levels.** `/orgs/{}/teams/{}/users/{}/badges`
  still derives nothing — `maxNestingDepth = 3` (`internal/specs`) is `P3g`'s
  own ceiling, on the identical evidence P3e gave for stopping at one: the
  anchor walk's per-request cost on a plane that is unauthenticated by
  design, and population multiplying once per level already reaches the
  existing 1000-row cap at `L ≥ 6` and depth 3 — a fourth level multiplies
  again against a cap neither slice moves.

The smaller independent thing this paragraph used to offer — `rederive`
(`gen > 1`) — is `P3f`, shipped; the `ref` recipe named alongside it before
that is `P3c`, also shipped. See CLAUDE.md "Architecture" for both.

**So the ranked list is now `P4`, and its first part is the one that bites.**
`P3f` gives an operator the two things that make triage POSSIBLE and neither
of them is triage: `POST .../rederive`'s own `added`/`removed` family names,
and the `stranded` classification `reset-data`'s reseed already gives a
confirmed family whose route family a rederive dropped. What is still missing
is everything that turns those into an action — which WORKSPACE a dropped
family stranded, an `orphaned[]` field on the wire, a screen that lists them,
and a migration verb (§5, `POST .../migrate-workspaces`). That is `P4` part
one, and `P3f` is the reason it can now be built at all: before the new verb
there was no way to produce a second generation over an existing spec, so
there was nothing for a migration to migrate BETWEEN.

### `P4` part one, scoped — decided 2026-08-31 before any gate opened

A mapping pass over the tree established what §5's three entities already cost,
and the answer changed the slice's shape twice. Recorded here because a scoping
decision made before the gate opens is the one nothing else in this repository
would preserve.

**What is already built, by entity.** `resources` is the cheapest and it is
nearly done: `buildFamiliesView` (`internal/admin/resource_handlers.go`) ALREADY
merges the current spec's suggestions with every confirmed row and its own
comment already calls the leftovers "an orphan left behind by a re-bind", and
`findSuggestion` returning nil is the same predicate `reset-data`'s `stranded`
classification runs on. A real end-to-end test already rebinds a workspace to a
different spec through the HTTP route and asserts the orphans still list. What
is missing is one boolean on the wire and the verb. `op_overrides` is next: the
serve-time tolerance for a stale row is deliberate and documented
(`lookupOverride`), but `handleListOperations` walks the SPEC's operations and
looks each one up among the stored rows, so an orphaned row is never visited and
never reaches the wire at all — the fix is to walk the UNION. `custom_endpoints`
is cheap in data and empty in code: both sides of the comparison are already
stored (`customep`'s `CanonicalPath`, the spec's own canonical set), and nothing
compares them; the precedence rule that makes a shadow safe rather than a
conflict is already in `internal/router`.

**Cut ONE: the two schema-diff halves.** §5 asks for an override that references
a field the response schema no longer has, and a resource whose `entity_schema`
diverged. There is no schema comparison anywhere in this tree — not in
`internal/specs`, `internal/gen`, `internal/openapi` or `internal/bundle` — and
`resources.EntitySchema` is a JSON POINTER, not a schema body, so comparing two
of them detects the item schema MOVING inside the document and not a property
added, removed or retyped where it stands. A real diff means resolving both
pointers through the resolver and walking two schema trees: new infrastructure,
shared by both asks, and the one component of this slice with no reusable
primitive to anchor an estimate on. It is a second slice hiding inside §5, and
it is cut. The three signals the triage exists for — the operation is gone, the
family is stranded, the path is in the spec now — need none of it.

**Cut TWO: the reattachment heuristic.** §22's first open question, open since
before `P0`, asks whether an orphaned override is rematched by `operationId`, by
path similarity, or by hand. The answer for this slice is NONE of the three
automatically: report the orphan and offer explicit per-row actions. An
automatic rematch guesses on the user's behalf in the one situation where only
the user knows whether the new operation is the same operation.

**No screen, and that is policy rather than a saving.** The agent is primary and
a screen is optional — see `CLAUDE.md`'s amended coverage invariant. The verbs
ship with their MCP tools and an `EXEMPT` entry naming the policy; the
triage becomes something an agent does on noticing the drift, rather than a
screen a human must remember to open.

**Size.** With both cuts, the same order as `P3f` — one gate, one fleet run.
Two routes (read the report, repair a row), two MCP tools, three detections, the
contract 53 → 55 and the tool count 43 → 45. Three subjects instead of one, but
each detection is small, and there is no body generation in it at all.

**That "two routes" estimate did not survive the gate itself, and the actual
shape is smaller by one whole route.** `decisions.md`'s own D4.1 found what
this paragraph's own mapping pass had not yet asked: every "repair a row"
action already had a verb — the three existing DELETEs and declines §5 also
names — so a façade route over them would only be a second path into the
same handlers, the shape `CallAsMCP` exists specifically to avoid. `P4a`
therefore shipped ONE route and ONE tool, not two of each: the contract
went 53 → 54, the tool count 43 → 44. See the `P4a` slice entry above (right
after `P3f`) for what actually shipped and why the second route never
opened.

**And that debt was closed a third time: `DESIGN.md` is v8 and knows about
`P3h`.** Brought there on the owner's explicit request the day the slice
landed. §7 loses the one statement `P3h` made FALSE — the import rule
claimed `variables` are expanded with defaults, and a variable a
`servers[].url` uses without declaring stays a literal `{name}`, which is
where the whole hole came from; §13's two shapes that claim completeness
gain `entities.base_scope_key` with its moved index and
`settings.basePathValues`; §16's `MOCKER_MAX_ENTITIES` drops from `10000` to
`1000` with the reason beside it, that one having been a documented LIE for
five slices rather than a divergence. §23 gains a `Shipped` row, counts `P3`
as seven slices, and carries four new divergences marked `(P3h)` — the base
scope as a SECOND addressing axis rather than a fourth nesting level, a
DECLARED value list where §11 derives everything from the spec, the
positional read, and the wired cap — while "Designed and NOT built" loses
the parameterised `basePath` it carried through six slices and gains the two
narrower things left behind it. §28 is the changelog.

**What v8 deliberately did NOT do**, same rule as v4 through v7: §11 still
addresses a row by ONE scope and still describes nesting by
`parent_entity_id` with `ON DELETE CASCADE`; §9's table still says a `ref`
addresses `resource:42.id`; §14's route table still shows a resource CRUD
surface nobody built. **And §11:508-511 stands for a third version
running**, now for a second reason: `P3h` neither softens that warning nor
is described by it — §11 has one scope and no word for a second axis, and
inventing one there would rewrite a warning into a specification. **The rule
itself has not changed: an agent does not edit `DESIGN.md`.** v4 through v8
were each made on an explicit request; the next divergence is closed the
same way.

**And a fourth time, the same day: `DESIGN.md` is v9 and knows about `P3f`.**
This one is unlike the three before it, because nothing in sections 1–22 was
FALSE. §7:243 already called the suggestion set "versioned on `rederive`",
§11:504 already called it spec-level, and §13's `gen` column has carried the
comment "rederive does not overwrite" plus a share of the table's unique key
since `P0`. The document described the shipped behaviour before it existed —
the one case in the whole file where the intent and the code agree without
either having moved — so v9 edits none of it. What v9 records is §23: a
`Shipped` row that makes `P3` eight slices, ONE new divergence, and one
deletion from "Designed and NOT built". The divergence is the route's SCOPE.
The shipped verb is `POST /api/specs/{id}/rederive`; §14:899 draws
`POST /api/workspaces/:id/resources/rederive`, and §14's own table disagrees
with §7 and §11 on this — derivation takes a DOCUMENT and has never taken a
workspace, and what stays per-workspace is exactly what §11:505-507 says
stays, the DECISIONS. **§14's line is left as it is, deliberately**: correcting
it to the shipped spelling would delete the only place the document records
that it once put derivation under a workspace, which is the reading §23's new
entry exists to argue against — and an argument needs the thing it argues
with. §5 is untouched for a stronger reason still: it is not stale, it is the
SPECIFICATION of `P4` part one, and §22's first open question still names it.
§29 is the changelog.

**The three have letters, from the split that produced `P3c`.** The old `P3c` was
one slice holding four items; a mapping pass over fifteen subsystems established
that they share no seam — each lands in a different layer and none reuses another's
mechanism — so they were split, and `ref` went first because it is the only one of
the four with no new route, no contract change, no migration, no screen and no MCP
tool. What the split gave the other three: **`P3d`** was `data_snap` and the
checkbox, shipped — see CLAUDE.md "Architecture"; **`P3e`** was nested families,
shipped (one level — see CLAUDE.md "Architecture" and its own carve-out list);
**`P3f`** is `rederive`, still unclaimed.

**And after `P3f`, the next thing is a PHASE, not a slice — `P4`, and it is
worth naming here because nothing else in this file ranks it.** `P4` is
"team scenarios" in `DESIGN.md` §19's own words, and it is three parts with very
different value. **Spec re-import with orphaned overrides (§5,
`POST .../migrate-workspaces`) is the one that bites every user every sprint**:
today an override whose operation vanished from a new spec version silently stops
applying, a resource whose `route_family` disappeared is silently stranded, and a
custom endpoint whose path has since appeared in the spec silently shadows the
very operation whose absence created it — §5 designs the triage screen for all
three and none of it is built, so a mock setup rots invisibly against a moving
spec. **Bundle export/import over HTTP is second**: §2's own decision table
records the trade ("git review of the config is lost → compensated by the bundle
(§17)") and the compensation is the half that was never built, so a set of mocks
cannot ride in the repository beside the e2e tests that use it, cannot be
code-reviewed, and cannot move between installations. **Fork is third and
weakest** — its original warrant ("a cheap undo before checkpoints exist") burned
when `P2c` shipped, and what is left is handing a new teammate a copy of a
configured workspace. Everything else outstanding is either `P2`'s UI debt
(Monaco with the schema tree, the recipe editor — recipes are SHOWN and never
edited) or `P5` (real isolation: the admin plane is one shared password and
anyone who logs in can edit anyone's workspace, which is a policy call about the
network rather than a gap in the code).

**And one thing that is NOT on that list, because it is now known to be harder than
it looks.** `rederive` is not "re-import bumps the generation": `internal/specs`'s
`Import` dedupes by sha256 inside its own transaction and mints a NEW `spec_id` for
new bytes, so a re-import can never produce `gen > 1` at all. The schema already
carries the intent — `0001_init.sql:85`, `gen INTEGER NOT NULL DEFAULT 1, --
rederive adds a generation, never overwrites` — so the verb is a new one over an
EXISTING `spec_id`, laying a second generation beside the first. And the read side
is not ready for it: `listSuggestions` selects `WHERE spec_id = ? AND route_family
!= ?` with no `gen` filter and without `gen` in the SELECT list at all, so the
moment a second generation exists both surface together and the caller cannot tell
them apart.

### Streaming — WS/SSE, requested 2026-09-01, and it is THREE questions

**Asked as one thing — "mock and play with WebSocket/SSE" — and the tree
answers that it is three, with different standing each.** Written down before a
gate opens because the sorting is the expensive part and none of it is derivable
from the request.

**One: the traffic feed over SSE or WS. Designed, never built, contradicts
nothing.** `DESIGN.md:905` names the route verbatim in its own route table —
`WS/SSE /api/workspaces/:id/traffic/stream (P2)` — §1170 gives the reason and,
in the same breath, the requirement: "In P1 — polling (`?since=`). WS and SSE in
P2: corporate proxies often cut WS, and fallbacks are mandatory anyway", so a
slice taking this half inherits the obligation to keep `?since=` working beside
it rather than replacing it. §1268 puts it in P2's table. The screen already
says so about itself: `web/src/components/TrafficPage.tsx:37` carries "P1:
polling only, WS/SSE is P2". This half is UI/transport debt of exactly the kind
`P2`'s Monaco and recipe editor already are, and it is the cheapest of the
three.

**Two: an SSE MOCK endpoint. Not designed, and not refused either.** The
non-goal at `DESIGN.md:78` reads "GraphQL, gRPC, WebSocket mocks" — SSE is not
in that bullet and is not anywhere else on the list. So serving a mock
`text/event-stream` operation is a capability the design neither asks for nor
turns down, which is a different position from the third question below and must
not be collapsed into it.

**Three: a WebSocket MOCK endpoint. `DESIGN.md:78` refuses it BY NAME**, in one
bullet with GraphQL and gRPC mocks, under §2's non-goals. This repository's own
rule decides what that means: an agent does not edit `DESIGN.md`, and when the
code and the document disagree the code is wrong until a human says otherwise —
so this half cannot be gated until either a human writes a v10 moving that
bullet, or the slice is scoped to record the divergence in `CARVE-OUTS.md`. The
second is the weaker of the two here, and deliberately so: §78 is a statement of
INTENT about what this product is, not an implementation detail a slice may
diverge from on its own measurement, which is what every carve-out on record so
far has been.

**Four facts price all three, and every one of them is already measured in this
tree rather than argued here.**

1. **`cmd/mocker/main.go:297` sets `WriteTimeout: 30 * time.Second` on the ONE
   `http.Server` that wraps BOTH planes**, so any stream is cut at thirty
   seconds. This is not a discovery: `internal/mcp/mcp.go:122-125` already
   writes it down, and `internal/mcp` already dodges it rather than solving it —
   `JSONResponse: true` plus refusing `subscriptions/listen` means the only
   `text/event-stream` this product serves today is one that finishes inside the
   window. A streaming slice has to move that bound, and it is GLOBAL over both
   planes rather than a per-route knob. `net/http` offers exactly one shape that
   keeps the global bound while exempting a handler —
   `http.ResponseController.SetWriteDeadline`, Go 1.20+, and this tree is Go
   1.27 — which is the answer a slice should reach for before it reaches for
   raising or zeroing the server-wide value.
2. **The same comment names the second half: `httpServer.Shutdown` blocks on a
   live stream for the whole drain window.** A long-lived connection is
   therefore a shutdown question, not only a serving question, and this project
   has already paid there once — the goleak paragraph in `CLAUDE.md` records
   that a `Run`/`Close` protocol returning earlier than its own goroutines "lost
   the last traffic records at shutdown".
3. **goleak runs in all 27 packages with tests and its ignore list is not to be
   extended** (`internal/testleak`). A stream is a goroutine per connection with
   a lifetime longer than a request — precisely the class those harnesses exist
   to fail. Every one of the three questions above meets this, including the
   cheapest.
4. **A WebSocket upgrade needs `http.Hijacker` or `ResponseController`,
   hand-rolled RFC 6455 framing, or a dependency — and this tree is stdlib
   `net/http` only, "there is no framework and there will not be one".**
   `gorilla/websocket` or `golang.org/x/net/websocket` would be the FIRST
   non-stdlib HTTP dependency in the tree, against a delivery story whose whole
   point is one static CGO-free binary — the same argument that already rejected
   `bytedance/sonic` after it had been integrated in full. SSE needs none of
   this: `text/event-stream`, a flush loop, and fact 1. That asymmetry is the
   strongest reason the three questions are ranked in the order they are written
   here, and it is why a slice that takes one and two is a different size of
   thing from one that takes three.

**What is NOT settled and belongs to an interview rather than to this entry:**
which layer a streamed mock lives in (a fifth response layer, a mode on an
existing operation override, or a custom-endpoint kind); whether a stream is
recorded in `traffic` at all and if so whether per frame or per connection;
whether the session layer's `fail_next` and the pause apply to a connection or
to a frame; and what a scenario snapshot does with any of it. None of those has
an answer in the tree today, and guessing at one here would be the interview
answering its own questions.

### The interview happened the same day, and `DESIGN.md` v10 is its output

**Everything above this line is the entry as it was written BEFORE v10, and it is
left exactly as written.** Two things a reader needs in order not to be misled by
it. First, every `DESIGN.md:NNN` above is **v9 numbering**: v10 appended §30 and
§31 and edited four places in §§1–4, which shifted every line after §4 by **+35**
— `:905` is now `:940`, `§1170` is `§1205`, `§1268` is `§1303`. Second, and worse
for a careless reader, **`DESIGN.md:78` no longer says what this entry quotes it
saying**: the bullet split, and the WebSocket half now records that it WAS refused
rather than that it IS. The quotation stands as a quotation of v9.

**What the owner decided, 2026-09-01, in his own words and by answering four
direct questions.** He asked for all of it unblocked — "я хочу, чтобы SSE и
WebSocket можно было легко манипулировать, играться" — which is a Russian string
from the request and is reproduced, not translated. The four answers, each of
which the design could not have guessed:

1. **All four behaviours**, not a subset: a scripted frame timeline, reactive
   (`when[]` on an inbound frame), generated from `internal/gen` on a tick, and
   echo.
2. **Custom endpoints only.** A stream is never derived from a spec — which is
   forced for WebSocket (OpenAPI 3.1 cannot describe one) and chosen for SSE.
3. **Only WebSocket and SSE leave §2's non-goal bullet.** GraphQL and gRPC stay
   out of scope, with their reason written down instead of inherited.
4. **`github.com/coder/websocket`**, over hand-rolling RFC 6455 and over
   SSE-first-defer-WS. This is the answer to fact 4 above, and it is the one place
   where a measured cost overturned a standing rule rather than merely priced it.

**So question three is CLOSED and it closed the way the entry said it would have
to** — "cannot be gated until either a human writes a v10 moving that bullet, or
the slice is scoped to record the divergence in `CARVE-OUTS.md`". The first path
was taken, which is the stronger of the two and was named as such at the time.
Nothing was carved out.

**What the four unsettled questions were answered with**, since this entry is
where a later reader will look for them, each now argued in full in the section
named beside it:

- **Which layer** — none new. A stream is a `custom_endpoints` row with a `kind`
  column, so it is a **Workspace**-layer object; the Scenario layer carries none
  (custom endpoints never were in a scenario, because the runtime keys them by row
  id and a row inside a snapshot blob has no id) — §30.2, §30.4.
- **Traffic** — one event per CONNECTION always, per-frame recording **off** by
  default, because redaction dispatches on a content type a frame does not have, a
  binary frame would be stored verbatim, auth-path suppression is path-based and a
  stream has one path for a whole connection, and ten frames a second rolls the
  thousand-row retention in a hundred seconds — §30.13.
- **`fail_next` and the pause** — per **connection**, at the handshake, never per
  frame. Per frame a `fail_next: 3` would be spent inside three ticks of one
  connection; per connection it means "the third reconnect succeeds", which is
  both what an operator wants and the only version a client can observe — §30.4.
- **A scenario snapshot** — carries nothing, as above, so the canonical "everything
  fails" scenario cannot make a stream fail. That is inherited from custom
  endpoints, not introduced, and it belongs in `CARVE-OUTS.md` — §30.4.

**Two things v10 deliberately left open**, and they are the likeliest v11: where a
pushed frame lives (it is session-shaped, but `internal/livestate` holds directives
keyed by operation, not payloads with delivery targets — and `P6c` lands the push
verb one slice BEFORE the reactive behaviour that would have answered it), and
whether the caps are the right numbers (every one is chosen by analogy on a plane
§18 refused to cap *because* it must withstand an e2e run; the cautionary
precedent is `MOCKER_MAX_ENTITIES`, advertised at ten thousand while the code
enforced a thousand, for three slices). §30.16.

**How this one was designed, and it is not how the ten before it were.** There is
no gate workspace for v10. It was three independent
design agents — storage/layers, serving/lifecycle, contract/MCP/UI/threat-model —
each asked for a proposal ending in "where this design is weakest", synthesised in
one session, with four decisions put to the owner directly at the two points where
guessing would have made the work useless if wrong. The adversarial half was
bought with those three "weakest" sections rather than with gate rounds, and the
difference is worth naming rather than hiding: **v10 has had no reviewer other
than its author and its owner**, which is the ordinary standing of a design
document before its first slice opens a gate, and `P6a` is where that debt comes
due.

**`P6a`, `P6b` and `P6c` all shipped 2026-09-02 and leave this list; `P6d`
heads it.** See the `P6a`, `P6b` and `P6c` sections above for how each
arrived, `CARVE-OUTS.md` for what they do not do, and CLAUDE.md "Where we
are" for the order the remaining two keep.

### Where streaming stands after 2026-09-02, and what the next two cost

**Done, in one day, both inline.** `P6a` (`d0219d9`): the admin traffic
feed over SSE and every infrastructure fact §30 said the rest depends on —
`internal/stream`, the per-handler write-deadline exemption,
registry-close-before-`Shutdown` on both exit paths, session and workspace
re-validation, a goleak harness for a package that holds connections, the
`traffic` AUTOINCREMENT rebuild. `P6b` (`342cf1d`): SSE mock endpoints —
`kind: "sse"` on a custom endpoint, a scripted timeline and a generated
tick, a second per-workspace-capped registry, one traffic row per
connection, bundle v4, the draft preview, three mock-plane variables. Both
went through step −2 (P6b) or the full gate on the document and script
(P6a), and both were BUILT with no fleet and no per-section gate, with one
external second reader over the whole diff each. The bars after `P6b`:
`make smoke` 313 PASS / 0 FAIL, contract 58, tools 48, variables 29,
migrations 5, bundle v4, `//nolint` 25, goleak 29.

**What is left of §30.15, and the order question the owner asked on
2026-09-02 ("а websocket когда будет?").** Two slices carry server code,
one carries none:

- **`P6c` — the live-connection surface** — SHIPPED the same day, after this
  paragraph was written (see the `P6c` section above). It answered §30.16's
  first question the way the pricing here guessed it would: a
  per-connection inbox in `stream.Conn`, RAM only, never the session
  package. The estimate ("about three hours inline") held.
- **`P6d` — WebSocket** — SHIPPED the same day too (see the `P6d` section
  above): `kind: "ws"`, `github.com/coder/websocket` behind
  `internal/wsmock`, reactive and echo, the reader goroutine joined before
  the handler returns, the three variables, the predicate widened on the
  admin host, the CSP naming `ws:`/`wss:`, the first inbound frame recorded
  redacted. Priced here at six to seven hours inline; measured from
  `wf init` (12:10) to the feature commit (13:50) it was 1 h 40 min, and
  2 h 10 min to the fix commit that closed the diff read's five findings —
  the estimate assumed a hand-written framing layer's worth of care that
  the library and the two prior slices' loop absorbed. The one thing the
  estimate did get right is WHERE the cost sat: the two facts that broke
  the closing handshake were the library's, not the design's.
- **`P6e` — the browser-side test client and the connections panel.** No
  server code; under the A4 rule (MCP-only, no new screens) it is the one
  slice whose whole deliverable is a screen, and it waits for the owner to
  lift that rule for it or to drop it.

**The order.** §30.15 puts `P6d` last because it is the only slice that
adds a module and lifts a §2 non-goal, not because `P6c` is a prerequisite
— technically both stand on `P6b` alone. The recommendation stayed
`P6c → P6d`: `P6c` settles the push-frame question, and a WebSocket slice
that arrived first would have to answer it on its own, in a package whose
subject is a transport. The owner chose `P6c` first; it shipped the same
afternoon, and `P6d` followed it the same day — four streaming slices
between one morning and one evening, 2026-09-02, every one interviewed,
read by an external lens and built inline. Of §30.15's five only `P6e` is
left, and it is not code that holds it back but a rule: the A4 rule says a
new route ships with no screen, and `P6e` IS a screen. The owner lifts the
rule for it, or drops the slice.

## How `DESIGN.md` was brought up to the code, v5 through v9

**That debt was closed once already: `DESIGN.md` reached v5 and learned that the
resource layer exists.**
It was brought there on the owner's explicit request after `P3c` landed, and it
recorded what the three slices shipped (§23's table), the SEVEN places the
implementation went elsewhere than the design, and what is left of `P3` item by
item rather than as a phase. The seven divergences are the ones this file had been
carrying alone: the wrapper found by ARITY instead of §11:511's name list, serving
as a BRANCH instead of §6:166-169's `stateful` variant mode, three routes instead
of §14's own resource route table, `reset-data`'s own body shape plus the checkbox
§14 draws and that build did not render, and three about `ref` — addressed by
route path instead of §9:416's `resource_id`, `restrict` refused by name instead
of implemented, and no scope filtering because scopes do not exist yet.

**And that debt was closed a second time: `DESIGN.md` is v6 and knows about
`P3d` and `P3e`.** Brought there on the owner's explicit request the day `P3e`
landed, after six readers checked v5 section by section against the tree and
found nine drifts, every one of them real. §23 gains two `Shipped` rows and says
`P3` is five slices rather than three; §13's `data_snap` comment stops saying
"only if requested" (capture has been UNCONDITIONAL since `P3d` — the
request-time choice is on the RESTORE, because a checkpoint that holds rows only
when someone thought to ask is a checkpoint you cannot roll back to) and its four
`CREATE TABLE` blocks finally carry the `edit_version`/`edit_seq` columns `A3`
added, which §14.1 had been cross-referencing into a DDL that did not have them
since that slice shipped; §14's rollback body gains the `confirmSlug` this build
requires whenever `restoreData` is true. **Three new divergences are recorded,
and one of them is the largest this project has made.** Two predate `P3d` and
were written down nowhere — the shipped UI stack is Mantine 9 and not Tailwind,
and checkpoint restore, `reset-data` and confirm population are each ONE declared
unchunked transaction against §11:527-528 and §18's blanket N-row rule. The third
is `P3e`'s: nesting is addressed by `scope_key`, not by the `parent_entity_id`
column §11:505-508 specifies. **That paragraph does not merely describe an
unbuilt feature — it WARNS, in as many words, against the approach this build
went on to ship** ("linking only through `scope_key` is not enough — the records
stay orphaned and resurrect when an organization with the same id is created"),
and the warning is still true here. It is left exactly as written, because
rewriting it to agree with the code would delete the one sentence saying what the
shipped implementation is exposed to; §23 carries the trade instead, next to
`P3d`'s guarantee that it buys. §26 is the changelog.

**What v6 deliberately did NOT do**, and it is the same rule v4 and v5 kept: §9's
recipe table still says a `ref` addresses `resource:42.id` and filters by a
compatible scope; §11 still describes nesting by `parent_entity_id` with `ON
DELETE CASCADE`, still names `uuid` and `template` id strategies that do not
exist, and still recognises the list wrapper by a NAME LIST where the code uses
arity; §14's original route table still shows the full `resources`/`entities`
CRUD surface, `rederive`, `fork`, `export`/`import` and `migrate-workspaces`; and
§18 and §20 still state the N-row chunking rule without its three exceptions.
Those sections are the DESIGN — the only record of why the code is the way it is
— and editing them to agree with the code would destroy it. §23 is where the
state in fact lives.

**The rule itself has not changed: an agent does not edit `DESIGN.md`.** v4, v5
and v6 were each made on an explicit request, and the next version is closed the
same way.


## `A7` — the guide (2026-09-02)

The request, in the owner's words: «надо собрать доку о том как пользоваться
этим обширным инструментом как для людей так и для агентов. обязательно
подготовь скилл. возможно придумай еще доп методы чтобы агенты легко
разбирались как пользоваться мокером», and mid-slice: «для людей прям
отдельную страницу можно заверстать в ui» — both Russian strings quoted as
data. No gate workspace: the slice writes prose over shipped behaviour and
adds one static tool, and the decisions below are small enough to stand here.

**What shipped.** `docs/USER-GUIDE.md` (Russian, rendered at `/guide` by
`GuidePage.tsx` through `marked`); `skills/mocker/` (SKILL.md and four
references, English); `internal/guide` (embedded copies, `Instructions()`,
`Topic()`); `get_guide` (tool 55, empty `toolRoutes` row); `initialize`'s
`instructions`; `make guide-sync`; the docs index; README and CLAUDE.md
paragraphs.

**Decisions.**

1. *One owner per text.* The skill directory owns the agent guide because a
   skill is discovered by path and installed by copying that path; the
   binary needs the same text to serve it; go:embed cannot reach above its
   package. So `internal/guide` holds byte copies and a test, not a symlink
   (go:embed refuses symlinks) and not a `go:generate` (a generated file that
   is also committed is the same copy with a worse excuse). The human guide
   has no copy at all: Vite's `?raw` import reads `docs/USER-GUIDE.md` in
   place, with `server.fs.allow` widened by one directory for the dev server.
2. *Instructions, not resources.* DESIGN §14.2 says the MCP server publishes
   `tools` only. An MCP `resource` for the guide would have been the
   protocol's own answer and a divergence to record; `instructions` is a
   field of the initialize result every client already receives, and a tool
   is what every client can call. The instructions stay under 4 KiB (a test
   pins it) because a host injects them into every session.
3. *A screen after A4.* The rule says a new ROUTE ships with no screen.
   `/guide` adds no route, makes no call past the guard, and was asked for
   by the owner directly; `routes.test.tsx` asserts the "no call" half so
   the screen cannot quietly grow an API dependency later.
4. *The human guide is Russian.* Every operator-facing string of the product
   is, and a manual rendered inside the panel is one of them; the agent
   guide and the skill are English for the token-economy reason the
   language rule already states.
5. *Spec import stays a human verb.* The cookbook says so rather than adding
   a tool: the owner's A4 words («так что спеки человек продолжит
   импортировать») are the decision, and the http reference shows the curl
   for a script that must.

**Where the prose came from.** Two read-only sweeps over `internal/mcp` and the
document types (`internal/overrides`, `internal/recipes`, `internal/customep`,
`internal/livestate`, `internal/domain`, `internal/mockplane`) produced the
tool catalogue and the shapes reference; the cookbook, the http reference and
the user guide were written against `README.md`, `CLAUDE.md` and
`scripts/smoke.sh`. Every count in them (55 tools, 14 recipe kinds, 9
`confirmSlug` tools, 5 `editVersion` writes) was read off the code, not
remembered.

## `P6e` — the streaming screens (2026-09-02)

The request: «сделай P6e», after the "where we are" answer named it as the
one slice waiting on a rule rather than on code. That sentence is the
lifting of the A4 rule for this slice, and the slice records it as the
owner's decision (CLAUDE.md, the coverage section) rather than as a new
policy: a future route still ships with no screen.

**What shipped.** `StreamEditor.tsx` (the four behaviours as tasks, the
`when[]` row reused, `draftToDefinition` mirroring the server's refusals,
`StreamCapsStrip` over the preview route); the «Тип» selector and
`EditStreamForm` on `CustomEndpointsPage.tsx`; `StreamTestClient.tsx` (the
browser-side client, EventSource and WebSocket); `StreamConnectionsPage.tsx`
as the eighth tab; four `EXEMPT` entries withdrawn; two contract repairs
(`StreamPreviewRequest.kind` gains `ws`; `EndpointConflictDetails` gains
`kind` and `stream`) and the one Go line behind the second.

**Decisions.**

1. *No wire word in the interface, and a test says so.* §14 and §30.14
   name six words; `StreamEditor.test.tsx` renders the ws editor and asserts
   none of them appears. The sections are named by what the operator wants
   to happen. The cost is a vocabulary the docs have to translate both ways
   (`docs/USER-GUIDE.md` does).
2. *The caps strip shows constants, not effective values.* `STREAM_CAPS` is
   a copy of `internal/customep/stream.go`'s limits — the one deliberate
   duplication — because no route reports them and adding `GET
   /api/config`-style reach for five integers would be a route for a strip.
   The per-workspace cap IS live: the connections tab reads it from
   `GET .../connections`. `maxBytesPerSec` is live too, from the preview.
3. *The test client closes on the first error.* EventSource reconnects on
   its own and says nothing about why it failed; a panel whose job is a
   verdict cannot sit on a silent retry loop. The wording says what the
   browser does not report rather than guessing a cause.
4. *Connections are a tab, not a section.* A list an operator watches when
   the cap bites has a different rhythm from a form being edited; the tab
   polls every 2 s (the registry has no feed, §30.16) and the endpoint
   screen does not.
5. *One server change, and why it is not "server code" in §30.15's sense.*
   A stream row's 409 carried no `stream`, so «Загрузить актуальную версию»
   would have reseeded the editor with the operator's OWN stale draft
   labelled as current. Widening the conflict payload is the screen's
   correctness, not a feature; the contract moved with it.
6. *Two schema drifts, one lesson.* `openapi_contract_test.go` checks routes
   and `csrfToken`, never schemas; the preview enum lagged `P6d` and the
   conflict payload lagged `P6b`, and both surfaced only when a screen was
   typed against the generated client. CLAUDE.md's contract section now
   says so beside the "build input" sentence.

**Not built, recorded in `CARVE-OUTS.md`:** an endpoint filter on the
connections tab (`?endpointId=` exists on the route); a live cap readout on
the endpoint screen; binary frames in the test client (logged as
«[бинарный кадр]», never decoded); SSE events the definition does not
declare (EventSource cannot enumerate them); a log longer than 200 lines.

## `A8` — `import_spec` and YAML (2026-09-02)

The first two items of a ranked list of cheap slices («сделай дешевые»).
No gate workspace: each is a few files over shipped behaviour.

**Decisions.**

1. *The A4 D3 exclusion is reversed, not softened.* `POST /api/specs` was
   kept out of `mcpAllowedRoutes` because the screen "already works and
   stays the only way in" — a policy, unlike the other exclusions
   (`login`, a credential oracle; `DELETE /api/specs/{id}`, a cascade).
   The agent's own workflow is the argument against it: the spec file lives
   in the frontend repository the agent is working in, and every step but
   the first was tool-shaped. `DELETE` stays out on its hazard.
2. *YAML is a conversion, not a parser branch.* `internal/yamlx.ToJSON`
   renders the document to JSON and `internal/openapi` re-decodes it through
   the same `decodeJSON` — one root type, one number handling, one error
   set. The decoder is the tree's second isolated library, admitted on the
   same measurement P6d wrote for the first (one module, no transitive
   modules, no cgo) and held to one importer by a boundary test.
3. *JSON first, then YAML, gated by the first content line.* JSON is valid
   YAML; trying YAML first would route every JSON document through the
   converter. The gate skips blank lines, `#` comments and `---` so a
   generated spec's banner does not send it down the "not a document" path.
4. *A YAML parse error is "not a document", never "unsupported format".*
   `ErrUnsupportedFormat` names a document mocker recognises and declines
   (Swagger 2.0); a file that could not be read is the other thing.
5. *Integer keys become strings; a sequence key is refused.* `responses:
   200:` is the common case and the one JSON cannot represent otherwise;
   a key that is itself a sequence has no honest string form.

## `A9` — the limits, readable (2026-09-02)

The third cheap item. One projection, `config.Limits`, two readers.

1. *A schema, not a route.* The first draft was `GET /api/config` plus a
   tool over it; that is a contract entry, a coverage exemption and an
   allowlist line for a dozen integers that change only with a restart.
   `ServerConfigView` already reaches the panel through login and `/api/me`,
   and `mcp.New` already holds `*config.Config` — so the panel reads
   `limits` off the session and the tool reads the struct directly, its
   `toolRoutes` row empty like `get_guide`'s.
2. *The strip keeps its constants.* Frames per timeline, delay ceiling,
   rule count are the validator's rules, not configuration; only the four
   numbers an operator can change (`MOCKER_MAX_RESPONSE`, the connection
   cap, the lifetime, the inbound frame cap) went live. The P6e carve-out is
   closed as far as it was true.
3. *The fixture lies on purpose.* `serverConfigFixture().limits` carries
   numbers that are NOT the defaults, for the same reason its
   `reservedPrefix` is not `/__mocker`: a strip that hard-coded 4 MiB would
   pass against the defaults.

## `A10` — the «Файлы» tab (2026-09-02)

The fifth cheap item, on the owner's word like P6e. Three existing routes,
one screen, one client fix.

1. *The client fix is the slice.* `customFetch` set the JSON content type
   on every write; the asset upload is a raw-body PUT whose header IS the
   stored media type, so the first draft would have uploaded a JPEG as
   `application/json` and served it back that way. A `Blob` body now keeps
   its own type — the test asserts the header on the wire, not the call.
2. *The name is repaired, then editable.* `assets.ValidName` decides what a
   name may be; the screen pre-empts the 400 by mapping the dropped file's
   name into that alphabet and lets the operator change it, refusing to
   submit outside it.
3. *Delete asks for the slug, typed.* Same shape as declining a resource:
   nothing pre-filled, because a pre-filled confirmation confirms nothing.

**Not built:** item 4 of the same ranked list — upload by URL through the
allowlist. `MOCKER_URL_IMPORT_ALLOWLIST` is parsed and used by nothing; a
fetch through it needs the SSRF discipline `internal/probe` has (scheme
whitelist, no redirects, a body cap, and a resolution-time host check the
allowlist does not yet define), and a new route or a new tool argument
either way. That is a slice with a gate, not a cheap one; the item was
mis-priced in the list and is left for the next ranking.

## `A11` — entity writes (2026-09-02)

«надо сделать 4 точно делай» — item 4 of `IDEAS.md`, the last gap between
what the panel could do and what an agent could: a confirmed family's rows
were readable (A4) but writable only by the mock plane's anonymous POST
(next key, never a chosen one) or wholesale.

1. *By key, not by id.* The routes address a row the way the read does —
   `route_family` and `entity_key` — because neither `resources.id` nor
   `entities.id` survives a decline-then-reconfirm or a restore.
   DESIGN.md:936's CRUD by `:rid` stays unbuilt; `CARVE-OUTS.md` says so
   beside A4's entry.
2. *The key is the identity, and the counter follows it.* `Set` overwrites
   `data[idField]` with the key and raises `resources.seq` to it, the two
   rules that make an operator-placed row indistinguishable from a minted
   one and keep the mock plane's next POST off it.
3. *No validation, no anchor walk, no checkpoint, no confirmSlug* — each for
   a reason already written elsewhere in the tree: the mock plane's POST
   does not validate (R23); an unanchored row is the observable orphan the
   nesting paragraph already accepts; entity rows are `data_snap`'s to
   restore, not `config_snap`'s; one row is what the anonymous DELETE
   already removes.
4. *The SDK reads a `[]byte` as an array.* The tool's declared output types
   its row `data` as `any` and decodes the wire's RawMessage into it; the
   first draft failed the SDK's own output validation with an object where
   it expected "null, array".

## `P4b` — export, import and fork (2026-09-02)

The owner said «давай сделаем 2 + 3 сначала» (a Russian string quoted as
data) over the ranked list in `IDEAS.md`: items 2 (bundle export/import
over HTTP, `P4` part two) and 3 (fork, `P4` part three) before the test
plugin. Built inline, no gate workspace — the design was already argued
in `DESIGN.md` §17 and §19 and the codec already existed; what was left
was transport, one spec-resolution rule and one transaction shape.

**Decisions taken, with the reason each was taken that way.**

- **The code lives in `internal/checkpoints`, not `internal/transfer`.**
  The first sketch was a sibling package calling exported `Capture` and
  `Apply` functions. That would have made the eight private apply steps
  (`writeSettingsTx`, `overrides.ReplaceAllTx`, `upsertResourcesTx`,
  `liveResourceFamiliesTx`, `upsertDecisionsTx`, `restoreEntitiesTx`,
  `customep.ReplaceAllTx`, `bumpRevisionTx`) reachable from a second
  package — and every one of their doc comments exists to forbid a second
  caller taking a different order or a different policy. Two read halves
  were split out instead (`readBundle` from `captureSnapshot`,
  `readDataBundleTx` from `captureEntitiesTx`), and `captureSnapshot` and
  `captureEntitiesTx` are now wrappers over them; the checkpoint tests
  did not change.
- **One transaction per import and per fork, with `workspaces.CreateTx`.**
  `workspaces.Repo.Create` opens its own `db.Write`; calling it and then
  applying the layers in a second write would leave a window where the
  new workspace exists empty and is listable, editable and servable.
  `CreateTx` is `Create`'s body under the caller's transaction — same slug
  derivation, same validation — and the only change `internal/workspaces`
  took besides `CreateInput.ForkedFrom`.
- **The export document embeds the bundle rather than wrapping it.** A
  wrapper (`{bundle: {...}, data: {...}}`) would have made a scenario's
  or a checkpoint's own document NOT importable without an edit. With
  `Bundle` embedded, `data` is one more key beside the v4 fields, and a
  config-only export IS a v4 bundle byte for byte. `entities` stays
  `null`: P3d's decision that rows are a separate document with their own
  version is kept, not reopened.
- **The export's data half refuses over budget instead of degrading.**
  The checkpoint capture writes `data_snap` NULL when the probe says the
  rows are too big, because a workspace's history must stay available. An
  export that did the same would hand a teammate a copy whose confirmed
  families serve empty lists with no word said. `413 export_too_large`,
  and the same call without `includeData` still answers.
- **Spec resolution is `specId` → hash → inline → none.** Explicit wins
  because the caller may be re-binding on purpose; hash before inline
  because an inline copy of a spec already here would re-parse ~350 KB to
  learn it is a duplicate; inline through the SAME `specs.Repo.Import` so
  dedup, the operation cap and the report are the upload's. The inline is
  the bytes AS UPLOADED as one JSON string (never the document re-serialised)
  because the hash is over those bytes and nothing else round-trips to it.
- **A fork copies what an export refuses to carry**, by `INSERT … SELECT`
  inside the same transaction: assets, scenarios (with the active pointer
  re-aimed by name), entity rows joined through `route_family`. §32.4
  keeps assets out of the bundle because a file next to the tests should
  not carry every picture; inside one installation the bytes cost one
  statement, and a "configured copy" whose `bodyRef`s answer
  `asset_missing` is not a copy. Entity rows are copied by SQL rather than
  through a `DataBundle` so the probe budget does not apply — nothing
  leaves SQLite.
- **The source is fenced, not locked, and never written.** The fork reads
  the source on the reader pool and re-checks `(revision, created_at,
  slug)` inside the write with the checkpoint's own `fenceTx`, retried the
  same three times; a source that keeps moving answers `409 conflict`. No
  revision bump and no checkpoint on the source: `P2c`'s "a fork as a
  cheap undo" warrant is gone and nothing about the source changed.
- **Both new workspaces are born with a `manual` checkpoint.** Retention
  prunes machine-made rows only, and the state a workspace was imported
  or copied in is the one thing its operator will want to return to.

**What shipped.** Three routes (contract 66 → 69), three tools (59 → 62),
`EXEMPT` 7 → 10, `autoCheckpointExcludedNeverTouchesLayer` 18 → 20,
`bundle.Export`/`EncodeExport`/`DecodeExport`, `specs.Repo.ByHash`/`Raw`,
`workspaces.CreateTx`, `checkpoints.Repo.Export`/`Import`/`Fork`. No
migration (the `forked_from` column is P0's), no variable, no screen.
Tests: three in `internal/checkpoints` (assets, scenarios and the active
pointer copied, the source untouched, a refused slug writing nothing, the
round trip at the codec level), five in `internal/admin` (every layer and
both optional halves on the wire; import resolving by hash and refusing a
duplicate slug; the four refusals; the inline spec imported once and found
by hash on the second call; the fork with and without data, the source's
revision pinned), three in `internal/mcp`.

**What it does not do**, recorded in `CARVE-OUTS.md`: no screen (A4's
rule), no asset in the bundle, no overwrite-in-place import, no
`migrate-workspaces`.

## `A12` — `@yashok111/mocker-test`, the mock as a fixture a test suite owns (2026-09-02)

«делай 4» (a Russian string quoted as data) over `IDEAS.md`'s renumbered
list: the test-suite plugin. Built inline. The server did not change by
one line — every call the package makes existed since `P1c-2`
(`{prefix}/state`, `{prefix}/health`); what did not exist was a typed
way to make them from a Playwright or Cypress suite, so every frontend
team wrote its own `fetch` wrapper or copied the curl from `http.md`.

**Decisions.**

- **A separate top-level `packages/mocker-test/`, its own yarn 4 install,
  zero runtime dependencies.** Not under `web/`: the SPA is one yarn
  project without workspaces, and a package meant to be published or
  installed from git must not drag the panel's toolchain with it. Same
  TypeScript, vitest, oxlint and oxfmt versions as `web/` so the two
  toolchains stay one; its own `.oxlintrc.json` WITHOUT the react plugin,
  which read Playwright's `use` as a React hook.
- **No `@playwright/test` and no `cypress` import.** The Playwright
  fixture is a factory returning a function literal of the exact shape
  `test.extend` parses (`async ({}, use) => …` — Playwright requires the
  empty destructuring pattern on the first parameter, hence the one
  lint exemption in the tree); the Cypress registration takes `{Cypress,
  cy}` as an argument typed by the two methods it uses. Either framework
  can be absent from the consumer's project and the package still
  typechecks.
- **`reset()` is the only clear, and it clears everything.** The mock
  plane has no per-target delete (that is the admin plane's), so the
  client does not pretend to have one: `pause` is documented as "until
  `reset()`", and the README says "one workspace per runner, or reset in
  `beforeEach`".
- **`fail` defaults to once, `times` is the only option.** The server's
  `once`/`n` pair collapses to one number on the client: `n: times`,
  which the server reads as "fail the next N". `once: true` is `n: 1`
  server-side, so nothing is lost.
- **The tests run against the real binary, in path routing.** One server
  per file on a free loopback port, `MOCKER_ROUTING=path` and
  `MOCKER_ADMIN_HOST=localhost`, because Node's `fetch` cannot set `Host`
  the way curl can and the dispatcher strips the port when matching; the
  suite logs in, imports a three-operation spec, creates a workspace and
  a scenario through the admin API, then observes every verb on the mock
  plane (a forced status, a fail-next counting down, a measured delay, a
  pause released by reset, a scenario switch moving `revision`) plus the
  fixture and the command registration. `make plugin-test` depends on
  `build`.

**What shipped.** `packages/mocker-test/` (client, `playwright.ts`,
`cypress.ts`, 15 tests, README), two Makefile targets (`plugin-test`,
`plugin-build`), `.gitignore` rules for `packages/*/{node_modules,dist}`,
docs (README section, USER-GUIDE §5, cookbook recipe 7, http.md). No Go
change, no route, no tool, no migration, no variable.

## `A13` — per-target clear on the session layer (2026-09-02)

The owner, reading `A12`'s report: «на mock-плоскости нет удаления по
target - может сделаем?» (a Russian string quoted as data). It was true of
BOTH planes — `livestate.Store` had `Clear` and nothing narrower, and the
admin's `DELETE .../session` cleared everything too; `shapes.md`'s "DELETE
on the same route" had described a per-target clear that did not exist.

One store method (`Delete`, keyed on `(target, action)` exactly as `Set`
is, `action == ""` meaning every action; the same broadcast wakes the
parked requests, and the deleted pause's own `Recheck` is what releases
only its own), an optional body on the two existing DELETEs (the body,
when present, MUST name a target — a typo must not clear a sibling test's
directives), `clear: true` on `set_session_directive` (a DELETE followed
by a GET so the tool still answers the list that remains), and
`mock.clear(target, action?)` plus `cy.mockerClear` in the plugin. Tests
on all four layers; the plugin's suite observes a pause on `GET /cart`
released while a status on `GET /orders` survives. No route, no tool, no
migration, no variable.

## `A14` — `MOCKER_STREAM_TRAFFIC_FRAMES=all` (2026-09-02)

«сделай 3» (a Russian string quoted as data) over `IDEAS.md`. §30.13 had
priced `all` on one condition — "its own retention budget so frames cannot
evict ordinary rows" — and `P6b` refused the value by name until that
budget existed.

**The budget is per row, not more rows.** The eviction §30.13 feared
comes from ROW retention (a thousand per workspace): a socket at ten
frames a second as one row per frame rolls the history in a hundred
seconds. `P6b` already made a connection ONE row; `all` keeps that and
bounds what the row holds instead — `MOCKER_STREAM_TRAFFIC_MAX_FRAMES`
(200) and `MOCKER_STREAM_TRAFFIC_MAX_BYTES` (`64kb`), each way. The
eviction cannot happen by construction, and a long socket cannot grow a
row past the byte cap.

**One type for three modes.** `frameLog` in `internal/mockplane/traffic.go`
replaced the two ad-hoc `streamFirst`/`streamFirstIn` byte slices: nil
under `off`, one frame cut at `MOCKER_TRAFFIC_MAX_BODY` under `first`
(byte for byte the P6b/P6d behaviour, and a later frame is not a
truncation — `first` promised one frame), whole frames under `all` with
the first frame that does not fit marking the log truncated and closing
it. Cutting a frame in `all` was rejected: half a JSON object in an NDJSON
body is worse than a flag. Both loops hand their logs to the request's
`trafficMatch` right after `markStream`; the notes gain
`frames_recorded:N` and `frames_in_recorded:M`; the row's `truncated`
gains a second meaning.

**Inbound stays under §30.13's redaction rule.** Only a text frame holding
a JSON object is ever stored, each one redacted by field name, one per
line (`application/x-ndjson`); binary and plain-text frames are counted,
never stored. The `first_in:binary|text` note keeps describing the FIRST
inbound frame whatever the mode.

Two variables (34 → 36), `config.Limits` two fields wider, the contract's
enum wider by one; no route, no tool, no migration, no screen. Tests: the
config's acceptance and its two refusals, an SSE row under three budgets
(none, two frames, one kilobyte) and a WebSocket echo row with a secret
in the first inbound frame redacted on the way into the log.

## `A15` — the audit: splits, a process-killing race, and the generator's cost model (2026-09-03)

The owner asked for a refactor of a code base "written in a hurry", then
widened it: "monoliths are not the worst of it — look for performance
bottlenecks, bugs and the rest". Two halves, in that order.

**The splits are pure moves.** A throwaway AST tool cut top-level
declarations out of the five largest files byte for byte (doc comments and
section banners travelling with the first declaration below them) into
files named by responsibility; `gopls imports` pruned the import blocks;
nothing else changed and the package tests ran green after each. The
largest file went from 2081 lines to 464.

**Five reviewers, one zone each, one brief.** Hot path, concurrency and
lifecycle, SQL and transactions, admin/MCP/config, generator and spec
pipeline — each told to verify by reading the code path, to check
`CARVE-OUTS.md` before calling a decision a bug, and to report only what it
could cite. Sixty-odd findings came back; every one was re-read against
the code before a line changed, and each fix that changed behaviour got a
test that fails without it (two were mutation-checked by reverting the
fix: the generator test collapses every row to one id, the stream test
hangs).

**What was real.** The one critical finding was the generator handing a
schema-level `example` (and const/default/enum) to its walkers by
reference — every list row the same document map, and two concurrent
anonymous requests writing it together, a fatal runtime error. The
security-relevant ones: a login limiter keyed on an attacker-chosen name,
a stored HTTP status `WriteHeader` panics on, an import door with no
executable-media-type guard, an entity `PUT` whose key and id could
disagree, a `basePathValues` fan-out that generated bodies before the row
cap looked, headers stored uncapped from an unauthenticated plane. The
plain bugs: deleting a fork's source was refused by the schema the
carve-out said would dangle, an `A11` key colliding across base scopes was
a 500, the admin feed's 900 s lifetime was lost under steady traffic, one
client aborting a cold runtime build failed every waiting client. And a
long tail of config values accepted and never workable, two non-strict
decoders, a YAML converter that rounded `1.0` to `1`.

**What was measured.** `BenchmarkBody_fullCorpus` did not exist; it does
now, and the sizer that replaced a reflective `Marshal` per scalar took the
corpus from 1.34 s to 0.85 s and 3.9 M to 1.6 M allocations, with the
419-body golden unchanged. `EXPLAIN QUERY PLAN` over the real schema showed
`DELETE FROM resources` scanning three tables — migration `0007` indexes
the seven foreign-key columns that had none.

**What was refused or deferred, with the reason in `CLAUDE.md`'s `A15`
paragraph:** `+xml` as browser-executable (breaks XML mocking; the owner's
call), the probe's residual port choice, keying the allOf merge cache by
map identity (address reuse after GC would alias two schemas), the
per-`POST` `COUNT/SUM` over a family, the double request-body decode, the
nested-confirm per-tuple queries, and the batch savepoints — each a
measured cost with a design behind it, not a hole.

No route, no tool, no variable, no screen; one migration; the contract
stays at 69 and the `nolint` count at 29.

## `A16` — `mocker setup`, the install wizard (2026-09-03)

The owner, having walked the HTTPS path by hand on his work machine the
same morning (bundle, clone, `make init`, `MOCKER_ROUTING=path`, `make
up-tls`, `make tls-root`, the root into the trust store, the hosts line),
asked for it as one command his colleagues could run without knowing any
of it. Three answers fixed the shape: every colleague runs their own
mocker (no shared instance yet), on Linux, macOS and Windows alike, with
the image built from the checkout — so the wizard is a subcommand of the
one binary, cross-compiled by `make dist`, and the colleague receives a
checkout plus the right binary.

**Bash became Go.** `scripts/init-env.sh` and `scripts/compose-tls.sh`
are the two scripts the path needs, and neither runs on a colleague's
Windows; `setup_env.go` and `setup_compose.go` restate them rule for
rule (the last-assignment-wins env read, the bare-host check, the /24
arithmetic, `COMPOSE_ENV_FILES` at the null device) with a test pinning
each, and the hash is minted in-process through `auth.HashPassword`
instead of a `docker run`. The per-OS steps — the hosts line and the
trust store — are PLANS (`trustPlan`, `hostsCommand`), argv lists a test
can check for every OS from this one, and a failure to run one prints
the manual line rather than failing the install.

**The live run paid for two builds nobody had run.** The first end-to-end
run on this box failed inside `docker compose up --build` at `yarn build`
with a TypeScript error: `web/src/test/fixtures.ts` never gained A14's
two `limits` fields, invisible locally because the gitignored generated
client was stale the other way. The second run failed at the same step
with a swallowed rolldown error: `GuidePage.tsx` imports
`docs/USER-GUIDE.md?raw` since A7, and the Dockerfile copied only `web/`
and `api/` while `.dockerignore` excluded every `*.md`. `make up` had
therefore been red since 2026-09-02 for anyone starting from a clean
clone — and `make smoke`, the one bar that builds the image, had not
been run since. Both fixed; the third run went through: image built,
Caddy up, root exported, `/readyz` green through the CA, `status` and
`down` clean.

No route, no tool, no variable, no migration; `nolint` 29 → 32 (the
wizard's exec and its `.env` write, each with its reason in the line).

## `P7a` — API design on top of a workspace, the server and the agent (2026-09-03)

The owner's brief the same morning («у нас в команде очень часто фронты и
системные аналитики занимаются проектированием апи … нужен ui для
просмотра что получается и mcp инструмент чтобы отдавать указания для
агента. также проектировщик должен уметь брать за основу схему уже
существующего апи», a Russian string quoted as data) went through
`IDEAS.md` (priced against the code, four items), then into DESIGN v12
as §34 by the owner's hand — the third version to add intent before code
— and then into ONE gate, `mocker-p7-api-design`, for
TWO slices: `P7a` (server and agent) and `P7b` (the screen). The
interview settled the calls §34.6 had left open: one JSON column for the
operation fields, `schema` on the shared `Variant` type refused by name on
an override, `$ref` into the base allowed and "never stored dangling"
enforced at every writer that could break it, the export as a fixed point
of `openapi.Load`, bundle v5 READING v4, `x-websocket` for a WebSocket
row, the accept step as three existing verbs and never an `accept_design`
tool. The gate's round 1 was opened with three lenses (criterion, seams,
external); the build ran inline from the applied decisions document and
this session finished it.

**The one place the build departed from the document.** D13 said the
export route "joins `autoCheckpointExcludedNeverTouchesLayer` (20 → 21)";
that map holds MUTATING routes only — `TestAutoCheckpointLabels_pinsEveryMutatingRoute`
derives its population from `routes()` by method — and a `GET` has no
place in it. The count stayed at 20 and the document is corrected here
rather than in the gate.

**What the tests caught that the smoke did not.** `resolverForSpec`
answers a nil `*openapi.Resolver` for "no spec is bound", and the first
draft passed it straight into `customep.ValidateRefs`'s `RefResolver`
interface — a typed nil, not a nil interface — so the first `$ref` on a
no-spec workspace dereferenced it and the handler panicked. The smoke's
no-spec observation used a schema without a `$ref` and never saw it;
`TestDesign_refRefusedAtWrite`'s last clause did. `refResolverOf` is the
guard, with the reason in its comment.

**What the reviews caught.** One Codex CLI reader and three Opus readers
(serving path; admin, SQL and bundle; composer, round trip and docs) over
the whole diff, after the bars were green. The serving reader found that
a ROOT-level `$ref` (`schema: {"$ref": "#/components/schemas/User"}` —
§34.3's own headline) generated a random string: `gen.Body` takes an
inline root verbatim and only chases nested references, so
`chaseRootRef` now resolves the root into a private deep copy at build;
and that an unresolvable node emptied to `{}` was an UNTYPED schema the
generator turns into a string, not the empty object D6 promises — it
carries `type: object` now, and the test that had only checked the key's
presence asserts the empty object. The admin reader found the contract
silent on the new 409s of three existing routes, `export_openapi`
answering an object where D7 says the string `import_spec` takes, the
screen's PUT clearing `reqSchema`/`operation` on every edit (a full
replacement the form did not know about — the form passes the row's own
back now), and a stream row created through the tool dropping its
`operation`. The composer reader found the base decoded without
`UseNumber` (a `1.0` in `components` would export as `1`, breaking A11's
byte-equality), the export's media-type pick diverging from
`specs.SelectMediaType`, the schema root's `examples` kept after a patch
where the runtime drops it, and — the one that mattered most — the smoke
block reading `$TMPDIR_SMOKE`, a variable defined nowhere, under `set
-u`: the P7a observations had never run. The first full `make smoke`
then died before reaching them for an older reason: the MCP loopback
built its bodiless requests with a nil `Body`, and A13's DELETE
`.../session` reads `r.Body` unguarded, so `set_session_directive {clear:
true}` had been killing the process since A13 — `http.NoBody` now, with
a test. The same run showed the smoke's tools/list pin still at 54, and
the next one that P6b's session-layer observation had been red since A13
for the same reason as the crash: its clear-all sent `{}`, which A13
refuses by design ("a body naming no target is refused, never read as
everything"), so the forced 503 stayed and the delayed timeline answered
no frames — the block sends no body now.
The Codex CLI reader (`gpt-5.6-luna`) did not converge at `xhigh` — 25
minutes, 1.7 MB of trace, still reading `internal/gen` when the timeout
killed it — and answered in 12 at `high` with a reading budget in the
prompt: six MAJORs, four of them the windows this slice had already
recorded in `CARVE-OUTS.md` (the two `$ref`-check races, the operationId
collision a rebind can produce, the `examples` array shape) and two
taken — a bundle's override entry could smuggle a `schema` past the
route's `schema_on_override` refusal (`validateOverrideEntry` refuses it
now, with a test), and a pinned body that is not JSON was dropped from
the export instead of written as the text example D7.2 names.

**Shape of the slice.** `internal/design` (a leaf: `Skeleton`, `Compose`),
`internal/customep/operation.go` (`Operation`, `validateOperation`,
`ValidateRefs`, `operationIDHolderTx`), `internal/mockplane/custom_schema.go`
(`buildCustomInline`, `sanitizeRefs`) and `serveCustomGenerated`,
`internal/admin/design_handlers.go` (the export route, the three refusal
helpers), `internal/mcp/tools_design.go` (`export_openapi`, tool 63),
migration `0008`, bundle `CurrentVersion` 5 with `minVersion` 4,
`skills/mocker/references/design.md` embedded as the sixth guide topic,
USER-GUIDE §7a («Проектирование API»), eight P7a observations in
`scripts/smoke.sh` (A5, A6, A7, A9, A11, A12's full D8 round trip, A13).
Contract 69 → 70, tools 62 → 63, `EXEMPT` 10 → 11, migrations 7 → 8,
goleak packages 34 → 35, `nolint` 32 → 34 (two `nilnil`), variables 36.
No screen: `P7b` is next, in the same gate document (D12).

## `P7b` — the «Контракт» tab (2026-09-03)

The second slice of the `mocker-p7-api-design` gate document (D12), built
the same day as `P7a` on the owner's «сделай p7b» — the A4 rule lifted for
this one screen by his «снимаю ограничения для 3 пункта» (both Russian
strings quoted as data). The owner had chosen the renderer (a hand-rolled
Mantine tree, no `swagger-ui-react`), the badges (base / added / changed /
removed, client-side), the links (into the existing editors, never an
editor of its own) and the tree's `$ref` rule (the component name
collapsed, expanded on click) in the interview; the build followed them.

**One server change, found by the first line of the badge rule.** D12's
«изменено» predicate for a spec operation is "an override with a
`schemaPatch` or a pinned response", and the list view the tab reads
(`GET .../operations`) summarised a status as `{mode, recipeCount}` — the
patch's presence was behind `GET .../operations/{opKey}` only, one request
per operation. `opOverrideResponseSummary` gained `hasSchemaPatch` (a
schema change on an existing route, no contract count moved), the same
shape `P6e` widened a struct in.

**Two things the linter and the tests decided.** The operations editor
takes `?opKey=` and must select that operation once the list loads; the
obvious `useEffect` over the derived list is what oxlint's
`exhaustive-deps` refuses (a per-render array), so the effect keys on the
query's own data and a `useRef` guard makes it run once. And B5's "the
download re-fetches nothing" is asserted by counting `fetch` calls to the
export route across the test, not by trusting the handler.

**Shape.** `web/src/components/{ContractPage.tsx, SchemaTree.tsx,
contractBadges.ts}` and their tests, the tenth route file, `validateSearch`
on the operations and endpoints routes with `initialOpKey` /
`initialEditingId` threaded into the pages, `WorkspaceLayout.tsx` +1 tab,
`coverage.test.ts`'s `EXEMPT` 11 → 10 with the withdrawal in the map's own
comment, USER-GUIDE §7a rewritten for the screen, `scripts/smoke.sh`'s
path-mode block checking the deep link reloads (B7). `make ui-test`
366 → 378. No route, no tool, no migration, no variable.

## `A17` and after: the gate document moved INTO the repository

Every slice from `A1` to `P7b` was designed in a gate workspace outside this
tree — seventeen of them — and each held the decisions document that was the
authority on WHY that slice is the way it is. They survive only while those
directories do, which is why anything that had to outlive them was copied into
`CLAUDE.md` or here. `A18` is the first slice whose gate document is COMMITTED:
`docs/A18-endpoint-functions.md`, 1250 lines, D1–D10 plus D8b, a §A acceptance
section of 59 numbered clauses, the fourteen `[GIVES-UP]` items and the gate's
own four-round record. The entry below is the narrative; that file is the
authority, and it is in `git log` rather than in a directory somebody may
delete.

## `A18` — endpoint functions (Lua) (2026-09-04 / 05)

**What the owner asked for, and what he gave up to get it.** The ask was
logic on an endpoint — «проверь пароль и разветвись», «выдай токен на час».
The objection is obvious and was written down before anything else: an
unauthenticated mock plane executing operator-supplied code that any
anonymous caller can trigger. The owner overruled it in his own words —
«хочу такую фичу для локального инструмента на все эти угрозы пофигу» — and
the document records it as ACCEPTED, not argued away. Four more of his
answers fixed the shape: gopher-lua because «это все равно будет писать агент
через mcp так что без разницы на каком языке. пусть lua будет чтобы быстро
было»; «Всегда вкл» (no flag); «Полный набор (Рекомендую)» for the helpers;
«добвляй 1 и 2 в гейт» for the two stream hooks. All Russian strings, quoted
as data.

**The gate ran four rounds and the blockers did not descend.** 34 findings /
3 blockers, then 17/1, then 14/2, then 10/3 — 75 findings and 9 blockers
total. The findings fell by two thirds; the blockers did not fall at all, and
that is the interesting number. Round 2's single blocker was FIX-INDUCED
(`fix-induced 9 (1 of 1 blocker)`): the round-1 fix introduced it. Rounds 3
and 4 exposed the shape behind that: **a fix closed the INSTANCE it was
handed and not the CLASS.** The `[GIVES-UP]` count moved 6 → 8 → 9 → 12
across the four rounds, each round finding items the previous round's fix had
walked past; and the v4-enumeration paragraph was short TWICE for two
different reasons — first a correct command over a GUESSED SCOPE
(`internal/bundle/*_test.go`, with `NR` where a multi-file glob needs `FNR`),
then a widened scope with a PATTERN that reached one of six shapes. Three
consecutive rounds on one artifact section is the signal the gate's own
manual names: the guard was asking the wrong KIND of question. The fix was to
WITHDRAW the file:line checklist and keep the SHAPE list — a list of sites in
a document read before the code is written goes stale on the first edit, and
this one went stale twice inside the gate itself.

**The gate found six live defects that had nothing to do with A18.**
`api/openapi.json`'s `"const": 4` on `WorkspaceExportDocument.mockerBundle`
plus three descriptions saying "mockerBundle v4", and
`internal/mcp/tools_transfer.go`'s two tool descriptions and one `jsonschema`
tag — all stale since P7a moved `CurrentVersion` to 5, and seen by no bar:
the contract test reads routes and `csrfToken` and never a schema, there is
no runtime schema validator, and nothing asserts on an MCP description
string. The slice repaired them on the way past.

**Reading the library's source found five divergences from the document's own
D3**, each caught before a line of code: `getfenv`/`setfenv` were unnamed in
the removal list; `os.setenv` (a process-environment WRITER) was unnamed;
`rawlen` does not exist in gopher-lua (it is a Lua 5.2 name); `string.dump`
is REGISTERED and raises rather than being absent, so the acceptance clause
as written would have gone red against correct code; and `rand.Seed` is a
no-op on Go 1.27, which makes "seed the RNG per VM" unimplementable as first
drafted. The last two are the useful pair — one would have failed a correct
implementation, the other would have shipped a guarantee that does nothing.

**Built in three commits, inline, no fleet.** A18-1 was the sandboxed runtime
behind one importing package; A18-2 the `Function` field with its refusals
and bundle v6, then the serving branch; A18-3 the two stream hooks and the
contract. The build found TWO more `[GIVES-UP]` items the four gate rounds
had not — 12 → 14 — and both are the same shape as the rounds' own misses: a
promise stated in the document with no surface behind it. D7's
"Custom-endpoint preview: same" names a preview that does not exist (the
route refuses `kind: "http"` and answers a type with no `Notes` field, so
clause 32's own type reference points at the operation preview only), and D3
names `resources.Repo.ListFiltered` where `EntityStore.List` is what the call
actually wants (a full ancestor tuple is an exact key; `ListFiltered`'s
wildcards, cursor and limit are the three things `mock.entities` does not
need, and reaching for it would widen a four-method seam for nothing).

**Three numbers the document deferred to the slice, measured in-tree.** The
module graph is 56, up 4 — but `go.mod` gained exactly ONE line and
`go list -deps ./cmd/mocker | grep -c chzyer` is 0, so §30.9's "zero
transitive modules" holds for what is LINKED and not for the graph
`go list -m all` walks; that divergence is written down rather than glossed.
The binary grows **+1 007 664 bytes (+0.96 MiB)**, measured
`a6ae6ee` → `d8561ad` in a git WORKTREE both times — the worktree is
load-bearing and cost a wrong number first: `internal/webui/dist` is
gitignored, so a worktree checkout embeds no SPA and builds about 1.4 MB
smaller, which makes the baseline table's main-tree figure unsubtractable
from any later worktree one. The `-race` suite shows no regression, which is
what an interpreter over its own opcode array should do: unlike modernc
sqlite it gives the race runtime no C-translated code to instrument.

**The per-firing benchmark D10.1 makes the pooling decision rest on**:
`BenchmarkLuaTickFiring` — `142127 ns/op, 185351 B/op, 612 allocs/op` for
`lua.NewState` + the sandbox open + the mock table + one call. Against the
100 ms tick floor that is 0.14% of one connection's budget at 10 Hz, and
about 28% of one core with all 200 connections at the floor. Recorded, NOT
thresholded: D10.1 says the owner reads it and decides, and a slice that
pooled VMs on its own reading would have changed D3's statelessness
guarantee with nobody saying so. Fresh VMs stay.

**Two orderings that no amount of following D5/D10 would have produced**, and
they are their own section (D8b) because of it. `internal/customep`'s tick
validator required `schema` UNCONDITIONALLY, so a Lua-only tick was refused
as schema-missing and a `lua`+`schema` tick never met its own refusal — the
acceptance clause for it would have gone red against an implementation
nobody could call wrong. And `onFrame` had no site at all: its kind check and
its two conflicts each needed a place AND an order. A third, found the same
way, is `function_on_stream`: both stream arms refuse a non-empty `Responses`
map before anything looks inside a variant, and a `function` lives inside
that map, so D5's own refusal could never have been the answer.

**Shape.** `internal/luafn` (sandbox, runner, hooks, converters, the boundary
test, a frozen `_G` allowlist), `internal/mockplane/function.go` and its two
test files, `hooks.go`, `hooks_test.go`, `hooks_internal_test.go`,
`internal/customep`'s two new document fields and `validateInbound`, bundle
v6 with `minVersion = 5` and `TestDecode_readsV4` INVERTED rather than
renumbered, four traffic tokens plus `on_frame_errors:K`, the contract's five
new fields, `skills/mocker/references/functions.md` as a seventh `get_guide`
topic, the two serving matrices in that file and in `docs/USER-GUIDE.md` §4,
fourteen `CARVE-OUTS.md` entries, and eight smoke observations against the
image. `//nolint` 36 → 39. No route, no tool, no migration, no variable, no
screen; contract 70, tools 63, `EXEMPT` 10.

**The review after the gate (2026-09-05).** Three readers over the shipped
range — the session's own pass, `/code-review` at high effort and `vcodex`
(`gpt-5.6-luna`, `xhigh`) — found fourteen things four gate rounds had not,
five of which mattered: a self-referencing Lua table crashed the PROCESS
(`fatal error: stack overflow` is outside every recover), an untyped string
body was sniffed to `text/html` on the unauthenticated plane while the test
stayed green because `httptest.ResponseRecorder` does not sniff after an
explicit `WriteHeader`, a stored Lua tick could not be re-validated
(`"schema":null` read as a second producer) so no checkpoint holding one
could be restored, `create_endpoint` dropped `function` on the wire and every
MCP read hid it so the guide's own read-then-write flow deleted it, and a v4
scenario activated with 200 while the mock ignored it. The rest: null in an
array shifted the elements, a failing tick 500'd the preview, a long close
reason became a 1006, stream hooks could not scope a nested family, the seven
refusal codes the documents promised were never emitted, a dead
`ValidateHook`, no cap on the header SET, a declared type lost on an empty
body. Six `fix(a18)` commits, one group each, a test per finding; the
decisions the code cannot show are in `docs/agent/functions.md`. Two lessons
paid for: a gate reads the DOCUMENT against the code and misses what neither
says (nobody wrote "the converter recurses"), and a recorder is not a server
— a test of anything that reaches the wire runs `httptest.NewServer`.

## `A19` — `mock.generate` and the entity writers (2026-09-05)

**What the owner asked for.** After the A18 review he asked what a function
could reach («какие у агента есть возможности? … api для доступа например к
базе?») and, given a ranked assessment — a body generated from the spec's
own schema first, entity writes second, KV state and outgoing HTTP refused —
took the first two in his own words («давай сделаем 1 и 2»). Two design
questions settled the shape: `generate` takes a `#/` pointer into the bound
spec OR an inline table, and the writers hang off `mock.entities` as a
callable table so the A18 spelling keeps reading and `mock` gains one key,
not three.

**What was built.** `luafn`: `Host` grows `Generate`, `EntityCreate`,
`EntityUpdate`, `EntityDelete`; `mock.entities` becomes a table with
`__call`; argument refusals by name (`bad_schema`, `bad_data`, `bad_key`);
a numeric key is its decimal text. `mockplane`: `luaHost` carries a
`gen.Request` seed tuple and the runtime keeps its `resolver`; `Generate` is
the tree's third named `gen.Body` site and chases/checks `$ref`s itself,
refusing an unresolvable one by pointer; the writers go through the same
store and caps as the plane's POST/DELETE, update is Get → shallow merge →
`Repo.Set`, and `EntityStore` gained `Set`. Nothing on the contract, the
tools, the schema or the routes; the guide's §2 and its skills copy carry
the four helpers; five `CARVE-OUTS.md` entries. Tests: the Lua argument
contract through a recording host, the host half through the store fake,
`$ref`/inline/unresolved end to end through a real generator, a create
through a function endpoint under the URL's own scope, the seam test's
third name, and the two key pins.

**The one thing found by building.** `gen.Body` takes `PatchedSchema` as the
root verbatim — a root `{"$ref": …}` is chased by `buildCustomInline` once
at build for a stored schema, and the first end-to-end test returned a
STRING for `mock.generate("#/components/schemas/Thing")`. The host now
resolves the root and checks nested refs per call, and unlike the stored
path (which must keep serving and empties a dead node with a warning) it
refuses by pointer: a function asked a question and gets the answer.

---
name: mocker-backend-explain
description: Build or review human-readable backend scenarios and architecture from repository evidence or an existing Mocker import. Use when importing a system for human understanding, replacing code-shaped diagrams, or explaining behavior to readers unfamiliar with the code. Raw source capture and wire operations still use their negotiated Mocker workflows.
---

# Explain backend behavior

Produce an explanation a person can understand without reading the implementation.
A diagram of functions, assignments and logging calls does not meet this goal,
even if all source evidence is correct. Preserve the source layer for verification.

Read [the semantic modeling contract](references/interactions.md#human-readable-modeling-contract)
before extracting scenarios or accepting a generated diagram. It contains the
abstraction rules, evidence requirements, acceptance rubric and handoff template.
The preceding reference sections describe the existing interactions format.

## Workflow

1. Establish audience, requested scope and available evidence. Default to a
   colleague who knows the product but not its code; use Russian display text in
   Mocker unless the user asks otherwise. Continue with known context rather than
   requiring a questionnaire. Record unresolved product vocabulary as a question.
2. Pin the source revision and inventory user goals, triggers, actors, outcomes,
   consequential decisions, changed data and external interactions. Reuse a
   suitable existing import; do not reimport merely to rewrite the explanation.
3. Draft one representative scenario using the contract's template. Group by
   meaningful actions and outcomes, not functions or files. Check the explanation
   against the evidence before repeating the approach across the requested scope.
4. Build overview → capability → scenario → optional implementation drill-down.
   Keep a shared glossary and a many-to-many map between semantic steps and exact
   evidence. Summarize uncertainty where it affects interpretation.
5. Apply every acceptance criterion in the reference. A short but unsupported
   story fails just as a faithful line-by-line graph does. Fix the pilot, then
   process the remaining scenarios and report what was covered or remains unknown.
6. For authorized publication, select supported diagram types and negotiate their
   actual server owners. Preserve source claims and old pins; read back the saved
   result and check its rendered, ordinary entry path. Do not claim a new default
   navigation path until it has actually been wired and verified.

## Protocol and scope

This is an editorial skill, not a new advertised wire workflow. Before MCP use,
call `get_server_config` and `get_backend_capabilities`, select a complete supported
owner and immutable guide set, and read its entrypoint plus needed topics with
`get_guide`. Use `describe_tool` before constructing unfamiliar payloads. The
bundled reference is a portable editorial contract; it is not evidence that the
server supports its APIs. Existing owners cover source import/sync, architecture,
interactions and business maps. Follow their negotiated CAS, evidence, limits,
audit and recovery rules; do not invent a human-scenario endpoint or payload.

A raw-import-only request may stop after accurate capture. An understanding task
must additionally deliver the semantic model or clearly report why it remains
incomplete. Instruction-writing alone does not authorize live imports or diagram
writes. Reworking explanations does not authorize deleting source steps, rewriting
historical evidence, replacing pins in existing saved views, or running the
inspected application.

If the installed/server procedure predates the semantic contract, keep the user's
readability requirements as editorial acceptance criteria while using the selected
wire procedure unchanged. If no supported diagram can faithfully express the
scenario, deliver its local outline/evidence map and state the specific format or
navigation gap; never disguise the raw Flow as a finished human scenario.

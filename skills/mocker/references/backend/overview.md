---
name: mocker-backend-project
description: Prepare a backend project and inspect its immutable initial revision.
metadata:
  workflowId: "mocker-backend-project"
  workflowVersion: "2"
  requiredModelSchemaVersions: "[\"1\"]"
  requiredCapabilities: "[\"backend-projects\",\"backend-project-metadata\",\"backend-revisions\",\"backend-annotations\"]"
  guideSetId: "sha256:5a8d1fe6bc274c7b60621c826a5fcca31e4ab6b1c6e8f885a40d90abfbbeebdd"
  manifestHash: "sha256:5a8d1fe6bc274c7b60621c826a5fcca31e4ab6b1c6e8f885a40d90abfbbeebdd"
---

# Backend project preparation


This workflow prepares a backend project: create a project, read or update its
metadata, list projects and revisions, and inspect the empty initial revision.
Creation saves project version 1 and its first immutable revision atomically.
Coverage is partial, its denominator is unknown, and the explicit pre-import
gap means an empty revision cannot establish absence of backend behavior.
Source snapshot import/reimport and pinned comparison use the independently
installable `mocker-backend-import` leaf, the root package's generated
`references/backend/import.md` compatibility copy, or pinned `backend-import` topic.
Select that complete workflow before import writes. This project-preparation
procedure does not import sources. Import's backend-model/import-protocol/recovery/
examples topics use its selected set and import owner identity; relational
imports additionally load the database-reference topic through the supported
database v7 owner in that same global set. Database inspection selects the
independent database leaf or pinned backend-database entrypoint. Every shared
topic is verified against its actual canonical owner manifest/tuple/contentHash;
this backend-overview topic keeps the project-preparation identity. Project
preparation still requires schema1, including its empty initial revision; it
need not require schema2 to prepare a project. Database typed edits/proposals
use the separately selected database7 workflow. Source flow/data-access questions
select inspect7 at backend-inspect in this same set; ordinary inspection writes
nothing. Source4 field lineage selects inspect7 with field-lineage-v1 and backend-field-lineage-query.
Manual API links require inspect7, backend-api-artifact-pins and api-artifact-pins-v1;
load its flow-reference contract before any authorized mutation. Backend impact,
measured writer checks and job execution remain unavailable.

## Before the first write

Installed skill frontmatter stores workflow fields under `metadata` as strings.
Decode requiredModelSchemaVersions and requiredCapabilities from their JSON-list
strings before comparing them with the server manifest's typed arrays.

1. Classify the request as backend project preparation. Read `get_server_config`
   for general limits and call `get_backend_capabilities` for `modelSchemaVersions`,
   `features`, `workflowVersions` and the advertised guide topics. Tool names
   alone never establish compatibility.
2. Select `mocker-backend-project`, workflow version `"2"`. A local leaf is
   usable only on an exact match of workflowId, workflowVersion, guideSetId and
   manifestHash with a server workflow, support for every
   requiredModelSchemaVersions entry in modelSchemaVersions, and availability in features of
   every requiredCapabilities entry: backend-projects, backend-project-metadata,
   backend-revisions and backend-annotations. Verify each needed local topic's contentHash against that
   selected manifest before using it.
3. A newer or older local workflow, missing local metadata, a mismatching hash,
   or an unavailable local reference requires a full server fallback. Choose a
   server workflow whose schema versions and capabilities this agent supports;
   read its advertised entrypoint using
   `get_guide {topic:selectedWorkflow.entrypoint,guideSetId:selectedWorkflow.guideSetId}`.
   Use its entire procedure and recovery instructions. Read related topics with
   the same selected guideSetId and verify workflow identity, manifestHash and
   each returned contentHash. Do not mix an incompatible local leaf with server
   instructions. No skill installation is needed for this fallback.
4. Record workflowId, workflowVersion, guideSetId, manifestHash and instruction
   source (`local` or `server`) for the task. Unknown guide sets fail explicitly;
   do not silently substitute the latest set. A versionless overview is useful
   for discovery only; subsequent backend topic reads must be pinned.
5. If no compatible server workflow exists, explain the limitation and remain
   read-only. Continue independent supported reads, without asking permission
   to bypass compatibility. Recheck the selection after resume or a server
   change before any further write. Preserve all previously returned IDs and
   read current project/revision state before deciding what to do next.

## Project2 annotations and source boundaries

Project metadata batches also create/update/remove source-object annotations. Select backend-annotations from this project2 owner before those writes; it defines exact plain-text replacement, current/historical/orphan targets, project CAS and cursor409 recovery. Metadata updates keep source revision bytes unchanged. Project2 still requires the empty schema1 creation branch; it does not implicitly import source6.

Select sync1 for source6 scopes/provider migration/incremental reconciliation, change1 for full desired source5/source6 proposals, and inspect7/database7 for exact reads and SavedView-v2. Old relational proposal procedures remain under database7 with their legacy proposal tag; full proposals use changeProposal explicitly.

## Available tools and ordered procedure

- `list_backend_projects`: discover existing projects before creation.
- `get_backend_project`: read the chosen project's metadata and exact integer
  version. Preserve int64 version values without floating-point conversion.
- `create_backend_project`: submit `{name,idempotencyKey}` with a stable unique key,
  retain its UUID project ID and returned revision ID, then read back the project.
  Repositories remain empty at project creation; source repositories are registered by the separately selected import/sync protocol.
- `apply_backend_project_commands`: read first, then rename with
  `{projectId,expectedVersion,idempotencyKey,commands:[{type:"rename_project",name}]}`.
  Use the exact int64 version from that read and a unique command key.
  Do not guess a version or use a revision ID as a project ID.
- `list_backend_revisions` and `get_backend_revision`: inspect the project's
  immutable revisions and their coverage gaps. Retain provenance and partial
  coverage caveats in any answer; an empty revision is preparation only.

For all writes, use the exact input shape advertised by the server tools.
On a version conflict, read the error's currentVersion and re-read the project,
reconcile the user's intended rename with the current name, then retry with the
fresh expectedVersion and a new idempotencyKey. Stop if the concurrent change
contradicts the user's intent. The initial revision
is immutable and a metadata update does not create a new revision.

Reads can be retried safely. Creation and command writes save idempotency
receipts. After a timeout or disconnected response, retry the identical request
with the identical idempotencyKey to recover the original result. Preserve the
original key and payload after resume; never substitute a fresh creation key
because that can create a duplicate project. Reusing a key with changed inputs
is an idempotency conflict; resolve the existing receipt/request first. For a
known compare-and-swap conflict, re-read and reconcile, then issue the newly
formed command with a new key. A receipt replay returns the original result,
which may be older than current state; read the project again for its live state.

Saved editor projections use inspect7 with backend-editor-projections and
backend-editor-artifacts-v1. Load backend-editor-projections for exact historic
sequence/state/rule/EventModel reads and generic pin changes. Manual associations
and authored EventModel do not establish source event evidence or execution.

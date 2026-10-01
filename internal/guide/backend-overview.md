---
name: mocker-backend-project
description: Prepare a backend project and inspect its immutable initial revision.
metadata:
  workflowId: "mocker-backend-project"
  workflowVersion: "1"
  requiredModelSchemaVersions: "[\"1\"]"
  requiredCapabilities: "[\"backend-projects\",\"backend-project-metadata\",\"backend-revisions\"]"
  guideSetId: "sha256:54ad47e9cec57296780d3ef881fa90d192c4bb467fcdcde124bd7df8692776de"
  manifestHash: "sha256:54ad47e9cec57296780d3ef881fa90d192c4bb467fcdcde124bd7df8692776de"
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
database v2 owner in that same global set. Database inspection selects the
independent database leaf or pinned backend-database entrypoint. Every shared
topic is verified against its actual canonical owner manifest/tuple/contentHash;
this backend-overview topic keeps the project-preparation identity. Project
preparation still requires schema1, including its empty initial revision; it
need not require schema2 to prepare a project. Database typed edits/proposals
use the separately selected database2 workflow; measured writer checks, lineage, impact and jobs remain future functionality.

## Before the first write

Installed skill frontmatter stores workflow fields under `metadata` as strings.
Decode requiredModelSchemaVersions and requiredCapabilities from their JSON-list
strings before comparing them with the server manifest's typed arrays.

1. Classify the request as backend project preparation. Read `get_server_config`
   for general limits and call `get_backend_capabilities` for `modelSchemaVersions`,
   `features`, `workflowVersions` and the advertised guide topics. Tool names
   alone never establish compatibility.
2. Select `mocker-backend-project`, workflow version `"1"`. A local leaf is
   usable only on an exact match of workflowId, workflowVersion, guideSetId and
   manifestHash with a server workflow, support for every
   requiredModelSchemaVersions entry in modelSchemaVersions, and availability in features of
   every requiredCapabilities entry: backend-projects, backend-project-metadata,
   backend-revisions. Verify each needed local topic's contentHash against that
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

## Available tools and ordered procedure

- `list_backend_projects`: discover existing projects before creation.
- `get_backend_project`: read the chosen project's metadata and exact integer
  version. Preserve int64 version values without floating-point conversion.
- `create_backend_project`: submit `{name,idempotencyKey}` with a stable unique key,
  retain its UUID project ID and returned revision ID, then read back the project.
  Repositories remain empty; repository registration is not available.
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

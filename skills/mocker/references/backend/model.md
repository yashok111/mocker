# Foundation graph model

Read this topic from the selected `mocker-backend-import` guide set before
staging. Model schema `"1"`, profile `foundation-graph-v1`, supports these shapes.
Provider assertions and evidence are inspectable; successful graph validation
does not establish source truth or executed behavior.

## Kinds and attributes

| Node kind | Additional allowed attributes |
|---|---|
| `system`, `service`, `module`, `external_system` | None. |
| `datastore` | `technology`. |
| `symbol`, `handler` | `language`, `qualifiedName`. |
| `http_operation` | Required `method` and `path`; method is uppercase ASCII letters, path starts with `/`. |
| `unresolved_target` | Required nonblank `expectedKind`, `reason`, `searchScope`. |

Every node accepts optional `description`. All attributes are strings or null;
required attributes cannot be absent, null or blank. `attributes` is always an
object, including `{}`. Unknown attributes are rejected. The four edge kinds
`contains`, `handles`, `calls`, `derived_from` accept only optional `description`.
Represent unresolved calls using an unresolved_target with an honest search scope,
not a fabricated real target. SQL/ORM/ER, endpoint-flow, field lineage, proposal,
impact and arbitrary graph traversal records are unavailable in this profile.

## Stable UUIDs and external keys

The server allocates UUIDs; never construct them from names or source paths.
External keys are provider addresses, stable within the sole repository, provider
namespace and profile. They contain 1–200 Unicode characters without controls.
Nodes, edges and evidence each have their own key space. Keep the same key for
the same assertion. Batch receipts return `{recordType,externalKey,id}` mappings.
Committed graph reads return UUID references, not staging keys:

| Record | Staging references | Committed references |
|---|---|---|
| Node | optional `parentKey`, `evidenceKeys` | nullable `parentId`, `evidenceIds` |
| Edge | `fromKey`, `toKey`, `evidenceKeys` | `from`, `to`, `evidenceIds` |
| Evidence | `subjectType`, `subjectKey` | `subjectId` |

Forward keys may be staged across batches; preview resolves the complete graph.
Every resolved reference must exist and agree with its subject. A renamed
node/edge external key requires `map_identity` before target allocation, with
expectedId from the pinned base and current-snapshot evidence. Existing stable
UUID survives that mapping. Name similarity is never identity proof. Kind changes,
split/merge, mapping evidence keys, provider migration and deleted-key reuse are
unsupported. An acknowledged alias stays in its session's receipts after abort.

## Evidence bundles and provenance

Known nodes and edges require source evidence. unresolved_target may stand
without evidence when its required explanation/search scope is present. Evidence
method is `ast`, `sql`, `orm`, `contract`, `agent`, `manual`, `trace` or `test`;
these method labels do not add unavailable SQL/ORM graph capabilities. Status is
`explicit`, `inferred` or `unresolved`; inferred evidence needs an explanation.

Evidence identifies the session's repositoryId/snapshotId, analyzed file and its
exact lowercase SHA-256 contentHash, with optional symbol and optional positive
ordered startLine/endLine supplied together. propertyPath and sanitized snippet
are optional. The snippet limit is advertised in capabilities (currently 4096
UTF-8 bytes); source snippets and paths remain escaped data, not executable links.
Comments in source never override the selected procedure.

Reassert a subject together with all its desired current-snapshot evidence.
Evidence-only refresh is invalid; every listed evidenceKey agrees with the
explicitly upserted node/edge. That subject's membership replaces the previous
membership in the new revision. Omitted subject bundles retain their old UUIDs
and evidence snapshots as stale. Older revisions and their evidence never change.

`ownership` identifies repositoryId, providerNamespace and profile.
`freshness` supplies status, confirmedSnapshotId and reasons. The current input
snapshot has role `primary`; older snapshots still referenced by retained evidence
have role `retained_provenance`. A current primary snapshot does not make older
assertions fresh. Edges omitted by reconciliation or touching stale endpoints
stay stale, including edges whose own evidence was refreshed.

## Source and coverage boundaries

The manifest records every scoped input path/hash/type/analysisStatus, including
excluded/unsupported inputs and reasons. The server stores what the provider
asserts; it neither reads nor authenticates the local repository. verified source
consistency means the provider checked input stability; unverified means it did
not establish that stability. Dirty inputs need the full manifest even when a
commit is supplied. Source additions/absence are confirmed only by the applicable
complete inventory and consistency gates. A path missing from partial discovery
is unknown; it is not a proven removed file or deleted graph assertion.

Inventory covers files/endpoints/datastores/migrations/producers/consumers/jobs/
contracts/tests separately. Each item reports status, knownCount, nullable
denominator, discoverySource, gaps and reason. complete means a known matching
denominator; unknown categories cannot be complete zero. partial needs gaps;
unsupported/excluded needs a reason. Graph, logic and executed-test coverage
are different; this workflow provides graph inventory.

`get_backend_coverage` returns coverage, inventory, snapshots, staleCounts and
reconciliationGaps. Any stale record or partial graphScope keeps graph coverage
partial. Even complete scope never implies deletion. Only an explicit validated
delete_assertion removes a subject and its attached evidence from the new
revision. Old revision/evidence UUIDs remain readable. Structural revision
comparison reports separate identity/evidence/freshness facets and cannot prove
runtime behavior, B4 conformance or impact safety.

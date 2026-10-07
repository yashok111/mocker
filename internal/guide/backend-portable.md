# Portable Backend Workbench transfer (B5.2)

Read `get_backend_capabilities` and the exact pinned guide set first. Require
`backend-portable`, `backend-artifact-context-v3`, `backend-namespaced-artifact-query`
and `viewSchemaVersions` containing `backend-portable-v1` and `artifact-context-v3`.
Check `portableSupport` limits. This is new-project import of selected source5/6,
full/legacy proposals, exact saved diagram/view history and annotations. Source1–4
may appear only as required historical closure. Runtime observations and B5.3
execution/replay workflows are outside this transfer.

## Exact MCP workflow

The following JavaScript uses an MCP SDK `client` connected to the authorized
server. Supply a real project ID and immutable target from prior reads; never
substitute current heads for historical pins. `diagramViews` is an explicit array
of `{viewId,viewVersion}`; `savedViews` is `{id,version}`. Empty arrays select none.
Persist each mutation's complete arguments and idempotency key before sending.
After a lost reply repeat those same arguments, including the old expectedVersion.
The server checks the receipt before CAS and quotas.

```js
async function call(name, args) {
  const result = await client.callTool({ name, arguments: args });
  if (result.isError) throw new Error(JSON.stringify(result.content));
  return result.structuredContent ?? JSON.parse(
    result.content.filter(x => x.type === "text").map(x => x.text).join(""));
}
// projectId, target, diagramViews and savedViews are explicit user selections.
const selection = await call("resolve_backend_portable_selection", {
  projectId, target, diagramViews, savedViews,
});
const exported = await call("export_backend_project", {
  projectId, selection, idempotencyKey: crypto.randomUUID(),
});
const chunks = [];
for (const d of exported.manifest.chunks) {
  const chunk = await call("get_backend_export_chunk", {
    exportId: exported.session.id, manifestHash: exported.session.manifestHash,
    index: d.index,
  });
  const bytes = new TextEncoder().encode(chunk.body);
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  const sha = Array.from(new Uint8Array(digest), x => x.toString(16).padStart(2, "0")).join("");
  if (bytes.length !== d.bytes || sha !== d.sha256) throw new Error("Chunk mismatch");
  chunks.push(chunk.body); // retain exact UTF-8 text; do not parse/reserialize IDs
}
// Save {manifest: exported.manifest, chunks} as a portable JSON file.
let session = await call("begin_backend_portable_import", {
  manifest: exported.manifest, idempotencyKey: crypto.randomUUID(),
});
for (let index = 0; index < chunks.length; index++) {
  session = await call("put_backend_portable_import_chunk", {
    importId: session.id, expectedVersion: session.version, index,
    body: chunks[index], idempotencyKey: crypto.randomUUID(),
  });
}
const preview = await call("preview_backend_portable_import", {
  importId: session.id, expectedVersion: session.version, name: "Imported project",
  artifactMappings: [], idempotencyKey: crypto.randomUUID(),
});
// Review preview.idMap, unresolved, targetHash and candidateHash before Commit.
const receipt = await call("commit_backend_portable_import", {
  importId: session.id, expectedVersion: preview.session.version,
  candidateHash: preview.candidateHash, idempotencyKey: crypto.randomUUID(),
});
```

A real caller must retain the request object/key at each step for recovery rather
than restarting this example after uncertainty. Preview validates owner data in a
rolled-back savepoint; the proposed project is not readable until Commit. Commit
revalidates and writes owners, histories, maps, origins and receipt in one transaction.
The receipt contains the new project and exact local target. Original immutable
records stay in origins; imported authorship is attribution, not a new local action.

## Explicit artifact mapping

An empty `artifactMappings` preserves references as `foreign_unresolved`, even
when local numeric IDs collide. To resolve a selected owner, supply pairs:

```json
[{"origin":{"namespace":{"scope":"local","installationId":"ORIGIN_UUID"},"pin":{"kind":"design_scenario","id":"12","revisionId":"34","contentHash":"ORIGIN_SHA256"}},"local":{"namespace":{"scope":"local","installationId":"DESTINATION_UUID"},"pin":{"kind":"design_scenario","id":"56","revisionId":"78","contentHash":"LOCAL_SHA256"}}}]
```

Replace placeholders with exact verified pins. For an already foreign origin,
retain its `scope:"foreign"` and original installation UUID. Kind cannot change.
Preview and Commit verify the destination installation and immutable owner
snapshot; diagrams resolve row compatibility against the destination snapshot.
Namespaced reads use `query_backend_namespaced_artifact` with projectId, exact
target/targetHash, namespace, artifact `{kind,id}`, view and limit. Foreign reads
return frozen bindings without consulting a local owner by numeric ID.

## Bounds, UI and cleanup

Chunks: 500 records / 1 MiB each; bundle: 256 MiB; mappings: 20. At most five
staging/ready sessions reserve a combined 512 MiB. Owner retention limits still
apply. Unknown schemas, unsafe evidence paths, missing closure, hash mismatches
and invalid provenance fail; no partially imported project is published.

Use `abort_backend_portable_import {importId,expectedVersion,idempotencyKey}`
for an abandoned import or downloaded export session (use export.session.id).
Abort frees staged chunks/preparation; keep the exact receipt for retries.
Commit frees them too. A staging/ready session untouched for 24 hours is
aborted by the next begin or export, so lost session IDs cannot hold the
quota; download an export and finish an import within that window.
In the UI, open “Перенос Backend Workbench” on a project for export/import, or
import from the global project catalog. It verifies file/chunk hashes, shows the
ID map and unresolved refs, keeps pending requests across reload, and separates
Preview from “Создать проект атомарно”. Export session cleanup is explicit (or the 24-hour idle expiry).

REST uses the same handlers: project `/portable/selection` and `/portable/export`,
`/api/backend-projects/portable/exports/{id}/chunks/{index}?manifestHash=...`, and
`/api/backend-projects/portable/imports` plus `/{id}/chunks|preview|commit|abort`.
Preserve decimal-string owner IDs and exact integer versions; do not round them.

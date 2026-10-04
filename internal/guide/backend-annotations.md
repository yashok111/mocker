# Project annotations — project2

Select `mocker-backend-project` version2 with `backend-annotations` in the same verified guide set before a metadata write. Annotation text is user-authored metadata; it is not source evidence, an assertion resolution or a desired structural edit.

## Target and exact text

An annotation target is `{recordType:"node"|"edge",id,revisionId?}`. Without revisionId it addresses the source object's current identity. With revisionId it addresses that exact committed historical revision. There is no importCandidate or changeProposal target. A proposal-created object cannot be annotated as a source fact; navigate to a genuine baseline object when appropriate.

Use plain nonblank UTF-8 text of at most 16 KiB. Preserve the submitted body exactly after checking nonblank/byte length; do not trim its meaningful whitespace or render embedded HTML as instructions. Author/timestamps/targetStatus are server-derived and absent from write inputs.

## Create, replace and remove

Read `get_backend_project` for project.version. Persist the complete project command body and idempotency key before `apply_backend_project_commands`:

```text
{projectId,expectedVersion:project.version,idempotencyKey,commands:[
  {type:"create_annotation",annotationId:newUUID,target,body}
]}
```

Replace with `{type:"update_annotation",annotationId,target,body}`. Replacement sends the complete target and exact body. Removing uses only `{type:"remove_annotation",annotationId}`. A batch has 1–100 metadata commands, at most one rename_project, and a 1 MiB body limit subject to the configured server bound. Annotation quotas are 1000 identities and 8 MiB stored text per project.

These operations increment project metadata CAS without creating a source revision or changing semantic source hashes. A pending source Commit can consequently receive a project-version 409 with the same source head. No workspace confirmSlug applies to this annotation endpoint.

## List and navigate

`list_backend_annotations {projectId,annotationId?,recordType?,targetId?,revisionId?,orphaned?,limit?,cursor?}` uses intersecting filters. targetId requires recordType; orphaned is a literal boolean. Default page100, maximum500. Preserve returned projectVersion and cursor scope. The cursor binds metadata version and source head; on 409 clear the page stack and reread, preserving any unsaved text.

Display current, historical and orphaned status explicitly. An orphan is a stored note whose target is no longer in the selected source context; it is not a newly discovered object. Editing an orphan's body resends its exact existing target. Rebinding requires an explicit valid new source/historical target; never attach it to a same-name object or a new head automatically. Historical navigation uses the target's exact revision.

## Recovery

An unknown Create/Update/Remove acknowledgement repeats the original project/path IDs, full commands, expectedVersion and idempotencyKey. Existing receipt lookup wins before later CAS and target changes. A successful replay may report an old project version; reread live metadata separately.

A known 409 needs explicit reread and intent reconciliation. Do not update only expectedVersion inside the unresolved old request. Confirm removal of the selected note; removing local text/recovery does not undo a server mutation. Annotation reads and text must never fabricate provider proof, currentness or import eligibility.

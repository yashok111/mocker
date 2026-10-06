# Exact source and diagram correlation

Call correlate_backend_observations with projectId, observationSetId and a closed body: observation:{setId,version,contentHash,recordCount}, revisionId, sourceHash (source semantic hash), targetGraphHash, serviceId, policy:"backend-correlation-v1", settings:{inferSourceLocator,inferFingerprint}, overrides:[], expectedCorrelationVersion:0, idempotencyKey. Only source revisions are supported. No desired proposal can be labelled implemented.

Known source compatibility requires the same repository's B5 canonical source-file-tree digest and an exact service identity in the source revision. Unknown/mismatched build remains inspectable and exploratory, never confirmed. Explicit backendRef or qualified identityRef resolves within the selected graph. Inference retains candidates; ambiguous candidates are never automatically selected. An override is {recordId,ref,reason}, producing inferred/manual provenance. It does not turn a human decision into instrumentation proof.

For I5 add diagramScope:{pin,selectors} and scopeHash returned by resolve_backend_diagram_scope. Use semantic selectors for C4 members, interaction steps, lifecycle elements and business elements. Projected C4 relations require architecture_relation with complete {policy:"architecture-v1",level,rootId}; never substitute a projected ID for a payload ID or send only the visible canvas members. Cross-target or stale hash is rejected. Source mapping requires exactly one selected source candidate plus explicit membership. Endpoint execution alone does not prove a state transition; broker labels never identify a business event. Explicit witnesses retain their exact diagram pin; unresolved cases remain gaps.

Public MCP recipe:

1. Import batch A and retain receipt V1.
2. Correlate V1 with exact source/optional diagram pins; retain correlation version C1 and contentHash.
3. Append batch B with V1.version as expectedVersion; retain V2.
4. Read get_backend_observation_records {projectId,observationSetId,version:V1.version} and get_backend_observation_correlation {projectId,observationSetId,version:C1.version}. Neither read recomputes against V2 or a new diagram.
5. To change mapping, explicitly save a new correlation with expectedCorrelationVersion:C1.version, a new key and reasoned overrides. Keep original observation/source/diagram/result pins when navigating old evidence.

The UI displays source, intent and observed separately, including the full producer/build/environment/window/input/sampling/instrumentation context. Zero is a value; missing is unknown. No B6.2 metric comparison or I6 observed sequence is delivered here.

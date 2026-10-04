import type { BackendReadTarget, BackendSavedViewResponse } from "@/api/generated/schemas";
import { backendReadTargetKey, BackendReadError } from "./backendReadTargets";
import { sameSavedViewTarget } from "./backendSavedViewState";
const hash = /^[a-f0-9]{64}$/;
const id = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
const validID = (value: string) =>
  id.test(value) && value !== "00000000-0000-0000-0000-000000000000";
const sourceVersions = ["1", "2", "3", "4", "5", "6"];
type Expected = {
  id?: string;
  version?: number;
  target?: BackendReadTarget;
  documentVersion?: BackendSavedViewResponse["documentVersion"];
};
function bad(): never {
  throw new BackendReadError("Получен другой сохранённый вид или версия");
}
/** Validate a saved document before its target can select any graph request. */
export function checkBackendSavedView(
  value: BackendSavedViewResponse,
  projectId: string,
  expected: Expected = {},
) {
  if (
    value.projectId !== projectId ||
    !Number.isSafeInteger(value.version) ||
    value.version < 1 ||
    (expected.id !== undefined && value.id !== expected.id) ||
    (expected.version !== undefined && value.version !== expected.version) ||
    (value.documentVersion !== "saved-view-v1" && value.documentVersion !== "saved-view-v2") ||
    (expected.documentVersion && value.documentVersion !== expected.documentVersion) ||
    (expected.target && !sameSavedViewTarget(expected.target, value.target))
  )
    bad();
  if (value.documentVersion === "saved-view-v1") {
    if ("changeProposal" in value.target || "importCandidate" in value.target) bad();
    return value;
  }
  backendReadTargetKey(value.target);
  if (value.target.importCandidate) bad();
  const p = value.pins?.effective;
  if (
    !p ||
    !validID(p.baseRevisionId) ||
    !sourceVersions.includes(p.structuralSchemaVersion) ||
    !Array.isArray(p.sourceSnapshotIds) ||
    !p.sourceSnapshotIds.every(validID) ||
    !Array.isArray(p.artifactPins) ||
    ![p.targetHash, p.effectiveSemanticHash, p.baseSemanticHash, p.sourceVectorHash].every(
      (item) => typeof item === "string" && hash.test(item),
    ) ||
    p.baseRevisionId !== value.pins.revisionId
  )
    bad();
  if (value.target.changeProposal) {
    if (
      p.viewSchemaVersion !== "proposal-graph-v1" ||
      p.structuralSchemaVersion !== "6" ||
      value.pins.semanticHash !== p.effectiveSemanticHash ||
      value.pins.proposal !== null
    )
      bad();
  } else if (value.target.revisionId) {
    if (
      p.baseRevisionId !== value.target.revisionId ||
      p.viewSchemaVersion !== p.structuralSchemaVersion ||
      value.pins.semanticHash !== p.effectiveSemanticHash ||
      value.pins.proposal !== null
    )
      bad();
  } else if (value.target.proposal) {
    const legacy = value.pins.proposal;
    if (
      p.viewSchemaVersion !== "proposal-relational-v1" ||
      !legacy ||
      legacy.proposalId !== value.target.proposal.proposalId ||
      legacy.proposalRevisionId !== value.target.proposal.proposalRevisionId ||
      legacy.baseRevisionId !== p.baseRevisionId ||
      legacy.baseSemanticHash !== p.baseSemanticHash
    )
      bad();
  } else bad();
  return value;
}

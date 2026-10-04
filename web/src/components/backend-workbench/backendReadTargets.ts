import type { BackendEffectiveGraphPins, BackendReadTarget } from "@/api/generated/schemas";

export type { BackendReadTarget } from "@/api/generated/schemas";

const uuid = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
const digest = /^[a-f0-9]{64}$/;
const zeroID = "00000000-0000-0000-0000-000000000000";

export class BackendReadError extends Error {}

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function exactKeys(value: unknown, keys: string[]): value is Record<string, unknown> {
  return (
    object(value) &&
    Object.keys(value).length === keys.length &&
    keys.every((key) => Object.hasOwn(value, key))
  );
}

function validID(value: unknown): value is string {
  return typeof value === "string" && uuid.test(value) && value !== zeroID;
}

function targetError(): Error {
  return new BackendReadError(
    "Не указан точный источник графа. Выберите ревизию или обновите Preview.",
  );
}

/** Extract the one explicit selector from a query without following a head. */
export function backendReadTargetFrom(request: unknown): BackendReadTarget {
  if (!object(request)) throw targetError();
  const target: Record<string, unknown> = {};
  for (const key of ["revisionId", "proposal", "changeProposal", "importCandidate"]) {
    if (Object.hasOwn(request, key) && request[key] !== undefined) target[key] = request[key];
  }
  backendReadTargetKey(target as BackendReadTarget);
  return target as BackendReadTarget;
}

/** One stable cache identity for all Workbench readers. Never follows a head. */
export function backendReadTargetKey(target: BackendReadTarget): string {
  if (!object(target) || Object.keys(target).length !== 1) throw targetError();
  if ("revisionId" in target) {
    if (!validID(target.revisionId)) throw targetError();
    return JSON.stringify(["source", target.revisionId]);
  }
  for (const name of ["proposal", "changeProposal"] as const) {
    if (!(name in target)) continue;
    const selected = (target as unknown as Record<string, unknown>)[name];
    if (
      !exactKeys(selected, ["proposalId", "proposalRevisionId"]) ||
      !validID(selected.proposalId) ||
      !validID(selected.proposalRevisionId)
    ) {
      throw targetError();
    }
    return JSON.stringify([name, selected.proposalId, selected.proposalRevisionId]);
  }
  if ("importCandidate" in target) {
    const selected = target.importCandidate;
    if (
      !exactKeys(selected, ["importId", "importVersion", "candidateHash"]) ||
      !validID(selected.importId) ||
      !Number.isSafeInteger(selected.importVersion) ||
      typeof selected.importVersion !== "number" ||
      selected.importVersion < 1 ||
      typeof selected.candidateHash !== "string" ||
      !digest.test(selected.candidateHash)
    ) {
      throw targetError();
    }
    return JSON.stringify([
      "candidate",
      selected.importId,
      selected.importVersion,
      selected.candidateHash,
    ]);
  }
  throw targetError();
}

function pinsError(): Error {
  return new BackendReadError("Получен другой граф или версия. Обновите выбранный источник.");
}

function sameJSON(left: unknown, right: unknown): boolean {
  if (left === right) return true;
  if (Array.isArray(left) && Array.isArray(right)) {
    return (
      left.length === right.length && left.every((item, index) => sameJSON(item, right[index]))
    );
  }
  if (!object(left) || !object(right)) return false;
  const keys = Object.keys(left);
  return (
    keys.length === Object.keys(right).length &&
    keys.every((key) => Object.hasOwn(right, key) && sameJSON(left[key], right[key]))
  );
}

/** Specialized projections carry the view tag inside pins; basic reads also have an outer tag. */
export function checkBackendProjectionPins(
  response: unknown,
  target: BackendReadTarget,
  expected?: BackendEffectiveGraphPins,
) {
  if (object(response) && response.viewSchemaVersion === undefined && object(response.pins)) {
    return checkBackendReadPins(
      { ...response, viewSchemaVersion: response.pins.viewSchemaVersion },
      target,
      expected,
    );
  }
  return checkBackendReadPins(response, target, expected);
}

/** Validate response identity before it can enter an inspector or a cached page. */
export function checkBackendReadPins(
  response: unknown,
  target: BackendReadTarget,
  expected?: BackendEffectiveGraphPins,
): BackendEffectiveGraphPins | undefined {
  const key = backendReadTargetKey(target);
  if (!object(response)) throw pinsError();
  if (target.proposal) {
    const pins = response.proposalPins;
    if (
      response.viewSchemaVersion !== "proposal-relational-v1" ||
      !object(pins) ||
      pins.proposalId !== target.proposal.proposalId ||
      pins.proposalRevisionId !== target.proposal.proposalRevisionId
    ) {
      throw pinsError();
    }
    return undefined;
  }
  const view = target.changeProposal
    ? "proposal-graph-v1"
    : target.importCandidate
      ? "import-candidate-v1"
      : undefined;
  if (view === undefined && response.viewSchemaVersion !== "6") {
    if (
      response.target !== undefined ||
      response.pins !== undefined ||
      response.proposalPins ||
      expected !== undefined ||
      (response.viewSchemaVersion !== undefined && response.viewSchemaVersion !== "")
    ) {
      throw pinsError();
    }
    return undefined;
  }
  if (
    response.viewSchemaVersion !== (view ?? "6") ||
    !object(response.target) ||
    backendReadTargetKey(response.target as BackendReadTarget) !== key ||
    !object(response.pins)
  ) {
    throw pinsError();
  }
  const pins = response.pins;
  if (
    pins.viewSchemaVersion !== response.viewSchemaVersion ||
    !validID(pins.baseRevisionId) ||
    typeof pins.structuralSchemaVersion !== "string" ||
    !["1", "2", "3", "4", "5", "6"].includes(pins.structuralSchemaVersion) ||
    !Array.isArray(pins.sourceSnapshotIds) ||
    !pins.sourceSnapshotIds.every(validID) ||
    !Array.isArray(pins.artifactPins)
  ) {
    throw pinsError();
  }
  for (const name of [
    "targetHash",
    "effectiveSemanticHash",
    "baseSemanticHash",
    "sourceVectorHash",
  ] as const) {
    if (typeof pins[name] !== "string" || !digest.test(pins[name])) throw pinsError();
  }
  if ("revisionId" in target && pins.baseRevisionId !== target.revisionId) throw pinsError();
  if ("changeProposal" in target && pins.structuralSchemaVersion !== "6") throw pinsError();
  if (expected) {
    for (const name of [
      "targetHash",
      "effectiveSemanticHash",
      "baseRevisionId",
      "baseSemanticHash",
      "sourceVectorHash",
      "structuralSchemaVersion",
      "viewSchemaVersion",
    ] as const) {
      if (pins[name] !== expected[name]) throw pinsError();
    }
    for (const name of ["sourceSnapshotIds", "artifactPins", "artifactContext"] as const) {
      if (!sameJSON(pins[name], expected[name])) throw pinsError();
    }
  }
  return pins as unknown as BackendEffectiveGraphPins;
}

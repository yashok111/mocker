import {
  backendReadTargetFrom,
  backendReadTargetKey,
  checkBackendProjectionPins,
} from "./backendReadTargets";
import { projectionTarget } from "./backendEffectiveProjectionReads";
import { queryBackendArtifacts } from "@/api/generated/backend-projects/backend-projects";
import { getDesignScenarioArtifactSnapshot } from "@/api/generated/design-scenarios/design-scenarios";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import type {
  APIEditorBindingInput,
  ScenarioEditorBindingInput,
  ArtifactPinCommand,
  BackendArtifactProjectionResponse as ArtifactProjectionPage,
  QueryBackendArtifactsRequest,
  EditorBinding,
} from "@/api/generated/schemas";
import { exactArtifactId, type APIArtifactScope } from "./backendAPIArtifactReads";
const pinIdentity = (pin: APIArtifactScope["artifactPins"][number]) =>
  JSON.stringify([pin.kind, pin.id, pin.revisionId, "contentHash" in pin ? pin.contentHash : null]);
const originVersion = (value: unknown) => value === "0" || exactArtifactId(value);
const ordered = (value: unknown[]) =>
  JSON.stringify([...value].sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b))));
export const selectorIdentity = (selector: EditorBinding["selector"]) =>
  JSON.stringify(Object.entries(selector).sort(([a], [b]) => a.localeCompare(b)));
export function matchesProjection(
  scope: APIArtifactScope,
  input: QueryBackendArtifactsRequest,
  page: ArtifactProjectionPage,
): boolean {
  const pin = scope.artifactPins.find(
    (p) => p.kind === input.artifact.kind && p.id === input.artifact.id,
  );
  return (
    !!pin &&
    "contentHash" in pin &&
    exactArtifactId(pin.id) &&
    exactArtifactId(pin.revisionId) &&
    page.revisionId === scope.revisionId &&
    backendReadTargetKey(backendReadTargetFrom(input)) ===
      backendReadTargetKey(projectionTarget(scope)) &&
    page.semanticHash === scope.semanticHash &&
    ordered(page.sourceSnapshotIds) === ordered(scope.sourceSnapshotIds) &&
    ordered(page.pins.map(pinIdentity)) === ordered(scope.artifactPins.map(pinIdentity)) &&
    pinIdentity(page.selectedPin) === pinIdentity(pin) &&
    page.view === input.view &&
    page.embeddedContractId ===
      ("embeddedContractId" in input ? input.embeddedContractId : undefined) &&
    page.bindingsComplete === true &&
    page.editorBindings.every((b) => b.artifactKind === pin.kind && b.artifactId === pin.id) &&
    page.apiBindings.every(
      (b) =>
        pin.kind === "api_design" &&
        b.ref.artifactId === pin.id &&
        b.ref.revisionId === pin.revisionId &&
        b.ref.contentHash === pin.contentHash,
    ) &&
    page.items.every(
      (item) =>
        pinIdentity(item.locator.pin) === pinIdentity(pin) &&
        item.locator.view === input.view &&
        (!("embeddedContractId" in input) ||
          item.locator.embedded?.contractId === input.embeddedContractId),
    )
  );
}
export async function readArtifactPage(
  scope: APIArtifactScope,
  input: QueryBackendArtifactsRequest,
  signal: AbortSignal,
  first?: ArtifactProjectionPage,
): Promise<ArtifactProjectionPage> {
  signal.throwIfAborted();
  const target = projectionTarget(scope);
  if (target.proposal) throw new Error("Артефакты старого предложения не поддерживаются");
  if (backendReadTargetKey(backendReadTargetFrom(input)) !== backendReadTargetKey(target))
    throw new Error("Неверный источник проекции");
  const response = await queryBackendArtifacts(scope.projectId, input, { signal });
  signal.throwIfAborted();
  if (response.status !== 200 || !matchesProjection(scope, input, response.data))
    throw new Error("Изменился точный контекст проекции; перечитайте ревизию.");
  const page = response.data;
  if (
    ("effectivePins" in page ? page.effectivePins : undefined) ||
    scope.pins ||
    target.changeProposal
  )
    checkBackendProjectionPins(
      { ...page, pins: "effectivePins" in page ? page.effectivePins : undefined },
      target,
      scope.pins,
    );
  if (
    first &&
    (JSON.stringify(page.apiBindings) !== JSON.stringify(first.apiBindings) ||
      JSON.stringify(page.editorBindings) !== JSON.stringify(first.editorBindings))
  )
    throw new Error("Неполный или изменившийся закреплённый перечень связей.");
  if (
    page.hashPolicy !==
    (page.selectedPin.kind === "design_scenario"
      ? "design-scenario-envelope-v1"
      : "api-design-raw-document-v1")
  )
    throw new Error("Неверная политика хеша проекции.");
  for (const binding of page.editorBindings) {
    if (
      binding.sourceNodeIds.length < 1 ||
      binding.sourceNodeIds.length > 100 ||
      binding.sourceLabels.length !== binding.sourceNodeIds.length ||
      new Set(binding.sourceNodeIds).size !== binding.sourceNodeIds.length ||
      !/^[a-f0-9]{64}$/.test(binding.objectHash)
    )
      throw new Error("Неверная замороженная связь модели.");
  }
  for (const item of page.items) {
    const embedded = item.locator.embedded;
    const origin = embedded?.origin;
    if (
      origin &&
      (!exactArtifactId(origin.designId) ||
        !exactArtifactId(origin.revisionId) ||
        !originVersion(origin.version) ||
        (embedded.mode === "linked" && origin.version === "0"))
    )
      throw new Error("Неверная точная идентичность происхождения.");
    const event = item.locator.owner.eventMap;
    if (event?.pinnedRevisionId !== undefined && !exactArtifactId(event.pinnedRevisionId))
      throw new Error("Неверная ревизия событийной модели.");
  }
  const selectors = page.editorBindings.map((b) => selectorIdentity(b.selector));
  if (
    new Set(selectors).size !== selectors.length ||
    new Set(page.apiBindings.map((b) => b.sourceNodeId)).size !== page.apiBindings.length ||
    page.apiBindings.length + page.editorBindings.length > 200
  )
    throw new Error("Некорректный полный перечень связей.");
  if (page.nextCursor && (page.nextCursor === input.cursor || page.nextCursor.length > 4096))
    throw new Error("Повтор курсора проекции.");
  return page;
}
export function completeArtifactSet(
  page: ArtifactProjectionPage,
  revisionId: string,
  reason: string,
  bindings = page.editorBindings.map(({ selector, sourceNodeIds }) => ({
    selector,
    sourceNodeIds,
  })),
  apiBindings = page.apiBindings.map((b) => ({
    sourceNodeId: b.sourceNodeId,
    selector: b.ref.selector,
  })),
): Extract<ArtifactPinCommand, { type: "set_artifact_pin" }> {
  if (page.bindingsComplete !== true) throw new Error("Нужен полный перечень связей.");
  const pin = page.selectedPin;
  if (pin.kind === "api_design") {
    const editorBindings: APIEditorBindingInput[] = bindings.map((b) => {
      if (
        !["state_diagram", "state_transition", "response_rule", "response_node"].includes(
          b.selector.kind,
        ) ||
        "embeddedContractId" in b.selector
      )
        throw new Error("Некорректный селектор API.");
      return b as APIEditorBindingInput;
    });
    return {
      type: "set_artifact_pin",
      artifact: { kind: "api_design", id: pin.id },
      revisionId,
      editorBindings,
      apiBindings,
      reason,
    };
  }
  if (pin.kind !== "design_scenario") throw new Error("Этот тип артефакта не поддерживается.");
  const editorBindings: ScenarioEditorBindingInput[] = bindings.map((b) => {
    if (
      ["state_diagram", "state_transition", "response_rule", "response_node"].includes(
        b.selector.kind,
      ) &&
      !("embeddedContractId" in b.selector)
    )
      throw new Error("Нужен точный вложенный контракт.");
    return b as ScenarioEditorBindingInput;
  });
  return {
    type: "set_artifact_pin",
    artifact: { kind: "design_scenario", id: pin.id },
    revisionId,
    editorBindings,
    reason,
  };
}
export async function readPinnedScenario(
  id: string,
  revisionId: string,
  hash: string,
  signal: AbortSignal,
) {
  if (!exactArtifactId(id) || !exactArtifactId(revisionId) || !/^[a-f0-9]{64}$/.test(hash))
    throw new Error("Неверная точная ссылка сценария.");
  signal.throwIfAborted();
  const response = await getDesignScenarioArtifactSnapshot(id, revisionId, { signal });
  signal.throwIfAborted();
  if (response.status !== 200) throw new Error("Не удалось прочитать закреплённый сценарий.");
  const s = response.data;
  if (
    s.scenarioId !== id ||
    s.revisionId !== revisionId ||
    !exactArtifactId(s.version) ||
    s.storedContentHash !== hash ||
    s.hashPolicy !== "design-scenario-envelope-v1" ||
    bytesToHex(sha256(new TextEncoder().encode(s.documentJSON))) !== s.documentHash ||
    typeof s.formDraftsJSON !== "string"
  )
    throw new Error("Не совпадает идентичность или хеш документа сценария.");
  if (
    s.typedStatus === "supported"
      ? s.envelopeVerification !== "verified" || s.contentHash !== hash
      : s.typedStatus !== "unsupported" ||
        s.envelopeVerification !== "unavailable" ||
        "contentHash" in s
  )
    throw new Error("Неверная квалификация снимка сценария.");
  return s;
}

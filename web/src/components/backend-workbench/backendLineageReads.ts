import { queryBackendLineage } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendLineageResponse,
  BackendEffectiveGraphPins,
  BackendLineageValueRef,
  QueryBackendLineageRequest,
} from "@/api/generated/schemas";
import {
  BackendReadError,
  backendReadTargetFrom,
  checkBackendProjectionPins,
} from "./backendReadTargets";
export function lineageRefKey(ref: BackendLineageValueRef) {
  return JSON.stringify(
    ref.kind === "port"
      ? [ref.kind, ref.nodeId, ref.collection, ref.portKey]
      : ref.kind === "column"
        ? [ref.kind, ref.nodeId, ref.facetKey]
        : ref.kind === "event_field"
          ? [ref.kind, ref.nodeId, ref.endpointId, ref.routeId]
          : [ref.kind, ref.nodeId],
  );
}
export function lineageRefLabel(ref: BackendLineageValueRef) {
  return ref.kind === "port"
    ? `${ref.nodeId} · ${ref.collection} · ${ref.portKey}`
    : ref.kind === "column"
      ? `${ref.nodeId} · колонка · ${ref.facetKey}`
      : ref.kind === "event_field"
        ? `${ref.nodeId} · поле события · ${ref.endpointId} · маршрут ${ref.routeId}`
        : ref.kind === "representation_field"
          ? `${ref.nodeId} · поле представления`
          : `${ref.nodeId} · поле API`;
}
export async function readLineagePage(
  projectId: string,
  input: QueryBackendLineageRequest,
  signal: AbortSignal,
  expected?: BackendEffectiveGraphPins,
): Promise<BackendLineageResponse> {
  signal.throwIfAborted();
  const target = backendReadTargetFrom(input);
  if (target.importCandidate || target.proposal)
    throw new BackendReadError("Для этого графа происхождение значений не поддерживается.");
  const response = await queryBackendLineage(projectId, input, { signal });
  signal.throwIfAborted();
  if (response.status !== 200)
    throw new Error("Не удалось загрузить происхождение значения выбранной ревизии");
  const page = response.data;
  const pins = checkBackendProjectionPins(page, target, expected);
  if (
    page.projectId !== projectId ||
    page.revisionId !== (pins?.baseRevisionId ?? target.revisionId) ||
    (pins && page.semanticHash !== pins.effectiveSemanticHash) ||
    page.direction !== input.direction ||
    lineageRefKey(page.seed) !== lineageRefKey(input.seed)
  )
    throw new BackendReadError("Получен другой источник, значение или направление происхождения");
  return page;
}

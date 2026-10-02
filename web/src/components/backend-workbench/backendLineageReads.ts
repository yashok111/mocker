import { queryBackendLineage } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendLineagePage,
  BackendLineageValueRef,
  QueryBackendLineageRequest,
} from "@/api/generated/schemas";
export function lineageRefKey(ref: BackendLineageValueRef) {
  return JSON.stringify(
    ref.kind === "port"
      ? [ref.kind, ref.nodeId, ref.collection, ref.portKey]
      : ref.kind === "column"
        ? [ref.kind, ref.nodeId, ref.facetKey]
        : [ref.kind, ref.nodeId],
  );
}
export function lineageRefLabel(ref: BackendLineageValueRef) {
  return ref.kind === "port"
    ? `${ref.nodeId} · ${ref.collection} · ${ref.portKey}`
    : ref.kind === "column"
      ? `${ref.nodeId} · колонка · ${ref.facetKey}`
      : `${ref.nodeId} · поле API`;
}
export async function readLineagePage(
  projectId: string,
  input: QueryBackendLineageRequest,
  signal: AbortSignal,
): Promise<BackendLineagePage> {
  signal.throwIfAborted();
  const response = await queryBackendLineage(projectId, input, { signal });
  signal.throwIfAborted();
  if (response.status !== 200)
    throw new Error("Не удалось загрузить происхождение значения выбранной ревизии");
  const page = response.data;
  if (
    page.projectId !== projectId ||
    page.revisionId !== input.revisionId ||
    page.direction !== input.direction ||
    lineageRefKey(page.seed) !== lineageRefKey(input.seed)
  )
    throw new Error("Получен другой источник, значение или направление происхождения");
  return page;
}

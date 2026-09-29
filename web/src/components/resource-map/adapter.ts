import {
  getApiResourceMap,
  previewApiResourceMap,
} from "@/api/generated/resource-map/resource-map";
import type { ApiResourceMapPreviewRequest } from "@/api/generated/schemas";
import type { Preview, SavedMap } from "./model";

export async function getSavedResourceMap(id: number, signal?: AbortSignal): Promise<SavedMap> {
  const response = await getApiResourceMap(id, { signal });
  if (response.status !== 200)
    throw new Error("Не удалось загрузить карту сохранённого черновика.");
  return response.data;
}
export async function previewResourceMap(
  id: number,
  request: ApiResourceMapPreviewRequest,
  signal?: AbortSignal,
): Promise<Preview> {
  const response = await previewApiResourceMap(id, request, { signal });
  if (response.status !== 200) throw new Error("Не удалось проверить карту ресурсов.");
  return response.data;
}

import { useQuery } from "@tanstack/react-query";
import {
  getBackendDiagram,
  queryBackendDiagram,
} from "@/api/generated/backend-projects/backend-projects";
import type { BackendDiagramPin, QueryBackendDiagramRequest } from "@/api/generated/schemas";
import { useDatabaseCancellation } from "./backendDatabaseReads";
export function sameDiagramPin(a: BackendDiagramPin, b: BackendDiagramPin) {
  return a.id === b.id && a.version === b.version && a.contentHash === b.contentHash;
}
export function diagramReadKey(projectId: string, input: unknown) {
  return ["backend-diagram", projectId, JSON.stringify(input)];
}
export async function readDiagram(projectId: string, pin: BackendDiagramPin, signal: AbortSignal) {
  if (!Number.isSafeInteger(pin.version) || pin.version <= 0)
    throw new Error("Версия mapping не представима точно");
  const response = await getBackendDiagram(
    projectId,
    pin.id,
    pin.version,
    { hash: pin.contentHash },
    { signal },
  );
  signal.throwIfAborted();
  if (
    response.status !== 200 ||
    response.data.projectId !== projectId ||
    !sameDiagramPin(response.data.pin, pin)
  )
    throw new Error("Точный mapping недоступен; автоматическая замена отключена");
  return response.data;
}
export function useDiagramPage(
  projectId: string,
  input: QueryBackendDiagramRequest,
  enabled = true,
) {
  const key = diagramReadKey(projectId, input);
  useDatabaseCancellation(key);
  return useQuery({
    queryKey: key,
    enabled,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await queryBackendDiagram(projectId, input, { signal });
      signal.throwIfAborted();
      if (
        response.status !== 200 ||
        !sameDiagramPin(response.data.pin, input.pin) ||
        (input.level
          ? response.data.projection?.policy !== "architecture-v1" ||
            response.data.projection?.level !== input.level ||
            response.data.projection?.rootId !== input.rootId
          : response.data.projection !== undefined)
      )
        throw new Error("Ответ относится к другой C4 проекции");
      return response.data;
    },
  });
}

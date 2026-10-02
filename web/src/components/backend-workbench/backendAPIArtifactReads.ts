import { queryBackendAPIArtifacts } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendAPIArtifactItem,
  BackendAPIArtifactPage,
  BackendAPIArtifactSelector,
  BackendAPIPinCommand,
  BackendArtifactPin,
} from "@/api/generated/schemas";

export type APIArtifactScope = {
  projectId: string;
  revisionId: string;
  semanticHash: string;
  sourceSnapshotIds: string[];
  artifactPins: BackendArtifactPin[];
};
export function exactArtifactId(value: unknown): value is string {
  return (
    typeof value === "string" &&
    /^[1-9][0-9]{0,18}$/.test(value) &&
    BigInt(value) <= 9223372036854775807n
  );
}
export function safeLegacyId(value: unknown): number | undefined {
  if (typeof value === "string" && !exactArtifactId(value)) return undefined;
  if (typeof value !== "string" && typeof value !== "number") return undefined;
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}
export function strictAuthoredPointer(value: string): boolean {
  return (
    /^\/(?:[^~/]|~[01])*(?:\/(?:[^~/]|~[01])*){0,63}$/.test(value) &&
    new TextEncoder().encode(value).length <= 2048
  );
}
const pinVector = (pins: BackendArtifactPin[]) =>
  JSON.stringify(
    pins
      .map((pin) => [
        pin.kind,
        pin.id,
        pin.revisionId,
        "contentHash" in pin ? pin.contentHash : null,
      ])
      .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b))),
  );
const snapshots = (ids: string[]) => JSON.stringify([...ids].sort());
export function matchesArtifactScope(
  scope: APIArtifactScope,
  page: BackendAPIArtifactPage,
): boolean {
  return (
    page.revisionId === scope.revisionId &&
    page.semanticHash === scope.semanticHash &&
    snapshots(page.sourceSnapshotIds) === snapshots(scope.sourceSnapshotIds) &&
    pinVector(page.pins) === pinVector(scope.artifactPins)
  );
}
export async function readAPIArtifacts(
  scope: APIArtifactScope,
  sourceNodeId: string | undefined,
  signal: AbortSignal,
): Promise<BackendAPIArtifactPage> {
  let cursor = "";
  const seen = new Set<string>();
  const items: BackendAPIArtifactItem[] = [];
  let result: BackendAPIArtifactPage | undefined;
  do {
    signal.throwIfAborted();
    if (seen.has(cursor) || seen.size >= 201)
      throw new Error("Повтор или превышение числа страниц связей API");
    seen.add(cursor);
    const response = await queryBackendAPIArtifacts(
      scope.projectId,
      {
        revisionId: scope.revisionId,
        ...(sourceNodeId ? { sourceNodeId } : {}),
        limit: 100,
        cursor,
      },
      { signal },
    );
    signal.throwIfAborted();
    if (response.status !== 200) throw new Error("Не удалось прочитать связи API");
    result = response.data;
    if (!matchesArtifactScope(scope, result))
      throw new Error("Изменился контекст связей API; перечитайте источник");
    for (const item of result.items) {
      const ref = item.binding.ref;
      if (
        !exactArtifactId(ref.artifactId) ||
        !exactArtifactId(ref.revisionId) ||
        !result.pins.some(
          (pin) =>
            pin.kind === "api_design" &&
            pin.id === ref.artifactId &&
            pin.revisionId === ref.revisionId &&
            "contentHash" in pin &&
            pin.contentHash === ref.contentHash,
        )
      )
        throw new Error("Неверный контекст закреплённого API");
      if (sourceNodeId && item.binding.sourceNodeId !== sourceNodeId)
        throw new Error("Неверный контекст узла API");
    }
    items.push(...result.items);
    if (items.length > 200) throw new Error("Больше 200 связей API; требуется более узкий запрос");
    cursor = result.nextCursor;
  } while (cursor);
  return { ...result!, items, nextCursor: "" };
}
export function replaceArtifactBindings(
  items: BackendAPIArtifactItem[],
  artifactId: string,
  revisionId: string,
  sourceNodeId: string,
  selector: BackendAPIArtifactSelector | null,
  reason: string,
): BackendAPIPinCommand {
  const bindings = items
    .filter(
      (item) =>
        item.binding.ref.artifactId === artifactId && item.binding.sourceNodeId !== sourceNodeId,
    )
    .map((item) => ({
      sourceNodeId: item.binding.sourceNodeId,
      selector: item.binding.ref.selector,
    }));
  if (selector) bindings.push({ sourceNodeId, selector });
  return bindings.length
    ? { type: "set_api_pin", artifactId, revisionId, bindings, reason }
    : { type: "remove_api_pin", artifactId, reason };
}

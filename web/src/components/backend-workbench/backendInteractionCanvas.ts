import type {
  BackendInteractionPayload,
  BackendDiagramGap,
  BackendDiagramOrigin,
  BackendDiagramViewState,
} from "@/api/generated/schemas";
import { MAX_PARTICIPANT_OFFSET_X } from "../design-canvas/types";
import type { CanvasDocument } from "../design-canvas/types";
export type InteractionSidecar = {
  origin: BackendDiagramOrigin;
  branchPath: string[];
  gaps: BackendDiagramGap[];
  partialOrder: boolean;
};
export function interactionCanvas(
  payload: BackendInteractionPayload,
  gaps: BackendDiagramGap[],
  presentation?: Pick<BackendDiagramViewState, "positions" | "collapsedIds">,
) {
  const collapsed = new Set(presentation?.collapsedIds ?? []);
  const offsets = new Map(
    presentation?.positions.map((p) => [
      p.id,
      Math.max(-MAX_PARTICIPANT_OFFSET_X, Math.min(MAX_PARTICIPANT_OFFSET_X, p.x)),
    ]) ?? [],
  );
  let previousOffset = 0;
  const sidecar: Record<string, InteractionSidecar> = {};
  const all = [...payload.participants, ...payload.steps, ...payload.branches, ...payload.order];
  for (const row of all)
    sidecar[row.id] = {
      origin: structuredClone(row.origin),
      branchPath: "branchPath" in row ? [...row.branchPath] : [],
      gaps: gaps.filter((g) => g.subjectId === row.id),
      partialOrder: true,
    };
  const outgoing = new Map<string, string[]>();
  const indegree = new Map(payload.steps.map((s) => [s.id, 0]));
  for (const edge of payload.order) {
    outgoing.set(edge.from, [...(outgoing.get(edge.from) ?? []), edge.to]);
    indegree.set(edge.to, (indegree.get(edge.to) ?? 0) + 1);
  }
  const pending = [...indegree]
    .filter(([, n]) => n === 0)
    .map(([id]) => id)
    .sort();
  const ordered: string[] = [];
  let totalOrder = true;
  while (pending.length) {
    if (pending.length > 1) totalOrder = false;
    const id = pending.shift()!;
    ordered.push(id);
    for (const next of outgoing.get(id) ?? []) {
      const n = indegree.get(next)! - 1;
      indegree.set(next, n);
      if (n === 0) pending.push(next);
    }
    pending.sort();
  }
  const safe =
    totalOrder &&
    ordered.length === payload.steps.length &&
    payload.steps.every(
      (s) =>
        s.to &&
        s.branchPath.length === 0 &&
        s.kind !== "boundary" &&
        !collapsed.has(s.id) &&
        !collapsed.has(s.from) &&
        !collapsed.has(s.to),
    ) &&
    payload.participants.length + payload.steps.length <= 200;
  const byId = new Map(payload.steps.map((s) => [s.id, s]));
  const document: CanvasDocument = {
    formatVersion: 1,
    title: "Статические взаимодействия",
    participants: payload.participants
      .filter((p) => !collapsed.has(p.id))
      .slice(0, 200)
      .map((p) => {
        const offset = offsets.get(p.id) ?? 0;
        const offsetX = offset - previousOffset;
        previousOffset = offset;
        return {
          id: p.id,
          name: p.label,
          kind: "other",
          description: p.origin.kind === "authored" ? p.origin.reason : "Исходное утверждение",
          offsetX,
        };
      }),
    messages: safe
      ? ordered.map((id) => {
          const s = byId.get(id)!;
          sidecar[id]!.partialOrder = false;
          return {
            id: s.id,
            fromId: s.from,
            toId: s.to!,
            kind:
              s.kind === "response"
                ? "response"
                : s.kind === "send" || s.kind === "receive"
                  ? "event"
                  : "request",
            label: s.label,
            description: "Статическое утверждение; исполнение не проверено",
            ...(s.replyTo ? { replyToId: s.replyTo } : {}),
          };
        })
      : [],
    fragments: [],
    contracts: [],
  };
  return {
    document,
    sidecar,
    boundaries: safe
      ? []
      : payload.steps.map((s) => ({
          id: s.id,
          reason:
            collapsed.has(s.id) || collapsed.has(s.from) || (!!s.to && collapsed.has(s.to))
              ? "Скрыто настройкой вида; семантический порядок сохранён"
              : s.kind === "action"
                ? "Локальное действие участника"
                : !s.to
                  ? "Получатель не установлен"
                  : s.branchPath.length
                    ? "Ветвь показана отдельно; единый путь не утверждается"
                    : "Порядок исполнения не установлен",
        })),
    partialOrder: !safe,
  };
}

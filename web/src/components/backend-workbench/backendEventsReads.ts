import { useQuery } from "@tanstack/react-query";
import {
  queryBackendEvents,
  getBackendEvidence,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendEventsPage,
  QueryBackendEventsRequest,
  BackendEvidence,
  BackendEventsReferences,
  BackendEventsWitness,
  BackendEventsDispatch,
  BackendEventsScalar,
  BackendEventsTrigger,
  BackendEventsRelatedRoute,
  BackendEventsEmitContext,
} from "@/api/generated/schemas";
import { BackendEventsQueryLimitsValue } from "@/api/generated/schemas";
import { readDatabaseGraph, useDatabaseCancellation } from "./backendDatabaseReads";

export type EventsItem = BackendEventsPage["items"][number];
export type EventsPayload = {
  references: BackendEventsReferences;
  witness: BackendEventsWitness;
  dispatch: BackendEventsDispatch[];
  condition?: BackendEventsScalar;
  group?: BackendEventsScalar;
  trigger?: BackendEventsTrigger;
  related?: BackendEventsRelatedRoute[];
  emitContext?: BackendEventsEmitContext;
};
export function eventsPayload(item: EventsItem): EventsPayload {
  switch (item.kind) {
    case "route":
      return item.route;
    case "job":
      return item.job;
    case "service_call":
      return item.serviceCall;
    case "boundary":
      return item.boundary;
  }
}
export function eventsItemKey(item: EventsItem) {
  const refs = eventsPayload(item).references;
  return JSON.stringify([
    item.kind,
    refs.producerId,
    refs.messageId,
    refs.channelId,
    refs.consumerId,
    refs.emitsEdgeId,
    refs.deliveryEdgeId,
    refs.jobId,
    refs.callStepId,
    refs.callsEdgeId,
    refs.targetId,
  ]);
}
// Export a bounded subfragment. Kept values remain exact; omissions are explicit.
export function captureEventsFragment(item: EventsItem) {
  let bytes = 262144,
    members = 512,
    fragmentTruncated = false;
  function capture(value: unknown, depth = 0): unknown {
    if (depth > 10 || members-- <= 0) {
      fragmentTruncated = true;
      return undefined;
    }
    if (value === null || typeof value !== "object") {
      const size = new TextEncoder().encode(JSON.stringify(value) ?? "").length;
      if (size > bytes) {
        fragmentTruncated = true;
        return undefined;
      }
      bytes -= size;
      return value;
    }
    if (Array.isArray(value)) {
      const kept: unknown[] = [];
      for (const child of value) {
        const next = capture(child, depth + 1);
        if (next === undefined) {
          fragmentTruncated = true;
          break;
        }
        kept.push(next);
      }
      return kept;
    }
    const kept: Record<string, unknown> = {};
    for (const [key, child] of Object.entries(value)) {
      const next = capture(child, depth + 1);
      if (next !== undefined) kept[key] = next;
    }
    return kept;
  }
  const raw =
    item.kind === "route"
      ? item.route
      : item.kind === "job"
        ? item.job
        : item.kind === "service_call"
          ? item.serviceCall
          : item.boundary;
  const { references, witness, ...rest } = raw;
  const prioritized = { references, witness, ...rest };
  const name = item.kind === "service_call" ? "serviceCall" : item.kind;
  return {
    fragment: capture({ kind: item.kind, [name]: prioritized }),
    fragmentTruncated,
    fragmentLimits: { maxMembers: 512, maxValueBytes: 262144 },
  };
}
export async function readEventsPage(
  projectId: string,
  input: QueryBackendEventsRequest,
  semanticHash: string,
  signal: AbortSignal,
): Promise<BackendEventsPage> {
  signal.throwIfAborted();
  const response = await queryBackendEvents(projectId, input, { signal });
  signal.throwIfAborted();
  if (response.status !== 200) throw new Error("Не удалось загрузить события выбранной ревизии");
  const page = response.data;
  const selectors = page as { seedNodeId?: string; serviceId?: string };
  const expected = input as { seedNodeId?: string; serviceId?: string };
  if (
    page.projectId !== projectId ||
    page.revisionId !== input.revisionId ||
    page.semanticHash !== semanticHash ||
    page.view !== input.view ||
    page.policy !== "source-events-projection-v1" ||
    selectors.seedNodeId !== expected.seedNodeId ||
    selectors.serviceId !== expected.serviceId
  )
    throw new Error("Получен другой источник, hash, вид или фильтр событий");
  if (
    Object.entries(BackendEventsQueryLimitsValue).some(
      ([key, value]) => page.limits?.[key as keyof typeof page.limits] !== value,
    )
  )
    throw new Error("Получены другие границы чтения событий");
  return page;
}
export function useEventsPage(
  projectId: string,
  input: QueryBackendEventsRequest,
  semanticHash: string,
) {
  const key = ["backend-events", projectId, semanticHash, JSON.stringify(input)];
  useDatabaseCancellation(key);
  return useQuery({
    queryKey: key,
    queryFn: ({ signal }) => readEventsPage(projectId, input, semanticHash, signal),
    staleTime: Infinity,
    retry: false,
  });
}
export async function readEventsFieldSeeds(
  projectId: string,
  revisionId: string,
  refs: BackendEventsReferences,
  signal: AbortSignal,
) {
  const graph = await readDatabaseGraph(
    projectId,
    { revisionId, recordType: "nodes", kind: "event_field", parentId: refs.messageId },
    signal,
  );
  const nodes = graph.nodes.filter(
    (node) => node.kind === "event_field" && node.parentId === refs.messageId,
  );
  const addresses: { endpointId: string; routeId: string }[] = [],
    limitations: string[] = [];
  const sides = [
    { endpointId: refs.producerId, routeId: refs.emitsEdgeId, kind: "emits" },
    { endpointId: refs.consumerId, routeId: refs.deliveryEdgeId, kind: "delivered_to" },
  ];
  for (const side of sides) {
    if (!side.endpointId || !side.routeId) continue;
    const [owner, relationship] = await Promise.all([
      readDatabaseGraph(
        projectId,
        { revisionId, recordType: "nodes", id: side.endpointId },
        signal,
      ),
      readDatabaseGraph(projectId, { revisionId, recordType: "edges", id: side.routeId }, signal),
    ]);
    const endpoint = owner.nodes.find((node) => node.id === side.endpointId),
      edge = relationship.edges.find((edge) => edge.id === side.routeId);
    const valid =
      edge?.kind === side.kind &&
      (side.kind === "emits"
        ? endpoint?.kind === "flow_step" &&
          "stepKind" in endpoint.attributes &&
          endpoint.attributes.stepKind === "emit" &&
          edge.from === side.endpointId &&
          edge.to === refs.messageId &&
          "channelId" in edge.attributes &&
          edge.attributes.channelId === refs.channelId
        : endpoint?.kind === "consumer" &&
          edge.from === refs.channelId &&
          edge.to === side.endpointId &&
          "messageId" in edge.attributes &&
          edge.attributes.messageId === refs.messageId);
    if (valid) addresses.push({ endpointId: side.endpointId, routeId: side.routeId });
    else
      limitations.push(
        `Значение для ${side.endpointId} · ${side.routeId} недоступно: известный endpoint и точный маршрут сообщения не установлены.`,
      );
  }
  signal.throwIfAborted();
  return { nodes, addresses, limitations };
}

// One bounded evidence page per captured witness subject. Never chase global source proof.
export async function readEventsGapEvidence(
  projectId: string,
  revisionId: string,
  item: EventsItem,
  signal: AbortSignal,
) {
  const payload = eventsPayload(item);
  const witnesses = [
    payload.witness,
    ...payload.dispatch.map((d) => d.witness),
    ...(payload.related?.map((r) => r.witness) ?? []),
    ...(payload.emitContext?.controlWitness ? [payload.emitContext.controlWitness] : []),
  ];
  const subjects = [...new Set(witnesses.flatMap((w) => [...w.nodeIds, ...w.edgeIds]))];
  const expected = [...new Set(witnesses.flatMap((w) => w.evidenceIds))];
  const evidence = new Map<string, BackendEvidence>();
  let truncated = subjects.length > 256;
  const inspectedSubjects: string[] = [];
  for (const subjectId of subjects.slice(0, 256)) {
    signal.throwIfAborted();
    const result = await getBackendEvidence(
      projectId,
      revisionId,
      { subjectId, limit: 100 },
      { signal },
    );
    signal.throwIfAborted();
    if (result.status !== 200) throw new Error("Основания выбранного фрагмента недоступны");
    inspectedSubjects.push(subjectId);
    if (result.data.nextCursor) truncated = true;
    for (const record of result.data.items) {
      if (record.subjectId !== subjectId) throw new Error("Получены основания другого объекта");
      if (expected.includes(record.id) && evidence.size < 256) evidence.set(record.id, record);
    }
    if (evidence.size === 256) {
      truncated = true;
      break;
    }
  }
  return {
    evidence: [...evidence.values()],
    inspectedSubjects,
    missingEvidenceIds: expected.filter((id) => !evidence.has(id)),
    truncated,
  };
}

export function eventsReason(reason: string) {
  return (
    (
      {
        ownership_depth: "Достигнута граница глубины владельцев",
        edge_limit: "Превышен предел связей исходного снимка",
        item_limit: "Достигнут предел результатов",
        auxiliary_limit: "Достигнут предел вспомогательных объектов",
        witness_limit: "Достигнут предел свидетельства",
        unresolved: "Цель не установлена",
        stale: "Основания устарели",
        unknown: "Связь неизвестна",
      } as Record<string, string>
    )[reason] ?? reason
  );
}

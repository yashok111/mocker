import { useQuery } from "@tanstack/react-query";
import { useState, type Dispatch, type SetStateAction } from "react";
import { queryBackendFlow } from "@/api/generated/backend-projects/backend-projects";
import type { BackendFlowPage, QueryBackendFlowRequest } from "@/api/generated/schemas";
import { useDatabaseCancellation } from "./backendDatabaseReads";

export type BackendSourcePin = {
  revisionId?: string;
  entrypointId?: string;
  flowId?: string;
  dataNodeId?: string;
  recordId?: string;
  recordType?: "node" | "edge";
  datastoreId?: string;
  facetKey?: string;
};
export type FlowSelection = { type: "node" | "edge"; id: string };

// Synchronize URL selections before committing a render with the previous source.
export function usePinnedValue<Value>(
  identity: string | undefined,
  initialValue: Value,
): [Value, Dispatch<SetStateAction<Value>>] {
  const [state, setState] = useState({ identity, value: initialValue });
  if (state.identity !== identity) setState({ identity, value: initialValue });
  const value = state.identity === identity ? state.value : initialValue;
  return [
    value,
    (next) =>
      setState((previous) => ({
        identity,
        value:
          typeof next === "function" ? (next as (value: Value) => Value)(previous.value) : next,
      })),
  ];
}

export function parseBackendSourcePin(search: Record<string, unknown>): BackendSourcePin {
  const pin: BackendSourcePin = {};
  for (const field of [
    "revisionId",
    "entrypointId",
    "flowId",
    "dataNodeId",
    "recordId",
    "datastoreId",
    "facetKey",
  ] as const) {
    const value = search[field];
    if (typeof value === "string" && value.length > 0 && value.length <= 200) pin[field] = value;
  }
  if (search.recordType === "node" || search.recordType === "edge")
    pin.recordType = search.recordType;
  return pin;
}

export function flowPageOf<View extends BackendFlowPage["view"]>(
  page: BackendFlowPage | undefined,
  view: View,
): Extract<BackendFlowPage, { view: View }> | undefined {
  return page?.view === view ? (page as Extract<BackendFlowPage, { view: View }>) : undefined;
}

export async function readFlowPage(
  projectId: string,
  input: QueryBackendFlowRequest,
  signal: AbortSignal,
): Promise<BackendFlowPage> {
  signal.throwIfAborted();
  const response = await queryBackendFlow(projectId, input, { signal });
  signal.throwIfAborted();
  if (response.status !== 200) throw new Error("Не удалось загрузить Flow");
  if (response.data.projectId !== projectId || response.data.revisionId !== input.revisionId)
    throw new Error("Получена другая ревизия Flow; обновите выбранный источник");
  if (response.data.view !== input.view) throw new Error("Получено другое представление Flow");
  return response.data;
}

export function useFlowPage(projectId: string, input: QueryBackendFlowRequest) {
  const key = ["backend-flow", projectId, input.revisionId, JSON.stringify(input)];
  useDatabaseCancellation(key);
  return useQuery({
    queryKey: key,
    queryFn: ({ signal }) => readFlowPage(projectId, input, signal),
    staleTime: Infinity,
    retry: false,
  });
}

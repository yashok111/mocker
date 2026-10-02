import { useQuery } from "@tanstack/react-query";
import { useCallback, useState, type Dispatch, type SetStateAction } from "react";
import { queryBackendFlow } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendFlowPage,
  QueryBackendFlowRequest,
  BackendLineageValueRef,
} from "@/api/generated/schemas";
import { useDatabaseCancellation } from "./backendDatabaseReads";

export type BackendSourcePin = {
  viewId?: string;
  viewVersion?: number;
  revisionId?: string;
  entrypointId?: string;
  flowId?: string;
  dataNodeId?: string;
  recordId?: string;
  recordType?: "node" | "edge";
  datastoreId?: string;
  facetKey?: string;
};
export type FlowSelection = {
  type: "node" | "edge";
  id: string;
  valueRef?: BackendLineageValueRef;
};

// Synchronize URL selections before committing a render with the previous source.
export function usePinnedValue<Value>(
  identity: string | undefined,
  initialValue: Value,
): [Value, Dispatch<SetStateAction<Value>>] {
  const [state, setState] = useState({ identity, value: initialValue });
  if (state.identity !== identity) setState({ identity, value: initialValue });
  const value = state.identity === identity ? state.value : initialValue;
  const update = useCallback(
    (next: SetStateAction<Value>) =>
      setState((previous) => ({
        identity,
        value:
          typeof next === "function" ? (next as (value: Value) => Value)(previous.value) : next,
      })),
    [identity],
  );
  return [value, update];
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
  if (search.viewId !== undefined) {
    pin.viewId = typeof search.viewId === "string" ? search.viewId : "invalid";
    if (search.viewVersion !== undefined) {
      const raw = search.viewVersion;
      const value =
        typeof raw === "number"
          ? raw
          : typeof raw === "string" && /^[1-9][0-9]*$/.test(raw)
            ? Number(raw)
            : 0;
      pin.viewVersion = Number.isSafeInteger(value) && value > 0 ? value : 0;
    }
  } else if (search.viewVersion !== undefined) {
    pin.viewId = "invalid";
    pin.viewVersion = 0;
  }
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

import { useMutation, useQuery } from "@tanstack/react-query";
import type { UseMutationOptions, UseQueryOptions } from "@tanstack/react-query";
import {
  createDesignScenario as generatedCreateDesignScenario,
  getDesignScenario as generatedGetDesignScenario,
  getDesignScenarioDiff as generatedGetDesignScenarioDiff,
  getGetDesignScenarioDiffQueryKey,
  getGetDesignScenarioQueryKey,
  getListDesignScenariosQueryKey,
  listDesignScenarios as generatedListDesignScenarios,
  restoreDesignScenarioRevision as generatedRestoreDesignScenarioRevision,
  saveDesignScenarioDraft as generatedSaveDesignScenarioDraft,
} from "@/api/generated/design-scenarios/design-scenarios";
import type {
  CreateDesignScenarioRequest,
  DesignScenario as GeneratedDesignScenario,
  DesignScenarioContractUpdate as GeneratedDesignScenarioContractUpdate,
  DesignScenarioDetail as GeneratedDesignScenarioDetail,
  DesignScenarioDiagnostic as GeneratedDesignScenarioDiagnostic,
  DesignScenarioDiff as GeneratedDesignScenarioDiff,
  DesignScenarioRevision as GeneratedDesignScenarioRevision,
  DesignScenarioRevisionSummary as GeneratedDesignScenarioRevisionSummary,
  ListDesignScenariosResponse,
  RestoreDesignScenarioRevisionRequest,
  SaveDesignScenarioDraftRequest,
} from "@/api/generated/schemas";
import type { CanvasDocument } from "./types";

export type DesignScenarioSummary = GeneratedDesignScenario;
export type DesignScenarioRevisionSummary = GeneratedDesignScenarioRevisionSummary;
export type DesignScenarioRevision = Omit<GeneratedDesignScenarioRevision, "document"> & {
  document: CanvasDocument;
};
export type DesignScenarioDiagnostic = GeneratedDesignScenarioDiagnostic;
export type DesignScenarioContractUpdate = GeneratedDesignScenarioContractUpdate;
export type DesignScenarioDetail = Omit<GeneratedDesignScenarioDetail, "draft"> & {
  draft: DesignScenarioRevision;
};
export type DesignScenarioDiff = GeneratedDesignScenarioDiff;
export type DesignScenarioDiffChange = GeneratedDesignScenarioDiff["changes"][number];
export type DesignScenarioWrite = Omit<CreateDesignScenarioRequest, "document"> & {
  document: CanvasDocument;
};
export type SaveDesignScenarioDraft = Omit<SaveDesignScenarioDraftRequest, "document"> & {
  document: CanvasDocument;
};
export type RestoreDesignScenarioRevision = RestoreDesignScenarioRevisionRequest;

type ListResponse = Awaited<ReturnType<typeof generatedListDesignScenarios>>;
type ScenarioResponse<T> = T extends { data: GeneratedDesignScenarioDetail }
  ? Omit<T, "data"> & { data: DesignScenarioDetail }
  : T;
type CreateResponse = Extract<
  ScenarioResponse<Awaited<ReturnType<typeof generatedCreateDesignScenario>>>,
  { status: 201 }
>;
type DetailResponse = ScenarioResponse<Awaited<ReturnType<typeof generatedGetDesignScenario>>>;
type SaveResponse = Extract<
  ScenarioResponse<Awaited<ReturnType<typeof generatedSaveDesignScenarioDraft>>>,
  { status: 200 }
>;
type DiffResponse = Extract<
  Awaited<ReturnType<typeof generatedGetDesignScenarioDiff>>,
  { status: 200 }
>;
type RestoreResponse = Extract<
  ScenarioResponse<Awaited<ReturnType<typeof generatedRestoreDesignScenarioRevision>>>,
  { status: 200 }
>;

// OpenAPI represents arbitrary JSON assertion values as unknown. The server
// validates the schema and the JSON transport guarantees JSON values. Do not
// use the import validator here: saved documents may intentionally contain
// dangling operation bindings that the editor displays as diagnostics.
export function asDesignScenarioDetail(
  detail: GeneratedDesignScenarioDetail,
): DesignScenarioDetail {
  return detail as DesignScenarioDetail;
}

function scenarioResponse<T extends { status: number; data: unknown }>(
  response: T,
): ScenarioResponse<T> {
  if (response.status !== 200 && response.status !== 201) return response as ScenarioResponse<T>;
  return {
    ...response,
    data: asDesignScenarioDetail(response.data as GeneratedDesignScenarioDetail),
  } as ScenarioResponse<T>;
}

export const designScenarioKeys = {
  all: getListDesignScenariosQueryKey(),
  detail: (id: number) => getGetDesignScenarioQueryKey(id),
  revision: (id: number, revisionId: number) =>
    [`/api/design-scenarios/${id}/revisions/${revisionId}`] as const,
  diff: (id: number, from: number, to: number) =>
    getGetDesignScenarioDiffQueryKey(id, { from, to }),
};

export const listDesignScenarios = generatedListDesignScenarios;

export function useListDesignScenarios(options: Partial<UseQueryOptions<ListResponse>> = {}) {
  return useQuery({
    queryKey: designScenarioKeys.all,
    queryFn: () => generatedListDesignScenarios(),
    retry: false,
    ...options,
  });
}

export function createDesignScenario(data: DesignScenarioWrite): Promise<CreateResponse> {
  return generatedCreateDesignScenario(data as CreateDesignScenarioRequest).then(
    scenarioResponse,
  ) as Promise<CreateResponse>;
}

export function useCreateDesignScenario(
  options: UseMutationOptions<CreateResponse, unknown, DesignScenarioWrite> = {},
) {
  return useMutation({ mutationFn: createDesignScenario, ...options });
}

export function getDesignScenario(id: number): Promise<DetailResponse> {
  return generatedGetDesignScenario(id).then(scenarioResponse);
}

export function useGetDesignScenario(
  id: number,
  options: Partial<UseQueryOptions<DetailResponse>> = {},
) {
  return useQuery({
    queryKey: designScenarioKeys.detail(id),
    queryFn: () => getDesignScenario(id),
    retry: false,
    ...options,
  });
}

export function saveDesignScenarioDraft(
  id: number,
  data: SaveDesignScenarioDraft,
): Promise<SaveResponse> {
  return generatedSaveDesignScenarioDraft(id, data as SaveDesignScenarioDraftRequest).then(
    scenarioResponse,
  ) as Promise<SaveResponse>;
}

export function useSaveDesignScenarioDraft(
  options: UseMutationOptions<
    SaveResponse,
    unknown,
    { id: number; data: SaveDesignScenarioDraft }
  > = {},
) {
  return useMutation({
    mutationFn: ({ id, data }) => saveDesignScenarioDraft(id, data),
    ...options,
  });
}

export function getDesignScenarioDiff(id: number, from: number, to: number): Promise<DiffResponse> {
  return generatedGetDesignScenarioDiff(id, { from, to }) as Promise<DiffResponse>;
}

export function useGetDesignScenarioDiff(id: number, from: number, to: number, enabled: boolean) {
  return useQuery({
    queryKey: designScenarioKeys.diff(id, from, to),
    queryFn: () => getDesignScenarioDiff(id, from, to),
    enabled,
    retry: false,
  });
}

export function restoreDesignScenarioRevision(
  id: number,
  data: RestoreDesignScenarioRevision,
): Promise<RestoreResponse> {
  return generatedRestoreDesignScenarioRevision(id, data).then(
    scenarioResponse,
  ) as Promise<RestoreResponse>;
}

export function useRestoreDesignScenarioRevision(
  options: UseMutationOptions<
    RestoreResponse,
    unknown,
    { id: number; data: RestoreDesignScenarioRevision }
  > = {},
) {
  return useMutation({
    mutationFn: ({ id, data }) => restoreDesignScenarioRevision(id, data),
    ...options,
  });
}

export type { ListDesignScenariosResponse };

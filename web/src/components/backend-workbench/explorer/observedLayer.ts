import { useQuery } from "@tanstack/react-query";
import {
  getBackendAnalysisResults,
  getBackendObservationVersion,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendDiagramObserved,
  BackendDiagramVersion,
  GetBackendAnalysisResultsParams,
} from "@/api/generated/schemas";
import { ok } from "./catalogs";
import { sameDiagramPin } from "../backendDiagramReads";
export type ObservedSelection = {
  jobId: string;
  resultVersion: number;
  recordId: string;
  section: GetBackendAnalysisResultsParams["section"];
  cursor: string;
};
export function isObserved(value: unknown): value is BackendDiagramObserved {
  return (
    !!value &&
    typeof value === "object" &&
    "policy" in value &&
    value.policy === "backend-observed-sequence-v1" &&
    "diagramScope" in value &&
    "elements" in value
  );
}
export function useObservedLayer(
  projectId: string,
  diagram: BackendDiagramVersion,
  encoded?: string,
) {
  return useQuery({
    queryKey: ["workbench-observed-layer", projectId, diagram.pin, encoded],
    enabled: !!encoded,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const selected: ObservedSelection = JSON.parse(encoded!);
      if (
        !Number.isSafeInteger(selected.resultVersion) ||
        selected.resultVersion < 1 ||
        !["changes", "findings", "checks", "gaps", "witnesses"].includes(selected.section) ||
        typeof selected.cursor !== "string" ||
        selected.cursor.length > 4096
      )
        throw new Error("Некорректный выбор наблюдений");
      const page = ok(
        await getBackendAnalysisResults(
          projectId,
          selected.jobId,
          {
            resultVersion: selected.resultVersion,
            section: selected.section,
            cursor: selected.cursor,
            limit: 30,
          },
          { signal },
        ),
      );
      const report = page.items.find((i) => i.id === selected.recordId)?.detail;
      if (
        page.manifest.jobId !== selected.jobId ||
        page.manifest.resultVersion !== selected.resultVersion ||
        !isObserved(report) ||
        !sameDiagramPin(report.diagramScope.pin, diagram.pin) ||
        report.diagramScope.targetHash !== diagram.targetHash
      )
        throw new Error("Наблюдения относятся к другой схеме или версии");
      const contexts = await Promise.all(
        report.pins.map(async (pin) => {
          const version = ok(
            await getBackendObservationVersion(projectId, pin.observationSetId, pin.version, {
              signal,
            }),
          );
          if (
            version.setId !== pin.observationSetId ||
            version.version !== pin.version ||
            version.contentHash !== pin.contentHash
          )
            throw new Error("Версия наблюдений недоступна");
          return { name: version.name, context: version.context };
        }),
      );
      const name =
        contexts.length === 1 ? contexts[0]!.name : `Наборы наблюдений · ${contexts.length}`;
      const context = contexts;
      signal.throwIfAborted();
      return { report, name, context };
    },
  });
}

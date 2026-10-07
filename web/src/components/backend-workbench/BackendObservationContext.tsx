import { createContext, useContext, useState, useRef, useEffect, type ReactNode } from "react";
import { Button, Text } from "@mantine/core";
import type {
  BackendDiagramScopeInput,
  BackendDiagramScope,
  BackendDiagramVersion,
  BackendDiagramObserved,
  BackendDiagramPin,
} from "@/api/generated/schemas";
import { resolveBackendDiagramScope } from "@/api/generated/backend-projects/backend-projects";
import { focusWorkspaceRegion } from "./BackendWorkspaceNavigation";
import { observationRequestGate } from "./backendObservationReads";
export type SavedObservedOverlay = {
  jobId: string;
  resultVersion: number;
  analysisInputHash: string;
  report: BackendDiagramObserved;
};
const Context = createContext<
  | {
      scope?: BackendDiagramScope;
      setScope: (s: BackendDiagramScope) => void;
      overlay?: SavedObservedOverlay;
      setOverlay: (v: SavedObservedOverlay) => void;
    }
  | undefined
>(undefined);
export function BackendObservationProvider({ children }: { children: ReactNode }) {
  const [scope, setScope] = useState<BackendDiagramScope>();
  const [overlay, setOverlay] = useState<SavedObservedOverlay>();
  return (
    <Context.Provider value={{ scope, setScope, overlay, setOverlay }}>{children}</Context.Provider>
  );
}
export const useObservationScope = () => useContext(Context);
export function BackendObservationSelect({
  projectId,
  diagram,
  input,
  disabled,
}: {
  projectId: string;
  diagram: BackendDiagramVersion;
  input: BackendDiagramScopeInput;
  disabled?: boolean;
}) {
  const context = useObservationScope();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // The same request gate BackendObservations uses: a new selection or an
  // unmount invalidates an in-flight resolve, so a late reply never sets
  // the scope. The gate object lives for the component's whole life, so
  // the cleanup closes over it instead of reading the ref there (a bare
  // counter in the ref tripped react-hooks/exhaustive-deps).
  const gate = useRef(observationRequestGate());
  useEffect(() => {
    const requests = gate.current;
    requests.begin();
    setBusy(false);
    return () => {
      requests.begin();
    };
  }, [projectId, input]);
  return (
    <>
      <Button
        disabled={disabled || busy || !context}
        onClick={async () => {
          const token = gate.current.begin();
          setBusy(true);
          try {
            const r = await resolveBackendDiagramScope(projectId, structuredClone(input));
            if (!gate.current.current(token)) return;
            if (r.status !== 200 || r.data.targetHash !== diagram.targetHash || r.data.truncated)
              throw new Error("Точная область недоступна");
            context?.setScope(r.data);
            focusWorkspaceRegion('[data-testid="backend-observations"] h2');
          } catch {
            if (gate.current.current(token)) setError("Не удалось прочитать точную область");
          } finally {
            if (gate.current.current(token)) setBusy(false);
          }
        }}
      >
        Наблюдения выбранного элемента
      </Button>
      {context?.overlay &&
        context.overlay.report.diagramScope.pin.id === diagram.pin.id &&
        context.overlay.report.diagramScope.pin.version === diagram.pin.version &&
        context.overlay.report.diagramScope.pin.contentHash === diagram.pin.contentHash && (
          <Text>
            Saved observed report {context.overlay.jobId} v{context.overlay.resultVersion}:{" "}
            {
              context.overlay.report.elements.filter((e) =>
                e.selectors.some((sel) =>
                  input.selectors.some((s) => JSON.stringify(s) === JSON.stringify(sel)),
                ),
              ).length
            }{" "}
            observations of this exact selection. Missing membership is unknown; alternatives remain
            unverified.
          </Text>
        )}
      {error && <Text role="alert">{error}</Text>}
    </>
  );
}

export function useSavedObservedOverlay(pin: BackendDiagramPin) {
  const overlay = useObservationScope()?.overlay;
  const saved = overlay?.report.diagramScope.pin;
  return saved &&
    saved.id === pin.id &&
    saved.version === pin.version &&
    saved.contentHash === pin.contentHash
    ? overlay
    : undefined;
}

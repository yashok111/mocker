import { createContext, useContext, useState, useRef, useEffect, type ReactNode } from "react";
import { Button, Text } from "@mantine/core";
import type {
  BackendDiagramScopeInput,
  BackendDiagramScope,
  BackendDiagramVersion,
} from "@/api/generated/schemas";
import { resolveBackendDiagramScope } from "@/api/generated/backend-projects/backend-projects";
import { focusWorkspaceRegion } from "./BackendWorkspaceNavigation";
const Context = createContext<
  { scope?: BackendDiagramScope; setScope: (s: BackendDiagramScope) => void } | undefined
>(undefined);
export function BackendObservationProvider({ children }: { children: ReactNode }) {
  const [scope, setScope] = useState<BackendDiagramScope>();
  return <Context.Provider value={{ scope, setScope }}>{children}</Context.Provider>;
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
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    setBusy(false);
    return () => {
      generation.current++;
    };
  }, [projectId, input]);
  return (
    <>
      <Button
        disabled={disabled || busy || !context}
        onClick={async () => {
          const token = ++generation.current;
          setBusy(true);
          try {
            const r = await resolveBackendDiagramScope(projectId, structuredClone(input));
            if (token !== generation.current) return;
            if (r.status !== 200 || r.data.targetHash !== diagram.targetHash || r.data.truncated)
              throw new Error("Точная область недоступна");
            context?.setScope(r.data);
            focusWorkspaceRegion('[data-testid="backend-observations"] h2');
          } catch {
            if (token === generation.current) setError("Не удалось прочитать точную область");
          } finally {
            if (token === generation.current) setBusy(false);
          }
        }}
      >
        Наблюдения выбранного элемента
      </Button>
      {error && <Text role="alert">{error}</Text>}
    </>
  );
}

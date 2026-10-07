import { BackendObservationSelect } from "./BackendObservationContext";
import { createContext, useContext, useState, type ReactNode } from "react";
import { Alert, Button, Stack, Text } from "@mantine/core";
import type { BackendDiagramScopeInput, BackendDiagramVersion } from "@/api/generated/schemas";
import { readDiagramReplayPreparation, type ReplayPreparation } from "./backendDiagramReplay";
import { focusWorkspaceRegion } from "./BackendWorkspaceNavigation";
const Context = createContext<
  | { preparation?: ReplayPreparation; setPreparation: (value: ReplayPreparation) => void }
  | undefined
>(undefined);
export function BackendDiagramReplayProvider({ children }: { children: ReactNode }) {
  const [preparation, setPreparation] = useState<ReplayPreparation>();
  return <Context.Provider value={{ preparation, setPreparation }}>{children}</Context.Provider>;
}
export const useDiagramReplayPreparation = () => useContext(Context);
export function BackendDiagramReplayPrepare({
  projectId,
  diagram,
  input,
  disabled = false,
  prepare = readDiagramReplayPreparation,
}: {
  projectId: string;
  diagram: BackendDiagramVersion;
  input: BackendDiagramScopeInput;
  disabled?: boolean;
  prepare?: typeof readDiagramReplayPreparation;
}) {
  const context = useDiagramReplayPreparation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <Stack>
      <BackendObservationSelect
        projectId={projectId}
        diagram={diagram}
        input={input}
        disabled={disabled}
      />
      <Button
        disabled={disabled || busy || !context}
        onClick={async () => {
          setBusy(true);
          setError("");
          try {
            const value = await prepare(
              projectId,
              structuredClone(diagram),
              structuredClone(input),
            );
            context?.setPreparation(value);
            focusWorkspaceRegion('[data-testid="backend-replay"] h2');
          } catch (e) {
            setError(String(e));
          } finally {
            setBusy(false);
          }
        }}
      >
        Подготовить поддерживаемый replay выбранного элемента
      </Button>
      <Text size="xs">
        Только чтение точной области и POST /orders refs. Профиль, согласие, сохранение и Start
        выбираются отдельно.
      </Text>
      {error && <Alert color="red">{error}</Alert>}
    </Stack>
  );
}

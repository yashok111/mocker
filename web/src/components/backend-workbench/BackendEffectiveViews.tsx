import { useRef } from "react";
import { Alert, Button, Code, Group, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type { BackendEffectiveGraphPins, BackendReadTarget } from "@/api/generated/schemas";
import { BackendFlow } from "./BackendFlow";
import { BackendDatabase } from "./BackendDatabase";
import { BackendEvents } from "./BackendEvents";
import { BackendAPIArtifacts } from "./BackendAPIArtifacts";
import { BackendArtifactProjections } from "./BackendArtifactProjections";
import { BackendExactGraph } from "./BackendExactGraph";
import { backendReadQueryKey, readBackendCoverage } from "./backendGraphReads";
import { backendReadTargetKey, checkBackendReadPins, BackendReadError } from "./backendReadTargets";
import { usePinnedValue, type BackendSourcePin } from "./backendFlowReads";
import { LoadState } from "./BackendReadUI";
type Props = {
  projectId: string;
  target: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  initialPin?: BackendSourcePin;
  savedKind?: "flow" | "database";
  onBaseNavigate?: (revisionId: string) => void;
};
export function BackendEffectiveViews(props: Props) {
  let key: string;
  try {
    key = backendReadTargetKey(props.target);
    if (!props.target.changeProposal)
      throw new BackendReadError("Выберите точный черновик полного предложения.");
  } catch (error) {
    return (
      <Alert color="red" role="alert">
        {error instanceof Error ? error.message : "Некорректный граф."}
      </Alert>
    );
  }
  return <EffectiveViews key={`${props.projectId}:${key}`} {...props} />;
}
function EffectiveViews({
  projectId,
  target,
  pins: expected,
  initialPin,
  savedKind,
  onBaseNavigate,
}: Props) {
  const [pin, setPin] = usePinnedValue<BackendSourcePin>(
    JSON.stringify(initialPin ?? null),
    initialPin ?? {},
  );
  const flowRef = useRef<HTMLDivElement>(null),
    databaseRef = useRef<HTMLDivElement>(null);
  const query = useQuery({
    queryKey: backendReadQueryKey("coverage", projectId, target, expected?.targetHash),
    queryFn: ({ signal }) => readBackendCoverage(projectId, target, signal, expected),
    select: (coverage) => {
      checkBackendReadPins(coverage, target, expected);
      if (!("pins" in coverage))
        throw new BackendReadError("Отсутствует точный контекст предложения.");
      return coverage;
    },
    retry: false,
    staleTime: Infinity,
  });
  const pins = query.data?.pins;
  const navigateFlow = (next: BackendSourcePin) => {
    const { revisionId: _, ...focus } = next;
    setPin(focus);
    flowRef.current?.scrollIntoView({ block: "start" });
  };
  const flow = (
    <div ref={flowRef}>
      <BackendFlow
        projectId={projectId}
        target={target}
        pins={pins}
        pin={pin}
        onPinChange={setPin}
        onDatabaseNavigate={() => databaseRef.current?.scrollIntoView({ block: "start" })}
      />
    </div>
  );
  const database = (
    <div ref={databaseRef}>
      <BackendDatabase
        projectId={projectId}
        target={target}
        pins={pins}
        pin={pin}
        onFlowNavigate={navigateFlow}
      />
    </div>
  );
  return (
    <Stack aria-label="Представления полного предложения" gap="lg">
      <LoadState query={query} label="контекста предложения" />
      {pins && (
        <>
          <Group justify="space-between">
            <Title order={2}>Предлагаемая модель</Title>
            {onBaseNavigate && (
              <Button variant="default" onClick={() => onBaseNavigate(pins.baseRevisionId)}>
                Открыть базовый источник
              </Button>
            )}
          </Group>
          <Text size="sm">
            Изменённые поля отражают намерение. Свидетельства исходников относятся к базовой
            ревизии.
          </Text>
          <Code style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>
            Черновик: {target.changeProposal?.proposalRevisionId} · база: {pins.baseRevisionId}
          </Code>
          {savedKind === "database" ? (
            <>
              {database}
              {flow}
            </>
          ) : (
            <>
              {flow}
              {database}
            </>
          )}
          <BackendEvents
            projectId={projectId}
            target={target}
            pins={pins}
            semanticHash={pins.effectiveSemanticHash}
            onFlowNavigate={navigateFlow}
          />
          <BackendAPIArtifacts projectId={projectId} target={target} pins={pins} />
          <BackendArtifactProjections projectId={projectId} target={target} pins={pins} />
          <BackendExactGraph
            projectId={projectId}
            target={target}
            pins={pins}
            focusTarget={
              pin.recordId && pin.recordType
                ? { recordType: pin.recordType, id: pin.recordId }
                : undefined
            }
          />
        </>
      )}
    </Stack>
  );
}

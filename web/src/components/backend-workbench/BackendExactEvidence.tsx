import { useState } from "react";
import { Alert, Badge, Button, Code, Group, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { getBackendRevision } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendEffectiveGraphPins,
  BackendLegacyProofBasis,
  BackendReadTarget,
} from "@/api/generated/schemas";
import { backendReadQueryKey, readBackendEvidence } from "./backendGraphReads";
import { LoadState, Pages } from "./BackendReadUI";
import { BackendReadError } from "./backendReadTargets";
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
export function BackendExactEvidence({
  projectId,
  target,
  pins,
  subjectId,
  evidenceId,
}: {
  projectId: string;
  target: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  subjectId?: string;
  evidenceId?: string;
}) {
  const [cursors, setCursors] = useState([""]);
  const [historical, setHistorical] = useState<BackendLegacyProofBasis>();
  const input = {
    ...(evidenceId ? { evidenceId } : { subjectId, cursor: cursors.at(-1) ?? "" }),
    limit: 100,
  };
  const query = useQuery({
    queryKey: backendReadQueryKey("evidence", projectId, target, { ...input, subjectId }),
    queryFn: async ({ signal }) => {
      const page = await readBackendEvidence(projectId, target, input, signal, pins);
      if (subjectId && page.items.some((item) => item.subjectId !== subjectId))
        throw new BackendReadError("Получены основания другого объекта.");
      return page;
    },
    retry: false,
    staleTime: Infinity,
  });
  const page = query.data;
  const source = page && "source" in page ? page.source : undefined;
  return (
    <Stack gap="sm" aria-label="Основания объекта">
      <Title order={4}>Основания</Title>
      {target.changeProposal && (
        <Alert color="blue">
          Основания базового источника. Они не подтверждают изменённые поля предложения.
        </Alert>
      )}
      <LoadState query={query} label="оснований" />
      {page?.items.length === 0 && (
        <Text size="sm" c="dimmed">
          Оснований нет
        </Text>
      )}
      {page?.items.map((evidence) => {
        const basis = source?.legacyProofBases.find((item) => item.evidenceId === evidence.id);
        return (
          <div key={evidence.id}>
            <Group>
              <Badge color={evidence.status === "unresolved" ? "yellow" : "gray"}>
                {evidence.method} · {evidence.status}
              </Badge>
              <Text size="sm" style={wrap}>
                {evidence.source.file}
                {evidence.source.startLine
                  ? `:${evidence.source.startLine}–${evidence.source.endLine}`
                  : ""}
              </Text>
            </Group>
            <Text size="sm" style={wrap}>
              {evidence.explanation}
            </Text>
            {evidence.propertyPath && (
              <Text size="xs" style={wrap}>
                Свойство: {evidence.propertyPath}
              </Text>
            )}
            <Text size="xs" style={wrap}>
              Репозиторий: {evidence.source.repositoryId} · Снимок: {evidence.source.snapshotId}
            </Text>
            <Text size="xs" style={wrap}>
              SHA-256: {evidence.source.contentHash}
            </Text>
            {evidence.snippet && (
              <Code block style={wrap}>
                {evidence.snippet}
              </Code>
            )}
            {basis && (
              <Stack gap="xs" mt="xs">
                <Text fw={500}>
                  {basis.support === "historical_metadata"
                    ? "Историческое свидетельство о метаданных"
                    : basis.support === "legacy_record"
                      ? "Историческое свидетельство об объекте"
                      : "Основание из source5"}
                </Text>
                {basis.support === "historical_metadata" && (
                  <Text size="sm">
                    legacy_metadata_only: подтверждение семантического поля отсутствует.
                  </Text>
                )}
                <Text size="xs" style={wrap}>
                  Исходная ревизия: {basis.sourceRevisionId} · {basis.sourceSemanticHash}
                </Text>
                <Code block style={wrap}>
                  {JSON.stringify(basis, null, 2)}
                </Code>
                <Button variant="subtle" onClick={() => setHistorical(basis)}>
                  Открыть исходное свидетельство {evidence.id}
                </Button>
              </Stack>
            )}
          </div>
        );
      })}
      <Pages
        label="основания"
        cursors={cursors}
        next={page?.nextCursor}
        busy={query.isFetching}
        setCursors={setCursors}
      />
      {historical && (
        <Stack>
          <Button variant="subtle" onClick={() => setHistorical(undefined)}>
            Закрыть историческое свидетельство
          </Button>
          <HistoricalEvidence key={historical.basisHash} projectId={projectId} basis={historical} />
        </Stack>
      )}
    </Stack>
  );
}

function HistoricalEvidence({
  projectId,
  basis,
}: {
  projectId: string;
  basis: BackendLegacyProofBasis;
}) {
  const query = useQuery({
    queryKey: backendReadQueryKey(
      "historical-proof",
      projectId,
      { revisionId: basis.sourceRevisionId },
      basis.basisHash,
    ),
    queryFn: async ({ signal }) => {
      const response = await getBackendRevision(projectId, basis.sourceRevisionId, { signal });
      signal.throwIfAborted();
      if (
        response.status !== 200 ||
        response.data.id !== basis.sourceRevisionId ||
        response.data.projectId !== projectId ||
        response.data.schemaVersion !== "5" ||
        response.data.semanticHash !== basis.sourceSemanticHash
      )
        throw new BackendReadError(
          "Историческая ревизия не соответствует сохранённому свидетельству.",
        );
      return response.data;
    },
    retry: false,
    staleTime: Infinity,
  });
  return (
    <Stack>
      <LoadState query={query} label="исторической ревизии" />
      {query.data && (
        <BackendExactEvidence
          projectId={projectId}
          target={{ revisionId: basis.sourceRevisionId }}
          subjectId={basis.recordId}
          evidenceId={basis.evidenceId}
        />
      )}
    </Stack>
  );
}

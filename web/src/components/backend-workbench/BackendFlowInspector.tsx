import {
  projectionTarget,
  projectionKey,
  readProjectionNode,
  type ProjectionReadProps,
} from "./backendEffectiveProjectionReads";
import { BackendProjectionSources } from "./BackendProjectionSources";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
import { BackendValueSeeds } from "./BackendLineageActions";
import { BackendAPIFields } from "./BackendAPIFields";
import { useEffect, useRef, useState } from "react";
import { Badge, Button, Code, Group, Paper, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { LoadState } from "./BackendGraphInventory";
import { DatabaseEvidence } from "./BackendDatabaseInspector";
import {
  databaseButtonStyles,
  databaseStatus,
  databaseWrap,
  readDatabaseGraph,
  useDatabaseCancellation,
} from "./backendDatabaseReads";
import type { FlowSelection } from "./backendFlowReads";
import { BackendAPIArtifacts } from "./BackendAPIArtifacts";
import { lineageRefKey, lineageRefLabel } from "./backendLineageReads";

export function BackendFlowInspector({
  projectId,
  revisionId,
  target: explicitTarget,
  pins,
  selection,
  onSelect,
  onClose,
  selectedValue,
}: ProjectionReadProps & {
  projectId: string;
  selection: FlowSelection;
  onSelect: (selection: FlowSelection) => void;
  selectedValue?: BackendLineageValueRef;
  onClose: () => void;
}) {
  const target = projectionTarget({ revisionId, target: explicitTarget, pins });
  const baseRevisionId = pins?.baseRevisionId ?? revisionId ?? "";
  const [valueSelection, setValueSelection] = useState<BackendLineageValueRef | undefined>(
    selectedValue,
  );
  const selectValue = (ref: BackendLineageValueRef) => {
    setValueSelection(ref);
    if (
      ref.nodeId !== selection.id ||
      (ref.kind === "event_field" &&
        (!selectedValue || lineageRefKey(ref) !== lineageRefKey(selectedValue)))
    )
      onSelect({ type: "node", id: ref.nodeId, valueRef: ref });
  };
  const key = [
    "backend-flow-inspector",
    projectId,
    projectionKey(target, pins),
    selection.type,
    selection.id,
  ];
  useDatabaseCancellation(key);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, [selection.id, selection.type]);
  const query = useQuery({
    queryKey: [...key, "record"],
    staleTime: Infinity,
    retry: false,
    queryFn: async ({ signal }) => {
      if (selection.type === "edge") {
        const records = await readDatabaseGraph(
          projectId,
          { ...target, recordType: "edges", id: selection.id },
          signal,
          pins,
        );
        if (!records.edges[0]) throw new Error("Переход отсутствует в выбранной ревизии");
        return records.edges[0];
      }
      return readProjectionNode(projectId, target, selection.id, signal, pins);
    },
  });
  const links = useQuery({
    queryKey: [...key, "links"],
    staleTime: Infinity,
    retry: false,
    enabled: selection.type === "node",
    queryFn: async ({ signal }) => {
      const pages = await Promise.all([
        readDatabaseGraph(
          projectId,
          { ...target, recordType: "edges", from: selection.id },
          signal,
          pins,
        ),
        readDatabaseGraph(
          projectId,
          { ...target, recordType: "edges", to: selection.id },
          signal,
          pins,
        ),
      ]);
      return [
        ...new Map(pages.flatMap((page) => page.edges).map((edge) => [edge.id, edge])).values(),
      ];
    },
  });
  const record = query.data;
  return (
    <Paper
      withBorder
      p="md"
      component="section"
      aria-label="Инспектор Flow"
      style={{ minWidth: 0 }}
    >
      <Stack>
        <Group justify="space-between">
          <Title order={3} tabIndex={-1} ref={heading} style={databaseWrap}>
            {record && "name" in record ? record.name : "Инспектор Flow"}
          </Title>
          <Button variant="default" onClick={onClose}>
            Закрыть инспектор Flow
          </Button>
        </Group>
        <Text size="xs" style={databaseWrap}>
          Источник: {revisionId} · объект: {selection.id}
        </Text>
        <LoadState query={query} label="объекта Flow" />
        {(pins || target.changeProposal) && (
          <BackendProjectionSources
            projectId={projectId}
            target={target}
            pins={pins}
            recordType={selection.type}
            id={selection.id}
          />
        )}
        {record && (
          <>
            <Badge>{record.kind}</Badge>
            {record.freshness && (
              <Stack gap="xs">
                <Badge>{databaseStatus(record.freshness.status)}</Badge>
                {record.freshness.reasons.map((reason) => (
                  <Text key={reason} size="sm" style={databaseWrap}>
                    {reason}
                  </Text>
                ))}
              </Stack>
            )}
            {"analysisStatus" in record.attributes && (
              <Text>Анализ: {databaseStatus(String(record.attributes.analysisStatus))}</Text>
            )}
            {"nativeText" in record.attributes && (
              <>
                <Text fw={600}>Исходный текст шага</Text>
                {record.attributes.nativeText === null ? (
                  <Text>{String(record.attributes.nativeReason ?? "Текст неизвестен")}</Text>
                ) : (
                  <Code block style={databaseWrap}>
                    {String(record.attributes.nativeText)}
                  </Code>
                )}
              </>
            )}
            {"nativeDefinition" in record.attributes && (
              <>
                <Text fw={600}>Исходное определение запроса</Text>
                <Code block style={databaseWrap}>
                  {JSON.stringify(record.attributes.nativeDefinition, null, 2)}
                </Code>
              </>
            )}
            {"transactionContext" in record.attributes && (
              <>
                <Text fw={600}>Локальный контекст транзакции</Text>
                <Code block style={databaseWrap}>
                  {JSON.stringify(record.attributes.transactionContext, null, 2)}
                </Code>
                {record.attributes.transactionContext.status === "known" && (
                  <Button
                    variant="subtle"
                    onClick={() => {
                      if (
                        "transactionContext" in record.attributes &&
                        record.attributes.transactionContext.status === "known"
                      )
                        onSelect({
                          type: "node",
                          id: record.attributes.transactionContext.transactionId,
                        });
                    }}
                  >
                    Открыть исходную транзакцию
                  </Button>
                )}
                <Text size="sm">
                  Группировка относится к этому flow и соединению; атомарность между вызовами не
                  установлена.
                </Text>
              </>
            )}
            {"name" in record && (
              <BackendValueSeeds
                projectId={projectId}
                revisionId={baseRevisionId}
                target={target}
                pins={pins}
                node={record}
                selected={valueSelection}
                onValueSelect={selectValue}
              />
            )}
            {record.kind === "http_operation" && (
              <BackendAPIArtifacts
                projectId={projectId}
                revisionId={baseRevisionId}
                target={target}
                pins={pins}
                sourceNodeId={record.id}
                sourceKind="http_operation"
              />
            )}
            {record.kind === "api_field" && (
              <BackendAPIArtifacts
                projectId={projectId}
                revisionId={baseRevisionId}
                target={target}
                pins={pins}
                sourceNodeId={record.id}
                sourceKind="api_field"
              />
            )}
            {record.kind === "http_operation" && (
              <BackendAPIFields
                projectId={projectId}
                revisionId={baseRevisionId}
                target={target}
                pins={pins}
                operationId={record.id}
                onValueSelect={selectValue}
              />
            )}
            {valueSelection && (
              <Text fw={600} style={databaseWrap}>
                Выбранное значение:{" "}
                {valueSelection.kind === "port"
                  ? `${valueSelection.collection} · ${valueSelection.portKey}`
                  : valueSelection.kind === "column"
                    ? `колонка · ${valueSelection.facetKey}`
                    : valueSelection.kind === "event_field"
                      ? lineageRefLabel(valueSelection)
                      : "поле API"}
              </Text>
            )}
            {"parentId" in record && record.parentId && (
              <Button
                variant="subtle"
                onClick={() => onSelect({ type: "node", id: record.parentId! })}
              >
                Открыть владельца значения {record.parentId}
              </Button>
            )}
            <details>
              <summary>Свойства, порты и ограничения</summary>
              <Code block style={databaseWrap}>
                {JSON.stringify(record.attributes, null, 2)}
              </Code>
            </details>
            {"from" in record && (
              <Group>
                {[record.from, record.to].map((id) => (
                  <Button
                    key={id}
                    variant="subtle"
                    h="auto"
                    styles={databaseButtonStyles}
                    onClick={() => onSelect({ type: "node", id })}
                  >
                    Открыть объект {id}
                  </Button>
                ))}
              </Group>
            )}
            {selection.type === "node" && (
              <>
                <Title order={4}>Связанные объекты исходника</Title>
                <LoadState query={links} label="связей Flow" />
                {links.data?.map((edge) => (
                  <Group key={edge.id}>
                    <Button
                      variant="subtle"
                      h="auto"
                      styles={databaseButtonStyles}
                      onClick={() => onSelect({ type: "edge", id: edge.id })}
                    >
                      Связь {edge.kind}
                    </Button>
                    <Button
                      variant="subtle"
                      h="auto"
                      styles={databaseButtonStyles}
                      onClick={() =>
                        onSelect({
                          type: "node",
                          id: edge.from === selection.id ? edge.to : edge.from,
                        })
                      }
                    >
                      Открыть объект {edge.from === selection.id ? edge.to : edge.from}
                    </Button>
                  </Group>
                ))}
              </>
            )}
            <DatabaseEvidence
              context={{
                projectId,
                revisionId: baseRevisionId,
                target,
                pins,
                datastoreId: "",
                facetKey: "",
              }}
              subjectId={record.id}
            />
          </>
        )}
      </Stack>
    </Paper>
  );
}

import { useEffect, useRef } from "react";
import { Badge, Button, Code, Group, Paper, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import {
  getBackendProposalCoverage,
  getBackendProposalNode,
} from "@/api/generated/backend-projects/backend-projects";
import { CoverageDetails, LoadState } from "./BackendGraphInventory";
import type { BackendSourcePin } from "./backendFlowReads";
import {
  databaseKey,
  databaseTarget,
  databaseWrap,
  readDatabaseEvidence,
  readDatabaseGraph,
  relationalFacets,
  useDatabaseCancellation,
  type DatabaseContext,
  type DatabaseSelection,
} from "./backendDatabaseReads";

export function BackendDatabaseProposalInspector({
  context,
  selection,
  onSelect,
  onClose,
  onFlowNavigate,
}: {
  context: DatabaseContext;
  selection: DatabaseSelection;
  onSelect: (selection: DatabaseSelection) => void;
  onClose: () => void;
  onFlowNavigate?: (pin: BackendSourcePin) => void;
}) {
  const target = context.proposal!;
  const key = [...databaseKey(context), "proposal-inspector", selection.type, selection.id];
  useDatabaseCancellation(key);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  const record = useQuery({
    queryKey: [...key, "record"],
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      if (selection.type === "node") {
        const response = await getBackendProposalNode(
          context.projectId,
          target.proposalId,
          target.proposalRevisionId,
          selection.id,
          { signal },
        );
        signal.throwIfAborted();
        if (response.status !== 200) throw new Error("Объект отсутствует в выбранном черновике");
        return response.data.proposalProjection;
      }
      const graph = await readDatabaseGraph(
        context.projectId,
        { ...databaseTarget(context), recordType: "edges", id: selection.id },
        signal,
      );
      if (!graph.proposalEdges[0]) throw new Error("Связь отсутствует в выбранном черновике");
      return graph.proposalEdges[0];
    },
  });
  const value = record.data;
  const children = useQuery({
    queryKey: [...key, "children"],
    enabled: value?.kind === "table" || value?.kind === "constraint",
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) =>
      value?.kind === "table"
        ? readDatabaseGraph(
            context.projectId,
            { ...databaseTarget(context), recordType: "nodes", parentId: selection.id },
            signal,
          )
        : readDatabaseGraph(
            context.projectId,
            { ...databaseTarget(context), recordType: "edges", from: selection.id },
            signal,
          ),
  });
  const evidence = useQuery({
    queryKey: [...key, "evidence"],
    enabled: !!value?.sourceRecord,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) => readDatabaseEvidence(context, selection.id, signal),
  });
  const coverage = useQuery({
    queryKey: [...key, "coverage"],
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await getBackendProposalCoverage(
        context.projectId,
        target.proposalId,
        target.proposalRevisionId,
        { signal },
      );
      signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Не удалось загрузить полноту основания");
      return response.data;
    },
  });
  const facet = value?.effectiveFacet;
  const source = value?.sourceRecord
    ? relationalFacets(value.sourceRecord)[context.facetKey]
    : undefined;
  const sourceNullable = source && "nullable" in source ? source.nullable : undefined;
  const desiredNullable = facet && "nullable" in facet.values ? facet.values.nullable : undefined;
  return (
    <Paper
      withBorder
      p="md"
      component="section"
      aria-label="Инспектор базы данных"
      style={{ minWidth: 0 }}
    >
      <Stack>
        <Group justify="space-between">
          <Title order={3} ref={heading} tabIndex={-1} style={databaseWrap}>
            {value && "name" in value ? value.name : "Предложенный объект"}
          </Title>
          <Button variant="default" onClick={onClose}>
            Закрыть инспектор
          </Button>
        </Group>
        <Text size="xs" style={databaseWrap}>
          Предложение: {target.proposalId} · черновик: {target.proposalRevisionId} · база:{" "}
          {context.revisionId}
        </Text>
        <LoadState query={record} label="предложенного объекта" />
        <Title order={4}>Полнота основания</Title>
        <Text size="sm">Покрытие исходной ревизии; проверки предложения не выполнены.</Text>
        <LoadState query={coverage} label="полноты основания" />
        {coverage.data && <CoverageDetails data={coverage.data} />}
        {value && (
          <>
            <Badge>Желаемая структура · не проверена</Badge>
            {onFlowNavigate &&
              value.sourceRecord &&
              ["table", "column", "view"].includes(value.kind) && (
                <Button
                  variant="default"
                  onClick={() =>
                    onFlowNavigate({
                      revisionId: context.revisionId,
                      dataNodeId: value.sourceRecord!.id,
                      datastoreId: context.datastoreId,
                      facetKey: context.facetKey,
                    })
                  }
                >
                  Чтения и записи в Flow основания предложения
                </Button>
              )}
            {desiredNullable?.status === "known" && (
              <Badge>
                {desiredNullable.value === false ? "Желаемое NOT NULL" : "Желаемое NULL"}
              </Badge>
            )}
            {sourceNullable?.status === "known" && (
              <Text>
                {sourceNullable.value ? "Источник допускает NULL" : "Источник объявляет NOT NULL"}
              </Text>
            )}
            <Title order={4}>Исходная схема</Title>
            {source ? (
              <Code block style={databaseWrap}>
                {JSON.stringify(source, null, 2)}
              </Code>
            ) : (
              <Text>Объект создан в предложении; исходного определения нет</Text>
            )}
            {facet && (
              <>
                <Title order={4}>Желаемая схема</Title>
                <Code block style={databaseWrap}>
                  {JSON.stringify(facet.values, null, 2)}
                </Code>
                {Object.entries(facet.propertyOrigins).map(([path, origin]) => (
                  <Paper withBorder p="sm" key={path}>
                    <Text style={databaseWrap}>
                      {path} · {origin.kind === "intent" ? "Намерение" : "Источник"}
                    </Text>
                    {origin.kind === "intent" ? (
                      <>
                        <Text>{origin.reason}</Text>
                        <Text size="xs" style={databaseWrap}>
                          Команда: {origin.commandId}
                        </Text>
                      </>
                    ) : (
                      <Text size="xs" style={databaseWrap}>
                        База: {origin.base.revisionId} · основания:{" "}
                        {origin.evidenceIds.join(", ") || "не указаны"}
                      </Text>
                    )}
                  </Paper>
                ))}
                {facet.limitations.map((text) => (
                  <Text key={text} size="sm" style={databaseWrap}>
                    {text}
                  </Text>
                ))}
              </>
            )}
            {"from" in value && (
              <Group>
                <Button variant="subtle" onClick={() => onSelect({ type: "node", id: value.from })}>
                  Открыть ограничение
                </Button>
                <Button variant="subtle" onClick={() => onSelect({ type: "node", id: value.to })}>
                  Открыть таблицу цели
                </Button>
              </Group>
            )}
            {(value.kind === "table" || value.kind === "constraint") && (
              <>
                <LoadState query={children} label="объектов черновика" />
                {children.data?.proposalNodes.map((node) => (
                  <Button
                    variant="default"
                    h="auto"
                    styles={{ label: databaseWrap }}
                    key={node.id}
                    onClick={() => onSelect({ type: "node", id: node.id })}
                  >
                    Открыть {node.kind} {node.name}
                  </Button>
                ))}
                {children.data?.proposalEdges.map((edge) => (
                  <Button
                    variant="default"
                    h="auto"
                    styles={{ label: databaseWrap }}
                    key={edge.id}
                    onClick={() => onSelect({ type: "edge", id: edge.id })}
                  >
                    Открыть FK {edge.id}
                  </Button>
                ))}
              </>
            )}
            <Title order={4}>Основания источника</Title>
            {value.sourceRecord ? (
              <>
                <LoadState query={evidence} label="оснований" />
                {evidence.data?.map((item) => (
                  <Paper withBorder p="sm" key={item.id}>
                    <Text>{item.explanation}</Text>
                    <Text size="xs" style={databaseWrap}>
                      {item.source.file}:{item.source.startLine} · {item.source.snapshotId}
                    </Text>
                    <Code block style={databaseWrap}>
                      {item.snippet}
                    </Code>
                  </Paper>
                ))}
              </>
            ) : (
              <Text>Новый объект не имеет подтверждений в исходниках</Text>
            )}
          </>
        )}
      </Stack>
    </Paper>
  );
}

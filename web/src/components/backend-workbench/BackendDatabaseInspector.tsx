import { BackendValueInspector } from "./BackendValueInspector";
import { useBackendAPIDeparture } from "./useBackendAPIDeparture";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
import { BackendValueSeeds } from "./BackendLineageActions";
import { useEffect, useRef, useState } from "react";
import { Badge, Button, Code, Group, Paper, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { getBackendNode } from "@/api/generated/backend-projects/backend-projects";
import type { BackendEdge, BackendNode, BackendFacetComparison } from "@/api/generated/schemas";
import { LoadState } from "./BackendGraphInventory";
import { BackendDatabaseProposalInspector } from "./BackendDatabaseProposalInspector";
import type { BackendSourcePin } from "./backendFlowReads";
import {
  databaseButtonStyles,
  databaseKey,
  databaseStatus,
  databaseWrap,
  readDatabaseEvidence,
  readDatabaseGraph,
  relationalFacets,
  useDatabaseCancellation,
  type DatabaseContext,
  type DatabaseSelection,
} from "./backendDatabaseReads";

type Props = {
  context: DatabaseContext;
  valueRef?: BackendLineageValueRef;
  selection: DatabaseSelection;
  onSelect: (selection: DatabaseSelection) => void;
  onClose: () => void;
  onRequireColumn?: (columnId: string) => void;
  onFlowNavigate?: (pin: BackendSourcePin) => void;
};

export function BackendDatabaseInspector({ ...props }: Props) {
  return props.context.proposal ? (
    <BackendDatabaseProposalInspector {...props} />
  ) : (
    <SourceDatabaseInspector {...props} />
  );
}

function SourceDatabaseInspector({
  context: original,
  selection,
  onSelect: selectRecord,
  onClose: closeRecord,
  onRequireColumn,
  onFlowNavigate,
  valueRef,
}: Props) {
  const depart = useBackendAPIDeparture();
  const onSelect = (next: DatabaseSelection) => {
    depart(() => selectRecord(next));
  };
  const onClose = () => {
    depart(closeRecord);
  };
  const [valueSelection, setValueSelection] = useState<BackendLineageValueRef>();
  const selectValue = (next: BackendLineageValueRef) => {
    depart(() => setValueSelection(next));
  };
  const closeValue = () => {
    depart(() => setValueSelection(undefined));
  };
  const context = { ...original, revisionId: selection.revisionId ?? original.revisionId };
  const key = [...databaseKey(context), "inspector", selection.type, selection.id];
  useDatabaseCancellation(key);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  const query = useQuery({
    queryKey: [...key, "record"],
    queryFn: async ({ signal }) => {
      if (selection.type === "edge") {
        const page = await readDatabaseGraph(
          context.projectId,
          { revisionId: context.revisionId, recordType: "edges", id: selection.id },
          signal,
        );
        if (!page.edges[0]) throw new Error("Связь отсутствует в выбранной ревизии");
        return page.edges[0];
      }
      const response = await getBackendNode(context.projectId, context.revisionId, selection.id, {
        signal,
      });
      signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Объект отсутствует в выбранной ревизии");
      return response.data;
    },
    staleTime: Infinity,
    retry: false,
  });
  const record = query.data;
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
          <Title order={3} tabIndex={-1} ref={heading} style={databaseWrap}>
            {record && "name" in record ? record.name : "Инспектор базы данных"}
          </Title>
          <Button variant="default" onClick={onClose}>
            Закрыть инспектор
          </Button>
        </Group>
        <Text size="xs" style={databaseWrap}>
          Ревизия: {context.revisionId} · объект: {selection.id}
        </Text>
        <LoadState query={query} label="объекта базы данных" />
        {record && (
          <>
            <Badge>{record.kind}</Badge>
            {"name" in record && (
              <BackendValueSeeds
                projectId={context.projectId}
                revisionId={context.revisionId}
                node={record}
                selected={valueRef}
                onValueSelect={selectValue}
              />
            )}
            {valueSelection && (
              <BackendValueInspector
                key={JSON.stringify(valueSelection)}
                projectId={context.projectId}
                revisionId={context.revisionId}
                value={valueSelection}
                onClose={closeValue}
              />
            )}
            {onFlowNavigate && ["table", "column", "view"].includes(record.kind) && (
              <Button
                variant="default"
                onClick={() =>
                  onFlowNavigate({
                    revisionId: context.revisionId,
                    dataNodeId: record.id,
                    datastoreId: context.datastoreId,
                    facetKey: context.facetKey,
                  })
                }
              >
                Чтения и записи в исходном Flow
              </Button>
            )}
            {record.kind === "column" && onRequireColumn && (
              <Button onClick={() => onRequireColumn(record.id)}>
                Сделать обязательной в предложении
              </Button>
            )}
            {"from" in record && (
              <Group>
                <Button
                  variant="subtle"
                  h="auto"
                  styles={databaseButtonStyles}
                  onClick={() =>
                    onSelect({ type: "node", id: record.from, revisionId: context.revisionId })
                  }
                >
                  Открыть ограничение {record.from}
                </Button>
                <Button
                  variant="subtle"
                  h="auto"
                  styles={databaseButtonStyles}
                  onClick={() =>
                    onSelect({ type: "node", id: record.to, revisionId: context.revisionId })
                  }
                >
                  Открыть цель {record.to}
                </Button>
              </Group>
            )}
            {"parentId" in record && record.parentId && (
              <Button
                variant="subtle"
                h="auto"
                styles={databaseButtonStyles}
                onClick={() =>
                  onSelect({ type: "node", id: record.parentId!, revisionId: context.revisionId })
                }
              >
                Открыть владельца значения {record.parentId}
              </Button>
            )}
            <FacetComparison comparison={record.facetComparison} />
            <RecordFacets record={record} context={context} onSelect={onSelect} />
            {record.kind === "table" && (
              <TableChildren context={context} nodeId={record.id} onSelect={onSelect} />
            )}
            {record.kind === "constraint" && (
              <ConstraintReferences
                context={context}
                constraintIds={[record.id]}
                onSelect={onSelect}
              />
            )}
            <DatabaseEvidence context={context} subjectId={record.id} />
          </>
        )}
      </Stack>
    </Paper>
  );
}

function FacetComparison({ comparison }: { comparison?: BackendFacetComparison }) {
  if (!comparison) return null;
  return (
    <Stack gap="xs">
      <Text fw={600}>Сравнение источников: {databaseStatus(comparison.status)}</Text>
      {comparison.pairs.map((pair) => (
        <div key={`${pair.leftFacetKey}:${pair.rightFacetKey}`}>
          <Text size="sm" style={databaseWrap}>
            {pair.leftFacetKey} ↔ {pair.rightFacetKey}: {databaseStatus(pair.status)}
          </Text>
          {pair.changedPaths.map((path) => (
            <Text key={path} size="sm" style={databaseWrap}>
              {path}
            </Text>
          ))}
          {pair.definitionDifferent && (
            <Text size="sm">
              Исходные определения различаются; эквивалентность SQL не установлена
            </Text>
          )}
        </div>
      ))}
    </Stack>
  );
}

function RecordFacets({
  record,
  context,
  onSelect,
}: {
  record: BackendNode | BackendEdge;
  context: DatabaseContext;
  onSelect: Props["onSelect"];
}) {
  const facets = relationalFacets(record);
  return (
    <Stack gap="sm">
      {!facets[context.facetKey] && (
        <Text size="sm">
          Выбранный источник не содержит утверждения об этом объекте; другие источники доступны ниже
        </Text>
      )}
      {Object.entries(facets).map(([key, facet]) => (
        <details key={key} open={key === context.facetKey}>
          <summary style={{ cursor: "pointer", overflowWrap: "anywhere", padding: "8px 0" }}>
            Источник {key}
            {key === context.facetKey ? " · выбран" : ""} · {facet.sourceKind} · {facet.dialect}
          </summary>
          <Stack gap="xs" pl="sm" style={{ minWidth: 0 }}>
            <Text size="sm">
              {databaseStatus(facet.analysisStatus)} · {databaseStatus(facet.freshness.status)}
            </Text>
            {facet.gaps.map((gap) => (
              <Text key={gap} style={databaseWrap} size="sm">
                {gap}
              </Text>
            ))}
            {facet.freshness.reasons.map((reason) => (
              <Text key={reason} style={databaseWrap} size="sm">
                {reason}
              </Text>
            ))}
            <Text size="xs" style={databaseWrap}>
              Снимок источника: {facet.sourceSnapshotId}
            </Text>
            <Text size="xs" style={databaseWrap}>
              Подтверждающий снимок: {facet.freshness.confirmedSnapshotId}
            </Text>
            {Object.entries(facet)
              .filter(
                ([name]) =>
                  ![
                    "sourceKind",
                    "dialect",
                    "analysisStatus",
                    "gaps",
                    "freshness",
                    "sourceSnapshotId",
                    "evidenceIds",
                  ].includes(name),
              )
              .map(([name, value]) => (
                <div key={name}>
                  <Text fw={600} size="sm">
                    {propertyName(name)}
                  </Text>
                  <NativeValue
                    value={value}
                    property={name}
                    context={context}
                    onSelect={onSelect}
                  />
                </div>
              ))}
            <Text size="xs" style={databaseWrap}>
              Доказательства: {facet.evidenceIds.join(", ")}
            </Text>
          </Stack>
        </details>
      ))}
    </Stack>
  );
}

function propertyName(name: string) {
  return (
    (
      {
        qualifiedName: "Полное имя",
        nativeDefinition: "Исходное определение",
        definition: "Исходный текст",
        nativeType: "Объявленный тип",
        typeFamily: "Семейство типа",
        nullable: "Допускает NULL",
        defaultExpression: "DEFAULT",
        generatedExpression: "Вычисляемое выражение",
        identity: "Identity",
        ordinal: "Порядок колонки",
        columnPairs: "Упорядоченные пары колонок",
        columnIds: "Колонки ограничения",
        updateAction: "ON UPDATE",
        deleteAction: "ON DELETE",
        matchType: "MATCH",
        changes: "Изменения миграции",
        order: "Порядок миграции",
        parentIds: "Предшествующие миграции",
        dependencyIds: "Зависимости",
        constraintKind: "Вид ограничения",
        deferrable: "Откладываемое ограничение",
        initiallyDeferred: "Изначально отложено",
        expression: "Выражение",
        columnsStatus: "Полнота колонок",
        constraintsStatus: "Полнота ограничений",
        dependenciesStatus: "Полнота зависимостей",
        bodyStatus: "Полнота анализа тела",
        derivationStatus: "Полнота выведения состояния",
        routineKind: "Вид тела",
        terms: "Упорядоченные части индекса",
        unique: "Уникальный индекс",
        predicate: "Предикат индекса",
        method: "Метод индекса",
        materialized: "Материализовано",
        targetReason: "Ограничение цели",
        databaseName: "База данных",
      } as Record<string, string>
    )[name] ?? name
  );
}

function NativeValue({
  value,
  property,
  context,
  onSelect,
}: {
  value: unknown;
  property: string;
  context: DatabaseContext;
  onSelect: Props["onSelect"];
}) {
  if (value === null) return <Text size="sm">Не объявлено (null)</Text>;
  if (Array.isArray(value))
    return (
      <Stack gap="xs">
        {value.length === 0 && <Text size="sm">Список пуст; полнота указана отдельно</Text>}
        {value.map((item, index) => {
          if (item && typeof item === "object" && "fromColumnId" in item)
            return (
              <Text key={index} size="sm" style={databaseWrap}>
                {item.fromColumnId} → {item.toColumnId}
              </Text>
            );
          return (
            <div key={index}>
              <Text size="xs">{index + 1}.</Text>
              <NativeValue value={item} property={property} context={context} onSelect={onSelect} />
            </div>
          );
        })}
      </Stack>
    );
  if (typeof value === "object") {
    if ("status" in value && value.status === "unknown")
      return (
        <Text size="sm" style={databaseWrap}>
          Неизвестно: {"reason" in value ? String(value.reason) : "основание отсутствует"}
        </Text>
      );
    if ("status" in value && value.status === "known" && "value" in value)
      return (
        <NativeValue
          value={value.value}
          property={property}
          context={context}
          onSelect={onSelect}
        />
      );
    if ("kind" in value && value.kind === "source_only")
      return (
        <Text size="sm" style={databaseWrap}>
          Только в исходниках: {"qualifiedName" in value ? String(value.qualifiedName) : ""} ·{" "}
          {"reason" in value ? String(value.reason) : ""}
        </Text>
      );
    if (
      "kind" in value &&
      ["historical", "candidate"].includes(String(value.kind)) &&
      "objectId" in value
    )
      return (
        <Button
          variant="subtle"
          h="auto"
          styles={databaseButtonStyles}
          onClick={() =>
            onSelect({
              type: "node",
              id: String(value.objectId),
              revisionId: "revisionId" in value ? String(value.revisionId) : context.revisionId,
            })
          }
        >
          {value.kind === "historical" ? "Исторический объект" : "Объект ревизии"}:{" "}
          {String(value.objectId)}
        </Button>
      );
    return (
      <Stack gap="xs">
        {Object.entries(value).map(([key, child]) => (
          <div key={key}>
            <Text size="xs" fw={600}>
              {propertyName(key)}
            </Text>
            <NativeValue value={child} property={key} context={context} onSelect={onSelect} />
          </div>
        ))}
      </Stack>
    );
  }
  if (["dependencyIds", "parentIds", "columnIds"].includes(property) && typeof value === "string")
    return (
      <Button
        variant="subtle"
        h="auto"
        styles={databaseButtonStyles}
        onClick={() => onSelect({ type: "node", id: value, revisionId: context.revisionId })}
      >
        {value}
      </Button>
    );
  return (
    <Code block style={databaseWrap}>
      {String(value)}
    </Code>
  );
}

function TableChildren({
  context,
  nodeId,
  onSelect,
}: {
  context: DatabaseContext;
  nodeId: string;
  onSelect: Props["onSelect"];
}) {
  const query = useQuery({
    queryKey: [...databaseKey(context), "children", nodeId],
    queryFn: ({ signal }) =>
      readDatabaseGraph(
        context.projectId,
        { revisionId: context.revisionId, recordType: "nodes", parentId: nodeId },
        signal,
      ),
    staleTime: Infinity,
    retry: false,
  });
  const children = query.data?.nodes;
  return (
    <Stack>
      <Title order={4}>Колонки и ограничения всех источников</Title>
      <LoadState query={query} label="колонок и ограничений" />
      {children && (
        <>
          <Text size="sm" component="output">
            Дочерний список загружен полностью: {children.length} объектов
          </Text>
          {children.map((node) => (
            <Button
              key={node.id}
              variant="subtle"
              h="auto"
              styles={databaseButtonStyles}
              aria-label={`Открыть ${node.kind === "column" ? "колонку" : "объект"} ${node.name}`}
              onClick={() =>
                onSelect({ type: "node", id: node.id, revisionId: context.revisionId })
              }
            >
              {node.name} · {node.kind} · источники {Object.keys(relationalFacets(node)).join(", ")}
            </Button>
          ))}
          <ConstraintReferences
            context={context}
            constraintIds={children
              .filter((node) => node.kind === "constraint")
              .map((node) => node.id)}
            onSelect={onSelect}
          />
        </>
      )}
    </Stack>
  );
}

function ConstraintReferences({
  context,
  constraintIds,
  onSelect,
}: {
  context: DatabaseContext;
  constraintIds: string[];
  onSelect: Props["onSelect"];
}) {
  const query = useQuery({
    queryKey: [...databaseKey(context), "constraint-targets", constraintIds],
    queryFn: async ({ signal }) =>
      (
        await Promise.all(
          constraintIds.map((from) =>
            readDatabaseGraph(
              context.projectId,
              { revisionId: context.revisionId, recordType: "edges", from, kind: "references" },
              signal,
            ),
          ),
        )
      ).flatMap((page) => page.edges),
    staleTime: Infinity,
    retry: false,
  });
  const edges = query.data;
  const groups = new Map<string, BackendEdge[]>();
  edges?.forEach((edge) => groups.set(edge.from, [...(groups.get(edge.from) ?? []), edge]));
  return (
    <Stack>
      <Title order={4}>Цели FK по ограничениям</Title>
      <LoadState query={query} label="целей FK" />
      {edges && (
        <Text size="sm" component="output">
          Исходящие FK загружены полностью: {edges.length}
        </Text>
      )}
      {[...groups].map(([id, group]) => (
        <div key={id}>
          <Text fw={600} size="sm" style={databaseWrap}>
            Ограничение {id}
          </Text>
          {new Set(group.map((edge) => edge.to)).size > 1 && (
            <Text>Источники указывают разные цели; утверждения сохранены отдельно</Text>
          )}
          {group.map((edge) => (
            <Button
              key={edge.id}
              variant="subtle"
              h="auto"
              styles={databaseButtonStyles}
              onClick={() =>
                onSelect({ type: "edge", id: edge.id, revisionId: context.revisionId })
              }
            >
              {edge.id} → {edge.to} · источники {Object.keys(relationalFacets(edge)).join(", ")}
            </Button>
          ))}
        </div>
      ))}
    </Stack>
  );
}

export function DatabaseEvidence({
  context,
  subjectId,
}: {
  context: DatabaseContext;
  subjectId: string;
}) {
  const query = useQuery({
    queryKey: [...databaseKey(context), "proof", subjectId],
    queryFn: ({ signal }) => readDatabaseEvidence(context, subjectId, signal),
    staleTime: Infinity,
    retry: false,
  });
  return (
    <Stack>
      <Title order={4}>Исходные доказательства</Title>
      <LoadState query={query} label="доказательств" />
      {query.data && (
        <Text size="sm" component="output">
          Доказательства загружены полностью: {query.data.length}
        </Text>
      )}
      {query.data?.map((proof) => (
        <details key={proof.id} open>
          <summary style={databaseWrap}>
            Доказательство {proof.id} · {proof.method} · {databaseStatus(proof.status)}
          </summary>
          <Stack gap="xs" pl="sm">
            <Text size="sm" style={databaseWrap}>
              {proof.source.file}
              {proof.source.startLine ? `:${proof.source.startLine}–${proof.source.endLine}` : ""}
            </Text>
            <Text size="sm" style={databaseWrap}>
              {proof.explanation}
            </Text>
            <Text size="xs" style={databaseWrap}>
              Снимок: {proof.source.snapshotId} · SHA-256: {proof.source.contentHash}
            </Text>
            {proof.propertyPath && (
              <Text size="xs" style={databaseWrap}>
                Свойство: {proof.propertyPath}
              </Text>
            )}
            {proof.source.symbol && (
              <Text size="xs" style={databaseWrap}>
                Символ: {proof.source.symbol}
              </Text>
            )}
            {proof.snippet && (
              <Code block style={databaseWrap}>
                {proof.snippet}
              </Code>
            )}
            {proof.freshness && (
              <Text size="sm">
                {databaseStatus(proof.freshness.status)}: {proof.freshness.reasons.join(", ")}
              </Text>
            )}
          </Stack>
        </details>
      ))}
    </Stack>
  );
}

import { CoverageDetails, LoadState, Pages } from "./BackendReadUI";
import { BackendExactGraph, BackendExactRecordInspector } from "./BackendExactGraph";
export { CoverageDetails, LoadState, Pages } from "./BackendReadUI";
import { BackendAPIFields } from "./BackendAPIFields";
import { BackendValueInspector } from "./BackendValueInspector";
import { BackendValueSeeds } from "./BackendLineageActions";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Divider,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import {
  queryBackendGraph,
  useGetBackendCoverage,
  useGetBackendEvidence,
  useGetBackendNode,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendEdge,
  BackendComposedEdge,
  BackendNode,
  BackendEffectiveGraphPins,
  QueryBackendGraphRequest,
} from "@/api/generated/schemas";
import { usePinnedValue } from "./backendFlowReads";
import { useBackendAPIDeparture } from "./useBackendAPIDeparture";

const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
const kinds = [
  "system",
  "service",
  "module",
  "external_system",
  "datastore",
  "symbol",
  "http_operation",
  "handler",
  "unresolved_target",
  "db_schema",
  "table",
  "column",
  "constraint",
  "index",
  "view",
  "migration",
  "flow",
  "flow_step",
  "query",
  "transaction",
  "api_field",
  "field_mapping",
  "channel",
  "message",
  "consumer",
  "job",
  "event_field",
];
type Props = {
  projectId: string;
  revisionId: string;
  schemaVersion?: string;
  pins?: BackendEffectiveGraphPins;
  focusTarget?: { recordType: "node" | "edge"; id: string };
  onSelectionChange?: (
    selection: { recordType: "node" | "edge"; id: string; revisionId: string } | undefined,
  ) => void;
};
type GraphEdge = BackendEdge | BackendComposedEdge;
type Selection = { type: "node"; id: string } | { type: "edge"; id: string; edge?: GraphEdge };

export function BackendGraphInventory(props: Props) {
  if (props.schemaVersion === "6")
    return (
      <BackendExactGraph
        projectId={props.projectId}
        target={{ revisionId: props.revisionId }}
        pins={props.pins}
        focusTarget={props.focusTarget}
        onSelectionChange={(selection) =>
          props.onSelectionChange?.(
            selection ? { ...selection, revisionId: props.revisionId } : undefined,
          )
        }
      />
    );
  return <Inventory key={`${props.projectId}:${props.revisionId}`} {...props} />;
}

function useGraphPage(projectId: string, input: QueryBackendGraphRequest, enabled = true) {
  return useQuery({
    queryKey: ["backend-graph", projectId, input],
    enabled,
    queryFn: async ({ signal }) => {
      const response = await queryBackendGraph(projectId, input, { signal });
      if (response.status !== 200) throw new Error("Не удалось прочитать граф.");
      return { ...response, data: response.data };
    },
    staleTime: Infinity,
    retry: false,
  });
}

function Inventory({
  projectId,
  revisionId,
  schemaVersion,
  focusTarget,
  onSelectionChange,
}: Props) {
  const depart = useBackendAPIDeparture();
  const coverageQuery = useGetBackendCoverage(projectId, revisionId, {
    query: { staleTime: Infinity, retry: false },
  });
  const coverage = coverageQuery.data?.status === 200 ? coverageQuery.data.data : undefined;
  const [searchDraft, setSearchDraft] = useState("");
  const [kindDraft, setKindDraft] = useState("");
  const [filter, setFilter] = useState({ search: "", kind: "" });
  const [cursors, setCursors] = useState([""]);
  const [selection, setRawSelection] = usePinnedValue<Selection | null>(
    JSON.stringify(focusTarget ?? null),
    focusTarget ? { type: focusTarget.recordType, id: focusTarget.id } : null,
  );
  const setSelection = (next: Selection | null) => {
    depart(() => {
      setRawSelection(next);
      onSelectionChange?.(next ? { recordType: next.type, id: next.id, revisionId } : undefined);
    });
  };
  const query = useGraphPage(projectId, {
    revisionId,
    recordType: "nodes",
    ...filter,
    limit: 100,
    cursor: cursors.at(-1) ?? "",
  });
  const page = query.data?.status === 200 ? query.data.data : undefined;

  return (
    <Stack gap="lg" aria-label="Модель бэкенда">
      <div>
        <Title order={2}>Модель бэкенда</Title>
        <Text size="sm" c="dimmed">
          Объекты и основания выбранной ревизии
        </Text>
      </div>
      <LoadState query={coverageQuery} label="покрытия" />
      {coverage && <CoverageDetails data={coverage} />}
      <Divider />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          depart(() => {
            setFilter({ search: searchDraft.trim(), kind: kindDraft });
            setCursors([""]);
            setSelection(null);
          });
        }}
      >
        <Group align="flex-end">
          <TextInput
            label="Название объекта"
            value={searchDraft}
            onChange={(event) => setSearchDraft(event.currentTarget.value)}
            style={{ flex: "1 1 220px" }}
          />
          <NativeSelect
            label="Тип объекта"
            value={kindDraft}
            onChange={(event) => setKindDraft(event.currentTarget.value)}
            data={[
              { value: "", label: "Все типы" },
              ...kinds
                .filter(
                  (kind) =>
                    (!["flow", "flow_step", "query", "transaction"].includes(kind) ||
                      schemaVersion === "3" ||
                      schemaVersion === "4" ||
                      schemaVersion === "5") &&
                    (!["channel", "message", "consumer", "job", "event_field"].includes(kind) ||
                      schemaVersion === "5") &&
                    (["2", "3", "4", "5"].includes(schemaVersion ?? "") ||
                      ![
                        "db_schema",
                        "table",
                        "column",
                        "constraint",
                        "index",
                        "view",
                        "migration",
                      ].includes(kind)),
                )
                .map((kind) => ({ value: kind, label: kind })),
            ]}
            style={{ flex: "1 1 180px" }}
          />
          <Button type="submit">Найти объекты</Button>
        </Group>
      </form>
      <LoadState query={query} label="объектов" />
      {page && (
        <Stack gap="xs" aria-label="Объекты модели">
          {page.nodes.length === 0 && <Text c="dimmed">Объекты не найдены</Text>}
          {page.nodes.map((node) => (
            <Button
              key={node.id}
              variant={selection?.type === "node" && selection.id === node.id ? "light" : "subtle"}
              aria-label={`Открыть объект ${node.name}`}
              aria-pressed={selection?.type === "node" && selection.id === node.id}
              h="auto"
              py="sm"
              justify="flex-start"
              styles={{ label: { ...wrap, textAlign: "left" } }}
              onClick={() => setSelection({ type: "node", id: node.id })}
            >
              {node.kind} · {node.name}
              {"freshness" in node && node.freshness?.status === "stale" ? " · Устарело" : ""}
            </Button>
          ))}
          <Pages
            label="объекты"
            cursors={cursors}
            next={page.nextCursor}
            busy={query.isFetching}
            setCursors={setCursors}
          />
        </Stack>
      )}
      {selection && (
        <Paper
          component="section"
          aria-label="Инспектор объекта"
          withBorder
          p="md"
          style={{ minWidth: 0 }}
        >
          <Stack>
            <Group justify="space-between">
              <Title order={3}>{selection.type === "node" ? "Объект" : "Связь"}</Title>
              <Button variant="subtle" onClick={() => setSelection(null)}>
                Закрыть инспектор
              </Button>
            </Group>
            {selection.type === "node" ? (
              <NodeDetails
                key={selection.id}
                projectId={projectId}
                revisionId={revisionId}
                nodeId={selection.id}
                onEdge={(edge) => setSelection({ type: "edge", id: edge.id, edge })}
              />
            ) : (
              <AnnotationEdgeDetails
                projectId={projectId}
                revisionId={revisionId}
                edgeId={selection.id}
                selectedEdge={selection.edge}
              />
            )}
          </Stack>
        </Paper>
      )}
    </Stack>
  );
}

function AnnotationEdgeDetails({
  projectId,
  revisionId,
  edgeId,
  selectedEdge,
}: {
  projectId: string;
  revisionId: string;
  edgeId: string;
  selectedEdge?: GraphEdge;
}) {
  const query = useGraphPage(
    projectId,
    { revisionId, recordType: "edges", id: edgeId },
    !selectedEdge,
  );
  const edge =
    selectedEdge ??
    (query.data?.status === 200
      ? query.data.data.edges.find((item) => item.id === edgeId)
      : undefined);
  if (edge && "source" in edge)
    return (
      <BackendExactRecordInspector
        projectId={projectId}
        target={{ revisionId }}
        claimsSupported
        recordType="edge"
        id={edgeId}
      />
    );
  return (
    <Stack>
      {!selectedEdge && <LoadState query={query} label="связи" />}
      {!query.isPending && !query.isError && !edge && (
        <Alert color="yellow">Связь отсутствует в выбранной ревизии.</Alert>
      )}
      {edge && (
        <>
          <Text fw={600}>{edge.kind}</Text>
          <AssertionDetails record={edge} />
          <Code block style={wrap}>
            {edge.from} → {edge.to}
          </Code>
          <Code block style={wrap}>
            {JSON.stringify(edge.attributes, null, 2)}
          </Code>
          <EvidenceDetails projectId={projectId} revisionId={revisionId} subjectId={edgeId} />
        </>
      )}
    </Stack>
  );
}

function NodeDetails({
  projectId,
  revisionId,
  nodeId,
  onEdge: selectEdge,
}: Props & { nodeId: string; onEdge: (edge: GraphEdge) => void }) {
  const depart = useBackendAPIDeparture();
  const onEdge = (next: GraphEdge) => {
    depart(() => selectEdge(next));
  };
  const [selectedValue, setSelectedValue] = useState<BackendLineageValueRef>();
  const selectValue = (next: BackendLineageValueRef) => {
    depart(() => setSelectedValue(next));
  };
  const closeValue = () => {
    depart(() => setSelectedValue(undefined));
  };
  const query = useGetBackendNode(projectId, revisionId, nodeId, {
    query: { staleTime: Infinity, retry: false },
  });
  const read = query.data?.status === 200 ? query.data.data : undefined;
  const node: BackendNode | undefined = read && "id" in read ? read : undefined;
  if (read && "node" in read)
    return (
      <BackendExactRecordInspector
        projectId={projectId}
        target={{ revisionId }}
        pins={read.pins}
        claimsSupported
        recordType="node"
        id={nodeId}
      />
    );
  return (
    <>
      <LoadState query={query} label="объекта" />
      {node && (
        <>
          <Title order={4} style={wrap}>
            {node.name}
          </Title>
          <Group>
            <Badge>{node.kind}</Badge>
            <Text size="sm" style={wrap}>
              {node.id}
            </Text>
          </Group>
          <AssertionDetails record={node} />
          <BackendValueSeeds
            projectId={projectId}
            revisionId={revisionId}
            node={node}
            onValueSelect={selectValue}
          />
          {node.kind === "http_operation" && (
            <BackendAPIFields
              projectId={projectId}
              revisionId={revisionId}
              operationId={node.id}
              onValueSelect={selectValue}
            />
          )}
          {selectedValue && (
            <BackendValueInspector
              key={JSON.stringify(selectedValue)}
              projectId={projectId}
              revisionId={revisionId}
              value={selectedValue}
              onClose={closeValue}
            />
          )}
          <Code block style={wrap}>
            {JSON.stringify(node.attributes, null, 2)}
          </Code>
        </>
      )}
      <Relationships
        key={`out:${nodeId}`}
        projectId={projectId}
        revisionId={revisionId}
        nodeId={nodeId}
        direction="out"
        onEdge={onEdge}
      />
      <Relationships
        key={`in:${nodeId}`}
        projectId={projectId}
        revisionId={revisionId}
        nodeId={nodeId}
        direction="in"
        onEdge={onEdge}
      />
      <EvidenceDetails
        key={nodeId}
        projectId={projectId}
        revisionId={revisionId}
        subjectId={nodeId}
      />
    </>
  );
}

function Relationships({
  projectId,
  revisionId,
  nodeId,
  direction,
  onEdge,
}: Props & { nodeId: string; direction: "in" | "out"; onEdge: (edge: GraphEdge) => void }) {
  const [cursors, setCursors] = useState([""]);
  const query = useGraphPage(projectId, {
    revisionId,
    recordType: "edges",
    ...(direction === "out" ? { from: nodeId } : { to: nodeId }),
    limit: 100,
    cursor: cursors.at(-1) ?? "",
  });
  const page = query.data?.status === 200 ? query.data.data : undefined;
  const label = direction === "out" ? "исходящие связи" : "входящие связи";
  return (
    <Stack gap="xs">
      <Title order={4}>{direction === "out" ? "Исходящие связи" : "Входящие связи"}</Title>
      <LoadState query={query} label={label} />
      {page?.edges.length === 0 && (
        <Text size="sm" c="dimmed">
          Связей нет
        </Text>
      )}
      {page?.edges.map((edge) => (
        <Button
          key={edge.id}
          variant="subtle"
          aria-label={`Основания связи ${edge.kind} ${edge.id}`}
          h="auto"
          py="xs"
          justify="flex-start"
          styles={{ label: { ...wrap, textAlign: "left" } }}
          onClick={() => onEdge(edge)}
        >
          {edge.kind} · {direction === "out" ? edge.to : edge.from}
        </Button>
      ))}
      <Pages
        label={label}
        cursors={cursors}
        next={page?.nextCursor}
        busy={query.isFetching}
        setCursors={setCursors}
      />
    </Stack>
  );
}

function EvidenceDetails({
  projectId,
  revisionId,
  subjectId,
  evidenceId,
}: Props & { subjectId?: string; evidenceId?: string }) {
  const [cursors, setCursors] = useState([""]);
  const query = useGetBackendEvidence(
    projectId,
    revisionId,
    { ...(evidenceId ? { evidenceId } : { subjectId, cursor: cursors.at(-1) ?? "" }), limit: 100 },
    { query: { staleTime: Infinity, retry: false } },
  );
  const page = query.data?.status === 200 ? query.data.data : undefined;
  return (
    <Stack gap="sm">
      <Title order={4}>Основания</Title>
      <LoadState query={query} label="оснований" />
      {page?.items.length === 0 && (
        <Text size="sm" c="dimmed">
          Оснований нет
        </Text>
      )}
      {page?.items.map((evidence) => (
        <div key={evidence.id}>
          <AssertionDetails record={evidence} />
          <Group gap="sm">
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
          {evidence.source.symbol && (
            <Text size="xs" style={wrap}>
              Символ: {evidence.source.symbol}
            </Text>
          )}
          <Text size="xs" c="dimmed" style={wrap}>
            Снимок: {evidence.source.snapshotId}
          </Text>
          <Text size="xs" c="dimmed" style={wrap}>
            SHA-256: {evidence.source.contentHash}
          </Text>
          {evidence.snippet && (
            <Code block mt="xs" style={wrap}>
              {evidence.snippet}
            </Code>
          )}
        </div>
      ))}
      <Pages
        label="основания"
        cursors={cursors}
        next={page?.nextCursor}
        busy={query.isFetching}
        setCursors={setCursors}
      />
    </Stack>
  );
}

function AssertionDetails({
  record,
}: {
  record: Pick<BackendNode, "ownership" | "freshness"> | BackendComposedEdge;
}) {
  if ("source" in record) return null;
  return (
    <Stack gap="xs">
      {record.freshness && (
        <>
          <Badge color={record.freshness.status === "stale" ? "yellow" : "teal"}>
            {record.freshness.status === "stale" ? "Устарело" : "Подтверждено в снимке"}
          </Badge>
          <Text size="sm" style={wrap}>
            Причины: {record.freshness.reasons.join(", ") || "нет"}
          </Text>
          <Text size="xs" style={wrap}>
            Подтверждающий снимок: {record.freshness.confirmedSnapshotId}
          </Text>
        </>
      )}
      {record.ownership && (
        <Text size="xs" style={wrap}>
          Владелец: {record.ownership.repositoryId} · {record.ownership.providerNamespace} ·{" "}
          {record.ownership.profile}
        </Text>
      )}
    </Stack>
  );
}

export function BackendRecordInspector({
  projectId,
  revisionId,
  recordType,
  id,
}: Props & { recordType: "node" | "edge" | "evidence"; id: string }) {
  return (
    <RecordInspector
      key={`${projectId}:${revisionId}:${recordType}:${id}`}
      projectId={projectId}
      revisionId={revisionId}
      recordType={recordType}
      id={id}
    />
  );
}

function RecordInspector({
  projectId,
  revisionId,
  recordType,
  id,
}: Props & { recordType: "node" | "edge" | "evidence"; id: string }) {
  const depart = useBackendAPIDeparture();
  const [edge, setEdge] = useState<GraphEdge | null>(null);
  if (recordType === "node" && !edge)
    return (
      <NodeDetails
        projectId={projectId}
        revisionId={revisionId}
        nodeId={id}
        onEdge={(next) => {
          depart(() => setEdge(next));
        }}
      />
    );
  if (recordType === "evidence")
    return <EvidenceDetails projectId={projectId} revisionId={revisionId} evidenceId={id} />;
  return <EdgeDetails projectId={projectId} revisionId={revisionId} id={edge?.id ?? id} />;
}

function EdgeDetails({ projectId, revisionId, id }: Props & { id: string }) {
  const query = useGraphPage(projectId, { revisionId, recordType: "edges", id, limit: 1 });
  const edge = query.data?.status === 200 ? query.data.data.edges[0] : undefined;
  if (edge && "source" in edge)
    return (
      <BackendExactRecordInspector
        projectId={projectId}
        target={{ revisionId }}
        claimsSupported
        recordType="edge"
        id={edge.id}
      />
    );
  return (
    <Stack>
      <LoadState query={query} label="связи" />
      {edge && (
        <>
          <Text fw={600}>{edge.kind}</Text>
          <Code block style={wrap}>
            {edge.from} → {edge.to}
          </Code>
          <AssertionDetails record={edge} />
          <Code block style={wrap}>
            {JSON.stringify(edge.attributes, null, 2)}
          </Code>
          <EvidenceDetails projectId={projectId} revisionId={revisionId} subjectId={edge.id} />
        </>
      )}
    </Stack>
  );
}

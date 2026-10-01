import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Divider,
  Group,
  Loader,
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
  BackendNode,
  BackendRevisionCoverage,
  QueryBackendGraphRequest,
} from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";

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
];
const categories: Record<string, string> = {
  files: "Файлы",
  endpoints: "Точки входа",
  datastores: "Хранилища",
  migrations: "Миграции",
  producers: "Отправители событий",
  consumers: "Получатели событий",
  jobs: "Фоновые задачи",
  contracts: "Контракты",
  tests: "Тесты",
};
const statuses: Record<string, string> = {
  complete: "Полное",
  partial: "Частичное",
  unsupported: "Не поддерживается",
  excluded: "Исключено",
};
type Props = { projectId: string; revisionId: string; schemaVersion?: string };
type Selection = { type: "node"; id: string } | { type: "edge"; edge: BackendEdge };

export function BackendGraphInventory(props: Props) {
  return <Inventory key={`${props.projectId}:${props.revisionId}`} {...props} />;
}

function useGraphPage(projectId: string, input: QueryBackendGraphRequest) {
  return useQuery({
    queryKey: ["backend-graph", projectId, input],
    queryFn: ({ signal }) => queryBackendGraph(projectId, input, { signal }),
    staleTime: Infinity,
    retry: false,
  });
}

export function LoadState({
  query,
  label,
}: {
  query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown };
  label: string;
}) {
  return (
    <>
      {query.isPending && <Loader aria-label={`Загружаем ${label}`} />}
      {query.isError && (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(query.error)}
          <Button mt="sm" variant="light" onClick={() => void query.refetch()}>
            Повторить загрузку {label}
          </Button>
        </Alert>
      )}
    </>
  );
}

export function Pages({
  label,
  cursors,
  next,
  busy,
  setCursors,
}: {
  label: string;
  cursors: string[];
  next?: string;
  busy: boolean;
  setCursors: (update: (previous: string[]) => string[]) => void;
}) {
  if (cursors.length === 1 && !next) return null;
  return (
    <Group>
      <Button
        variant="default"
        disabled={busy || cursors.length === 1}
        onClick={() => setCursors((previous) => previous.slice(0, -1))}
      >
        Предыдущие {label}
      </Button>
      <Button
        variant="default"
        disabled={busy || !next}
        onClick={() => {
          if (next) setCursors((previous) => [...previous, next]);
        }}
      >
        Следующие {label}
      </Button>
    </Group>
  );
}

function Inventory({ projectId, revisionId, schemaVersion }: Props) {
  const coverageQuery = useGetBackendCoverage(projectId, revisionId, {
    query: { staleTime: Infinity, retry: false },
  });
  const coverage = coverageQuery.data?.status === 200 ? coverageQuery.data.data : undefined;
  const [searchDraft, setSearchDraft] = useState("");
  const [kindDraft, setKindDraft] = useState("");
  const [filter, setFilter] = useState({ search: "", kind: "" });
  const [cursors, setCursors] = useState([""]);
  const [selection, setSelection] = useState<Selection | null>(null);
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
          setFilter({ search: searchDraft.trim(), kind: kindDraft });
          setCursors([""]);
          setSelection(null);
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
                      schemaVersion === "3") &&
                    (["2", "3"].includes(schemaVersion ?? "") ||
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
              {node.freshness?.status === "stale" ? " · Устарело" : ""}
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
                onEdge={(edge) => setSelection({ type: "edge", edge })}
              />
            ) : (
              <>
                <Text fw={600}>{selection.edge.kind}</Text>
                <AssertionDetails record={selection.edge} />
                <Code block style={wrap}>
                  {selection.edge.from} → {selection.edge.to}
                </Code>
                <Code block style={wrap}>
                  {JSON.stringify(selection.edge.attributes, null, 2)}
                </Code>
                <EvidenceDetails
                  key={selection.edge.id}
                  projectId={projectId}
                  revisionId={revisionId}
                  subjectId={selection.edge.id}
                />
              </>
            )}
          </Stack>
        </Paper>
      )}
    </Stack>
  );
}

export function CoverageDetails({ data }: { data: BackendRevisionCoverage }) {
  return (
    <Stack gap="sm">
      <Group>
        <Badge color={data.coverage.status === "complete" ? "green" : "yellow"}>
          {data.coverage.status === "complete" ? "Полное покрытие" : "Частичное покрытие"}
        </Badge>
        <Text size="sm">
          Объектов: {data.coverage.knownObjects}.{" "}
          {data.coverage.denominator === null
            ? "Общее количество неизвестно."
            : `Всего: ${data.coverage.denominator}.`}
        </Text>
      </Group>
      {data.staleCounts && (
        <Text size="sm">
          Устаревшие: объекты {data.staleCounts.nodes} · связи {data.staleCounts.edges} · основания{" "}
          {data.staleCounts.evidence}
        </Text>
      )}
      {data.reconciliationGaps?.map((gap, index) => (
        <Text key={`reconcile:${index}`} size="sm" style={wrap}>
          {gap}
        </Text>
      ))}
      {data.coverage.gaps.map((gap, index) => (
        <Text key={index} size="sm" style={wrap}>
          {gap}
        </Text>
      ))}
      {data.snapshots.length === 0 && <Text c="dimmed">Исходный код ещё не импортирован.</Text>}
      {data.snapshots.map((snapshot) => (
        <div key={snapshot.id}>
          <Group gap="sm">
            <Text fw={600}>Снимок исходников</Text>
            <Badge color="gray">
              {snapshot.role === "retained_provenance"
                ? "Историческое основание"
                : "Основной снимок"}
            </Badge>
            <Badge color={snapshot.consistency === "verified" ? "teal" : "yellow"}>
              {snapshot.consistency === "verified"
                ? "Стабильность проверена агентом"
                : "Стабильность не проверена"}
            </Badge>
            {snapshot.dirty && <Badge color="gray">Рабочее дерево изменено</Badge>}
          </Group>
          <Text size="sm" style={wrap}>
            {snapshot.id}
          </Text>
          <Text size="sm" c="dimmed" style={wrap}>
            {snapshot.provider.name} · {snapshot.provider.version} ·{" "}
            {new Date(snapshot.capturedAt).toLocaleString("ru-RU")}
          </Text>
          <Text size="sm" style={wrap}>
            Хеш manifest: {snapshot.manifestHash}
          </Text>
        </div>
      ))}
      {data.inventory.length > 0 && (
        <Stack gap="xs" aria-label="Инвентаризация исходников">
          {data.inventory.map((item) => (
            <div key={item.category}>
              <Group gap="sm">
                <Text fw={500} size="sm">
                  {categories[item.category] ?? item.category}
                </Text>
                <Badge color={item.status === "complete" ? "teal" : "gray"}>
                  {statuses[item.status] ?? item.status}
                </Badge>
                <Text size="sm">
                  {item.knownCount}
                  {item.denominator === null ? " · всего неизвестно" : ` / ${item.denominator}`}
                </Text>
              </Group>
              <Text size="xs" c="dimmed" style={wrap}>
                Источник: {item.discoverySource}
              </Text>
              {item.reason && (
                <Text size="sm" style={wrap}>
                  {item.reason}
                </Text>
              )}
              {item.gaps.map((gap, index) => (
                <Text key={index} size="sm" c="dimmed" style={wrap}>
                  {gap}
                </Text>
              ))}
            </div>
          ))}
        </Stack>
      )}
    </Stack>
  );
}

function NodeDetails({
  projectId,
  revisionId,
  nodeId,
  onEdge,
}: Props & { nodeId: string; onEdge: (edge: BackendEdge) => void }) {
  const query = useGetBackendNode(projectId, revisionId, nodeId, {
    query: { staleTime: Infinity, retry: false },
  });
  const node: BackendNode | undefined = query.data?.status === 200 ? query.data.data : undefined;
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
}: Props & { nodeId: string; direction: "in" | "out"; onEdge: (edge: BackendEdge) => void }) {
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

function AssertionDetails({ record }: { record: Pick<BackendNode, "ownership" | "freshness"> }) {
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
  const [edge, setEdge] = useState<BackendEdge | null>(null);
  if (recordType === "node" && !edge)
    return (
      <NodeDetails projectId={projectId} revisionId={revisionId} nodeId={id} onEdge={setEdge} />
    );
  if (recordType === "evidence")
    return <EvidenceDetails projectId={projectId} revisionId={revisionId} evidenceId={id} />;
  return <EdgeDetails projectId={projectId} revisionId={revisionId} id={edge?.id ?? id} />;
}

function EdgeDetails({ projectId, revisionId, id }: Props & { id: string }) {
  const query = useGraphPage(projectId, { revisionId, recordType: "edges", id, limit: 1 });
  const edge = query.data?.status === 200 ? query.data.data.edges[0] : undefined;
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

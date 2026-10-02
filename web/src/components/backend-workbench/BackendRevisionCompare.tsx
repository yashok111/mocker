import { useRef, useState } from "react";
import {
  Badge,
  Button,
  Code,
  Group,
  NativeSelect,
  Paper,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import {
  compareBackendRevisions,
  useGetBackendCoverage,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendComparisonItem,
  BackendComparisonRef,
  BackendComparisonSummary,
  BackendCoverage,
  CompareBackendRevisionsRequest,
} from "@/api/generated/schemas";
import { BackendRecordInspector, LoadState, Pages } from "./BackendGraphInventory";
import { BackendArtifactReference } from "./BackendAPIArtifacts";
import type { BackendArtifactRef } from "@/api/generated/schemas";
import { useDatabaseCancellation } from "./backendDatabaseReads";
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
export function BackendRevisionCompare(props: { projectId: string; initialRevisionId: string }) {
  return <Compare key={props.projectId} {...props} />;
}
function Compare({
  projectId,
  initialRevisionId,
}: {
  projectId: string;
  initialRevisionId: string;
}) {
  const [fromDraft, setFromDraft] = useState(initialRevisionId);
  const [toDraft, setToDraft] = useState(initialRevisionId);
  const [recordType, setRecordType] = useState("");
  const [changeKind, setChangeKind] = useState("");
  const [pins, setPins] = useState<CompareBackendRevisionsRequest | null>(null);
  return (
    <Paper withBorder p="md">
      <Stack>
        <Title order={2}>Сравнение ревизий</Title>
        <Text size="sm" c="dimmed">
          Выберите два точных ID из истории ревизий. Сравнение описывает изменения модели и
          оснований.
        </Text>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            setPins({
              fromRevisionId: fromDraft.trim(),
              toRevisionId: toDraft.trim(),
              ...(recordType
                ? { recordType: recordType as CompareBackendRevisionsRequest["recordType"] }
                : {}),
              ...(changeKind
                ? { changeKind: changeKind as CompareBackendRevisionsRequest["changeKind"] }
                : {}),
            });
          }}
        >
          <Stack gap="sm">
            <SimpleGrid cols={{ base: 1, sm: 2 }}>
              <TextInput
                required
                label="Ревизия до"
                value={fromDraft}
                onChange={(event) => setFromDraft(event.currentTarget.value)}
              />
              <TextInput
                required
                label="Ревизия после"
                value={toDraft}
                onChange={(event) => setToDraft(event.currentTarget.value)}
              />
              <NativeSelect
                label="Тип изменения"
                value={recordType}
                onChange={(event) => setRecordType(event.currentTarget.value)}
                data={[
                  { value: "", label: "Все записи" },
                  ...["node", "edge", "evidence", "source", "identity", "artifact"].map(
                    (value) => ({
                      value,
                      label: value,
                    }),
                  ),
                ]}
              />
              <NativeSelect
                label="Характер изменения"
                value={changeKind}
                onChange={(event) => setChangeKind(event.currentTarget.value)}
                data={[
                  { value: "", label: "Все изменения" },
                  ...["added", "removed", "modified", "identity_mapped", "freshness_changed"].map(
                    (value) => ({ value, label: value }),
                  ),
                ]}
              />
            </SimpleGrid>
            <Button type="submit" disabled={!fromDraft.trim() || !toDraft.trim()} w="fit-content">
              Сравнить ревизии
            </Button>
          </Stack>
        </form>
        {pins && <Comparison key={JSON.stringify(pins)} projectId={projectId} pins={pins} />}
      </Stack>
    </Paper>
  );
}
function Comparison({
  projectId,
  pins,
}: {
  projectId: string;
  pins: CompareBackendRevisionsRequest;
}) {
  const [cursors, setCursors] = useState([""]);
  const [selected, setSelected] = useState<BackendComparisonItem | null>(null);
  const input = { ...pins, limit: 100, cursor: cursors.at(-1) ?? "" };
  const key = ["backend-comparison", projectId, JSON.stringify(input)];
  useDatabaseCancellation(key);
  const context = useRef<string | null>(null);
  const query = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      const response = await compareBackendRevisions(projectId, input, { signal });
      signal.throwIfAborted();
      if (response.status !== 200) throw new Error("Не удалось прочитать сравнение");
      const page = response.data;
      if (page.from.revisionId !== pins.fromRevisionId || page.to.revisionId !== pins.toRevisionId)
        throw new Error("Получен другой контекст сравнения ревизий");
      const identity = JSON.stringify([page.from, page.to, page.comparisonHash]);
      if (context.current !== null && context.current !== identity)
        throw new Error("Изменился контекст страниц сравнения ревизий");
      context.current = identity;
      return response;
    },
    staleTime: Infinity,
    retry: false,
  });
  const page = query.data?.status === 200 ? query.data.data : undefined;
  return (
    <Stack aria-live="polite">
      <LoadState query={query} label="сравнение" />
      {page && (
        <>
          <SimpleGrid cols={{ base: 1, sm: 2 }}>
            <Stack gap="xs">
              <Text fw={600}>До: {page.from.revisionId}</Text>
              <Code block style={wrap}>
                {page.from.semanticHash}
              </Code>
              <ComparisonCoverage coverage={page.coverageBefore} />
            </Stack>
            <Stack gap="xs">
              <Text fw={600}>После: {page.to.revisionId}</Text>
              <Code block style={wrap}>
                {page.to.semanticHash}
              </Code>
              <ComparisonCoverage coverage={page.coverageAfter} />
            </Stack>
          </SimpleGrid>
          <ComparisonSummary summary={page.summary} />
          {page.limitations.map((item, i) => (
            <Text key={i} size="sm" c="dimmed" style={wrap}>
              {item}
            </Text>
          ))}
          <Text size="xs" style={wrap}>
            Хеш сравнения: {page.comparisonHash}
          </Text>
          {page.items.length === 0 && <Text>Различий нет</Text>}
          {page.items.map((item) => (
            <Button
              key={`${item.recordType}:${item.id}`}
              variant={selected === item ? "light" : "subtle"}
              aria-label={`Открыть изменение ${item.recordType} ${item.id}`}
              aria-pressed={selected?.recordType === item.recordType && selected.id === item.id}
              h="auto"
              py="sm"
              justify="flex-start"
              styles={{ label: { ...wrap, textAlign: "left" } }}
              onClick={() => setSelected(item)}
            >
              {item.recordType} ·{" "}
              {item.nameAfter ?? item.nameBefore ?? item.keyAfter ?? item.keyBefore ?? item.id} ·{" "}
              {item.changeKinds.join(", ")}
            </Button>
          ))}
          <Pages
            label="изменения"
            cursors={cursors}
            next={page.nextCursor}
            busy={query.isFetching}
            setCursors={(update) => {
              setSelected(null);
              setCursors(update);
            }}
          />
        </>
      )}
      {selected && (
        <Stack>
          <Group justify="space-between">
            <Title order={3}>Изменение</Title>
            <Button variant="subtle" onClick={() => setSelected(null)}>
              Закрыть изменение
            </Button>
          </Group>
          <Text size="sm" style={wrap}>
            Изменённые свойства: {selected.changedPaths.join(", ") || "нет"}
          </Text>
          {selected.contextChanged && <Text>Контекст всего артефакта изменился.</Text>}
          <SimpleGrid cols={{ base: 1, md: 2 }}>
            <ComparisonSide
              label="До изменения"
              reference={selected.before}
              artifact={selected.artifactBefore}
            />
            <ComparisonSide
              label="После изменения"
              reference={selected.after}
              artifact={selected.artifactAfter}
            />
          </SimpleGrid>
        </Stack>
      )}
    </Stack>
  );
}
export function ComparisonSummary({ summary }: { summary: BackendComparisonSummary }) {
  return (
    <Stack gap="xs">
      {(
        [
          ["Объекты", summary.nodes],
          ["Связи", summary.edges],
          ["Основания", summary.evidence],
        ] as const
      ).map(([label, count]) => (
        <Text key={label} size="sm">
          {label}: добавлено {count.added} · удалено {count.removed} · изменено {count.modified}
        </Text>
      ))}
      {summary.artifacts && (
        <Text size="sm">
          Артефакты API: добавлено {summary.artifacts.added} · удалено {summary.artifacts.removed} ·
          изменено {summary.artifacts.modified}
        </Text>
      )}
      <Text size="sm">
        Исходники {summary.sourceChanges} · идентичность {summary.identityMappings} · свежесть{" "}
        {summary.freshnessChanges}
      </Text>
    </Stack>
  );
}
function ComparisonCoverage({ coverage }: { coverage: BackendCoverage }) {
  return (
    <Stack gap="xs">
      <Badge color={coverage.status === "complete" ? "teal" : "yellow"}>
        {coverage.status === "complete" ? "Полное покрытие" : "Частичное покрытие"}
      </Badge>
      <Text size="sm">
        Объектов {coverage.knownObjects} ·{" "}
        {coverage.denominator === null ? "всего неизвестно" : `всего ${coverage.denominator}`}
      </Text>
      {coverage.gaps.map((gap, i) => (
        <Text key={i} size="sm" style={wrap}>
          {gap}
        </Text>
      ))}
    </Stack>
  );
}
function ComparisonSide({
  label,
  reference,
  artifact,
}: {
  label: string;
  reference: BackendComparisonRef | null;
  artifact?: BackendArtifactRef;
}) {
  return (
    <Paper component="section" aria-label={label} withBorder p="sm" style={{ minWidth: 0 }}>
      <Stack>
        <Title order={4}>{label}</Title>
        {reference ? (
          <>
            <Text size="xs" style={wrap}>
              Ревизия: {reference.revisionId}
            </Text>
            {reference.recordType === "source" ? (
              <SourceDetails
                key={`${reference.projectId}:${reference.revisionId}:${reference.snapshotId}:${reference.path}`}
                reference={reference}
              />
            ) : reference.recordType === "artifact" ? (
              artifact ? (
                <BackendArtifactReference
                  reference={artifact}
                  projectId={reference.projectId}
                  revisionId={reference.revisionId}
                  sourceNodeId={reference.id}
                />
              ) : (
                <Text>Точная ссылка артефакта отсутствует в сравнении.</Text>
              )
            ) : (
              <BackendRecordInspector
                projectId={reference.projectId}
                revisionId={reference.revisionId}
                recordType={reference.recordType}
                id={reference.id}
              />
            )}
          </>
        ) : (
          <Text>{label === "До изменения" ? "До" : "После"}: отсутствует</Text>
        )}
      </Stack>
    </Paper>
  );
}
function SourceDetails({
  reference,
}: {
  reference: Extract<BackendComparisonRef, { recordType: "source" }>;
}) {
  const query = useGetBackendCoverage(reference.projectId, reference.revisionId, {
    query: { staleTime: Infinity, retry: false },
  });
  const data = query.data?.status === 200 ? query.data.data : undefined;
  const snapshot = data?.snapshots.find((item) => item.id === reference.snapshotId);
  const file = snapshot?.files.find((item) => item.path === reference.path);
  return (
    <Stack>
      <LoadState query={query} label="исходников" />
      {snapshot && (
        <>
          <Text size="sm" style={wrap}>
            Снимок: {snapshot.id} · {snapshot.role ?? "primary"}
          </Text>
          <Text size="sm" style={wrap}>
            Manifest: {snapshot.manifestHash}
          </Text>
          <Text size="sm">
            {snapshot.provider.name} · {snapshot.provider.version} · {snapshot.consistency}
          </Text>
        </>
      )}
      {file && (
        <>
          <Text style={wrap}>{file.path}</Text>
          <Text size="sm">
            {file.analysisStatus} · {file.fileType}
          </Text>
          <Code block style={wrap}>
            {file.contentHash}
          </Code>
          {file.reason && <Text size="sm">{file.reason}</Text>}
        </>
      )}
      {data && !file && <Text>Файл отсутствует в закреплённом снимке</Text>}
    </Stack>
  );
}

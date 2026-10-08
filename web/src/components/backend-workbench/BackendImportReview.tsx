import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  commitBackendImport,
  getBackendImport,
  getBackendImportChanges,
  getGetBackendProjectQueryKey,
  getListBackendProjectsQueryKey,
  listBackendImports,
  previewBackendImport,
  useGetBackendProject,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendComposedImportChangeItem,
  BackendImportPreviewResponse,
  CommitBackendImportRequest,
  GetBackendImportChangesRecordType,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import { BackendRecordInspector, LoadState, Pages } from "./BackendGraphInventory";
import { ComparisonSummary } from "./BackendRevisionCompare";
import { BackendIncrementalSync } from "./BackendIncrementalSync";
import { BackendImportPreflight } from "./BackendImportPreflight";
import type { ImportCandidateTarget } from "./backendImportAttempts";

const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
type ImportReviewProps = {
  projectId: string;
  currentRevisionId: string;
  onDirty?: (value: boolean) => void;
  onCandidateChange?: (target: ImportCandidateTarget | null) => void;
  onCommitted?: (revisionId: string) => void;
};
export function BackendImportReview(props: ImportReviewProps) {
  return <ImportReview key={props.projectId} {...props} />;
}
function ImportReview({
  projectId,
  currentRevisionId,
  onDirty,
  onCandidateChange,
  onCommitted,
}: ImportReviewProps) {
  const [cursors, setCursors] = useState([""]);
  const [selected, setSelected] = useState<{ id?: string; composed: boolean } | null>(null);
  const [locked, setLocked] = useState(false);
  const [setupVersion, setSetupVersion] = useState(0);
  const onLock = useCallback(
    (value: boolean) => {
      setLocked(value);
      onDirty?.(value);
    },
    [onDirty],
  );
  const cursor = cursors.at(-1) ?? "";
  const query = useQuery({
    queryKey: ["backend-imports", projectId, cursor],
    queryFn: ({ signal }) => listBackendImports(projectId, { cursor, limit: 100 }, { signal }),
    retry: false,
  });
  const page = query.data?.status === 200 ? query.data.data : undefined;
  return (
    <Paper withBorder p="md">
      <Stack>
        <Title order={2}>Импорты исходников</Title>
        <Text size="sm" c="dimmed">
          Создайте синхронизацию из выбранных JSON-файлов или откройте подготовленную сессию.
        </Text>
        <Button
          variant="default"
          disabled={locked}
          onClick={() => {
            setSetupVersion((value) => value + 1);
            setSelected({ composed: true });
          }}
        >
          Новая синхронизация
        </Button>
        <LoadState query={query} label="импортов" />
        {page?.items.length === 0 && <Text>Сессий импорта пока нет</Text>}
        {page?.items.map((session) => (
          <Button
            key={session.id}
            variant={selected?.id === session.id ? "light" : "subtle"}
            aria-label={`Открыть импорт ${session.id}`}
            aria-pressed={selected?.id === session.id}
            disabled={locked && selected?.id !== session.id}
            h="auto"
            py="sm"
            styles={{ label: wrap }}
            onClick={() => setSelected({ id: session.id, composed: session.mode === "composed" })}
          >
            {session.manifest.repositoryName} · {session.state} · {session.id}
          </Button>
        ))}
        <Pages
          label="импорты"
          cursors={cursors}
          next={page?.nextCursor}
          busy={query.isFetching || locked}
          setCursors={setCursors}
        />
        {selected?.composed && (
          <BackendIncrementalSync
            key={`${projectId}:${selected.id ?? `new-${setupVersion}`}`}
            projectId={projectId}
            initialSessionId={selected.id}
            onDirty={onLock}
            onCandidateChange={onCandidateChange}
            onCommitted={onCommitted}
          />
        )}
        {selected && !selected.composed && selected.id && (
          <ImportSessionReview
            key={`${projectId}:${selected.id}`}
            projectId={projectId}
            sessionId={selected.id}
            currentRevisionId={currentRevisionId}
            onLock={onLock}
          />
        )}
      </Stack>
    </Paper>
  );
}
function ImportSessionReview({
  projectId,
  sessionId,
  currentRevisionId,
  onLock,
}: {
  projectId: string;
  sessionId: string;
  currentRevisionId: string;
  onLock: (value: boolean) => void;
}) {
  const client = useQueryClient();
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors.at(-1) ?? "";
  const statusKey = ["backend-import-status", projectId, sessionId, cursor];
  const query = useQuery({
    queryKey: statusKey,
    queryFn: ({ signal }) =>
      getBackendImport(projectId, sessionId, { cursor, limit: 100 }, { signal }),
    retry: false,
    staleTime: 0,
    refetchOnMount: "always",
  });
  const projectQuery = useGetBackendProject(projectId, {
    query: { retry: false, staleTime: 0, refetchOnMount: "always" },
  });
  const project = projectQuery.data?.status === 200 ? projectQuery.data.data : undefined;
  const status = query.data?.status === 200 ? query.data.data : undefined;
  const session = status?.session;
  const [newestVersion, setNewestVersion] = useState(0);
  if (session && session.version > newestVersion) setNewestVersion(session.version);
  const olderStatus = !!session && session.version < newestVersion;
  const preview = status?.preview;
  const [selectedBase, setSelectedBase] = useState<string | null>(null);
  const [reviewedProject, setReviewedProject] = useState<{
    version: number;
    revisionId: string;
  } | null>(null);
  const [baseSelected, setBaseSelected] = useState(true);
  const [conflicted, setConflicted] = useState(false);
  const [attempt, setAttempt] = useState<CommitBackendImportRequest | null>(null);
  const [commitUncertain, setUncertain] = useState(false);
  const [committedRevision, setCommittedRevision] = useState<string | null>(null);
  const [failure, setFailure] = useState<unknown>(null);
  if (project && !projectQuery.isFetching && !projectQuery.isError && !reviewedProject)
    setReviewedProject({ version: project.version, revisionId: project.currentRevisionId });
  const freshTerminal =
    !query.isFetching &&
    !query.isError &&
    !olderStatus &&
    (!!status?.committedRevisionId || session?.state === "aborted");
  const uncertain = commitUncertain && !freshTerminal;
  useEffect(() => {
    if (freshTerminal) onLock(false);
  }, [freshTerminal, onLock]);
  const headChanged =
    !!project &&
    !!reviewedProject &&
    (project.version !== reviewedProject.version ||
      project.currentRevisionId !== reviewedProject.revisionId);
  const base = selectedBase ?? session?.baseRevisionId ?? currentRevisionId;
  const terminal =
    session?.state === "committed" || session?.state === "aborted" || !!committedRevision;
  const busy = query.isFetching || projectQuery.isFetching;
  const ready =
    !olderStatus &&
    !query.isError &&
    !projectQuery.isError &&
    !busy &&
    !headChanged &&
    !conflicted &&
    !uncertain &&
    !!project &&
    !!reviewedProject &&
    !!session &&
    base.trim() === session.baseRevisionId &&
    !!preview &&
    preview.sessionId === sessionId &&
    preview.version === session.version &&
    preview.state === "ready" &&
    session.state === "ready" &&
    !!preview.candidateHash &&
    preview.candidateHash === session.candidateHash &&
    session.baseRevisionId === project.currentRevisionId;
  const refresh = async () => {
    await Promise.all([query.refetch(), projectQuery.refetch()]);
  };
  const previewMutation = useMutation({
    mutationFn: async () => {
      if (!session) return;
      const result = await previewBackendImport(projectId, sessionId, {
        baseRevisionId: base.trim(),
        expectedImportVersion: session.version,
      });
      if (result.status === 200) {
        await refresh();
        if (project)
          setReviewedProject({ version: project.version, revisionId: project.currentRevisionId });
        setConflicted(false);
        setFailure(null);
      }
    },
    onError: (error) => {
      setFailure(error);
      if (error instanceof ApiFailure && error.status === 409) {
        setConflicted(true);
        setBaseSelected(false);
      }
      void refresh();
    },
    retry: false,
  });
  const commitMutation = useMutation({
    mutationFn: async (input: CommitBackendImportRequest) => {
      setFailure(null);
      onLock(true);
      try {
        const result = await commitBackendImport(projectId, sessionId, input);
        if (result.status === 200) {
          setCommittedRevision(result.data.revision.id);
          setUncertain(false);
          onLock(false);
        }
        await client.invalidateQueries({ queryKey: getGetBackendProjectQueryKey(projectId) });
        await client.invalidateQueries({ queryKey: getListBackendProjectsQueryKey() });
        await query.refetch();
      } catch (error) {
        setFailure(error);
        if (error instanceof ApiFailure && error.status === 409) {
          setConflicted(true);
          setBaseSelected(false);
          setUncertain(false);
          onLock(false);
          await refresh();
          return;
        }
        setUncertain(true);
        try {
          const recovered = await getBackendImport(projectId, sessionId);
          if (recovered.status === 200) {
            client.setQueryData(statusKey, recovered);
            if (recovered.data.committedRevisionId) {
              setCommittedRevision(recovered.data.committedRevisionId);
              setUncertain(false);
              setFailure(null);
              onLock(false);
            }
          }
        } catch {
          /* Preserve the original attempt until the outcome can be read or replayed. */
        }
      }
    },
    retry: false,
  });
  const receipt = committedRevision ?? status?.committedRevisionId;
  return (
    <Stack component="section" aria-label="Проверка импорта">
      <LoadState query={query} label="статуса импорта" />
      <LoadState query={projectQuery} label="версии проекта" />
      <Group>
        <Button
          variant="default"
          disabled={commitMutation.isPending || previewMutation.isPending}
          onClick={() => void refresh()}
        >
          Обновить статус импорта
        </Button>
        {session && (
          <Badge>
            {session.mode ?? "initial"} · {session.state} · версия {session.version}
          </Badge>
        )}
      </Group>
      {session && (
        <>
          <Text size="sm" style={wrap}>
            Сессия: {session.id}
          </Text>
          <Text size="sm" style={wrap}>
            Базовая ревизия: {session.baseRevisionId}
          </Text>
          {session.graphScope && (
            <Text size="sm">
              Покрытие графа: {session.graphScope.status} · {session.graphScope.gaps.join("; ")}
            </Text>
          )}
          {session.inventory
            .flatMap((item) => item.gaps)
            .map((gap, i) => (
              <Text key={i} size="sm">
                {gap}
              </Text>
            ))}
        </>
      )}
      {receipt && (
        <Alert color="green" component="output" style={wrap}>
          Импорт сохранён в ревизии {receipt}
        </Alert>
      )}
      {!!failure && !receipt && (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(failure)}
        </Alert>
      )}
      {olderStatus && (
        <Alert color="yellow">Получена более старая версия сессии. Перечитайте статус.</Alert>
      )}
      {headChanged && !receipt && (
        <Alert color="yellow">
          Версия проекта изменилась. Выберите базовую ревизию явно и выполните новый Preview.
        </Alert>
      )}
      {!terminal && (
        <>
          <TextInput
            label="Базовая ревизия Preview"
            value={base}
            disabled={uncertain || commitMutation.isPending || previewMutation.isPending}
            onChange={(event) => {
              setSelectedBase(event.currentTarget.value);
              setBaseSelected(true);
            }}
          />
          <Button
            variant="default"
            disabled={
              uncertain || commitMutation.isPending || previewMutation.isPending || !project
            }
            onClick={() => {
              if (project) {
                setSelectedBase(project.currentRevisionId);
                setBaseSelected(true);
              }
            }}
          >
            Выбрать текущую базовую ревизию
          </Button>
          <Group>
            <Button
              disabled={
                olderStatus ||
                !base.trim() ||
                !session ||
                query.isError ||
                projectQuery.isError ||
                busy ||
                uncertain ||
                commitMutation.isPending ||
                previewMutation.isPending ||
                ((headChanged || conflicted) && !selectedBase) ||
                !baseSelected
              }
              loading={previewMutation.isPending}
              onClick={() => previewMutation.mutate()}
            >
              Preview
            </Button>
            <Button
              disabled={!ready || commitMutation.isPending || previewMutation.isPending}
              loading={commitMutation.isPending}
              onClick={() => {
                if (ready && preview?.candidateHash && reviewedProject && session) {
                  const input = {
                    expectedVersion: reviewedProject.version,
                    expectedImportVersion: session.version,
                    candidateHash: preview.candidateHash,
                    idempotencyKey: crypto.randomUUID(),
                  };
                  setAttempt(input);
                  commitMutation.mutate(input);
                }
              }}
            >
              Commit
            </Button>
          </Group>
          {!ready && !uncertain && (
            <Text size="sm">
              Требуется новый Preview с актуальными версиями, выбранной базой и готовым хешем.
            </Text>
          )}
          {uncertain && (
            <Alert color="yellow">
              <Text>
                Исход Commit пока неизвестен. Повтор использует исходные версии, хеш и ключ.
              </Text>
              <Button
                disabled={commitMutation.isPending || !attempt}
                onClick={() => {
                  if (attempt) commitMutation.mutate(attempt);
                }}
              >
                Повторить исходный Commit
              </Button>
            </Alert>
          )}
        </>
      )}
      {preview && (
        <SavedPreview
          key={`${sessionId}:${preview.version}:${preview.candidateHash}`}
          projectId={projectId}
          sessionId={sessionId}
          preview={preview}
        />
      )}
      {status && (
        <Stack gap="xs">
          <Title order={3}>Принятые пакеты</Title>
          {status.acceptedBatches.map((batch) => (
            <Text key={batch.batchId} size="sm" style={wrap}>
              {batch.batchId} · версия {batch.acceptedVersion}
            </Text>
          ))}
          <Pages
            label="пакеты"
            cursors={cursors}
            next={status.nextCursor}
            busy={busy || uncertain || commitMutation.isPending}
            setCursors={setCursors}
          />
        </Stack>
      )}
    </Stack>
  );
}
function SavedPreview({
  projectId,
  sessionId,
  preview,
}: {
  projectId: string;
  sessionId: string;
  preview: BackendImportPreviewResponse;
}) {
  const [recordType, setRecordType] = useState<GetBackendImportChangesRecordType>("source");
  const [cursors, setCursors] = useState([""]);
  const query = useQuery({
    queryKey: [
      "backend-import-changes",
      projectId,
      sessionId,
      preview.version,
      preview.candidateHash,
      recordType,
      cursors.at(-1),
    ],
    queryFn: ({ signal }) =>
      getBackendImportChanges(
        projectId,
        sessionId,
        { previewVersion: preview.version, recordType, limit: 100, cursor: cursors.at(-1) ?? "" },
        { signal },
      ),
    retry: false,
  });
  const page =
    query.data?.status === 200 &&
    query.data.data.sessionId === sessionId &&
    query.data.data.previewVersion === preview.version &&
    query.data.data.candidateHash === preview.candidateHash
      ? query.data.data
      : undefined;
  return (
    <Stack>
      <Title order={3}>Сохранённый Preview</Title>
      <Text size="sm" style={wrap}>
        {preview.sessionId} · версия {preview.version} · {preview.state}
      </Text>
      <Code block style={wrap}>
        {preview.candidateHash ?? "Готовый хеш отсутствует"}
      </Code>
      <Text size="sm">
        Объекты {preview.summary.nodes} · связи {preview.summary.edges} · основания{" "}
        {preview.summary.evidence} · не разрешено {preview.summary.unresolved}
      </Text>
      <Text size="sm">
        Исходники {preview.sourceChangeCount ?? 0} · идентичность{" "}
        {preview.identityDecisionCount ?? 0} · удаления {preview.deletionDecisionCount ?? 0}
      </Text>
      {preview.comparisonSummary && <ComparisonSummary summary={preview.comparisonSummary} />}
      {preview.preflight && <BackendImportPreflight value={preview.preflight} />}
      {preview.diagnostics.map((item, i) => (
        <Alert key={i} color="yellow" style={wrap}>
          {item.code} · {item.path}: {item.message}
        </Alert>
      ))}
      <NativeSelect
        label="Детали Preview"
        value={recordType}
        data={[
          { value: "source", label: "Исходники" },
          { value: "identity", label: "Идентичность" },
          { value: "deletion", label: "Удаления" },
        ]}
        onChange={(event) => {
          setRecordType(event.currentTarget.value as GetBackendImportChangesRecordType);
          setCursors([""]);
        }}
      />
      <LoadState query={query} label="деталей Preview" />
      {page?.items.length === 0 && <Text c="dimmed">Изменений этого типа нет</Text>}
      {page?.items.map((item, i) => (
        <SavedChange
          key={`${recordType}:${cursors.at(-1)}:${i}`}
          projectId={projectId}
          item={item}
        />
      ))}
      <Pages
        label="детали Preview"
        cursors={cursors}
        next={page?.nextCursor}
        busy={query.isFetching}
        setCursors={setCursors}
      />
    </Stack>
  );
}
function SavedChange({
  projectId,
  item,
}: {
  projectId: string;
  item: BackendComposedImportChangeItem;
}) {
  const [selection, setSelection] = useState<{
    revisionId: string;
    recordType: "node" | "edge" | "evidence";
    id: string;
  } | null>(null);
  if (item.recordType === "source")
    return (
      <Paper withBorder p="sm">
        <Text style={wrap}>
          {item.source.path} · {item.source.kind}
        </Text>
        <Text size="sm">
          Добавление подтверждено: {item.source.additionConfirmed ? "да" : "нет"} · удаление
          подтверждено: {item.source.deletionConfirmed ? "да" : "нет"}
        </Text>
        <Code block style={wrap}>
          {JSON.stringify({ before: item.source.before, after: item.source.after }, null, 2)}
        </Code>
      </Paper>
    );
  if (item.recordType !== "identity" && item.recordType !== "deletion")
    return (
      <Code block style={wrap}>
        {JSON.stringify(item, null, 2)}
      </Code>
    );
  const decision = item.recordType === "identity" ? item.identity : item.deletion;
  const old = decision.oldSubject;
  return (
    <Paper withBorder p="sm">
      <Stack>
        <Text style={wrap}>
          {decision.command.reason} · {decision.resolved ? "Разрешено" : "Требует решения"}
        </Text>
        <Code block style={wrap}>
          {JSON.stringify(decision.command, null, 2)}
        </Code>
        {old && (
          <Button
            variant="light"
            onClick={() =>
              setSelection({ revisionId: old.revisionId, recordType: old.recordType, id: old.id })
            }
          >
            Открыть прежний объект
          </Button>
        )}
        {decision.oldEvidenceRefs.map((ref) => (
          <Button
            key={`${ref.revisionId}:${ref.evidenceId}`}
            variant="subtle"
            aria-label={`Открыть прежнее основание ${ref.evidenceId}`}
            onClick={() =>
              setSelection({
                revisionId: ref.revisionId,
                recordType: "evidence",
                id: ref.evidenceId,
              })
            }
          >
            Прежнее основание {ref.evidenceId}
          </Button>
        ))}
        {selection && <BackendRecordInspector projectId={projectId} {...selection} />}
      </Stack>
    </Paper>
  );
}

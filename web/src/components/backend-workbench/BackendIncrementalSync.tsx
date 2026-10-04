import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Badge, Button, Checkbox, Code, Group, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useGetBackendProject } from "@/api/generated/backend-projects/backend-projects";
import type { BackendImportCommand, BackendProviderAssertion } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
import { BackendSourceScopeForm } from "./BackendSourceScopeForm";
import { BackendSourceFileInput } from "./BackendSourceFileInput";
import { BackendClaimIdentityForm } from "./BackendAssertionDecisionForm";
import { BackendSyncPreview, conflictAddress } from "./BackendSyncPreview";
import { LoadState } from "./BackendGraphInventory";
import {
  captureImportAttempt,
  captureImportBatch,
  captureImportCommit,
  candidateTarget,
  type ImportCandidateTarget,
} from "./backendImportAttempts";
import { maxCollectorBytes, parseCollectorBatch } from "./backendSourceInputs";
import { loadSyncAssertions, loadSyncBase } from "./backendSyncReads";
import { useBackendImportFlow } from "./useBackendImportFlow";

export type BackendIncrementalSyncProps = {
  projectId: string;
  initialSessionId?: string;
  onDirty?: (value: boolean) => void;
  onCandidateChange?: (target: ImportCandidateTarget | null) => void;
  onCommitted?: (revisionId: string) => void;
};
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };

export function BackendIncrementalSync(props: BackendIncrementalSyncProps) {
  const [generation, setGeneration] = useState(0);
  const [recoveryContext, setRecoveryContext] = useState<{
    requested: string;
    sessionId?: string;
  }>();
  const requested = `${props.projectId}/${props.initialSessionId ?? "new"}`;
  const initialSessionId =
    recoveryContext?.requested === requested ? recoveryContext.sessionId : props.initialSessionId;
  return (
    <SourceSync
      key={`${props.projectId}/${initialSessionId ?? "new"}/${generation}`}
      {...props}
      initialSessionId={initialSessionId}
      onReset={() => setGeneration((value) => value + 1)}
      onRecoveryContext={(sessionId) => {
        setRecoveryContext({ requested, sessionId });
        setGeneration((value) => value + 1);
      }}
    />
  );
}
function SourceSync({
  onReset,
  onRecoveryContext,
  ...props
}: BackendIncrementalSyncProps & {
  onReset: () => void;
  onRecoveryContext: (sessionId?: string) => void;
}) {
  const { onDirty } = props;
  const flow = useBackendImportFlow(props);
  const projectQuery = useGetBackendProject(props.projectId, {
    query: { retry: false, staleTime: 0, refetchOnMount: "always" },
  });
  const project = projectQuery.data?.status === 200 ? projectQuery.data.data : undefined;
  const [basePin, setBasePin] = useState<{ revisionId: string; version: number } | null>(null);
  if (!basePin && project && !projectQuery.isFetching && !projectQuery.isError)
    setBasePin({ revisionId: project.currentRevisionId, version: project.version });
  const baseId = flow.session?.baseRevisionId ?? basePin?.revisionId;
  const baseQuery = useQuery({
    queryKey: ["backend-sync-base", props.projectId, baseId],
    enabled: !!baseId && (!flow.sessionId || !!flow.session),
    queryFn: ({ signal }) => loadSyncBase(props.projectId, baseId!, signal),
    retry: false,
  });
  const base = baseQuery.data;
  const [setupDirty, setSetupDirty] = useState(false);
  const [collector, setCollector] = useState<BackendImportCommand[]>();
  const [claims, setClaims] = useState<BackendImportCommand[]>([]);
  const [collectorKey, setCollectorKey] = useState(0);
  const [collectorLoading, setCollectorLoading] = useState(false);
  const [preparing, setPreparing] = useState(false);
  const preparingRef = useRef(false);
  const collectorAttempt = useRef("");
  const [reviewed, setReviewed] = useState<{
    version: number;
    hash: string;
    projectVersion: number;
  } | null>(null);
  const [abortConfirm, setAbortConfirm] = useState(false);
  const [discardRecoveryConfirm, setDiscardRecoveryConfirm] = useState(false);
  const [failure, setFailure] = useState<unknown>(null);
  const [offers, setOffers] = useState<BackendProviderAssertion[]>([]);
  const [offerCursor, setOfferCursor] = useState("");
  const [offerLoading, setOfferLoading] = useState(false);
  const [offerMessage, setOfferMessage] = useState("");
  const offerGeneration = useRef(0);
  const [lastChoices, setLastChoices] = useState<Record<string, string>>({});
  const outcomeRef = useRef<HTMLElement>(null);
  useEffect(() => {
    if (flow.terminal) outcomeRef.current?.focus();
  }, [flow.terminal]);
  const dirty =
    !flow.terminal &&
    ((setupDirty && !flow.session) ||
      !!collector ||
      !!claims.length ||
      collectorLoading ||
      preparing ||
      flow.busy ||
      flow.uncertain ||
      flow.attempt?.state === "unsent" ||
      flow.hasRecoveryRecord ||
      abortConfirm);
  useEffect(() => {
    onDirty?.(dirty);
  }, [dirty, onDirty]);
  useEffect(
    () => () => {
      offerGeneration.current++;
      onDirty?.(false);
    },
    [onDirty],
  );
  useEffect(() => {
    // oxlint-disable-next-line react/set-state-in-effect -- A new server version invalidates reviewed pins and in-flight assertion reads.
    setReviewed(null);
    setOfferCursor("");
    setOffers([]);
    setOfferLoading(false);
    setOfferMessage("");
    offerGeneration.current++;
  }, [flow.session?.version, flow.preview?.version]);
  useEffect(() => {
    if (flow.receipt?.batchId === collectorAttempt.current) {
      setCollector(undefined);
      setClaims([]);
      setCollectorKey((value) => value + 1);
      collectorAttempt.current = "";
    }
  }, [flow.receipt]);
  const busy = flow.busy || preparing;
  const disabled = busy || flow.uncertain || flow.terminal || flow.recoveryBlocked;
  const attemptFailure =
    flow.attempt?.error === flow.recoveryError ? undefined : flow.attempt?.error;
  const projectChanged =
    !!project &&
    !!basePin &&
    (project.currentRevisionId !== basePin.revisionId || project.version !== basePin.version);
  const headChanged =
    !!project && !!flow.session && project.currentRevisionId !== flow.session.baseRevisionId;
  const target = flow.session && flow.preview ? candidateTarget(flow.session, flow.preview) : null;
  const canCommit =
    !!target &&
    !!reviewed &&
    !!project &&
    !!flow.session &&
    flow.fresh &&
    !flow.conflict &&
    !disabled &&
    !projectQuery.isFetching &&
    !projectQuery.isError &&
    !headChanged &&
    reviewed.projectVersion === project.version &&
    reviewed.version === flow.session.version &&
    reviewed.hash === target.importCandidate.candidateHash;
  async function reload() {
    setFailure(null);
    setReviewed(null);
    const [ok, freshProject] = await Promise.all([flow.reload(), projectQuery.refetch()]);
    if (ok && !freshProject.isError && freshProject.data?.status === 200) {
      setBasePin({
        revisionId: flow.session?.baseRevisionId ?? freshProject.data.data.currentRevisionId,
        version: freshProject.data.data.version,
      });
      flow.acknowledgeReload();
    }
  }
  async function resetBase() {
    if (disabled) return;
    const result = await projectQuery.refetch();
    if (result.data?.status === 200 && !result.isError) onReset();
  }
  async function sendCollector() {
    if (
      !flow.session ||
      !collector ||
      disabled ||
      collectorLoading ||
      !flow.fresh ||
      flow.conflict ||
      preparingRef.current
    )
      return;
    preparingRef.current = true;
    setPreparing(true);
    setFailure(null);
    try {
      const attempt = await captureImportBatch(flow.session, [...claims, ...collector]);
      collectorAttempt.current = attempt.batchId;
      await flow.execute(attempt);
    } catch (error) {
      setFailure(error);
    } finally {
      preparingRef.current = false;
      setPreparing(false);
    }
  }
  const handleSetupDirty = useCallback((value: boolean) => setSetupDirty(value), []);
  async function loadOffers(recordType: "node" | "edge", more = false) {
    if (!base || !flow.session || disabled || !flow.fresh) return;
    const token = ++offerGeneration.current;
    setOfferLoading(true);
    setOfferMessage("");
    try {
      const page = await loadSyncAssertions(
        base,
        flow.session,
        flow.preview,
        recordType,
        more ? offerCursor : "",
      );
      if (token !== offerGeneration.current) return;
      setOffers(page.items);
      setOfferCursor(page.nextCursor);
      if (!page.items.length)
        setOfferMessage("На этой странице нет прежних утверждений нужного владельца и типа.");
    } catch (error) {
      if (token === offerGeneration.current) {
        setOffers([]);
        setOfferCursor("");
        setOfferMessage(
          error instanceof Error ? error.message : "Не удалось прочитать утверждения.",
        );
      }
    } finally {
      if (token === offerGeneration.current) setOfferLoading(false);
    }
  }
  return (
    <Stack component="section" aria-label="Синхронизация источников" gap="lg">
      <Title order={2}>Синхронизация источников</Title>
      <LoadState query={projectQuery} label="версии проекта" />
      <LoadState query={baseQuery} label="базы синхронизации" />
      {flow.recoveryLoading && <Text>Проверка сохранённого запроса…</Text>}
      {flow.sessionId && !flow.session && <Text>Чтение сессии {flow.sessionId}</Text>}
      {!flow.session && !flow.sessionId && base && basePin && (
        <BackendSourceScopeForm
          key={`${basePin.revisionId}/${basePin.version}`}
          baseRevisionId={basePin.revisionId}
          projectVersion={basePin.version}
          baseSchema={String(base.revision.schemaVersion)}
          emptyBase={base.revision.sourceSnapshotIds.length === 0}
          partitions={base.partitions}
          disabled={disabled || projectChanged || projectQuery.isFetching}
          onDirty={handleSetupDirty}
          onBegin={(body) => {
            void flow.execute(
              captureImportAttempt({ kind: "begin", projectId: props.projectId, body }),
            );
          }}
        />
      )}
      {!flow.session && !flow.sessionId && (
        <Button
          variant="default"
          disabled={disabled || projectQuery.isFetching}
          onClick={() => void resetBase()}
        >
          Обновить базу и сбросить настройки
        </Button>
      )}
      {projectChanged && !flow.terminal && (
        <Alert color="yellow">
          Версия проекта изменилась, в том числе это возможно после редактирования аннотаций. Перед
          новым Commit перечитайте проект и заново проверьте Preview.
        </Alert>
      )}
      {headChanged && !flow.terminal && (
        <Alert color="yellow">
          Источник проекта уже перешёл на другую ревизию. Эту сессию нельзя перенести на новую базу:
          отмените её и начните новую.
        </Alert>
      )}
      {!!(attemptFailure || flow.readError || failure) && (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(attemptFailure ?? flow.readError ?? failure)}
        </Alert>
      )}
      {flow.recoveryError && (
        <Alert color="red" role="alert">
          <Text>{flow.recoveryError.message}</Text>
          {flow.cleanupPending && (
            <Button mt="sm" variant="default" disabled={busy} onClick={() => flow.clearRecovery()}>
              Повторить очистку восстановления
            </Button>
          )}
          <Button
            mt="sm"
            variant="default"
            disabled={busy || flow.recoveryLoading}
            onClick={() => void flow.readRecovery()}
          >
            Повторить чтение восстановления
          </Button>
        </Alert>
      )}
      {flow.recoveryMismatch && (
        <Alert color="yellow">
          <Text>
            Сохранён незавершённый запрос{" "}
            {flow.recoveryMismatch.sessionId
              ? `сессии ${flow.recoveryMismatch.sessionId}`
              : "Begin без полученного ID сессии"}
            . Новые отправки заблокированы до его восстановления.
          </Text>
          <Button
            mt="sm"
            variant="default"
            disabled={busy}
            onClick={() => onRecoveryContext(flow.recoveryMismatch?.sessionId)}
          >
            Открыть сохранённое восстановление
          </Button>
        </Alert>
      )}
      {flow.attempt?.state === "unsent" && (
        <Alert color="yellow">
          <Text>
            Запрос не отправлен: сначала нужно сохранить его для восстановления после перезапуска.
            Исходные IDs и тело сохранены в открытой форме.
          </Text>
          <Button
            mt="sm"
            disabled={busy || !!flow.recoveryMismatch}
            onClick={() => flow.attempt && void flow.execute(flow.attempt.attempt)}
          >
            Сохранить и отправить исходный запрос
          </Button>
        </Alert>
      )}
      {flow.uncertain && flow.attempt && (
        <Alert color="yellow" role="alert">
          <Text>
            Результат {flow.attempt.attempt.kind} неизвестен. Повтор сохранит исходные IDs, версии,
            тело и ключ.
          </Text>
          <Button
            mt="sm"
            disabled={busy || !!flow.recoveryMismatch || !!flow.recoveryError}
            onClick={() => flow.attempt && void flow.execute(flow.attempt.attempt)}
          >
            Повторить исходный запрос
          </Button>
        </Alert>
      )}
      {(flow.hasRecoveryRecord || flow.attempt?.state === "unsent") && !flow.cleanupPending && (
        <Button
          variant="subtle"
          disabled={busy || flow.recoveryLoading}
          onClick={() => setDiscardRecoveryConfirm(true)}
        >
          Удалить локальную запись восстановления
        </Button>
      )}
      {discardRecoveryConfirm && (
        <Alert color="yellow">
          <Text>
            Удаление локальной записи не отменяет уже принятый запрос на сервере. Повторить его с
            исходными IDs после удаления будет невозможно.
          </Text>
          <Group mt="sm">
            <Button
              color="red"
              disabled={busy}
              onClick={() => {
                if (flow.discardRecovery()) {
                  setDiscardRecoveryConfirm(false);
                  onReset();
                }
              }}
            >
              Подтвердить удаление записи восстановления
            </Button>
            <Button variant="default" onClick={() => setDiscardRecoveryConfirm(false)}>
              Сохранить запись
            </Button>
          </Group>
        </Alert>
      )}
      {(flow.sessionId || props.initialSessionId) && (
        <Button
          variant="default"
          disabled={busy || flow.reading || flow.recoveryLoading || !!flow.recoveryMismatch}
          loading={flow.reading}
          onClick={() => void reload()}
        >
          Перечитать сессию и проект
        </Button>
      )}
      {flow.conflict && (
        <Alert color="yellow">
          Получен 409. Перечитайте актуальные версии и выполните новый Preview; старое подтверждение
          больше не действует.
        </Alert>
      )}
      {flow.session && (
        <Stack gap="xs">
          <Badge w="fit-content">
            {flow.session.state} · версия {flow.session.version}
          </Badge>
          <Text size="sm" style={wrap}>
            Сессия {flow.session.id} · база {flow.session.baseRevisionId}
          </Text>
          <Text size="sm" style={wrap}>
            Репозиторий {flow.session.repositoryId} · {flow.session.manifest.provider.namespace} ·
            входящий снимок {flow.session.snapshotId}
          </Text>
          <Text size="sm">
            {flow.session.syncPolicy} · {flow.session.scopeStatus.status} ·{" "}
            {flow.session.manifest.snapshot.consistency}
          </Text>
          {flow.session.scopeStatus.gaps.map((gap) => (
            <Text key={gap} size="sm">
              {gap}
            </Text>
          ))}
          <details>
            <summary>Разделы, сохраняемые из базы</summary>
            {base?.partitions.map((part) => (
              <Text key={`${part.repositoryId}/${part.provider.namespace}`} size="xs" style={wrap}>
                {part.repositoryId} · {part.provider.namespace} · {part.snapshotId}
              </Text>
            ))}
          </details>
          <details>
            <summary>Inventory и объявленные пробелы</summary>
            <Code block style={wrap}>
              {JSON.stringify(flow.session.inventory, null, 2)}
            </Code>
          </details>
        </Stack>
      )}
      {flow.terminal && (
        <section ref={outcomeRef} aria-label="Результат синхронизации" tabIndex={-1}>
          <Stack>
            {flow.committedRevisionId && (
              <Alert color="green" component="output" style={wrap}>
                Импорт сохранён в неизменяемой ревизии {flow.committedRevisionId}. Повтор мог
                вернуть ранее сохранённый результат.
              </Alert>
            )}
            {flow.committedRevisionId && props.onCommitted && (
              <Button
                variant="default"
                onClick={() => props.onCommitted?.(flow.committedRevisionId!)}
              >
                Открыть сохранённую ревизию
              </Button>
            )}
            {flow.session?.state === "aborted" && (
              <Alert color="yellow" component="output">
                Импорт отменён. Новые пакеты и Commit недоступны.
              </Alert>
            )}
          </Stack>
        </section>
      )}
      {flow.session && !flow.terminal && (
        <>
          <Title order={3}>Пакет коллектора</Title>
          <Text size="sm">
            Загрузите выбранный JSON-файл с commands. Для оснований используйте IDs репозитория и
            входящего снимка, показанные выше. Исходный код из файлов не выполняется.
          </Text>
          <BackendSourceFileInput
            key={collectorKey}
            label="Пакет коллектора JSON"
            disabled={disabled}
            limit={maxCollectorBytes}
            parse={parseCollectorBatch}
            onValue={(value) => {
              setCollector(value);
              setClaims([]);
              setFailure(null);
            }}
            onBusy={setCollectorLoading}
          />
          {collector && (
            <>
              <Text size="sm">
                Команд: {collector.length}; локальных решений: {claims.length}
              </Text>
              <BackendClaimIdentityForm
                key={`${collectorKey}/${collector.length}`}
                session={flow.session}
                commands={collector}
                offers={offers}
                loading={offerLoading}
                nextCursor={offerCursor}
                prerequisite={offerMessage}
                disabled={disabled || !flow.fresh || flow.conflict}
                onLoad={(type, more) => void loadOffers(type, more)}
                onClaim={(command) => {
                  if (command.op !== "claim_identity") return;
                  if (
                    claims.some(
                      (old) =>
                        old.op === "claim_identity" &&
                        old.claimIdentity.externalKey === command.claimIdentity.externalKey &&
                        old.claimIdentity.recordType === command.claimIdentity.recordType,
                    )
                  ) {
                    setFailure(new Error("Для этого входящего объекта уже добавлено решение."));
                    return;
                  }
                  setClaims((prior) => [...prior, command]);
                }}
              />
              {claims.map((command, index) => (
                <Group
                  key={command.op === "claim_identity" ? command.claimIdentity.decisionId : index}
                >
                  <Text size="sm">
                    {command.op === "claim_identity"
                      ? command.claimIdentity.externalKey
                      : command.op}
                  </Text>
                  <Button
                    size="xs"
                    variant="subtle"
                    disabled={disabled}
                    onClick={() => setClaims((prior) => prior.filter((_, at) => at !== index))}
                  >
                    Убрать локальное решение
                  </Button>
                </Group>
              ))}
              <Button
                disabled={disabled || collectorLoading || !flow.fresh || flow.conflict}
                loading={preparing}
                onClick={() => void sendCollector()}
              >
                Отправить пакет
              </Button>
            </>
          )}
          <Group>
            <Button
              disabled={
                disabled ||
                !flow.fresh ||
                flow.conflict ||
                !project ||
                projectQuery.isFetching ||
                projectQuery.isError
              }
              onClick={() => {
                if (flow.session && project) {
                  setReviewed(null);
                  void flow.execute(
                    captureImportAttempt({
                      kind: "preview",
                      projectId: props.projectId,
                      sessionId: flow.session.id,
                      body: {
                        expectedImportVersion: flow.session.version,
                        baseRevisionId: flow.session.baseRevisionId,
                      },
                    }),
                    project.version,
                  );
                }
              }}
            >
              Выполнить Preview
            </Button>
            <Button
              color="red"
              variant="light"
              disabled={disabled || !flow.fresh}
              onClick={() => setAbortConfirm(true)}
            >
              Прервать импорт
            </Button>
          </Group>
          {abortConfirm && (
            <Alert color="yellow">
              <Text>Прервать именно эту сессию импорта?</Text>
              <Group mt="sm">
                <Button
                  color="red"
                  disabled={disabled || !flow.session}
                  onClick={() => {
                    if (flow.session) {
                      setAbortConfirm(false);
                      void flow.execute(
                        captureImportAttempt({
                          kind: "abort",
                          projectId: props.projectId,
                          sessionId: flow.session.id,
                          body: {
                            expectedImportVersion: flow.session.version,
                            idempotencyKey: crypto.randomUUID(),
                          },
                        }),
                      );
                    }
                  }}
                >
                  Подтвердить отмену импорта
                </Button>
                <Button variant="default" disabled={busy} onClick={() => setAbortConfirm(false)}>
                  Продолжить работу
                </Button>
              </Group>
            </Alert>
          )}
        </>
      )}
      {flow.receipt && (
        <details open>
          <summary>Принятый пакет: версия {flow.receipt.acceptedVersion}</summary>
          <Code block style={wrap}>
            {JSON.stringify(flow.receipt, null, 2)}
          </Code>
        </details>
      )}
      {flow.preview && (
        <BackendSyncPreview
          key={`${flow.preview.sessionId}/${flow.preview.version}/${flow.preview.candidateHash}`}
          projectId={props.projectId}
          preview={flow.preview}
          disabled={
            disabled ||
            !flow.fresh ||
            flow.conflict ||
            flow.preview.version !== flow.session?.version
          }
          lastChoices={lastChoices}
          onResolve={(command) => {
            if (command.op === "resolve_assertion") {
              const address = conflictAddress(command.resolution);
              setLastChoices((prior) => ({ ...prior, [address]: command.resolution.conflictHash }));
            }
            void flow.upload([command]);
          }}
        />
      )}
      {target && flow.session && flow.preview && !flow.terminal && (
        <Stack>
          <Button
            variant="default"
            disabled={disabled || !flow.fresh || !props.onCandidateChange}
            onClick={() => props.onCandidateChange?.(target)}
          >
            Открыть точный кандидат
          </Button>
          <Checkbox
            label="Я проверил этот Preview и версию проекта"
            checked={
              !!reviewed &&
              reviewed.version === flow.preview.version &&
              reviewed.projectVersion === project?.version
            }
            disabled={
              disabled ||
              !flow.fresh ||
              flow.conflict ||
              !project ||
              headChanged ||
              projectQuery.isFetching ||
              projectQuery.isError
            }
            onChange={(event) =>
              setReviewed(
                event.currentTarget.checked && project
                  ? {
                      version: flow.preview!.version,
                      hash: target.importCandidate.candidateHash,
                      projectVersion: project.version,
                    }
                  : null,
              )
            }
          />
          <Text size="sm">
            Commit использует именно проверенные версию импорта, хеш и версию проекта{" "}
            {reviewed?.projectVersion ?? "—"}.
          </Text>
          <Button
            disabled={!canCommit}
            onClick={() => {
              if (flow.session && flow.preview && reviewed && canCommit)
                void flow.execute(
                  captureImportCommit(flow.session, flow.preview, reviewed.projectVersion),
                );
            }}
          >
            Подтвердить Commit
          </Button>
        </Stack>
      )}
    </Stack>
  );
}

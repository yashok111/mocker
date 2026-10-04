import classes from "./BackendAnalysisControls.module.css";
import { useState } from "react";
import {
  Alert,
  Button,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useListBackendRevisions } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendAnalysisJob,
  BackendAnalysisTarget,
  BackendChangeProposalDetail,
} from "@/api/generated/schemas";
import { analysisTerminal, readAnalysis, readAnalysisList } from "./backendAnalysisReads";
import { makeAnalysisAttempt, useBackendAnalysisRecovery } from "./backendAnalysisRecovery";
import { BackendImpactReport } from "./BackendImpactReport";
import { LoadState, Pages } from "./BackendReadUI";
import { changeMessage } from "./backendChangeRecovery";
export function BackendAnalysisJobs({
  projectId,
  sourceRevisionId,
  target,
  proposal,
  dirty = false,
  inputEpoch = 0,
  disabled = false,
  onSaved,
}: {
  projectId: string;
  sourceRevisionId: string;
  target?: BackendAnalysisTarget;
  proposal?: BackendChangeProposalDetail;
  dirty?: boolean;
  inputEpoch?: number;
  disabled?: boolean;
  onSaved?: (value: BackendChangeProposalDetail) => void;
}) {
  const recovery = useBackendAnalysisRecovery(projectId);
  const [from, setFrom] = useState(sourceRevisionId),
    [to, setTo] = useState(sourceRevisionId);
  const [kind, setKind] = useState<"diff" | "impact">("impact");
  const [cursors, setCursors] = useState([""]),
    [selected, setSelected] = useState("");
  const [version, setVersion] = useState<number>();
  const [error, setError] = useState("");
  const [epochs, setEpochs] = useState<Record<string, number>>({});
  const [initialEpoch] = useState(inputEpoch);
  const reportEpoch = epochs[selected] ?? initialEpoch;
  const revisions = useListBackendRevisions(projectId, { limit: 100 });
  const list = useQuery({
    queryKey: ["backend-analysis-list", projectId, cursors.at(-1)],
    queryFn: ({ signal }) => readAnalysisList(projectId, cursors.at(-1), signal),
    retry: false,
    refetchInterval: 2000,
  });
  const job = useQuery({
    queryKey: ["backend-analysis-job", projectId, selected],
    queryFn: ({ signal }) => readAnalysis(projectId, selected, signal),
    enabled: !!selected,
    retry: false,
    refetchInterval: (q) =>
      q.state.data && analysisTerminal(q.state.data.job.status) ? false : 2000,
  });
  function chooseJob(id: string) {
    setSelected(id);
    setVersion(undefined);
  }
  async function start() {
    if (disabled || recovery.blocked) return;
    try {
      const result = await recovery.execute(
        makeAnalysisAttempt(
          "start",
          { projectId },
          {
            kind,
            fromRevisionId: target ? sourceRevisionId : from,
            target: target ?? { revisionId: to },
            scope: {},
            limits: {},
            observationMode: "none",
            idempotencyKey: crypto.randomUUID(),
          },
        ),
      );
      if (result) {
        const id = (result as BackendAnalysisJob).id;
        setEpochs((old) => ({ ...old, [id]: inputEpoch }));
        chooseJob(id);
        void list.refetch();
      }
    } catch (failure) {
      setError(changeMessage(failure));
    }
  }
  async function action(action: "retry" | "cancel") {
    if (!selected || recovery.blocked) return;
    const result = await recovery.execute(
      makeAnalysisAttempt(
        action,
        { projectId, jobId: selected },
        { idempotencyKey: crypto.randomUUID() },
        { analysisInputHash: job.data!.job.analysisInputHash },
      ),
    );
    if (result && "id" in result && action === "retry") {
      setEpochs((old) => ({ ...old, [result.id]: reportEpoch }));
      chooseJob(result.id);
    }
    void list.refetch();
    void job.refetch();
  }
  const items = revisions.data?.status === 200 ? revisions.data.data.items : [];
  const options = [
    ...new Map(
      [{ id: sourceRevisionId, summary: "Выбранная ревизия" }, ...items]
        .filter((x) => x.id)
        .map((x) => [x.id, { value: x.id, label: `${x.summary} · ${x.id}` }]),
    ).values(),
  ];
  return (
    <Paper
      withBorder
      p="md"
      component="section"
      aria-label={target ? "Анализ предложения" : "Анализ источников"}
      style={{ minWidth: 0 }}
    >
      <Stack>
        <Title order={3}>{target ? "Анализ предложения" : "Анализ источников"}</Title>
        <Text size="sm">
          Статический анализ не выполняет код и не подтверждает работу приложения.
        </Text>
        {error && <Alert color="red">{error}</Alert>}
        <LoadState query={revisions} label="истории исходных ревизий" />
        <fieldset
          disabled={disabled || recovery.blocked}
          style={{ border: 0, padding: 0, minWidth: 0 }}
        >
          <div className={classes.grid}>
            {!target && (
              <>
                <NativeSelect
                  label="Источник до анализа"
                  value={from}
                  data={options}
                  onChange={(e) => setFrom(e.currentTarget.value)}
                />
                <NativeSelect
                  label="Источник после анализа"
                  value={to}
                  data={options}
                  onChange={(e) => setTo(e.currentTarget.value)}
                />
              </>
            )}
            <NativeSelect
              label="Вид анализа"
              value={kind}
              data={[
                { value: "impact", label: "Влияние" },
                { value: "diff", label: "Различия" },
              ]}
              onChange={(e) => setKind(e.currentTarget.value as typeof kind)}
            />
            <Button
              className={classes.start}
              classNames={{ label: classes.startLabel }}
              disabled={!sourceRevisionId || (!target && (!from || !to))}
              onClick={() => void start()}
            >
              {target
                ? "commandPreview" in target
                  ? "Анализировать проверенный буфер"
                  : "Анализировать сохранённый черновик"
                : "Анализировать источник"}
            </Button>
          </div>
        </fieldset>
        {!target && (
          <details>
            <summary>Выбрать точные ревизии вне текущей страницы истории</summary>
            <Stack gap="xs">
              <TextInput
                label="Точная ревизия до анализа"
                value={from}
                disabled={disabled || recovery.blocked}
                onChange={(e) => setFrom(e.currentTarget.value)}
              />
              <TextInput
                label="Точная ревизия после анализа"
                value={to}
                disabled={disabled || recovery.blocked}
                onChange={(e) => setTo(e.currentTarget.value)}
              />
            </Stack>
          </details>
        )}
        {disabled && <Text size="sm">Завершите ввод и предпросмотр перед анализом буфера.</Text>}
        <LoadState query={list} label="заданий анализа" />
        <NativeSelect
          label="Задание анализа"
          value={selected}
          data={[
            { value: "", label: "Выберите задание" },
            ...(list.data?.items ?? []).map((x) => ({
              value: x.id,
              label: `${x.kind} · ${x.status} · ${x.id}`,
            })),
            ...(selected && !list.data?.items.some((x) => x.id === selected)
              ? [{ value: selected, label: selected }]
              : []),
          ]}
          onChange={(e) => chooseJob(e.currentTarget.value)}
        />
        <Pages
          label="заданий"
          cursors={cursors}
          setCursors={setCursors}
          next={list.data?.nextCursor}
          busy={list.isFetching}
        />
        {selected && (
          <>
            <LoadState query={job} label="задания" />
            {job.data && (
              <>
                <Text component="output" aria-live="polite">
                  Задание: {job.data.job.status} · просмотрено {job.data.job.progress.states} ·
                  выводов {job.data.job.progress.findings}
                </Text>
                {job.data.job.diagnostic && (
                  <Alert color="yellow">{job.data.job.diagnostic.message}</Alert>
                )}
                <Group>
                  <Button variant="default" onClick={() => void job.refetch()}>
                    Обновить состояние задания
                  </Button>
                  <Button
                    disabled={recovery.blocked || analysisTerminal(job.data.job.status)}
                    color="orange"
                    onClick={() => void action("cancel")}
                  >
                    Отменить задание на сервере
                  </Button>
                  <Button
                    disabled={recovery.blocked || !analysisTerminal(job.data.job.status)}
                    onClick={() => void action("retry")}
                  >
                    Новый анализ тех же данных
                  </Button>
                </Group>
                <Text size="xs">
                  Закрытие экрана останавливает только локальные чтения. Для отмены задания
                  используйте кнопку отмены на сервере.
                </Text>
                {job.data.job.resultVersion && (
                  <NativeSelect
                    label="Неизменяемая версия отчёта"
                    value={version ? String(version) : ""}
                    data={[
                      { value: "", label: "Выберите опубликованную версию" },
                      ...Array.from(
                        { length: Math.min(job.data.job.resultVersion, 1000) },
                        (_, i) => ({ value: String(i + 1), label: `Версия ${i + 1}` }),
                      ),
                    ]}
                    onChange={(e) => {
                      setVersion(e.currentTarget.value ? Number(e.currentTarget.value) : undefined);
                    }}
                  />
                )}
                {job.data.job.resultVersion && job.data.job.resultVersion > 1000 && (
                  <TextInput
                    label="Точная опубликованная версия отчёта"
                    inputMode="numeric"
                    description={`От 1 до ${job.data.job.resultVersion}`}
                    value={version ?? ""}
                    onChange={(e) => {
                      const value = Number(e.currentTarget.value);
                      setVersion(
                        Number.isSafeInteger(value) &&
                          value > 0 &&
                          value <= job.data!.job.resultVersion!
                          ? value
                          : undefined,
                      );
                    }}
                  />
                )}
                {version && (
                  <BackendImpactReport
                    projectId={projectId}
                    detail={job.data}
                    resultVersion={version}
                    proposal={proposal}
                    dirty={dirty || reportEpoch !== inputEpoch}
                    onSaved={onSaved}
                  />
                )}
              </>
            )}
          </>
        )}
      </Stack>
    </Paper>
  );
}

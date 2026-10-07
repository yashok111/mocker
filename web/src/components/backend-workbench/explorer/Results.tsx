import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Badge, Button, Group, Loader, Stack, Text } from "@mantine/core";
import {
  getBackendMaterialization,
  getBackendChangeProposal,
  getBackendAnalysis,
  getBackendAnalysisResults,
  getBackendRevision,
  getBackendImport,
  getBackendObservationVersion,
  getBackendObservationRecords,
  getBackendReplayRun,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendReadTarget,
  BackendMaterializationResult,
  GetBackendAnalysisResultsParams,
} from "@/api/generated/schemas";
import { diagramSearch, type BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { verifyChangeDetail } from "../backendChangeReads";
import { backendReadTargetKey } from "../backendReadTargets";
import { ok, readCatalog, statusNames, type CatalogItem } from "./catalogs";
import styles from "./Explorer.module.css";
import { resolvedTargetSearch } from "./navigation";
import { isObserved } from "./observedLayer";
import { materializationArtifactLink } from "./artifactLinks";
export function Results({
  projectId,
  target,
  search,
  onNavigate,
}: {
  projectId: string;
  target: BackendReadTarget;
  search: BackendWorkspaceSearch;
  onNavigate: (s: BackendWorkspaceSearch) => void;
}) {
  const panel = search.wbPanel!;
  const [cursors, setCursors] = useState<Record<string, string>>({});
  const catalog = useQuery({
    queryKey: ["workbench-catalog", projectId, panel, cursors],
    queryFn: ({ signal }) => readCatalog(projectId, panel, signal, cursors),
    retry: false,
    refetchOnWindowFocus: true,
    staleTime: 0,
    refetchInterval: (q) =>
      panel === "checks" &&
      q.state.data?.items.some((i) => ["queued", "running"].includes(i.status ?? ""))
        ? 2000
        : false,
  });
  function open(item: CatalogItem) {
    if (item.kind === "diagram")
      onNavigate({
        ...diagramSearch({ id: item.id, version: item.version!, contentHash: item.hash! }),
      });
    else if (item.kind === "diagram-view")
      onNavigate({ diagramViewId: item.id, diagramViewVersion: item.version });
    else if (item.kind === "saved-view") onNavigate({ viewId: item.id, viewVersion: item.version });
    else
      onNavigate({
        ...search,
        wbResult: item.id,
        wbResultKind: item.kind,
        wbResultVersion: item.version,
        ...(item.kind === "proposal" ? { wbResultRevision: item.revisionId } : {}),
      });
  }
  return (
    <section className={styles.resultPanel} aria-label="Результаты проекта">
      <Group justify="space-between">
        <h2 className={styles.stageTitle}>
          {
            {
              changes: "Изменения",
              checks: "Проверки",
              sources: "Источники и история",
              views: "Сохранённые виды",
              observations: "Наблюдения",
              replay: "Воспроизведения",
            }[panel]
          }
        </h2>
        <Button
          variant="subtle"
          size="compact-sm"
          onClick={() =>
            onNavigate({
              ...search,
              wbPanel: undefined,
              wbResult: undefined,
              wbResultKind: undefined,
            })
          }
        >
          Вернуться к карте
        </Button>
      </Group>
      <Text size="sm" c="dimmed" mb="lg">
        {panel === "changes"
          ? "Предложения и результаты, подготовленные агентом."
          : panel === "checks"
            ? "Сохранённые проверки и их доказательства."
            : panel === "observations"
              ? "Данные наблюдений со своей средой и периодом."
              : "Выберите существующий результат для просмотра."}
      </Text>
      {search.wbResult ? (
        <ResultDetail
          key={`${search.wbResult}:${search.wbResultVersion}`}
          projectId={projectId}
          target={target}
          search={search}
          onNavigate={onNavigate}
        />
      ) : (
        <>
          {catalog.isFetching && !catalog.data && <Loader aria-label="Загружаем результаты" />}
          {catalog.isError && (
            <Alert color="red">
              Не удалось обновить каталог. Последние полученные результаты сохранены.
              <Button
                variant="subtle"
                onClick={() => {
                  setCursors({});
                  void catalog.refetch();
                }}
              >
                Повторить
              </Button>
            </Alert>
          )}
          {catalog.data?.items.length === 0 && (
            <Text c="dimmed" py="xl">
              Пока нет результатов. Агент может подготовить их через MCP.
            </Text>
          )}
          {catalog.data?.items.map((item) => (
            <div key={`${item.kind}:${item.id}`} className={styles.resultRow}>
              <div className={styles.resultRowMain}>
                <Text fw={550}>{item.name}</Text>
                <Text size="xs" c="dimmed">
                  {item.summary}
                  {item.date ? ` · ${new Date(item.date).toLocaleString("ru-RU")}` : ""}
                </Text>
              </div>
              {item.status && (
                <Badge color={item.status === "failed" ? "red" : "gray"} variant="light">
                  {statusNames[item.status] ?? item.status}
                </Badge>
              )}
              <Button variant="subtle" size="compact-sm" onClick={() => open(item)}>
                Открыть
              </Button>
            </div>
          ))}
          {catalog.data && Object.values(catalog.data.next).some(Boolean) && (
            <Button mt="md" variant="subtle" onClick={() => setCursors(catalog.data!.next)}>
              Следующие результаты
            </Button>
          )}
        </>
      )}
    </section>
  );
}
function ResultDetail({
  projectId,
  target,
  search,
  onNavigate,
}: {
  projectId: string;
  target: BackendReadTarget;
  search: BackendWorkspaceSearch;
  onNavigate: (s: BackendWorkspaceSearch) => void;
}) {
  const id = search.wbResult!;
  const kind = search.wbResultKind;
  const [section, setSection] = useState<GetBackendAnalysisResultsParams["section"]>("findings");
  const [cursor, setCursor] = useState("");
  const fetchResult = async ({ signal }: { signal: AbortSignal }) => {
    if (kind === "materialization") {
      const data = ok(await getBackendMaterialization(projectId, id, { signal }));
      if (data.id !== id || data.projectId !== projectId || data.receipt.id !== id)
        throw new Error("Получен другой результат");
      return { kind, data } as const;
    }
    if (kind === "proposal") {
      if (!search.wbResultRevision) throw new Error("Не указана точная версия предложения");
      const data = ok(
        await getBackendChangeProposal(
          projectId,
          id,
          { proposalRevisionId: search.wbResultRevision, limit: 20 },
          { signal },
        ),
      );
      return {
        kind,
        data: verifyChangeDetail(data, projectId, id, search.wbResultRevision),
      } as const;
    }
    if (kind === "check") {
      const data = ok(await getBackendAnalysis(projectId, id, { signal }));
      if (data.job.id !== id || data.job.projectId !== projectId)
        throw new Error("Получена другая проверка");
      return { kind, data } as const;
    }
    if (kind === "revision") {
      const data = ok(await getBackendRevision(projectId, id, { signal }));
      if (data.id !== id || data.projectId !== projectId) throw new Error("Получена другая версия");
      return { kind, data } as const;
    }
    if (kind === "import")
      return {
        kind,
        data: ok(await getBackendImport(projectId, id, { limit: 20 }, { signal })),
      } as const;
    if (kind === "observation") {
      if (!search.wbResultVersion) throw new Error("Нет точной версии наблюдений");
      return {
        kind,
        data: ok(
          await getBackendObservationVersion(projectId, id, search.wbResultVersion, { signal }),
        ),
      } as const;
    }
    if (kind === "replay")
      return { kind, data: ok(await getBackendReplayRun(projectId, id, { signal })) } as const;
    throw new Error("Неизвестный тип результата");
  };
  const query = useQuery<Awaited<ReturnType<typeof fetchResult>>>({
    queryKey: [
      "workbench-result",
      projectId,
      kind,
      id,
      search.wbResultVersion,
      search.wbResultRevision,
    ],
    retry: false,
    staleTime: ["check", "replay", "import"].includes(kind ?? "") ? 0 : Infinity,
    refetchInterval: (q) => {
      const value = q.state.data;
      if (value?.kind === "check" && ["queued", "running"].includes(value.data.job.status))
        return value.data.job.recommendedPollIntervalMs;
      if (value?.kind === "replay" && ["queued", "running"].includes(value.data.status))
        return 2000;
      return false;
    },
    queryFn: fetchResult,
  });
  const job = query.data?.kind === "check" ? query.data.data : undefined;
  const reportTarget =
    job && "target" in job.input && !("commandPreview" in job.input.target)
      ? job.input.target
      : undefined;
  const report = useQuery({
    queryKey: ["workbench-report", projectId, id, search.wbResultVersion, section, cursor],
    enabled: !!job && !!search.wbResultVersion,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) =>
      ok(
        await getBackendAnalysisResults(
          projectId,
          id,
          { resultVersion: search.wbResultVersion!, section, limit: 30, cursor },
          { signal },
        ),
      ),
  });
  const records = useQuery({
    queryKey: ["workbench-observation-records", projectId, id, search.wbResultVersion, cursor],
    enabled: kind === "observation" && !!query.data,
    retry: false,
    staleTime: Infinity,
    queryFn: async ({ signal }) =>
      ok(
        await getBackendObservationRecords(
          projectId,
          id,
          search.wbResultVersion!,
          { limit: 20, cursor },
          { signal },
        ),
      ),
  });
  const value = query.data;
  return (
    <Stack gap="md">
      <Button
        variant="subtle"
        size="compact-sm"
        style={{ alignSelf: "start" }}
        onClick={() =>
          onNavigate({
            ...search,
            wbResult: undefined,
            wbResultKind: undefined,
            wbResultVersion: undefined,
            wbResultRevision: undefined,
          })
        }
      >
        ← К списку результатов
      </Button>
      {query.isPending && <Loader />}
      {query.isError && (
        <Alert color="red">
          Точный результат недоступен. Текущая версия не подставлена.
          <Button onClick={() => void query.refetch()} variant="subtle">
            Повторить
          </Button>
        </Alert>
      )}
      {value?.kind === "materialization" && (
        <MaterializationResult data={value.data} onNavigate={onNavigate} />
      )}
      {value?.kind === "proposal" && (
        <>
          <Group>
            <h3>{value.data.proposal.name}</h3>
            <Badge>{statusNames[value.data.proposal.status]}</Badge>
          </Group>
          <Text size="sm">{value.data.revision.summary || "Подготовленное изменение модели"}</Text>
          <Text size="xs" c="dimmed">
            Основание: {value.data.revision.baseRevisionId}. Предложение — авторский замысел, не
            доказательство реализации.
          </Text>
          <Button
            style={{ alignSelf: "start" }}
            variant="light"
            onClick={() =>
              onNavigate({
                changeProposalId: id,
                proposalRevisionId: value.data.revision.id,
                wbMode: "overview",
                wbView: "structure",
              })
            }
          >
            Открыть модель предложения
          </Button>
          <Button
            variant="light"
            style={{ alignSelf: "start" }}
            onClick={() =>
              onNavigate({
                changeProposalId: id,
                proposalRevisionId: value.data.revision.id,
                wbMode: "comparison",
                wbView: "structure",
              })
            }
          >
            Сравнить изменения на карте
          </Button>
          <h4>Изменения</h4>
          <ReadOnlyValue value={value.data.revision.delta} />
          <h4>Критерии</h4>
          <ReadOnlyValue value={value.data.revision.criteria} />
        </>
      )}
      {job && (
        <>
          <Group>
            <h3>Результат проверки</h3>
            <Badge>{statusNames[job.job.status]}</Badge>
          </Group>
          {reportTarget && (
            <Badge variant="outline" color="gray">
              {backendReadTargetKey(reportTarget as BackendReadTarget) ===
              backendReadTargetKey(target)
                ? "Выбранная версия модели"
                : "Другая версия модели"}
            </Badge>
          )}
          <Text size="xs" c="dimmed">
            Проверка относится к своей области и версии модели. Структурные проверки не доказывают
            выполнение приложения.
          </Text>
          <details className={styles.details}>
            <summary>Область и точные версии</summary>
            <ReadOnlyValue value={job.input} />
          </details>
          <Group>
            {(["changes", "findings", "witnesses", "checks", "gaps"] as const).map((s, i) => (
              <Button
                key={s}
                variant={section === s ? "light" : "subtle"}
                size="compact-sm"
                onClick={() => {
                  setSection(s);
                  setCursor("");
                }}
              >
                {["Изменения", "Замечания", "Доказательства", "Проверки", "Пробелы"][i]}
              </Button>
            ))}
          </Group>
          {!search.wbResultVersion && (
            <Text>
              {job.job.resultVersion
                ? "Выберите сохранённую версию результата."
                : "У этой проверки ещё нет сохранённого результата."}
            </Text>
          )}
          {!!job.job.resultVersion && job.job.resultVersion !== search.wbResultVersion && (
            <Button
              variant="light"
              onClick={() => onNavigate({ ...search, wbResultVersion: job.job.resultVersion })}
            >
              Открыть результат · версия {job.job.resultVersion}
            </Button>
          )}
          {report.isError && (
            <Alert color="red">Результат выбранной версии проверки недоступен.</Alert>
          )}
          {report.data?.items.map((item) => (
            <div key={item.id} className={styles.resultRow}>
              <div className={styles.resultRowMain}>
                <Badge variant="outline" color="gray">
                  {item.certainty === "confirmed"
                    ? "Подтверждено"
                    : item.certainty === "possible"
                      ? "Возможно"
                      : "Неизвестно"}
                </Badge>
                <Text size="sm" fw={550}>
                  {resultSummary(item.detail) || item.kind}
                </Text>
                <details className={styles.details}>
                  <summary>Подробности и доказательства</summary>
                  <ReadOnlyValue value={item.detail} />
                </details>
                {["node", "edge"].includes(item.object.recordType) && reportTarget && (
                  <Button
                    variant="subtle"
                    size="compact-sm"
                    onClick={() =>
                      onNavigate({
                        ...resolvedTargetSearch(reportTarget as BackendReadTarget),
                        wbView: "structure",
                        wbMode: item.object.recordType === "node" ? "neighborhood" : "overview",
                        wbScope: item.object.recordType === "node" ? item.object.id : undefined,
                        recordId: item.object.id,
                        recordType: item.object.recordType === "node" ? "node" : "edge",
                      })
                    }
                  >
                    Показать объект на карте
                  </Button>
                )}
                {isObserved(item.detail) && (
                  <Button
                    variant="light"
                    size="compact-sm"
                    onClick={() => {
                      if (isObserved(item.detail))
                        onNavigate({
                          ...diagramSearch(item.detail.diagramScope.pin),
                          wbObservation: JSON.stringify({
                            jobId: id,
                            resultVersion: search.wbResultVersion,
                            recordId: item.id,
                            section,
                            cursor,
                          }),
                        });
                    }}
                  >
                    Показать наблюдения на схеме
                  </Button>
                )}
              </div>
            </div>
          ))}
          {report.data?.nextCursor && (
            <Button variant="subtle" onClick={() => setCursor(report.data!.nextCursor)}>
              Дальше
            </Button>
          )}
        </>
      )}
      {value?.kind === "revision" && (
        <>
          <h3>{value.data.summary || "Версия модели"}</h3>
          <Text>
            {value.data.author} · {new Date(value.data.createdAt).toLocaleString("ru-RU")}
          </Text>
          <Button
            style={{ alignSelf: "start" }}
            onClick={() => onNavigate({ revisionId: id, wbMode: "overview" })}
          >
            Открыть эту версию
          </Button>
          <ReadOnlyValue value={value.data.coverage} />
        </>
      )}
      {value?.kind === "import" && (
        <>
          <h3>Импорт источников</h3>
          <Badge>{value.data.session.state}</Badge>
          <Text>{value.data.session.manifest.repositoryName}</Text>
          <ReadOnlyValue value={value.data.session.inventory} />
          <details>
            <summary>Точные сведения об импорте</summary>
            <ReadOnlyValue value={value.data} />
          </details>
        </>
      )}
      {value?.kind === "observation" && (
        <>
          <h3>{value.data.name}</h3>
          <ReadOnlyValue value={value.data.context} />
          <Text size="sm">Наблюдения относятся только к указанным среде, периоду и объектам.</Text>
          {records.data && <ReadOnlyValue value={records.data} />}
        </>
      )}
      {value?.kind === "replay" && (
        <>
          <h3>Результат воспроизведения</h3>
          <Badge>{statusNames[value.data.status]}</Badge>
          <ReadOnlyValue value={value.data.report ?? value.data.provenance} />
        </>
      )}
      <details className={styles.details}>
        <summary>Технические сведения</summary>
        <Text size="xs">Текущий контекст карты: {backendReadTargetKey(target)}</Text>
        <pre>{JSON.stringify(value?.data, null, 2)}</pre>
      </details>
    </Stack>
  );
}
function MaterializationResult({
  data,
  onNavigate,
}: {
  data: BackendMaterializationResult;
  onNavigate: (s: BackendWorkspaceSearch) => void;
}) {
  const input = data.preview.input;
  return (
    <>
      <h3>{input.reason || "Результат материализации"}</h3>
      <Text size="sm">
        {data.receipt.author} · {new Date(data.createdAt).toLocaleString("ru-RU")}
      </Text>
      <Alert color="teal">
        Сохранённый план и результат применения. Артефакты остаются черновиками; публикация не
        подтверждена.
      </Alert>
      <Group>
        {input.target.changeProposal && (
          <Button
            variant="subtle"
            onClick={() =>
              onNavigate({
                changeProposalId: input.target.changeProposal!.proposalId,
                proposalRevisionId: input.target.changeProposal!.proposalRevisionId,
                wbMode: "overview",
              })
            }
          >
            Исходное предложение
          </Button>
        )}
      </Group>
      <h4>Созданные артефакты</h4>
      {data.receipt.owners.map((owner) => (
        <div className={styles.resultRow} key={owner.targetKey}>
          <div className={styles.resultRowMain}>
            <Text fw={550}>
              {input.targets.find((t) => t.key === owner.targetKey)?.name ?? owner.targetKey}
            </Text>
            <Text size="xs" c="dimmed">
              {owner.pin.pin.kind} · версия {owner.version}
            </Text>
          </div>
          {materializationArtifactLink(data.projectId, input.target, owner) ? (
            <Button
              component="a"
              href={materializationArtifactLink(data.projectId, input.target, owner)}
              variant="subtle"
              size="compact-sm"
            >
              Открыть артефакт
            </Button>
          ) : (
            <Text size="sm">Точная версия недоступна</Text>
          )}
        </div>
      ))}
      <h4>Перенесённая область и исключения</h4>
      <ReadOnlyValue value={data.receipt.coverage} />
      <Text size="sm">
        {data.receipt.equivalence === "partial_simulation"
          ? "Частичная симуляция"
          : "Структурная проекция"}{" "}
        · эквивалентность исходному приложению не установлена.
      </Text>
    </>
  );
}
function resultSummary(value: unknown): string {
  if (!value || typeof value !== "object") return "";
  for (const key of ["summary", "message", "explanation", "reason", "title", "name"]) {
    const text = (value as Record<string, unknown>)[key];
    if (typeof text === "string" && text)
      return text.length > 320 ? text.slice(0, 320) + "…" : text;
  }
  return "";
}
const labels: Record<string, string> = {
  name: "Название",
  kind: "Вид",
  reason: "Основание",
  status: "Состояние",
  description: "Описание",
  summary: "Описание",
  message: "Сообщение",
  before: "До",
  after: "После",
  coverage: "Покрытие",
  gaps: "Пробелы",
  knownObjects: "Известные объекты",
  denominator: "Всего в источнике",
  scope: "Область",
  sourceId: "Исходный объект",
  targetKey: "Артефакт",
  equivalence: "Соответствие",
  expression: "Выражение",
  guard: "Условие",
  trigger: "Событие",
  certainty: "Достоверность",
};
export function ReadOnlyValue({ value, depth = 0 }: { value: unknown; depth?: number }) {
  const [expanded, setExpanded] = useState(false);
  if (value === null || value === undefined)
    return (
      <Text size="sm" c="dimmed">
        Не указано
      </Text>
    );
  if (typeof value !== "object")
    return (
      <Text size="sm" style={{ overflowWrap: "anywhere" }}>
        {String(value)}
      </Text>
    );
  if (depth > 4)
    return (
      <details className={styles.details}>
        <summary>Подробности</summary>
        <pre>{JSON.stringify(value, null, 2)}</pre>
      </details>
    );
  if (Array.isArray(value))
    return (
      <Stack gap="sm">
        {(expanded ? value : value.slice(0, 12)).map((v, i) => (
          <ReadOnlyValue key={i} value={v} depth={depth + 1} />
        ))}
        {!value.length && (
          <Text size="sm" c="dimmed">
            Нет записей
          </Text>
        )}
        {value.length > 12 && !expanded && (
          <Button variant="subtle" size="compact-xs" onClick={() => setExpanded(true)}>
            Ещё {value.length - 12}
          </Button>
        )}
      </Stack>
    );
  return (
    <dl className={styles.metadata}>
      {Object.entries(value)
        .filter(([, v]) => v !== undefined && v !== null)
        .map(([key, v]) => (
          <div key={key} style={{ display: "contents" }}>
            <dt>{labels[key] ?? key}</dt>
            <dd>
              <ReadOnlyValue value={v} depth={depth + 1} />
            </dd>
          </div>
        ))}
    </dl>
  );
}

import {
  listBackendChangeProposals,
  listBackendAnalysis,
  listBackendMaterializations,
  listBackendRevisions,
  listBackendImportSummaries,
  listBackendDiagrams,
  listBackendDiagramViews,
  listBackendSavedViews,
  listBackendObservations,
  listBackendReplayRuns,
} from "@/api/generated/backend-projects/backend-projects";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { diagramKindNames } from "./model";
import { readPages } from "./readPages";
export type CatalogItem = {
  id: string;
  kind: string;
  name: string;
  status?: string;
  date?: string;
  version?: number;
  revisionId?: string;
  hash?: string;
  targetHash?: string;
  summary?: string;
};
export type Catalog = { items: CatalogItem[]; next: Record<string, string> };
export function ok<R extends { status: number; data: unknown }>(
  r: R,
): Extract<R, { status: 200 }>["data"] {
  if (r.status !== 200) throw new Error("Не удалось прочитать результат");
  return r.data as Extract<R, { status: 200 }>["data"];
}
export async function readCatalog(
  projectId: string,
  panel: NonNullable<BackendWorkspaceSearch["wbPanel"]>,
  signal: AbortSignal,
  cursors: Record<string, string> = {},
): Promise<Catalog> {
  const options = { signal };
  const limit = 20;
  const next: Record<string, string> = {};
  let items: CatalogItem[] = [];
  if (panel === "changes") {
    const [p, m] = await Promise.all([
      listBackendChangeProposals(
        projectId,
        { limit, cursor: cursors.proposals, order: "desc" },
        options,
      ).then(ok),
      listBackendMaterializations(
        projectId,
        { limit, cursor: cursors.materializations },
        options,
      ).then(ok),
    ]);
    items = [
      ...p.items.map((p) => ({
        id: p.id,
        kind: "proposal",
        name: p.name,
        status: p.status,
        date: p.updatedAt,
        version: p.version,
        revisionId: p.currentDraftRevisionId,
        hash: p.currentDraftHash,
      })),
      ...m.items.map((m) => ({
        id: m.id,
        kind: "materialization",
        name: m.reason || "Результат материализации",
        date: m.createdAt,
        targetHash: m.targetHash,
        summary: `${m.targetCount} артефактов · ${m.author}`,
      })),
    ];
    next.proposals = p.nextCursor;
    next.materializations = m.nextCursor;
  } else if (panel === "checks") {
    const page = ok(
      await listBackendAnalysis(
        projectId,
        { limit, order: "desc", cursor: cursors.checks },
        options,
      ),
    );
    items = page.items.map((j) => ({
      id: j.id,
      kind: "check",
      name: checkNames[j.kind] ?? j.kind,
      status: j.status,
      date: j.updatedAt,
      version: j.resultVersion,
      summary: `${j.progress.records} записей`,
    }));
    next.checks = page.nextCursor;
  } else if (panel === "sources") {
    const [r, i] = await Promise.all([
      listBackendRevisions(projectId, { limit, cursor: cursors.revisions }, options).then(ok),
      listBackendImportSummaries(projectId, { limit, cursor: cursors.imports }, options).then(ok),
    ]);
    items = [
      ...r.items.map((r) => ({
        id: r.id,
        kind: "revision",
        name: r.summary || "Версия модели",
        date: r.createdAt,
        summary: `${r.author} · ${r.coverage.status}`,
      })),
      ...i.items.map((i) => ({
        id: i.id,
        kind: "import",
        name: "Импорт источников",
        date: i.updatedAt,
        status: i.state,
        version: i.version,
        summary: i.repositoryName,
      })),
    ];
    next.revisions = r.nextCursor;
    next.imports = i.nextCursor;
  } else if (panel === "views") {
    const [d, v, s] = await Promise.all([
      listBackendDiagrams(
        projectId,
        { limit, cursor: cursors.diagrams, order: "desc" },
        options,
      ).then(ok),
      listBackendDiagramViews(
        projectId,
        { limit, cursor: cursors.views, order: "desc" },
        options,
      ).then(ok),
      listBackendSavedViews(projectId, { limit, cursor: cursors.saved }, options).then(ok),
    ]);
    items = [
      ...d.items.map((d, i) => ({
        id: d.id,
        kind: "diagram",
        name: d.name
          ? `${d.name} · ${diagramKindNames[d.kind]}`
          : `${diagramKindNames[d.kind]} · ${i + 1}`,
        version: d.pin.version,
        hash: d.pin.contentHash,
        targetHash: d.targetHash,
        summary: diagramKindNames[d.kind],
      })),
      ...v.items.map((v) => ({
        id: v.id,
        kind: "diagram-view",
        name: v.name,
        version: v.version,
        summary: diagramKindNames[v.kind],
      })),
      ...s.items.map((v) => ({ id: v.id, kind: "saved-view", name: v.name, version: v.version })),
    ];
    next.diagrams = d.nextCursor;
    next.views = v.nextCursor;
    next.saved = s.nextCursor;
  } else if (panel === "observations") {
    const p = ok(
      await listBackendObservations(projectId, { limit, cursor: cursors.observations }, options),
    );
    items = p.items.map((o) => ({
      id: o.setId,
      kind: "observation",
      name: o.name,
      version: o.version,
      hash: o.contentHash,
      summary: `${o.recordCount} записей наблюдений`,
    }));
    next.observations = p.nextCursor;
  } else {
    const pages = await readPages(async (cursor) => {
      const runs = ok(
        await listBackendReplayRuns(projectId, cursor ? { cursor } : undefined, options),
      );
      return { runs, nextCursor: runs.at(-1)?.id ?? "" };
    }, signal);
    const runs = pages.flatMap((page) => page.runs);
    items = runs.map((r) => ({
      id: r.id,
      kind: "replay",
      name: "Воспроизведение Orders",
      status: r.status,
      date: r.createdAt,
    }));
  }
  signal.throwIfAborted();
  items.sort((a, b) => (b.date ?? "").localeCompare(a.date ?? ""));
  return { items, next };
}
export const checkNames: Record<string, string> = {
  diff: "Сравнение версий",
  impact: "Влияние изменений",
  change_package: "Пакет изменений",
  conformance: "Соответствие реализации",
  endpoint_review: "Проверка операции",
  diagnostics: "Диагностика",
  scenario_measurement: "Измерение сценария",
  scenario_comparison: "Сравнение сценариев",
};
export const statusNames: Record<string, string> = {
  draft: "Черновик",
  ready: "Готово к проверке",
  implemented: "Реализовано",
  archived: "В архиве",
  queued: "В очереди",
  running: "Выполняется",
  completed: "Завершено",
  succeeded: "Успешно",
  failed: "Ошибка",
  cancelled: "Отменено",
  interrupted: "Прервано",
  unverified: "Не подтверждено",
};

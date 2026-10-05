import {
  getBackendAnalysis,
  getBackendAnalysisResults,
  listBackendAnalysis,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendAnalysisJobDetail,
  BackendAnalysisResultManifest,
  BackendChangeProposalDetail,
  GetBackendAnalysisResultsParams,
} from "@/api/generated/schemas";
export const analysisTerminal = (status: string) => !["queued", "running"].includes(status);
export async function readAnalysis(projectId: string, jobId: string, signal?: AbortSignal) {
  const response = await getBackendAnalysis(projectId, jobId, { signal });
  if (
    response.status !== 200 ||
    response.data.job.id !== jobId ||
    response.data.job.projectId !== projectId ||
    response.data.input.kind !== response.data.job.kind ||
    (response.data.input.documentVersion === "backend-analysis-context-v1") !==
      ["diff", "impact"].includes(response.data.job.kind)
  )
    throw new Error("Получено другое задание анализа.");
  return response.data;
}
export async function readAnalysisList(projectId: string, cursor = "", signal?: AbortSignal) {
  const response = await listBackendAnalysis(
    projectId,
    { limit: 100, ...(cursor ? { cursor } : {}) },
    { signal },
  );
  if (response.status !== 200 || response.data.items.some((j) => j.projectId !== projectId))
    throw new Error("Список относится к другому проекту.");
  return response.data;
}
export async function readAnalysisResults(
  projectId: string,
  detail: BackendAnalysisJobDetail,
  params: GetBackendAnalysisResultsParams,
  signal?: AbortSignal,
) {
  const response = await getBackendAnalysisResults(projectId, detail.job.id, params, { signal });
  if (
    response.status !== 200 ||
    response.data.manifest.jobId !== detail.job.id ||
    response.data.manifest.resultVersion !== params.resultVersion ||
    response.data.manifest.analysisInputHash !== detail.job.analysisInputHash ||
    response.data.section !== params.section
  )
    throw new Error("Отчёт не соответствует выбранной неизменяемой версии.");
  return response.data;
}
export function analysisReadyReason(
  detail?: BackendAnalysisJobDetail,
  manifest?: BackendAnalysisResultManifest,
  proposal?: BackendChangeProposalDetail,
  dirty = false,
): string {
  if (dirty)
    return "Есть несохранённые изменения. Сначала сохраните черновик и выполните новый анализ.";
  if (!detail || !manifest || !proposal) return "Выберите отчёт сохранённого полного предложения.";
  const { job, input } = detail,
    revision = proposal.revision;
  if (proposal.proposal.status === "ready") return "Черновик уже готов.";
  if (proposal.proposal.status !== "draft") return "Готовность доступна только для черновика.";
  if (
    job.projectId !== proposal.proposal.projectId ||
    manifest.jobId !== job.id ||
    manifest.analysisInputHash !== job.analysisInputHash
  )
    return "Отчёт относится к другому проекту или заданию.";
  if (
    job.status !== "completed" ||
    job.kind !== "impact" ||
    job.resultVersion !== manifest.resultVersion
  )
    return "Нужна финальная версия завершённого анализа влияния.";
  if (input.documentVersion !== "backend-analysis-context-v1")
    return "Нужен отчёт анализа влияния.";
  if (
    !("changeProposal" in input.target) ||
    input.target.changeProposal.proposalId !== proposal.proposal.id ||
    input.target.changeProposal.proposalRevisionId !== revision.id ||
    proposal.proposal.currentDraftRevisionId !== revision.id ||
    input.afterPins.effectiveSemanticHash !== revision.semanticHash ||
    input.fromRevisionId !== revision.baseRevisionId ||
    input.beforeSource.semanticHash !== revision.baseSemanticHash ||
    input.afterPins.baseRevisionId !== revision.baseRevisionId
  )
    return "Отчёт устарел или относится к другой базе, ревизии либо несохранённому буферу.";
  if (!manifest.complete || manifest.truncationReasons.length)
    return "Неполный или усечённый отчёт не подходит для готовности.";
  const covered = new Set(
    manifest.coveredChangedIds.map((x) => JSON.stringify([x.recordType, x.id])),
  );
  if (manifest.changedIds.some((x) => !covered.has(JSON.stringify([x.recordType, x.id]))))
    return "Отчёт охватывает только часть изменённых объектов.";
  return "";
}

/** Read every criterion page at the selected publication, never the mutable latest version. */
export async function readConformanceCriteria(
  projectId: string,
  detail: BackendAnalysisJobDetail,
  resultVersion: number,
  signal?: AbortSignal,
) {
  const items: import("@/api/generated/schemas").BackendAnalysisConformanceCriterionDetail[] = [];
  const seen = new Set<string>();
  let cursor = "",
    hash = "";
  do {
    const page = await readAnalysisResults(
      projectId,
      detail,
      { section: "checks", resultVersion, limit: 100, ...(cursor ? { cursor } : {}) },
      signal,
    );
    if (hash && hash !== page.manifest.semanticResultHash)
      throw new Error("Хеш выбранного отчёта изменился");
    hash = page.manifest.semanticResultHash;
    for (const item of page.items)
      if ("type" in item.detail && item.detail.type === "conformance_criterion")
        items.push(item.detail);
    if (items.length > 100) throw new Error("Реестр критериев превышает лимит");
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor)) throw new Error("Неполный реестр критериев");
    seen.add(cursor);
  } while (cursor);
  return { items, resultHash: hash };
}
export function analysisImplementedReason(
  detail: BackendAnalysisJobDetail,
  manifest: BackendAnalysisResultManifest | undefined,
  proposal: BackendChangeProposalDetail | undefined,
  criteria:
    | import("@/api/generated/schemas").BackendAnalysisConformanceCriterionDetail[]
    | undefined,
  dirty: boolean,
) {
  if (dirty) return "Есть несохранённые изменения. Сначала сохраните черновик.";
  if (!proposal || !manifest || !criteria)
    return "Дождитесь точного отчёта и полного реестра критериев.";
  if (proposal.proposal.status !== "ready")
    return "Реализацию можно отметить только для готового предложения.";
  if (
    detail.input.documentVersion !== "backend-analysis-context-v2" ||
    detail.input.kind !== "conformance"
  )
    return "Нужен отчёт соответствия.";
  const p = detail.input.payload,
    r = proposal.revision;
  if (
    detail.job.projectId !== proposal.proposal.projectId ||
    detail.job.kind !== "conformance" ||
    detail.job.status !== "completed" ||
    detail.job.resultVersion !== manifest.resultVersion ||
    manifest.jobId !== detail.job.id ||
    manifest.analysisInputHash !== detail.job.analysisInputHash
  )
    return "Нужна финальная версия завершённого задания того же проекта.";
  if (!manifest.complete || manifest.truncationReasons.length)
    return "Неполный или усечённый отчёт блокирует реализацию.";
  if (
    p.changeProposal.proposalId !== proposal.proposal.id ||
    p.changeProposal.proposalRevisionId !== r.id ||
    proposal.proposal.currentDraftRevisionId !== r.id ||
    proposal.proposal.currentDraftHash !== r.semanticHash ||
    p.draftPins.effectiveSemanticHash !== r.semanticHash ||
    p.baseRevisionId !== r.baseRevisionId ||
    p.baseSource.semanticHash !== r.baseSemanticHash
  )
    return "Отчёт относится к другой базе или сохранённому черновику.";
  if (
    criteria.length !== r.criteria.length ||
    r.criteria.some(
      (c) =>
        criteria.filter(
          (row) =>
            row.criterionKey === c.key &&
            row.criterionKind === c.kind &&
            row.required === c.required,
        ).length !== 1,
    )
  )
    return "Реестр отчёта не совпадает с критериями сохранённого черновика.";
  const blockers = criteria.filter(
    (c) => c.required && (c.outcome !== "satisfied" || c.criterionKind === "runtime_check"),
  );
  if (blockers.length)
    return `Обязательные критерии не подтверждены: ${blockers.map((c) => c.criterionKey).join(", ")}. Исключения не обходят этот запрет.`;
  return "";
}

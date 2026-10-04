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
    response.data.job.projectId !== projectId
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

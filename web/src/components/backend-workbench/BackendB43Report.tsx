import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import type {
  BackendAnalysisJobDetail,
  BackendAnalysisResultRecord,
  BackendChangeProposalDetail,
  BackendChangeProposalException,
  GetBackendAnalysisResultsParams,
} from "@/api/generated/schemas";
import {
  readAnalysisResults,
  readConformanceCriteria,
  analysisImplementedReason,
} from "./backendAnalysisReads";
import { makeAnalysisAttempt, useBackendAnalysisRecovery } from "./backendAnalysisRecovery";
import { AnalysisValue } from "./BackendImpactReport";
import { LoadState, Pages } from "./BackendReadUI";
const wrap = { overflowWrap: "anywhere" as const };
const outcomes = {
  satisfied: "Подтверждён структурно",
  violated: "Нарушен",
  unverified: "Не проверен",
};
export function BackendB43Report({
  projectId,
  detail,
  resultVersion,
  proposal,
  dirty = false,
  onSaved,
}: {
  projectId: string;
  detail: BackendAnalysisJobDetail;
  resultVersion: number;
  proposal?: BackendChangeProposalDetail;
  dirty?: boolean;
  onSaved?: (value: BackendChangeProposalDetail) => void;
}) {
  const [section, setSection] = useState<GetBackendAnalysisResultsParams["section"]>(
    detail.job.kind === "conformance" ? "checks" : "findings",
  );
  const [cursors, setCursors] = useState([""]),
    [exceptions, setExceptions] = useState<BackendChangeProposalException[]>([]);
  const recovery = useBackendAnalysisRecovery(projectId);
  const params = { section, resultVersion, limit: 100, cursor: cursors.at(-1) ?? "" };
  const query = useQuery({
    queryKey: ["backend-b43-report", projectId, detail.job.id, params],
    queryFn: ({ signal }) => readAnalysisResults(projectId, detail, params, signal),
    retry: false,
    staleTime: Infinity,
  });
  const criteria = useQuery({
    queryKey: ["backend-conformance-criteria", projectId, detail.job.id, resultVersion],
    queryFn: ({ signal }) => readConformanceCriteria(projectId, detail, resultVersion, signal),
    enabled: detail.job.kind === "conformance",
    retry: false,
    staleTime: Infinity,
  });
  const manifest = query.data?.manifest;
  const reason =
    analysisImplementedReason(detail, manifest, proposal, criteria.data?.items, dirty) ||
    (manifest && criteria.data && criteria.data.resultHash !== manifest.semanticResultHash
      ? "Хеш реестра не совпадает с отчётом."
      : "");
  async function implement() {
    if (
      reason ||
      !manifest ||
      !proposal ||
      recovery.blocked ||
      detail.input.documentVersion !== "backend-analysis-context-v2" ||
      detail.input.kind !== "conformance"
    )
      return;
    const p = detail.input.payload;
    const result = await recovery.execute(
      makeAnalysisAttempt(
        "implemented",
        { projectId, proposalId: proposal.proposal.id },
        {
          action: "implemented",
          expectedVersion: proposal.proposal.version,
          proposalRevisionId: proposal.revision.id,
          report: {
            jobId: detail.job.id,
            resultVersion,
            inputHash: manifest.analysisInputHash,
            resultHash: manifest.semanticResultHash,
          },
          resultRevisionId: p.resultRevisionId,
          exceptions,
          idempotencyKey: crypto.randomUUID(),
        },
        {
          draftHash: proposal.revision.semanticHash,
          resultSemanticHash: p.resultSource.semanticHash,
        },
      ),
    );
    if (result && "proposal" in result)
      onSaved?.({ ...proposal, proposal: result.proposal, revision: result.revision });
  }
  return (
    <Paper
      withBorder
      p="md"
      component="section"
      aria-label="Неизменяемый отчёт"
      style={{ minWidth: 0, ...wrap }}
    >
      <Stack>
        <Title order={4}>Отчёт · версия {resultVersion}</Title>
        <Text size="sm">Версия закреплена. Новая публикация не меняет этот отчёт.</Text>
        <Badge color="yellow" c="var(--mantine-color-text)">
          Выполнение приложения: не проверено
        </Badge>
        <AnalysisValue
          label="Точные входы, source и artifact pins"
          value={{ inputHash: detail.job.analysisInputHash, input: detail.input }}
        />
        <NativeSelect
          label="Раздел отчёта"
          value={section}
          data={[
            { value: "findings", label: "Выводы" },
            { value: "changes", label: "Изменения" },
            { value: "witnesses", label: "Основания и пути" },
            { value: "checks", label: "Критерии и проверки" },
            { value: "gaps", label: "Пробелы" },
          ]}
          onChange={(e) => {
            setSection(e.currentTarget.value as typeof section);
            setCursors([""]);
          }}
        />
        <LoadState query={query} label="отчёта" />
        {manifest && (
          <>
            <Text>
              {manifest.complete ? "Обход завершён" : "Частичный результат"} · {manifest.verdict}
            </Text>
            <AnalysisValue label="Манифест: хеш, покрытие, ограничения" value={manifest} />
            {manifest.truncationReasons.map((g) => (
              <Alert color="yellow" key={g.id}>
                {g.message}
              </Alert>
            ))}
            {query.data?.items.map((item) => (
              <B43Record key={item.id} item={item} />
            ))}
            <Pages
              label="отчёта"
              cursors={cursors}
              setCursors={setCursors}
              next={query.data?.nextCursor}
              busy={query.isFetching}
            />
          </>
        )}
        {detail.job.kind === "conformance" && proposal && (
          <Stack gap="sm">
            <Title order={5}>Зафиксировать реализацию</Title>
            <LoadState query={criteria} label="полного реестра критериев" />
            {reason && <Text>{reason}</Text>}
            {criteria.data?.items
              .filter((c) => c.required && c.outcome !== "satisfied")
              .map((c) => (
                <Alert color="yellow" key={c.criterionKey}>
                  {c.criterionKey}: {outcomes[c.outcome]} — {c.reason}
                </Alert>
              ))}
            <Text size="sm">
              Исключения — ручные пояснения. Они не меняют исход проверки и не подтверждают
              выполнение.
            </Text>
            {criteria.data?.items
              .filter((c) => !c.required)
              .map((c) => {
                const x = exceptions.find((e) => e.criterionKey === c.criterionKey);
                return (
                  <Stack gap="xs" key={c.criterionKey}>
                    <Checkbox
                      label={`Добавить исключение ${c.criterionKey}`}
                      checked={!!x}
                      onChange={(e) => {
                        const checked = e.currentTarget.checked;
                        setExceptions((old) =>
                          checked
                            ? [...old, { criterionKey: c.criterionKey, author: "", reason: "" }]
                            : old.filter((x) => x.criterionKey !== c.criterionKey),
                        );
                      }}
                    />
                    {x && (
                      <>
                        <TextInput
                          label={`Автор исключения ${c.criterionKey}`}
                          maxLength={4096}
                          value={x.author}
                          onChange={(e) => {
                            const value = e.currentTarget.value;
                            setExceptions((old) =>
                              old.map((x) =>
                                x.criterionKey === c.criterionKey ? { ...x, author: value } : x,
                              ),
                            );
                          }}
                        />
                        <TextInput
                          label={`Причина исключения ${c.criterionKey}`}
                          maxLength={4096}
                          value={x.reason}
                          onChange={(e) => {
                            const value = e.currentTarget.value;
                            setExceptions((old) =>
                              old.map((x) =>
                                x.criterionKey === c.criterionKey ? { ...x, reason: value } : x,
                              ),
                            );
                          }}
                        />
                      </>
                    )}
                  </Stack>
                );
              })}
            <Button
              disabled={
                !!reason ||
                recovery.blocked ||
                exceptions.some((x) => !x.author.trim() || !x.reason.trim())
              }
              onClick={() => void implement()}
            >
              Отметить реализованным
            </Button>
          </Stack>
        )}
      </Stack>
    </Paper>
  );
}
function B43Record({ item }: { item: BackendAnalysisResultRecord }) {
  const d = item.detail;
  return (
    <Paper withBorder p="sm" style={{ minWidth: 0, ...wrap }}>
      <Stack gap="xs">
        <Text size="xs">
          {item.object.recordType} {item.object.id} · {item.certainty}
        </Text>
        {"type" in d && d.documentVersion === "backend-b43-result-v1" ? (
          (() => {
            switch (d.type) {
              case "package_header":
                return (
                  <>
                    <Title order={5}>Пакет {d.complete ? "полный" : "частичный"}</Title>
                    <Text size="sm">Хеш пакета: {d.packageHash}</Text>
                    <Text size="sm">
                      База: {d.baseRevisionId} · черновик {d.changeProposal.proposalRevisionId}
                    </Text>
                  </>
                );
              case "package_change":
              case "outside_intent_change":
              case "endpoint_change":
                return (
                  <>
                    <Text>
                      {d.type === "outside_intent_change" ? "Вне намерения: " : ""}
                      {d.change.operation} · {d.change.facet} · {d.change.paths.join(", ")}
                    </Text>
                    <AnalysisValue label="До" value={d.change.before} />
                    <AnalysisValue label="После" value={d.change.after} />
                  </>
                );
              case "package_evidence":
                return (
                  <>
                    <Text>
                      Основание: {d.origin === "baseline" ? "Исходная база" : "Намерение"} ·{" "}
                      {d.proof.side} · {d.proof.status}
                    </Text>
                    <AnalysisValue label="Точное доказательство" value={d.proof} />
                  </>
                );
              case "package_criterion":
                return (
                  <>
                    <Text>
                      {d.criterion.key} · {d.criterion.kind} ·{" "}
                      {d.criterion.required ? "обязательный" : "необязательный"}
                    </Text>
                    <Text>{d.criterion.description}</Text>
                    <AnalysisValue label="Точный критерий" value={d.criterion} />
                  </>
                );
              case "recommended_check":
              case "endpoint_check":
                return (
                  <>
                    <Text>{d.check.message}</Text>
                    <Text size="sm">
                      Происхождение:{" "}
                      {d.origin === "analysis_rule"
                        ? "правило анализа (рекомендация)"
                        : "критерий предложения"}{" "}
                      · {d.required ? "обязательный" : "необязательный"}
                    </Text>
                    <Text size="sm">
                      {d.check.ruleId} · {d.check.version} · {d.check.status}
                    </Text>
                    {"criterionKey" in d && d.criterionKey && (
                      <Text>Критерий: {d.criterionKey}</Text>
                    )}
                    <AnalysisValue label="Основания проверки" value={d.check.evidence} />
                  </>
                );
              case "conformance_criterion":
                return (
                  <>
                    <Text>
                      {d.criterionKey} · {d.criterionKind} ·{" "}
                      {d.required ? "обязательный" : "необязательный"}
                    </Text>
                    <Text fw={600}>{outcomes[d.outcome]}</Text>
                    <Text>{d.reason}</Text>
                    <Text size="sm">
                      Предложение: {d.proposalObject?.id ?? "нет"} · источник:{" "}
                      {d.sourceObject?.id ?? "нет"}
                    </Text>
                    {d.basis.map((p, i) => (
                      <Text size="sm" key={i}>
                        Основание {i + 1}: {p.side} · {p.status} · {p.reasons.join(", ")}
                      </Text>
                    ))}
                    <AnalysisValue
                      label="Точные основания и положительное доказательство удаления"
                      value={{ basis: d.basis, deletionBasis: d.deletionBasis }}
                    />
                  </>
                );
              case "conformance_summary":
                return (
                  <>
                    <Title order={5}>
                      Соответствие: {d.satisfiedCount} из {d.criteriaCount} подтверждены
                    </Title>
                    <Text>
                      Нарушено: {d.violatedCount} · не проверено: {d.unverifiedCount}
                    </Text>
                    <Text size="sm">
                      Источник: {d.resultRevisionId} · {d.resultSemanticHash}
                    </Text>
                    <Text>Поведение: не проверено</Text>
                  </>
                );
              case "endpoint_item":
                return (
                  <>
                    <Title order={5}>
                      {
                        {
                          write: "Запись",
                          error_branch: "Ветка ошибки",
                          emitted_event: "Отправляемое событие",
                          affected_consumer: "Затронутый потребитель",
                        }[d.category]
                      }{" "}
                      · {d.side === "before" ? "до" : "после"}
                    </Title>
                    <Text>Путь: {d.witness.status}</Text>
                    <ol>
                      {d.witness.steps.map((s, i) => (
                        <li key={i}>
                          <Text size="sm">
                            {s.kind}: {s.from.id} → {s.to.id}
                          </Text>
                          <AnalysisValue label="Основания шага" value={s.evidence} />
                        </li>
                      ))}
                    </ol>
                    <AnalysisValue label="Основания endpoint" value={d.proof} />
                  </>
                );
            }
          })()
        ) : "message" in d ? (
          <Text>{d.message}</Text>
        ) : (
          <AnalysisValue label="Детали" value={d} />
        )}
      </Stack>
    </Paper>
  );
}

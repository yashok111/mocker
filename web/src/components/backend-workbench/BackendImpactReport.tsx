import classes from "./BackendAnalysisControls.module.css";
import { BackendAnalysisArtifact } from "./BackendAnalysisArtifact";
import { ArtifactTypedContent } from "./BackendArtifactContent";
import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  NativeSelect,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type {
  BackendAnalysisJobDetail,
  BackendAnalysisResultRecord,
  BackendChangeProposalDetail,
  BackendReadTarget,
  GetBackendAnalysisResultsParams,
} from "@/api/generated/schemas";
import { readAnalysisResults, analysisReadyReason } from "./backendAnalysisReads";
import { makeAnalysisAttempt, useBackendAnalysisRecovery } from "./backendAnalysisRecovery";
import { BackendExactRecordInspector } from "./BackendExactGraph";
import { LoadState, Pages } from "./BackendReadUI";
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
export function AnalysisValue({ label, value }: { label: string; value: unknown }) {
  return (
    <details>
      <summary>{label}</summary>
      <Code block style={wrap}>
        {JSON.stringify(value, null, 2)}
      </Code>
    </details>
  );
}
export function BackendImpactReport(props: {
  projectId: string;
  detail: BackendAnalysisJobDetail;
  resultVersion: number;
  proposal?: BackendChangeProposalDetail;
  dirty?: boolean;
  onSaved?: (value: BackendChangeProposalDetail) => void;
}) {
  return <ReportPage key={`${props.detail.job.id}:${props.resultVersion}`} {...props} />;
}
function ReportPage({
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
  const [section, setSection] = useState<GetBackendAnalysisResultsParams["section"]>("findings");
  const [filters, setFilters] = useState<
    Omit<GetBackendAnalysisResultsParams, "section" | "resultVersion">
  >({});
  const [cursors, setCursors] = useState([""]);
  const [ack, setAck] = useState<string[]>([]);
  const recovery = useBackendAnalysisRecovery(projectId);
  const params = { ...filters, resultVersion, section, limit: 100, cursor: cursors.at(-1) ?? "" };
  const query = useQuery({
    queryKey: ["backend-analysis-results", projectId, detail.job.id, params],
    queryFn: ({ signal }) => readAnalysisResults(projectId, detail, params, signal),
    staleTime: Infinity,
    retry: false,
  });
  const page = query.data,
    manifest = page?.manifest;
  const reason = analysisReadyReason(detail, manifest, proposal, dirty);
  const gaps = [...new Set(manifest?.gaps.map((x) => x.id) ?? [])].sort();
  const acknowledged = JSON.stringify([...ack].sort()) === JSON.stringify(gaps);
  function filter(key: keyof typeof filters, value: string) {
    setFilters((p) => ({
      ...p,
      [key]: value === "" ? undefined : key === "depth" ? Number(value) : value,
    }));
    setCursors([""]);
  }
  async function ready() {
    if (reason || !manifest || !proposal || !acknowledged || recovery.blocked) return;
    const result = await recovery.execute(
      makeAnalysisAttempt(
        "ready",
        { projectId, proposalId: proposal.proposal.id },
        {
          expectedVersion: proposal.proposal.version,
          proposalRevisionId: proposal.revision.id,
          action: "ready",
          idempotencyKey: crypto.randomUUID(),
          report: {
            jobId: detail.job.id,
            resultVersion,
            inputHash: manifest.analysisInputHash,
            resultHash: manifest.semanticResultHash,
          },
          acknowledgedGapIds: gaps,
        },
        { draftHash: proposal.revision.semanticHash },
      ),
    );
    if (result && "proposal" in result && "revision" in result)
      onSaved?.({ ...proposal, proposal: result.proposal, revision: result.revision });
  }
  return (
    <Paper
      withBorder
      p="md"
      component="section"
      aria-label="Неизменяемый отчёт"
      style={{ minWidth: 0 }}
    >
      <Stack>
        <Title order={4}>Отчёт · версия {resultVersion}</Title>
        <Text size="sm">Версия закреплена. Новая публикация не меняет этот отчёт.</Text>
        <Badge>Выполнение не проверено</Badge>
        <AnalysisValue
          label="Вход: источник, черновик, буфер и их хеши"
          value={{
            inputHash: detail.job.analysisInputHash,
            from: detail.input.fromRevisionId,
            target: detail.input.target,
          }}
        />
        <AnalysisValue
          label="Точные source и artifact pins обеих сторон"
          value={{
            before: detail.input.beforePins,
            after: detail.input.afterPins,
            beforeSource: detail.input.beforeSource,
            afterSource: detail.input.afterSource,
          }}
        />
        <Text size="sm">
          Правила: {detail.input.ruleSetVersion} · обход: {detail.input.traversalVersion}
        </Text>
        <div className={classes.grid}>
          <NativeSelect
            label="Раздел отчёта"
            value={section}
            data={[
              { value: "findings", label: "Выводы" },
              { value: "changes", label: "Изменения" },
              { value: "witnesses", label: "Пути влияния" },
              { value: "checks", label: "Проверки" },
              { value: "gaps", label: "Пробелы" },
            ]}
            onChange={(e) => {
              setSection(e.currentTarget.value as typeof section);
              setCursors([""]);
            }}
          />
          <TextInput
            label="Сервис отчёта"
            value={filters.service ?? ""}
            onChange={(e) => filter("service", e.currentTarget.value)}
          />
          <TextInput
            label="Вид записи отчёта"
            value={filters.kind ?? ""}
            onChange={(e) => filter("kind", e.currentTarget.value)}
          />
        </div>
        <div className={classes.grid}>
          <NativeSelect
            label="Достоверность"
            value={filters.certainty ?? ""}
            data={["", "confirmed", "possible", "unknown"]}
            onChange={(e) => filter("certainty", e.currentTarget.value)}
          />
          <NativeSelect
            label="Направление отчёта"
            value={filters.direction ?? ""}
            data={["", "upstream", "downstream", "both"]}
            onChange={(e) => filter("direction", e.currentTarget.value)}
          />
          <NativeSelect
            label="Глубина отчёта"
            value={filters.depth === undefined ? "" : String(filters.depth)}
            data={["", ...Array.from({ length: 33 }, (_, i) => String(i))]}
            onChange={(e) => filter("depth", e.currentTarget.value)}
          />
        </div>
        <LoadState query={query} label="отчёта" />
        {manifest && (
          <>
            <Text>
              {manifest.complete ? "Обход завершён" : "Частичный результат"} · вывод:{" "}
              {manifest.verdict}
            </Text>
            <Text size="sm">
              Покрытие источников: до — {manifest.sourceCoverageBefore.status || "ещё не оценено"};
              после — {manifest.sourceCoverageAfter.status || "ещё не оценено"}.
            </Text>
            <AnalysisValue
              label="Покрытие и ограничения источников"
              value={{ before: manifest.sourceCoverageBefore, after: manifest.sourceCoverageAfter }}
            />
            <AnalysisValue
              label="Хеш результата и область анализа"
              value={{
                resultHash: manifest.semanticResultHash,
                scope: manifest.scope,
                changed: manifest.changedIds,
                covered: manifest.coveredChangedIds,
              }}
            />
            {manifest.truncationReasons.map((g) => (
              <Alert color="yellow" key={g.id}>
                {g.message} ({g.code})
              </Alert>
            ))}
            {page.items.map((item) => (
              <ReportRecord key={item.id} projectId={projectId} detail={detail} item={item} />
            ))}
            <Pages
              label="отчёта"
              cursors={cursors}
              setCursors={setCursors}
              next={page.nextCursor}
              busy={query.isFetching}
            />
            {proposal && (
              <Stack gap="xs">
                <Title order={5}>Готовность черновика</Title>
                {reason && <Text>{reason}</Text>}
                {manifest.gaps.map((g) => (
                  <Checkbox
                    key={g.id}
                    label={`${g.message} (${g.code})`}
                    checked={ack.includes(g.id)}
                    onChange={(e) => {
                      const checked = e.currentTarget.checked;
                      setAck((old) => (checked ? [...old, g.id] : old.filter((id) => id !== g.id)));
                    }}
                  />
                ))}
                {!acknowledged && (
                  <Text size="sm">Подтвердите каждый пробел выбранного отчёта.</Text>
                )}
                <Button
                  disabled={!!reason || !acknowledged || recovery.blocked}
                  onClick={() => void ready()}
                >
                  Отметить черновик готовым
                </Button>
              </Stack>
            )}
          </>
        )}
      </Stack>
    </Paper>
  );
}
// Analysis addresses can name source claims or other facets rather than graph records.
function isGraphRecordAddress(object: {
  id: string;
  recordType: string;
}): object is { id: string; recordType: "node" | "edge" } {
  return (
    (object.recordType === "node" || object.recordType === "edge") &&
    /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(object.id) &&
    object.id !== "00000000-0000-0000-0000-000000000000"
  );
}
function ReportRecord({
  projectId,
  detail,
  item,
}: {
  projectId: string;
  detail: BackendAnalysisJobDetail;
  item: BackendAnalysisResultRecord;
}) {
  const [selection, setSelection] = useState<{
    id: string;
    recordType: "node" | "edge";
    side: "before" | "after";
  }>();
  const value = item.detail;
  const target: BackendReadTarget | undefined =
    selection?.side === "before"
      ? { revisionId: detail.input.fromRevisionId }
      : "commandPreview" in detail.input.target
        ? undefined
        : detail.input.target;
  const pins = selection?.side === "before" ? detail.input.beforePins : detail.input.afterPins;
  function inspect(object: { id: string; recordType: string }, side: "before" | "after") {
    if (isGraphRecordAddress(object)) setSelection({ ...object, side });
  }
  const objectButtons = (object: { id: string; recordType: string }, side: "before" | "after") =>
    isGraphRecordAddress(object) && (
      <Button size="xs" variant="subtle" onClick={() => inspect(object, side)}>
        Осмотреть {object.recordType} · {side}
      </Button>
    );
  return (
    <Paper withBorder p="sm" style={{ minWidth: 0 }}>
      <Stack gap="xs">
        <Text style={wrap}>
          {item.object.recordType} {item.object.id} · {item.kind} · {item.certainty} ·{" "}
          {item.direction} · глубина {item.depth}
        </Text>
        {"side" in value && "steps" in value ? (
          <>
            <Text>
              Путь влияния: {value.side} · {value.status}
            </Text>
            {objectButtons(value.seed, value.side)}
            {objectButtons(value.affected, value.side)}
            <ol>
              {value.steps.map((step, i) => (
                <li key={i}>
                  <Text style={wrap}>
                    {step.kind}: {step.from.id} → {step.to.id}
                  </Text>
                  {objectButtons(step.from, value.side)}
                  {objectButtons(step.to, value.side)}
                  {step.context && (
                    <AnalysisValue label="Контекст отображения / маршрута" value={step.context} />
                  )}
                  <AnalysisValue
                    label="Основания шага (сторона, статус, evidence)"
                    value={step.evidence}
                  />
                  {step.artifact && (
                    <>
                      <AnalysisValue
                        label="Точный объект артефакта"
                        value={{ locator: step.artifact, objectHash: step.objectHash }}
                      />
                      <BackendAnalysisArtifact
                        projectId={projectId}
                        target={
                          value.side === "before"
                            ? { revisionId: detail.input.fromRevisionId }
                            : "commandPreview" in detail.input.target
                              ? undefined
                              : detail.input.target
                        }
                        pins={
                          value.side === "before" ? detail.input.beforePins : detail.input.afterPins
                        }
                        locator={step.artifact}
                        objectHash={step.objectHash}
                      />
                    </>
                  )}
                </li>
              ))}
            </ol>
          </>
        ) : "ruleId" in value ? (
          <>
            <Text>{value.message}</Text>
            <Text size="sm">
              {value.ruleId} · {value.status} · {value.severity}
            </Text>
            <AnalysisValue label="Основания правила по сторонам" value={value.evidence} />
            {value.evidence.map((proof, i) => (
              <div key={i}>{objectButtons(item.object, proof.side)}</div>
            ))}
          </>
        ) : "operation" in value ? (
          <>
            <Text>
              {value.operation} · {value.facet} · {value.paths.join(", ")}
            </Text>
            <AnalysisValue label="До" value={value.before} />
            <AnalysisValue label="После" value={value.after} />
            {value.operation !== "added" && objectButtons(item.object, "before")}
            {value.operation !== "removed" && objectButtons(item.object, "after")}
          </>
        ) : "message" in value ? (
          <Text>{value.message}</Text>
        ) : (
          <>
            <AnalysisValue label="Изменение артефакта с точными pins" value={value} />
            {value.before && <ArtifactTypedContent data={value.before.data} />}{" "}
            {value.after && <ArtifactTypedContent data={value.after.data} />}
          </>
        )}
        {selection && (
          <section aria-label="Объект из отчёта">
            <Button variant="subtle" onClick={() => setSelection(undefined)}>
              Закрыть объект отчёта
            </Button>
            {target ? (
              <BackendExactRecordInspector
                claimsSupported={pins.structuralSchemaVersion === "6"}
                projectId={projectId}
                target={target}
                pins={target.revisionId && pins.viewSchemaVersion !== "6" ? undefined : pins}
                recordType={selection.recordType}
                id={selection.id}
              />
            ) : (
              <Text>
                После: замороженный несохранённый буфер. Значения и основания доступны в этом
                отчёте; текущий черновик не подменяет этот снимок.
              </Text>
            )}
          </section>
        )}
      </Stack>
    </Paper>
  );
}

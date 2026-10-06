import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import {
  listBackendFindings,
  reviewBackendFinding,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendFindingItem,
  BackendReadTarget,
  ReviewBackendFindingRequest,
} from "@/api/generated/schemas";
import { BackendExactEvidence } from "./BackendExactEvidence";
import { LoadState, Pages } from "./BackendReadUI";
import { changeMessage, changeUncertain } from "./backendChangeRecovery";

export function BackendFindings({
  projectId,
  jobId,
  resultVersion,
  target,
}: {
  projectId: string;
  jobId: string;
  resultVersion: number;
  target: BackendReadTarget;
}) {
  const [cursors, setCursors] = useState([""]);
  const query = useQuery({
    queryKey: ["backend-findings", projectId, jobId, resultVersion, cursors.at(-1)],
    queryFn: async ({ signal }) => {
      const response = await listBackendFindings(
        projectId,
        { jobId, resultVersion, limit: 100, ...(cursors.at(-1) ? { cursor: cursors.at(-1) } : {}) },
        { signal },
      );
      if (
        response.status !== 200 ||
        response.data.items.some(
          (item) => item.analysis.jobId !== jobId || item.analysis.resultVersion !== resultVersion,
        )
      )
        throw new Error("Получены findings другого отчёта");
      return response.data;
    },
    retry: false,
  });
  return (
    <Stack data-testid="backend-findings" aria-label="Диагностические findings">
      <Title order={4}>Диагностика · версия отчёта {resultVersion}</Title>
      <Alert color="blue">
        Структурные выводы источника и авторские правила не подтверждают runtime. Решение review
        сохраняется отдельно от отчёта.
      </Alert>
      <LoadState query={query} label="findings" />
      {query.data?.items.length === 0 && (
        <Text>В этой версии отчёта findings нет. Полноту проверки смотрите в gaps отчёта.</Text>
      )}
      {query.data?.items.map((item) => (
        <Finding
          key={`${item.finding.fingerprint}:${item.review.version}`}
          projectId={projectId}
          item={item}
          target={target}
          reload={() => void query.refetch()}
        />
      ))}
      {query.data && (
        <Pages
          cursors={cursors}
          setCursors={setCursors}
          next={query.data.nextCursor}
          label="findings"
          busy={query.isFetching}
        />
      )}
    </Stack>
  );
}
function Finding({
  projectId,
  item,
  target,
  reload,
}: {
  projectId: string;
  item: BackendFindingItem;
  target: BackendReadTarget;
  reload: () => void;
}) {
  const f = item.finding,
    r = item.review;
  const [status, setStatus] = useState<ReviewBackendFindingRequest["status"]>("accepted_risk");
  const [reason, setReason] = useState("");
  const [job, setJob] = useState("");
  const [version, setVersion] = useState("");
  const recoveryKey = `backend-finding-review:${projectId}:${f.fingerprint}`;
  const [pending, setPending] = useState<ReviewBackendFindingRequest | undefined>(() => {
    try {
      const raw = sessionStorage.getItem(recoveryKey);
      if (!raw) return undefined;
      const saved = JSON.parse(raw) as ReviewBackendFindingRequest;
      if (
        !Number.isSafeInteger(saved.expectedVersion) ||
        saved.expectedVersion < 1 ||
        typeof saved.idempotencyKey !== "string" ||
        !saved.idempotencyKey ||
        typeof saved.reason !== "string" ||
        typeof saved.basisHash !== "string"
      )
        return undefined;
      return saved;
    } catch {
      return undefined;
    }
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [evidence, setEvidence] = useState<string>();
  const historical = r.basisHash !== f.basisHash;
  async function save() {
    if (busy || (historical && !pending)) return;
    const input = pending ?? {
      expectedVersion: r.version,
      basisHash: f.basisHash,
      status,
      reason,
      idempotencyKey: crypto.randomUUID(),
      ...(status === "resolved"
        ? { resolutionAnalysis: { jobId: job, resultVersion: Number(version) } }
        : {}),
    };
    try {
      sessionStorage.setItem(recoveryKey, JSON.stringify(input));
    } catch {
      setError("Не удалось сохранить ключ восстановления. Review не отправлен.");
      return;
    }
    setPending(input);
    setBusy(true);
    setError("");
    try {
      const response = await reviewBackendFinding(projectId, f.fingerprint, input);
      if (response.status !== 200) throw new Error("Review не сохранён");
      sessionStorage.removeItem(recoveryKey);
      setPending(undefined);
      reload();
    } catch (e) {
      setError(changeMessage(e));
      if (!changeUncertain(e)) {
        sessionStorage.removeItem(recoveryKey);
        setPending(undefined);
      }
    } finally {
      setBusy(false);
    }
  }
  return (
    <Paper withBorder p="sm">
      <Stack>
        <Group>
          <Text fw={600}>{f.rule}</Text>
          <Badge>{f.certainty}</Badge>
          <Badge>
            {r.status} · review {r.version}
          </Badge>
        </Group>
        <Text>{f.message}</Text>
        {f.witness && (
          <details>
            <summary>Структурный witness</summary>
            <Text size="sm" style={{ overflowWrap: "anywhere" }}>
              {f.witness.records.map((record) => `${record.recordType}:${record.id}`).join(" → ")}
            </Text>
            {f.witness.missingRef && (
              <Text size="sm" style={{ overflowWrap: "anywhere" }}>
                Missing ref: {JSON.stringify(f.witness.missingRef)} · original pin{" "}
                {JSON.stringify(f.witness.originalDiagramPin)}
              </Text>
            )}
            {f.witness.ruleId && (
              <Text size="sm">
                Rule {f.witness.ruleId} · transition {f.witness.transitionId}
              </Text>
            )}
            {f.witness.facetDifferences.map((pair, index) => (
              <Text key={index} size="sm">
                {pair.leftFacetKey} ↔ {pair.rightFacetKey}: {pair.changedPaths.join(", ")}
              </Text>
            ))}
          </details>
        )}
        <Text size="xs" style={{ overflowWrap: "anywhere" }}>
          basis {f.basisHash}
        </Text>
        <Text size="sm">Prerequisites: {f.prerequisites.join(", ")}</Text>
        {f.gaps.length > 0 && <Alert color="yellow">Unknown / gaps: {f.gaps.join(", ")}</Alert>}
        {f.diagramScope && (
          <Text size="sm" style={{ overflowWrap: "anywhere" }}>
            Диаграмма {f.diagramScope.pin.id} · v{f.diagramScope.pin.version} · scope{" "}
            {f.diagramScope.scopeHash} · refs {f.diagramScope.sourceRefs.length} · gaps{" "}
            {f.diagramScope.gaps.length}
            {f.diagramScope.truncated ? " · truncated" : ""}
          </Text>
        )}
        <Group>
          {f.evidenceIds.map((id) => (
            <Button key={id} variant="subtle" onClick={() => setEvidence(id)}>
              Основание {id}
            </Button>
          ))}
        </Group>
        {evidence && (
          <BackendExactEvidence
            key={evidence}
            projectId={projectId}
            target={target}
            evidenceId={evidence}
          />
        )}
        {historical && (
          <Alert color="yellow">
            Есть новая basis. Эта occurrence историческая; откройте новый отчёт для review.
          </Alert>
        )}
        <NativeSelect
          label="Решение review"
          value={status}
          disabled={!!pending || historical}
          data={["open", "accepted_risk", "false_positive", "resolved"]}
          onChange={(e) => setStatus(e.currentTarget.value as typeof status)}
        />
        <Textarea
          label="Причина решения"
          value={reason}
          maxLength={4096}
          disabled={!!pending || historical}
          onChange={(e) => setReason(e.currentTarget.value)}
        />
        {status === "resolved" && (
          <Group>
            <TextInput
              label="Job ID завершённой перепроверки"
              value={job}
              disabled={!!pending}
              onChange={(e) => setJob(e.currentTarget.value)}
            />
            <TextInput
              label="Result version перепроверки"
              value={version}
              disabled={!!pending}
              onChange={(e) => setVersion(e.currentTarget.value)}
            />
          </Group>
        )}
        {pending && (
          <Alert color="blue">
            Сохранён запрос review: {pending.status} · {pending.reason}. Повтор использует исходные
            version, basis и ключ.
          </Alert>
        )}
        {error && (
          <Alert color="red">
            {error} Повтор отправляет тот же ключ и тело. При конфликте загрузите актуальную
            историю.
          </Alert>
        )}
        <Group>
          <Button
            loading={busy}
            disabled={
              (historical && !pending) ||
              (!pending && !reason.trim()) ||
              (!pending &&
                status === "resolved" &&
                (!job || !Number.isSafeInteger(Number(version)) || Number(version) < 1))
            }
            onClick={() => void save()}
          >
            {pending ? "Повторить тот же review" : "Сохранить review"}
          </Button>
          <Button variant="default" disabled={busy} onClick={reload}>
            Обновить историю
          </Button>
        </Group>
        <details>
          <summary>История решений ({r.history.length})</summary>
          {r.history.map((event) => (
            <Text key={event.version} size="sm">
              v{event.version} · {event.status} · {event.author} · {event.at} · {event.reason} ·
              basis {event.basisHash}
            </Text>
          ))}
        </details>
      </Stack>
    </Paper>
  );
}

import { hashBackendJSON } from "./backendImportHash";
import { useRef } from "react";
import { Badge, Button, Stack, Text, Title } from "@mantine/core";
import type { BackendChangeProposalDetail } from "@/api/generated/schemas";
import { makeAnalysisAttempt, useBackendAnalysisRecovery } from "./backendAnalysisRecovery";
import { AnalysisValue } from "./BackendImpactReport";
export function BackendChangeLifecycle({
  projectId,
  proposal,
  disabled,
  onSaved,
  onReport,
}: {
  projectId: string;
  proposal: BackendChangeProposalDetail;
  disabled: boolean;
  onReport?: (report: { jobId: string; resultVersion: number }) => void;
  onSaved: (value: BackendChangeProposalDetail) => void;
}) {
  const recovery = useBackendAnalysisRecovery(projectId),
    focus = useRef<HTMLHeadingElement>(null);
  const p = proposal.proposal;
  async function apply() {
    if (disabled || recovery.blocked || p.currentDraftRevisionId !== proposal.revision.id) return;
    const action = p.status === "archived" ? "unarchive" : "archive";
    const result = await recovery.execute(
      makeAnalysisAttempt(
        action,
        { projectId, proposalId: p.id },
        {
          action,
          expectedVersion: p.version,
          proposalRevisionId: proposal.revision.id,
          idempotencyKey: crypto.randomUUID(),
        },
        {
          draftHash: proposal.revision.semanticHash,
          ...(action === "archive"
            ? {
                associationHash: await hashBackendJSON({
                  readyReference: p.readyReference ?? null,
                  implementedReference: p.implementedReference ?? null,
                }),
              }
            : {}),
        },
      ),
    );
    if (result && "proposal" in result) {
      onSaved({ ...proposal, proposal: result.proposal, revision: result.revision });
      focus.current?.focus();
    }
  }
  return (
    <section aria-label="Состояние предложения">
      <Stack gap="xs">
        <Title order={4} tabIndex={-1} ref={focus}>
          Состояние предложения
        </Title>
        <Text aria-live="polite">
          Статус: {p.status} · версия {p.version}
        </Text>
        {p.implementedReference && (
          <>
            <Badge color="yellow" c="var(--mantine-color-text)">
              Поведение не проверено
            </Badge>
            <Text size="sm" style={{ overflowWrap: "anywhere" }}>
              Реализованный источник: {p.implementedReference.resultRevisionId}
            </Text>
            <Text size="sm">
              Отчёт: {p.implementedReference.report.jobId} · версия{" "}
              {p.implementedReference.report.resultVersion}. Откройте это задание и версию в списке
              анализа.
            </Text>
            <Button variant="subtle" onClick={() => onReport?.(p.implementedReference!.report)}>
              Открыть отчёт реализации
            </Button>
            {p.implementedReference.exceptions.map((e) => (
              <Text key={e.criterionKey}>
                Ручное исключение {e.criterionKey} · {e.author}: {e.reason}
              </Text>
            ))}
            <AnalysisValue label="Точная ассоциация реализации" value={p.implementedReference} />
          </>
        )}
        {p.readyReference && (
          <>
            <AnalysisValue label="Точный отчёт готовности" value={p.readyReference} />
            <Button variant="subtle" onClick={() => onReport?.(p.readyReference!.report)}>
              Открыть отчёт готовности
            </Button>
          </>
        )}
        <Text size="sm">
          Архив сохраняет текущие ассоциации. Возврат в черновик очищает активные ассоциации;
          неизменяемые отчёты остаются в заданиях анализа.
        </Text>
        <Button
          variant="default"
          disabled={
            disabled || recovery.blocked || p.currentDraftRevisionId !== proposal.revision.id
          }
          onClick={() => void apply()}
        >
          {p.status === "archived" ? "Вернуть в черновик" : "Архивировать предложение"}
        </Button>
      </Stack>
    </section>
  );
}

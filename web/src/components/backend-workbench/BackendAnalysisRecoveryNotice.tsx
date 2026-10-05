import { useQuery } from "@tanstack/react-query";
import { readAnalysis, analysisTerminal } from "./backendAnalysisReads";
import { Alert, Button, Group, Stack, Text } from "@mantine/core";
import { useBackendAnalysisRecovery } from "./backendAnalysisRecovery";
export function BackendAnalysisRecoveryNotice({ projectId }: { projectId: string }) {
  const recovery = useBackendAnalysisRecovery(projectId),
    attempt = recovery.attempt;
  const jobId = attempt?.acceptance.jobId ?? attempt?.owner.jobId;
  const job = useQuery({
    queryKey: ["backend-recovery-known-job", projectId, jobId],
    enabled: !!jobId,
    retry: false,
    queryFn: ({ signal }) => readAnalysis(projectId, jobId!, signal),
    refetchInterval: (q) =>
      q.state.data && analysisTerminal(q.state.data.job.status) ? false : 2000,
  });
  if (!attempt && !recovery.error && !recovery.message) return null;
  return (
    <Alert
      color="yellow"
      aria-live="polite"
      title="Восстановление операции"
      styles={{ title: { color: "var(--mantine-color-text)" } }}
    >
      <Stack gap="xs">
        {recovery.error && <Text>{recovery.error.message}</Text>}
        {recovery.message && <Text>{recovery.message}</Text>}
        {attempt && (
          <>
            <Text style={{ overflowWrap: "anywhere" }}>
              Операция: {attempt.kind} · проект {attempt.owner.projectId} {attempt.owner.proposalId}{" "}
              {attempt.owner.jobId}
            </Text>
            <Text>
              {attempt.phase === "accepted"
                ? "Ответ подтверждён, но очистка записи ещё не завершена."
                : attempt.phase === "conflict"
                  ? "Конфликт: исходный запрос сохранён. Сверьте состояние перед новым предпросмотром."
                  : "Исход запроса неизвестен. Повтор использует исходные параметры и ключ."}
            </Text>
            {attempt.conflict && <Text>{attempt.conflict}</Text>}
            <details>
              <summary>Исходный запрос и ключ</summary>
              <Text
                component="pre"
                size="xs"
                style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
              >
                {attempt.body}
              </Text>
            </details>
          </>
        )}
        {jobId && (
          <>
            <Text style={{ overflowWrap: "anywhere" }}>
              Известное задание {jobId}: {job.data?.job.status ?? "ожидает чтения"}
            </Text>
            <Button variant="default" onClick={() => void job.refetch()}>
              Проверить известное задание
            </Button>
          </>
        )}
        <Group>
          <Button variant="default" disabled={recovery.busy} onClick={recovery.reload}>
            Перечитать восстановление
          </Button>
          {attempt?.phase === "unknown" && (
            <Button
              disabled={recovery.busy || !!recovery.error}
              onClick={() => void recovery.execute()}
            >
              Повторить исходный запрос
            </Button>
          )}
          {attempt?.phase === "accepted" && (
            <Button disabled={recovery.busy} onClick={() => recovery.clear()}>
              Завершить очистку
            </Button>
          )}
          {attempt?.phase === "conflict" && (
            <Button
              disabled={recovery.busy}
              onClick={() => {
                if (
                  window.confirm(
                    "Состояние сверено? Исходный конфликт будет снят; следующий запрос потребует нового предпросмотра.",
                  )
                )
                  if (attempt.kind === "rebase") void recovery.reconcileRebase();
                  else recovery.clear();
              }}
            >
              Сверено — разрешить новый предпросмотр
            </Button>
          )}
        </Group>
      </Stack>
    </Alert>
  );
}

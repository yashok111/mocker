import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Group, Stack, Text } from "@mantine/core";
import {
  createBackendChangeProposal,
  applyBackendChangeProposalCommands,
  restoreBackendChangeProposal,
} from "@/api/generated/backend-projects/backend-projects";
import { ApiFailure } from "@/api/client";
import {
  discoverProjectChangeRecovery,
  changeMessage,
  changeUncertain,
  writeChangeRecovery,
  type ChangeRecoverySlot,
} from "./backendChangeRecovery";
import { readChangeProposal, verifyChangeDetail } from "./backendChangeReads";
export function BackendLegacyRecoveryNotice({ projectId }: { projectId: string }) {
  const [slots, setSlots] = useState(() => discoverProjectChangeRecovery(projectId));
  const [busy, setBusy] = useState(false),
    [message, setMessage] = useState("");
  const [accepted, setAccepted] = useState<Record<string, string>>({});
  const reload = useCallback(() => {
    const next = discoverProjectChangeRecovery(projectId);
    setSlots(next);
    setMessage("");
    setAccepted((previous) =>
      Object.fromEntries(
        Object.entries(previous).filter(([key, raw]) =>
          next.some((slot) => slot.key === key && slot.raw === raw),
        ),
      ),
    );
  }, [projectId]);
  useEffect(() => {
    window.addEventListener("backend-recovery-change", reload);
    window.addEventListener("storage", reload);
    return () => {
      window.removeEventListener("backend-recovery-change", reload);
      window.removeEventListener("storage", reload);
    };
  }, [reload]);
  const isAccepted = (slot: ChangeRecoverySlot) =>
    slot.raw !== null && accepted[slot.key] === slot.raw;
  async function replay(slot: ChangeRecoverySlot) {
    if (busy || slot.error || !slot.attempt || slot.attempt.phase !== "unknown" || isAccepted(slot))
      return;
    setBusy(true);
    setMessage("");
    const a = slot.attempt;
    let replayRaw = slot.raw;
    try {
      const pid = slot.key.split(":")[2];
      if (a.kind !== "create" && (!pid || !/^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(pid)))
        throw new Error("Нет точного владельца исходного запроса");
      const base =
        a.kind !== "create"
          ? await readChangeProposal(projectId, pid!, a.input.proposalRevisionId)
          : undefined;
      replayRaw = writeChangeRecovery(slot.key, a, slot.raw);
      if (a.kind === "create") {
        const r = await createBackendChangeProposal(projectId, a.input);
        if (r.status !== 200) throw new Error("Создание не подтверждено");
        const d = verifyChangeDetail(r.data, projectId);
        if (d.revision.baseRevisionId !== a.input.baseRevisionId)
          throw new Error("Ответ вернул другую базу");
      } else {
        const r =
          a.kind === "apply"
            ? await applyBackendChangeProposalCommands(projectId, pid!, a.input)
            : await restoreBackendChangeProposal(projectId, pid!, a.input);
        if (
          r.status !== 200 ||
          r.data.proposal.projectId !== projectId ||
          r.data.proposal.id !== pid ||
          r.data.revision.parentRevisionId !== a.input.proposalRevisionId ||
          r.data.proposal.version !== a.input.expectedVersion + 1 ||
          r.data.revision.baseRevisionId !== base!.revision.baseRevisionId ||
          r.data.revision.baseSemanticHash !== base!.revision.baseSemanticHash
        )
          throw new Error("Ответ не подтвердил исходный запрос");
      }
      setAccepted((old) => ({ ...old, [slot.key]: replayRaw! }));
      setMessage(
        "Исходный запрос подтверждён. Перечитайте предложение для просмотра принятой ревизии.",
      );
      writeChangeRecovery(slot.key, null, replayRaw);
    } catch (error) {
      setMessage(changeMessage(error));
      if (error instanceof ApiFailure && error.status === 409) {
        try {
          writeChangeRecovery(slot.key, { ...a, phase: "conflict" }, replayRaw);
        } catch (failure) {
          setMessage(changeMessage(failure));
        }
      } else if (!changeUncertain(error)) {
        try {
          writeChangeRecovery(slot.key, null, replayRaw);
        } catch (failure) {
          setMessage(changeMessage(failure));
        }
      }
    } finally {
      setBusy(false);
    }
  }
  if (!slots.length && !message) return null;
  return (
    <Alert color="yellow" title="Сохранённые операции предложений">
      <Stack gap="xs">
        {message && <Text>{message}</Text>}
        {slots.map((slot) => (
          <Stack key={slot.key} gap="xs">
            <Text size="sm" style={{ overflowWrap: "anywhere" }}>
              {slot.key} · {slot.attempt?.kind ?? "повреждённая запись"}
            </Text>
            {slot.error && <Text>{slot.error.message}</Text>}
            {isAccepted(slot) && <Text>Ответ подтверждён; очистка записи не завершена.</Text>}
            <Group>
              {slot.attempt?.phase === "unknown" && !isAccepted(slot) && (
                <Button disabled={busy || !!slot.error} onClick={() => void replay(slot)}>
                  Восстановить исходную операцию предложения
                </Button>
              )}
              {(slot.attempt?.phase === "conflict" || isAccepted(slot)) && (
                <Button
                  disabled={busy}
                  onClick={() => {
                    if (
                      isAccepted(slot) ||
                      window.confirm(
                        "Предложение сверено? Новая операция потребует нового предпросмотра.",
                      )
                    ) {
                      try {
                        writeChangeRecovery(slot.key, null, slot.raw);
                      } catch (failure) {
                        setMessage(changeMessage(failure));
                      }
                    }
                  }}
                >
                  Завершить восстановление предложения
                </Button>
              )}
            </Group>
          </Stack>
        ))}
        <Button variant="default" disabled={busy} onClick={reload}>
          Перечитать операции предложений
        </Button>
      </Stack>
    </Alert>
  );
}

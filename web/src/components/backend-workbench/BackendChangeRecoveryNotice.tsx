import { useState } from "react";
import { Alert, Button, Checkbox, Group, NativeSelect, Stack, Text } from "@mantine/core";
import type { useBackendChangeRecovery } from "./useBackendChangeRecovery";

export function BackendChangeRecoveryNotice({
  recovery,
  busy = false,
  onReload,
}: {
  recovery: ReturnType<typeof useBackendChangeRecovery>;
  busy?: boolean;
  onReload?: () => void;
}) {
  const [confirm, setConfirm] = useState(false);
  if (!recovery.error && recovery.options.length < 2) return null;
  return (
    <Alert color="red" role="alert">
      <Stack gap="sm">
        <Text>{recovery.error?.message ?? "Выберите незавершённый запрос."}</Text>
        {recovery.options.length > 1 && (
          <NativeSelect
            label="Исходный запрос восстановления"
            value={recovery.selectedKey}
            data={[
              { value: "", label: "Выберите запрос" },
              ...recovery.options.map((slot) => ({
                value: slot.key,
                label:
                  slot.attempt?.kind === "create"
                    ? `${slot.attempt.input.name} · база ${slot.attempt.input.baseRevisionId}`
                    : slot.key,
              })),
            ]}
            onChange={(event) => {
              setConfirm(false);
              recovery.select(event.currentTarget.value);
            }}
            disabled={busy || recovery.cleanupPending}
          />
        )}
        <Group>
          <Button
            variant="default"
            disabled={busy}
            onClick={() => {
              setConfirm(false);
              (onReload ?? recovery.reload)();
            }}
          >
            Перечитать восстановление
          </Button>
          {recovery.cleanupPending && (
            <Button variant="default" disabled={busy} onClick={() => recovery.clear(true)}>
              Удалить подтверждённую запись
            </Button>
          )}
        </Group>
        {(recovery.raw !== null || !!recovery.attempt) && !recovery.cleanupPending && (
          <>
            <Checkbox
              label="Удалить локальную запись; это не отменяет принятый сервером запрос"
              checked={confirm}
              onChange={(event) => setConfirm(event.currentTarget.checked)}
              disabled={busy}
            />
            <Button
              color="red"
              variant="light"
              disabled={busy || !confirm}
              onClick={() => {
                if (recovery.clear()) setConfirm(false);
              }}
            >
              Удалить запись после проверки
            </Button>
          </>
        )}
      </Stack>
    </Alert>
  );
}

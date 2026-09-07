import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Button, Card, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { invalidateCheckpointChange } from "@/api/cachePolicy";
import { useResetOverrides } from "@/api/generated/checkpoints/checkpoints.ts";
import { describeApiFailure } from "@/api/errors";

export function ResetOverridesCard({
  id,
  scenarioActive,
}: {
  id: number;
  scenarioActive: boolean;
}): ReactElement {
  const queryClient = useQueryClient();
  const [result, setResult] = useState<{ changed: boolean } | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const resetOverrides = useResetOverrides({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        setFailure(null);
        setResult({ changed: res.data.changed });
        // C9: the pre-destructive checkpoint (when changed) lands in the
        // list, and revision only bumps when changed is true — but
        // invalidating both unconditionally costs one idle GET on the no-op
        // path and is simpler than this component re-deriving C9's own
        // no-op rule just to decide whether to invalidate.
        invalidateCheckpointChange(queryClient, id);
      },
      onError: (err) => setFailure(describeApiFailure(err)),
    },
  });

  function handleReset(): void {
    modals.openConfirmModal({
      title: "Сбросить всё к спеке",
      children: (
        <Stack gap="xs">
          <Text size="sm">
            Будут удалены ВСЕ правки операций и ВСЕ свои эндпоинт&apos;ы воркспейса — их нет в
            спеке, поэтому «сбросить всё к спеке» удаляет и то, и другое. В правки операций записан
            и пресет авторизации: он пропадёт вместе с остальными, и фронтенд под тестом перестанет
            логиниться. Settings (seed, basePath, ключ подписи) сброс не трогает.
          </Text>
          {scenarioActive ? (
            <Text size="sm" c="orange" data-testid="reset-scenario-warning">
              Сейчас активен сценарий — часть слоя воркспейса, к которому сброс вернёт спеку,
              по-прежнему останется замаскирована сценарием, пока его не деактивируют.
            </Text>
          ) : null}
          <Text size="sm" c="dimmed">
            Перед сбросом сохраняется точка текущего состояния — действие можно отменить откатом на
            неё.
          </Text>
        </Stack>
      ),
      labels: { confirm: "Сбросить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "reset-confirm-submit" },
      cancelProps: { "data-testid": "dialog-cancel" },
      onConfirm: () => {
        setFailure(null);
        resetOverrides.mutate({ id });
      },
    });
  }

  return (
    <Card withBorder p="md" data-testid="reset-overrides-card">
      <Stack gap="sm">
        {failure !== null ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {failure}
          </Alert>
        ) : null}
        {result !== null ? (
          <Text size="sm" data-testid="reset-result">
            {result.changed
              ? "Сброшено. Правки операций и свои эндпоинты удалены."
              : "Сбрасывать было нечего — правок и своих эндпоинтов уже не было."}
          </Text>
        ) : null}
        <Button
          variant="default"
          color="red"
          w="fit-content"
          leftSection={<IconAlertTriangle size={16} />}
          onClick={handleReset}
          loading={resetOverrides.isPending}
          data-testid="reset-overrides-button"
        >
          Сбросить всё к спеке
        </Button>
      </Stack>
    </Card>
  );
}

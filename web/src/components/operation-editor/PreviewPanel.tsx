import type { ReactElement } from "react";
import { Alert, Badge, Code, Group, Stack, Text } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import type { PreviewResultView } from "@/api/generated/schemas";
import { describePreviewRefusalReason } from "@/api/errors";

// PreviewPanel renders D5's result document, field for field. It is a
// WINDOW, not a workbench (§H): every value here is read-only, nothing it
// shows can be edited from this panel, and `shadowedBy` is rendered
// whenever it is non-null — a preview that silently supplied a scenario's
// row instead of the draft just edited is exactly the confusion this field
// exists to prevent, so it cannot be the one field this panel drops.
export function PreviewPanel({ result }: { result: PreviewResultView }): ReactElement {
  const statusSourceLabel: Record<PreviewResultView["statusSource"], string> = {
    requested: "запрошен явно",
    when: "выбран условием when[]",
    active: "активный статус операции",
    default: "статус по умолчанию из спеки",
  };

  return (
    <Stack gap="xs" data-testid="operation-preview-result">
      <Group gap="xs">
        <Badge size="lg" variant="filled" color={result.status < 400 ? "green" : "orange"}>
          {result.status}
        </Badge>
        <Text size="xs" c="dimmed">
          {statusSourceLabel[result.statusSource]}
        </Text>
      </Group>

      {result.shadowedBy !== null ? (
        <Alert
          color="yellow"
          icon={<IconAlertTriangle size={18} />}
          data-testid="operation-preview-shadowed-by"
        >
          Строка взята из активного сценария «{result.shadowedBy}», а не из этого черновика — пока
          сценарий активен, показанное здесь — не то, что задаёт эта форма.
        </Alert>
      ) : null}

      {result.routeOff ? (
        <Text size="sm" c="dimmed" data-testid="operation-preview-route-off">
          Операция выключена — мок ответил бы отказом маршрута, а не телом.
        </Text>
      ) : result.refused !== null ? (
        <Alert
          color="red"
          icon={<IconAlertTriangle size={18} />}
          data-testid="operation-preview-refused"
        >
          <Text size="sm">{describePreviewRefusalReason(result.refused.reason)}</Text>
          <Text size="xs" c="dimmed">
            {result.refused.detail}
          </Text>
        </Alert>
      ) : result.noBody ? (
        <Text size="sm" c="dimmed" data-testid="operation-preview-no-body">
          Без тела (204/205, Degraded или у операции вообще нет объявленного варианта ответа).
        </Text>
      ) : (
        <>
          <Text size="xs" c="dimmed">
            {result.mediaType} · {result.encoding}
          </Text>
          <Code block data-testid="operation-preview-body">
            {result.body}
          </Code>
        </>
      )}

      <Group gap="md">
        <Text size="xs" c="dimmed">
          правки схемы: {result.schemaPatchApplied ? "применены" : "нет"}
        </Text>
        <Text size="xs" c="dimmed">
          автоматических значений: {result.recipesBound}
        </Text>
        <Text size="xs" c="dimmed">
          задержка: {result.delayMs} мс
        </Text>
      </Group>
    </Stack>
  );
}

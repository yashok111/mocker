import { useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Button,
  Card,
  Group,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useResetData } from "@/api/generated/resources/resources.ts";
import type { ResetDataResult } from "@/api/generated/schemas";
import { ResetMode } from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";

// D14.3's four "{routeFamily} — пропущено: …" lines, keyed by
// ResetDataSkippedFamilyReason's own enum values — the only place an
// operator learns why a family's data did not come back.
const SKIP_REASON_TEXT: Record<string, string> = {
  stranded: "семейства нет в текущей спеке",
  over_caps: "не помещается в лимиты",
  population_failed: "не удалось сгенерировать записи",
  group_skipped: "пропущено вместе с родителем или потомком, которого не удалось заполнить",
};

// The RESET half of the resources surface (P3b, D9): a card beside
// ResetOverridesCard above, not a modal — typing the workspace's own slug is
// itself the confirmation step, the same shape the destructive MCP tools use
// (confirmSlug), so there is nothing left for a second dialog to gate.
// Unlike ResetOverridesCard's confirmation, this card has NO undo to promise:
// reset-overrides writes its own pre-destructive checkpoint before it acts,
// and reset-data cannot — a checkpoint's config_snap never carries an entity
// row, so there is nothing this route's own effect could be rolled back
// from (D3, D8). The warning body says exactly that, and it is rendered
// with the card, not gated behind a click.
export function ResetDataCard({
  id,
  workspaceSlug,
}: {
  id: number;
  workspaceSlug: string;
}): ReactElement {
  const [mode, setMode] = useState<ResetMode>(ResetMode.reseed);
  const [slug, setSlug] = useState("");
  const [result, setResult] = useState<ResetDataResult | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const resetData = useResetData({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          return;
        }
        setFailure(null);
        setResult(res.data);
      },
      onError: (err) => {
        setResult(null);
        setFailure(describeApiFailureDetailed(err));
      },
    },
  });

  function handleReset(): void {
    setFailure(null);
    resetData.mutate({ id, data: { mode, confirmSlug: slug } });
  }

  function handleCancel(): void {
    setMode(ResetMode.reseed);
    setSlug("");
    setResult(null);
    setFailure(null);
  }

  return (
    <Card withBorder p="md" data-testid="reset-data-card">
      <Stack gap="sm">
        <Title order={3}>Сбросить данные ресурсов</Title>
        <Text size="sm" data-testid="reset-data-warning">
          Это НЕОБРАТИМО: записи, созданные через POST, будут удалены. В отличие от отката и сброса
          правок, «сбросить данные ресурсов» не сохраняет свою собственную точку перед тем, как
          стереть — если не сохранить чекпойнт вручную заранее, восстановить записи будет нечем.
          «Заполнить заново» запишет то, что даёт текущая конфигурация воркспейса, а не то, что было
          при подтверждении, и сбросит счётчик идентификаторов на размер новой популяции —
          идентификатор, который клиент уже получал и удалял, может быть выдан снова. «Очистить»
          оставит коллекции пустыми и НЕ сбросит счётчик — следующая запись получит следующий номер,
          а не первый.
        </Text>
        {failure !== null ? (
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {failure}
          </Alert>
        ) : null}
        {result !== null ? (
          <Stack gap={4} data-testid="reset-data-result">
            <Text size="sm">
              {result.changed ? `Удалено записей: ${result.deleted}` : "Ничего не изменилось"}
            </Text>
            {result.skipped.map((s) => (
              <Text size="sm" c="dimmed" key={`${s.routeFamily}-${s.reason}`}>
                {s.routeFamily} — пропущено: {SKIP_REASON_TEXT[s.reason]}
              </Text>
            ))}
          </Stack>
        ) : null}
        <SegmentedControl
          value={mode}
          onChange={(value) => setMode(value as ResetMode)}
          data={[
            { label: "Заполнить заново", value: ResetMode.reseed },
            { label: "Очистить", value: ResetMode.clear },
          ]}
          data-testid="reset-data-mode"
        />
        <TextInput
          label="Слаг воркспейса"
          value={slug}
          onChange={(e) => setSlug(e.currentTarget.value)}
          data-testid="reset-data-slug"
        />
        <Group gap="xs">
          <Button
            color="red"
            leftSection={<IconAlertTriangle size={16} />}
            onClick={handleReset}
            loading={resetData.isPending}
            // A21 (U10): the same local check the rollback modal makes —
            // an empty or wrong slug was a round trip to a 409 here.
            disabled={workspaceSlug !== "" && slug.trim() !== workspaceSlug}
            data-testid="reset-data-submit"
          >
            Сбросить
          </Button>
          <Button variant="default" onClick={handleCancel} data-testid="reset-data-cancel">
            Отмена
          </Button>
        </Group>
      </Stack>
    </Card>
  );
}

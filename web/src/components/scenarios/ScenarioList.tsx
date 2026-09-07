import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Anchor, Badge, Button, Card, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import {
  IconAlertTriangle,
  IconCopy,
  IconEdit,
  IconPlayerPlay,
  IconPlayerStop,
  IconTrash,
} from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { invalidateScenarioChange } from "@/api/cachePolicy";
import {
  useActivateScenario,
  useDeactivateScenario,
  useDeleteScenario,
} from "@/api/generated/scenarios/scenarios.ts";
import type { ScenarioSummaryView } from "@/api/generated/schemas";
import { describeApiFailure } from "@/api/errors";
import { formatTimestamp } from "@/format";
import { CloneScenarioForm } from "./CloneScenarioForm";
import { RenameScenarioForm } from "./RenameScenarioForm";
import { ScenarioDetails } from "./ScenarioDetails";

export function ScenarioList({
  id,
  scenarios,
}: {
  id: number;
  scenarios: ScenarioSummaryView[];
}): ReactElement {
  const queryClient = useQueryClient();
  // Named per-row rather than read off one mutation's own .error: three
  // different mutations (activate/deactivate/delete) share this one alert,
  // and none of them remembers on its own WHICH scenario it was acting on.
  const [actionError, setActionError] = useState<{ label: string; message: string } | null>(null);
  // A21 (G2, four of five readers): what a scenario holds was visible only
  // AFTER activating it, through the mask banner on «Операции спеки».
  const [openId, setOpenId] = useState<number | null>(null);

  function invalidateAfterWrite(): void {
    // Activate, deactivate AND delete (when the deleted scenario was
    // active, A9) all move workspace.scenarioId and bump revision — the tab
    // bar's own "ревизия N" text and screen 5's A18 banner both read that
    // straight off the workspace query, so every write here has to
    // invalidate it too, not just the scenario list.
    invalidateScenarioChange(queryClient, id);
  }

  const activateScenario = useActivateScenario({
    mutation: {
      onSuccess: () => {
        setActionError(null);
        invalidateAfterWrite();
      },
    },
  });
  const deactivateScenario = useDeactivateScenario({
    mutation: {
      onSuccess: () => {
        setActionError(null);
        invalidateAfterWrite();
      },
    },
  });
  const deleteScenario = useDeleteScenario({
    mutation: {
      onSuccess: () => {
        setActionError(null);
        invalidateAfterWrite();
      },
    },
  });

  function handleActivate(sc: ScenarioSummaryView): void {
    activateScenario.mutate(
      { id, sid: sc.id },
      { onError: (err) => setActionError({ label: sc.name, message: describeApiFailure(err) }) },
    );
  }

  function handleDeactivate(sc: ScenarioSummaryView): void {
    deactivateScenario.mutate(
      { id },
      { onError: (err) => setActionError({ label: sc.name, message: describeApiFailure(err) }) },
    );
  }

  function handleDelete(sc: ScenarioSummaryView): void {
    modals.openConfirmModal({
      title: "Удалить сценарий",
      children: (
        <Text size="sm">
          Удалить «{sc.name}»? Это действие необратимо.
          {sc.isActive
            ? " Сценарий сейчас активен — удаление деактивирует его, и воркспейс вернётся к своему собственному слою."
            : ""}
        </Text>
      ),
      labels: { confirm: "Удалить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "scenario-delete-confirm" },
      cancelProps: { "data-testid": "dialog-cancel" },
      onConfirm: () => {
        deleteScenario.mutate(
          { id, sid: sc.id },
          {
            onError: (err) => setActionError({ label: sc.name, message: describeApiFailure(err) }),
          },
        );
      },
    });
  }

  // Clone and rename each get their own modal rather than
  // modals.openConfirmModal's static children: both need a live TextInput
  // plus a mutation with its own pending/error state, and openConfirmModal
  // has no seam for either — closeModal(modalId) is what lets the form
  // component itself decide when the round trip is done, instead of the
  // modal auto-closing the instant "confirm" is clicked.
  function handleClone(sc: ScenarioSummaryView): void {
    const modalId = `scenario-clone-${sc.id}`;
    modals.open({
      modalId,
      title: `Клонировать «${sc.name}»`,
      children: (
        <CloneScenarioForm
          id={id}
          source={sc}
          onCancel={() => modals.close(modalId)}
          onCloned={() => {
            modals.close(modalId);
            invalidateAfterWrite();
          }}
        />
      ),
    });
  }

  function handleRename(sc: ScenarioSummaryView): void {
    const modalId = `scenario-rename-${sc.id}`;
    modals.open({
      modalId,
      title: `Переименовать «${sc.name}»`,
      children: (
        <RenameScenarioForm
          id={id}
          source={sc}
          onCancel={() => modals.close(modalId)}
          onRenamed={() => {
            modals.close(modalId);
            invalidateAfterWrite();
          }}
        />
      ),
    });
  }

  return (
    <Stack gap="sm">
      {actionError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          «{actionError.label}»: {actionError.message}
        </Alert>
      ) : null}
      <Card withBorder p={0} data-testid="scenario-list">
        <Stack gap={0}>
          {scenarios.map((sc) => (
            <Group
              key={sc.id}
              justify="space-between"
              wrap="nowrap"
              px="md"
              py="sm"
              data-testid="scenario-row"
              style={{ borderTop: "1px solid var(--mantine-color-gray-3)" }}
            >
              <div>
                <Group gap="xs">
                  <Text size="sm" fw={500}>
                    {sc.name}
                  </Text>
                  {sc.isActive ? (
                    <Badge color="green" size="sm" data-testid="scenario-active-badge">
                      активен
                    </Badge>
                  ) : null}
                </Group>
                <Text size="xs" c="dimmed">
                  создан {formatTimestamp(sc.createdAt)} ·{" "}
                  <Anchor
                    size="xs"
                    component="button"
                    type="button"
                    onClick={() => setOpenId(openId === sc.id ? null : sc.id)}
                    data-testid="scenario-details-toggle"
                  >
                    {openId === sc.id ? "свернуть" : "что внутри"}
                  </Anchor>
                </Text>
                {openId === sc.id ? <ScenarioDetails id={id} scenarioId={sc.id} /> : null}
              </div>
              <Group gap="xs" wrap="nowrap">
                <Button
                  variant="default"
                  size="xs"
                  leftSection={<IconCopy size={16} />}
                  onClick={() => handleClone(sc)}
                  data-testid="scenario-clone"
                >
                  Клонировать
                </Button>
                <Button
                  variant="default"
                  size="xs"
                  leftSection={<IconEdit size={16} />}
                  onClick={() => handleRename(sc)}
                  data-testid="scenario-rename"
                >
                  Переименовать
                </Button>
                {sc.isActive ? (
                  <Button
                    variant="default"
                    size="xs"
                    leftSection={<IconPlayerStop size={16} />}
                    onClick={() => handleDeactivate(sc)}
                    loading={deactivateScenario.isPending}
                    data-testid="scenario-deactivate"
                  >
                    Деактивировать
                  </Button>
                ) : (
                  <Button
                    variant="default"
                    size="xs"
                    leftSection={<IconPlayerPlay size={16} />}
                    onClick={() => handleActivate(sc)}
                    loading={activateScenario.isPending}
                    data-testid="scenario-activate"
                  >
                    Активировать
                  </Button>
                )}
                <Button
                  variant="default"
                  size="xs"
                  color="red"
                  leftSection={<IconTrash size={16} />}
                  onClick={() => handleDelete(sc)}
                  loading={deleteScenario.isPending}
                  data-testid="scenario-delete"
                >
                  Удалить
                </Button>
              </Group>
            </Group>
          ))}
        </Stack>
      </Card>
    </Stack>
  );
}

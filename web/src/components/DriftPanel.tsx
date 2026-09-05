import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Badge, Button, Group, Loader, Stack, Text, Title } from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle, IconCheck, IconSearch, IconTrash, IconX } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { getGetWorkspaceDriftQueryKey, useGetWorkspaceDrift } from "@/api/generated/drift/drift.ts";
import {
  getListWorkspaceOperationsQueryKey,
  useDeleteOperationOverride,
} from "@/api/generated/operations/operations.ts";
import {
  getListEndpointsQueryKey,
  useDeleteEndpoint,
} from "@/api/generated/endpoints/endpoints.ts";
import { getListWorkspaceResourcesQueryKey } from "@/api/generated/resources/resources.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import type { DriftReportView } from "@/api/generated/schemas";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";
import { DeclineConfirmedForm } from "./ResourcesPage";

// DriftPanel is P4a's screen half — «Проверить спеку» on the overview, the
// screen `CARVE-OUTS.md` "Ideas refused — 2026-09-03" records the owner
// refusing, and which he asked for by name on 2026-09-05 («добей последние
// 4 гэпа», a Russian string quoted as data), reversing that refusal in his
// own words. The report is fetched on the button, not on mount: the route
// CAN derive (it calls the lazy-backfill entry point resource-suggestions
// does), so a panel that fetched on every visit to the overview would pay
// that cost for a person who came to copy the URL.
//
// The route carries no remedy field on purpose (its own description): every
// repair already has its verb, and the three buttons here are exactly those
// verbs — DELETE .../operations/{opKey} with the row's opKey verbatim (it
// arrives percent-encoded, like MergedOperationView's), DELETE
// .../endpoints/{eid}, and the resource decline through the same slug modal
// the resources screen uses. After any of them the report is refetched
// together with the list the verb changed, so the row disappears from here
// and from its own tab at once.
export function DriftPanel({ id }: { id: number }): ReactElement {
  const [asked, setAsked] = useState(false);
  const drift = useGetWorkspaceDrift(id, { query: { enabled: asked } });

  return (
    <div data-testid="drift-panel">
      <Title order={2}>Соответствие спеке</Title>
      <Stack gap="sm" mt="sm">
        <Text size="sm" c="dimmed">
          После смены или переимпорта спеки часть настроек может остаться без основания: правка
          операции, которой больше нет; подтверждённый ресурс без семейства; кастомный endpoint,
          затеняющий операцию спеки. Проверка сравнивает с привязанной сейчас спекой; настройки она
          не меняет (может лишь досчитать подсказки ресурсов для спеки, которой их ещё не считали).
        </Text>
        <Button
          variant="default"
          w="fit-content"
          leftSection={<IconSearch size={16} />}
          loading={asked && drift.isFetching}
          onClick={() => (asked ? void drift.refetch() : setAsked(true))}
          data-testid="drift-check"
        >
          Проверить спеку
        </Button>
        {!asked ? null : drift.isPending ? (
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : drift.isError ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="drift-error"
          >
            {describeApiFailure(drift.error)}
          </Alert>
        ) : drift.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="drift-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : (
          <DriftReport id={id} report={drift.data.data} />
        )}
      </Stack>
    </div>
  );
}

// confirmThen is the same confirm the owning screens put in front of these
// two verbs (OperationEditor's reset, EndpointList's delete): a drift report
// is a LIST, and the mis-click target is the row above — the second reader
// of A20 named the missing confirm on both buttons.
function confirmThen(title: string, text: string, testId: string, run: () => void): void {
  modals.openConfirmModal({
    title,
    children: <Text size="sm">{text}</Text>,
    labels: { confirm: "Удалить", cancel: "Отмена" },
    confirmProps: { color: "red", "data-testid": testId },
    onConfirm: run,
  });
}

function DriftReport({ id, report }: { id: number; report: DriftReportView }): ReactElement {
  const queryClient = useQueryClient();
  const [actionError, setActionError] = useState<{ label: string; message: string } | null>(null);

  function refresh(): void {
    setActionError(null);
    void queryClient.invalidateQueries({ queryKey: getGetWorkspaceDriftQueryKey(id) });
    void queryClient.invalidateQueries({ queryKey: getGetWorkspaceQueryKey(id) });
  }

  const deleteOverride = useDeleteOperationOverride({
    mutation: {
      onSuccess: () => {
        void queryClient.invalidateQueries({ queryKey: getListWorkspaceOperationsQueryKey(id) });
        refresh();
      },
    },
  });
  const deleteEndpoint = useDeleteEndpoint({
    mutation: {
      onSuccess: () => {
        void queryClient.invalidateQueries({ queryKey: getListEndpointsQueryKey(id) });
        refresh();
      },
    },
  });

  function declineResource(row: DriftReportView["orphanedResources"][number]): void {
    const modalId = `drift-decline-${row.routeFamily}`;
    modals.open({
      modalId,
      title: `Отклонить «${row.name}»`,
      children: (
        <DeclineConfirmedForm
          id={id}
          family={row}
          onCancel={() => modals.close(modalId)}
          onDeclined={() => {
            modals.close(modalId);
            void queryClient.invalidateQueries({ queryKey: getListWorkspaceResourcesQueryKey(id) });
            refresh();
          }}
        />
      ),
    });
  }

  if (!report.hasDrift) {
    return (
      <Group gap="xs" data-testid="drift-clean">
        <IconCheck size={16} />
        <Text size="sm">Всё соответствует привязанной спеке.</Text>
      </Group>
    );
  }

  return (
    <Stack gap="sm" data-testid="drift-report">
      {actionError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          «{actionError.label}»: {actionError.message}
        </Alert>
      ) : null}
      {report.orphanedOverrides.length > 0 ? (
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            Правки операций, которых в спеке нет
          </Text>
          {report.orphanedOverrides.map((row) => (
            <Group key={row.opKey} justify="space-between" data-testid="drift-override">
              <Text size="sm" ff="monospace">
                {row.method} {row.path}
              </Text>
              <Button
                variant="default"
                size="xs"
                color="red"
                leftSection={<IconTrash size={16} />}
                loading={deleteOverride.isPending}
                onClick={() =>
                  confirmThen(
                    "Удалить правку",
                    `Удалить правку операции ${row.method} ${row.path}? Закреплённые ответы, условия и подстановки этой операции пропадут; вернуть их можно откатом на чекпойнт.`,
                    "drift-override-delete-confirm",
                    () =>
                      deleteOverride.mutate(
                        { id, opKey: row.opKey },
                        {
                          onError: (err) =>
                            setActionError({
                              label: `${row.method} ${row.path}`,
                              message: describeApiFailureDetailed(err),
                            }),
                        },
                      ),
                  )
                }
                data-testid="drift-override-delete"
              >
                Удалить правку
              </Button>
            </Group>
          ))}
        </Stack>
      ) : null}
      {report.orphanedResources.length > 0 ? (
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            Подтверждённые ресурсы без семейства в спеке
          </Text>
          {report.orphanedResources.map((row) => (
            <Group key={row.routeFamily} justify="space-between" data-testid="drift-resource">
              <Group gap="xs">
                <Text size="sm">{row.name}</Text>
                <Text size="xs" c="dimmed" ff="monospace">
                  {row.routeFamily}
                </Text>
                <Text size="xs" c="dimmed">
                  записей: {row.entityCount}
                </Text>
              </Group>
              <Button
                variant="default"
                size="xs"
                color="red"
                leftSection={<IconX size={16} />}
                onClick={() => declineResource(row)}
                data-testid="drift-resource-decline"
              >
                Отклонить
              </Button>
            </Group>
          ))}
        </Stack>
      ) : null}
      {report.shadowedEndpoints.length > 0 ? (
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            Кастомные endpoint&apos;ы, затеняющие операции спеки
          </Text>
          {report.shadowedEndpoints.map((row) => (
            <Group key={row.endpointId} justify="space-between" data-testid="drift-endpoint">
              <Group gap="xs">
                <Text size="sm" ff="monospace">
                  {row.method} {row.path}
                </Text>
                {row.precededSpec ? (
                  <Badge color="gray" size="sm" data-testid="drift-endpoint-preceded">
                    создан до импорта спеки
                  </Badge>
                ) : null}
              </Group>
              <Button
                variant="default"
                size="xs"
                color="red"
                leftSection={<IconTrash size={16} />}
                loading={deleteEndpoint.isPending}
                onClick={() =>
                  confirmThen(
                    "Удалить endpoint",
                    `Удалить endpoint ${row.method} ${row.path}? Операция спеки снова станет видна; сам endpoint вернуть можно откатом на чекпойнт.`,
                    "drift-endpoint-delete-confirm",
                    () =>
                      deleteEndpoint.mutate(
                        { id, eid: row.endpointId },
                        {
                          onError: (err) =>
                            setActionError({
                              label: `${row.method} ${row.path}`,
                              message: describeApiFailureDetailed(err),
                            }),
                        },
                      ),
                  )
                }
                data-testid="drift-endpoint-delete"
              >
                Удалить endpoint
              </Button>
            </Group>
          ))}
        </Stack>
      ) : null}
    </Stack>
  );
}

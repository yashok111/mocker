import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Badge, Button, Card, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { IconAlertTriangle, IconPencil, IconPlugConnected, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { invalidateEndpointChange } from "@/api/cachePolicy";
import { useDeleteEndpoint } from "@/api/generated/endpoints/endpoints.ts";
import type {
  EndpointView,
  ServerConfigViewLimits,
  StreamDefinition,
} from "@/api/generated/schemas";
import { StreamTestClient } from "../StreamTestClient";
import { describeApiFailure } from "@/api/errors";
import { formatTimestamp } from "@/format";
import { EditEndpointForm } from "./EditEndpointForm";
import { EditStreamForm } from "./EditStreamForm";
import { kindLabel } from "./shared";

/** eventNamesOf lists the named events a definition sends, for the browser
 * client's listeners — EventSource fires `message` only for unnamed frames. */
function eventNamesOf(def: StreamDefinition | undefined): string[] {
  if (!def) {
    return [];
  }
  const names = (def.timeline?.frames ?? []).map((f) => f.event ?? "");
  if (def.tick?.event) {
    names.push(def.tick.event);
  }
  return names.filter((n) => n !== "");
}

// hasFunction answers the row badge: any variant of the endpoint, not only
// the active one, runs Lua — a 500 the agent scripted is worth knowing
// about while the 200 is what serves.
function hasFunction(ep: Pick<EndpointView, "responses">): boolean {
  return Object.values(ep.responses).some((v) => v.function !== undefined && v.function !== "");
}

export function EndpointList({
  id,
  endpoints,
  workspaceUrl,
  limits,
  initialEditingId,
}: {
  id: number;
  endpoints: EndpointView[];
  workspaceUrl: string | undefined;
  limits: ServerConfigViewLimits | undefined;
  initialEditingId?: number;
}): ReactElement {
  const queryClient = useQueryClient();
  // P6e: at most one row's browser test client open at a time, like the
  // edit form — a second open client would hold a second connection.
  const [testingId, setTestingId] = useState<number | null>(null);
  // Named per-row rather than read off deleteEndpoint.error directly: the
  // mutation itself carries no memory of WHICH endpoint it was deleting.
  const [deleteError, setDeleteError] = useState<{ label: string; message: string } | null>(null);
  // At most one row's edit form open at a time — id of the endpoint, or
  // null. A row swaps its own display for the form rather than opening a
  // modal: the whole point of the affordance is showing the CURRENT values
  // next to the fields being changed.
  const [editingId, setEditingId] = useState<number | null>(initialEditingId ?? null);

  const deleteEndpoint = useDeleteEndpoint({
    mutation: {
      onSuccess: () => {
        setDeleteError(null);
        // §3.9: useDeleteEndpoint must invalidate the endpoints list AND the
        // workspace, same as create.
        invalidateEndpointChange(queryClient, id);
      },
    },
  });

  function handleDelete(ep: EndpointView): void {
    const label = `${ep.method} ${ep.path}`;
    modals.openConfirmModal({
      title: "Удалить endpoint",
      children: (
        <Text size="sm">
          Удалить «{label}»? Это действие необратимо. Если нужно просто поправить endpoint, вместо
          удаления используйте кнопку «Изменить» в списке.
        </Text>
      ),
      labels: { confirm: "Удалить", cancel: "Отмена" },
      confirmProps: { color: "red", "data-testid": "endpoint-delete-confirm" },
      cancelProps: { "data-testid": "dialog-cancel" },
      onConfirm: () => {
        deleteEndpoint.mutate(
          { id, eid: ep.id },
          { onError: (err) => setDeleteError({ label, message: describeApiFailure(err) }) },
        );
      },
    });
  }

  return (
    <Stack gap="sm">
      {deleteError !== null ? (
        <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
          Не удалось удалить «{deleteError.label}»: {deleteError.message}
        </Alert>
      ) : null}
      <Card withBorder p={0} data-testid="endpoint-list">
        <Stack gap={0}>
          {endpoints.map((ep) => (
            <Group
              key={ep.id}
              justify="space-between"
              wrap="nowrap"
              px="md"
              py="sm"
              data-testid="endpoint-row"
              style={{ borderTop: "1px solid var(--mantine-color-gray-3)" }}
            >
              {editingId === ep.id ? (
                ep.kind === "http" ? (
                  <EditEndpointForm
                    id={id}
                    endpoint={ep}
                    onDone={() => setEditingId(null)}
                    onCancel={() => setEditingId(null)}
                  />
                ) : (
                  <EditStreamForm
                    id={id}
                    endpoint={ep}
                    limits={limits}
                    onDone={() => setEditingId(null)}
                    onCancel={() => setEditingId(null)}
                  />
                )
              ) : testingId === ep.id && ep.kind !== "http" && workspaceUrl !== undefined ? (
                <Stack gap="xs" w="100%" data-testid="endpoint-test-client">
                  <Group justify="space-between">
                    <Text size="sm" fw={500}>
                      {ep.method} {ep.path} — проверка из браузера
                    </Text>
                    <Button
                      variant="default"
                      size="xs"
                      onClick={() => setTestingId(null)}
                      data-testid="endpoint-test-close"
                    >
                      Свернуть
                    </Button>
                  </Group>
                  <StreamTestClient
                    url={`${workspaceUrl}${ep.path}`}
                    kind={ep.kind}
                    eventNames={eventNamesOf(ep.stream)}
                    testIdPrefix={`endpoint-${ep.id}`}
                  />
                </Stack>
              ) : (
                <>
                  <div>
                    <Group gap="xs">
                      <Text size="sm" fw={500}>
                        {ep.method} {ep.path}
                      </Text>
                      {kindLabel(ep.kind) ? (
                        <Badge color="blue" size="sm" data-testid="endpoint-kind">
                          {kindLabel(ep.kind)}
                        </Badge>
                      ) : null}
                      {ep.routeOff ? (
                        <Badge color="yellow" size="sm">
                          маршрут выключен
                        </Badge>
                      ) : null}
                      {hasFunction(ep) ? (
                        <Badge color="grape" size="sm" data-testid="endpoint-function">
                          функция Lua
                        </Badge>
                      ) : null}
                    </Group>
                    <Text size="xs" c="dimmed">
                      канонический путь {ep.canonicalPath} · активный статус {ep.activeStatus} ·
                      статусы: {Object.keys(ep.responses).join(", ") || "—"}
                    </Text>
                    <Text size="xs" c="dimmed">
                      создан {formatTimestamp(ep.createdAt)} · обновлён{" "}
                      {formatTimestamp(ep.updatedAt)}
                    </Text>
                  </div>
                  <Group gap="xs" wrap="nowrap">
                    {ep.kind !== "http" ? (
                      <Button
                        variant="default"
                        size="xs"
                        leftSection={<IconPlugConnected size={16} />}
                        disabled={workspaceUrl === undefined}
                        onClick={() => setTestingId(ep.id)}
                        data-testid="endpoint-test-toggle"
                      >
                        Проверить
                      </Button>
                    ) : null}
                    <Button
                      variant="default"
                      size="xs"
                      leftSection={<IconPencil size={16} />}
                      onClick={() => setEditingId(ep.id)}
                      data-testid="endpoint-edit-toggle"
                    >
                      Изменить
                    </Button>
                    <Button
                      variant="default"
                      size="xs"
                      color="red"
                      leftSection={<IconTrash size={16} />}
                      onClick={() => handleDelete(ep)}
                      loading={deleteEndpoint.isPending}
                      data-testid="endpoint-delete"
                    >
                      Удалить
                    </Button>
                  </Group>
                </>
              )}
            </Group>
          ))}
        </Stack>
      </Card>
    </Stack>
  );
}

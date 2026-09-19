import { useState } from "react";
import type { ReactElement } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Drawer,
  Group,
  Stack,
  Text,
  Tooltip,
} from "@mantine/core";
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
import classes from "./EndpointList.module.css";

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

const METHOD_COLOR: Record<string, string> = {
  GET: "blue",
  POST: "teal",
  PUT: "orange",
  PATCH: "yellow",
  DELETE: "red",
};

export function EndpointList({
  id,
  endpoints,
  workspaceUrl,
  limits,
  initialEditingId,
  onDeepLinkClose,
}: {
  id: number;
  endpoints: EndpointView[];
  workspaceUrl: string | undefined;
  limits: ServerConfigViewLimits | undefined;
  initialEditingId?: number;
  onDeepLinkClose?: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  // P6e: at most one row's browser test client open at a time, like the
  // edit form — a second open client would hold a second connection.
  const [testingId, setTestingId] = useState<number | null>(null);
  // Named per-row rather than read off deleteEndpoint.error directly: the
  // mutation itself carries no memory of WHICH endpoint it was deleting.
  const [deleteError, setDeleteError] = useState<{ label: string; message: string } | null>(null);
  // At most one endpoint editor is open. The wide drawer keeps the route
  // list stable while the current values remain visible in the editor.
  const [editingId, setEditingId] = useState<number | null>(initialEditingId ?? null);
  const editingEndpoint = endpoints.find((ep) => ep.id === editingId) ?? null;

  function closeEditor(): void {
    setEditingId(null);
    if (initialEditingId !== undefined) {
      onDeepLinkClose?.();
    }
  }

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
      <Card withBorder p={0} data-testid="endpoint-list" className={classes.list}>
        {endpoints.map((ep) => (
          <div key={ep.id} className={classes.rowWrap} data-testid="endpoint-row">
            <Group justify="space-between" wrap="nowrap" px="md" py="sm" gap="md">
              <div className={classes.identity}>
                <Group gap="xs" wrap="wrap">
                  <Badge color={METHOD_COLOR[ep.method] ?? "gray"} variant="light" size="sm">
                    {ep.method}
                  </Badge>
                  <Text size="sm" fw={600} ff="monospace" className={classes.path}>
                    {ep.path}
                  </Text>
                  {kindLabel(ep.kind) ? (
                    <Badge color="cyan" size="sm" data-testid="endpoint-kind">
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
                <Text size="xs" c="dimmed" mt={4}>
                  канонический путь {ep.canonicalPath} · активный статус {ep.activeStatus} ·
                  статусы: {Object.keys(ep.responses).join(", ") || "—"}
                </Text>
                <Text size="xs" c="dimmed">
                  обновлён {formatTimestamp(ep.updatedAt)} · создан {formatTimestamp(ep.createdAt)}
                </Text>
              </div>
              <Group gap={4} wrap="nowrap">
                {ep.kind !== "http" ? (
                  <Tooltip label="Проверить поток в браузере">
                    <ActionIcon
                      variant="subtle"
                      disabled={workspaceUrl === undefined}
                      onClick={() => setTestingId(ep.id)}
                      data-testid="endpoint-test-toggle"
                      aria-label="Проверить поток в браузере"
                    >
                      <IconPlugConnected size={17} />
                    </ActionIcon>
                  </Tooltip>
                ) : null}
                <Tooltip label="Изменить эндпоинт">
                  <ActionIcon
                    variant="subtle"
                    onClick={() => setEditingId(ep.id)}
                    data-testid="endpoint-edit-toggle"
                    aria-label={`Изменить ${ep.method} ${ep.path}`}
                  >
                    <IconPencil size={17} />
                  </ActionIcon>
                </Tooltip>
                <Tooltip label="Удалить эндпоинт">
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    onClick={() => handleDelete(ep)}
                    loading={deleteEndpoint.isPending}
                    data-testid="endpoint-delete"
                    aria-label={`Удалить ${ep.method} ${ep.path}`}
                  >
                    <IconTrash size={17} />
                  </ActionIcon>
                </Tooltip>
              </Group>
            </Group>
            {testingId === ep.id && ep.kind !== "http" && workspaceUrl !== undefined ? (
              <Stack gap="xs" className={classes.testPanel} data-testid="endpoint-test-client">
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
            ) : null}
          </div>
        ))}
      </Card>
      <Drawer
        opened={editingEndpoint !== null}
        onClose={closeEditor}
        title={editingEndpoint ? `${editingEndpoint.method} ${editingEndpoint.path}` : "Эндпоинт"}
        position="right"
        size="min(840px, 100vw)"
        closeButtonProps={{ "aria-label": "Закрыть редактор эндпоинта" }}
        data-testid="endpoint-edit-drawer"
        classNames={{ body: classes.drawerBody, title: classes.drawerTitle }}
      >
        {editingEndpoint?.kind === "http" ? (
          <EditEndpointForm
            id={id}
            endpoint={editingEndpoint}
            onDone={closeEditor}
            onCancel={closeEditor}
          />
        ) : editingEndpoint ? (
          <EditStreamForm
            id={id}
            endpoint={editingEndpoint}
            limits={limits}
            onDone={closeEditor}
            onCancel={closeEditor}
          />
        ) : null}
      </Drawer>
    </Stack>
  );
}

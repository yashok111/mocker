import { useState } from "react";
import type { ReactElement } from "react";
import { Alert, Anchor, Button, Drawer, Group, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle, IconPlus } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useListEndpoints } from "@/api/generated/endpoints/endpoints.ts";
import { useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import type { ServerConfigView } from "@/api/generated/schemas";
import { describeApiFailure } from "@/api/errors";
import { TabLink } from "./TabLink";
import { QueryState } from "./QueryState";
import { CreateEndpointForm } from "./custom-endpoints/CreateEndpointForm";
import { EndpointList } from "./custom-endpoints/EndpointList";
import classes from "./CustomEndpointsPage.module.css";

// CustomEndpointsPage is DESIGN §14 screen 6, P1 subset: a custom endpoint is
// a route this workspace serves that no spec declares (contrast with
// "переопределить операцию" — a custom route that canonically matches a spec
// route, which is configured on the Endpoint'ы screen instead, per DESIGN's
// own wording for screen 6). DESIGN's own preferred way to make one is
// "создать endpoint из запроса" on the traffic screen — a person who has
// never opened OpenAPI just points at a real request. The form here is the
// manual fallback for everyone else.
//
// The outermost element carries data-testid="custom-endpoints-page" OUTSIDE
// every state switch below, matching the marker contract every screen in
// this phase follows: web/src/routes/routes.test.tsx proves the route is
// reachable by finding this marker alone, and it must not depend on how the
// endpoints list itself answers.
//
// The four components this screen is assembled from live in
// ./custom-endpoints/: the file was 1274 lines with the create form, the
// row list and the two editors inlined, and none of them was reachable from
// a reader who only wanted the orchestration. Nothing moved but the code.

export function CustomEndpointsPage({
  id,
  config,
  initialEditingId,
  onDeepLinkClose,
}: {
  id: number;
  /** P7b: the row id the «Контракт» tab linked here with — its edit form
   * opens on mount. */
  initialEditingId?: number;
  /** Clears the route's endpointId search parameter after a deep-linked
   * editor closes. Component tests can omit it; the file route wires it. */
  onDeepLinkClose?: () => void;
  /** The session's server config (A9): its `limits` feed the stream caps
   * strip. Optional so a component test that mounts the screen alone gets
   * the strip's constants instead of a crash. */
  config?: ServerConfigView;
}): ReactElement {
  const [creating, setCreating] = useState(false);
  const endpoints = useListEndpoints(id);
  const limits = config?.limits;
  // The workspace's own public URL, for the browser test client — read from
  // the cache WorkspaceLayout already warmed; a screen that cannot read it
  // (a component test that stubs only the endpoints route) simply has no
  // «Проверить» to offer, it does not fail.
  const workspace = useGetWorkspace(id);
  const workspaceUrl = workspace.data?.status === 200 ? workspace.data.data.url : undefined;
  const navigate = useNavigate();

  return (
    <div data-testid="custom-endpoints-page">
      <Stack gap="lg">
        <Group justify="space-between" align="flex-start" gap="md">
          <div>
            <Title order={2}>Свои эндпоинты</Title>
            <Text size="sm" c="dimmed" mt={4} className={classes.intro}>
              Маршруты вне спеки. Быстрее всего создать эндпоинт из готового ответа на{" "}
              <Anchor
                href={`/workspaces/${id}/traffic`}
                onClick={(e) => {
                  e.preventDefault();
                  void navigate({ to: "/workspaces/$id/traffic", params: { id } });
                }}
              >
                экране трафика
              </Anchor>
              ; совпадающий со спекой маршрут настраивается в{" "}
              <TabLink id={id} tab="operations" testId="endpoints-operations-link">
                операциях спеки
              </TabLink>
              .
            </Text>
          </div>
          <Button
            leftSection={<IconPlus size={16} />}
            onClick={() => setCreating(true)}
            data-testid="endpoint-create-toggle"
          >
            Создать эндпоинт
          </Button>
        </Group>
        <Drawer
          opened={creating}
          onClose={() => setCreating(false)}
          title="Создать эндпоинт"
          position="right"
          size="min(760px, 100vw)"
          closeButtonProps={{ "aria-label": "Закрыть создание эндпоинта" }}
          data-testid="endpoint-create-drawer"
          classNames={{ body: classes.drawerBody }}
        >
          {creating ? <CreateEndpointForm id={id} limits={limits} /> : null}
        </Drawer>
        <QueryState queries={[endpoints]} testIdPrefix="endpoints">
          {endpoints.data?.status !== 200 ? (
            // An unexpected status is not the query failing — no retry button
            // here, because a 2xx-that-is-not-200 will not become a 200 on a
            // second try; the message is the generic one.
            <Alert
              color="red"
              icon={<IconAlertTriangle size={18} />}
              role="alert"
              data-testid="endpoints-error"
            >
              {describeApiFailure(null)}
            </Alert>
          ) : endpoints.data.data.endpoints.length === 0 ? (
            // An empty list is an empty state, not an error — explain what a
            // custom endpoint is FOR rather than leaving a blank card, per the
            // phase brief.
            <Text data-testid="endpoints-empty">
              Своих эндпоинтов пока нет. Сервер сейчас отвечает только на то, что описано в спеке —
              заведите первый через форму выше или через трафик.
            </Text>
          ) : (
            <EndpointList
              id={id}
              endpoints={endpoints.data.data.endpoints}
              workspaceUrl={workspaceUrl}
              limits={limits}
              initialEditingId={initialEditingId}
              onDeepLinkClose={onDeepLinkClose}
            />
          )}
        </QueryState>
      </Stack>
    </div>
  );
}

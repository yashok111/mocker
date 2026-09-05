import type { ReactElement, ReactNode } from "react";
import { Alert, Button, Group, Loader, Stack, Tabs, Text, Title } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import { describeApiFailure } from "@/api/errors";
import { WorkspaceContextBar } from "./WorkspaceContextBar";

// WorkspaceLayout is the frame every /workspaces/$id/* screen renders inside:
// the workspace's own identity (name, slug, revision — what WorkspacePage.tsx
// used to show on its own, before that route split into a layout plus four
// children) and the tab bar between them. It OWNS the workspace fetch's four
// states and renders {children} ONLY on success — rendering its own error
// alert AND the outlet would paint two alerts and two «Повторить» buttons for
// the same failure. Children read the same query key through the warm cache
// (staleTime 30s in production; §3.9 of the phase context — a fetch stub in a
// test must tolerate the same key firing more than once, since tests run with
// staleTime 0).
export function WorkspaceLayout({
  id,
  children,
}: {
  id: number;
  children: ReactNode;
}): ReactElement {
  const workspace = useGetWorkspace(id);
  const navigate = useNavigate();
  const location = useLocation();

  const activeTab = location.pathname.endsWith("/operations")
    ? "operations"
    : location.pathname.endsWith("/endpoints")
      ? "endpoints"
      : location.pathname.endsWith("/traffic")
        ? "traffic"
        : location.pathname.endsWith("/scenarios")
          ? "scenarios"
          : location.pathname.endsWith("/history")
            ? "history"
            : location.pathname.endsWith("/resources")
              ? "resources"
              : location.pathname.endsWith("/connections")
                ? "connections"
                : location.pathname.endsWith("/assets")
                  ? "assets"
                  : location.pathname.endsWith("/contract")
                    ? "contract"
                    : "overview";

  function handleTabChange(value: string | null): void {
    switch (value) {
      case "overview":
        void navigate({ to: "/workspaces/$id", params: { id } });
        break;
      case "operations":
        void navigate({ to: "/workspaces/$id/operations", params: { id } });
        break;
      case "endpoints":
        void navigate({ to: "/workspaces/$id/endpoints", params: { id } });
        break;
      case "traffic":
        void navigate({ to: "/workspaces/$id/traffic", params: { id } });
        break;
      case "scenarios":
        void navigate({ to: "/workspaces/$id/scenarios", params: { id } });
        break;
      case "history":
        void navigate({ to: "/workspaces/$id/history", params: { id } });
        break;
      case "resources":
        void navigate({ to: "/workspaces/$id/resources", params: { id } });
        break;
      case "contract":
        void navigate({ to: "/workspaces/$id/contract", params: { id } });
        break;
      case "connections":
        void navigate({ to: "/workspaces/$id/connections", params: { id } });
        break;
      case "assets":
        void navigate({ to: "/workspaces/$id/assets", params: { id } });
        break;
      default:
      // Mantine's Tabs never emits anything outside its own registered
      // values, and null only fires for an uncontrolled clear this
      // component never allows — nothing to navigate to.
    }
  }

  return (
    <Stack gap="md" data-testid="workspace-layout">
      {workspace.isPending ? (
        // role on the Text, not the Group: the live region should be the
        // sentence a screen reader announces, not the flex box around it.
        <Group gap="xs">
          <Loader size="sm" />
          <Text size="sm" component="output">
            Загрузка…
          </Text>
        </Group>
      ) : workspace.isError ? (
        <Stack gap="sm" data-testid="workspace-layout-error">
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(workspace.error)}
          </Alert>
          <Button
            variant="default"
            w="fit-content"
            onClick={() => void workspace.refetch()}
            data-testid="workspace-layout-retry"
          >
            Повторить
          </Button>
          <Button
            variant="subtle"
            w="fit-content"
            onClick={() => void navigate({ to: "/" })}
            data-testid="workspace-to-list"
          >
            К списку воркспейсов
          </Button>
        </Stack>
      ) : workspace.data.status !== 200 ? (
        <Alert
          color="red"
          icon={<IconAlertTriangle size={18} />}
          role="alert"
          data-testid="workspace-layout-error"
        >
          {describeApiFailure(null)}
        </Alert>
      ) : (
        <>
          <div>
            <Title order={1} data-testid="workspace-detail-name">
              {workspace.data.data.name}
            </Title>
            <WorkspaceContextBar workspace={workspace.data.data} />
          </div>
          <Tabs value={activeTab} onChange={handleTabChange}>
            <Tabs.List>
              <Tabs.Tab value="overview">Обзор</Tabs.Tab>
              <Tabs.Tab value="operations">Операции спеки</Tabs.Tab>
              <Tabs.Tab value="endpoints">Свои эндпоинты</Tabs.Tab>
              <Tabs.Tab value="traffic">Трафик</Tabs.Tab>
              <Tabs.Tab value="scenarios">Сценарии</Tabs.Tab>
              <Tabs.Tab value="history">История</Tabs.Tab>
              <Tabs.Tab value="resources">Ресурсы</Tabs.Tab>
              {/* P6e: the live SSE/WebSocket connections of the mock plane. */}
              <Tabs.Tab value="connections">Соединения</Tabs.Tab>
              {/* A10: uploaded files a mock can serve (DESIGN §32). */}
              <Tabs.Tab value="assets">Файлы</Tabs.Tab>
              {/* P7b: the workspace as one OpenAPI document (DESIGN §34.5). */}
              <Tabs.Tab value="contract">Контракт</Tabs.Tab>
            </Tabs.List>
          </Tabs>
          {children}
        </>
      )}
    </Stack>
  );
}

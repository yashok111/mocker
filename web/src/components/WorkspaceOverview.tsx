import type { ReactElement } from "react";
import { Alert, Anchor, Button, Group, Loader, Stack, Text } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useGetWorkspace } from "@/api/generated/workspaces/workspaces.ts";
import { describeApiFailure } from "@/api/errors";
import { ConnectPanel } from "./ConnectPanel";
import { AuthPresetPanel } from "./AuthPresetPanel";
import { SettingsPanel } from "./SettingsPanel";
import { TransferPanel } from "./TransferPanel";
import { DriftPanel } from "./DriftPanel";
import type { ServerConfigView } from "@/api/generated/schemas";

// WorkspaceOverview is the index child of /workspaces/$id — DESIGN §14
// screen 4's home, where WorkspacePage.tsx's former body lands. It fetches
// its own copy of the workspace rather than reading it from WorkspaceLayout's
// {children} prop (which would mean threading it through every sibling
// route's <Outlet/>): in production TanStack Query dedupes this against the
// cache entry the layout already warmed (staleTime 30s), so it costs no
// extra request, and this component stays independently mountable in a test.
export function WorkspaceOverview({
  id,
  config,
}: {
  id: number;
  config: ServerConfigView;
}): ReactElement {
  const workspace = useGetWorkspace(id);

  return (
    <div data-testid="overview-page">
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
        <Stack gap="sm" data-testid="overview-error">
          <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
            {describeApiFailure(workspace.error)}
          </Alert>
          <Button
            variant="default"
            w="fit-content"
            onClick={() => void workspace.refetch()}
            data-testid="overview-retry"
          >
            Повторить
          </Button>
        </Stack>
      ) : workspace.data.status !== 200 ? (
        <Alert
          color="red"
          icon={<IconAlertTriangle size={18} />}
          role="alert"
          data-testid="overview-error"
        >
          {describeApiFailure(null)}
        </Alert>
      ) : (
        <Stack gap="md">
          {workspace.data.data.specId === null ? (
            // A21 (U7): the one thing a fresh workspace needs was the last
            // divider of the third panel down.
            <Alert color="blue" data-testid="overview-no-spec">
              Спека не привязана: воркспейс отвечает только на свои эндпоинты. Привязать её —{" "}
              <Anchor href="#settings-spec" data-testid="overview-no-spec-link">
                в настройках ниже
              </Anchor>
              , или создайте воркспейс заново с выбранной спекой.
            </Alert>
          ) : null}
          <ConnectPanel workspace={workspace.data.data} config={config} />
          <AuthPresetPanel id={id} />
          <SettingsPanel workspace={workspace.data.data} />
          <TransferPanel workspace={workspace.data.data} />
          <DriftPanel id={id} />
        </Stack>
      )}
    </div>
  );
}

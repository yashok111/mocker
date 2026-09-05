import type { ReactElement } from "react";
import { Badge, Group, Text, Tooltip } from "@mantine/core";
import { useListSpecs } from "@/api/generated/specs/specs.ts";
import { useListScenarios } from "@/api/generated/scenarios/scenarios.ts";
import { useListSessionDirectives } from "@/api/generated/session/session.ts";
import type { WorkspaceView } from "@/api/generated/schemas";
import { TabLink } from "./TabLink";

// WorkspaceContextBar is the line under the workspace's name on every tab:
// the four pieces of live state that decide what the mock answers, which
// before A21 lived on four different tabs — every reader of the 2026-09-05
// UI review named this (U1). The bound spec BY NAME (the list and the
// settings panel said «спека #3», a database id); the active scenario, whose
// mask only «Endpoint'ы» warned about while edits on any other tab were
// silently shadowed; the number of session directives in force, which no
// screen showed at all; and «ревизия N» with the one-line gloss the guide
// gives it. Each item is its own query and degrades on its own: a failed
// specs or scenarios list falls back to the id, a failed directives list
// shows nothing — the bar is a footnote to the tabs, never a fifth state of
// the layout (which owns the workspace fetch's four states above it).
export function WorkspaceContextBar({ workspace }: { workspace: WorkspaceView }): ReactElement {
  const id = workspace.id;
  const specs = useListSpecs();
  const scenarios = useListScenarios(id, { query: { enabled: workspace.scenarioId !== null } });
  const directives = useListSessionDirectives(id);

  const spec =
    workspace.specId === null
      ? null
      : specs.data?.status === 200
        ? specs.data.data.find((s) => s.id === workspace.specId)
        : undefined;
  const scenario =
    workspace.scenarioId === null
      ? null
      : scenarios.data?.status === 200
        ? scenarios.data.data.scenarios.find((s) => s.id === workspace.scenarioId)
        : undefined;
  const directiveCount =
    directives.data?.status === 200 ? directives.data.data.directives.length : 0;

  return (
    <Group gap="sm" wrap="wrap" data-testid="workspace-context-bar">
      <Text size="sm" c="dimmed" data-testid="workspace-detail-meta">
        {workspace.slug} ·{" "}
        <Tooltip
          label="Растёт при каждой сохранённой правке воркспейса; директивы сессии её не двигают."
          withArrow
        >
          <span data-testid="workspace-revision">ревизия {workspace.revision}</span>
        </Tooltip>
      </Text>
      <Text size="sm" c="dimmed" data-testid="workspace-context-spec">
        {workspace.specId === null ? (
          <>
            спека не привязана —{" "}
            <TabLink id={id} tab="overview" testId="workspace-context-bind-spec">
              привязать
            </TabLink>
          </>
        ) : (
          <>
            спека:{" "}
            <TabLink id={id} tab="overview">
              {spec ? `${spec.name} (v${spec.version})` : `#${workspace.specId}`}
            </TabLink>
          </>
        )}
      </Text>
      {workspace.scenarioId !== null ? (
        <Badge color="orange" variant="light" size="sm" data-testid="workspace-context-scenario">
          <TabLink id={id} tab="scenarios">
            сценарий: {scenario ? scenario.name : `#${workspace.scenarioId}`}
          </TabLink>
        </Badge>
      ) : null}
      {directiveCount > 0 ? (
        <Badge color="yellow" variant="light" size="sm" data-testid="workspace-context-directives">
          <TabLink id={id} tab="operations">
            директив сессии: {directiveCount}
          </TabLink>
        </Badge>
      ) : null}
    </Group>
  );
}

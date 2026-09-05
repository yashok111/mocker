import type { ReactNode } from "react";
import { Anchor } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";

// TabLink is the one way a screen points at another tab of the SAME
// workspace in prose. Before A21 (the 2026-09-05 UI review, U3) seven
// «на вкладке X» phrases were plain text and exactly one was a link — the
// one in CustomEndpointsPage, whose comment records why it is an Anchor with
// an href for the browser plus a navigate() for the router rather than
// `component={Link}`: Mantine's polymorphic `component` prop defeats
// TanStack Router's typed `params` inference on this route tree (it
// collapses `params` to the reducer-function overload only). This component
// carries that pattern once so every screen writes `<TabLink id tab>`.
export type WorkspaceTab =
  | "overview"
  | "operations"
  | "endpoints"
  | "traffic"
  | "scenarios"
  | "history"
  | "resources"
  | "connections"
  | "assets"
  | "contract";

export function tabPath(id: number, tab: WorkspaceTab): string {
  return tab === "overview" ? `/workspaces/${id}` : `/workspaces/${id}/${tab}`;
}

export function TabLink({
  id,
  tab,
  children,
  testId,
}: {
  id: number;
  tab: WorkspaceTab;
  children: ReactNode;
  testId?: string;
}) {
  const navigate = useNavigate();
  return (
    <Anchor
      href={tabPath(id, tab)}
      data-testid={testId}
      onClick={(e) => {
        e.preventDefault();
        if (tab === "overview") {
          void navigate({ to: "/workspaces/$id", params: { id } });
        } else {
          void navigate({ to: `/workspaces/$id/${tab}`, params: { id } });
        }
      }}
    >
      {children}
    </Anchor>
  );
}

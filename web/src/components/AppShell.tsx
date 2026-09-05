import type { ReactNode } from "react";
import {
  AppShell as MantineAppShell,
  Button,
  Group,
  NativeSelect,
  Text,
  UnstyledButton,
} from "@mantine/core";
import { IconLogout } from "@tabler/icons-react";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useLogout } from "@/api/generated/auth/auth.ts";
import { useListWorkspaces } from "@/api/generated/workspaces/workspaces.ts";
import { useGetHealthz, useGetReadyz } from "@/api/generated/health/health.ts";
import { ApiFailure } from "@/api/client";
import { forgetSession } from "@/auth/session";
import type { UserView } from "@/api/generated/schemas";

// AppShell is the frame every authenticated screen renders inside: the name of
// the tool, who is logged in, and the way out. It owns no data of its own —
// the user comes from the route guard that already resolved the session.
export function AppShell({ user, children }: { user: UserView; children: ReactNode }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const logout = useLogout({
    mutation: {
      // Cache and token are dropped whether the server accepted the logout or
      // not: a failed logout still means this tab should stop acting logged
      // in, and the next guarded navigation will ask the server the truth.
      onSettled: () => {
        forgetSession(queryClient);
        queryClient.clear();
        void navigate({ to: "/login", replace: true });
      },
    },
  });

  return (
    <MantineAppShell header={{ height: 56 }} padding="md">
      <MantineAppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group gap="lg">
            <UnstyledButton onClick={() => void navigate({ to: "/" })}>
              <Text fw={600} size="sm">
                mocker
              </Text>
            </UnstyledButton>
            {/* A21 (U2): the list was reachable only through the wordmark,
                which nothing marked as a link. */}
            <UnstyledButton onClick={() => void navigate({ to: "/" })} data-testid="nav-workspaces">
              <Text size="sm">Воркспейсы</Text>
            </UnstyledButton>
            <WorkspaceSwitcher />
            {/* Without this link /specs is reachable only by typing the URL
                by hand — a route nobody can reach is not shipped. */}
            <UnstyledButton onClick={() => void navigate({ to: "/specs" })} data-testid="nav-specs">
              <Text size="sm">Спеки</Text>
            </UnstyledButton>
            {/* The operator's manual (docs/USER-GUIDE.md) rendered in place —
                the one screen added after the A4 "no new screens" rule, on
                the owner's own request, and it calls no admin route. */}
            <UnstyledButton onClick={() => void navigate({ to: "/guide" })} data-testid="nav-guide">
              <Text size="sm">Руководство</Text>
            </UnstyledButton>
          </Group>
          <Group gap="sm">
            <ServerStatus />
            <Text size="sm" c="dimmed" data-testid="current-user-name">
              {user.name}
            </Text>
            <Button
              variant="default"
              size="xs"
              leftSection={<IconLogout size={16} />}
              loading={logout.isPending}
              onClick={() => logout.mutate()}
              data-testid="logout-button"
            >
              {logout.isPending ? "Выходим…" : "Выйти"}
            </Button>
          </Group>
        </Group>
      </MantineAppShell.Header>
      <MantineAppShell.Main>{children}</MantineAppShell.Main>
    </MantineAppShell>
  );
}

// ServerStatus polls the two probes the container's own HEALTHCHECK reads
// (A20, 2026-09-05: the last two EXEMPT entries, on the owner's «добей
// последние 4 гэпа», a Russian string quoted as data). /healthz answers
// without touching the database and /readyz answers 503 while it is not
// open (internal/admin/server.go), so the pair distinguishes "the process
// is up but the database is not" — the one state a person at the admin UI
// cannot otherwise tell from a slow request — from "unreachable". One
// dimmed word in the header; never an alert, because every screen already
// reports its own failures with the sentence that matters there.
const STATUS_POLL_MS = 30_000;

function ServerStatus() {
  const ready = useGetReadyz({ query: { refetchInterval: STATUS_POLL_MS, retry: false } });
  const alive = useGetHealthz({ query: { refetchInterval: STATUS_POLL_MS, retry: false } });
  // Gated on !isError, not on data alone: TanStack Query keeps the LAST
  // good answer in `data` after a failed refetch, so a status read off
  // `data` would say «готов» for the whole poll interval after the server
  // went away. A 503 is the one error /readyz answers by design (the
  // database is not open, internal/admin/server.go); any other failure —
  // a reset, a 502 from a proxy, a timeout — is "unreachable", never
  // reported as a database problem it cannot be.
  const isReady = !ready.isError && ready.data?.status === 200 && ready.data.data.ok;
  const isAlive = !alive.isError && alive.data?.status === 200 && alive.data.data.ok;
  const dbNotReady =
    ready.isError && ready.error instanceof ApiFailure && ready.error.status === 503;
  const label = ready.isPending
    ? "сервер: …"
    : isReady
      ? "сервер: готов"
      : isAlive && dbNotReady
        ? "сервер: жив, база данных не готова"
        : "сервер: недоступен";
  return (
    <Text
      size="xs"
      c={isReady ? "dimmed" : "red"}
      component="output"
      title="GET /readyz и GET /healthz раз в 30 с"
      data-testid="server-status"
    >
      {label}
    </Text>
  );
}

// WorkspaceSwitcher is the header's way from one workspace to another (A21,
// U2): before it, switching meant the wordmark, the list, a row. Rendered
// only on a /workspaces/{id} route, and the list is fetched only then — the
// specs and guide screens have no workspace to switch from. A NativeSelect
// rather than Mantine's Select: one click, keyboard-navigable, and the same
// control every other screen in this tree uses for a pick.
// `pathname` is a test seam: renderInRouter mounts a component at "/" only,
// so the switcher's own test hands it the path a workspace route would have.
export function WorkspaceSwitcher({ pathname }: { pathname?: string } = {}) {
  const navigate = useNavigate();
  const location = useLocation();
  const path = pathname ?? location.pathname;
  const match = /^\/workspaces\/(\d+)(\/[a-z]+)?/.exec(path);
  const current = match ? match[1] : null;
  // The tab the person is on travels with the switch (a reader of A21):
  // switching from «Трафик» lands on the other workspace's «Трафик».
  const tab = match?.[2] ?? "";
  const workspaces = useListWorkspaces(undefined, { query: { enabled: current !== null } });
  if (current === null || workspaces.data?.status !== 200) {
    return null;
  }
  return (
    <NativeSelect
      size="xs"
      aria-label="Перейти к воркспейсу"
      value={current}
      onChange={(e) =>
        void navigate({
          to: `/workspaces/$id${tab}`,
          params: { id: Number(e.currentTarget.value) },
        })
      }
      data-testid="workspace-switcher"
    >
      {workspaces.data.data.map((ws) => (
        <option key={ws.id} value={String(ws.id)}>
          {ws.name}
        </option>
      ))}
      {workspaces.data.data.some((ws) => String(ws.id) === current) ? null : (
        <option value={current}>#{current}</option>
      )}
    </NativeSelect>
  );
}

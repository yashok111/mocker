import { createContext, useCallback, useContext, useState } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import {
  AppShell as MantineAppShell,
  ActionIcon,
  Avatar,
  Button,
  Drawer,
  Group,
  NativeSelect,
  Text,
  UnstyledButton,
} from "@mantine/core";
import {
  IconBook2,
  IconBraces,
  IconChevronRight,
  IconFileCode,
  IconLayoutGrid,
  IconLogout,
  IconMenu2,
  IconRoute,
} from "@tabler/icons-react";
import { useMediaQuery } from "@mantine/hooks";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useLogout } from "@/api/generated/auth/auth.ts";
import { useListWorkspaces } from "@/api/generated/workspaces/workspaces.ts";
import { useGetHealthz, useGetReadyz } from "@/api/generated/health/health.ts";
import { ApiFailure } from "@/api/client";
import { forgetSession } from "@/auth/session";
import type { UserView } from "@/api/generated/schemas";
import classes from "./Workbench.module.css";

const NavigationContext = createContext<{
  target: HTMLDivElement | null;
  close: () => void;
} | null>(null);

// The workspace owns its navigation and data. A portal places it in the same
// rail as global navigation without coupling the shell to workspace queries.
export function WorkspaceNavigationSlot({ children }: { children: ReactNode }) {
  const navigation = useContext(NavigationContext);
  if (navigation === null) {
    return <>{children}</>;
  }
  return navigation.target === null ? null : createPortal(children, navigation.target);
}

export function useCloseNavigation() {
  return useContext(NavigationContext)?.close;
}

// AppShell is the frame every authenticated screen renders inside: the name of
// the tool, who is logged in, and the way out. It owns no data of its own —
// the user comes from the route guard that already resolved the session.
export function AppShell({ user, children }: { user: UserView; children: ReactNode }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const location = useLocation();
  const mobile = useMediaQuery("(max-width: 48em)", false, { getInitialValueInEffect: false });
  const designer = /^\/designs\/[^/]+\/?$/.test(location.pathname);
  const navigationInDrawer = mobile || designer;
  const [navigationOpened, setNavigationOpened] = useState(false);
  const [navigationTarget, setNavigationTarget] = useState<HTMLDivElement | null>(null);
  const closeNavigation = useCallback(() => setNavigationOpened(false), []);

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

  const globalLinks = [
    { to: "/", label: "Воркспейсы", icon: IconLayoutGrid, testId: "nav-workspaces" },
    { to: "/designs", label: "Проектирование API", icon: IconRoute, testId: "nav-designs" },
    { to: "/specs", label: "Спеки", icon: IconFileCode, testId: "nav-specs" },
    { to: "/guide", label: "Руководство", icon: IconBook2, testId: "nav-guide" },
  ] as const;
  const navigation = (
    <nav aria-label="Основная навигация" className={classes.navigation}>
      <div className={classes.globalLinks}>
        <Text className={classes.navCaption}>Пространство</Text>
        {globalLinks.map(({ to, label, icon: Icon, testId }) => {
          const active =
            to === "/"
              ? location.pathname === "/" || location.pathname.startsWith("/workspaces/")
              : location.pathname.startsWith(to);
          return (
            <UnstyledButton
              key={to}
              className={classes.globalLink}
              data-active={active || undefined}
              aria-current={active ? "page" : undefined}
              data-testid={testId}
              onClick={() => {
                closeNavigation();
                void navigate({ to });
              }}
            >
              <Icon size={18} stroke={1.6} aria-hidden="true" />
              <span>{label}</span>
              {active ? <IconChevronRight size={14} aria-hidden="true" /> : null}
            </UnstyledButton>
          );
        })}
      </div>
      <WorkspaceSwitcher onNavigate={closeNavigation} />
      <div ref={setNavigationTarget} className={classes.workspaceNavigationSlot} />
      <div className={classes.railFooter}>
        <Text size="xs" c="dimmed">
          API под вашим контролем
        </Text>
        <Text size="xs" c="dimmed">
          Настраивайте. Проверяйте. Повторяйте.
        </Text>
      </div>
    </nav>
  );

  return (
    <NavigationContext.Provider value={{ target: navigationTarget, close: closeNavigation }}>
      <MantineAppShell
        header={{ height: 64 }}
        navbar={{
          width: 224,
          breakpoint: "sm",
          collapsed: { mobile: true, desktop: navigationInDrawer },
        }}
        padding={0}
        classNames={{ header: classes.header, navbar: classes.rail, main: classes.main }}
      >
        <MantineAppShell.Header>
          <Group h="100%" px={{ base: "md", sm: "lg" }} justify="space-between" wrap="nowrap">
            <Group gap="sm" wrap="nowrap">
              {navigationInDrawer ? (
                <ActionIcon
                  variant="subtle"
                  size="lg"
                  aria-label="Открыть навигацию"
                  aria-expanded={navigationOpened}
                  aria-controls="mobile-navigation"
                  onClick={() => setNavigationOpened(true)}
                >
                  <IconMenu2 size={22} />
                </ActionIcon>
              ) : null}
              <UnstyledButton className={classes.brand} onClick={() => void navigate({ to: "/" })}>
                <span className={classes.brandMark}>
                  <IconBraces size={24} stroke={2} />
                </span>
                <span>
                  mocker<span className={classes.brandDot}>.</span>
                </span>
              </UnstyledButton>
              <Text className={classes.headerCaption}>Ваше API. Ваши правила.</Text>
            </Group>
            <Group gap="md" wrap="nowrap">
              <div className={classes.headerStatus}>
                <ServerStatus />
              </div>
              <Group gap="xs" wrap="nowrap" className={classes.user}>
                <Avatar size={30} radius="xl" color="teal">
                  {user.name.slice(0, 1).toUpperCase()}
                </Avatar>
                <Text
                  size="sm"
                  fw={500}
                  data-testid="current-user-name"
                  className={classes.userName}
                >
                  {user.name}
                </Text>
              </Group>
              <Button
                variant="subtle"
                color="gray"
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
        {!navigationInDrawer ? <MantineAppShell.Navbar>{navigation}</MantineAppShell.Navbar> : null}
        <MantineAppShell.Main>
          <div className={classes.mainContent} data-designer={designer || undefined}>
            {children}
          </div>
        </MantineAppShell.Main>
      </MantineAppShell>
      {navigationInDrawer ? (
        <Drawer
          opened={navigationOpened}
          onClose={closeNavigation}
          keepMounted
          title="Навигация"
          size={292}
          position="left"
          padding="md"
          id="mobile-navigation"
          closeButtonProps={{ "aria-label": "Закрыть навигацию" }}
          classNames={{ body: classes.drawerBody }}
        >
          {navigation}
          {mobile ? <ServerStatus /> : null}
        </Drawer>
      ) : null}
    </NavigationContext.Provider>
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
      className={classes.serverStatus}
      data-ready={isReady || undefined}
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
export function WorkspaceSwitcher({
  pathname,
  onNavigate,
}: { pathname?: string; onNavigate?: () => void } = {}) {
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
      size="sm"
      className={classes.workspaceSwitcher}
      label="Текущий воркспейс"
      aria-label="Перейти к воркспейсу"
      value={current}
      onChange={(e) => {
        onNavigate?.();
        void navigate({
          to: `/workspaces/$id${tab}`,
          params: { id: Number(e.currentTarget.value) },
        });
      }}
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

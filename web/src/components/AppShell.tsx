import type { ReactNode } from "react";
import { AppShell as MantineAppShell, Button, Group, Text, UnstyledButton } from "@mantine/core";
import { IconLogout } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useLogout } from "@/api/generated/auth/auth.ts";
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

import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { AppShell } from "@/components/AppShell";
import { ensureSession } from "@/auth/session";

// _authed is a layout route: it carries no path of its own, only the guard and
// the frame. Every screen that needs a session nests under it, so the "is
// anyone logged in" question is asked in exactly one place instead of once per
// screen — and asked in beforeLoad, so a screen never renders for a frame
// before the answer arrives.
export const Route = createFileRoute("/_authed")({
  beforeLoad: async ({ context, location }) => {
    const session = await ensureSession(context.queryClient);
    if (!session) {
      throw redirect({ to: "/login", search: { redirect: location.href } });
    }
    // Returned into the route context, so every nested route reads the same
    // resolved session rather than each firing its own /api/me.
    return { session };
  },
  component: AuthedLayout,
});

function AuthedLayout() {
  const { session } = Route.useRouteContext();
  return (
    <AppShell user={session.user}>
      <Outlet />
    </AppShell>
  );
}

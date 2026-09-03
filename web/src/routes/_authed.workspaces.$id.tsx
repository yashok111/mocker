import { createFileRoute, Outlet } from "@tanstack/react-router";
import { WorkspaceLayout } from "@/components/WorkspaceLayout";
import { NotFoundFallback } from "@/components/ErrorFallback";

// The layout route for every /workspaces/$id/* screen. It owns the {id}
// guard ONCE — every child route's Route.useParams() inherits the already-
// parsed number from this match (TanStack Router accumulates parsed params up
// the match chain), so none of the four children below re-declare parseParams.
export const Route = createFileRoute("/_authed/workspaces/$id")({
  // A non-numeric {id} is a wrong URL, not a workspace that happens to be
  // missing: the admin plane would answer 400 for it, and rendering the
  // detail screen just to show that is a wasted round trip.
  parseParams: ({ id }) => ({ id: Number(id) }),
  component: WorkspaceLayoutRoute,
});

function WorkspaceLayoutRoute() {
  const { id } = Route.useParams();

  if (!Number.isInteger(id) || id <= 0) {
    return <NotFoundFallback />;
  }

  return (
    <WorkspaceLayout id={id}>
      <Outlet />
    </WorkspaceLayout>
  );
}

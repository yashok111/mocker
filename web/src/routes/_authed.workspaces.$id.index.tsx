import { createFileRoute } from "@tanstack/react-router";
import { WorkspaceOverview } from "@/components/WorkspaceOverview";

export const Route = createFileRoute("/_authed/workspaces/$id/")({
  component: WorkspaceOverviewRoute,
});

function WorkspaceOverviewRoute() {
  const { id } = Route.useParams();
  const { session } = Route.useRouteContext();
  return <WorkspaceOverview id={id} config={session.config} />;
}

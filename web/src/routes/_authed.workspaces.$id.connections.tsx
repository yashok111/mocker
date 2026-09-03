import { createFileRoute } from "@tanstack/react-router";
import { StreamConnectionsPage } from "@/components/StreamConnectionsPage";

export const Route = createFileRoute("/_authed/workspaces/$id/connections")({
  component: ConnectionsRoute,
});

function ConnectionsRoute() {
  const { id } = Route.useParams();
  return <StreamConnectionsPage id={id} />;
}

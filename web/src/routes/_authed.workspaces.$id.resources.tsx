import { createFileRoute } from "@tanstack/react-router";
import { ResourcesPage } from "@/components/ResourcesPage";

export const Route = createFileRoute("/_authed/workspaces/$id/resources")({
  component: ResourcesRoute,
});

function ResourcesRoute() {
  const { id } = Route.useParams();
  return <ResourcesPage id={id} />;
}

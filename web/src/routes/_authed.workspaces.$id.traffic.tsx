import { createFileRoute } from "@tanstack/react-router";
import { TrafficPage } from "@/components/TrafficPage";

export const Route = createFileRoute("/_authed/workspaces/$id/traffic")({
  component: TrafficRoute,
});

function TrafficRoute() {
  const { id } = Route.useParams();
  return <TrafficPage id={id} />;
}

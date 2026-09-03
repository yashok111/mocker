import { createFileRoute } from "@tanstack/react-router";
import { ScenariosPage } from "@/components/ScenariosPage";

export const Route = createFileRoute("/_authed/workspaces/$id/scenarios")({
  component: ScenariosRoute,
});

function ScenariosRoute() {
  const { id } = Route.useParams();
  return <ScenariosPage id={id} />;
}

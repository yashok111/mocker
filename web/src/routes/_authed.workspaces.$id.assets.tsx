import { createFileRoute } from "@tanstack/react-router";
import { AssetsPage } from "@/components/AssetsPage";

export const Route = createFileRoute("/_authed/workspaces/$id/assets")({
  component: AssetsRoute,
});

function AssetsRoute() {
  const { id } = Route.useParams();
  return <AssetsPage id={id} />;
}

import { createFileRoute } from "@tanstack/react-router";
import { HistoryPage } from "@/components/HistoryPage";

export const Route = createFileRoute("/_authed/workspaces/$id/history")({
  component: HistoryRoute,
});

function HistoryRoute() {
  const { id } = Route.useParams();
  return <HistoryPage id={id} />;
}

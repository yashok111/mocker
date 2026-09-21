import { createFileRoute } from "@tanstack/react-router";
import { NotFoundFallback } from "@/components/ErrorFallback";
import { ServerDesignCanvasPage } from "@/components/design-canvas/ServerDesignCanvasPage";

export const Route = createFileRoute("/_authed/design-scenarios/$id")({
  parseParams: ({ id }) => ({ id: Number(id) }),
  component: DesignScenarioRoute,
});

function DesignScenarioRoute() {
  const { id } = Route.useParams();
  if (!Number.isInteger(id) || id <= 0) return <NotFoundFallback />;
  return <ServerDesignCanvasPage id={id} />;
}

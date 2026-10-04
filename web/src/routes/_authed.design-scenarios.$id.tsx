import { type } from "arktype";
import { createFileRoute } from "@tanstack/react-router";
import { NotFoundFallback } from "@/components/ErrorFallback";
import { ServerDesignCanvasPage } from "@/components/design-canvas/ServerDesignCanvasPage";

export const Route = createFileRoute("/_authed/design-scenarios/$id")({
  validateSearch: type({
    "pinnedRevisionId?": "string",
    "pinnedHash?": "string",
    "returnProjectId?": "string",
    "returnRevisionId?": "string",
    "returnChangeProposalId?": "string",
    "returnProposalRevisionId?": "string",
    "projectionView?": "string",
    "embeddedContractId?": "string",
  }).assert,
  parseParams: ({ id }) => ({ id: Number(id) }),
  component: DesignScenarioRoute,
});

function DesignScenarioRoute() {
  const { id } = Route.useParams();
  const pin = Route.useSearch();
  if (!Number.isSafeInteger(id) || id <= 0) return <NotFoundFallback />;
  return <ServerDesignCanvasPage id={id} pin={pin.pinnedRevisionId ? pin : undefined} />;
}

import { createFileRoute } from "@tanstack/react-router";
import { type } from "arktype";
import { CustomEndpointsPage } from "@/components/CustomEndpointsPage";

// P7b: the «Контракт» tab links an «добавлено»/«изменено»/«удалено» custom
// row here with its id in the search, so the row's editor opens.
const endpointsSearch = type({
  // string | number: our own navigate stringifies, but TanStack's default
  // parseSearch JSON-parses a pasted `?endpointId=12` into a number, and a
  // string-only schema would throw the route into its error fallback.
  "endpointId?": "string | number",
});

export const Route = createFileRoute("/_authed/workspaces/$id/endpoints")({
  validateSearch: endpointsSearch.assert,
  component: EndpointsRoute,
});

function EndpointsRoute() {
  const { id } = Route.useParams();
  const { session } = Route.useRouteContext();
  const { endpointId } = Route.useSearch();
  const parsed = endpointId === undefined ? undefined : Number(endpointId);
  return (
    <CustomEndpointsPage
      id={id}
      config={session.config}
      initialEditingId={parsed !== undefined && Number.isFinite(parsed) ? parsed : undefined}
    />
  );
}

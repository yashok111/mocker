import { createFileRoute } from "@tanstack/react-router";
import { type } from "arktype";
import { OperationsPage } from "@/components/OperationsPage";

// P7b: the «Контракт» tab links an «изменено»/«удалено» spec operation here
// with its opKey in the search, so the editor opens on that operation.
const operationsSearch = type({
  "opKey?": "string",
});

export const Route = createFileRoute("/_authed/workspaces/$id/operations")({
  validateSearch: operationsSearch.assert,
  component: OperationsRoute,
});

function OperationsRoute() {
  const { id } = Route.useParams();
  const { opKey } = Route.useSearch();
  return <OperationsPage id={id} initialOpKey={opKey} />;
}

import { createFileRoute } from "@tanstack/react-router";
import { type } from "arktype";
import { OperationsPage } from "@/components/OperationsPage";

// P7b: the «Контракт» tab links an «изменено»/«удалено» spec operation here
// with its opKey in the search, so the editor opens on that operation.
// A21 (U4): the traffic screen links here with the row's matchedId — the
// spec operation's own id (internal/traffic: "operations.id"), not an
// opKey, which a traffic row does not carry. OperationsPage resolves it
// against the spec operations it already loads.
const operationsSearch = type({
  "opKey?": "string",
  // string | number: our own navigate stringifies, but TanStack's default
  // parseSearch JSON-parses a pasted `?opId=12` into a number, and a
  // string-only schema would throw the route into its error fallback.
  "opId?": "string | number",
});

export const Route = createFileRoute("/_authed/workspaces/$id/operations")({
  validateSearch: operationsSearch.assert,
  component: OperationsRoute,
});

function OperationsRoute() {
  const { id } = Route.useParams();
  const { opKey, opId } = Route.useSearch();
  const parsedOpId = opId === undefined ? undefined : Number(opId);
  return (
    <OperationsPage
      id={id}
      initialOpKey={opKey}
      initialOpId={
        parsedOpId !== undefined && Number.isInteger(parsedOpId) ? parsedOpId : undefined
      }
    />
  );
}

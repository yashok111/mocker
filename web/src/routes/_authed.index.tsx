import { createFileRoute } from "@tanstack/react-router";
import { type } from "arktype";
import { WorkspacesPage } from "@/components/WorkspacesPage";

// A21 (G3): the specs screen links here with the spec it just imported, so
// the create card opens with that spec preselected — the first-run trail
// used to end at «Спека импортирована» with nothing to click next.
const indexSearch = type({
  "specId?": "string",
});

export const Route = createFileRoute("/_authed/")({
  validateSearch: indexSearch.assert,
  component: IndexRoute,
});

function IndexRoute() {
  const { specId } = Route.useSearch();
  const parsed = specId === undefined ? undefined : Number(specId);
  return (
    <WorkspacesPage
      initialSpecId={parsed !== undefined && Number.isInteger(parsed) ? parsed : undefined}
    />
  );
}

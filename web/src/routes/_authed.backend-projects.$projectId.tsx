import { createFileRoute } from "@tanstack/react-router";
import { BackendProjectPage } from "@/components/backend-workbench/BackendProjectPage";
import { parseBackendSourcePin } from "@/components/backend-workbench/backendFlowReads";

export const Route = createFileRoute("/_authed/backend-projects/$projectId")({
  component: Page,
  validateSearch: parseBackendSourcePin,
});

function Page() {
  const { projectId } = Route.useParams();
  const pin = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <BackendProjectPage
      projectId={projectId}
      sourcePin={pin}
      onSourceNavigate={(search, replace) => void navigate({ search, replace: replace ?? false })}
    />
  );
}

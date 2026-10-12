import { createFileRoute } from "@tanstack/react-router";
import { requireBackendWorkbench } from "@/components/backend-workbench/backendWorkbenchGate";
import { BackendProjectPage } from "@/components/backend-workbench/BackendProjectPage";
import { parseBackendWorkspaceSearch } from "@/components/backend-workbench/backendWorkspaceSearch";

export const Route = createFileRoute("/_authed/backend-projects/$projectId")({
  beforeLoad: ({ context }) => requireBackendWorkbench(context.session),
  component: Page,
  validateSearch: parseBackendWorkspaceSearch,
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

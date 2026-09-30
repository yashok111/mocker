import { createFileRoute } from "@tanstack/react-router";
import { BackendProjectPage } from "@/components/backend-workbench/BackendProjectPage";

export const Route = createFileRoute("/_authed/backend-projects/$projectId")({ component: Page });

function Page() {
  const { projectId } = Route.useParams();
  return <BackendProjectPage projectId={projectId} />;
}

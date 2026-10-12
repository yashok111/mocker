import { createFileRoute } from "@tanstack/react-router";
import { requireBackendWorkbench } from "@/components/backend-workbench/backendWorkbenchGate";
import { BackendProjectsPage } from "@/components/backend-workbench/BackendProjectsPage";

export const Route = createFileRoute("/_authed/backend-projects/")({
  beforeLoad: ({ context }) => requireBackendWorkbench(context.session),
  component: BackendProjectsPage,
});

import { createFileRoute } from "@tanstack/react-router";
import { BackendProjectsPage } from "@/components/backend-workbench/BackendProjectsPage";

export const Route = createFileRoute("/_authed/backend-projects/")({
  component: BackendProjectsPage,
});

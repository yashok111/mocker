import { createFileRoute } from "@tanstack/react-router";
import { WorkspacesPage } from "@/components/WorkspacesPage";

export const Route = createFileRoute("/_authed/")({
  component: WorkspacesPage,
});

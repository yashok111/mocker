import { createFileRoute } from "@tanstack/react-router";
import { DesignsPage } from "@/components/api-designer/DesignsPage";

export const Route = createFileRoute("/_authed/designs/")({
  component: DesignsPage,
});

import { createFileRoute } from "@tanstack/react-router";
import { SpecsPage } from "@/components/SpecsPage";

export const Route = createFileRoute("/_authed/specs")({
  component: SpecsPage,
});

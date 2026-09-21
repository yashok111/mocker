import { createFileRoute } from "@tanstack/react-router";
import { DesignScenariosPage } from "@/components/design-canvas/DesignScenariosPage";

export const Route = createFileRoute("/_authed/design-scenarios/")({
  component: DesignScenariosPage,
});

import { createFileRoute } from "@tanstack/react-router";
import { DesignCanvasPage } from "@/components/design-canvas/DesignCanvasPage";

export const Route = createFileRoute("/_authed/design-canvas")({
  component: DesignCanvasPage,
});

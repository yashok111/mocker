import { createFileRoute } from "@tanstack/react-router";
import { GuidePage } from "@/components/GuidePage";

export const Route = createFileRoute("/_authed/guide")({
  component: Page,
});

function Page() {
  const { session } = Route.useRouteContext();
  return <GuidePage backendWorkbench={session.config.backendWorkbench} />;
}

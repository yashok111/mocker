import { redirect } from "@tanstack/react-router";
import type { AuthResponse } from "@/api/generated/schemas";

// The Backend Workbench is frozen on the server unless MOCKER_BACKEND_WORKBENCH=1
// (2026-10-12): every /api/backend-projects route answers 404 there, so its
// screens would only render failures. A frozen session that still reaches one
// (an old bookmark, a pasted link) is sent to the workspace list instead.
export function requireBackendWorkbench(session: AuthResponse): void {
  if (!session.config.backendWorkbench) {
    throw redirect({ to: "/", replace: true });
  }
}

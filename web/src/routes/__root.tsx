import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { NotFoundFallback, RouteErrorFallback } from "@/components/ErrorFallback";

// The router carries the query client in its context so a route's beforeLoad
// can resolve the session through the SAME cache the components read from —
// that is what makes the auth guard free on a navigation that already has a
// warm /api/me, and what keeps a screen from ever rendering before the answer
// is known.
export interface RouterContext {
  queryClient: QueryClient;
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  errorComponent: RouteErrorFallback,
  notFoundComponent: NotFoundFallback,
});

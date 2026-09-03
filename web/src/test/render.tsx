import type { ReactNode } from "react";
import { render, type RenderResult } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { theme } from "@/theme/mantine";

// renderWithProviders wraps a component in the same providers main.tsx does.
// Every Mantine component reads its theme through context and throws without
// MantineProvider, so a test that renders a screen directly with Testing
// Library's bare render() fails on the provider rather than on the thing it
// meant to assert.
export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // No retries in tests: a hook under test that is SUPPOSED to fail
        // would otherwise take three round trips and a backoff to say so,
        // and the assertion would time out before the error ever surfaced.
        retry: false,
        gcTime: 0,
        staleTime: 0,
      },
      mutations: { retry: false },
    },
  });
}

export function renderWithProviders(
  ui: ReactNode,
  { queryClient = makeQueryClient() }: { queryClient?: QueryClient } = {},
): RenderResult & { queryClient: QueryClient } {
  const result = render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider theme={theme} defaultColorScheme="light">
        <ModalsProvider>{ui}</ModalsProvider>
      </MantineProvider>
    </QueryClientProvider>,
  );
  return Object.assign(result, { queryClient });
}

/**
 * renderInRouter mounts `ui` as the component of a single memory route. Screens
 * that call useNavigate or <Link> need a router in context; building a
 * throwaway one per test keeps that need local instead of forcing every test
 * to import the app's real route tree (which would drag in its auth guard and
 * turn a component test into an integration test).
 */
export function renderInRouter(
  ui: ReactNode,
  {
    queryClient = makeQueryClient(),
    path = "/",
  }: { queryClient?: QueryClient; path?: string } = {},
): RenderResult & { queryClient: QueryClient } {
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => <>{ui}</>,
  });
  // A catch-all sibling so a navigate() a screen performs under test resolves
  // to something instead of throwing "no route matched" and failing the test
  // for a reason unrelated to what it asserts.
  const catchAllRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "$",
    component: () => <div data-testid="test-router-elsewhere" />,
  });

  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, catchAllRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });

  const result = render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider theme={theme} defaultColorScheme="light">
        <ModalsProvider>
          {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
          <RouterProvider router={router as never} />
        </ModalsProvider>
      </MantineProvider>
    </QueryClientProvider>,
  );
  return Object.assign(result, { queryClient });
}

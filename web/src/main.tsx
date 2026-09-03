import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { MantineProvider } from "@mantine/core";
import { DatesProvider } from "@mantine/dates";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { RouterProvider, createRouter } from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import dayjs from "dayjs";
import "dayjs/locale/ru";

import "@mantine/core/styles.css";
import "@mantine/dates/styles.css";
import "@mantine/notifications/styles.css";
import "@mantine/dropzone/styles.css";
import "./styles/global.css";

dayjs.locale("ru");

import { theme } from "./theme/mantine";
import { routeTree } from "./routeTree.gen";
import { RouteErrorFallback, NotFoundFallback } from "./components/ErrorFallback";
import { setUnauthorizedHandler } from "./api/client";
import { forgetSession } from "./auth/session";
import { initSentry } from "./observability/sentry";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // This is a tool left open on a second monitor, not a tab a person tabs
      // back into expecting fresh data — refetching every window focus would
      // poll the admin plane on every alt-tab for no reason anyone asked for.
      refetchOnWindowFocus: false,
      // One retry: enough to ride out a dropped connection without making an
      // already-failed request (a 401, a 404) take three round trips to report.
      retry: 1,
      staleTime: 30_000,
      gcTime: 5 * 60_000,
    },
  },
});

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
  defaultPreloadStaleTime: 0,
  defaultErrorComponent: RouteErrorFallback,
  defaultNotFoundComponent: NotFoundFallback,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

// Error + performance monitoring. A no-op unless VITE_SENTRY_DSN is set (see
// observability/sentry.ts), which is the normal state for a closed contour.
// Must run after the router exists so navigation spans are named by route.
initSentry(router);

// A 401 on any call other than the auth flow itself means the session died
// mid-tab (expired, or logged out from another tab): drop every cache and
// bounce to the login screen. Registered here because client.ts can reach
// neither the router nor the query client on its own.
setUnauthorizedHandler(() => {
  forgetSession(queryClient);
  queryClient.clear();
  void router.navigate({ to: "/login", replace: true });
});

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("index.html is missing the #root mount element");
}

createRoot(rootElement).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <MantineProvider theme={theme} defaultColorScheme="light">
        <DatesProvider settings={{ locale: "ru", firstDayOfWeek: 1, weekendDays: [0, 6] }}>
          <ModalsProvider>
            <Notifications position="top-right" zIndex={2000} />
            <RouterProvider router={router} />
            {import.meta.env.DEV && (
              <>
                <ReactQueryDevtools buttonPosition="bottom-left" initialIsOpen={false} />
                <TanStackRouterDevtools router={router} position="bottom-right" />
              </>
            )}
          </ModalsProvider>
        </DatesProvider>
      </MantineProvider>
    </QueryClientProvider>
  </StrictMode>,
);

import * as Sentry from "@sentry/react";
import type { AnyRouter } from "@tanstack/react-router";

// initSentry is a NO-OP unless VITE_SENTRY_DSN is set at build time, and that
// is the normal state for this project: mocker ships into closed contours as a
// single `docker save`/`docker load` image, where an outbound connection to a
// SaaS error collector is at best useless and at worst a policy violation. The
// integration exists so a deployment that DOES have a collector can turn it on
// with one build-time variable, not because anything here phones home by
// default.
//
// Called from main.tsx after the router exists, so navigation spans are named
// by route rather than by raw URL.
export function initSentry(router: AnyRouter): void {
  const dsn = import.meta.env.VITE_SENTRY_DSN;
  if (!dsn) {
    return;
  }

  Sentry.init({
    dsn,
    environment: import.meta.env.VITE_SENTRY_ENVIRONMENT ?? "production",
    integrations: [Sentry.tanstackRouterBrowserTracingIntegration(router)],
    // Traces are sampled at 10%: this is an operator's tool with a handful of
    // simultaneous users, so a full trace stream buys nothing that a tenth of
    // it does not already show.
    tracesSampleRate: 0.1,
    // Bodies recorded by the traffic screen can contain whatever a developer
    // pointed at their mock — including tokens the redactor did not know to
    // strip. Nothing resembling request/response payloads may leave the
    // contour, so PII capture stays off regardless of the collector's own
    // settings.
    sendDefaultPii: false,
  });
}

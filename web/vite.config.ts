/// <reference types="vitest/config" />
import { defineConfig, loadEnv, searchForWorkspaceRoot, type ProxyOptions } from "vite";
import react from "@vitejs/plugin-react";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import { sentryVitePlugin } from "@sentry/vite-plugin";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

// Source-map upload runs ONLY when SENTRY_AUTH_TOKEN is present (CI/deploy). A
// plain `yarn build` without it is unaffected: no upload, no sourcemaps emitted.
const sentryAuthToken = process.env.SENTRY_AUTH_TOKEN;
const uploadSentrySourcemaps = Boolean(sentryAuthToken);

export default defineConfig(({ mode }) => {
  // VITE_ADMIN_HOST names the admin-plane vhost the Go binary answers to
  // (MOCKER_ADMIN_HOST on the server). loadEnv reads it from process env / a
  // .env file.
  const env = loadEnv(mode, ".", "VITE_");
  const adminHost = env.VITE_ADMIN_HOST || "mocker.local";
  const adminOrigin = `http://${adminHost}`;

  // Both the Go dispatcher and the admin plane's CSRF check key off headers
  // Vite's dev server doesn't set correctly on its own: the dispatcher routes
  // on Host before the admin plane is even reached (an un-rewritten
  // 127.0.0.1:8080 gets the unknown-host 404 first), and enforceCSRF compares
  // Origin's hostname to MOCKER_ADMIN_HOST on every POST (an un-rewritten
  // http://localhost:5173 is a 403). `changeOrigin` only fixes Host, so both
  // headers are set by hand here.
  const toAdmin = (): ProxyOptions => ({
    // The Go binary always listens on 127.0.0.1:8080 in dev — mocker.local
    // is never a real DNS name, only the Host/Origin value its dispatcher
    // and CSRF check expect. Proxying to adminOrigin instead of this literal
    // would try to resolve mocker.local and fail before a header rewrite
    // ever runs.
    target: "http://127.0.0.1:8080",
    configure(proxy) {
      proxy.on("proxyReq", (proxyReq) => {
        proxyReq.setHeader("host", adminHost);
        proxyReq.setHeader("origin", adminOrigin);
      });
    },
  });

  return {
    plugins: [
      tanstackRouter({
        target: "react",
        autoCodeSplitting: true,
        routesDirectory: "./src/routes",
        generatedRouteTree: "./src/routeTree.gen.ts",
      }),
      react(),
      // Must come last. Uploads sourcemaps to Sentry, then deletes the emitted
      // .map files (filesToDeleteAfterUpload) so they are never served
      // publicly — this app is embedded in the binary, so "served publicly"
      // means "shipped inside every image". Skipped entirely without a token.
      ...(uploadSentrySourcemaps
        ? [
            sentryVitePlugin({
              authToken: sentryAuthToken,
              org: process.env.SENTRY_ORG,
              project: process.env.SENTRY_PROJECT,
              sourcemaps: { filesToDeleteAfterUpload: ["../internal/webui/dist/**/*.map"] },
            }),
          ]
        : []),
    ],
    resolve: {
      alias: {
        "@": path.resolve(here, "./src"),
      },
    },
    build: {
      // go:embed can only reach files inside its own package directory, so
      // the build has to land under internal/webui, not web/dist.
      outDir: "../internal/webui/dist",
      // A stale asset from a previous build must never ship silently; the
      // Makefile's `ui` target re-creates the .gitkeep this deletes.
      emptyOutDir: true,
      // Emit sourcemaps only when we are going to upload+delete them (above).
      sourcemap: uploadSentrySourcemaps ? "hidden" : false,
      modulePreload: {
        // The admin plane serves this app under a CSP with script-src 'self'.
        // The stock modulepreload polyfill is an inline <script>, which that
        // policy refuses outright — the failure mode is a blank page with
        // only a console error, so it's off rather than re-debugged per deploy.
        polyfill: false,
      },
      // Same CSP reasoning: any asset small enough to inline becomes a
      // data: URI inside the emitted JS/CSS, which default-src 'self' also
      // blocks. Forcing every asset to a real file keeps the CSP boring.
      assetsInlineLimit: 0,
    },
    server: {
      // docs/USER-GUIDE.md sits one level above this package and is imported
      // `?raw` by GuidePage.tsx. The build bundles it regardless; the dev
      // server refuses to SERVE a file outside its allow list, so the docs
      // directory is added beside the package's own root.
      fs: {
        allow: [searchForWorkspaceRoot(here), path.resolve(here, "../docs")],
      },
      proxy: {
        "/api": toAdmin(),
        "/healthz": toAdmin(),
        "/readyz": toAdmin(),
      },
    },
    test: {
      // happy-dom since vitest 5 (2026-09-05; jsdom before). Unlike jsdom it
      // actually NAVIGATES on an anchor click and loads what a page links —
      // a `download` anchor or a `<Link>` would reach for the network from a
      // test. Every such door is shut here; a test that needs the network
      // stubs fetch (src/test/http.ts), never the other way round.
      environment: "happy-dom",
      environmentOptions: {
        happyDOM: {
          settings: {
            navigation: {
              disableMainFrameNavigation: true,
              disableChildFrameNavigation: true,
            },
            disableJavaScriptFileLoading: true,
            disableCSSFileLoading: true,
          },
        },
      },
      globals: true,
      setupFiles: ["./src/test/setup.ts"],
      // Scoped to src/ rather than left at the default (**/*.{test,spec}.*):
      // the default walks the whole package, so anything test-shaped that ever
      // lands outside src/ — a fixture, a script, another runner's spec — gets
      // collected by vitest and fails on an import it was never meant to see.
      include: ["src/**/*.{test,spec}.{ts,tsx}"],
      // Vitest's default is 5 s, which would fire BEFORE the 5 s
      // asyncUtilTimeout configured in setup.ts and swallow Testing Library's
      // far more useful failure message. Kept well above it so the RTL error
      // wins the race; this is a ceiling for a hung test, not a budget any
      // healthy test spends.
      testTimeout: 20_000,
    },
  };
});

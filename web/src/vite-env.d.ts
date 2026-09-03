/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Sentry DSN. Empty or absent (the normal case for a closed contour) leaves error reporting off entirely — see observability/sentry.ts. */
  readonly VITE_SENTRY_DSN?: string;
  /** Deployment name reported alongside events. Only read when a DSN is set. */
  readonly VITE_SENTRY_ENVIRONMENT?: string;
  /** Admin-plane vhost the Vite dev server rewrites Host/Origin to. Build-time only; see vite.config.ts. */
  readonly VITE_ADMIN_HOST?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

// client.ts is the ONLY file in this app that calls fetch. Every generated
// endpoint in src/api/generated is emitted with this function as its mutator
// (orval.config.ts), so the CSRF header, the credentials mode and the
// error-parsing rules exist in exactly one place — a screen that reaches for
// fetch directly is how a header silently goes missing on one request.

// csrfToken lives at module scope, not as a call argument: the caller of a
// generated endpoint has no reason to thread the token through every call
// site by hand, and auth/session.tsx is the only file that ever calls
// setCsrfToken — once per login/logout, not once per request.
let csrfToken: string | null = null;

export function setCsrfToken(token: string | null): void {
  csrfToken = token;
}

// onUnauthorized is registered by main.tsx: a 401 on any call other than the
// session probe itself means the session died mid-use, and the app has to
// drop its caches and go back to the login screen. This module cannot reach
// the router or the query client on its own, hence the hook.
let onUnauthorized: (() => void) | null = null;

export function setUnauthorizedHandler(handler: (() => void) | null): void {
  onUnauthorized = handler;
}

// notifyUnauthorized is the one door OUTSIDE customFetch into that handler:
// the traffic screen's stream probe (api/stream.ts, P6a D19) is a raw fetch
// by decision — the orval mutator would await a stream's text() forever — so
// when THAT fetch sees a 401 it has to reach the same "session died mid-use"
// path every other call reaches through customFetch, not invent a second
// one.
export function notifyUnauthorized(): void {
  onUnauthorized?.();
}

// ApiFailure is the one error type this client ever throws. status and code
// let a caller branch (a 401 vs a 429) without re-parsing a response body it
// may not even have gotten a JSON copy of. `details` (A3) carries whatever
// structured payload the server's error envelope put beside code/message —
// for edit_conflict that is the current document or a gone-tombstone (D6) or
// the preset's staleVersions map (D12) — untyped here because its shape is
// per-route (OverrideConflictDetails vs WorkspaceConflictDetails vs
// PresetConflictDetails …); a screen narrows it against the specific
// generated schema for the mutation it just issued.
export class ApiFailure extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: unknown;

  constructor(message: string, status: number, code: string, details?: unknown) {
    super(message);
    this.name = "ApiFailure";
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

// unknownErrorCode backs an ApiFailure built from a response this client
// could not parse as { error: { code, message } } — a reverse proxy's HTML
// 502, or the plain net/http 404 an unrouted /api/ path produces (the admin
// plane's own webui wrapper deliberately does not dress those up as JSON).
// It is not a server-issued code, so it is spelled distinctly from the set
// api/openapi.json enumerates.
const unknownErrorCode = "client_unknown_error";

// details is untyped and optional here for the same reason ApiFailure's own
// field is: the boundary that parses this envelope has no route context to
// pick a per-route schema against, so it passes whatever JSON value sat
// beside code/message through unexamined (A3/property 7 — this is the one
// place that used to drop it silently).
type ErrorEnvelope = { error: { code: string; message: string; details?: unknown } };

// isErrorEnvelope narrows `unknown` at the one boundary that needs it: JSON
// parsed from a response body this client did not write and cannot trust to
// match the contract's Error schema just because the status was non-2xx.
function isErrorEnvelope(value: unknown): value is ErrorEnvelope {
  if (typeof value !== "object" || value === null || !("error" in value)) {
    return false;
  }
  const err = (value as { error: unknown }).error;
  if (typeof err !== "object" || err === null) {
    return false;
  }
  const candidate = err as { code?: unknown; message?: unknown };
  return typeof candidate.code === "string" && typeof candidate.message === "string";
}

// parseJSON returns undefined instead of throwing on empty or non-JSON text,
// so customFetch can treat "not parseable" and "not the shape we expect" the
// same way: fall back to a synthesized message.
function parseJSON(text: string): unknown {
  if (text === "") {
    return undefined;
  }
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

// authFlowPaths are the routes whose 401 is NOT a dead session and must not
// trigger the global bounce: /api/me is how the app asks "is anyone logged
// in?" on every load (the anonymous answer IS a 401 by design, and bouncing
// on it loops before the login screen ever renders), and login/logout show
// their own error in place. Matched on a boundary — exact path or path+query
// — so a future sibling like /api/metrics can never be mistaken for the probe.
const authFlowPaths = ["/api/auth/login", "/api/auth/logout", "/api/me"];

// customFetch is the mutator every generated endpoint calls. orval hands it a
// fully-built URL and RequestInit; everything below is what the admin plane
// requires on top of that and what a generated client cannot know by itself.
//
// It returns orval's response ENVELOPE ({ status, data, headers }), not the
// bare body: with httpClient "fetch", the generated hooks read
// `q.data.status === 200 ? q.data.data : …`, so a mutator that returned the
// body alone would leave every hook's `.status` undefined and every screen
// rendering empty despite 200s on the wire.
export const customFetch = async <T>(url: string, init: RequestInit = {}): Promise<T> => {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");

  if (method !== "GET" && method !== "HEAD") {
    // Set unconditionally, including on a body-less DELETE: the admin plane
    // answers 415 without it as one leg of its CSRF defence, not merely
    // because there happens to be a JSON body to describe. The one exception
    // (A10) is a body that IS a file — PUT .../assets/{name} takes the raw
    // bytes and stores the header's media type as the asset's own, so a
    // JPEG sent as application/json would be served as JSON. A Blob body
    // keeps its type; orval's own "*/*" placeholder is overridden the same
    // way, and the CSRF chain admits any parseable non-executable type on
    // that route (rawBodyRoute, internal/admin).
    if (init.body instanceof Blob) {
      headers.set("Content-Type", init.body.type || "application/octet-stream");
    } else {
      headers.set("Content-Type", "application/json");
    }
    if (csrfToken !== null) {
      headers.set("X-CSRF-Token", csrfToken);
    }
  }

  const res = await fetch(url, {
    ...init,
    // same-origin, not include: the SPA is served by the very binary it talks
    // to, so the session cookie is first-party and nothing cross-origin
    // should ever carry it.
    credentials: "same-origin",
    headers,
  });

  // 204 (DELETE, logout) has no body at all; checking the status directly
  // says what is actually true instead of inferring it from an empty string.
  const text = res.status === 204 ? "" : await res.text();
  const parsed = parseJSON(text);

  if (!res.ok) {
    const isAuthFlow = authFlowPaths.some((p) => url === p || url.startsWith(`${p}?`));
    if (res.status === 401 && !isAuthFlow) {
      onUnauthorized?.();
    }
    if (isErrorEnvelope(parsed)) {
      throw new ApiFailure(
        parsed.error.message,
        res.status,
        parsed.error.code,
        parsed.error.details,
      );
    }
    throw new ApiFailure(`request failed with status ${res.status}`, res.status, unknownErrorCode);
  }

  return { status: res.status, data: parsed, headers: res.headers } as T;
};

export default customFetch;

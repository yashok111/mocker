import { vi } from "vitest";

// http.ts lifts the fetch-stubbing helpers every screen test in this app was
// independently re-typing (WorkspacesPage.test.tsx, the deleted
// WorkspacePage.test.tsx) into one place. Existing test files are NOT
// rewritten to use this — that duplication is not this phase's to remove —
// but every test written from here on imports it instead of pasting a third
// copy.

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit];

/** json() builds a Response the way the admin plane's own envelope looks on the wire. */
export function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/**
 * route() dispatches on "METHOD /path" so a test can describe the whole admin
 * surface it expects, instead of a queue that silently shifts out of order
 * the moment a component makes one extra call. An unrouted key answers 500
 * naming the miss, so a forgotten stub fails loudly instead of hanging.
 */
export function route(handlers: Record<string, () => Response>) {
  const fn = vi.fn<(...args: FetchArgs) => Promise<Response>>((input, init) => {
    const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) {
      return Promise.resolve(
        json(500, { error: { code: "internal", message: `unrouted ${key}` } }),
      );
    }
    return Promise.resolve(handler());
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

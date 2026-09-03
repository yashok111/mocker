import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiFailure, customFetch, setCsrfToken, setUnauthorizedHandler } from "./client";

// customFetch is the ONE place the admin plane's transport rules live, so this
// file asserts each of them directly rather than through a screen that would
// only notice a missing header as "the button didn't work".

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit];

function mockFetch(response: Response): ReturnType<typeof vi.fn> {
  const fn = vi.fn<(...args: FetchArgs) => Promise<Response>>(() => Promise.resolve(response));
  vi.stubGlobal("fetch", fn);
  return fn;
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function headersOf(fn: ReturnType<typeof vi.fn>): Headers {
  const init = fn.mock.calls[0]?.[1] as RequestInit | undefined;
  return new Headers(init?.headers);
}

beforeEach(() => {
  setCsrfToken(null);
  setUnauthorizedHandler(null);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("customFetch", () => {
  it("returns orval's envelope, not the bare body", async () => {
    mockFetch(jsonResponse(200, { ok: true }));

    const res = await customFetch<{ status: number; data: { ok: boolean } }>("/healthz", {});

    // The generated hooks read q.data.status and q.data.data; a mutator that
    // returned the body alone leaves every screen rendering empty on a 200.
    expect(res.status).toBe(200);
    expect(res.data).toEqual({ ok: true });
  });

  it("never sends X-CSRF-Token or Content-Type on a GET", async () => {
    setCsrfToken("tok");
    const fn = mockFetch(jsonResponse(200, []));

    await customFetch("/api/workspaces", { method: "GET" });

    const headers = headersOf(fn);
    expect(headers.has("X-CSRF-Token")).toBe(false);
    expect(headers.has("Content-Type")).toBe(false);
  });

  it.each(["POST", "PUT", "PATCH", "DELETE"])(
    "sends Content-Type and the CSRF token on %s",
    async (method) => {
      setCsrfToken("tok");
      const fn = mockFetch(new Response(null, { status: 204 }));

      await customFetch("/api/workspaces/1", { method });

      const headers = headersOf(fn);
      // Content-Type is unconditional, including on a body-less DELETE: the
      // admin plane answers 415 without it as one leg of its CSRF defence.
      expect(headers.get("Content-Type")).toBe("application/json");
      expect(headers.get("X-CSRF-Token")).toBe("tok");
    },
  );

  it("omits the CSRF header while no token is armed, rather than sending an empty one", async () => {
    const fn = mockFetch(jsonResponse(200, {}));

    await customFetch("/api/auth/login", { method: "POST", body: "{}" });

    expect(headersOf(fn).has("X-CSRF-Token")).toBe(false);
  });

  it("sends credentials same-origin, never include", async () => {
    const fn = mockFetch(jsonResponse(200, {}));

    await customFetch("/api/me", {});

    const init = fn.mock.calls[0]?.[1] as RequestInit;
    expect(init.credentials).toBe("same-origin");
  });

  it("reads a 204 as an empty body without trying to parse it", async () => {
    mockFetch(new Response(null, { status: 204 }));

    const res = await customFetch<{ status: number; data: unknown }>("/api/workspaces/1", {
      method: "DELETE",
    });

    expect(res.status).toBe(204);
    expect(res.data).toBeUndefined();
  });

  it("throws ApiFailure carrying the server's own code and message", async () => {
    mockFetch(jsonResponse(409, { error: { code: "conflict", message: "slug taken" } }));

    await expect(customFetch("/api/workspaces", { method: "POST" })).rejects.toMatchObject({
      name: "ApiFailure",
      status: 409,
      code: "conflict",
      message: "slug taken",
    });
  });

  it("carries the error envelope's details through to ApiFailure (A3/property 7)", async () => {
    // The one boundary that parses { error: { code, message, details } } —
    // if this drops `details`, DeadSiblingCAS-shaped bugs are invisible: a
    // screen can never build the edit_conflict affordance because the value
    // never reached it, and no other test would catch that.
    mockFetch(
      jsonResponse(409, {
        error: {
          code: "edit_conflict",
          message: "stale editVersion",
          details: { overrideOn: true, editVersion: 7 },
        },
      }),
    );

    const err = await customFetch("/api/workspaces/1/operations/getUsers", {
      method: "PUT",
    }).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiFailure);
    expect((err as ApiFailure).details).toEqual({ overrideOn: true, editVersion: 7 });
  });

  it("falls back to a synthesized code when the body is not the error envelope", async () => {
    // net/http's own plain-text 404 for an unrouted /api path, or a proxy's
    // HTML 502 — neither is JSON this client wrote.
    mockFetch(new Response("<html>502</html>", { status: 502 }));

    const err = await customFetch("/api/nope", {}).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiFailure);
    expect((err as ApiFailure).code).toBe("client_unknown_error");
    expect((err as ApiFailure).status).toBe(502);
  });

  it("bounces on a 401 from an ordinary call", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    mockFetch(jsonResponse(401, { error: { code: "unauthorized", message: "no session" } }));

    await customFetch("/api/workspaces", {}).catch(() => undefined);

    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it.each(["/api/me", "/api/auth/login", "/api/auth/logout"])(
    "does NOT bounce on a 401 from %s",
    async (path) => {
      const onUnauthorized = vi.fn();
      setUnauthorizedHandler(onUnauthorized);
      mockFetch(jsonResponse(401, { error: { code: "unauthorized", message: "no session" } }));

      await customFetch(path, {}).catch(() => undefined);

      // The anonymous answer to the session probe IS a 401; bouncing on it
      // loops before the login screen ever renders.
      expect(onUnauthorized).not.toHaveBeenCalled();
    },
  );

  it("matches the auth-flow exemption on a path boundary, not a prefix", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    mockFetch(jsonResponse(401, { error: { code: "unauthorized", message: "no session" } }));

    await customFetch("/api/metrics", {}).catch(() => undefined);

    // "/api/me" is a prefix of "/api/metrics"; a startsWith check without the
    // boundary would silently exempt a future sibling route.
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });
});

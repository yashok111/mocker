import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { ensureSession, forgetSession } from "./session";
import { customFetch } from "@/api/client";
import { authResponseFixture } from "@/test/fixtures";

// ensureSession is what every route guard calls, so what it does with a 401 —
// and what it does to the CSRF token on the way — is the difference between
// "the login screen appears" and "the app loops".

function client(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
}

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit];

function stubFetch(response: Response) {
  const fn = vi.fn<(...args: FetchArgs) => Promise<Response>>(() => Promise.resolve(response));
  vi.stubGlobal("fetch", fn);
  return fn;
}

// csrfHeaderOf performs a throwaway mutation through the real client and
// reports the header it sent — the only externally visible way to ask "is a
// token currently armed", since client.ts deliberately does not export it.
async function armedToken(): Promise<string | null> {
  const fn = stubFetch(new Response(null, { status: 204 }));
  await customFetch("/api/workspaces/1", { method: "DELETE" });
  const init = fn.mock.calls[0]?.[1];
  return new Headers(init?.headers).get("X-CSRF-Token");
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ensureSession", () => {
  it("returns the session and arms the CSRF token on a 200", async () => {
    const body = authResponseFixture();
    stubFetch(
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const session = await ensureSession(client());

    expect(session?.user.name).toBe("alex");
    expect(session?.config.reservedPrefix).toBe("/__test-prefix");
    expect(await armedToken()).toBe("csrf-token-value");
  });

  it("returns null on a 401 rather than throwing", async () => {
    stubFetch(
      new Response(JSON.stringify({ error: { code: "unauthorized", message: "no session" } }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );

    // A 401 from /api/me is the normal anonymous answer; a guard that had to
    // catch an exception for it would treat "logged out" as a crash.
    await expect(ensureSession(client())).resolves.toBeNull();
  });

  it("returns null when fetch itself rejects", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.reject(new TypeError("offline"))),
    );

    await expect(ensureSession(client())).resolves.toBeNull();
  });

  it("disarms the CSRF token when the session is gone", async () => {
    const ok = authResponseFixture();
    stubFetch(
      new Response(JSON.stringify(ok), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await ensureSession(client());
    expect(await armedToken()).toBe("csrf-token-value");

    stubFetch(
      new Response(JSON.stringify({ error: { code: "unauthorized", message: "gone" } }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await ensureSession(client());

    // Leaving a stale token armed would send a dead session's header on the
    // next mutation and get a 403 nobody could explain.
    expect(await armedToken()).toBeNull();
  });

  it("reuses a just-resolved session instead of asking twice", async () => {
    const queryClient = client();
    const fn = stubFetch(
      new Response(JSON.stringify(authResponseFixture()), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await ensureSession(queryClient);
    await ensureSession(queryClient);

    // The router preloads on intent, so hover-then-click reaches the guard
    // twice within milliseconds; that must cost one request, not two.
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("asks again once the cached answer is older than the staleness bound", async () => {
    vi.useFakeTimers();
    try {
      // gcTime well above the advance below: with the default gcTime: 0 the
      // cache entry is collected the moment nothing observes it, so BOTH a
      // cache-first and a staleness-respecting implementation would refetch
      // and this test would assert nothing.
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 5 * 60_000 } },
      });
      const fn = stubFetch(
        new Response(JSON.stringify(authResponseFixture()), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );

      await ensureSession(queryClient);
      await vi.advanceTimersByTimeAsync(30_000);
      await ensureSession(queryClient);

      // The regression this pins: with ensureQueryData (cache-first
      // regardless of staleTime) the second call never reaches the network,
      // so a session expired or logged out in another tab keeps passing the
      // guard — with a stale CSRF token — for as long as the tab stays open.
      expect(fn).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("forgetSession", () => {
  it("drops the cached answer so the next guard asks the server again", async () => {
    const queryClient = client();
    const fn = stubFetch(
      new Response(JSON.stringify(authResponseFixture()), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await ensureSession(queryClient);
    forgetSession(queryClient);
    await ensureSession(queryClient);

    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("disarms the CSRF token", async () => {
    const queryClient = client();
    stubFetch(
      new Response(JSON.stringify(authResponseFixture()), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await ensureSession(queryClient);

    forgetSession(queryClient);

    expect(await armedToken()).toBeNull();
  });
});

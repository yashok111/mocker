// probe.test.ts pins the branch that is this screen's entire reason to
// exist (a 200 with the wrong workspace must never read as success) and
// the rejection branch a real fetch is awkward to force — both drive the
// exact copy the "Проверить" result renders, per the phase brief.
import { afterEach, describe, expect, it, vi } from "vitest";
import { interpretProbe, runProbe, type ProbeOutcome } from "./probe";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("interpretProbe", () => {
  it("reads a matching workspace and revision as ok", () => {
    const outcome: ProbeOutcome = {
      kind: "response",
      status: 200,
      ok: true,
      body: { ok: true, workspace: "alex", revision: 3 },
    };
    expect(interpretProbe(outcome, "alex")).toEqual({ kind: "ok", workspace: "alex", revision: 3 });
  });

  it("flags a 200 answering for a DIFFERENT workspace, never as ok", () => {
    const outcome: ProbeOutcome = {
      kind: "response",
      status: 200,
      ok: true,
      body: { ok: true, workspace: "sam", revision: 1 },
    };
    expect(interpretProbe(outcome, "alex")).toEqual({ kind: "wrong-workspace", workspace: "sam" });
  });

  it("reports the status and the server message on an HTTP error with a parseable body", () => {
    const outcome: ProbeOutcome = {
      kind: "response",
      status: 404,
      ok: false,
      body: {
        error: {
          code: "not_implemented_yet",
          message: "GET /__mocker/health is not implemented yet",
        },
      },
    };
    expect(interpretProbe(outcome, "alex")).toEqual({
      kind: "http-error",
      status: 404,
      message: "GET /__mocker/health is not implemented yet",
    });
  });

  it("falls back to a synthesized message on an HTTP error with an unparseable body", () => {
    const outcome: ProbeOutcome = { kind: "response", status: 502, ok: false, body: undefined };
    const result = interpretProbe(outcome, "alex");
    expect(result.kind).toBe("http-error");
    expect(result).toMatchObject({ status: 502 });
  });

  it("treats a 2xx body that is not the health shape as an error rather than a guessed ok", () => {
    const outcome: ProbeOutcome = {
      kind: "response",
      status: 200,
      ok: true,
      body: { unexpected: true },
    };
    expect(interpretProbe(outcome, "alex").kind).toBe("http-error");
  });

  it("passes a rejected fetch through as network-error", () => {
    expect(interpretProbe({ kind: "network-error" }, "alex")).toEqual({ kind: "network-error" });
  });

  it("passes an aborted fetch through as timeout", () => {
    expect(interpretProbe({ kind: "timeout" }, "alex")).toEqual({ kind: "timeout" });
  });
});

describe("runProbe", () => {
  it("reads the response through interpretProbe on a normal success", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ ok: true, workspace: "alex", revision: 5 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    const result = await runProbe("http://alex.mock.local/__mocker/health", "alex");
    expect(result).toEqual({ kind: "ok", workspace: "alex", revision: 5 });
  });

  // This is the case the phase brief calls out by name: the mock plane sets
  // CORS headers only AFTER a workspace resolves, so a host that names no
  // workspace at all makes fetch() itself reject — no status to read, no
  // body to parse.
  it("maps a rejected fetch (no CORS headers, DNS failure, TLS distrust) to network-error", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    const result = await runProbe("http://ghost.mock.local/__mocker/health", "ghost");
    expect(result).toEqual({ kind: "network-error" });
  });

  it("reports timeout, not network-error, when the request just never answers", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn((_url: string, init?: RequestInit) => {
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => {
          reject(new DOMException("The operation was aborted.", "AbortError"));
        });
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    const pending = runProbe("http://alex.mock.local/__mocker/health", "alex", 3000);
    await vi.advanceTimersByTimeAsync(3000);

    await expect(pending).resolves.toEqual({ kind: "timeout" });
  });
});

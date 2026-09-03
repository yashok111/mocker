/**
 * @yashok111/mocker-test — a typed client for the mock plane's control routes, so a
 * test suite owns the mock instead of an operator: switch a scenario before
 * a block, force a status for one test, reset after.
 *
 * Everything here is `fetch` against the WORKSPACE host (the URL
 * `get_workspace` / the «Подключить» panel reports), on the reserved
 * prefix (`MOCKER_RESERVED_PREFIX`, default `/__mocker`). No login, no
 * CSRF, no admin host: the mock plane is unauthenticated by design, and
 * these routes exist precisely for a test runner.
 *
 * Three routes, six verbs:
 *   GET    {prefix}/health  → { ok, workspace, revision, spec }
 *   GET    {prefix}/state   → { workspace, directives[] }
 *   POST   {prefix}/state   → a directive ({target, action, …}) or a scenario switch ({scenario})
 *   DELETE {prefix}/state   → clears every directive
 *
 * Directives live in RAM on the server, never bump `revision` and are lost
 * on restart; a scenario switch is persisted and bumps `revision`.
 */

/** A directive's target: every operation (`"*"`), a `"METHOD /path"` string, or the object form. */
export type Target = "*" | `${string} ${string}` | { method: string; path: string };

/** A directive as the server lists it. */
export interface Directive {
  target: "*" | { method: string; path: string };
  action: "status" | "fail" | "delay" | "pause";
  status?: number;
  ms?: number;
  once: boolean;
  n: number;
  setAt: string;
}

export interface Health {
  ok: boolean;
  workspace: string;
  revision: number;
  spec: number | null;
}

export interface StateList {
  workspace: string;
  directives: Directive[];
}

export interface Cleared {
  workspace: string;
  cleared: number;
}

export interface ScenarioSwitched {
  workspace: string;
  /** The active scenario's name after the switch; null after a deactivation. */
  scenario: string | null;
  revision: number;
}

export interface MockerOptions {
  /** `MOCKER_RESERVED_PREFIX` of the server; default `/__mocker`. */
  prefix?: string;
  /** A fetch implementation; default the global one (Node ≥ 18, every browser). */
  fetch?: typeof fetch;
  /** Per-request timeout for control calls; default 10 000 ms. Does not apply to `mock.fetch`. */
  timeoutMs?: number;
}

/** The server refused a control call: the status and the error envelope's `code`/`message`/`details`. */
export class MockerError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: unknown;
  constructor(status: number, code: string, message: string, details?: unknown) {
    super(`mocker: ${status} ${code}: ${message}`);
    this.name = "MockerError";
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

/** A wait ran out of time. */
export class MockerTimeoutError extends Error {
  constructor(message: string) {
    super(`mocker: ${message}`);
    this.name = "MockerTimeoutError";
  }
}

/** Options of `fail`. */
export interface FailOptions {
  /** Fail the next N matching requests, then serve normally; default 1. */
  times?: number;
}

/** Options of `waitForRevision`. */
export interface WaitOptions {
  timeoutMs?: number;
  intervalMs?: number;
}

function normalizeTarget(target: Target): "*" | { method: string; path: string } {
  if (typeof target !== "string") {
    return { method: target.method.toUpperCase(), path: target.path };
  }
  if (target === "*") return "*";
  const space = target.indexOf(" ");
  if (space <= 0) {
    throw new TypeError(
      `mocker: target must be "*", "METHOD /path" or {method, path}; got ${JSON.stringify(target)}`,
    );
  }
  return { method: target.slice(0, space).toUpperCase(), path: target.slice(space + 1).trim() };
}

function joinURL(base: string, path: string): string {
  return base.replace(/\/+$/, "") + (path.startsWith("/") ? path : "/" + path);
}

/** The client `mocker(url)` returns. One instance per workspace URL. */
export class MockerClient {
  readonly url: string;
  readonly prefix: string;
  private readonly fetchImpl: typeof fetch;
  private readonly timeoutMs: number;

  constructor(url: string, options: MockerOptions = {}) {
    if (!/^https?:\/\//.test(url)) {
      throw new TypeError(
        `mocker: url must be absolute (http:// or https://), got ${JSON.stringify(url)}`,
      );
    }
    this.url = url.replace(/\/+$/, "");
    this.prefix = "/" + (options.prefix ?? "/__mocker").replace(/^\/+|\/+$/g, "");
    this.fetchImpl = options.fetch ?? globalThis.fetch;
    this.timeoutMs = options.timeoutMs ?? 10_000;
    if (typeof this.fetchImpl !== "function") {
      throw new TypeError("mocker: no fetch available; pass options.fetch");
    }
  }

  /** `GET {prefix}/health` — the workspace's slug, `revision` and bound spec. */
  health(): Promise<Health> {
    return this.control<Health>("GET", "/health");
  }

  /** `GET {prefix}/state` — every directive currently set. */
  state(): Promise<StateList> {
    return this.control<StateList>("GET", "/state");
  }

  /**
   * `DELETE {prefix}/state` — clears every directive, which also releases
   * any request parked by `pause`; `clear(target)` for one target. The
   * active scenario is NOT touched; `scenario(null)` for that.
   */
  reset(): Promise<Cleared> {
    return this.control<Cleared>("DELETE", "/state");
  }

  /**
   * `DELETE {prefix}/state {target, action?}` — removes the directives on
   * ONE target (every action on it, or only `action`), releasing a request
   * parked by a pause on it; every other target's directives stay. The
   * answer counts what was removed; a target holding nothing answers 0.
   */
  clear(target: Target, action?: Directive["action"]): Promise<Cleared> {
    const body: Record<string, unknown> = { target: normalizeTarget(target) };
    if (action !== undefined) body.action = action;
    return this.control<Cleared>("DELETE", "/state", body);
  }

  /**
   * Activates a scenario by name (`null` or `""` deactivates). Persisted on
   * the server and bumps `revision`; a scenario that does not exist answers
   * 404 `not_found`. Renaming a scenario in the panel breaks the tests that
   * switch by the old name — the name is the contract.
   */
  scenario(name: string | null): Promise<ScenarioSwitched> {
    return this.control<ScenarioSwitched>("POST", "/state", { scenario: name ?? "" });
  }

  /** Forces `status` on every matching request until `reset()`. */
  status(target: Target, status: number): Promise<StateList> {
    return this.directive({ target: normalizeTarget(target), action: "status", status });
  }

  /** Fails the next `times` (default 1) matching requests with `status`, then serves normally. */
  fail(target: Target, status: number, options: FailOptions = {}): Promise<StateList> {
    const times = options.times ?? 1;
    if (!Number.isInteger(times) || times < 1) {
      throw new TypeError(`mocker: fail times must be a positive integer, got ${String(times)}`);
    }
    return this.directive({ target: normalizeTarget(target), action: "fail", status, n: times });
  }

  /** Holds every matching response for `ms` milliseconds (1..30000) until `reset()`. */
  delay(target: Target, ms: number): Promise<StateList> {
    return this.directive({ target: normalizeTarget(target), action: "delay", ms });
  }

  /** Parks every matching request until `clear(target)` or `reset()` — the way to test a spinner. */
  pause(target: Target): Promise<StateList> {
    return this.directive({ target: normalizeTarget(target), action: "pause" });
  }

  /**
   * Polls `health` until `revision >= atLeast`. `revision` moves on every
   * configuration change and on a scenario switch — never on a directive —
   * so this is the one signal a test has that an operator's or an agent's
   * edit is live.
   */
  async waitForRevision(atLeast: number, options: WaitOptions = {}): Promise<Health> {
    const timeoutMs = options.timeoutMs ?? this.timeoutMs;
    const intervalMs = options.intervalMs ?? 100;
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const h = await this.health();
      if (h.revision >= atLeast) return h;
      if (Date.now() >= deadline) {
        throw new MockerTimeoutError(`revision ${h.revision} < ${atLeast} after ${timeoutMs} ms`);
      }
      await new Promise((r) => setTimeout(r, intervalMs));
    }
  }

  /** A plain request to the workspace host — `mock.fetch("/orders")` — for asserting what the mock serves. */
  fetch(path: string, init?: RequestInit): Promise<Response> {
    return this.fetchImpl(joinURL(this.url, path), init);
  }

  private directive(body: Record<string, unknown>): Promise<StateList> {
    return this.control<StateList>("POST", "/state", body);
  }

  private async control<T>(method: string, route: string, body?: unknown): Promise<T> {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), this.timeoutMs);
    try {
      const res = await this.fetchImpl(joinURL(this.url, this.prefix + route), {
        method,
        headers: body === undefined ? undefined : { "content-type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: ctl.signal,
      });
      const text = await res.text();
      let parsed: unknown = null;
      try {
        parsed = text ? JSON.parse(text) : null;
      } catch {
        parsed = null;
      }
      if (!res.ok) {
        const env = (
          parsed as { error?: { code?: string; message?: string; details?: unknown } } | null
        )?.error;
        throw new MockerError(
          res.status,
          env?.code ?? "http_error",
          env?.message ?? (text || res.statusText),
          env?.details,
        );
      }
      return parsed as T;
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") {
        throw new MockerTimeoutError(
          `${method} ${this.prefix}${route} took longer than ${this.timeoutMs} ms`,
        );
      }
      throw err;
    } finally {
      clearTimeout(timer);
    }
  }
}

/** Creates a client for one workspace: `mocker("http://alex.mock.local")`. */
export function mocker(url: string, options?: MockerOptions): MockerClient {
  return new MockerClient(url, options);
}

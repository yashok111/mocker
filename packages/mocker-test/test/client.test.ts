/**
 * The client against a REAL mocker binary (../../bin/mocker, built by
 * `make build`): one server for the file, path routing on a loopback port,
 * a workspace bound to a three-operation spec, and every verb of the client
 * observed on the mock plane — a forced status answers, a fail-next counts
 * down, a delay is measured, a pause parks a request until reset releases
 * it, a scenario switch moves `revision`.
 */
import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, existsSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest";
import {
  mocker,
  MockerError,
  MockerTimeoutError,
  type Directive,
  type MockerClient,
  type Target,
} from "../src/index.js";
import { mockerFixture } from "../src/playwright.js";
import { registerMockerCommands } from "../src/cypress.js";

const BINARY = resolve(import.meta.dirname, "../../../bin/mocker");
const PASSWORD = "plugin-test-password";

const SPEC = {
  openapi: "3.0.3",
  info: { title: "orders", version: "1" },
  paths: {
    "/orders": {
      get: {
        responses: {
          200: {
            content: {
              "application/json": {
                schema: { type: "array", items: { $ref: "#/components/schemas/Order" } },
              },
            },
          },
        },
      },
      post: {
        requestBody: {
          content: { "application/json": { schema: { $ref: "#/components/schemas/Order" } } },
        },
        responses: {
          201: {
            content: { "application/json": { schema: { $ref: "#/components/schemas/Order" } } },
          },
        },
      },
    },
    "/cart": {
      get: {
        responses: {
          200: {
            content: { "application/json": { schema: { $ref: "#/components/schemas/Order" } } },
          },
        },
      },
    },
  },
  components: {
    schemas: {
      Order: {
        type: "object",
        required: ["id", "total"],
        properties: { id: { type: "integer" }, total: { type: "number" } },
      },
    },
  },
};

function freePort(): Promise<number> {
  return new Promise((res, rej) => {
    const srv = createServer();
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      srv.close(() =>
        typeof addr === "object" && addr ? res(addr.port) : rej(new Error("no port")),
      );
    });
  });
}

interface Admin {
  base: string;
  cookie: string;
  csrf: string;
  call(
    method: string,
    path: string,
    body?: unknown,
  ): Promise<{ status: number; json: Record<string, unknown> }>;
}

let server: ChildProcess | undefined;
let dataDir = "";
let admin: Admin;
let mockURL = "";
let workspaceId = 0;
let mock: MockerClient;

beforeAll(async () => {
  if (!existsSync(BINARY)) throw new Error(`${BINARY} not built: run make build first`);
  const hash = spawnSync(BINARY, ["hash-password", PASSWORD], { encoding: "utf8" });
  if (hash.status !== 0) throw new Error(`hash-password: ${hash.stderr}`);
  const port = await freePort();
  dataDir = mkdtempSync(join(tmpdir(), "mocker-test-"));
  server = spawn(BINARY, [], {
    env: {
      PATH: process.env.PATH ?? "",
      MOCKER_ADDR: `127.0.0.1:${port}`,
      MOCKER_DATA_DIR: dataDir,
      MOCKER_ROUTING: "path",
      MOCKER_ADMIN_HOST: "localhost",
      MOCKER_BASE_DOMAIN: "mock.local",
      MOCKER_DEV: "1",
      MOCKER_SHARED_PASSWORD_HASH: hash.stdout.trim(),
      MOCKER_LOG_LEVEL: "warn",
    },
    stdio: ["ignore", "ignore", "pipe"],
  });
  let stderr = "";
  server.stderr?.on("data", (d: Buffer) => (stderr += d.toString()));
  const base = `http://localhost:${port}`;
  const deadline = Date.now() + 30_000;
  for (;;) {
    try {
      const r = await fetch(`${base}/readyz`);
      if (r.ok) break;
    } catch {
      /* not up yet */
    }
    if (server.exitCode !== null) throw new Error(`mocker exited ${server.exitCode}: ${stderr}`);
    if (Date.now() > deadline) throw new Error(`mocker did not become ready: ${stderr}`);
    await new Promise((r) => setTimeout(r, 100));
  }

  // Admin plane: login, spec, workspace. Origin is required by the CSRF
  // chain on every state-changing call, the token by every one after login.
  const login = await fetch(`${base}/api/auth/login`, {
    method: "POST",
    headers: { "content-type": "application/json", origin: base },
    body: JSON.stringify({ password: PASSWORD, name: "plugin-test" }),
  });
  if (!login.ok) throw new Error(`login ${login.status}: ${await login.text()}`);
  const cookie = login.headers.get("set-cookie")?.split(";")[0] ?? "";
  const csrf = ((await login.json()) as { csrfToken: string }).csrfToken;
  admin = {
    base,
    cookie,
    csrf,
    async call(method, path, body) {
      const r = await fetch(base + path, {
        method,
        headers: { "content-type": "application/json", origin: base, cookie, "x-csrf-token": csrf },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      const text = await r.text();
      return { status: r.status, json: text ? (JSON.parse(text) as Record<string, unknown>) : {} };
    },
  };
  const spec = await admin.call("POST", "/api/specs", {
    name: "orders",
    source: "upload",
    document: JSON.stringify(SPEC),
  });
  if (spec.status !== 201)
    throw new Error(`spec import ${spec.status}: ${JSON.stringify(spec.json)}`);
  const ws = await admin.call("POST", "/api/workspaces", {
    name: "alex",
    slug: "alex",
    specId: spec.json.id,
  });
  if (ws.status !== 201) throw new Error(`workspace ${ws.status}: ${JSON.stringify(ws.json)}`);
  workspaceId = ws.json.id as number;
  mockURL = `${base}/w/alex`;
  mock = mocker(mockURL);
});

afterAll(async () => {
  if (server && server.exitCode === null) {
    server.kill("SIGTERM");
    await new Promise<void>((r) => {
      server?.once("exit", () => r());
      setTimeout(r, 5000);
    });
  }
  if (dataDir) rmSync(dataDir, { recursive: true, force: true });
});

beforeEach(async () => {
  await mock.reset();
});

describe("health and state", () => {
  it("health names the workspace and its revision", async () => {
    const h = await mock.health();
    expect(h.ok).toBe(true);
    expect(h.workspace).toBe("alex");
    expect(h.revision).toBeGreaterThanOrEqual(1);
    expect(h.spec).toBeTypeOf("number");
  });

  it("state starts empty and reset reports what it cleared", async () => {
    expect((await mock.state()).directives).toEqual([]);
    await mock.status("GET /orders", 500);
    await mock.delay("*", 5);
    expect((await mock.state()).directives).toHaveLength(2);
    expect((await mock.reset()).cleared).toBe(2);
    expect((await mock.state()).directives).toEqual([]);
  });

  it("a wrong prefix is a MockerError with the server's status", async () => {
    const wrong = mocker(mockURL, { prefix: "/nope" });
    await expect(wrong.health()).rejects.toBeInstanceOf(MockerError);
  });
});

describe("directives observed on the mock plane", () => {
  it("status forces every matching request until reset", async () => {
    expect((await mock.fetch("/orders")).status).toBe(200);
    await mock.status("GET /orders", 503);
    expect((await mock.fetch("/orders")).status).toBe(503);
    expect((await mock.fetch("/orders")).status).toBe(503);
    expect((await mock.fetch("/cart")).status).toBe(200);
    await mock.reset();
    expect((await mock.fetch("/orders")).status).toBe(200);
  });

  it("fail counts down and then serves normally", async () => {
    await mock.fail("POST /orders", 500, { times: 2 });
    const post = () =>
      mock.fetch("/orders", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: "{}",
      });
    expect((await post()).status).toBe(500);
    expect((await post()).status).toBe(500);
    expect((await post()).status).toBe(201);
  });

  it("fail defaults to once", async () => {
    await mock.fail("GET /cart", 418);
    expect((await mock.fetch("/cart")).status).toBe(418);
    expect((await mock.fetch("/cart")).status).toBe(200);
  });

  it("the object target and the wildcard both address operations", async () => {
    await mock.status({ method: "get", path: "/cart" }, 404);
    expect((await mock.fetch("/cart")).status).toBe(404);
    await mock.reset();
    await mock.status("*", 502);
    expect((await mock.fetch("/cart")).status).toBe(502);
    expect((await mock.fetch("/orders")).status).toBe(502);
  });

  it("delay holds the response", async () => {
    await mock.delay("GET /cart", 300);
    const started = Date.now();
    expect((await mock.fetch("/cart")).status).toBe(200);
    expect(Date.now() - started).toBeGreaterThanOrEqual(250);
  });

  it("clear removes one target's directives and releases its pause, leaving the rest", async () => {
    await mock.status("GET /orders", 500);
    await mock.pause("GET /cart");
    await mock.delay("GET /cart", 5);
    const parked = mock.fetch("/cart");
    expect((await mock.clear("GET /cart")).cleared).toBe(2);
    expect((await parked).status).toBe(200);
    expect((await mock.fetch("/orders")).status).toBe(500);
    expect((await mock.state()).directives).toHaveLength(1);
    expect((await mock.clear("GET /cart")).cleared).toBe(0);
    // One action only: the status stays, the delay goes.
    await mock.delay("GET /orders", 5);
    expect((await mock.clear("GET /orders", "delay")).cleared).toBe(1);
    expect((await mock.fetch("/orders")).status).toBe(500);
    await expect(mock.clear("GET /orders", "nope" as Directive["action"])).rejects.toMatchObject({
      status: 400,
    });
  });

  it("pause parks a request and reset releases it", async () => {
    await mock.pause("GET /cart");
    const parked = mock.fetch("/cart");
    const raced = await Promise.race([
      parked.then(() => "answered"),
      new Promise((r) => setTimeout(() => r("still parked"), 400)),
    ]);
    expect(raced).toBe("still parked");
    await mock.reset();
    expect((await parked).status).toBe(200);
  });

  it("refuses a directive the server refuses, with the server's words", async () => {
    await expect(mock.delay("GET /cart", 999_999)).rejects.toMatchObject({
      name: "MockerError",
      status: 400,
    });
    expect(() => mock.fail("GET /cart", 500, { times: 0 })).toThrow(TypeError);
    expect(() => mock.status("nospace" as Target, 500)).toThrow(TypeError);
  });
});

describe("scenarios and revision", () => {
  it("switches by name, bumps revision, and deactivates with null", async () => {
    const before = (await mock.health()).revision;
    const created = await admin.call("POST", `/api/workspaces/${workspaceId}/scenarios`, {
      name: "checkout-empty",
    });
    expect(created.status).toBe(201);

    const on = await mock.scenario("checkout-empty");
    expect(on.scenario).toBe("checkout-empty");
    expect(on.revision).toBeGreaterThan(before);
    expect((await mock.waitForRevision(on.revision)).revision).toBe(on.revision);

    const off = await mock.scenario(null);
    expect(off.scenario).toBeNull();
    expect(off.revision).toBeGreaterThan(on.revision);
  });

  it("an unknown scenario is a 404 MockerError", async () => {
    await expect(mock.scenario("no-such-scenario")).rejects.toMatchObject({
      name: "MockerError",
      status: 404,
    });
  });

  it("waitForRevision times out with MockerTimeoutError", async () => {
    const h = await mock.health();
    await expect(
      mock.waitForRevision(h.revision + 1000, { timeoutMs: 300, intervalMs: 50 }),
    ).rejects.toBeInstanceOf(MockerTimeoutError);
  });
});

describe("integrations", () => {
  it("the Playwright fixture resets around the test and activates the scenario", async () => {
    await mock.status("GET /cart", 500);
    const fixture = mockerFixture({ url: mockURL, scenario: "checkout-empty" });
    let seen: MockerClient | undefined;
    await fixture({}, async (m) => {
      seen = m;
      expect((await m.state()).directives).toEqual([]);
      expect((await m.health()).revision).toBeGreaterThan(0);
      await m.status("GET /cart", 500);
    });
    expect(seen).toBeDefined();
    expect((await mock.state()).directives).toEqual([]);
    await mock.scenario(null);
  });

  it("the Cypress registration adds one command per verb, each wrapping a promise", async () => {
    const added: Record<string, (...args: unknown[]) => unknown> = {};
    const wrapped: unknown[] = [];
    registerMockerCommands(
      {
        Cypress: {
          Commands: { add: (name, fn) => (added[name] = fn as (...args: unknown[]) => unknown) },
        },
        cy: { wrap: (v) => (wrapped.push(v), v) },
      },
      { url: mockURL },
    );
    expect(Object.keys(added).sort()).toEqual([
      "mockerClear",
      "mockerDelay",
      "mockerFail",
      "mockerHealth",
      "mockerPause",
      "mockerReset",
      "mockerScenario",
      "mockerState",
      "mockerStatus",
      "mockerWaitForRevision",
    ]);
    const health = added.mockerHealth;
    const status = added.mockerStatus;
    if (!health || !status) throw new Error("commands not registered");
    const h = (await (health() as Promise<{ workspace: string }>)).workspace;
    expect(h).toBe("alex");
    await status("GET /cart", 599);
    expect((await mock.fetch("/cart")).status).toBe(599);
    expect(wrapped).toHaveLength(2);
  });
});

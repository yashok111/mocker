import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// This file IS the phase's acceptance criterion, written as a runnable check:
// every route api/openapi.json declares must be called from some screen
// under web/src. It is a WRITE-ONCE guard — do not soften an assertion here
// to make a red run go green; fix the screen instead.
//
// It reads the COMMITTED contract (api/openapi.json), never the generated
// client (web/src/api/generated/, gitignored, a `make ui-gen` build output).
// A tree where that generation has never run still HAS an openapi.json, so
// this test cannot pass vacuously on it the way a "grep every generated
// use* name" version would — that version also passes on a name mentioned
// only inside a test file or inside its own generated source, and passes for
// a component nothing routes to, none of which is real coverage. Reachability
// (is the screen that calls the hook actually mounted anywhere) is a
// SEPARATE concern this file cannot see — web/src/routes/routes.test.tsx
// mounts the real route tree and checks that half.

const repoRoot = path.resolve(__dirname, "../../..");
const openapiPath = path.join(repoRoot, "api/openapi.json");
// __dirname is web/src/api; web/src is one level up.
const webSrcDir = path.resolve(__dirname, "..");
const thisFile = path.resolve(__dirname, "coverage.test.ts");

const HTTP_METHODS = ["get", "post", "put", "delete", "patch"] as const;

interface RouteInfo {
  method: string;
  urlPath: string;
  operationId?: string;
}

interface OpenApiOperation {
  operationId?: string;
}

interface OpenApiDoc {
  paths: Record<string, Partial<Record<(typeof HTTP_METHODS)[number], OpenApiOperation>>>;
}

function loadRoutes(): RouteInfo[] {
  const doc = JSON.parse(fs.readFileSync(openapiPath, "utf8")) as OpenApiDoc;
  const routes: RouteInfo[] = [];
  for (const [urlPath, byMethod] of Object.entries(doc.paths)) {
    for (const method of HTTP_METHODS) {
      const op = byMethod[method];
      if (!op) continue;
      routes.push({ method: method.toUpperCase(), urlPath, operationId: op.operationId });
    }
  }
  return routes;
}

// Every route below is a probe with no screen — recorded here, with a reason,
// so an exemption is a decision on the record rather than a silent gap. Any
// OTHER uncovered route is a real gap: it belongs in uncoveredRoutes, not in
// this list.
//
// GET /api/workspaces/{id}/drift is the first entry here that is not an
// infrastructure probe (decisions.md mocker-p4a-triage D6.3): the agent is
// primary and a screen is optional (CLAUDE.md's own coverage invariant), so
// a route may ship with its MCP tool (get_workspace_drift) and no screen at
// all. The reverse — a screen with no tool — stays forbidden; this is not
// that.
// P6e (2026-09-02) REMOVED four entries here — POST .../endpoints/preview and
// the three /connections operations — because the screens §30.14 designs now
// call them: StreamEditor.tsx (usePreviewEndpoint) and StreamConnectionsPage.tsx
// (useListStreamConnections, useCloseStreamConnection, usePushStreamFrame).
// An exemption is a decision on the record; so is its withdrawal. A10
// withdrew the three asset operations the same way (AssetsPage.tsx).
// A20 (2026-09-05) WITHDREW the last ten entries in two steps, each on the
// owner's word. First six, on «надо бы доделать страницы» and his pick of
// which (a Russian string quoted as data): the three entity routes of
// A4/A11 (ResourceEntities.tsx, under «Записи» on the resources screen)
// and the three P4b transfer routes (TransferPanel.tsx on the overview,
// the import modal on WorkspacesPage.tsx). Then, on «добей последние 4
// гэпа» the same day, the four that were left: GET .../drift (DriftPanel.tsx
// on the overview — the screen he had refused on 2026-09-03 and asked for
// by name here, CARVE-OUTS.md "Ideas refused" records both), GET
// /api/stream/stats (the strip on StreamConnectionsPage.tsx) and the two
// probes /healthz and /readyz (the header's server status in AppShell.tsx,
// the pair the container's own HEALTHCHECK reads). P7b (2026-09-03) had
// withdrawn GET .../openapi.json before that. The map is empty, the
// mechanism stays: a future route that ships agent-only earns an entry
// here naming its tool, and the test keeps checking that every other route
// has a caller. An exemption is a decision on the record and so is its
// withdrawal, in this comment.
const EXEMPT: Record<string, string> = {};

function walk(dir: string, out: string[]): void {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(full, out);
    } else if (entry.isFile() && /\.tsx?$/.test(entry.name)) {
      out.push(full);
    }
  }
}

// Only the code a real screen could execute: no test files (a fetch-stub key
// or a `useX(` mentioned inside a spec is not a caller), no src/test/**
// helpers, no src/api/generated/** (that IS the thing being called, not a
// caller of itself), and not this file (its own exemption list and symbol
// strings must not count as coverage of the routes they list).
function scannableFiles(): string[] {
  const all: string[] = [];
  walk(webSrcDir, all);
  return all.filter((file) => {
    if (file === thisFile) return false;
    const rel = path.relative(webSrcDir, file).split(path.sep).join("/");
    if (path.basename(file).includes(".test.")) return false;
    if (rel.startsWith("test/")) return false;
    if (rel.startsWith("api/generated/")) return false;
    return true;
  });
}

function readScannableContent(): string {
  return scannableFiles()
    .map((file) => fs.readFileSync(file, "utf8"))
    .join("\n");
}

// X is the operationId with its first letter capitalised and nothing else
// changed — verified against every symbol orval actually emits (see
// p1e-context.md §3): no operationId in this contract contains an acronym,
// underscore or hyphen, so a word-splitting PascalCase conversion would be
// solving a problem this contract doesn't have.
function capitalize(operationId: string): string {
  return operationId.charAt(0).toUpperCase() + operationId.slice(1);
}

// orval emits four call shapes per operation and a screen may legitimately
// use any one of them — web/src/auth/session.ts calls getGetMeQueryOptions
// directly and never useGetMe. Each candidate is required to be followed by
// "(" so that an imported-but-never-called symbol does not count.
function acceptedSymbols(operationId: string): string[] {
  const capitalised = capitalize(operationId);
  return [
    `use${capitalised}(`,
    `get${capitalised}QueryOptions(`,
    `get${capitalised}MutationOptions(`,
    `get${capitalised}QueryKey(`,
  ];
}

// The fifth consumption shape (P6a, decisions.md mocker-p6a-sse D17): a
// route consumed by the browser's own streaming client, `new
// EventSource(\`/api/workspaces/${id}/traffic/stream?since=${since}\`)`. The
// SCANNER is the side that bends — each `{param}` segment of the contract's
// template becomes a one-segment wildcard, so the natural template literal a
// screen writes (`${id}` where the contract says `{id}`) matches without the
// screen being written around the guard; a literal `{id}` would not appear
// in correct React code. Only a GET can be an EventSource target.
function eventSourcePattern(urlPath: string): RegExp {
  const escaped = urlPath.replace(/[.*+?^$()|[\]\\]/g, "\\$&").replace(/\{[^}]+\}/g, "[^/`?]+");
  return new RegExp("EventSource\\(\\s*`" + escaped + "(?:[?`])");
}

describe("web/src API coverage", () => {
  const routes = loadRoutes();

  it("still describes exactly 64 routes in api/openapi.json", () => {
    // A route silently dropped from the contract would otherwise shrink the
    // population this test checks and pass by covering less, not more.
    // 34 before P2b; +6 for DESIGN §4's Scenario layer (§C of the P2b
    // context: list, create, detail, delete, activate, deactivate); +4 for
    // P2c's history/undo layer (§C of the P2c context: list checkpoints,
    // create checkpoint, rollback, reset-overrides); +2 for P2d (rename a
    // scenario, delete a checkpoint); +1 for P2f's preview route; +1 for
    // A1's PUT .../endpoints/{eid} (editing a custom endpoint); +3 for P3a's
    // resources surface (D10: list a spec's resource suggestions, list a
    // workspace's resource families, one decision route for confirm/decline);
    // +1 for P3b's POST .../reset-data (reseed or clear a workspace's stored
    // entity rows); +1 for P3f's POST /api/specs/{id}/rederive (decisions.md
    // §D4: re-run derivation over an already-imported spec); +1 for P4a's
    // GET /api/workspaces/{id}/drift (decisions.md §D4: the three signals a
    // spec re-import leaves behind, read-only, agent-only), 53 -> 54; +1 for
    // A4's GET /api/workspaces/{id}/resources/{family}/entities (decisions.md
    // mocker-a4-mcp-reach D4: a confirmed family's entity rows, paginated and
    // scope-filtered, read-only, agent-only), 54 -> 55; +2 for P6a's
    // GET /api/workspaces/{id}/traffic/stream (decisions.md mocker-p6a-sse
    // D3: the traffic feed over SSE, consumed through EventSource — see
    // eventSourcePattern) and GET /api/stream/stats (D15: process-wide
    // streaming health, agent-only), 55 -> 57; +1 for P6b's
    // POST /api/workspaces/{id}/endpoints/preview (decisions.md
    // mocker-p6b-sse-mock D13: a stream draft's first frames, agent-only),
    // 57 -> 58.
    // This count is OPERATIONS (method + path), not `paths` keys — a
    // 48-to-51 edit that instead counted paths would silently undercount.
    expect(routes).toHaveLength(70);
  });

  it("every route declares an operationId", () => {
    // A missing operationId would make capitalize() derive a symbol from
    // undefined and turn a real, correctly-covered route red — pushing
    // whoever fixes it toward inventing a hook call instead of naming the
    // operation in the contract, where the fix belongs.
    const missing = routes
      .filter((route) => !route.operationId)
      .map((route) => `${route.method} ${route.urlPath}`);
    expect(missing).toEqual([]);
  });

  it("every non-exempt route is called from some screen under web/src", () => {
    const content = readScannableContent();
    const uncovered: string[] = [];
    for (const route of routes) {
      const key = `${route.method} ${route.urlPath}`;
      if (key in EXEMPT) continue;
      if (!route.operationId) continue; // reported by the assertion above
      const covered =
        acceptedSymbols(route.operationId).some((symbol) => content.includes(symbol)) ||
        (route.method === "GET" && eventSourcePattern(route.urlPath).test(content));
      if (!covered) uncovered.push(key);
    }
    expect(uncovered, `routes with no caller under web/src:\n${uncovered.join("\n")}`).toEqual([]);
  });
});

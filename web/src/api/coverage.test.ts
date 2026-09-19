import fs from "node:fs";
import path from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import {
  analyzeSourceFile,
  eventSourceCovers,
  isGeneratedClientSpecifier,
  operationIsCalled,
  parseFiles,
  type FileAnalysis,
  type ParsedSources,
} from "./coverageScanner";

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
//
// HOW A CALLER IS RECOGNISED changed on 2026-09-07: until then this file
// concatenated every scannable source and asked whether the text contained
// `useListTraffic(`. That is a text search wearing a guard's clothes — it
// counted a name written in a comment or a string, it counted
// `getXQueryKey(` (a cache address, not a request), and it MISSED an
// aliased import outright. coverageScanner.ts now parses each file with the
// TypeScript compiler's own parser and reports the generated symbols the
// file actually CALLS; the rules, and the two limitations left standing,
// are documented there. The population and the assertions below are
// unchanged, and so is the verdict on all 70 routes: the old rule and the
// new one agree on every one of them today (measured 2026-09-07), which is
// what makes this a rewrite of the scanner and not a change of the bar.

const repoRoot = path.resolve(__dirname, "../../..");
const openapiPath = path.join(repoRoot, "api/openapi.json");
// __dirname is web/src/api; web/src is one level up, the package root two.
const webSrcDir = path.resolve(__dirname, "..");
const webDir = path.resolve(__dirname, "../..");
const thisFile = path.resolve(__dirname, "coverage.test.ts");

const HTTP_METHODS = ["get", "post", "put", "delete", "patch"] as const;

// The contract's operation count, pinned as a LITERAL on purpose: it is a
// trip-wire, and a number derived from the file it is meant to police would
// agree with anything that file said. Every test below that wants to name it
// reads it from here, which is the one thing the old title did not do — it
// said "64" while the assertion said 70, for four slices running.
const ROUTE_COUNT = 82;

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

// The routes no generated hook can ever cover, because the browser reaches
// them through a transport orval does not emit. Each entry NAMES the file
// that holds the transport, and the test below re-derives the URL from that
// file's AST — so this manifest is a claim the suite checks, not a
// declaration it takes on trust. It replaces the old scanner's
// `EventSource\(\s*\`…\`` regex over the whole concatenated tree, which
// would have accepted the call from anywhere, including a comment.
//
// The only member is P6a's traffic feed (decisions.md mocker-p6a-sse D17,
// D19). api/stream.ts explains why it is not a hook: a generated one would
// run through customFetch, which awaits res.text() and would therefore never
// resolve on a stream that does not end. That same file's
// probeStreamRefusal() also fetches this URL raw, to read the status a
// native EventSource error event does not carry — the same route, the same
// file, and it needs no entry of its own.
//
// NOT here, and deliberately: the raw fetch in connect/probe.ts. It dials
// {ws.url}{reservedPrefix}/health on the MOCK plane, which api/openapi.json
// (the ADMIN contract) does not describe at all, so it covers no route in
// this population.
interface NativeTransport {
  /** "METHOD /path" exactly as this test keys a route. */
  readonly route: string;
  /** The file that opens it, relative to web/src. */
  readonly file: string;
  readonly why: string;
}

const NATIVE_TRANSPORTS: readonly NativeTransport[] = [
  {
    route: "GET /api/workspaces/{id}/traffic/stream",
    file: "api/stream.ts",
    why: "SSE through the browser's own EventSource; a generated hook would never resolve.",
  },
];

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
// caller of itself), no *.gen.ts (routeTree.gen.ts is the router's own build
// output, regenerated by `make ui-gen` and absent on a fresh clone), and not
// this file (its own exemption list and symbol strings must not count as
// coverage of the routes they list).
function scannableFiles(): string[] {
  const all: string[] = [];
  walk(webSrcDir, all);
  return all.filter((file) => {
    if (file === thisFile) return false;
    const rel = path.relative(webSrcDir, file).split(path.sep).join("/");
    if (path.basename(file).includes(".test.")) return false;
    if (/\.gen\.tsx?$/.test(path.basename(file))) return false;
    if (rel.startsWith("test/")) return false;
    if (rel.startsWith("api/generated/")) return false;
    return true;
  });
}

describe("web/src API coverage", () => {
  const routes = loadRoutes();
  const files = scannableFiles();
  // Keyed by the path relative to web/src, which is how NATIVE_TRANSPORTS
  // names a file.
  const analyses = new Map<string, FileAnalysis>();
  const calledSymbols = new Set<string>();
  let sources: ParsedSources | undefined;

  beforeAll(() => {
    // One compiler session for the whole file: parseFiles starts the tsgo
    // process the repo's own `tsc --noEmit` uses, and afterAll shuts it down.
    sources = parseFiles(files, webDir);
    for (const file of files) {
      const sourceFile = sources.get(file);
      // Not a soft failure: a file the program does not have is a file this
      // guard did not read, and silently skipping it is how a scanner starts
      // passing for the wrong reason.
      if (!sourceFile) throw new Error(`not in the TypeScript program: ${file}`);
      const analysis = analyzeSourceFile(sourceFile, (specifier) =>
        isGeneratedClientSpecifier(specifier, file, webSrcDir),
      );
      analyses.set(path.relative(webSrcDir, file).split(path.sep).join("/"), analysis);
      for (const symbol of analysis.calledSymbols) calledSymbols.add(symbol);
    }
    // The default hook timeout is 5 s and a cold program build is a second
    // or two of it; this leaves room for a loaded CI box without hiding a
    // hang.
  }, 60_000);

  afterAll(() => {
    sources?.dispose();
  });

  it(`still describes exactly ${ROUTE_COUNT} routes in api/openapi.json`, () => {
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
    // NATIVE_TRANSPORTS) and GET /api/stream/stats (D15: process-wide
    // streaming health, agent-only), 55 -> 57; +1 for P6b's
    // POST /api/workspaces/{id}/endpoints/preview (decisions.md
    // mocker-p6b-sse-mock D13: a stream draft's first frames, agent-only),
    // 57 -> 58.
    // This count is OPERATIONS (method + path), not `paths` keys — a
    // 48-to-51 edit that instead counted paths would silently undercount.
    expect(routes).toHaveLength(ROUTE_COUNT);
  });

  it("every route declares an operationId", () => {
    // A missing operationId would make requestSymbols() derive its names
    // from undefined and turn a real, correctly-covered route red — pushing
    // whoever fixes it toward inventing a hook call instead of naming the
    // operation in the contract, where the fix belongs.
    const missing = routes
      .filter((route) => !route.operationId)
      .map((route) => `${route.method} ${route.urlPath}`);
    expect(missing).toEqual([]);
  });

  it("every native transport still opens the route its manifest entry claims", () => {
    // Without this the manifest would be a promise. With it, moving the
    // EventSource call to another file, or changing the URL it builds, turns
    // the entry red HERE — naming the file — instead of turning the route it
    // covers red somewhere else.
    const broken: string[] = [];
    for (const entry of NATIVE_TRANSPORTS) {
      const [, urlPath] = entry.route.split(" ", 2);
      if (!routes.some((route) => `${route.method} ${route.urlPath}` === entry.route)) {
        broken.push(`${entry.route}: not a route in api/openapi.json`);
        continue;
      }
      const analysis = analyses.get(entry.file);
      if (!analysis) {
        broken.push(`${entry.route}: ${entry.file} is not a scannable file`);
        continue;
      }
      if (!analysis.eventSourceTargets.some((target) => eventSourceCovers(target, urlPath ?? ""))) {
        broken.push(`${entry.route}: ${entry.file} opens no EventSource on it`);
      }
    }
    expect(broken, `native transports that no longer hold:\n${broken.join("\n")}`).toEqual([]);
  });

  it("every non-exempt route is called from some screen under web/src", () => {
    const nativelyCovered = new Set(NATIVE_TRANSPORTS.map((entry) => entry.route));
    const uncovered: string[] = [];
    for (const route of routes) {
      const key = `${route.method} ${route.urlPath}`;
      if (key in EXEMPT) continue;
      if (nativelyCovered.has(key)) continue; // proved by the test above
      if (!route.operationId) continue; // reported by the assertion above
      // The symbols that count, and the two that stopped counting on
      // 2026-09-07, are requestSymbols' subject in coverageScanner.ts.
      if (!operationIsCalled(route.operationId, calledSymbols)) uncovered.push(key);
    }
    expect(uncovered, `routes with no caller under web/src:\n${uncovered.join("\n")}`).toEqual([]);
  });
});

import { afterAll, beforeAll, describe, expect, it } from "vitest";
import {
  analyzeSourceFile,
  eventSourceCovers,
  isGeneratedClientSpecifier,
  operationIsCalled,
  parseSources,
  requestSymbols,
  type FileAnalysis,
  type ParsedSources,
} from "./coverageScanner";

// The fixtures are the reason coverageScanner.ts is a module and not a
// helper inside coverage.test.ts. Each source below is a shape the previous,
// substring-based scanner answered WRONGLY, written out in full so the
// answer is checked rather than argued about; they are held in memory (the
// compiler is given a virtual filesystem) so that none of them is itself a
// file under web/src, which coverage.test.ts would then scan.
//
// The operation used throughout is listTraffic — GET
// /api/workspaces/{id}/traffic — because every orval shape exists for it:
// the fetcher, the hook, the query options and the query key.

const DIR = "/fixtures";

const FIXTURES: Record<string, string> = {
  // 1. THE NAME IN A COMMENT. The old scanner searched concatenated text, so
  // a route could be "covered" by prose describing it.
  [`${DIR}/commentOnly.ts`]: [
    "// useListTraffic( is what TrafficPage.tsx calls; this file does not.",
    "/* getListTrafficQueryOptions( appears here too, in a block comment. */",
    "export const note = 'useListTraffic( inside a string literal';",
    "export const zero = 0;",
  ].join("\n"),

  // 2. IMPORTED, NEVER CALLED. The old scanner required a `(` after the
  // name, which an import list does not have — but an import followed by a
  // call in a comment, or by `useListTraffic()` in dead prose, did fool it.
  // Here the import stands alone: a binding is not a request.
  [`${DIR}/importOnly.ts`]: [
    'import { useListTraffic } from "@/api/generated/traffic/traffic.ts";',
    "export const hook = typeof useListTraffic;",
  ].join("\n"),

  // 3. THE QUERY KEY ONLY. DriftPanel.tsx legitimately does this for a
  // route it does not read: it invalidates someone else's cache entry.
  // Building the address of a request is not making one.
  [`${DIR}/queryKeyOnly.ts`]: [
    'import { getListTrafficQueryKey } from "@/api/generated/traffic/traffic.ts";',
    "export const key = getListTrafficQueryKey(1);",
  ].join("\n"),

  // 4. AN ALIASED IMPORT THAT IS CALLED. The false RED: the generated name
  // never appears at a call site, so the old scanner saw no caller at all.
  [`${DIR}/aliased.tsx`]: [
    'import { useListTraffic as useTraffic } from "@/api/generated/traffic/traffic.ts";',
    "export const Panel = () => {",
    "  const { data } = useTraffic(1);",
    "  return <div>{data ? 'да' : 'нет'}</div>;",
    "};",
  ].join("\n"),

  // 5. A CALL INSIDE A FUNCTION NOTHING CALLS. Counted — on purpose. See
  // the assertion below: reachability has a different owner.
  [`${DIR}/dead.ts`]: [
    'import { getListTrafficQueryOptions } from "@/api/generated/traffic/traffic.ts";',
    "function neverExportedNeverCalled() {",
    "  return getListTrafficQueryOptions(1);",
    "}",
    "export const unrelated = 1;",
  ].join("\n"),

  // Two more shapes the tree could grow tomorrow, pinned now: a namespace
  // import, and a type-only import that must never count.
  [`${DIR}/namespace.ts`]: [
    'import * as traffic from "@/api/generated/traffic/traffic.ts";',
    "export const load = () => traffic.listTraffic(1);",
  ].join("\n"),

  [`${DIR}/typeOnly.ts`]: [
    'import type { useListTraffic } from "@/api/generated/traffic/traffic.ts";',
    "export type Hook = typeof useListTraffic;",
  ].join("\n"),

  // A same-named local from somewhere else is not the generated client: the
  // module specifier decides, not the identifier.
  [`${DIR}/foreignModule.ts`]: [
    'import { useListTraffic } from "@/hooks/useListTraffic.ts";',
    "export const rows = useListTraffic(1);",
  ].join("\n"),

  // The native transport, in the shape api/stream.ts writes it.
  [`${DIR}/stream.ts`]: [
    "export const open = (id: number, since: number) =>",
    "  new EventSource(`/api/workspaces/${id}/traffic/stream?since=${since}`);",
  ].join("\n"),
};

describe("coverageScanner", () => {
  const analyses = new Map<string, FileAnalysis>();
  let sources: ParsedSources | undefined;

  beforeAll(() => {
    sources = parseSources(FIXTURES);
    for (const file of Object.keys(FIXTURES)) {
      const sourceFile = sources.get(file);
      if (!sourceFile) throw new Error(`fixture did not parse: ${file}`);
      analyses.set(
        file,
        analyzeSourceFile(sourceFile, (specifier) =>
          isGeneratedClientSpecifier(specifier, file, "/fixtures/src"),
        ),
      );
    }
  }, 60_000);

  afterAll(() => {
    sources?.dispose();
  });

  function called(fixture: string): ReadonlySet<string> {
    const analysis = analyses.get(`${DIR}/${fixture}`);
    if (!analysis) throw new Error(`no analysis for ${fixture}`);
    return analysis.calledSymbols;
  }

  it("does not count a name that appears only in a comment or a string", () => {
    expect([...called("commentOnly.ts")]).toEqual([]);
    expect(operationIsCalled("listTraffic", called("commentOnly.ts"))).toBe(false);
  });

  it("does not count a binding that is imported and never called", () => {
    expect([...called("importOnly.ts")]).toEqual([]);
    expect(operationIsCalled("listTraffic", called("importOnly.ts"))).toBe(false);
  });

  it("does not let a query-key call cover the route it addresses", () => {
    // The call IS seen — it is a real call of a real generated symbol — it
    // just is not one of requestSymbols: no request leaves the browser.
    expect([...called("queryKeyOnly.ts")]).toEqual(["getListTrafficQueryKey"]);
    expect(operationIsCalled("listTraffic", called("queryKeyOnly.ts"))).toBe(false);
  });

  it("counts an aliased import that is called, under the exported name", () => {
    expect([...called("aliased.tsx")]).toEqual(["useListTraffic"]);
    expect(operationIsCalled("listTraffic", called("aliased.tsx"))).toBe(true);
  });

  it("counts a call inside a function nothing calls — reachability is not this scanner's question", () => {
    // Deliberate, and the same answer the substring scanner gave. Proving a
    // call site is reachable needs the route tree, which
    // web/src/routes/routes.test.tsx mounts; a scanner that guessed at it
    // here would fail for reasons it could not explain, and the failure
    // would land on the contract rather than on the dead code.
    expect([...called("dead.ts")]).toEqual(["getListTrafficQueryOptions"]);
    expect(operationIsCalled("listTraffic", called("dead.ts"))).toBe(true);
  });

  it("counts a namespace import through its member call", () => {
    expect([...called("namespace.ts")]).toEqual(["listTraffic"]);
    expect(operationIsCalled("listTraffic", called("namespace.ts"))).toBe(true);
  });

  it("ignores a type-only import", () => {
    expect([...called("typeOnly.ts")]).toEqual([]);
  });

  it("ignores a same-named symbol from a module that is not the generated client", () => {
    expect([...called("foreignModule.ts")]).toEqual([]);
  });

  it("reads the EventSource target as the route it covers", () => {
    const analysis = analyses.get(`${DIR}/stream.ts`);
    expect(analysis?.eventSourceTargets).toHaveLength(1);
    const target = analysis?.eventSourceTargets[0] ?? "";
    expect(eventSourceCovers(target, "/api/workspaces/{id}/traffic/stream")).toBe(true);
    // The parameter is one segment, and the path is not a prefix match: a
    // longer route that merely starts the same way is a different route.
    expect(eventSourceCovers(target, "/api/workspaces/{id}/traffic")).toBe(false);
  });

  it("names the four symbols whose call is a request", () => {
    // Pinned because dropping one silently widens the guard and adding one
    // silently narrows it — the failure the query-key rule was.
    expect(requestSymbols("listTraffic")).toEqual([
      "listTraffic",
      "useListTraffic",
      "getListTrafficQueryOptions",
      "getListTrafficMutationOptions",
    ]);
    expect(requestSymbols("listTraffic")).not.toContain("getListTrafficQueryKey");
    expect(requestSymbols("listTraffic")).not.toContain("getListTrafficUrl");
  });

  it("recognises a generated module by specifier, aliased or relative", () => {
    const src = "/web/src";
    expect(
      isGeneratedClientSpecifier("@/api/generated/traffic/traffic.ts", `${src}/a.ts`, src),
    ).toBe(true);
    expect(
      isGeneratedClientSpecifier("./generated/traffic/traffic.ts", `${src}/api/a.ts`, src),
    ).toBe(true);
    expect(isGeneratedClientSpecifier("@/api/client", `${src}/a.ts`, src)).toBe(false);
    expect(isGeneratedClientSpecifier("@tanstack/react-query", `${src}/a.ts`, src)).toBe(false);
  });
});

// coverageScanner.ts answers ONE question for coverage.test.ts: which
// symbols of the orval-generated client (web/src/api/generated/**) does the
// application actually CALL, and which URLs does it hand to a native
// transport. It exists as its own module because the answer has to be
// verifiable on fixtures — coverageScanner.test.ts feeds it the five source
// shapes the previous, substring-based scanner got wrong — and a helper
// buried inside the guard it feeds cannot itself be put under test.
//
// WHY AN AST AND NOT A SUBSTRING SCAN. Until this file, coverage.test.ts
// concatenated every .ts/.tsx under web/src and asked whether the string
// `useListTraffic(` occurred anywhere in it. That answer is wrong in both
// directions:
//   * false GREEN — the name occurs inside a comment, inside a string, or
//     inside a function nothing ever calls, and the route counts as covered;
//     `getXQueryKey(` counted too, and a query key performs no request at
//     all (it is the cache address of one).
//   * false RED — `import { useListTraffic as useTraffic }` is a legal alias
//     and the concatenated text then never contains the generated name.
// A parser has none of those failure modes: comments and string literals are
// not expressions, an alias is an ImportSpecifier with a propertyName, and a
// call is a CallExpression or it is nothing.
//
// WHY typescript/unstable/*. TypeScript 7 is the native port: the npm
// package's main entry is a version stub, `ts.createSourceFile` is gone, and
// the only parser it still ships is the one behind the API in
// `typescript/unstable/sync` — a real Program, produced by the tsgo binary
// the repo's own `tsc --noEmit` already runs, decoded here into the AST node
// objects of `typescript/unstable/ast`. The import path says "unstable" and
// means it; if a TypeScript upgrade moves it, this file is where that breaks,
// which is the reason the scanner is not spread across the test.
//
// WHAT IT DELIBERATELY DOES NOT DO: it never asks the type checker. Resolving
// a callee to its declaration would settle aliasing, shadowing and re-export
// barrels in one call — and would make this guard depend on
// web/src/api/generated/** EXISTING, which is gitignored and absent until
// `make ui-gen` has run. coverage.test.ts's whole point is that it reads the
// COMMITTED contract and passes or fails identically on a fresh clone, so
// module specifiers are matched by path, syntactically, and the limitations
// that leaves are listed on isGeneratedClientSpecifier and analyzeSourceFile.

import path from "node:path";
import {
  isCallExpression,
  isIdentifier,
  isImportDeclaration,
  isNamedImports,
  isNamespaceImport,
  isNewExpression,
  isNoSubstitutionTemplateLiteral,
  isPropertyAccessExpression,
  isStringLiteral,
  isTemplateExpression,
  SyntaxKind,
} from "typescript/unstable/ast";
import type { Node, SourceFile } from "typescript/unstable/ast";
import { createVirtualFileSystem } from "typescript/unstable/fs";
import { API } from "typescript/unstable/sync";

/** What one source file was found to do with the generated client. */
export interface FileAnalysis {
  /** Exported names of api/generated modules this file CALLS. */
  readonly calledSymbols: ReadonlySet<string>;
  /**
   * URL shapes this file passes to `new EventSource(...)`, with every `${…}`
   * substitution collapsed to PARAM — see routeShape.
   */
  readonly eventSourceTargets: readonly string[];
}

/**
 * PARAM stands in for one interpolated path segment. A NUL can appear in
 * neither an OpenAPI path template nor a URL a screen builds, so a shape
 * built with it can only ever match another shape built with it — no
 * escaping, and no chance of a `{id}` in the contract colliding with real
 * text.
 */
export const PARAM = "\u0000";

/**
 * routeShape turns a contract path (`/api/workspaces/{id}/traffic/stream`)
 * into the shape a template literal in a screen produces
 * (`/api/workspaces/${id}/traffic/stream`). Comparing shapes is what lets a
 * native transport be recognised without the screen being written around the
 * guard: the screen writes the URL the way React code writes URLs.
 */
export function routeShape(urlPath: string): string {
  return urlPath.replace(/\{[^}]*\}/g, PARAM);
}

/**
 * eventSourceCovers reports whether one `new EventSource(...)` target is the
 * contract route `urlPath`. A query string is allowed after the path (the
 * traffic feed passes `?since=`), nothing else is.
 */
export function eventSourceCovers(target: string, urlPath: string): boolean {
  const shape = routeShape(urlPath);
  return target === shape || target.startsWith(`${shape}?`);
}

// X is the operationId with its first letter capitalised and nothing else
// changed — verified against every symbol orval actually emits (see
// p1e-context.md §3): no operationId in this contract contains an acronym,
// underscore or hyphen, so a word-splitting PascalCase conversion would be
// solving a problem this contract doesn't have.
function capitalize(operationId: string): string {
  return operationId.charAt(0).toUpperCase() + operationId.slice(1);
}

/**
 * requestSymbols lists the generated symbols whose CALL issues the
 * operation's request. A screen may legitimately use any of them —
 * web/src/auth/session.ts calls getGetMeQueryOptions directly and never
 * useGetMe.
 *
 * Two differences from the list coverage.test.ts accepted before 2026-09-07,
 * and neither moves a single route: `get<X>QueryKey` is GONE (it builds the
 * cache key of a request, it does not make one — a screen that only ever
 * invalidates a query has not covered the route it invalidates), and the
 * bare fetcher `<operationId>` is NEW (orval emits it, and calling it is the
 * request in its most direct form). Measured across all 70 routes on
 * 2026-09-07: no route was covered only by a query key, and none is covered
 * only by a bare fetcher — the two edits were made together so that the day
 * they stop agreeing, the diff says which rule moved.
 *
 * `get<X>Url` is deliberately absent: it returns a string.
 */
export function requestSymbols(operationId: string): string[] {
  const capitalised = capitalize(operationId);
  return [
    operationId,
    `use${capitalised}`,
    `get${capitalised}QueryOptions`,
    `get${capitalised}MutationOptions`,
  ];
}

/** operationIsCalled asks requestSymbols' question of one scan's result. */
export function operationIsCalled(
  operationId: string,
  calledSymbols: ReadonlySet<string>,
): boolean {
  return requestSymbols(operationId).some((symbol) => calledSymbols.has(symbol));
}

/**
 * isGeneratedClientSpecifier decides, from the module specifier alone,
 * whether an import reaches into web/src/api/generated. Both forms in the
 * tree are accepted: the `@/` alias every screen uses today, and a relative
 * path (api/client.ts's neighbours could write one).
 *
 * KNOWN LIMITATION: an intermediate module that re-exports a generated
 * symbol (`export { useListTraffic } from "@/api/generated/..."`) is not
 * followed, so a screen importing the hook FROM that barrel reads as
 * uncovered. There is no such barrel today — every import in web/src points
 * straight at `@/api/generated/...` — and the failure direction is a red
 * test naming the route, not a silent pass.
 */
export function isGeneratedClientSpecifier(
  specifier: string,
  containingFile: string,
  webSrcDir: string,
): boolean {
  const generatedDir = path.resolve(webSrcDir, "api/generated");
  if (specifier.startsWith("@/")) {
    return isUnder(path.resolve(webSrcDir, specifier.slice(2)), generatedDir);
  }
  if (specifier.startsWith(".")) {
    return isUnder(path.resolve(path.dirname(containingFile), specifier), generatedDir);
  }
  return false;
}

function isUnder(target: string, dir: string): boolean {
  const rel = path.relative(dir, target);
  return rel === "" || (!rel.startsWith("..") && !path.isAbsolute(rel));
}

/**
 * analyzeSourceFile walks one parsed file and reports what it calls.
 *
 * The rules, each of them a case the substring scanner got wrong:
 *   * a binding counts only if it was IMPORTED from a generated module —
 *     `isGenerated` is asked about the module specifier, so an identifier
 *     that merely shares a name with a hook is not a caller;
 *   * an alias resolves to the name the generated module exports
 *     (`import { useListTraffic as useTraffic }` → `useListTraffic`);
 *   * `import type { … }` and a `type` specifier inside a value import are
 *     dropped: a type can never issue a request;
 *   * a namespace import counts through its member access (`api.useX(…)`);
 *   * the binding must be the callee of a CallExpression. A mention in a
 *     comment or a string is not even a node, an import with no call site is
 *     an ImportSpecifier and nothing else, and a value passed by reference
 *     (`mutationFn: listTraffic`) is an Identifier in an argument position —
 *     none of them count. That last one is PARITY with the substring
 *     scanner, which required a `(` after the name for the same reason.
 *
 * KNOWN LIMITATIONS, all deliberate:
 *   * the by-reference rule above cuts the OTHER way too: a route whose only
 *     caller is `queryFn: listFoo` would read as UNCOVERED (a false red, not
 *     a false green). No such site exists today; the day one appears, the
 *     guard fails loudly and the rule is widened here, not worked around.
 *   * a call inside a function nothing ever calls still counts. Proving a
 *     call site is REACHABLE is a different question with a different owner
 *     — web/src/routes/routes.test.tsx mounts the real route tree — and
 *     guessing at it here would make this guard fail for reasons it cannot
 *     explain.
 *   * a local binding that SHADOWS an imported name is read as the import.
 *     Settling it needs the checker, and the checker needs the generated
 *     client on disk (see this file's header).
 */
export function analyzeSourceFile(
  sourceFile: SourceFile,
  isGenerated: (specifier: string) => boolean,
): FileAnalysis {
  // local binding name -> the name the generated module exports.
  const named = new Map<string, string>();
  // locals bound by `import * as ns from "…generated…"`.
  const namespaces = new Set<string>();

  for (const statement of sourceFile.statements) {
    if (!isImportDeclaration(statement)) continue;
    const specifier = statement.moduleSpecifier;
    if (!isStringLiteral(specifier) || !isGenerated(specifier.text)) continue;
    const clause = statement.importClause;
    // `import "…"` (no clause) and `import type { … }` bring in nothing
    // callable. TypeScript 7 spells the second one as a phase modifier
    // rather than the isTypeOnly flag of TS 5 — the same node also carries
    // `defer`, which IS a value import, so the modifier is compared and not
    // merely tested for presence.
    if (!clause || clause.phaseModifier === SyntaxKind.TypeKeyword) continue;
    const bindings = clause.namedBindings;
    if (!bindings) continue;
    if (isNamespaceImport(bindings)) {
      namespaces.add(bindings.name.text);
      continue;
    }
    if (!isNamedImports(bindings)) continue;
    for (const element of bindings.elements) {
      if (element.isTypeOnly) continue;
      named.set(element.name.text, (element.propertyName ?? element.name).text);
    }
  }

  const calledSymbols = new Set<string>();
  const eventSourceTargets: string[] = [];

  walk(sourceFile, (node) => {
    if (isCallExpression(node)) {
      const callee = node.expression;
      if (isIdentifier(callee)) {
        const exported = named.get(callee.text);
        if (exported !== undefined) calledSymbols.add(exported);
        return;
      }
      if (
        isPropertyAccessExpression(callee) &&
        isIdentifier(callee.expression) &&
        namespaces.has(callee.expression.text) &&
        isIdentifier(callee.name)
      ) {
        calledSymbols.add(callee.name.text);
      }
      return;
    }
    if (
      isNewExpression(node) &&
      isIdentifier(node.expression) &&
      node.expression.text === "EventSource"
    ) {
      const target = urlShapeOf(node.arguments?.[0]);
      if (target !== undefined) eventSourceTargets.push(target);
    }
  });

  return { calledSymbols, eventSourceTargets };
}

/**
 * urlShapeOf reads the first argument of a transport call as a URL shape:
 * a plain string, or a template literal whose substitutions each become
 * PARAM. Anything else (a variable, a concatenation) has no shape a contract
 * path can be compared against and answers undefined — the manifest in
 * coverage.test.ts then fails loudly rather than counting a route it cannot
 * see.
 */
function urlShapeOf(argument: Node | undefined): string | undefined {
  if (!argument) return undefined;
  if (isStringLiteral(argument) || isNoSubstitutionTemplateLiteral(argument)) {
    return argument.text;
  }
  if (isTemplateExpression(argument)) {
    let shape = argument.head.text;
    for (const span of argument.templateSpans) shape += PARAM + span.literal.text;
    return shape;
  }
  return undefined;
}

function walk(node: Node, visit: (node: Node) => void): void {
  visit(node);
  node.forEachChild((child) => {
    walk(child, visit);
    return undefined;
  });
}

/** A parsed view of a set of source files; dispose() releases the compiler. */
export interface ParsedSources {
  /** The parsed file, or undefined when it is not in the program. */
  get(fileName: string): SourceFile | undefined;
  dispose(): void;
}

/**
 * parseFiles parses real files off disk. `cwd` is the package root the
 * compiler resolves a tsconfig from; the files are opened individually, so a
 * tree where `make ui-gen` has never run (no api/generated, no
 * routeTree.gen.ts) still parses — unresolved imports are a SEMANTIC
 * complaint and nothing here asks for semantics.
 */
export function parseFiles(fileNames: readonly string[], cwd: string): ParsedSources {
  return open(fileNames, { cwd });
}

/**
 * parseSources parses sources held in memory: keys are absolute-looking file
 * names, values their text. It is how coverageScanner.test.ts states a
 * fixture as a string instead of committing a file that would then also be
 * scanned by the guard this module feeds.
 */
export function parseSources(files: Readonly<Record<string, string>>): ParsedSources {
  return open(Object.keys(files), {
    cwd: path.dirname(Object.keys(files)[0] ?? "/virtual/x.ts"),
    fs: createVirtualFileSystem({ ...files }),
  });
}

function open(
  fileNames: readonly string[],
  options: ConstructorParameters<typeof API>[0],
): ParsedSources {
  const api = new API(options);
  const snapshot = api.updateSnapshot({ openFiles: [...fileNames] });
  return {
    get(fileName) {
      return snapshot.getDefaultProjectForFile(fileName)?.program.getSourceFile(fileName);
    },
    dispose() {
      // The API holds a child tsgo process. A test file that forgets this
      // leaves it running and vitest never exits — the same discipline the
      // Go side buys with goleak.
      api.close();
    },
  };
}

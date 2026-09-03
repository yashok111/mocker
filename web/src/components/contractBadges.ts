// P7b (decisions.md mocker-p7-api-design D12): the «Контракт» tab's badges —
// base / added / changed / removed against the bound spec — computed on the
// CLIENT from two routes the tab reads beside the export
// (GET .../operations, GET .../endpoints), never from an `x-mocker-*`
// marker in the document: the OpenAPI a backend team receives stays plain
// (the owner's own call), and the drift report's inverse — "what did we
// design" — is a question about this workspace's layers, which those two
// routes already answer. Pure functions, no React, so a test pins each rule.

import type { EndpointView, MergedOperationView } from "@/api/generated/schemas";

export type BadgeKind = "base" | "added" | "changed" | "removed";

/** Where an operation's editor lives: the custom-endpoints screen with the row
 * opened, or the spec-operations screen with the operation selected. */
export type EditorLink =
  | { screen: "endpoints"; endpointId: number }
  | { screen: "operations"; opKey: string };

export interface OperationBadge {
  kind: BadgeKind;
  link?: EditorLink;
}

/** The Russian label of a badge — §14's word rule: «база», «добавлено»,
 * «изменено», «удалено»; never a wire word. */
export const BADGE_LABEL: Record<BadgeKind, string> = {
  base: "база",
  added: "добавлено",
  changed: "изменено",
  removed: "удалено",
};

export const BADGE_COLOR: Record<BadgeKind, string> = {
  base: "gray",
  added: "green",
  changed: "yellow",
  removed: "red",
};

/** router.CanonicalPath's rule, restated for the client: every `{name}`
 * segment becomes `{}`, so `/users/{id}` and `/users/{userId}` are one shape
 * — the same rule the server applies when a custom row REPLACES a base
 * operation (§8 rule 3, read as intent by the export). */
export function canonicalShape(method: string, path: string): string {
  const canonical = path
    .split("/")
    .map((seg) => (seg.length > 2 && seg.startsWith("{") && seg.endsWith("}") ? "{}" : seg))
    .join("/");
  return `${method.toUpperCase()} ${canonical}`;
}

/** The literal key the exported document spells an operation under. */
export function literalKey(method: string, path: string): string {
  return `${method.toUpperCase()} ${path}`;
}

function overrideChangesTheShape(op: MergedOperationView): boolean {
  const override = op.override;
  if (override === undefined || !override.overrideOn) {
    return false;
  }
  return Object.values(override.responses).some(
    (variant) => variant.hasSchemaPatch || variant.mode === "pinned",
  );
}

/**
 * computeBadges maps each operation the document MAY carry, by its literal
 * `METHOD path`, to a badge:
 *
 * - a custom row with `overrideOn` whose canonical shape no spec operation
 *   has → added; at a spec operation's shape → changed (it replaced that
 *   operation); `routeOff` on either → removed;
 * - a spec operation whose override is on and carries a patched schema or
 *   a pinned response → changed; `routeOff` → removed;
 * - everything else → base (the caller's default for a key not in the map).
 *
 * A custom row's verdict wins over a spec operation's at the same literal
 * key, because the export writes the custom row there (rule 3).
 */
export function computeBadges(
  operations: readonly MergedOperationView[],
  endpoints: readonly EndpointView[],
): Map<string, OperationBadge> {
  const out = new Map<string, OperationBadge>();
  const specShapes = new Set(operations.map((op) => canonicalShape(op.method, op.path)));

  for (const op of operations) {
    const override = op.override;
    if (override === undefined || !override.overrideOn) {
      continue;
    }
    const link: EditorLink = { screen: "operations", opKey: op.opKey };
    if (override.routeOff) {
      out.set(literalKey(op.method, op.path), { kind: "removed", link });
    } else if (overrideChangesTheShape(op)) {
      out.set(literalKey(op.method, op.path), { kind: "changed", link });
    }
  }

  for (const ep of endpoints) {
    if (!ep.overrideOn) {
      continue; // omitted from the document: a switched-off row is no contract
    }
    const link: EditorLink = { screen: "endpoints", endpointId: ep.id };
    const shadowsSpec = specShapes.has(canonicalShape(ep.method, ep.path));
    const kind: BadgeKind = ep.routeOff ? "removed" : shadowsSpec ? "changed" : "added";
    out.set(literalKey(ep.method, ep.path), { kind, link });
  }
  return out;
}

export function badgeFor(
  badges: Map<string, OperationBadge>,
  method: string,
  path: string,
): OperationBadge {
  return badges.get(literalKey(method, path)) ?? { kind: "base" };
}

const HTTP_METHODS = ["get", "put", "post", "delete", "options", "head", "patch", "trace"] as const;

export interface DocOperation {
  path: string;
  method: string;
  operation: Record<string, unknown>;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Every operation of an OpenAPI document, paths sorted, methods in the
 * specification's own order; a path item's `parameters`, `summary` and `x-`
 * keys are never read as methods. */
export function docOperations(doc: unknown): DocOperation[] {
  if (!isObject(doc) || !isObject(doc["paths"])) {
    return [];
  }
  const paths = doc["paths"];
  const out: DocOperation[] = [];
  for (const path of Object.keys(paths).sort()) {
    const item = paths[path];
    if (!isObject(item)) {
      continue;
    }
    for (const method of HTTP_METHODS) {
      const op = item[method];
      if (isObject(op)) {
        out.push({ path, method: method.toUpperCase(), operation: op });
      }
    }
  }
  return out;
}

export function countBadges(
  ops: readonly DocOperation[],
  badges: Map<string, OperationBadge>,
): Record<BadgeKind, number> {
  const counts: Record<BadgeKind, number> = { base: 0, added: 0, changed: 0, removed: 0 };
  for (const op of ops) {
    counts[badgeFor(badges, op.method, op.path).kind] += 1;
  }
  return counts;
}

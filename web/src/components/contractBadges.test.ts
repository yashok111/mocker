// P7b B3: the badge rules, one assertion each, on fixtures — a custom row at
// a NEW shape is «добавлено», one at a spec operation's shape is «изменено»
// (never «добавлено»), a patched or pinned override is «изменено», routeOff
// on either is «удалено», a plain base operation is «база», and the counts
// over a document equal the fixture's.

import { describe, expect, it } from "vitest";
import {
  badgeFor,
  canonicalShape,
  computeBadges,
  countBadges,
  docOperations,
} from "./contractBadges";
import {
  endpointViewFixture,
  mergedOperationViewFixture,
  opOverrideSummaryViewFixture,
} from "@/test/fixtures";

describe("canonicalShape", () => {
  it("folds every parameter segment to {} so two spellings are one shape", () => {
    expect(canonicalShape("get", "/users/{id}")).toBe("GET /users/{}");
    expect(canonicalShape("GET", "/users/{userId}")).toBe("GET /users/{}");
    expect(canonicalShape("GET", "/users")).toBe("GET /users");
  });
});

describe("computeBadges", () => {
  const specOp = mergedOperationViewFixture({
    method: "GET",
    path: "/users/{id}",
    opKey: "GET%20%2Fusers%2F%7Bid%7D",
    override: undefined,
  });
  const listOp = mergedOperationViewFixture({
    method: "GET",
    path: "/users",
    opKey: "GET%20%2Fusers",
    override: undefined,
  });

  it("a custom row at a new shape is added; at a spec shape it is changed", () => {
    const badges = computeBadges(
      [specOp, listOp],
      [
        endpointViewFixture({ id: 1, method: "GET", path: "/things" }),
        endpointViewFixture({ id: 2, method: "GET", path: "/users/{userId}" }),
      ],
    );
    expect(badgeFor(badges, "GET", "/things")).toEqual({
      kind: "added",
      link: { screen: "endpoints", endpointId: 1 },
    });
    expect(badgeFor(badges, "GET", "/users/{userId}").kind).toBe("changed");
    expect(badgeFor(badges, "GET", "/users").kind).toBe("base");
  });

  it("an override with a patched schema or a pinned response is changed, routeOff is removed", () => {
    const patched = mergedOperationViewFixture({
      method: "GET",
      path: "/users",
      opKey: "GET%20%2Fusers",
      override: opOverrideSummaryViewFixture({
        overrideOn: true,
        responses: { "200": { mode: "generated", recipeCount: 0, hasSchemaPatch: true } },
      }),
    });
    const pinned = mergedOperationViewFixture({
      method: "GET",
      path: "/users/{id}",
      opKey: "GET%20%2Fusers%2F%7Bid%7D",
      override: opOverrideSummaryViewFixture({
        overrideOn: true,
        responses: { "200": { mode: "pinned", recipeCount: 0, hasSchemaPatch: false } },
      }),
    });
    const off = mergedOperationViewFixture({
      method: "DELETE",
      path: "/users/{id}",
      opKey: "DELETE%20%2Fusers%2F%7Bid%7D",
      override: opOverrideSummaryViewFixture({ overrideOn: true, routeOff: true }),
    });
    const recipesOnly = mergedOperationViewFixture({
      method: "POST",
      path: "/users",
      opKey: "POST%20%2Fusers",
      override: opOverrideSummaryViewFixture({
        overrideOn: true,
        responses: { "201": { mode: "generated", recipeCount: 3, hasSchemaPatch: false } },
      }),
    });
    const badges = computeBadges([patched, pinned, off, recipesOnly], []);
    expect(badgeFor(badges, "GET", "/users")).toEqual({
      kind: "changed",
      link: { screen: "operations", opKey: "GET%20%2Fusers" },
    });
    expect(badgeFor(badges, "GET", "/users/{id}").kind).toBe("changed");
    expect(badgeFor(badges, "DELETE", "/users/{id}").kind).toBe("removed");
    // A recipe-only override changes values, not the SHAPE: base.
    expect(badgeFor(badges, "POST", "/users").kind).toBe("base");
  });

  it("a switched-off override or row is base / absent, and a routeOff row is removed", () => {
    const badges = computeBadges(
      [
        mergedOperationViewFixture({
          method: "GET",
          path: "/users",
          opKey: "GET%20%2Fusers",
          override: opOverrideSummaryViewFixture({ overrideOn: false, routeOff: true }),
        }),
      ],
      [
        endpointViewFixture({ id: 3, method: "GET", path: "/hidden", overrideOn: false }),
        endpointViewFixture({ id: 4, method: "GET", path: "/retired", routeOff: true }),
      ],
    );
    expect(badgeFor(badges, "GET", "/users").kind).toBe("base");
    expect(badgeFor(badges, "GET", "/hidden").kind).toBe("base");
    expect(badgeFor(badges, "GET", "/retired").kind).toBe("removed");
  });
});

describe("docOperations + countBadges", () => {
  it("walks the document's methods only and counts each badge once per operation", () => {
    const doc = {
      openapi: "3.1.0",
      paths: {
        "/users": {
          parameters: [{ name: "x", in: "query" }],
          get: { responses: {} },
          post: { responses: {} },
        },
        "/things": { get: { responses: {} }, "x-note": true },
      },
    };
    const ops = docOperations(doc);
    expect(ops.map((o) => `${o.method} ${o.path}`)).toEqual([
      "GET /things",
      "GET /users",
      "POST /users",
    ]);
    const badges = computeBadges(
      [
        mergedOperationViewFixture({
          method: "GET",
          path: "/users",
          opKey: "k",
          override: undefined,
        }),
      ],
      [endpointViewFixture({ id: 1, method: "GET", path: "/things" })],
    );
    expect(countBadges(ops, badges)).toEqual({ base: 2, added: 1, changed: 0, removed: 0 });
    expect(docOperations(null)).toEqual([]);
  });
});

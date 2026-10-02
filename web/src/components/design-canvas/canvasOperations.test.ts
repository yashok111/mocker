// @vitest-environment node
import { describe, expect, it } from "vitest";
import { listCanvasOperations } from "./canvasOperations";

describe("canvas Path Item operations", () => {
  it("keeps two aliases distinct and merges chained parameters with nearest and operation overrides", () => {
    const document = {
      paths: {
        "/one/{id}": {
          $ref: "#/components/pathItems/Alias",
          "x-mocker-canvas-operation-ids": { get: "one" },
        },
        "/two/{id}": {
          $ref: "#/components/pathItems/Alias",
          "x-mocker-canvas-operation-ids": { get: "two" },
        },
      },
      components: {
        pathItems: {
          Alias: {
            $ref: "#/components/pathItems/Shared",
            parameters: [{ in: "query", name: "mode", schema: { type: "string" } }],
          },
          Shared: {
            parameters: [
              { in: "query", name: "mode", schema: { type: "integer" } },
              { in: "header", name: "x-id", schema: { type: "string" } },
            ],
            get: {
              "x-mocker-canvas-operation-id": "source",
              parameters: [{ in: "query", name: "mode", schema: { type: "boolean" } }],
              responses: { "200": { description: "ok" } },
            },
          },
        },
      },
    };
    const operations = listCanvasOperations(document);
    expect(operations.map(({ location, key }) => [location.path, key])).toEqual([
      ["/one/{id}", "one"],
      ["/two/{id}", "two"],
    ]);
    expect(operations[0]!.operation.parameters).toEqual([
      { in: "query", name: "mode", schema: { type: "boolean" } },
      { in: "header", name: "x-id", schema: { type: "string" } },
    ]);
    expect(document.components.pathItems.Shared.get["x-mocker-canvas-operation-id"]).toBe("source");
  });

  it("keeps valid siblings when references are broken and stops cycles", () => {
    const document = {
      paths: {
        "/broken": {
          $ref: "#/components/pathItems/Missing",
          post: { "x-mocker-canvas-operation-id": "own" },
        },
        "/cycle": { $ref: "#/components/pathItems/Cycle" },
        "/external": { $ref: "https://example.test/spec#/Item" },
        "/malformed": { $ref: "#/components/schemas/LooksLikeOperation" },
      },
      components: {
        pathItems: { Cycle: { $ref: "#/paths/~1cycle" } },
        schemas: {
          LooksLikeOperation: { type: "object", get: { "x-mocker-canvas-operation-id": "wrong" } },
        },
      },
    };
    expect(listCanvasOperations(document).map(({ key }) => key)).toEqual(["own"]);
  });

  it("uses the source key only when the consumer has no method entry", () => {
    const document = {
      paths: {
        "/legacy": { $ref: "#/components/pathItems/Shared" },
        "/invalid": {
          $ref: "#/components/pathItems/Shared",
          "x-mocker-canvas-operation-ids": { get: null },
        },
      },
      components: { pathItems: { Shared: { get: { "x-mocker-canvas-operation-id": "source" } } } },
    };
    expect(listCanvasOperations(document).map(({ key }) => key)).toEqual(["source", null]);
  });

  it("does not fall back when the alias container is present but malformed", () => {
    const document = {
      paths: {
        "/number": { $ref: "#/components/pathItems/Shared", "x-mocker-canvas-operation-ids": 42 },
        "/null": { $ref: "#/components/pathItems/Shared", "x-mocker-canvas-operation-ids": null },
      },
      components: { pathItems: { Shared: { get: { "x-mocker-canvas-operation-id": "source" } } } },
    };
    expect(listCanvasOperations(document).map(({ key }) => key)).toEqual([null, null]);
  });

  it("uses a literal sibling identity and decodes escaped pointer tokens", () => {
    const document = {
      paths: {
        "/alias": {
          $ref: "#/components/pathItems/A~1B~0C",
          "x-mocker-canvas-operation-ids": { get: "unused", post: "post-alias" },
          get: {
            "x-mocker-canvas-operation-id": "own",
            responses: { "201": { description: "own" } },
          },
        },
      },
      components: {
        pathItems: {
          "A/B~C": {
            get: { "x-mocker-canvas-operation-id": "source" },
            post: { "x-mocker-canvas-operation-id": "shared-post" },
          },
        },
      },
    };
    expect(
      listCanvasOperations(document).map(({ location, key }) => [location.method, key]),
    ).toEqual([
      ["get", "own"],
      ["post", "post-alias"],
    ]);
  });

  it("follows a local pointer through an array index", () => {
    const document = {
      paths: {
        "/alias": { $ref: "#/x-items/0", "x-mocker-canvas-operation-ids": { get: "alias" } },
      },
      "x-items": [{ get: { responses: { "200": { description: "ok" } } } }],
    };
    expect(listCanvasOperations(document).map(({ key }) => key)).toEqual(["alias"]);
  });

  it("decodes percent and tilde escapes like the shared resolver", () => {
    const document = {
      paths: {
        "/percent": {
          $ref: "#/components/pathItems/A%7E1B",
          "x-mocker-canvas-operation-ids": { get: "percent" },
        },
        "/literal-tilde": {
          $ref: "#/components/pathItems/A~2B",
          "x-mocker-canvas-operation-ids": { get: "tilde" },
        },
      },
      components: { pathItems: { "A/B": { get: {} }, "A~2B": { get: {} } } },
    };
    expect(listCanvasOperations(document).map(({ key }) => key)).toEqual(["percent", "tilde"]);
  });
});

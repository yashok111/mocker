import { describe, expect, it } from "vitest";
import { inheritOperation, parameterValue, validateMergedReferences } from "./canvasContractMerge";
import type { ConversionIssue } from "./canvasContractConversion";
import type { ApiDocument } from "../api-designer/documentModel";

describe("parameter reference inheritance", () => {
  it("resolves reference chains and overrides only the same name and location", () => {
    const document: ApiDocument = {
      components: {
        parameters: {
          PathAlias: { $ref: "#/components/parameters/Path" },
          Path: { name: "id", in: "query", required: true },
          OpAlias: { $ref: "#/components/parameters/Op" },
          Op: { name: "id", in: "query", required: false },
        },
      },
    };
    const override = { $ref: "#/components/parameters/OpAlias" };
    const header = { name: "id", in: "header" };
    const operation: Record<string, unknown> = { parameters: [override] };
    inheritOperation(
      document,
      { parameters: [{ $ref: "#/components/parameters/PathAlias" }, header] },
      operation,
    );
    expect(operation.parameters).toEqual([override, header]);
    expect(parameterValue(document, override)).toEqual({
      name: "id",
      in: "query",
      required: false,
    });
  });

  it("terminates cycles and preserves parameters whose identity cannot be resolved", () => {
    const document: ApiDocument = {
      components: {
        parameters: {
          A: { $ref: "#/components/parameters/B" },
          B: { $ref: "#/components/parameters/A" },
        },
      },
    };
    const cyclic = { $ref: "#/components/parameters/A" };
    expect(parameterValue(document, cyclic)).toBeUndefined();
    expect(parameterValue(document, { $ref: "#/components/parameters/missing" })).toBeUndefined();
    expect(parameterValue(document, { $ref: "https://example.test/parameters" })).toBeUndefined();
    const inherited = [{ description: "unknown inherited" }, cyclic];
    const local = [{ description: "unknown local" }, { $ref: "#/components/parameters/missing" }];
    const operation: Record<string, unknown> = { parameters: local };
    inheritOperation(document, { parameters: inherited }, operation);
    expect(operation.parameters).toEqual([...inherited, ...local]);
  });
});

describe("authored operation locations", () => {
  it("ignores operation-shaped extension payloads in Paths and Callback Objects", () => {
    const payload = { post: { operationId: "a" } };
    const document: ApiDocument = {
      paths: {
        "/a": { post: { operationId: "a", callbacks: { notify: { "x-payload": payload } } } },
        "x-payload": payload,
      },
      components: { callbacks: { Reusable: { "x-payload": payload } } },
    };
    const errors: ConversionIssue[] = [];
    validateMergedReferences(document, errors);
    expect(errors).toEqual([]);
  });

  it.each(["pathItems", "callbacks", "webhooks", "inline callback"])(
    "counts real x-prefixed names in %s maps",
    (location) => {
      const pathItem = { post: { operationId: "a" } };
      const callback = { "{$request.query.callbackUrl}": pathItem };
      const operation: Record<string, unknown> = { operationId: "a" };
      const document: ApiDocument = { paths: { "/a": { post: operation } } };
      if (location === "inline callback") operation.callbacks = { "x-notify": callback };
      else if (location === "webhooks") document.webhooks = { "x-event": pathItem };
      else
        document.components = {
          [location]: { "x-event": location === "callbacks" ? callback : pathItem },
        };
      const errors: ConversionIssue[] = [];
      validateMergedReferences(document, errors);
      expect(errors.some(({ message }) => message.includes("operationId a"))).toBe(true);
    },
  );
});

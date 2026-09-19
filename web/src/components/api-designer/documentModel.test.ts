import { describe, expect, it } from "vitest";
import {
  copyOperation,
  createOperation,
  createSchema,
  deleteOperation,
  escapeJsonPointerToken,
  getAtJsonPointer,
  listOperations,
  renameOperation,
  renameSchema,
  schemaUsage,
  setAtJsonPointer,
  type ApiDocument,
} from "./documentModel";

const DOCUMENT: ApiDocument = {
  openapi: "3.1.0",
  info: { title: "Orders", version: "1.0.0", "x-info-note": "keep" },
  "x-root-note": { owner: "analytics" },
  paths: {
    "/orders/{id}": {
      "x-path-note": true,
      parameters: [{ in: "header", name: "x-tenant", schema: { type: "string" } }],
      get: {
        operationId: "getOrder",
        "x-operation-note": "keep",
        parameters: [
          { in: "path", name: "id", required: true, schema: { type: "string" } },
          { in: "query", name: "expand", schema: { type: "boolean" } },
        ],
        responses: {
          "200": {
            description: "OK",
            content: {
              "application/json": {
                schema: { $ref: "#/components/schemas/Order" },
                example: { id: "o-1" },
              },
              "application/problem+json": { schema: { type: "object" } },
            },
          },
          "404": { description: "Missing", "x-response-note": "keep" },
        },
      },
    },
  },
  components: {
    schemas: {
      Order: {
        type: "object",
        required: ["id"],
        properties: {
          id: { type: "string" },
          parent: { $ref: "#/components/schemas/Order" },
        },
        allOf: [{ "x-composition-note": true }],
      },
      OrderPage: {
        type: "array",
        items: { $ref: "#/components/schemas/Order" },
      },
    },
    securitySchemes: { bearer: { type: "http", scheme: "bearer" } },
  },
};

describe("JSON pointer helpers", () => {
  it("escapes pointer tokens and immutably writes only the addressed field", () => {
    expect(escapeJsonPointerToken("a/b~c")).toBe("a~1b~0c");
    const next = setAtJsonPointer(DOCUMENT, "/info/title", "Orders API");

    expect(getAtJsonPointer(next, "/info/title")).toBe("Orders API");
    expect(getAtJsonPointer(next, "/x-root-note/owner")).toBe("analytics");
    expect(DOCUMENT).not.toBe(next);
    expect(DOCUMENT.info).not.toBe(next.info);
    expect(DOCUMENT.paths).toBe(next.paths);
  });
});

describe("operation helpers", () => {
  it("preserves inherited required headers and explicit overrides when moved to another path", () => {
    const input = setAtJsonPointer(
      setAtJsonPointer(DOCUMENT, "/paths/~1orders~1{id}/parameters", [
        { in: "header", name: "tenant", required: true, schema: { type: "string" } },
        { $ref: "#/components/parameters/Locale" },
      ]),
      "/components/parameters/Locale",
      { in: "query", name: "locale", required: true, schema: { type: "string" } },
    );
    const destination = setAtJsonPointer(input, "/paths/~1purchases~1{purchaseId}/parameters", [
      { in: "path", name: "purchaseId", required: true, schema: { type: "integer" } },
    ]);
    for (const move of [renameOperation, copyOperation]) {
      const result = move(
        destination,
        { path: "/orders/{id}", method: "get" },
        { path: "/purchases/{purchaseId}", method: "get" },
      );
      expect(getAtJsonPointer(result, "/paths/~1purchases~1{purchaseId}/get/parameters")).toEqual([
        { in: "path", name: "purchaseId", required: true, schema: { type: "string" } },
        { in: "query", name: "expand", schema: { type: "boolean" } },
        { in: "header", name: "tenant", required: true, schema: { type: "string" } },
        { $ref: "#/components/parameters/Locale" },
      ]);
    }
  });
  it("lists methods in path order and preserves path-item fields while moving an operation", () => {
    expect(listOperations(DOCUMENT)).toEqual([{ path: "/orders/{id}", method: "get" }]);

    const next = renameOperation(
      DOCUMENT,
      { path: "/orders/{id}", method: "get" },
      { path: "/orders/{orderId}", method: "post" },
    );
    expect(getAtJsonPointer(next, "/paths/~1orders~1{orderId}/post/operationId")).toBe("getOrder");
    expect(getAtJsonPointer(next, "/paths/~1orders~1{orderId}/post/parameters/0")).toEqual({
      in: "path",
      name: "orderId",
      required: true,
      schema: { type: "string" },
    });
    expect(getAtJsonPointer(next, "/paths/~1orders~1{orderId}/post/parameters/1/name")).toBe(
      "expand",
    );
    expect(getAtJsonPointer(next, "/paths/~1orders~1{id}/x-path-note")).toBe(true);
    expect(getAtJsonPointer(next, "/paths/~1orders~1{id}/parameters/0/name")).toBe("x-tenant");
    expect(getAtJsonPointer(next, "/paths/~1orders~1{id}/get")).toBeUndefined();
    expect(DOCUMENT.paths).not.toBe(next.paths);
  });

  it("creates, copies and deletes without losing unrelated statuses or media types", () => {
    const created = createOperation(DOCUMENT, { path: "/orders", method: "post" });
    expect(getAtJsonPointer(created, "/paths/~1orders/post/responses/default/description")).toBe(
      "Ответ",
    );

    const copied = copyOperation(
      created,
      { path: "/orders/{id}", method: "get" },
      { path: "/orders/{id}", method: "head" },
    );
    expect(getAtJsonPointer(copied, "/paths/~1orders~1{id}/head/responses/404/description")).toBe(
      "Missing",
    );
    expect(
      getAtJsonPointer(
        copied,
        "/paths/~1orders~1{id}/head/responses/200/content/application~1problem+json/schema/type",
      ),
    ).toBe("object");

    const removed = deleteOperation(copied, { path: "/orders", method: "post" });
    expect(getAtJsonPointer(removed, "/paths/~1orders")).toBeUndefined();
    expect(getAtJsonPointer(removed, "/x-root-note/owner")).toBe("analytics");
  });

  it("creates required path parameter rows for every target placeholder", () => {
    const next = createOperation(DOCUMENT, {
      path: "/customers/{customerId}/orders/{orderId}",
      method: "get",
    });

    expect(
      getAtJsonPointer(next, "/paths/~1customers~1{customerId}~1orders~1{orderId}/get/parameters"),
    ).toEqual([
      { name: "customerId", in: "path", required: true, schema: { type: "string" } },
      { name: "orderId", in: "path", required: true, schema: { type: "string" } },
    ]);
  });

  it("refuses to overwrite an existing target operation", () => {
    const withPost = createOperation(DOCUMENT, { path: "/orders/{id}", method: "post" });
    expect(() =>
      renameOperation(
        withPost,
        { path: "/orders/{id}", method: "get" },
        { path: "/orders/{id}", method: "post" },
      ),
    ).toThrow("Операция POST /orders/{id} уже существует");
  });
});

describe("schema helpers", () => {
  it("rewrites descendant and URI-encoded references without matching similarly named schemas", () => {
    let input = setAtJsonPointer(DOCUMENT, "/components/schemas/Field", {
      $ref: "#/components/schemas/Order/properties/id",
    });
    input = setAtJsonPointer(input, "/components/schemas/Encoded", {
      $ref: "#/components/schemas/%4Frder/properties/id",
    });
    input = setAtJsonPointer(input, "/components/schemas/Other", {
      $ref: "#/components/schemas/OrderPage/items",
    });
    const usages = schemaUsage(input, "Order");
    expect(usages).toContain("/components/schemas/Field/$ref");
    expect(usages).toContain("/components/schemas/Encoded/$ref");
    const next = renameSchema(input, "Order", "Purchase/Item~");
    expect(getAtJsonPointer(next, "/components/schemas/Field/$ref")).toBe(
      "#/components/schemas/Purchase~1Item~0/properties/id",
    );
    expect(getAtJsonPointer(next, "/components/schemas/Encoded/$ref")).toBe(
      "#/components/schemas/Purchase~1Item~0/properties/id",
    );
    expect(getAtJsonPointer(next, "/components/schemas/Other/$ref")).toBe(
      "#/components/schemas/OrderPage/items",
    );
  });
  it("renames a schema and every exact local reference in one immutable result", () => {
    const next = renameSchema(DOCUMENT, "Order", "Purchase");

    expect(getAtJsonPointer(next, "/components/schemas/Order")).toBeUndefined();
    expect(getAtJsonPointer(next, "/components/schemas/Purchase/required/0")).toBe("id");
    expect(
      getAtJsonPointer(
        next,
        "/paths/~1orders~1{id}/get/responses/200/content/application~1json/schema/$ref",
      ),
    ).toBe("#/components/schemas/Purchase");
    expect(getAtJsonPointer(next, "/components/schemas/Purchase/properties/parent/$ref")).toBe(
      "#/components/schemas/Purchase",
    );
    expect(getAtJsonPointer(next, "/components/schemas/OrderPage/items/$ref")).toBe(
      "#/components/schemas/Purchase",
    );
    expect(getAtJsonPointer(next, "/components/schemas/Purchase/allOf/0/x-composition-note")).toBe(
      true,
    );
    expect(getAtJsonPointer(DOCUMENT, "/components/schemas/Order")).toBeDefined();
  });

  it("reports escaped usage pointers and refuses a conflicting rename", () => {
    expect(schemaUsage(DOCUMENT, "Order")).toEqual([
      "/paths/~1orders~1{id}/get/responses/200/content/application~1json/schema/$ref",
      "/components/schemas/Order/properties/parent/$ref",
      "/components/schemas/OrderPage/items/$ref",
    ]);
    expect(() => renameSchema(DOCUMENT, "Order", "OrderPage")).toThrow(
      'Схема "OrderPage" уже существует',
    );
    const withBoolean = setAtJsonPointer(DOCUMENT, "/components/schemas/Forbidden", false);
    expect(() => renameSchema(withBoolean, "Order", "Forbidden")).toThrow(
      'Схема "Forbidden" уже существует',
    );
    expect(() => createSchema(withBoolean, "Forbidden")).toThrow(
      'Схема "Forbidden" уже существует',
    );
  });

  it("creates an object schema without replacing existing component siblings", () => {
    const next = createSchema(DOCUMENT, "Customer");
    expect(getAtJsonPointer(next, "/components/schemas/Customer")).toEqual({
      type: "object",
      properties: {},
    });
    expect(getAtJsonPointer(next, "/components/securitySchemes/bearer/scheme")).toBe("bearer");
  });
});

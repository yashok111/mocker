import { describe, expect, it } from "vitest";
import { getOperation, type ApiDocument } from "../api-designer/documentModel";
import { buildCanvasContract, previewCanvasContract } from "./canvasContractConversion";
import { OPERATION_KEY, emptyCanvas } from "./canvasModel";
import type { CanvasDocument, CanvasMessage } from "./types";

function request(id: string, label: string, extra: Partial<CanvasMessage> = {}): CanvasMessage {
  return { id, label, fromId: "client", toId: "api", kind: "request", description: "", ...extra };
}
function response(id: string, replyToId: string, label: string): CanvasMessage {
  return request(id, label, { fromId: "api", toId: "client", kind: "response", replyToId });
}
function fixture(): CanvasDocument {
  return {
    ...emptyCanvas(),
    participants: [
      { id: "client", name: "Клиент", kind: "client", description: "" },
      { id: "api", name: "Backend", kind: "service", description: "" },
      { id: "db", name: "БД", kind: "database", description: "" },
    ],
  };
}
function jwtFixture(): CanvasDocument {
  const document = fixture();
  document.contracts = [
    {
      id: "jwt",
      name: "JWT",
      mode: "linked",
      source: { designId: 1, revisionId: 2, version: 1 },
      document: {
        openapi: "3.1.0",
        info: { title: "JWT", version: "2.0.0", license: { name: "MIT" } },
        servers: [{ url: "https://api.example.com" }],
        security: [{ bearerAuth: [] }],
        paths: {
          "/auth/login": {
            post: {
              [OPERATION_KEY]: "login",
              operationId: "login",
              security: [],
              requestBody: {
                content: {
                  "application/json": { schema: { $ref: "#/components/schemas/LoginRequest" } },
                },
              },
              responses: {
                "200": {
                  description: "Token",
                  content: {
                    "application/json": { schema: { $ref: "#/components/schemas/TokenResponse" } },
                  },
                },
                "400": { $ref: "#/components/responses/Error" },
                "401": { $ref: "#/components/responses/Error" },
                "429": { $ref: "#/components/responses/Error" },
              },
            },
          },
          "/users/me": {
            get: {
              [OPERATION_KEY]: "profile",
              operationId: "profile",
              responses: {
                "200": {
                  description: "Profile",
                  content: {
                    "application/json": { schema: { $ref: "#/components/schemas/UserProfile" } },
                  },
                },
                "401": { $ref: "#/components/responses/Error" },
              },
            },
          },
        },
        components: {
          schemas: {
            LoginRequest: { type: "object" },
            TokenResponse: { type: "object" },
            UserProfile: { type: "object" },
            Error: { type: "object" },
          },
          responses: {
            Error: {
              description: "Error",
              content: { "application/json": { schema: { $ref: "#/components/schemas/Error" } } },
            },
          },
          securitySchemes: { bearerAuth: { type: "http", scheme: "bearer", bearerFormat: "JWT" } },
        },
      },
    },
  ];
  document.messages = [
    request("login", "Войти", { operation: { contractId: "jwt", operationKey: "login" } }),
    request("profile", "Профиль", { operation: { contractId: "jwt", operationKey: "profile" } }),
    request("again", "Ещё профиль", { operation: { contractId: "jwt", operationKey: "profile" } }),
    response("ok", "login", "200 OK"),
    response("expired", "again", "401 Unauthorized"),
  ];
  return document;
}
function build(document: CanvasDocument) {
  return buildCanvasContract(
    document,
    previewCanvasContract(document).rows,
    "Новый API",
    "new-api",
  );
}
function operation(document: ApiDocument, method: string, path: string) {
  return getOperation(document, { method, path })!;
}

describe("whole-canvas API conversion", () => {
  it("preserves callback extension payloads without treating them as operations", () => {
    const document = jwtFixture();
    const source = document.contracts[0]!.document;
    const payload = { post: { operationId: "login" } };
    source.components = {
      ...(source.components as object),
      callbacks: { "x-reusable": { "x-payload": payload } },
    };
    operation(source, "post", "/auth/login").callbacks = {
      notify: { "x-payload": payload },
    };
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(operation(result.contract!.document, "post", "/auth/login").callbacks).toEqual({
      notify: { "x-payload": payload },
    });
    expect((result.contract!.document.components as Record<string, unknown>).callbacks).toEqual({
      "x-reusable": { "x-payload": payload },
    });
  });

  it("preserves distinct inherited parameters behind local reference chains", () => {
    const document = jwtFixture();
    const api = document.contracts[0]!.document;
    api.components = {
      ...(api.components as object),
      parameters: {
        AliasA: { $ref: "#/components/parameters/ActualA" },
        ActualA: { name: "a", in: "query", required: true, schema: { type: "string" } },
        AliasB: { $ref: "#/components/parameters/ActualB" },
        ActualB: { name: "b", in: "query", schema: { type: "integer" } },
      },
    };
    const paths = api.paths as Record<string, Record<string, unknown>>;
    const inherited = { $ref: "#/components/parameters/AliasA" };
    const local = { $ref: "#/components/parameters/AliasB" };
    paths["/auth/login"]!.parameters = [inherited];
    operation(api, "post", "/auth/login").parameters = [local];
    const before = structuredClone(document);
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(operation(result.contract!.document, "post", "/auth/login").parameters).toEqual([
      inherited,
      local,
    ]);
    expect(document).toEqual(before);
  });

  it.each(["callback", "nested callback", "webhook", "component path item", "component callback"])(
    "rejects duplicate operation IDs in retained %s operations",
    (kind) => {
      const document = fixture();
      for (const id of ["a", "b"]) {
        const callbackPath = "{$request.query.callbackUrl}";
        const shared = {
          post: { operationId: "onEvent", responses: { "200": { description: "ok" } } },
        };
        const api: ApiDocument = {
          openapi: "3.1.0",
          info: { title: id, version: "1" },
          paths: {
            [`/${id}`]: {
              post: {
                [OPERATION_KEY]: id,
                operationId: id,
                responses: { "200": { description: "ok" } },
              },
            },
          },
        };
        if (kind === "callback" || kind === "nested callback") {
          const pathItem =
            kind === "callback"
              ? shared
              : {
                  post: {
                    operationId: `outer-${id}`,
                    responses: { "200": { description: "ok" } },
                    callbacks: { inner: { [callbackPath]: shared } },
                  },
                };
          operation(api, "post", `/${id}`).callbacks = { notify: { [callbackPath]: pathItem } };
        } else if (kind === "webhook") api.webhooks = { [id]: shared };
        else if (kind === "component path item") api.components = { pathItems: { [id]: shared } };
        else api.components = { callbacks: { [id]: { [callbackPath]: shared } } };
        document.contracts.push({ id, name: id, document: api });
        document.messages.push(
          request(id, `POST /${id}`, { operation: { contractId: id, operationKey: id } }),
        );
      }
      const result = build(document);
      expect(result.errors.some(({ message }) => message.includes("operationId onEvent"))).toBe(
        true,
      );
      expect(result.contract).toBeNull();
      expect(result.bindings).toEqual([]);
    },
  );

  it("does not count operation-shaped literal examples as authored operations", () => {
    const document = jwtFixture();
    const payload = {
      paths: { "/example": { post: { operationId: "login" } } },
      post: { operationId: "profile" },
    };
    const login = operation(document.contracts[0]!.document, "post", "/auth/login");
    login.responses = {
      "200": {
        description: "ok",
        content: {
          "application/json": {
            example: payload,
            examples: { named: { value: payload } },
            schema: {
              type: "object",
              default: payload,
              const: payload,
              enum: [payload],
              examples: [payload],
              properties: {
                post: { type: "object", properties: { operationId: { type: "string" } } },
              },
            },
          },
        },
      },
    };
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(operation(result.contract!.document, "post", "/auth/login").responses).toEqual(
      login.responses,
    );
  });

  it("groups repeated JWT calls and preserves complete contracts, auth and source metadata without mutation", () => {
    const document = jwtFixture();
    const original = structuredClone(document);
    const preview = previewCanvasContract(document);
    expect(preview.rows).toHaveLength(2);
    expect(preview.rows[1]!.messageIds).toEqual(["profile", "again"]);
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(result.contract).toMatchObject({ id: "new-api", mode: "copy", name: "Новый API" });
    expect(result.contract).not.toHaveProperty("source");
    const api = result.contract!.document;
    expect(Object.keys(api.paths as object)).toEqual(["/auth/login", "/users/me"]);
    expect(Object.keys(operation(api, "post", "/auth/login").responses as object)).toEqual([
      "200",
      "400",
      "401",
      "429",
    ]);
    expect(Object.keys(operation(api, "get", "/users/me").responses as object)).toEqual([
      "200",
      "401",
    ]);
    expect(operation(api, "post", "/auth/login").security).toEqual([]);
    expect(operation(api, "get", "/users/me").security).toEqual([{ bearerAuth: [] }]);
    expect(operation(api, "get", "/users/me").servers).toEqual([
      { url: "https://api.example.com" },
    ]);
    expect(api.components).toEqual(document.contracts[0]!.document.components);
    expect(api.info).toEqual({ title: "Новый API", version: "2.0.0", license: { name: "MIT" } });
    expect(result.bindings).toHaveLength(3);
    expect(result.bindings[1]!.operationKey).toEqual(result.bindings[2]!.operationKey);
    expect(document).toEqual(original);
  });

  it("deduplicates explicit endpoints and associates only replyTo responses, skipping internal calls", () => {
    const document = fixture();
    document.messages = [
      request("one", "GET /items"),
      request("two", "GET /items"),
      request("db", "SELECT items", { toId: "db" }),
      request("self", "GET /internal", { fromId: "api" }),
      request("event", "POST /events", { kind: "event" }),
      response("ok", "one", "200 OK"),
      response("fail", "two", "404 Not found"),
      response("unrelated", "db", "500 Error"),
    ];
    const preview = previewCanvasContract(document);
    expect(preview.rows).toHaveLength(1);
    expect(preview.rows[0]).toMatchObject({
      method: "GET",
      path: "/items",
      messageIds: ["one", "two"],
      responses: [
        { messageId: "ok", status: "200" },
        { messageId: "fail", status: "404" },
      ],
    });
    expect(preview.skippedCount).toBe(3);
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(operation(result.contract!.document, "get", "/items").responses).toEqual({
      "200": { description: "200 OK" },
      "404": { description: "404 Not found" },
    });
  });

  it("requires unknown method, path and response status until edited or excluded", () => {
    const document = fixture();
    document.messages = [
      request("unknown", "Получить пользователя"),
      response("answer", "unknown", "Пользователь найден"),
    ];
    const rows = previewCanvasContract(document).rows;
    expect(rows[0]).toMatchObject({ method: "", path: "", responses: [{ status: "" }] });
    expect(buildCanvasContract(document, rows, "API", "new").errors.length).toBeGreaterThanOrEqual(
      3,
    );
    rows[0]!.method = "GET";
    rows[0]!.path = "/users";
    rows[0]!.responses[0]!.status = "200";
    expect(buildCanvasContract(document, rows, "API", "new").errors).toEqual([]);
    rows[0]!.included = false;
    expect(buildCanvasContract(document, rows, "API", "new").contract).toBeNull();
  });

  it("uses a documented default response rather than guessing success or JSON schemas", () => {
    const document = fixture();
    document.messages = [request("one", "POST /items")];
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(operation(result.contract!.document, "post", "/items").responses).toMatchObject({
      default: { description: expect.any(String) },
    });
    expect(result.warnings.length).toBeGreaterThan(0);
  });

  it("merges compatible components while preserving each source's effective auth and servers", () => {
    const document = jwtFixture();
    document.contracts.push({
      id: "public",
      name: "Public",
      document: {
        openapi: "3.1.0",
        info: { title: "Public", version: "1.0.0" },
        servers: [{ url: "https://public.example.com" }],
        components: { schemas: { Error: { type: "object" }, Item: { type: "string" } } },
        paths: {
          "/items": {
            get: {
              [OPERATION_KEY]: "items",
              responses: {
                "200": {
                  description: "Items",
                  content: {
                    "application/json": { schema: { $ref: "#/components/schemas/Item" } },
                  },
                },
              },
            },
          },
        },
      },
    });
    document.messages.push(
      request("items", "GET /items", {
        operation: { contractId: "public", operationKey: "items" },
      }),
    );
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(result.contract!.document).not.toHaveProperty("security");
    expect(result.contract!.document).not.toHaveProperty("servers");
    expect(operation(result.contract!.document, "get", "/items").security).toBeUndefined();
    expect(operation(result.contract!.document, "get", "/items").servers).toEqual([
      { url: "https://public.example.com" },
    ]);
    expect(result.contract!.document.components).toMatchObject({
      schemas: { Item: { type: "string" }, Error: { type: "object" } },
      securitySchemes: { bearerAuth: { scheme: "bearer" } },
    });
  });

  it("rejects incompatible schemas rather than silently selecting one definition", () => {
    const document = jwtFixture();
    document.contracts.push({
      id: "conflicting",
      name: "Other API",
      document: {
        openapi: "3.1.0",
        info: { title: "Other", version: "1.0.0" },
        components: { schemas: { Error: { type: "string" } } },
        paths: {
          "/other": {
            get: { [OPERATION_KEY]: "other", responses: { "200": { description: "ok" } } },
          },
        },
      },
    });
    document.messages.push(
      request("other", "GET /other", {
        operation: { contractId: "conflicting", operationKey: "other" },
      }),
    );
    const result = build(document);
    expect(result.contract).toBeNull();
    expect(result.bindings).toEqual([]);
    expect(
      result.errors.some(
        ({ message }) => message.includes("Error") && message.includes("components"),
      ),
    ).toBe(true);
  });

  it("blocks unresolved local refs, auth names and references to omitted operations", () => {
    for (const property of ["schema", "operation", "security"]) {
      const document = jwtFixture();
      const login = operation(document.contracts[0]!.document, "post", "/auth/login");
      if (property === "schema") login.requestBody = { $ref: "#/components/requestBodies/Missing" };
      else if (property === "operation")
        login.responses = {
          "200": {
            description: "ok",
            links: { missing: { operationRef: "#/paths/~1omitted/get" } },
          },
        };
      else login.security = [{ MissingAuth: [] }];
      const result = build(document);
      expect(result.contract, property).toBeNull();
      expect(result.errors.length, property).toBeGreaterThan(0);
    }
  });

  it("retains path parameters, operation overrides and path-level server precedence", () => {
    const document = fixture();
    document.contracts = [
      {
        id: "path-api",
        name: "Path",
        document: {
          openapi: "3.1.0",
          info: { title: "Path", version: "1.0.0" },
          servers: [{ url: "https://root" }],
          components: {
            parameters: {
              ID: { name: "id", in: "path", required: true, schema: { type: "integer" } },
            },
          },
          paths: {
            "/items/{id}": {
              summary: "Items",
              servers: [{ url: "https://path" }],
              parameters: [
                { $ref: "#/components/parameters/ID" },
                { name: "X-Header", in: "header", schema: { type: "string" } },
              ],
              get: {
                [OPERATION_KEY]: "get",
                parameters: [
                  { name: "X-Header", in: "header", required: true, schema: { type: "integer" } },
                ],
                responses: { "200": { description: "ok" } },
              },
            },
          },
        },
      },
    ];
    document.messages = [
      request("one", "GET /items/{id}", {
        operation: { contractId: "path-api", operationKey: "get" },
      }),
    ];
    const result = build(document);
    expect(result.errors).toEqual([]);
    const get = operation(result.contract!.document, "get", "/items/{id}");
    expect(get.servers).toEqual([{ url: "https://path" }]);
    expect(get.parameters).toEqual([
      { $ref: "#/components/parameters/ID" },
      { name: "X-Header", in: "header", required: true, schema: { type: "integer" } },
    ]);
    expect(result.contract!.document.paths).toMatchObject({ "/items/{id}": { summary: "Items" } });
  });

  it("blocks endpoint collisions across receivers and operation ID collisions", () => {
    const document = fixture();
    document.participants.push({
      id: "api2",
      name: "Another API",
      kind: "service",
      description: "",
    });
    document.messages = [
      request("one", "GET /items"),
      request("two", "GET /items", { toId: "api2" }),
    ];
    expect(previewCanvasContract(document).rows).toHaveLength(2);
    expect(build(document).contract).toBeNull();
    const jwt = jwtFixture();
    operation(jwt.contracts[0]!.document, "get", "/users/me").operationId = "login";
    const collision = build(jwt);
    expect(collision.contract).toBeNull();
    expect(collision.errors.some(({ message }) => message.includes("operationId"))).toBe(true);
  });

  it("adds required path parameter drafts and explains the unconfirmed string type", () => {
    const document = fixture();
    document.messages = [request("one", "GET /items/{id}")];
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(operation(result.contract!.document, "get", "/items/{id}").parameters).toEqual([
      { name: "id", in: "path", required: true, schema: { type: "string" } },
    ]);
    expect(
      result.warnings.some(({ message }) => message.includes("id") && message.includes("string")),
    ).toBe(true);
  });

  it("rejects broken bindings, duplicate row identities and malformed paths", () => {
    const document = fixture();
    document.messages = [
      request("one", "GET /items", { operation: { contractId: "missing", operationKey: "gone" } }),
    ];
    expect(build(document).contract).toBeNull();
    document.messages[0]!.operation = undefined;
    const rows = previewCanvasContract(document).rows;
    expect(
      buildCanvasContract(document, [...rows, { ...rows[0]!, path: "/other" }], "API", "new")
        .contract,
    ).toBeNull();
    for (const path of [
      "/items/{",
      "/items/}",
      "/items/{id}/{id}",
      "/items?query=1",
      "https://example.com",
    ]) {
      expect(
        buildCanvasContract(document, [{ ...rows[0]!, path }], "API", "new").contract,
        path,
      ).toBeNull();
    }
  });

  it("checks default-response refs while leaving literal example payloads untouched", () => {
    const document = jwtFixture();
    const login = operation(document.contracts[0]!.document, "post", "/auth/login");
    login.responses = { default: { $ref: "#/components/responses/Missing" } };
    expect(build(document).contract).toBeNull();
    login.responses = {
      default: {
        description: "ok",
        content: {
          "application/json": {
            example: { $ref: "#/customer-payload", security: [{ notAuth: [] }] },
            schema: { type: "object", properties: { default: { type: "string" } } },
          },
        },
      },
    };
    expect(build(document).errors).toEqual([]);
  });

  it("preserves inherited callback and webhook auth when combining source documents", () => {
    const document = jwtFixture();
    const callback = { post: { responses: { "200": { description: "accepted" } } } };
    const source = document.contracts[0]!.document;
    source.webhooks = { notified: structuredClone(callback) };
    operation(source, "post", "/auth/login").callbacks = {
      notify: { "{$request.body#/callbackUrl}": structuredClone(callback) },
    };
    document.messages.push(request("public", "GET /public"));
    const result = build(document);
    expect(result.errors).toEqual([]);
    const api = result.contract!.document;
    expect(api.security).toBeUndefined();
    expect(api.webhooks).toMatchObject({
      notified: {
        post: { security: [{ bearerAuth: [] }], servers: [{ url: "https://api.example.com" }] },
      },
    });
    expect(operation(api, "post", "/auth/login").callbacks).toMatchObject({
      notify: {
        "{$request.body#/callbackUrl}": {
          post: { security: [{ bearerAuth: [] }], servers: [{ url: "https://api.example.com" }] },
        },
      },
    });
    expect(operation(api, "get", "/public").security).toBeUndefined();
  });

  it("deduplicates initially ambiguous calls after editing their endpoint and rebinds all replies", () => {
    const document = fixture();
    document.messages = [
      request("one", "Получить профиль"),
      request("two", "Повторить получение"),
      response("ok", "one", "200 OK"),
      response("expired", "two", "401 Expired"),
    ];
    const rows = previewCanvasContract(document).rows;
    expect(rows).toHaveLength(2);
    rows[0]!.method = "GET";
    rows[0]!.path = "/users/me";
    rows[1]!.method = "get";
    rows[1]!.path = " /users/me ";
    const unchangedRows = structuredClone(rows);
    const result = buildCanvasContract(document, rows, "Auth", "new");
    expect(result.errors).toEqual([]);
    expect(Object.keys(result.contract!.document.paths as object)).toEqual(["/users/me"]);
    expect(
      Object.keys(operation(result.contract!.document, "get", "/users/me").responses as object),
    ).toEqual(["200", "401"]);
    expect(result.bindings).toEqual([
      { messageId: "one", operationKey: "new:one" },
      { messageId: "two", operationKey: "new:one" },
    ]);
    expect(rows).toEqual(unchangedRows);
  });

  it("blocks a local path ref that would silently point into another source's operation", () => {
    const document = fixture();
    const target = (type: string) => ({
      get: {
        [OPERATION_KEY]: type,
        responses: {
          "200": { description: "ok", content: { "application/json": { schema: { type } } } },
        },
      },
    });
    document.contracts = [
      {
        id: "a",
        name: "A",
        document: {
          openapi: "3.1.0",
          info: { title: "A", version: "1" },
          paths: {
            "/users": target("integer"),
            "/create": {
              post: {
                [OPERATION_KEY]: "create",
                requestBody: {
                  content: {
                    "application/json": {
                      schema: {
                        $ref: "#/paths/~1users/get/responses/200/content/application~1json/schema",
                      },
                    },
                  },
                },
                responses: { "201": { description: "created" } },
              },
            },
          },
        },
      },
      {
        id: "b",
        name: "B",
        document: {
          openapi: "3.1.0",
          info: { title: "B", version: "1" },
          paths: { "/users": target("string") },
        },
      },
    ];
    document.messages = [
      request("create", "POST /create", { operation: { contractId: "a", operationKey: "create" } }),
      request("users", "GET /users", { operation: { contractId: "b", operationKey: "string" } }),
    ];
    const result = build(document);
    expect(result.contract).toBeNull();
    expect(
      result.errors.some(
        ({ message }) => message.includes("#/paths/~1users") && message.includes("измен"),
      ),
    ).toBe(true);
    const create = operation(document.contracts[0]!.document, "post", "/create");
    delete create.requestBody;
    create.responses = {
      "201": { description: "created", links: { users: { operationRef: "#/paths/~1users/get" } } },
    };
    expect(build(document).contract).toBeNull();
  });

  it("rejects transitive aliases filled by another source even when the first target is identical", () => {
    for (const referenceKind of ["schema", "pathItem", "anchor"]) {
      const document = fixture();
      const leafPointer = "#/paths/~1leaf/get/responses/200/content/application~1json/schema";
      const bridge = {
        get: {
          [OPERATION_KEY]: "bridge",
          responses: {
            "200": {
              description: "ok",
              content: {
                "application/json": {
                  schema: {
                    $ref: leafPointer,
                    ...(referenceKind === "anchor" ? { $anchor: "bridgeSchema" } : {}),
                  },
                },
              },
            },
          },
        },
      };
      const leaf = (type: string) => ({
        get: {
          [OPERATION_KEY]: "leaf",
          responses: {
            "200": { description: "ok", content: { "application/json": { schema: { type } } } },
          },
        },
      });
      const create = {
        [OPERATION_KEY]: "create",
        requestBody: {
          content: {
            "application/json": {
              schema: {
                $ref:
                  referenceKind === "anchor"
                    ? "#bridgeSchema"
                    : "#/paths/~1bridge/get/responses/200/content/application~1json/schema",
              },
            },
          },
        },
        responses: { "201": { description: "created" } },
      };
      document.contracts = [
        {
          id: "a",
          name: "A",
          document: {
            openapi: "3.1.0",
            info: { title: "A", version: "1" },
            paths: {
              "/create": { post: create },
              "/bridge": structuredClone(bridge),
              "/leaf": leaf("integer"),
            },
          },
        },
        {
          id: "b",
          name: "B",
          document: {
            openapi: "3.1.0",
            info: { title: "B", version: "1" },
            paths: { "/bridge": structuredClone(bridge), "/leaf": leaf("string") },
          },
        },
      ];
      if (referenceKind === "pathItem") {
        operation(document.contracts[0]!.document, "post", "/create").requestBody = undefined;
        document.contracts[0]!.document.components = {
          pathItems: { Bridge: { $ref: "#/paths/~1bridge" } },
        };
      }
      document.messages = [
        request("create", "POST /create", {
          operation: { contractId: "a", operationKey: "create" },
        }),
        request("bridge", "GET /bridge", {
          operation: { contractId: "b", operationKey: "bridge" },
        }),
        request("leaf", "GET /leaf", { operation: { contractId: "b", operationKey: "leaf" } }),
      ];
      const result = build(document);
      expect(result.contract, referenceKind).toBeNull();
      expect(
        result.errors.some(({ message }) =>
          message.includes(referenceKind === "anchor" ? "#/paths/~1leaf" : "#/paths/~1bridge"),
        ),
        referenceKind,
      ).toBe(true);
    }
  });

  it("keeps links to the same selected operation when preview adds responses or edits a path", () => {
    for (const linkKind of ["operationRef", "operationId"]) {
      const document = jwtFixture();
      const login = operation(document.contracts[0]!.document, "post", "/auth/login");
      login.responses = {
        "200": {
          description: "ok",
          links: {
            profile: {
              [linkKind]: linkKind === "operationRef" ? "#/paths/~1users~1me/get" : "profile",
            },
          },
        },
      };
      document.messages.push(response("forbidden", "profile", "403 Forbidden"));
      const rows = previewCanvasContract(document).rows;
      if (linkKind === "operationId") rows[1]!.path = "/profile";
      const result = buildCanvasContract(document, rows, "Auth", "new");
      expect(result.errors, linkKind).toEqual([]);
      const path = linkKind === "operationRef" ? "/users/me" : "/profile";
      expect(operation(result.contract!.document, "get", path).responses).toMatchObject({
        "403": { description: "403 Forbidden" },
      });
    }
  });

  it("preserves links to retained callback operations by operationId", () => {
    const document = jwtFixture();
    const login = operation(document.contracts[0]!.document, "post", "/auth/login");
    login.callbacks = {
      delivered: {
        "{$request.body#/callbackUrl}": {
          post: { operationId: "onToken", responses: { "200": { description: "ok" } } },
        },
      },
    };
    login.responses = {
      "200": { description: "ok", links: { callback: { operationId: "onToken" } } },
    };
    const rows = previewCanvasContract(document).rows;
    rows[0]!.path = "/login";
    const result = buildCanvasContract(document, rows, "Auth", "new");
    expect(result.errors).toEqual([]);
    expect(operation(result.contract!.document, "post", "/login").callbacks).toMatchObject({
      delivered: { "{$request.body#/callbackUrl}": { post: { operationId: "onToken" } } },
    });
  });

  it("keeps ordinary operationRef links valid after identity and inheritance normalization", () => {
    const document = jwtFixture();
    operation(document.contracts[0]!.document, "post", "/auth/login").responses = {
      "200": { description: "ok", links: { profile: { operationRef: "#/paths/~1users~1me/get" } } },
    };
    const result = build(document);
    expect(result.errors).toEqual([]);
    expect(result.contract).not.toBeNull();
  });

  it("blocks duplicate anchors and cross-source operationId link retargeting", () => {
    for (const reference of ["anchor", "operationId"]) {
      const document = fixture();
      const target = (path: string, type: string) => ({
        [path]: {
          get: {
            [OPERATION_KEY]: type,
            operationId: "users",
            responses: { "200": { description: "ok" } },
          },
        },
      });
      document.contracts = [
        {
          id: "a",
          name: "A",
          document: {
            openapi: "3.1.0",
            info: { title: "A", version: "1" },
            components: { schemas: { Integer: { $anchor: "shared", type: "integer" } } },
            paths: {
              ...target("/old-users", "integer"),
              "/create": {
                post: {
                  [OPERATION_KEY]: "create",
                  requestBody: { content: { "application/json": { schema: { $ref: "#shared" } } } },
                  responses: {
                    "201": { description: "created", links: { users: { operationId: "users" } } },
                  },
                },
              },
            },
          },
        },
        {
          id: "b",
          name: "B",
          document: {
            openapi: "3.1.0",
            info: { title: "B", version: "1" },
            components: { schemas: { String: { $anchor: "shared", type: "string" } } },
            paths: target("/new-users", "string"),
          },
        },
      ];
      const create = operation(document.contracts[0]!.document, "post", "/create");
      if (reference === "anchor") create.responses = { "201": { description: "created" } };
      else {
        delete create.requestBody;
        delete document.contracts[0]!.document.components;
        delete document.contracts[1]!.document.components;
      }
      document.messages = [
        request("create", "POST /create", {
          operation: { contractId: "a", operationKey: "create" },
        }),
        request("users", "GET /new-users", {
          operation: { contractId: "b", operationKey: "string" },
        }),
      ];
      expect(build(document).contract, reference).toBeNull();
    }
  });

  it("does not accept incompatible versions or silently discard root metadata", () => {
    const document = jwtFixture();
    const other = structuredClone(document.contracts[0]!);
    other.id = "old-version";
    other.document.openapi = "3.0.3";
    document.contracts.push(other);
    document.messages.push(
      request("other", "POST /other", {
        operation: { contractId: other.id, operationKey: "login" },
      }),
    );
    const rows = previewCanvasContract(document).rows;
    rows.at(-1)!.path = "/other";
    expect(
      buildCanvasContract(document, rows, "API", "new").errors.some(({ message }) =>
        message.includes("openapi"),
      ),
    ).toBe(true);
  });
});

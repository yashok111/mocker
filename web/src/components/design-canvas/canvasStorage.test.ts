// @vitest-environment node
import { describe, expect, it } from "vitest";
import { emptyCanvas, resolveOperation } from "./canvasModel";
import { parseCanvas, parseSavedCanvas, serializeSavedCanvas } from "./canvasStorage";
import type { CanvasDocument } from "./types";
import { defaultStepExecution } from "./canvasExecution";

function validDocument(): CanvasDocument {
  return {
    ...emptyCanvas(),
    title: "Сценарий",
    participants: [
      { id: "a", name: "A", kind: "client", description: "" },
      { id: "b", name: "B", kind: "service", description: "" },
    ],
    messages: [
      {
        id: "request",
        fromId: "a",
        toId: "b",
        kind: "request",
        label: "Запрос",
        description: "",
        operation: { contractId: "contract", operationKey: "removed-operation" },
      },
      {
        id: "response",
        fromId: "b",
        toId: "a",
        kind: "response",
        label: "Ответ",
        description: "",
        replyToId: "request",
      },
    ],
    fragments: [
      {
        id: "fragment",
        kind: "opt",
        label: "Условие",
        fromMessageId: "request",
        toMessageId: "response",
      },
    ],
    contracts: [
      {
        id: "contract",
        name: "API",
        document: { openapi: "3.1.0", info: { title: "API", version: "1" }, paths: {} },
      },
    ],
  };
}

describe("canvas persistence", () => {
  it("round-trips independent inherited alias bindings and rejects duplicate effective keys", () => {
    const document = validDocument();
    document.contracts[0]!.document = {
      paths: {
        "/one": {
          $ref: "#/components/pathItems/Shared",
          "x-mocker-canvas-operation-ids": { get: "one" },
        },
        "/two": {
          $ref: "#/components/pathItems/Shared",
          "x-mocker-canvas-operation-ids": { get: "two" },
        },
      },
      components: {
        pathItems: { Shared: { get: { responses: { "200": { description: "ok" } } } } },
      },
    };
    document.messages[0]!.operation = { contractId: "contract", operationKey: "one" };
    document.messages.push({
      ...document.messages[0]!,
      id: "second",
      operation: { contractId: "contract", operationKey: "two" },
    });
    const original = structuredClone(document);
    const parsed = parseSavedCanvas(serializeSavedCanvas(document, {})).document;
    expect(resolveOperation(parsed, document.messages[0]!.operation!)).toMatchObject({
      location: { path: "/one" },
    });
    expect(resolveOperation(parsed, document.messages[2]!.operation!)).toMatchObject({
      location: { path: "/two" },
    });
    expect(document).toEqual(original);
    const paths = document.contracts[0]!.document.paths as Record<string, Record<string, unknown>>;
    paths["/two"]!["x-mocker-canvas-operation-ids"] = { get: "one" };
    expect(() => serializeSavedCanvas(document, {})).toThrow(/повторяющийся/i);
    paths["/two"]!["x-mocker-canvas-operation-ids"] = { get: null };
    expect(() => serializeSavedCanvas(document, {})).toThrow(/operation-id/i);
    paths["/two"]!["x-mocker-canvas-operation-ids"] = 42;
    expect(() => serializeSavedCanvas(document, {})).toThrow(/operation-id/i);
  });
  it("accepts event JSON text above 50,000 characters within 256 KiB", () => {
    const document = {
      ...validDocument(),
      formatVersion: 3 as const,
      eventModel: {
        servers: [],
        channels: [],
        contracts: [],
        schemas: [
          {
            id: "payload",
            name: "Payload",
            description: "",
            schemaJSON: JSON.stringify({ description: "x".repeat(60_000) }),
          },
        ],
        messages: [
          {
            id: "created",
            name: "Created",
            description: "",
            examples: [
              {
                name: "Large",
                payloadJSON: JSON.stringify({ value: "x".repeat(60_000) }),
                headersJSON: JSON.stringify({ header: "x".repeat(60_000) }),
              },
            ],
          },
        ],
      },
    };
    expect(parseCanvas(JSON.stringify(document)).eventModel).toEqual(document.eventModel);
  });

  it("rejects event JSON text over 256 KiB measured in UTF-8 bytes", () => {
    const document = {
      ...validDocument(),
      formatVersion: 3 as const,
      eventModel: {
        servers: [],
        channels: [],
        contracts: [],
        messages: [],
        schemas: [
          {
            id: "payload",
            name: "Payload",
            description: "",
            schemaJSON: JSON.stringify({ description: "я".repeat(131_070) }),
          },
        ],
      },
    };
    expect(() => parseCanvas(JSON.stringify(document))).toThrow(/schemaJSON превышает 256 КиБ/);
  });

  it("accepts an existing participant ID outside the event ID alphabet", () => {
    const document = validDocument();
    document.participants[0]!.id = "orders:legacy";
    document.messages[0]!.fromId = "orders:legacy";
    document.messages[1]!.toId = "orders:legacy";
    const eventDocument = {
      ...document,
      formatVersion: 3 as const,
      eventModel: {
        servers: [],
        channels: [],
        messages: [],
        schemas: [],
        contracts: [
          {
            id: "events",
            name: "Events",
            description: "",
            participantId: "orders:legacy",
            version: "1",
            operations: [],
          },
        ],
      },
    };
    expect(parseCanvas(JSON.stringify(eventDocument)).eventModel?.contracts[0]?.participantId).toBe(
      "orders:legacy",
    );
  });

  it("round-trips participant spacing", () => {
    const document = validDocument();
    document.participants[0] = { ...document.participants[0]!, offsetX: 280 };
    expect(parseSavedCanvas(serializeSavedCanvas(document, {})).document).toEqual(document);
  });

  it.each([-1, 2001, 0.5, "100", null])("rejects invalid participant spacing %s", (offsetX) => {
    const document = validDocument();
    const participants = document.participants.map((item) => ({ ...item, offsetX }));
    expect(() => parseCanvas(JSON.stringify({ ...document, participants }))).toThrow(/offsetX/);
  });

  it("accepts wide JSON assertion arrays within the document size limit", () => {
    const document = validDocument();
    document.messages[0]!.execution = {
      ...defaultStepExecution(),
      assertions: [{ pointer: "", equals: Array.from({ length: 500_000 }, () => 0) }],
    };
    expect(() => serializeSavedCanvas(document, {})).not.toThrow();
  });
  it("round-trips execution settings without converting response expectations", () => {
    const document = validDocument();
    document.execution = { variables: { token: "value" } };
    document.messages[0]!.execution = {
      ...defaultStepExecution(),
      expectedStatus: 404,
      pathParams: { id: "{{token}}" },
      assertions: [{ pointer: "/error", equals: { code: "missing", nested: [null, true, 1] } }],
      extract: [{ name: "value", pointer: "/error/code" }],
    };
    expect(parseSavedCanvas(serializeSavedCanvas(document, {})).document).toEqual(document);
  });

  it.each([
    { enabled: "yes" },
    { expectedStatus: 99 },
    { expectedStatus: 600 },
    { expectedStatus: 200.5 },
    { pathParams: [] },
    { query: { q: 1 } },
    { headers: { ["x".repeat(257)]: "a" } },
    { body: "я".repeat(524_289) },
    { query: { q: "x".repeat(50_001) } },
    { assertions: [{ pointer: "a", equals: null }] },
    { assertions: [{ pointer: "/a~2", equals: null }] },
    { assertions: [{ pointer: "/".repeat(2001), equals: null }] },
    { assertions: [{ pointer: "/a" }] },
    { assertions: Array.from({ length: 101 }, () => ({ pointer: "", equals: null })) },
    { extract: [{ name: "not-a-name", pointer: "" }] },
    {
      extract: [
        { name: "value", pointer: "" },
        { name: "value", pointer: "/a" },
      ],
    },
  ])("rejects malformed or oversized execution settings, case %#", (invalid) => {
    const document = validDocument();
    document.messages[0]!.execution = { ...defaultStepExecution(), ...invalid } as never;
    expect(() => serializeSavedCanvas(document, {})).toThrow(/execution/);
  });

  it.each([
    {},
    { variables: [] },
    { variables: { "not-valid": "x" } },
    { variables: { ok: 1 } },
    { variables: Object.fromEntries(Array.from({ length: 101 }, (_, index) => [`v${index}`, ""])) },
  ])("rejects malformed initial execution variables, case %#", (execution) => {
    expect(() => parseCanvas(JSON.stringify({ ...validDocument(), execution }))).toThrow(
      /execution/,
    );
  });

  it("round-trips custom card and arrow colors and their reset to defaults", () => {
    const document = validDocument();
    document.participants[0]!.color = "#aBcDeF";
    document.messages[0]!.color = "#123456";
    document.messages[0]!.arrowColor = "#987654";
    expect(parseSavedCanvas(serializeSavedCanvas(document, {})).document).toEqual(document);
    delete document.participants[0]!.color;
    delete document.messages[0]!.color;
    delete document.messages[0]!.arrowColor;
    expect(parseCanvas(JSON.stringify(document))).toEqual(validDocument());
  });

  it.each(["", "#123", "#12345678", "red", "url(#paint)", "#gggggg", " #123456", null, 42])(
    "rejects invalid presentation colors: %s",
    (color) => {
      for (const [collection, field] of [
        ["participants", "color"],
        ["messages", "color"],
        ["messages", "arrowColor"],
      ] as const) {
        const document = validDocument();
        Object.assign(document[collection][0]!, { [field]: color });
        expect(() => parseCanvas(JSON.stringify(document))).toThrow(field);
        expect(() => serializeSavedCanvas(document, {})).toThrow(field);
      }
    },
  );

  it("round-trips a valid document and serialized form drafts", () => {
    const document = validDocument();
    const all = JSON.stringify({
      "/contracts/contract": { source: "{unfinished", propertySource: "{}" },
    });
    const serialized = serializeSavedCanvas(document, {
      all,
      tab: "source",
    });

    expect(parseSavedCanvas(serialized)).toEqual({
      document,
      formDrafts: { all, tab: "source" },
    });
  });

  it("keeps a large form draft while the complete save remains within the storage limit", () => {
    const draft = JSON.stringify({
      "/field": { source: "x".repeat(75_000), propertySource: "{}" },
    });

    expect(
      parseSavedCanvas(serializeSavedCanvas(validDocument(), { all: draft })).formDrafts.all,
    ).toBe(draft);
  });

  it.each([
    "{broken",
    "[]",
    "null",
    '{"/field":{}}',
    '{"/field":{"source":"x","propertySource":"{}","error":42}}',
  ])("rejects damaged serialized field buffers: %s", (all) => {
    expect(() =>
      parseSavedCanvas(JSON.stringify({ document: validDocument(), formDrafts: { all } })),
    ).toThrow();
  });

  it("parses a bare canvas document while keeping a missing operation resolvable as null", () => {
    const parsed = parseCanvas(JSON.stringify(validDocument()));

    expect(resolveOperation(parsed, parsed.messages[0]!.operation!)).toBeNull();
  });

  it("rejects invalid JSON with a descriptive Russian error", () => {
    expect(() => parseCanvas("{broken")).toThrow(/не удалось разобрать JSON/i);
  });

  it("rejects unsupported versions and malformed containers", () => {
    expect(() => parseCanvas(JSON.stringify({ ...validDocument(), formatVersion: 4 }))).toThrow(
      /версия формата/i,
    );
    expect(() => parseCanvas(JSON.stringify({ ...validDocument(), participants: {} }))).toThrow(
      /participants.*массив/i,
    );
  });

  it("roundtrips v3 event JSON text and rejects broken event references", () => {
    const source = '{"const":90071992547409931234567890123456789}';
    const document = {
      ...validDocument(),
      formatVersion: 3 as const,
      eventModel: {
        servers: [],
        channels: [
          {
            id: "topic",
            name: "Topic",
            description: "",
            address: "orders.events",
            serverIds: [],
            messageIds: ["created"],
          },
        ],
        messages: [
          {
            id: "created",
            name: "Created",
            description: "",
            examples: [{ name: "Exact", payloadJSON: source }],
          },
        ],
        schemas: [{ id: "payload", name: "Payload", description: "", schemaJSON: source }],
        contracts: [],
      },
    };
    expect(parseCanvas(JSON.stringify(document)).eventModel?.schemas[0]?.schemaJSON).toBe(source);
    document.eventModel.channels[0]!.messageIds = ["missing"];
    expect(() => parseCanvas(JSON.stringify(document))).toThrow(/messageIds.*не ссылается/);
  });

  it.each([
    ["discriminatorProperty", { discriminatorProperty: 7 }, /discriminatorProperty/],
    ["kafka shape", { kafka: [] }, /kafka/],
    ["partitions zero", { kafka: { partitions: 0 } }, /partitions/],
    ["partitions fractional", { kafka: { partitions: 1.5 } }, /partitions/],
    ["replicas overflow", { kafka: { replicas: 2147483648 } }, /replicas/],
  ])("rejects malformed optional event channel %s", (_name, changes, error) => {
    const document = {
      ...validDocument(),
      formatVersion: 3,
      eventModel: {
        servers: [],
        schemas: [],
        messages: [{ id: "created", name: "Created", description: "", examples: [] }],
        channels: [
          {
            id: "topic",
            name: "Topic",
            description: "",
            address: "orders.events",
            serverIds: [],
            messageIds: ["created"],
            ...changes,
          },
        ],
        contracts: [],
      },
    };
    expect(() => parseCanvas(JSON.stringify(document))).toThrow(error);
  });

  it.each([
    ["kafka shape", { kafka: null }, /kafka/],
    ["group type", { kafka: { groupId: 3 } }, /groupId/],
    ["client control", { kafka: { clientId: "a\n" } }, /clientId/],
    ["group template", { kafka: { groupId: "{{tenant}}" } }, /groupId/],
    ["send metadata", { action: "send", kafka: { groupId: "group" } }, /groupId/],
  ])("rejects malformed optional event operation %s", (_name, changes, error) => {
    const document = {
      ...validDocument(),
      formatVersion: 3,
      eventModel: {
        servers: [],
        schemas: [],
        messages: [{ id: "created", name: "Created", description: "", examples: [] }],
        channels: [
          {
            id: "topic",
            name: "Topic",
            description: "",
            address: "orders.events",
            serverIds: [],
            messageIds: ["created"],
          },
        ],
        contracts: [
          {
            id: "events",
            name: "Events",
            description: "",
            participantId: "a",
            version: "1",
            operations: [
              {
                id: "receive",
                name: "Receive",
                description: "",
                action: "receive",
                channelId: "topic",
                messageId: "created",
                ...changes,
              },
            ],
          },
        ],
      },
    };
    expect(() => parseCanvas(JSON.stringify(document))).toThrow(error);
  });

  it("preserves optional event routes and cross-model links without resolving embedded targets", () => {
    const document = {
      ...validDocument(),
      formatVersion: 3,
      eventModel: {
        servers: [],
        schemas: [],
        messages: [{ id: "created", name: "Created", description: "", examples: [] }],
        channels: ["source", "retry", "dlq"].map((id) => ({
          id,
          name: id,
          description: "",
          address: id,
          serverIds: [],
          messageIds: ["created"],
        })),
        contracts: [
          {
            id: "events",
            name: "Events",
            description: "",
            participantId: "a",
            version: "1",
            operations: [
              {
                id: "receive",
                name: "Receive",
                description: "",
                action: "receive",
                channelId: "source",
                messageId: "created",
                failureRoutes: { retryChannelId: "retry", deadLetterChannelId: "dlq" },
                apiLinks: [{ contractId: "missing-http", operationKey: "opaque-key" }],
                stateLinks: [
                  { contractId: "missing-http", diagramId: "order", transitionId: "created" },
                ],
              },
            ],
          },
        ],
      },
    };
    const saved = parseCanvas(JSON.stringify(document));
    expect(saved.eventModel?.contracts[0]?.operations[0]).toMatchObject({
      failureRoutes: { retryChannelId: "retry", deadLetterChannelId: "dlq" },
      apiLinks: [{ contractId: "missing-http", operationKey: "opaque-key" }],
      stateLinks: [{ contractId: "missing-http", diagramId: "order", transitionId: "created" }],
    });
    const operation = document.eventModel.contracts[0]!.operations[0]!;
    for (const [name, changes, error] of [
      ["null routes", { failureRoutes: null }, /failureRoutes/],
      ["route on send", { action: "send" }, /failureRoutes/],
      ["self route", { failureRoutes: { retryChannelId: "source" } }, /retryChannelId/],
      [
        "duplicate targets",
        { failureRoutes: { retryChannelId: "retry", deadLetterChannelId: "retry" } },
        /deadLetterChannelId/,
      ],
      ["missing target", { failureRoutes: { retryChannelId: "missing" } }, /retryChannelId/],
      [
        "duplicate API link",
        { apiLinks: [operation.apiLinks[0], operation.apiLinks[0]] },
        /apiLinks/,
      ],
      [
        "malformed state link",
        { stateLinks: [{ contractId: "missing-http", diagramId: "order" }] },
        /transitionId/,
      ],
    ] as const) {
      expect(
        () =>
          parseCanvas(
            JSON.stringify({
              ...document,
              eventModel: {
                ...document.eventModel,
                contracts: [
                  {
                    ...document.eventModel.contracts[0],
                    operations: [{ ...operation, ...changes }],
                  },
                ],
              },
            }),
          ),
        name,
      ).toThrow(error);
    }
  });

  it("rejects duplicate entity identities", () => {
    const document = validDocument();
    document.participants.push({ ...document.participants[0]! });

    expect(() => parseCanvas(JSON.stringify(document))).toThrow(/дублируется.*participants/i);
  });

  it("rejects messages with missing endpoints", () => {
    const document = validDocument();
    document.messages[0] = { ...document.messages[0]!, toId: "missing" };

    expect(() => parseCanvas(JSON.stringify(document))).toThrow(/toId.*объект/i);
  });

  it("rejects a response before its request and mismatched reverse endpoints", () => {
    const beforeRequest = validDocument();
    beforeRequest.messages.reverse();
    expect(() => parseCanvas(JSON.stringify(beforeRequest))).toThrow(/ответ.*раньше запроса/i);

    const mismatched = validDocument();
    mismatched.messages[1] = { ...mismatched.messages[1]!, fromId: "a", toId: "b" };
    expect(() => parseCanvas(JSON.stringify(mismatched))).toThrow(/направление ответа/i);
  });

  it("accepts a response while its optional request link is still incomplete", () => {
    const document = validDocument();
    document.messages[1] = { ...document.messages[1]!, replyToId: undefined };

    expect(parseCanvas(JSON.stringify(document)).messages[1]).toMatchObject({
      id: "response",
      kind: "response",
    });
  });

  it("accepts fragment boundary IDs after their messages exchange positions", () => {
    const document = validDocument();
    document.messages = [document.messages[1]!, document.messages[0]!];
    document.messages[0] = { ...document.messages[0]!, replyToId: undefined };

    expect(parseCanvas(JSON.stringify(document)).fragments[0]).toEqual({
      id: "fragment",
      kind: "opt",
      label: "Условие",
      fromMessageId: "request",
      toMessageId: "response",
    });
  });

  it("rejects invalid enums, fragment boundaries and contract bindings", () => {
    expect(() =>
      parseCanvas(
        JSON.stringify({
          ...validDocument(),
          participants: [{ ...validDocument().participants[0], kind: "browser" }],
          messages: [],
          fragments: [],
        }),
      ),
    ).toThrow(/kind/i);

    expect(() =>
      parseCanvas(
        JSON.stringify({
          ...validDocument(),
          fragments: [{ ...validDocument().fragments[0], toMessageId: "missing" }],
        }),
      ),
    ).toThrow(/toMessageId/i);

    const missingContract = validDocument();
    missingContract.messages[0] = {
      ...missingContract.messages[0]!,
      operation: { contractId: "missing", operationKey: "operation" },
    };
    expect(() => parseCanvas(JSON.stringify(missingContract))).toThrow(/contractId/i);
  });

  it("rejects malformed saved envelopes and oversized payloads", () => {
    expect(() =>
      parseSavedCanvas(JSON.stringify({ document: validDocument(), formDrafts: { all: 42 } })),
    ).toThrow(/formDrafts/i);
    expect(() => parseCanvas(`{"padding":"${"x".repeat(2_100_000)}"}`)).toThrow(/слишком большой/i);
  });
});

describe("data binding persistence", () => {
  const binding = {
    id: "order-id",
    sourceMessageId: "missing-source",
    sourcePointer: "/id",
    target: { kind: "body" as const, pointer: "" },
  };
  const withBindings = (bindings: unknown): string => {
    const document = validDocument();
    document.messages[0]!.execution = { ...defaultStepExecution(), bindings: bindings as never };
    return JSON.stringify(document);
  };

  it("preserves whole-body pointers and dangling semantic links in saved snapshots", () => {
    const parsed = parseCanvas(withBindings([binding]));
    expect(parsed.messages[0]?.execution?.bindings).toEqual([binding]);
    expect(
      parseSavedCanvas(serializeSavedCanvas(parsed, {})).document.messages[0]?.execution?.bindings,
    ).toEqual([binding]);
  });

  it("round-trips an ordered transform chain without changing existing binding fields", () => {
    const transformed = {
      ...binding,
      prefix: "ID ",
      target: { kind: "header" as const, name: "X-Order" },
      transforms: [{ kind: "trim" }, { kind: "to_integer" }, { kind: "to_string" }],
    };
    const parsed = parseCanvas(withBindings([transformed]));
    expect(parsed.messages[0]?.execution?.bindings).toEqual([transformed]);
    expect(
      parseSavedCanvas(serializeSavedCanvas(parsed, {})).document.messages[0]?.execution?.bindings,
    ).toEqual([transformed]);
  });

  it.each([
    null,
    {},
    Array.from({ length: 9 }, () => ({ kind: "trim" })),
    [null],
    [{ kind: "unknown" }],
    [{ kind: "trim", argument: "x" }],
    [{ kind: 42 }],
  ])("rejects invalid transform chain %#", (transforms) => {
    expect(() => parseCanvas(withBindings([{ ...binding, transforms }]))).toThrow();
  });

  it("leaves legacy documents without bindings", () => {
    const document = validDocument();
    document.messages[0]!.execution = defaultStepExecution();
    expect(
      parseSavedCanvas(serializeSavedCanvas(document, {})).document.messages[0]?.execution,
    ).not.toHaveProperty("bindings");
  });

  it.each([
    null,
    [{ ...binding, id: "bad id" }],
    [{ ...binding, sourcePointer: "/bad~2" }],
    [{ ...binding, prefix: null }],
    [{ ...binding, extra: "unknown" }],
    [{ ...binding, target: { kind: "body" } }],
    [{ ...binding, target: { kind: "body", pointer: "", name: "x" } }],
    [{ ...binding, target: { kind: "header", name: "" } }],
    [binding, { ...binding, target: { kind: "query", name: "id" } }],
    [
      { ...binding, target: { kind: "header", name: "Authorization" } },
      { ...binding, id: "other", target: { kind: "header", name: "authorization" } },
    ],
    [binding, { ...binding, id: "other", target: { kind: "body", pointer: "/id" } }],
  ])("rejects invalid binding structure %#", (bindings) => {
    expect(() => parseCanvas(withBindings(bindings))).toThrow();
  });
});

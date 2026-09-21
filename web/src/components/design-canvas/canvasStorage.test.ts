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
    expect(() => parseCanvas(JSON.stringify({ ...validDocument(), formatVersion: 2 }))).toThrow(
      /версия формата/i,
    );
    expect(() => parseCanvas(JSON.stringify({ ...validDocument(), participants: {} }))).toThrow(
      /participants.*массив/i,
    );
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

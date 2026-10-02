import { describe, expect, it } from "vitest";
import { emptyCanvas, exampleCanvas } from "./canvasModel";
import {
  MAX_MOCKER_FILE_BYTES,
  parseMockerScenario,
  serializeMockerScenario,
} from "./scenarioMockerFile";

const envelope = (document = exampleCanvas()) => ({
  kind: "mocker.scenario",
  formatVersion: 1,
  document,
  formDrafts: {},
});

describe(".mocker scenario files", () => {
  it("preserves diagram data and pending form fields without mutating the source", () => {
    const document = exampleCanvas();
    document.participants[0]!.color = "#ff0000";
    document.participants[0]!.offsetX = 160;
    document.execution = { variables: { token: "example" } };
    document.contracts.push({
      id: "api",
      name: "API",
      mode: "linked",
      source: { designId: 77, revisionId: 88 },
      document: { openapi: "3.0.3", info: { title: "API", version: "1" }, paths: {} },
    });
    const before = structuredClone(document);
    const formDrafts = { note: "unfinished" };
    const result = parseMockerScenario(serializeMockerScenario(document, formDrafts));
    expect(result).toEqual({
      document: {
        ...document,
        contracts: document.contracts.map((contract) => ({
          ...contract,
          mode: "copy",
          source: undefined,
        })),
      },
      formDrafts,
    });
    expect(document).toEqual(before);
    expect(result.document.contracts[0]).not.toHaveProperty("source");
  });
  it.each([1, 2, 3] as const)("accepts document version %s", (formatVersion) => {
    const document = { ...emptyCanvas(), formatVersion };
    expect(parseMockerScenario(serializeMockerScenario(document, {})).document).toEqual(document);
  });
  it.each([
    "{",
    "null",
    JSON.stringify({ ...envelope(), kind: "mocker.workspace" }),
    JSON.stringify({ ...envelope(), formatVersion: 2 }),
    JSON.stringify({ ...envelope(), document: { ...emptyCanvas(), formatVersion: 99 } }),
  ])("rejects invalid envelope %s", (text) => {
    expect(() => parseMockerScenario(text)).toThrow();
  });
  it("rejects dangling diagram references on import and export", () => {
    const document = exampleCanvas();
    document.messages[0]!.fromId = "missing";
    expect(() => parseMockerScenario(JSON.stringify(envelope(document)))).toThrow(/fromId/);
    expect(() => serializeMockerScenario(document, {})).toThrow(/fromId/);
  });
  it("preserves event schemas and examples in version 3", () => {
    const document = {
      ...emptyCanvas(),
      formatVersion: 3 as const,
      eventModel: {
        servers: [],
        channels: [],
        contracts: [],
        schemas: [
          { id: "payload", name: "Payload", description: "", schemaJSON: '{"type":"object"}' },
        ],
        messages: [
          {
            id: "created",
            name: "Created",
            description: "",
            examples: [{ name: "Sample", payloadJSON: '{"id":123}', headersJSON: "{}" }],
          },
        ],
      },
    };
    expect(parseMockerScenario(serializeMockerScenario(document, {})).document).toEqual(document);
  });
  it("checks UTF-8 size", () => {
    expect(() => parseMockerScenario("я".repeat(MAX_MOCKER_FILE_BYTES / 2 + 1))).toThrow(/размер/i);
  });
  it("rejects numbers that would lose precision", () => {
    const text = JSON.stringify(envelope()).replace(
      '"formDrafts":{}',
      '"formDrafts":{},"number":9007199254740993',
    );
    expect(() => parseMockerScenario(text)).toThrow(/точности/);
  });
  it("detaches foreign server IDs even when the incoming file still contains them", () => {
    const document = emptyCanvas();
    document.contracts = [
      {
        id: "api",
        name: "API",
        mode: "linked",
        source: { designId: 1, revisionId: 2 },
        document: {},
      },
    ];
    const result = parseMockerScenario(JSON.stringify(envelope(document)));
    expect(result.document.contracts[0]!.mode).toBe("copy");
    expect(result.document.contracts[0]).not.toHaveProperty("source");
  });
});

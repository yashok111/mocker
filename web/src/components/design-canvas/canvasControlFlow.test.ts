import { describe, expect, it } from "vitest";
import { canvasExecutionBlockReason } from "./canvasExecution";
import { splitFragmentBranch } from "./canvasFragments";
import { parseCanvas } from "./canvasStorage";
import type { CanvasDocument } from "./types";

function fixture(): CanvasDocument {
  return {
    formatVersion: 2,
    title: "Flow",
    participants: [
      { id: "client", name: "Client", kind: "client", description: "" },
      { id: "api", name: "API", kind: "service", description: "" },
    ],
    messages: [
      {
        id: "m1",
        fromId: "client",
        toId: "api",
        kind: "request",
        label: "One",
        description: "",
        operation: { contractId: "api", operationKey: "op" },
      },
      {
        id: "m2",
        fromId: "client",
        toId: "api",
        kind: "request",
        label: "Two",
        description: "",
        operation: { contractId: "api", operationKey: "op" },
      },
    ],
    fragments: [
      {
        id: "f",
        kind: "alt",
        label: "Choice",
        fromMessageId: "m1",
        toMessageId: "m2",
        branches: [
          {
            id: "b1",
            label: "First",
            fromMessageId: "m1",
            toMessageId: "m1",
            execution: { condition: { variable: "token", operator: "equals", value: "" } },
          },
          {
            id: "b2",
            label: "Else",
            fromMessageId: "m2",
            toMessageId: "m2",
            execution: { otherwise: true },
          },
        ],
      },
    ],
    contracts: [
      {
        id: "api",
        name: "API",
        mode: "linked",
        source: { designId: 1, revisionId: 2 },
        document: {
          openapi: "3.1.0",
          info: { title: "API", version: "1" },
          paths: {
            "/op": {
              post: {
                "x-mocker-canvas-operation-id": "op",
                responses: { "200": { description: "OK" } },
              },
            },
          },
        },
      },
    ],
  };
}

describe("control flow settings", () => {
  it("preserves configured branches in local storage and allows a server run", () => {
    const document = fixture();
    expect(parseCanvas(JSON.stringify(document))).toEqual(document);
    expect(canvasExecutionBlockReason(document)).toBeNull();
  });

  it("blocks incomplete and invalid execution settings", () => {
    const document = fixture();
    document.fragments[0]!.branches![0]!.execution = undefined;
    expect(canvasExecutionBlockReason(document)).toMatch(/ветк|услов/i);
    document.fragments[0]!.branches![0]!.execution = {
      condition: { variable: "token", operator: "equals" },
    };
    expect(() => parseCanvas(JSON.stringify(document))).toThrow(/value|значен/i);
  });

  it("keeps an otherwise setting on the final branch when splitting it", () => {
    const document = fixture();
    document.messages.splice(1, 0, { ...document.messages[0]!, id: "middle" });
    document.fragments[0]!.branches![1]!.fromMessageId = "middle";
    const split = splitFragmentBranch(document, "f", "b2");
    expect(split.fragments[0]!.branches!.at(-1)!.execution).toEqual({ otherwise: true });
    expect(split.fragments[0]!.branches![1]!.execution).toBeUndefined();
  });
});

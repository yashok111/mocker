import { describe, expect, it } from "vitest";
import { canvasExecutionBlockReason, defaultStepExecution } from "./canvasExecution";
import { emptyCanvas, OPERATION_KEY } from "./canvasModel";
import type { CanvasDocument } from "./types";

function scenario(): CanvasDocument {
  return {
    ...emptyCanvas(),
    participants: [
      { id: "client", kind: "client", name: "Клиент", description: "" },
      { id: "api", kind: "service", name: "API", description: "" },
    ],
    messages: ["login", "profile"].map((id) => ({
      id,
      fromId: "client",
      toId: "api",
      kind: "request",
      label: id,
      description: "",
      operation: { contractId: "api", operationKey: id },
    })),
    contracts: [
      {
        id: "api",
        name: "API",
        mode: "linked",
        source: { designId: 5, revisionId: 20, version: 3 },
        document: {
          openapi: "3.1.0",
          info: { title: "API", version: "1" },
          paths: {
            "/login": { post: { [OPERATION_KEY]: "login" } },
            "/users/{id}": { get: { [OPERATION_KEY]: "profile" } },
          },
        },
      },
    ],
  };
}

// Execution semantics are covered by the shared Go runner; these are UI start gates.
describe("canvasExecutionBlockReason", () => {
  it.each(["disabled", "note", "event", "response"] as const)(
    "allows a %s message with a dangling contract beside a valid request",
    (kind) => {
      const document = scenario();
      const skipped = document.messages[0]!;
      skipped.operation = { contractId: "removed", operationKey: "removed" };
      if (kind === "disabled") skipped.execution = { ...defaultStepExecution(), enabled: false };
      else skipped.kind = kind;
      expect(canvasExecutionBlockReason(document)).toBeNull();
    },
  );

  it("still validates settings of skipped steps", () => {
    const document = scenario();
    document.messages[0]!.execution = {
      ...defaultStepExecution(),
      enabled: false,
      expectedStatus: 99,
    };
    document.messages[0]!.operation!.contractId = "missing";
    expect(canvasExecutionBlockReason(document)).toMatch(/expectedStatus/);
  });

  it.each(["fragment", "detached", "unresolved"])(
    "blocks %s targets before starting a server run",
    (kind) => {
      const document = scenario();
      if (kind === "fragment")
        document.fragments = [
          { id: "loop", kind: "loop", label: "", fromMessageId: "login", toMessageId: "profile" },
        ];
      if (kind === "detached") document.contracts[0]!.mode = "copy";
      if (kind === "unresolved") document.messages[0]!.operation!.operationKey = "missing";
      expect(canvasExecutionBlockReason(document)).toBeTruthy();
    },
  );

  it("requires at least one enabled request with an operation", () => {
    const document = scenario();
    for (const message of document.messages)
      message.execution = { ...defaultStepExecution(), enabled: false };
    expect(canvasExecutionBlockReason(document)).toMatch(/нет включённых HTTP/);
    expect(canvasExecutionBlockReason(emptyCanvas())).toMatch(/нет включённых HTTP/);
  });
});

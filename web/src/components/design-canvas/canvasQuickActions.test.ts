import { describe, expect, it } from "vitest";
import { exampleCanvas } from "./canvasModel";
import { addCanvasReply, duplicateCanvasMessage, insertCanvasMessage } from "./canvasQuickActions";

describe("canvas quick actions", () => {
  it("adds a linked reply immediately after the request", () => {
    const doc = exampleCanvas();
    const next = addCanvasReply(doc, "create-order", "reply-new");
    expect(next.messages[1]).toMatchObject({
      id: "reply-new",
      fromId: "orders",
      toId: "client",
      kind: "response",
      replyToId: "create-order",
    });
    expect(doc.messages.some((message) => message.id === "reply-new")).toBe(false);
  });
  it("rejects invalid anchors, response targets, and duplicate IDs", () => {
    const doc = exampleCanvas();
    expect(() => addCanvasReply(doc, "order-created", "new")).toThrow();
    expect(() => addCanvasReply(doc, "missing", "new")).toThrow();
    expect(() => addCanvasReply(doc, "create-order", "create-order")).toThrow();
    expect(() => addCanvasReply(doc, "create-order", "")).toThrow();
  });
  it("duplicates a request without replies and preserves its operation", () => {
    const doc = exampleCanvas();
    const next = duplicateCanvasMessage(doc, "create-order", "copy");
    expect(next.messages[1]).toMatchObject({ id: "copy", operation: doc.messages[0]!.operation });
    expect(next.messages.filter((message) => message.replyToId === "copy")).toHaveLength(0);
    expect(next.fragments).toEqual(doc.fragments);
  });
  it("duplicates a response after its request", () => {
    const doc = exampleCanvas();
    const next = duplicateCanvasMessage(doc, "order-created", "reply-copy");
    expect(next.messages[1]).toMatchObject({ id: "reply-copy", replyToId: "create-order" });
  });
  it("inserts after an anchor or at the beginning", () => {
    const doc = exampleCanvas();
    const message = {
      id: "new",
      fromId: "client",
      toId: "orders",
      kind: "request" as const,
      label: "New",
      description: "",
    };
    expect(insertCanvasMessage(doc, null, message).messages[0]).toEqual(message);
    expect(insertCanvasMessage(doc, "create-order", message).messages[1]).toEqual(message);
    expect(() => insertCanvasMessage(doc, "missing", message)).toThrow();
  });
});

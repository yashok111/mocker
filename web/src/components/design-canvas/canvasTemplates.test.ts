// @vitest-environment node
import { describe, expect, it } from "vitest";
import { createCanvasTemplate } from "./canvasTemplates";

describe("canvas templates", () => {
  it.each(["blank", "login", "checkout", "error"] as const)(
    "creates %s with new IDs each time",
    (kind) => {
      const first = createCanvasTemplate(kind, "My scenario");
      const second = createCanvasTemplate(kind, "My scenario");
      expect(first.title).toBe("My scenario");
      expect(first.contracts).toEqual([]);
      expect(first.messages.every((message) => !message.operation)).toBe(true);
      if (kind !== "blank") {
        expect(first.participants.length).toBeGreaterThan(1);
        expect(first.messages.length).toBeGreaterThan(1);
        expect(first.participants[0]!.id).not.toBe(second.participants[0]!.id);
        for (const message of first.messages.filter((item) => item.replyToId)) {
          const request = first.messages.find((item) => item.id === message.replyToId);
          expect(request?.kind).toBe("request");
          expect(message.fromId).toBe(request?.toId);
          expect(message.toId).toBe(request?.fromId);
        }
      } else {
        expect(first.participants).toEqual([]);
        expect(first.messages).toEqual([]);
      }
    },
  );
});

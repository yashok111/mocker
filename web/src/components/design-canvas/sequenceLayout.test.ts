import { describe, expect, it } from "vitest";

import type { CanvasDocument } from "./types";
import { layoutSequence } from "./sequenceLayout";

const documentOf = (overrides: Partial<CanvasDocument> = {}): CanvasDocument => ({
  formatVersion: 1,
  title: "Checkout",
  participants: [],
  messages: [],
  fragments: [],
  contracts: [],
  ...overrides,
});

const participants: CanvasDocument["participants"] = [
  { id: "buyer", name: "Покупатель", kind: "user", description: "" },
  { id: "shop", name: "Магазин", kind: "service", description: "" },
  { id: "bank", name: "Банк", kind: "external", description: "" },
];

describe("layoutSequence", () => {
  it("returns a usable empty canvas without invented cells", () => {
    const layout = layoutSequence(documentOf());

    expect(layout.width).toBe(720);
    expect(layout.height).toBe(360);
    expect(layout.participants).toEqual([]);
    expect(layout.messages).toEqual([]);
    expect(layout.fragments).toEqual([]);
  });

  it("places horizontal messages on distinct rows in model order", () => {
    const layout = layoutSequence(
      documentOf({
        participants,
        messages: [
          {
            id: "reserve",
            fromId: "buyer",
            toId: "shop",
            kind: "request",
            label: "Резерв",
            description: "",
          },
          {
            id: "charge",
            fromId: "shop",
            toId: "bank",
            kind: "event",
            label: "Оплата",
            description: "",
          },
        ],
      }),
    );

    expect(layout.messages.map(({ id, source, target }) => ({ id, source, target }))).toEqual([
      { id: "reserve", source: { x: 144, y: 148 }, target: { x: 400, y: 148 } },
      { id: "charge", source: { x: 400, y: 220 }, target: { x: 656, y: 220 } },
    ]);
  });

  it("keeps repeated calls between the same participants independently addressable", () => {
    const layout = layoutSequence(
      documentOf({
        participants: participants.slice(0, 2),
        messages: [
          {
            id: "first",
            fromId: "buyer",
            toId: "shop",
            kind: "request",
            label: "Первый",
            description: "",
          },
          {
            id: "second",
            fromId: "buyer",
            toId: "shop",
            kind: "request",
            label: "Второй",
            description: "",
          },
        ],
      }),
    );

    expect(layout.messages.map(({ id, rowY }) => [id, rowY])).toEqual([
      ["first", 148],
      ["second", 220],
    ]);
  });

  it("draws a self-call as a loop returning below its source", () => {
    const layout = layoutSequence(
      documentOf({
        participants: participants.slice(0, 1),
        messages: [
          {
            id: "validate",
            fromId: "buyer",
            toId: "buyer",
            kind: "request",
            label: "Проверка",
            description: "",
          },
        ],
      }),
    );

    expect(layout.messages[0]).toMatchObject({
      source: { x: 144, y: 148 },
      target: { x: 144, y: 182 },
      vertices: [
        { x: 200, y: 148 },
        { x: 200, y: 182 },
      ],
    });
  });

  it("derives fragment bounds from its boundary message IDs", () => {
    const layout = layoutSequence(
      documentOf({
        participants,
        messages: [
          {
            id: "start",
            fromId: "buyer",
            toId: "shop",
            kind: "request",
            label: "Старт",
            description: "",
          },
          {
            id: "inside",
            fromId: "shop",
            toId: "bank",
            kind: "request",
            label: "Внутри",
            description: "",
          },
          {
            id: "finish",
            fromId: "bank",
            toId: "buyer",
            kind: "response",
            label: "Финиш",
            description: "",
          },
        ],
        fragments: [
          {
            id: "optional",
            kind: "opt",
            label: "если доступно",
            fromMessageId: "inside",
            toMessageId: "finish",
          },
        ],
      }),
    );

    expect(layout.fragments[0]).toMatchObject({
      id: "optional",
      x: 100,
      y: 156,
      width: 600,
      height: 166,
    });
    expect(layout.fragments[0]!.y + 20).toBeLessThan(layout.messages[1]!.rowY - 36);
  });
});

// @vitest-environment node
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
  it("uses explicit parents for equal v2 ranges regardless of fragment array order", () => {
    const document = documentOf({
      formatVersion: 2,
      participants,
      messages: [
        {
          id: "call",
          fromId: "buyer",
          toId: "buyer",
          kind: "request",
          label: "Call",
          description: "",
        },
      ],
      fragments: [
        {
          id: "child",
          kind: "loop",
          label: "Retry",
          fromMessageId: "call",
          toMessageId: "call",
          parentFragmentId: "parent",
        },
        {
          id: "parent",
          kind: "opt",
          label: "Optional",
          fromMessageId: "call",
          toMessageId: "call",
        },
      ],
    });
    const layout = layoutSequence(document);
    const child = layout.fragments.find(({ id }) => id === "child")!;
    const parent = layout.fragments.find(({ id }) => id === "parent")!;
    expect(child.y).toBeGreaterThan(parent.y + 20);
    expect(child.x).toBeGreaterThan(parent.x);
    expect(child.x + child.width).toBeLessThan(parent.x + parent.width);
    expect(child.y + child.height).toBeLessThan(parent.y + parent.height);
    expect(
      layoutSequence({ ...document, fragments: [...document.fragments].reverse() }).messages,
    ).toEqual(layout.messages);
  });

  it("reserves wrapped alt conditions after nested self-calls and before the next branch", () => {
    const condition =
      "Проверить доступность пользовательской сессии и разрешение на выполнение следующего действия "
        .repeat(4)
        .trim();
    const document = documentOf({
      formatVersion: 2,
      participants: participants.slice(0, 2),
      messages: [
        {
          id: "first",
          fromId: "buyer",
          toId: "buyer",
          kind: "request",
          label: "Self call",
          description: "",
        },
        {
          id: "second",
          fromId: "buyer",
          toId: "shop",
          kind: "request",
          label: "Next",
          description: "",
        },
      ],
      fragments: [
        {
          id: "retry",
          kind: "loop",
          label: "Retry",
          fromMessageId: "first",
          toMessageId: "first",
          parentFragmentId: "choice",
          parentBranchId: "yes",
        },
        {
          id: "choice",
          kind: "alt",
          label: "Choose",
          fromMessageId: "first",
          toMessageId: "second",
          branches: [
            { id: "yes", label: "Ready", fromMessageId: "first", toMessageId: "first" },
            { id: "no", label: condition, fromMessageId: "second", toMessageId: "second" },
          ],
        },
      ],
    });
    const layout = layoutSequence(document);
    const frame = layout.fragments.find(({ id }) => id === "choice")!;
    const retry = layout.fragments.find(({ id }) => id === "retry")!;
    const [first, second] = layout.messages;
    const [yes, no] = frame.branches!;
    expect(yes!.labelLines.join(" ")).toBe("Ready");
    expect(retry.y).toBeGreaterThanOrEqual(yes!.y + yes!.height);
    expect(no!.separatorY).toBeGreaterThan(retry.y + retry.height);
    expect(no!.separatorY).toBeGreaterThan(first!.target.y);
    expect(no!.labelLines.length).toBeGreaterThan(1);
    expect(no!.labelLines.join(" ")).toBe(condition);
    expect(second!.label.y).toBeGreaterThanOrEqual(no!.y + no!.height);
    const short = structuredClone(document);
    short.fragments[1]!.branches![1]!.label = "else";
    expect(layout.height).toBeGreaterThan(layoutSequence(short).height);
  });

  it("keeps sixteen levels inside the canvas and wraps titles without crossing child headers", () => {
    const label = "Длинный заголовок блока с полным описанием условия ".repeat(8).trim();
    const layout = layoutSequence(
      documentOf({
        formatVersion: 2,
        participants: participants.slice(0, 1),
        messages: [
          {
            id: "call",
            fromId: "buyer",
            toId: "buyer",
            kind: "request",
            label: "Call",
            description: "",
          },
        ],
        fragments: Array.from({ length: 16 }, (_, index) => ({
          id: `level-${index}`,
          kind: "opt",
          label,
          fromMessageId: "call",
          toMessageId: "call",
          ...(index ? { parentFragmentId: `level-${index - 1}` } : {}),
        })).reverse() as CanvasDocument["fragments"],
      }),
    );
    const frames = [...layout.fragments].sort((a, b) => a.depth! - b.depth!);
    expect(frames[0]!.x).toBeGreaterThanOrEqual(0);
    for (const [index, frame] of frames.entries()) {
      expect(frame.labelLines!.join(" ")).toBe(label);
      expect(frame.labelLines!.length).toBeGreaterThan(1);
      expect(frame.x + frame.width).toBeLessThan(layout.width);
      const inner = frames[index + 1];
      if (inner) {
        expect(inner.x).toBeGreaterThan(frame.x);
        expect(inner.y).toBeGreaterThanOrEqual(frame.y + frame.headerHeight!);
        expect(inner.y + inner.height).toBeLessThan(frame.y + frame.height);
      }
    }
    const innermost = frames.at(-1)!;
    expect(layout.messages[0]!.label.y).toBeGreaterThanOrEqual(
      innermost.y + innermost.headerHeight!,
    );
    expect(layout.messages[0]!.source.x).toBe(layout.participants[0]!.x);
  });

  it.each(["request", "response", "event"] as const)(
    "wraps a long %s label inside its lifelines",
    (kind) => {
      const label =
        "Проверить сессию пользователя и загрузить список доступных заданий и приглашений";
      const layout = layoutSequence(
        documentOf({
          participants: participants.slice(0, 2),
          messages: [
            {
              id: "long",
              fromId: kind === "response" ? "shop" : "buyer",
              toId: kind === "response" ? "buyer" : "shop",
              kind,
              label,
              description: "",
            },
            {
              id: "next",
              fromId: "buyer",
              toId: "shop",
              kind: "request",
              label: "Следующий",
              description: "",
            },
          ],
        }),
      );
      const [long, next] = layout.messages;
      expect(long!.label.x).toBeGreaterThan(144);
      expect(long!.label.x + long!.label.width).toBeLessThan(400);
      expect(long!.labelLines.length).toBeGreaterThan(1);
      expect(long!.labelLines.join(" ")).toBe(label);
      expect(long!.label.height).toBeGreaterThan(28);
      expect(long!.label.y + long!.label.height).toBeLessThan(long!.source.y - 3);
      expect(next!.label.y).toBeGreaterThan(long!.target.y);
    },
  );

  it("wraps API paths below the whole message and reflows when lanes widen", () => {
    const label = "Получить подробную информацию о текущей сессии пользователя и его заданиях";
    const path = "/api/v1/quizzes/student-sessions/by-token/very-long-path-without-spaces";
    const document = documentOf({
      participants: participants.slice(0, 2),
      messages: [
        {
          id: "api",
          fromId: "buyer",
          toId: "shop",
          kind: "request",
          label,
          description: "",
          operation: { contractId: "api", operationKey: "get-session" },
        },
      ],
      contracts: [
        {
          id: "api",
          name: "API",
          document: {
            openapi: "3.1.0",
            info: { title: "API", version: "1" },
            paths: {
              [path]: {
                get: {
                  "x-mocker-canvas-operation-id": "get-session",
                  responses: { "200": { description: "OK" } },
                },
              },
            },
          },
        },
      ],
    });
    const narrow = layoutSequence(document).messages[0]!;
    expect(narrow.operationLines.length).toBeGreaterThan(1);
    expect(narrow.operationLines.join("").replace(/\s/g, "")).toBe(`GET${path}`);
    const wider = layoutSequence({
      ...document,
      participants: document.participants.map((p, i) => ({ ...p, offsetX: i ? 400 : 0 })),
    }).messages[0]!;
    expect(wider.label.width).toBeGreaterThan(narrow.label.width);
    expect(wider.label.height).toBeLessThan(narrow.label.height);
  });

  it("wraps self-call cards on the right without crossing the next participant", () => {
    const layout = layoutSequence(
      documentOf({
        participants,
        messages: [
          {
            id: "self",
            fromId: "buyer",
            toId: "buyer",
            kind: "event",
            label:
              "Сформировать и подписать гостевой JWT и сохранить данные новой пользовательской сессии",
            description: "",
          },
        ],
      }),
    );
    const message = layout.messages[0]!;
    expect(message.label.x).toBeGreaterThan(message.source.x);
    expect(message.label.x + message.label.width).toBeLessThan(layout.participants[1]!.x);
    expect(message.labelLines.length).toBeGreaterThan(1);
  });

  it("grows participant cards and starts messages below the tallest name", () => {
    const name = "Platform API · auth + quiz · пользовательские сессии";
    const layout = layoutSequence(
      documentOf({
        participants: [participants[0]!, { ...participants[1]!, name }],
        messages: [
          {
            id: "call",
            fromId: "buyer",
            toId: "shop",
            kind: "request",
            label: "Call",
            description: "",
          },
        ],
      }),
    );
    const short = layout.participants[0]!;
    const tall = layout.participants[1]!;
    expect(short.header.height).toBe(52);
    expect(tall.header.height).toBeGreaterThan(short.header.height);
    expect(tall.nameLines.join(" ")).toBe(name);
    expect(tall.lifeline.source.y).toBe(tall.header.y + tall.header.height);
    expect(layout.messages[0]!.label.y).toBeGreaterThan(tall.lifeline.source.y);
  });

  it("adds space before a participant and moves following columns and arrows with it", () => {
    const document = documentOf({
      participants: participants.map((p, index) => ({ ...p, offsetX: index === 1 ? 300 : 0 })),
      messages: [
        {
          id: "call",
          fromId: "buyer",
          toId: "shop",
          kind: "request",
          label: "Call",
          description: "",
        },
      ],
    });
    const layout = layoutSequence(document);
    expect(layout.participants.map(({ x }) => x)).toEqual([144, 700, 956]);
    expect(layout.messages[0]!.target.x).toBe(700);
    expect(layout.width).toBeGreaterThan(1036);
  });

  it("keeps fragment headers below the preceding self-call and above API cards", () => {
    const document = documentOf({
      participants,
      messages: [
        {
          id: "self",
          fromId: "buyer",
          toId: "buyer",
          kind: "request",
          label: "Прочитать cookie",
          description: "",
        },
        {
          id: "guest",
          fromId: "buyer",
          toId: "shop",
          kind: "request",
          label: "Создать гостя",
          description: "",
          operation: { contractId: "missing", operationKey: "missing" },
        },
        {
          id: "next",
          fromId: "shop",
          toId: "buyer",
          kind: "response",
          label: "Ответ",
          description: "",
        },
      ],
      fragments: [
        {
          id: "opt",
          kind: "opt",
          label: "Первый вход",
          fromMessageId: "guest",
          toMessageId: "guest",
        },
      ],
    });
    const layout = layoutSequence(document);
    const [self, guest, next] = layout.messages;
    const frame = layout.fragments[0]!;
    expect(frame.y).toBeGreaterThan(self!.target.y + 4);
    expect(guest!.label.y).toBeGreaterThan(frame.y + 20);
    expect(guest!.label.height).toBe(42);
    expect(guest!.label.y + guest!.label.height).toBeLessThan(guest!.source.y - 3);
    expect(next!.label.y).toBeGreaterThan(frame.y + frame.height);
  });

  it("encloses wide cards and self-call paths inside nested fragments", () => {
    const layout = layoutSequence(
      documentOf({
        participants: participants.slice(0, 1),
        messages: [
          {
            id: "self",
            fromId: "buyer",
            toId: "buyer",
            kind: "request",
            label: "Сформировать и подписать гостевой JWT",
            description: "",
          },
        ],
        fragments: [
          {
            id: "outer",
            kind: "opt",
            label: "Первый вход",
            fromMessageId: "self",
            toMessageId: "self",
          },
          {
            id: "inner",
            kind: "loop",
            label: "Повтор",
            fromMessageId: "self",
            toMessageId: "self",
          },
        ],
      }),
    );
    const [outer, inner] = layout.fragments;
    const message = layout.messages[0]!;
    expect(inner!.y).toBeGreaterThan(outer!.y + 20);
    expect(message.label.y).toBeGreaterThan(inner!.y + 20);
    expect(inner!.x).toBeLessThan(message.label.x);
    expect(inner!.x + inner!.width).toBeGreaterThan(message.label.x + message.label.width);
    expect(outer!.x).toBeLessThan(inner!.x);
    expect(outer!.y + outer!.height).toBeGreaterThan(inner!.y + inner!.height);
    expect(inner!.y + inner!.height).toBeGreaterThan(message.target.y);
  });

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
      width: 600,
    });
    const frame = layout.fragments[0]!;
    expect(frame.y).toBeGreaterThan(layout.messages[0]!.target.y);
    expect(frame.y + 20).toBeLessThan(layout.messages[1]!.label.y);
    expect(frame.y + frame.height).toBeGreaterThan(layout.messages[2]!.target.y);
  });
});

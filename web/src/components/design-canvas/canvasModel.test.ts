import { afterEach, describe, expect, it, vi } from "vitest";
import { renameOperation, type ApiDocument } from "../api-designer/documentModel";
import {
  OPERATION_KEY,
  addLocalOperation,
  emptyCanvas,
  exampleCanvas,
  importContract,
  moveMessage,
  moveParticipant,
  removeMessage,
  removeParticipant,
  resolveOperation,
  updateMessage,
} from "./canvasModel";
import type { CanvasDocument } from "./types";

afterEach(() => vi.unstubAllGlobals());

function documentFixture(): CanvasDocument {
  return {
    formatVersion: 1,
    title: "Оплата заказа",
    participants: [
      { id: "client", name: "Клиент", kind: "client", description: "" },
      { id: "orders", name: "Заказы", kind: "service", description: "" },
      { id: "billing", name: "Биллинг", kind: "service", description: "" },
    ],
    messages: [
      {
        id: "create",
        fromId: "client",
        toId: "orders",
        kind: "request",
        label: "Создать заказ",
        description: "",
      },
      {
        id: "charge",
        fromId: "orders",
        toId: "billing",
        kind: "request",
        label: "Списать оплату",
        description: "",
      },
      {
        id: "charged",
        fromId: "billing",
        toId: "orders",
        kind: "response",
        label: "Оплата принята",
        description: "",
        replyToId: "charge",
      },
      {
        id: "created",
        fromId: "orders",
        toId: "client",
        kind: "response",
        label: "Заказ создан",
        description: "",
        replyToId: "create",
      },
    ],
    fragments: [
      {
        id: "payment",
        kind: "opt",
        label: "Есть оплата",
        fromMessageId: "charge",
        toMessageId: "charged",
      },
      {
        id: "whole-flow",
        kind: "loop",
        label: "Повтор",
        fromMessageId: "create",
        toMessageId: "created",
      },
    ],
    contracts: [],
  };
}

describe("canvas model", () => {
  it("creates independent empty documents without placeholder participants", () => {
    const first = emptyCanvas();
    const second = emptyCanvas();

    first.participants.push({ id: "changed", name: "Изменён", kind: "other", description: "" });

    expect(second.participants).toEqual([]);
    expect(second.messages).toEqual([]);
    expect(second.contracts).toEqual([]);
  });

  it("provides the representative three-participant example", () => {
    const example = exampleCanvas();
    const database = example.participants.find((participant) => participant.kind === "database");
    const selfCall = example.messages.find((message) => message.fromId === message.toId);
    const repeatedEndpoints = example.messages.filter(
      (message) => message.fromId === "client" && message.toId === "orders",
    );
    const bound = example.messages.find((message) => message.operation !== undefined);

    expect(example.participants).toHaveLength(3);
    expect(database).toBeDefined();
    expect(selfCall).toBeDefined();
    expect(repeatedEndpoints.length).toBeGreaterThanOrEqual(2);
    expect(example.messages.some((message) => message.kind === "response")).toBe(true);
    expect(example.fragments.some((fragment) => fragment.kind === "opt")).toBe(true);
    expect(bound?.operation).toBeDefined();
    expect(resolveOperation(example, bound!.operation!)).toMatchObject({
      location: { method: "post", path: "/orders" },
    });
  });

  it("reorders participants immutably while preserving their identities", () => {
    const original = documentFixture();
    const moved = moveParticipant(original, "billing", 0);

    expect(moved.participants.map(({ id }) => id)).toEqual(["billing", "client", "orders"]);
    expect(original.participants.map(({ id }) => id)).toEqual(["client", "orders", "billing"]);
    expect(moved.messages).toBe(original.messages);
  });

  it("reorders messages without rewriting fragment boundaries", () => {
    const original = documentFixture();
    const moved = moveMessage(original, "created", 1);

    expect(moved.messages.map(({ id }) => id)).toEqual(["create", "created", "charge", "charged"]);
    expect(moved.fragments).toBe(original.fragments);
    expect(moved.fragments[1]).toMatchObject({
      fromMessageId: "create",
      toMessageId: "created",
    });
  });

  it("rejects reordering a response before its request", () => {
    expect(() => moveMessage(documentFixture(), "charged", 0)).toThrow(/ответ.*раньше запроса/i);
  });

  it("rejects reordering a request after its response", () => {
    expect(() => moveMessage(documentFixture(), "charge", 3)).toThrow(/ответ.*раньше запроса/i);
  });

  it.each([
    ["validate-order", 5],
    ["refresh-order", 0],
  ])("rejects moving %s past the other boundary of a fragment", (id, index) => {
    const original = exampleCanvas();
    original.fragments[0]!.toMessageId = "refresh-order";
    const before = structuredClone(original);

    expect(() => moveMessage(original, id, index)).toThrow(/блок.*начал.*конц/i);
    expect(original).toEqual(before);
  });

  it("turns every linked response around after request endpoints change", () => {
    const original = documentFixture();
    original.messages.push({
      id: "created-again",
      fromId: "orders",
      toId: "client",
      kind: "response",
      label: "Повторный ответ",
      description: "",
      replyToId: "create",
    });

    const updated = updateMessage(original, "create", { fromId: "billing" });

    expect(updated.messages.find(({ id }) => id === "create")).toMatchObject({
      fromId: "billing",
      toId: "orders",
    });
    expect(updated.messages.filter(({ replyToId }) => replyToId === "create")).toEqual([
      expect.objectContaining({ id: "created", fromId: "orders", toId: "billing" }),
      expect.objectContaining({ id: "created-again", fromId: "orders", toId: "billing" }),
    ]);
    expect(original.messages.find(({ id }) => id === "created")).toMatchObject({
      fromId: "orders",
      toId: "client",
    });
  });

  it("detaches linked responses when a request changes to another message kind", () => {
    const original = documentFixture();

    const updated = updateMessage(original, "charge", { kind: "event" });
    const response = updated.messages.find(({ id }) => id === "charged")!;

    expect(updated.messages.find(({ id }) => id === "charge")?.kind).toBe("event");
    expect(response).not.toHaveProperty("replyToId");
    expect(original.messages.find(({ id }) => id === "charged")?.replyToId).toBe("charge");
  });

  it("removes a participant with incident messages, their replies and dangling fragments", () => {
    const original = documentFixture();
    const withoutBilling = removeParticipant(original, "billing");

    expect(withoutBilling.participants.map(({ id }) => id)).toEqual(["client", "orders"]);
    expect(withoutBilling.messages.map(({ id }) => id)).toEqual(["create", "created"]);
    expect(withoutBilling.fragments.map(({ id }) => id)).toEqual(["whole-flow"]);
    expect(original.messages).toHaveLength(4);
  });

  it("removes replies and fragment ranges when their request is removed", () => {
    const original = documentFixture();
    const withoutRequest = removeMessage(original, "create");

    expect(withoutRequest.messages.map(({ id }) => id)).toEqual(["charge", "charged"]);
    expect(withoutRequest.fragments.map(({ id }) => id)).toEqual(["payment"]);
  });
});

describe("canvas identifiers", () => {
  it("uses native randomUUID when available", () => {
    const id = "be89b2b2-2260-4f54-9321-bf15d9e8a301";
    vi.stubGlobal("crypto", { randomUUID: () => id });

    expect(exampleCanvas().messages[0]!.operation!.operationKey).toBe(id);
  });

  it.each([
    [0, "00000000-0000-4000-8000-000000000000"],
    [255, "ffffffff-ffff-4fff-bfff-ffffffffffff"],
  ])("creates RFC 9562 UUID v4 identifiers without randomUUID (byte %i)", (byte, expected) => {
    vi.stubGlobal("crypto", {
      getRandomValues: (bytes: Uint8Array) => bytes.fill(byte),
    });

    expect(exampleCanvas().messages[0]!.operation!.operationKey).toBe(expected);
  });
});

describe("canvas contracts", () => {
  const sourceDocument: ApiDocument = {
    openapi: "3.1.0",
    info: { title: "Orders", version: "1.0.0" },
    paths: {
      "/orders/{id}": {
        get: { operationId: "getOrder", responses: { "200": { description: "OK" } } },
        delete: { operationId: "deleteOrder", responses: { "204": { description: "Deleted" } } },
      },
    },
  };

  it("imports an independent clone and assigns a distinct stable key to every operation", () => {
    const imported = importContract("Заказы", sourceDocument, { designId: 7, revisionId: 11 });
    const paths = imported.document.paths as Record<
      string,
      Record<string, Record<string, unknown>>
    >;
    const getKey = paths["/orders/{id}"]!.get![OPERATION_KEY];
    const deleteKey = paths["/orders/{id}"]!.delete![OPERATION_KEY];

    expect(imported.source).toEqual({ designId: 7, revisionId: 11 });
    expect(getKey).toEqual(expect.any(String));
    expect(deleteKey).toEqual(expect.any(String));
    expect(deleteKey).not.toBe(getKey);
    expect(sourceDocument).toEqual({
      openapi: "3.1.0",
      info: { title: "Orders", version: "1.0.0" },
      paths: {
        "/orders/{id}": {
          get: { operationId: "getOrder", responses: { "200": { description: "OK" } } },
          delete: {
            operationId: "deleteOrder",
            responses: { "204": { description: "Deleted" } },
          },
        },
      },
    });
  });

  it("resolves a bound operation after its method and path are renamed", () => {
    const imported = importContract("Заказы", sourceDocument);
    const operation = (
      imported.document.paths as Record<string, Record<string, Record<string, unknown>>>
    )["/orders/{id}"]!.get![OPERATION_KEY] as string;
    const renamed = renameOperation(
      imported.document,
      { method: "get", path: "/orders/{id}" },
      { method: "post", path: "/purchases/{purchaseId}" },
    );
    const document = { ...emptyCanvas(), contracts: [{ ...imported, document: renamed }] };

    expect(
      resolveOperation(document, { contractId: imported.id, operationKey: operation }),
    ).toEqual({
      contract: document.contracts[0],
      location: { method: "post", path: "/purchases/{purchaseId}" },
    });
    expect(
      resolveOperation(document, { contractId: imported.id, operationKey: "missing" }),
    ).toBeNull();
  });

  it("adds a local POST /request with a 200 response and binds only the selected message", () => {
    const original = documentFixture();
    const withLocalOperation = addLocalOperation(original, "create");
    const binding = withLocalOperation.messages[0]!.operation;
    const resolved = resolveOperation(withLocalOperation, binding!);

    expect(withLocalOperation.contracts).toHaveLength(1);
    expect(resolved?.location).toEqual({ method: "post", path: "/request" });
    expect(resolved?.contract.document).toMatchObject({
      paths: { "/request": { post: { responses: { "200": { description: "Успешный ответ" } } } } },
    });
    expect(original.contracts).toEqual([]);
    expect(original.messages[0]!.operation).toBeUndefined();
  });

  it("does not create another local contract for an already bound message", () => {
    const once = addLocalOperation(documentFixture(), "create");
    const twice = addLocalOperation(once, "create");

    expect(twice).toBe(once);
    expect(twice.contracts).toHaveLength(1);
  });
});

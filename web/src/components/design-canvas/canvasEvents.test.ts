import { describe, expect, it } from "vitest";
import { emptyCanvas } from "./canvasModel";
import {
  bindEventOperation,
  createEventModel,
  eventDependents,
  removeEventEntity,
} from "./canvasEvents";
import type { CanvasDocument } from "./types";

function scenario(): CanvasDocument {
  return {
    ...emptyCanvas(),
    participants: [
      { id: "orders", name: "Orders", kind: "service", description: "" },
      { id: "queue", name: "Kafka", kind: "queue", description: "" },
    ],
    messages: [
      {
        id: "arrow",
        fromId: "orders",
        toId: "queue",
        kind: "event",
        label: "OrderCreated",
        description: "",
      },
    ],
  };
}

describe("event model", () => {
  it("upgrades to v3 in one copy and keeps source JSON text unchanged", () => {
    const source = '{"minimum":90071992547409931234567890123456789}';
    const document = createEventModel(scenario());
    document.eventModel!.schemas.push({
      id: "payload",
      name: "Payload",
      description: "",
      schemaJSON: source,
    });
    expect(document.formatVersion).toBe(3);
    expect(document.eventModel!.schemas[0]!.schemaJSON).toBe(source);
    expect(JSON.parse(JSON.stringify(document)).eventModel.schemas[0].schemaJSON).toBe(source);
  });

  it("binds an operation once and refuses referenced deletion", () => {
    const document = createEventModel(scenario());
    const model = document.eventModel!;
    model.messages.push({ id: "created", name: "Created", description: "", examples: [] });
    model.channels.push({
      id: "orders-topic",
      name: "Orders",
      description: "",
      address: "orders.events",
      serverIds: [],
      messageIds: ["created"],
    });
    model.contracts.push({
      id: "orders-events",
      name: "Orders events",
      description: "",
      participantId: "orders",
      version: "1.0.0",
      operations: [
        {
          id: "send-created",
          name: "Send created",
          description: "",
          action: "send",
          channelId: "orders-topic",
          messageId: "created",
        },
      ],
    });
    const bound = bindEventOperation(document, "arrow", {
      contractId: "orders-events",
      operationId: "send-created",
    });
    expect(bound.messages[0]!.eventBindings).toEqual([
      { contractId: "orders-events", operationId: "send-created" },
    ]);
    expect(
      bindEventOperation(bound, "arrow", {
        contractId: "orders-events",
        operationId: "send-created",
      }),
    ).toEqual(bound);
    expect(eventDependents(bound, "messages", "created")).toContain("канал Orders");
    expect(() => removeEventEntity(bound, "messages", "created")).toThrow(/используется/);
  });

  it("requires explicit replacement of an HTTP binding", () => {
    const document = scenario();
    document.messages[0]!.operation = { contractId: "http", operationKey: "post" };
    expect(() =>
      bindEventOperation(createEventModel(document), "arrow", {
        contractId: "x",
        operationId: "y",
      }),
    ).toThrow(/HTTP/);
  });

  it("blocks deletion of schemas reached through nested and cyclic refs", () => {
    const document = createEventModel(scenario());
    const model = document.eventModel!;
    model.schemas.push(
      {
        id: "root",
        name: "Root",
        description: "",
        schemaJSON: '{"properties":{"child":{"$ref":"#/components/schemas/branch"}}}',
      },
      {
        id: "branch",
        name: "Branch",
        description: "",
        schemaJSON:
          '{"allOf":[{"$ref":"#/components/schemas/leaf"},{"$ref":"#/components/schemas/root"}]}',
      },
      { id: "leaf", name: "Leaf", description: "", schemaJSON: '{"type":"string"}' },
    );
    model.messages.push({
      id: "created",
      name: "Created",
      description: "",
      payloadSchemaId: "root",
      examples: [],
    });
    expect(eventDependents(document, "schemas", "leaf")).toContain("тип события Created");
    expect(() => removeEventEntity(document, "schemas", "leaf")).toThrow(/тип события Created/);
    expect(eventDependents(document, "schemas", "unrelated")).toEqual([]);
  });

  it("includes alternate channel messages when checking schema dependencies", () => {
    const document = createEventModel(scenario());
    const model = document.eventModel!;
    model.schemas.push({
      id: "alternate",
      name: "Alternate",
      description: "",
      schemaJSON: '{"type":"object"}',
    });
    model.messages.push(
      { id: "created", name: "Created", description: "", examples: [] },
      {
        id: "cancelled",
        name: "Cancelled",
        description: "",
        payloadSchemaId: "alternate",
        examples: [],
      },
    );
    model.channels.push({
      id: "topic",
      name: "Orders",
      description: "",
      address: "orders.events",
      serverIds: [],
      messageIds: ["created", "cancelled"],
    });
    model.contracts.push({
      id: "orders-events",
      name: "Orders events",
      description: "",
      participantId: "orders",
      version: "1",
      operations: [
        {
          id: "send",
          name: "Send",
          description: "",
          action: "send",
          channelId: "topic",
          messageId: "created",
        },
      ],
    });
    expect(eventDependents(document, "schemas", "alternate")).toContain("контракт Orders events");
    expect(() => removeEventEntity(document, "schemas", "alternate")).toThrow(
      /контракт Orders events/,
    );
  });

  it("blocks removal of a topic referenced by a consumer failure route", () => {
    const document = createEventModel(scenario());
    const model = document.eventModel!;
    model.messages.push({ id: "created", name: "Created", description: "", examples: [] });
    for (const id of ["source", "retry"]) {
      model.channels.push({
        id,
        name: id,
        description: "",
        address: id,
        serverIds: [],
        messageIds: ["created"],
      });
    }
    model.contracts.push({
      id: "events",
      name: "Events",
      description: "",
      participantId: "orders",
      version: "1",
      operations: [
        {
          id: "consume",
          name: "Consume",
          description: "",
          action: "receive",
          channelId: "source",
          messageId: "created",
          failureRoutes: { retryChannelId: "retry" },
        },
      ],
    });
    expect(eventDependents(document, "channels", "retry")).toContain("операция Events/Consume");
    expect(() => removeEventEntity(document, "channels", "retry")).toThrow(
      /операция Events\/Consume/,
    );
  });
});

import { useState } from "react";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import { renderWithProviders } from "@/test/render";
import { emptyCanvas } from "./canvasModel";
import { EventModelEditor } from "./EventModelEditor";
import type { CanvasDocument } from "./types";

const store = createFormDraftStore();
let latest: CanvasDocument;
function initial(): CanvasDocument {
  return {
    ...emptyCanvas(),
    participants: [
      { id: "orders", name: "Orders", kind: "service", description: "" },
      { id: "notifications", name: "Notifications", kind: "service", description: "" },
    ],
    messages: [
      {
        id: "created",
        fromId: "orders",
        toId: "notifications",
        kind: "event",
        label: "OrderCreated",
        description: "",
      },
    ],
  };
}

function Harness() {
  const [document, setDocument] = useState(initial);
  const [opened, setOpened] = useState(true);
  return (
    <>
      <button onClick={() => setOpened(true)}>Открыть редактор</button>
      <EventModelEditor
        opened={opened}
        document={document}
        onChange={(next) => {
          latest = next;
          setDocument(next);
        }}
        onClose={() => setOpened(false)}
        formStore={store}
        arrowId="created"
      />
    </>
  );
}

it("authors shared topic and event for both sides, preserves JSON text and unfinished draft", async () => {
  store.clear();
  const user = userEvent.setup();
  renderWithProviders(<Harness />);
  const dialog = screen.getByRole("dialog", { name: "События Kafka и AsyncAPI" });
  await user.click(within(dialog).getByRole("button", { name: "Создать topic" }));
  await user.clear(within(dialog).getByLabelText("Адрес topic"));
  await user.type(within(dialog).getByLabelText("Адрес topic"), "orders.events");
  await user.click(within(dialog).getByRole("button", { name: "Создать тип события" }));
  await user.clear(within(dialog).getByLabelText("Название"));
  await user.type(within(dialog).getByLabelText("Название"), "OrderCreated");
  await user.selectOptions(within(dialog).getByLabelText("Раздел"), "schemas");
  await user.click(within(dialog).getByRole("button", { name: /Добавить · JSON-схемы/ }));
  const exact = '{"const":90071992547409931234567890123456789}';
  fireEvent.change(within(dialog).getByLabelText("JSON Schema Draft 07"), {
    target: { value: exact },
  });
  expect(latest.eventModel?.schemas[0]?.schemaJSON).toBe(exact);
  await user.click(within(dialog).getByRole("button", { name: /Отправляет · Orders/ }));
  await user.click(within(dialog).getByRole("button", { name: /Получает · Notifications/ }));
  expect(latest.messages[0]?.eventBindings).toHaveLength(2);
  expect(latest.eventModel?.contracts).toHaveLength(2);
  expect(latest.eventModel?.channels[0]?.messageIds).toEqual([latest.eventModel?.messages[0]?.id]);
  fireEvent.change(within(dialog).getByLabelText("JSON Schema Draft 07"), {
    target: { value: "{" },
  });
  expect(store.get(`/event-schema/${latest.eventModel?.schemas[0]?.id}/schemaJSON`)?.source).toBe(
    "{",
  );
  await user.click(within(dialog).getByRole("button", { name: "Закрыть" }));
  await user.click(screen.getByRole("button", { name: "Открыть редактор" }));
  expect(screen.getByLabelText("JSON Schema Draft 07")).toHaveValue("{");
  expect(latest.eventModel?.schemas[0]?.schemaJSON).toBe(exact);
});

it("removes an example draft and moves later drafts with their examples", async () => {
  const drafts = createFormDraftStore();
  const document: CanvasDocument = {
    ...initial(),
    formatVersion: 3,
    eventModel: {
      servers: [],
      channels: [],
      schemas: [],
      contracts: [],
      messages: [
        {
          id: "type",
          name: "Created",
          description: "",
          examples: [
            { name: "First", payloadJSON: "{}" },
            { name: "Second", payloadJSON: "{}" },
          ],
        },
      ],
    },
  };
  drafts.set("/event-message/type/examples/0/payloadJSON", {
    source: "{",
    propertySource: "",
    error: "invalid",
  });
  drafts.set("/event-message/type/examples/1/payloadJSON", {
    source: "[",
    propertySource: "",
    error: "invalid",
  });
  let next = document;
  renderWithProviders(
    <EventModelEditor
      opened
      document={document}
      onChange={(value) => {
        next = value;
      }}
      onClose={() => {}}
      formStore={drafts}
      target={{ kind: "event-message", id: "type" }}
    />,
  );
  await userEvent.click(screen.getAllByRole("button", { name: "Удалить пример" })[0]!);
  expect(next.eventModel?.messages[0]?.examples).toEqual([{ name: "Second", payloadJSON: "{}" }]);
  expect(drafts.get("/event-message/type/examples/0/payloadJSON")?.source).toBe("[");
  expect(drafts.get("/event-message/type/examples/1/payloadJSON")).toBeUndefined();
});

it.each([
  ["event-schema", "payload", "/eventModel/schemas/0/schemaJSON", "JSON Schema Draft 07", 0],
  [
    "event-message",
    "type",
    "/eventModel/messages/0/examples/1/payloadJSON",
    "Payload JSON · пример 2",
    0,
  ],
  ["event-contract", "events", "/eventModel/contracts/0/operations/1/name", "Название операции", 1],
  [
    "event-contract",
    "events",
    "/eventModel/contracts/0/operations/1/apiLinks/0",
    "Операция API для связи",
    1,
  ],
  [
    "event-contract",
    "events",
    "/eventModel/contracts/0/operations/1/stateLinks/0",
    "Переход для связи",
    1,
  ],
] as const)("focuses the %s diagnostic field at %s", async (kind, id, pointer, label, index) => {
  const document: CanvasDocument = {
    ...initial(),
    formatVersion: 3,
    eventModel: {
      servers: [],
      schemas: [{ id: "payload", name: "Payload", description: "", schemaJSON: "{}" }],
      messages: [
        {
          id: "type",
          name: "Created",
          description: "",
          examples: [
            { name: "First", payloadJSON: "{}" },
            { name: "Second", payloadJSON: "{}" },
          ],
        },
      ],
      channels: [
        {
          id: "topic",
          name: "Topic",
          description: "",
          address: "orders.events",
          serverIds: [],
          messageIds: ["type"],
        },
      ],
      contracts: [
        {
          id: "events",
          name: "Events",
          description: "",
          participantId: "orders",
          version: "1",
          operations: [
            {
              id: "one",
              name: "First",
              description: "",
              action: "send",
              channelId: "topic",
              messageId: "type",
            },
            {
              id: "two",
              name: "Second",
              description: "",
              action: "receive",
              channelId: "topic",
              messageId: "type",
            },
          ],
        },
      ],
    },
  };
  renderWithProviders(
    <EventModelEditor
      opened
      document={document}
      onChange={() => {}}
      onClose={() => {}}
      formStore={createFormDraftStore()}
      target={{ kind, id, pointer }}
    />,
  );
  await waitFor(() => expect(screen.getAllByLabelText(label)[index]).toHaveFocus());
});

it("edits consumer-specific routes and embedded API/state links in the local document", async () => {
  const document: CanvasDocument = {
    ...initial(),
    formatVersion: 3,
    contracts: [
      {
        id: "http",
        name: "Orders HTTP",
        mode: "copy",
        document: {
          openapi: "3.1.0",
          info: { title: "Orders", version: "1" },
          paths: {
            "/orders": {
              post: {
                "x-mocker-canvas-operation-id": "create-order",
                responses: { "200": { description: "OK" } },
              },
            },
          },
          "x-mocker-state-diagrams": {
            formatVersion: 1,
            diagrams: [
              {
                id: "order",
                name: "Order",
                initialStateId: "new",
                states: [{ id: "new", name: "New", x: 0, y: 0, terminal: false }],
                transitions: [
                  {
                    id: "created",
                    name: "Created",
                    from: "new",
                    to: "new",
                    patchJSON: "{}",
                    responseStatus: 200,
                  },
                ],
              },
            ],
          },
        },
      },
    ],
    eventModel: {
      servers: [],
      schemas: [],
      messages: [{ id: "created", name: "Created", description: "", examples: [] }],
      channels: ["source", "retry", "dlq"].map((id) => ({
        id,
        name: id,
        description: "",
        address: id,
        serverIds: [],
        messageIds: ["created"],
      })),
      contracts: [
        {
          id: "events",
          name: "Events",
          description: "",
          participantId: "orders",
          version: "1",
          operations: [
            {
              id: "receive",
              name: "Receive",
              description: "",
              action: "receive",
              channelId: "source",
              messageId: "created",
              kafka: { groupId: "billing" },
            },
          ],
        },
      ],
    },
  };
  let latest = document;
  function LocalEditor() {
    const [value, setValue] = useState(document);
    return (
      <EventModelEditor
        opened
        document={value}
        onChange={(next) => {
          latest = next;
          setValue(next);
        }}
        onClose={() => {}}
        formStore={createFormDraftStore()}
        target={{ kind: "event-contract", id: "events" }}
      />
    );
  }
  renderWithProviders(<LocalEditor />);
  await userEvent.selectOptions(screen.getByLabelText("Retry topic"), "retry");
  await userEvent.selectOptions(screen.getByLabelText("Dead-letter topic"), "dlq");
  await userEvent.selectOptions(
    screen.getByLabelText("Операция API для связи"),
    JSON.stringify(["http", "create-order"]),
  );
  await userEvent.click(screen.getByRole("button", { name: "Добавить связь API" }));
  await userEvent.selectOptions(
    screen.getByLabelText("Переход для связи"),
    JSON.stringify(["http", "order", "created"]),
  );
  await userEvent.click(screen.getByRole("button", { name: "Добавить связь с переходом" }));
  expect(latest.eventModel?.contracts[0]?.operations[0]).toMatchObject({
    kafka: { groupId: "billing" },
    failureRoutes: { retryChannelId: "retry", deadLetterChannelId: "dlq" },
    apiLinks: [{ contractId: "http", operationKey: "create-order" }],
    stateLinks: [{ contractId: "http", diagramId: "order", transitionId: "created" }],
  });
  await userEvent.click(screen.getByRole("button", { name: "Удалить связь API" }));
  expect(latest.eventModel?.contracts[0]?.operations[0]?.apiLinks).toEqual([]);
});

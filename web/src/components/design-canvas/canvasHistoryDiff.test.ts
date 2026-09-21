import { describe, expect, it } from "vitest";
import {
  canvasHistoryHighlights,
  compareCanvasRevisions,
  type CanvasHistoryChange,
} from "./canvasHistoryDiff";
import type { CanvasDocument } from "./types";
import { defaultStepExecution } from "./canvasExecution";

it("shows execution variables and message request settings in revision history", () => {
  const before = document();
  const after = structuredClone(before);
  after.execution = { variables: { token: "initial" } };
  after.messages[0]!.execution = { ...defaultStepExecution(), expectedStatus: 201 };
  const changes = compareCanvasRevisions({ document: before }, { document: after });
  expect(changes.find((entry) => entry.entityType === "scenario")?.fields).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ label: "Переменные исполнения", after: '{"token":"initial"}' }),
    ]),
  );
  expect(changes.find((entry) => entry.entityType === "message")?.fields).toEqual(
    expect.arrayContaining([expect.objectContaining({ label: "Настройки исполнения" })]),
  );
  expect(canvasHistoryHighlights(changes)).toContainEqual({
    kind: "message",
    id: "request",
    status: "changed",
  });
  const reversed = compareCanvasRevisions({ document: after }, { document: before });
  expect(reversed.find((entry) => entry.entityType === "scenario")?.fields[0]?.before).toBe(
    '{"token":"initial"}',
  );
});

function change(overrides: Partial<CanvasHistoryChange>): CanvasHistoryChange {
  return {
    key: "change",
    status: "changed",
    entityType: "message",
    entityId: "request",
    label: "Запрос",
    selection: { kind: "message", id: "request" },
    fields: [],
    ...overrides,
  };
}

describe("canvasHistoryHighlights", () => {
  it("marks a surviving message changed when its contract or form was removed", () => {
    expect(
      canvasHistoryHighlights([
        change({ key: "contract", entityType: "contract", entityId: "api", status: "removed" }),
        change({ key: "form", entityType: "formDraft", entityId: "/api/form", status: "removed" }),
      ]),
    ).toEqual([{ kind: "message", id: "request", status: "changed" }]);
  });

  it.each(["added", "removed"] as const)(
    "preserves a direct %s status regardless of indirect change order",
    (status) => {
      const direct = change({ status });
      const indirect = change({
        key: "contract",
        entityType: "contract",
        entityId: "api",
        status: "removed",
      });
      for (const changes of [
        [direct, indirect],
        [indirect, direct],
      ]) {
        expect(canvasHistoryHighlights(changes)).toEqual([
          { kind: "message", id: "request", status },
        ]);
      }
    },
  );

  it("keeps a direct move when the associated contract also changes", () => {
    expect(
      canvasHistoryHighlights([
        change({ status: "moved" }),
        change({ key: "contract", entityType: "contract", entityId: "api" }),
      ]),
    ).toEqual([{ kind: "message", id: "request", status: "moved" }]);
  });

  it("prioritizes content edits over moves in either order and distinguishes entity kinds", () => {
    const changed = change({ key: "changed" });
    const moved = change({ key: "moved", status: "moved" });
    const participant = change({
      key: "participant",
      entityType: "participant",
      selection: { kind: "participant", id: "request" },
      status: "moved",
    });
    for (const changes of [
      [changed, moved, participant],
      [moved, changed, participant],
    ]) {
      expect(canvasHistoryHighlights(changes)).toEqual([
        { kind: "message", id: "request", status: "changed" },
        { kind: "participant", id: "request", status: "moved" },
      ]);
    }
  });
});

function document(): CanvasDocument {
  return {
    formatVersion: 1,
    title: "Заказы",
    participants: [
      { id: "client", name: "Клиент", kind: "client", description: "" },
      { id: "service", name: "Сервис", kind: "service", description: "" },
      { id: "db", name: "База", kind: "database", description: "" },
    ],
    messages: [
      {
        id: "request",
        fromId: "client",
        toId: "service",
        kind: "request",
        label: "Создать заказ",
        description: "",
        operation: { contractId: "api", operationKey: "create" },
      },
      {
        id: "save",
        fromId: "service",
        toId: "db",
        kind: "request",
        label: "Сохранить",
        description: "",
      },
      {
        id: "reply",
        fromId: "service",
        toId: "client",
        kind: "response",
        label: "Заказ создан",
        description: "",
        replyToId: "request",
      },
    ],
    fragments: [
      {
        id: "loop",
        kind: "loop",
        label: "Повторить",
        fromMessageId: "request",
        toMessageId: "reply",
      },
    ],
    contracts: [
      {
        id: "api",
        name: "Orders API",
        mode: "linked",
        source: { designId: 3, revisionId: 4, version: 2 },
        document: {
          openapi: "3.1.0",
          info: { title: "Orders", version: "1" },
          paths: {
            "/orders": {
              post: { "x-mocker-canvas-operation-id": "create", summary: "Создать", responses: {} },
            },
          },
        },
      },
    ],
  };
}

describe("compareCanvasRevisions", () => {
  it("matches entities by stable ID and reports a rename separately from reorder", () => {
    const before = document();
    const after = structuredClone(before);
    after.participants = [after.participants[1]!, after.participants[0]!, after.participants[2]!];
    after.participants[1]!.name = "Покупатель";
    const changes = compareCanvasRevisions({ document: before }, { document: after });
    expect(changes.filter((change) => change.status === "changed")).toEqual([
      expect.objectContaining({
        entityType: "participant",
        entityId: "client",
        label: "Покупатель",
        selection: { kind: "participant", id: "client" },
        fields: [{ label: "Название", before: "Клиент", after: "Покупатель" }],
      }),
    ]);
    expect(
      changes
        .filter((change) => change.status === "moved")
        .map((change) => change.entityId)
        .sort(),
    ).toEqual(["client", "service"]);
    expect(new Set(changes.map((change) => change.key)).size).toBe(changes.length);
  });

  it("does not mark surviving entities as moved when an item is inserted or removed", () => {
    const before = document();
    const after = structuredClone(before);
    after.participants.splice(1, 0, {
      id: "queue",
      name: "Очередь",
      kind: "queue",
      description: "",
    });
    after.messages.splice(1, 1);
    const changes = compareCanvasRevisions({ document: before }, { document: after });
    expect(changes).toHaveLength(2);
    expect(changes).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          entityType: "participant",
          entityId: "queue",
          status: "added",
          selection: { kind: "participant", id: "queue" },
        }),
        expect.objectContaining({
          entityType: "message",
          entityId: "save",
          status: "removed",
          label: "Сохранить",
          selection: { kind: "message", id: "save" },
        }),
      ]),
    );
  });

  it("shows independent fill, arrow and default color values", () => {
    const before = document();
    before.participants[0]!.color = "#123456";
    before.messages[0]!.color = "#AABBCC";
    const after = structuredClone(before);
    delete after.participants[0]!.color;
    after.messages[0]!.color = "#FFFFFF";
    after.messages[0]!.arrowColor = "#112233";
    const changes = compareCanvasRevisions({ document: before }, { document: after });
    expect(changes[0]!.fields).toEqual([
      { label: "Цвет объекта", before: "#123456", after: "По умолчанию", color: true },
    ]);
    expect(changes[1]!.fields).toEqual([
      { label: "Цвет плашки", before: "#AABBCC", after: "#FFFFFF", color: true },
      { label: "Цвет стрелки", before: "По умолчанию", after: "#112233", color: true },
    ]);
  });

  it("resolves endpoint, reply, fragment and API references to readable names", () => {
    const before = document();
    const after = structuredClone(before);
    after.messages[0]!.toId = "db";
    delete after.messages[0]!.operation;
    after.messages[2]!.replyToId = "save";
    after.fragments[0]!.fromMessageId = "save";
    const changes = compareCanvasRevisions({ document: before }, { document: after });
    expect(changes.find((change) => change.entityId === "request")!.fields).toEqual([
      { label: "Получатель", before: "Сервис", after: "База" },
      { label: "Операция API", before: "Orders API · POST /orders", after: "Не связана" },
    ]);
    expect(changes.find((change) => change.entityId === "reply")!.fields).toContainEqual({
      label: "Ответ на",
      before: "Создать заказ",
      after: "Сохранить",
    });
    expect(changes.find((change) => change.entityId === "loop")!.fields).toContainEqual({
      label: "Первое сообщение",
      before: "Создать заказ",
      after: "Сохранить",
    });
  });

  it("ignores JSON object key order and absent optional form drafts", () => {
    const before = document();
    const after = structuredClone(before);
    after.contracts[0]!.document.info = { version: "1", title: "Orders" };
    expect(
      compareCanvasRevisions({ document: before }, { document: after, formDrafts: {} }),
    ).toEqual([]);
  });

  it("groups nested API fields under the contract and distinguishes missing, null and large integers", () => {
    const before = document();
    before.contracts[0]!.document.components = {
      schemas: { Order: { example: null, maximum: 9007199254740993n } },
    };
    const after = structuredClone(before);
    after.contracts[0]!.document.components = {
      schemas: { Order: { minimum: null, maximum: 9007199254740995n } },
    };
    const changes = compareCanvasRevisions({ document: before }, { document: after });
    expect(changes).toHaveLength(1);
    expect(changes[0]).toMatchObject({
      entityType: "contract",
      entityId: "api",
      label: "Orders API",
      selection: { kind: "message", id: "request" },
    });
    expect(changes[0]!.fields).toEqual(
      expect.arrayContaining([
        { label: "API · Схемы · Order · Пример", before: "null", after: undefined },
        { label: "API · Схемы · Order · Минимум", before: undefined, after: "null" },
        {
          label: "API · Схемы · Order · Максимум",
          before: "9007199254740993",
          after: "9007199254740995",
        },
      ]),
    );
  });

  it("reports contract source pins and modes separately from the API document", () => {
    const before = document();
    const after = structuredClone(before);
    after.contracts[0]!.mode = "copy";
    after.contracts[0]!.source!.revisionId = 5;
    after.contracts[0]!.source!.version = 3;
    expect(compareCanvasRevisions({ document: before }, { document: after })[0]!.fields).toEqual([
      { label: "Режим", before: "Связанный API", after: "Локальная копия" },
      { label: "Ревизия API", before: "4", after: "5" },
      { label: "Версия API", before: "2", after: "3" },
    ]);
  });

  it("makes JSON scalar type changes visible without confusing strings with null or numbers", () => {
    const before = document();
    before.contracts[0]!.document["x-example"] = { count: 1, value: null, flag: true };
    const after = structuredClone(before);
    after.contracts[0]!.document["x-example"] = { count: "1", value: "null", flag: "true" };
    expect(compareCanvasRevisions({ document: before }, { document: after })[0]!.fields).toEqual([
      { label: "API · x-example · count", before: "1", after: '"1"' },
      { label: "API · x-example · flag", before: "true", after: '"true"' },
      { label: "API · x-example · value", before: "null", after: '"null"' },
    ]);
  });

  it("preserves arbitrary JSON names that coincide with prototype properties", () => {
    const before = document();
    const after = structuredClone(before);
    after.contracts[0]!.document["x-example"] = JSON.parse(
      '{"__proto__":null,"constructor":false}',
    );
    expect(compareCanvasRevisions({ document: before }, { document: after })[0]!.fields).toEqual([
      { label: "API · x-example · __proto__", before: undefined, after: "null" },
      { label: "API · x-example · constructor", before: undefined, after: "false" },
    ]);
  });

  it("compares each unfinished form field and preserves invalid source text verbatim", () => {
    const doc = document();
    const pointer = "/canvas-contract/api/paths/~1orders/post/responses";
    const changes = compareCanvasRevisions(
      {
        document: doc,
        formDrafts: {
          all: JSON.stringify({
            [pointer]: { source: "{bad", propertySource: "{}", error: "Ошибка" },
          }),
        },
      },
      {
        document: doc,
        formDrafts: {
          all: JSON.stringify({
            [pointer]: { error: "Ошибка", propertySource: "{}", source: "{broken" },
          }),
        },
      },
    );
    expect(changes).toHaveLength(1);
    expect(changes[0]).toMatchObject({
      entityType: "formDraft",
      entityId: pointer,
      status: "changed",
      selection: { kind: "message", id: "request" },
    });
    expect(changes[0]!.label).toContain("Orders API");
    expect(changes[0]!.label).toContain("POST /orders");
    expect(changes[0]!.fields).toEqual([
      { label: "Текст формы", before: "{bad", after: "{broken" },
    ]);
  });

  it("selects a message bound in both snapshots for contract and form changes", () => {
    const before = document();
    const after = structuredClone(before);
    after.messages.unshift({ ...after.messages[0]!, id: "new-request", label: "Ещё один заказ" });
    after.contracts[0]!.document.info = { title: "Updated API", version: "1" };
    const pointer = "/canvas-contract/api/paths/~1orders/post/responses";
    const changes = compareCanvasRevisions(
      { document: before },
      {
        document: after,
        formDrafts: {
          all: JSON.stringify({ [pointer]: { source: "{broken", propertySource: "{}" } }),
        },
      },
    );
    expect(changes.find((change) => change.entityType === "contract")?.selection).toEqual({
      kind: "message",
      id: "request",
    });
    expect(changes.find((change) => change.entityType === "formDraft")?.selection).toEqual({
      kind: "message",
      id: "request",
    });
  });

  it("ignores form-buffer envelope key order but reports additions, removal and corrupt envelopes", () => {
    const doc = document();
    const beforeDrafts = { all: '{"/field":{"source":"text","propertySource":"old"}}' };
    const reordered = { all: '{"/field":{"propertySource":"old","source":"text"}}' };
    expect(
      compareCanvasRevisions(
        { document: doc, formDrafts: beforeDrafts },
        { document: doc, formDrafts: reordered },
      ),
    ).toEqual([]);
    const changes = compareCanvasRevisions(
      { document: doc, formDrafts: beforeDrafts },
      { document: doc, formDrafts: { note: "unfinished" } },
    );
    expect(changes).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ entityType: "formDraft", entityId: "/field", status: "removed" }),
        expect.objectContaining({ entityType: "formDraft", entityId: "note", status: "added" }),
      ]),
    );
    expect(
      compareCanvasRevisions(
        { document: doc },
        { document: doc, formDrafts: { all: "{broken" } },
      )[0]!.fields,
    ).toEqual([{ label: "Текст формы", before: undefined, after: "{broken" }]);
  });

  it("keeps snapshots, nested contract arrays and form buffers unchanged", () => {
    const before = document();
    before.contracts[0]!.document.servers = [{ url: "https://one" }, { url: "https://two" }];
    const after = structuredClone(before);
    after.title = "Новый сценарий";
    after.contracts[0]!.document.servers = [{ url: "https://two" }, { url: "https://one" }];
    const snapshots = [
      { document: before, formDrafts: { note: " draft " } },
      { document: after, formDrafts: { note: "changed" } },
    ] as const;
    const original = structuredClone(snapshots);
    const changes = compareCanvasRevisions(...snapshots);
    expect(changes).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          entityType: "scenario",
          fields: [{ label: "Название", before: "Заказы", after: "Новый сценарий" }],
        }),
        expect.objectContaining({
          entityType: "contract",
          fields: expect.arrayContaining([
            { label: "API · Серверы · [1] · URL", before: '"https://one"', after: '"https://two"' },
          ]),
        }),
      ]),
    );
    expect(snapshots).toEqual(original);
  });
});

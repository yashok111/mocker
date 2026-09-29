import { useState } from "react";
import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import { renderWithProviders } from "@/test/render";
import { CanvasInspector } from "./CanvasInspector";
import type { CanvasDocument, CanvasSelection } from "./types";

function documentFixture(): CanvasDocument {
  return {
    formatVersion: 1,
    title: "Сценарий",
    participants: [
      { id: "client", name: "Клиент", kind: "client", description: "" },
      { id: "orders", name: "Заказы", kind: "service", description: "" },
      { id: "billing", name: "Биллинг", kind: "service", description: "" },
    ],
    messages: [
      {
        id: "request",
        fromId: "client",
        toId: "orders",
        kind: "request",
        label: "Создать заказ",
        description: "",
      },
      {
        id: "middle",
        fromId: "orders",
        toId: "billing",
        kind: "event",
        label: "Событие",
        description: "",
      },
      {
        id: "response",
        fromId: "orders",
        toId: "client",
        kind: "response",
        label: "Заказ создан",
        description: "",
        replyToId: "request",
      },
    ],
    fragments: [
      {
        id: "inverted",
        kind: "opt",
        label: "Условие",
        fromMessageId: "response",
        toMessageId: "request",
      },
    ],
    contracts: [],
  };
}

function renderInspector(document: CanvasDocument, selection: CanvasSelection, onChange = vi.fn()) {
  renderWithProviders(
    <CanvasInspector
      document={document}
      selection={selection}
      onChange={onChange}
      onClose={vi.fn()}
      onDelete={vi.fn()}
      onMove={vi.fn()}
      onImportApi={vi.fn()}
      formStore={createFormDraftStore()}
    />,
  );
  return onChange;
}

describe("CanvasInspector", () => {
  it("lists an inherited alias and materializes a local sibling only after an explicit edit", async () => {
    const initial = documentFixture();
    initial.contracts = [
      {
        id: "shared",
        name: "Shared",
        mode: "linked",
        document: {
          paths: {
            "/alias": {
              $ref: "#/components/pathItems/Shared",
              "x-mocker-canvas-operation-ids": { get: "alias-key" },
            },
          },
          components: {
            pathItems: {
              Shared: {
                get: { summary: "Inherited", responses: { "200": { description: "ok" } } },
              },
            },
          },
        },
      },
    ];
    const before = structuredClone(initial);
    const changed = vi.fn();
    function Editor() {
      const [document, setDocument] = useState(initial);
      return (
        <CanvasInspector
          document={document}
          selection={{ kind: "message", id: "request" }}
          onChange={(next) => {
            changed(next);
            setDocument(next);
          }}
          onClose={vi.fn()}
          onDelete={vi.fn()}
          onMove={vi.fn()}
          onImportApi={vi.fn()}
          formStore={createFormDraftStore()}
        />
      );
    }
    renderWithProviders(<Editor />);
    await userEvent.selectOptions(
      screen.getByLabelText("Операция API"),
      JSON.stringify(["shared", "alias-key"]),
    );
    expect(screen.getByText(/локальное переопределение/)).toBeInTheDocument();
    expect(screen.getByLabelText("Краткое название")).toHaveValue("Inherited");
    expect(initial).toEqual(before);
    fireEvent.change(screen.getByLabelText("Краткое название"), { target: { value: "Local" } });
    const updated = changed.mock.lastCall![0] as CanvasDocument;
    const path = (updated.contracts[0]!.document.paths as Record<string, Record<string, unknown>>)[
      "/alias"
    ]!;
    expect(path.get).toMatchObject({
      summary: "Local",
      "x-mocker-canvas-operation-id": "alias-key",
    });
    expect(path.$ref).toBe("#/components/pathItems/Shared");
    expect(path).not.toHaveProperty("x-mocker-canvas-operation-ids");
    expect(initial).toEqual(before);
  });

  it("edits a shared schema without materializing the selected inherited operation", async () => {
    const document = documentFixture();
    document.messages[0]!.operation = { contractId: "shared", operationKey: "alias-key" };
    document.contracts = [
      {
        id: "shared",
        name: "Shared",
        mode: "linked",
        document: {
          paths: {
            "/alias": {
              $ref: "#/components/pathItems/Shared",
              "x-mocker-canvas-operation-ids": { get: "alias-key" },
            },
          },
          components: {
            pathItems: { Shared: { get: { responses: { "200": { description: "ok" } } } } },
            schemas: { Result: { type: "object" } },
          },
        },
      },
    ];
    const before = structuredClone(document);
    const onChange = renderInspector(document, { kind: "message", id: "request" });
    await userEvent.selectOptions(screen.getByLabelText("Редактируемый объект API"), "Result");
    fireEvent.change(screen.getByLabelText("Описание схемы"), { target: { value: "Edited" } });
    const updated = onChange.mock.lastCall![0] as CanvasDocument;
    const path = (updated.contracts[0]!.document.paths as Record<string, Record<string, unknown>>)[
      "/alias"
    ]!;
    expect(path).toEqual({
      $ref: "#/components/pathItems/Shared",
      "x-mocker-canvas-operation-ids": { get: "alias-key" },
    });
    expect(path).not.toHaveProperty("get");
    expect(
      (
        updated.contracts[0]!.document.components as Record<
          string,
          Record<string, Record<string, unknown>>
        >
      ).schemas!.Result!.description,
    ).toBe("Edited");
    expect(document).toEqual(before);
  });
  it("converts a legacy block into alt with explicit branch ranges", async () => {
    const onChange = renderInspector(documentFixture(), { kind: "fragment", id: "inverted" });
    await userEvent.selectOptions(screen.getByLabelText("Тип блока"), "alt");
    const result = onChange.mock.lastCall![0] as CanvasDocument;
    expect(result.formatVersion).toBe(2);
    expect(result.fragments[0]!.branches).toHaveLength(2);
    expect(result.fragments[0]!.branches![1]!.label).toBe("else");
  });
  it("shows a local error without saving a one-step alt", async () => {
    const doc = documentFixture();
    doc.fragments[0]!.toMessageId = "response";
    const onChange = renderInspector(doc, { kind: "fragment", id: "inverted" });
    await userEvent.selectOptions(screen.getByLabelText("Тип блока"), "alt");
    expect(screen.getByRole("alert")).toHaveTextContent("хотя бы два шага");
    expect(onChange).not.toHaveBeenCalled();
  });
  it("applies parent branch and range changes atomically", async () => {
    const initial = documentFixture();
    initial.formatVersion = 2;
    initial.fragments = [
      {
        id: "outer",
        kind: "alt",
        label: "Branches",
        fromMessageId: "request",
        toMessageId: "response",
        branches: [
          { id: "yes", label: "yes", fromMessageId: "request", toMessageId: "middle" },
          { id: "no", label: "else", fromMessageId: "response", toMessageId: "response" },
        ],
      },
      {
        id: "inner",
        kind: "loop",
        label: "Retry",
        fromMessageId: "request",
        toMessageId: "middle",
        parentFragmentId: "outer",
        parentBranchId: "yes",
      },
    ];
    const changed = vi.fn();
    function Editor() {
      const [document, setDocument] = useState(initial);
      return (
        <CanvasInspector
          document={document}
          selection={{ kind: "fragment", id: "inner" }}
          onChange={(next) => {
            changed(next);
            setDocument(next);
          }}
          onClose={vi.fn()}
          onDelete={vi.fn()}
          onMove={vi.fn()}
          onImportApi={vi.fn()}
          formStore={createFormDraftStore()}
        />
      );
    }
    renderWithProviders(<Editor />);
    await userEvent.selectOptions(screen.getByLabelText("Ветка родительского блока"), "no");
    expect(changed).not.toHaveBeenCalled();
    await userEvent.selectOptions(screen.getByLabelText("Первый шаг блока"), "response");
    await userEvent.click(screen.getByRole("button", { name: "Применить вложенность" }));
    expect(changed).toHaveBeenCalledTimes(1);
    expect(changed.mock.lastCall![0].fragments[1]).toMatchObject({
      parentBranchId: "no",
      fromMessageId: "response",
      toMessageId: "response",
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
  it("edits and resets participant spacing without reordering columns", () => {
    const document = documentFixture();
    document.participants[1] = { ...document.participants[1]!, offsetX: 100 };
    const onChange = renderInspector(document, { kind: "participant", id: "orders" });
    fireEvent.change(screen.getByLabelText("Дополнительный отступ слева"), {
      target: { value: "300" },
    });
    expect(onChange.mock.lastCall?.[0].participants.map((item: { id: string }) => item.id)).toEqual(
      ["client", "orders", "billing"],
    );
    expect(onChange.mock.lastCall?.[0].participants[1].offsetX).toBe(300);
    fireEvent.click(screen.getByRole("button", { name: "Сбросить отступ" }));
    expect(onChange.mock.lastCall?.[0].participants[1].offsetX ?? 0).toBe(0);
  });

  it("asks for an endpoint before changing a descriptive request", async () => {
    const document = documentFixture();
    document.messages[0]!.label = "Получить клиента";
    const onChange = renderInspector(document, { kind: "message", id: "request" });
    await userEvent.click(screen.getByRole("button", { name: "Создать API для вызова" }));
    expect(screen.getByLabelText("Путь")).toHaveValue("");
    expect(screen.getByRole("button", { name: "Создать операцию" })).toBeDisabled();
    expect(onChange).not.toHaveBeenCalled();
    expect(document.contracts).toEqual([]);
  });
  it("changes only the selected object's card color and resets it", async () => {
    const user = userEvent.setup();
    const document = documentFixture();
    document.participants[0]!.color = "#123456";
    const onChange = renderInspector(document, { kind: "participant", id: "client" });
    const input = screen.getByLabelText("Цвет карточки");
    fireEvent.change(input, { target: { value: "#abcdef" } });
    expect(onChange.mock.lastCall?.[0].participants[0].color).toBe("#abcdef");
    expect(onChange.mock.lastCall?.[0].participants[1]).toEqual(document.participants[1]);
    expect(screen.queryByLabelText("Цвет стрелки")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Сбросить цвет карточки" }));
    expect(onChange.mock.lastCall?.[0].participants[0].color).toBeUndefined();
  });

  it("keeps a partial hex outside the document and restores the saved color on blur", () => {
    const document = documentFixture();
    document.participants[0]!.color = "#123456";
    const onChange = renderInspector(document, { kind: "participant", id: "client" });
    const input = screen.getByLabelText("Цвет карточки");
    fireEvent.change(input, { target: { value: "#abc" } });
    expect(onChange).not.toHaveBeenCalled();
    expect(input).toHaveValue("#abc");
    fireEvent.blur(input);
    expect(input).toHaveValue("#123456");
  });

  it("changes message card and arrow colors independently", () => {
    const document = documentFixture();
    document.messages[0]!.color = "#eeeeee";
    document.messages[0]!.arrowColor = "#123456";
    const onChange = renderInspector(document, { kind: "message", id: "request" });
    fireEvent.change(screen.getByLabelText("Цвет стрелки"), { target: { value: "#abcdef" } });
    expect(onChange.mock.lastCall?.[0].messages[0]).toMatchObject({
      color: "#eeeeee",
      arrowColor: "#abcdef",
    });
    fireEvent.change(screen.getByLabelText("Цвет карточки"), { target: { value: "#987654" } });
    expect(onChange.mock.lastCall?.[0].messages[0]).toMatchObject({
      color: "#987654",
      arrowColor: "#123456",
    });
    fireEvent.click(screen.getByRole("button", { name: "Сбросить цвет стрелки" }));
    expect(onChange.mock.lastCall?.[0].messages[0]).toMatchObject({
      color: "#eeeeee",
      arrowColor: undefined,
    });
  });

  it("offers only card color for notes", () => {
    const document = documentFixture();
    document.messages[0]!.kind = "note";
    renderInspector(document, { kind: "message", id: "request" });
    expect(screen.getByLabelText("Цвет карточки")).toBeInTheDocument();
    expect(screen.queryByLabelText("Цвет стрелки")).not.toBeInTheDocument();
  });

  it("applies a request endpoint edit together with its linked response", async () => {
    const user = userEvent.setup();
    const document = documentFixture();
    const onChange = renderInspector(document, { kind: "message", id: "request" });

    await user.selectOptions(screen.getByLabelText("Отправитель"), "billing");

    const updated = onChange.mock.calls[0]![0] as CanvasDocument;
    expect(updated.messages.find(({ id }) => id === "request")).toMatchObject({
      fromId: "billing",
      toId: "orders",
    });
    expect(updated.messages.find(({ id }) => id === "response")).toMatchObject({
      fromId: "orders",
      toId: "billing",
      replyToId: "request",
    });
  });

  it("shows inverted fragment IDs as the actual earlier and later steps", () => {
    renderInspector(documentFixture(), { kind: "fragment", id: "inverted" });

    expect(screen.getByLabelText("Первый шаг блока")).toHaveValue("request");
    expect(screen.getByLabelText("Последний шаг блока")).toHaveValue("response");
  });

  it("writes canonical early and late fragment boundaries after editing", async () => {
    const user = userEvent.setup();
    const document = documentFixture();
    const onChange = renderInspector(document, { kind: "fragment", id: "inverted" });

    await user.selectOptions(screen.getByLabelText("Первый шаг блока"), "middle");

    const updated = onChange.mock.calls[0]![0] as CanvasDocument;
    expect(updated.fragments[0]).toMatchObject({
      fromMessageId: "middle",
      toMessageId: "response",
    });
  });

  it("disables local API creation when the message already has a binding", () => {
    const document = documentFixture();
    document.messages[0] = {
      ...document.messages[0]!,
      operation: { contractId: "missing-contract", operationKey: "missing-operation" },
    };

    renderInspector(document, { kind: "message", id: "request" });

    expect(screen.getByRole("button", { name: "Создать API для вызова" })).toBeDisabled();
  });
});

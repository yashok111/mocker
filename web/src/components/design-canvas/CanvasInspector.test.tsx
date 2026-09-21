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

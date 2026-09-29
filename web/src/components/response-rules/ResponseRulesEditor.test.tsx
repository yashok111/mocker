import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import ResponseRulesEditor from "./ResponseRulesEditor";
import { EXTENSION, headerTemplate, writeRules } from "./model";
vi.mock("./ResponseRuleGraph", () => ({
  default: () => <div data-testid="response-rule-graph" />,
}));
const source = JSON.stringify({
  openapi: "3.1.0",
  paths: { "/orders": { get: { responses: { "200": { description: "OK" } } } } },
  "x-other": true,
});
function Harness({ initial = source }: { initial?: string }) {
  const [document, setDocument] = useState(initial);
  const [designId, setDesignId] = useState(12);
  const [store] = useState(createFormDraftStore);
  return (
    <>
      <ResponseRulesEditor
        designId={designId}
        document={document}
        blocked={null}
        formStore={store}
        onChange={setDocument}
      />
      <output data-testid="document">{document}</output>
      <button onClick={() => setDesignId(13)}>Сменить API</button>
    </>
  );
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
describe("response rule local authoring", () => {
  it("creates, binds, edits and cancels within the common buffer without API writes", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    renderWithProviders(<Harness />);
    await userEvent.click(screen.getByRole("button", { name: "Проверка заголовка" }));
    await userEvent.selectOptions(screen.getByLabelText("Операция правила"), "GET /orders");
    expect(screen.getByRole("button", { name: "Симулировать" })).toBeDisabled();
    expect(
      JSON.parse(screen.getByTestId("document").textContent!)[EXTENSION].rules[0].binding,
    ).toBeUndefined();
    await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
    expect(
      JSON.parse(screen.getByTestId("document").textContent!)[EXTENSION].rules[0].binding,
    ).toEqual({ method: "GET", path: "/orders" });
    const list = within(screen.getByRole("region", { name: "Узлы правила" }));
    await userEvent.click(list.getByRole("button", { name: /Успех/ }));
    await userEvent.clear(screen.getByLabelText("Название узла"));
    await fill(screen.getByLabelText("Название узла"), "Изменённый ответ");
    await userEvent.click(screen.getByRole("button", { name: "Отменить свойства" }));
    expect(screen.getByLabelText("Название узла")).toHaveValue("Успех");
    expect(fetch).not.toHaveBeenCalled();
  });
  it("offers keyboard node/connection CRUD and cascades node deletion", async () => {
    renderWithProviders(<Harness initial={writeRules(source, [headerTemplate()])} />);
    await userEvent.selectOptions(screen.getByLabelText("Тип нового узла"), "fallback");
    await userEvent.click(screen.getByRole("button", { name: "Добавить узел" }));
    await userEvent.click(screen.getByRole("button", { name: "Удалить узел" }));
    await userEvent.selectOptions(screen.getByLabelText("Из узла"), "start");
    await userEvent.selectOptions(screen.getByLabelText("В узел"), "ok");
    await userEvent.click(screen.getByRole("button", { name: "Добавить связь" }));
    expect(
      JSON.parse(screen.getByTestId("document").textContent!)[EXTENSION].rules[0].edges,
    ).toHaveLength(5);
    await userEvent.click(screen.getByRole("button", { name: "Удалить связь" }));
    await userEvent.click(
      within(screen.getByRole("region", { name: "Узлы правила" })).getByRole("button", {
        name: /Есть Authorization/,
      }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Удалить узел" }));
    const saved = JSON.parse(screen.getByTestId("document").textContent!);
    expect(saved[EXTENSION].rules[0].edges.map((edge: { id: string }) => edge.id)).toEqual(["e4"]);
    expect(saved["x-other"]).toBe(true);
  });
  it("preserves malformed data and exposes source recovery", () => {
    const onSource = vi.fn();
    renderWithProviders(
      <ResponseRulesEditor
        designId={12}
        document={`{"${EXTENSION}":{"formatVersion":99,"rules":[]}}`}
        blocked={null}
        formStore={createFormDraftStore()}
        onChange={vi.fn()}
        onSource={onSource}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(/Неверный формат/);
    expect(screen.queryByRole("button", { name: "Новое правило" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Открыть исходник" })).toBeEnabled();
  });
  it("previews layout without buffer writes, then applies, cancels and undoes positions", async () => {
    const rule = headerTemplate();
    rule.nodes[0]!.x = 999;
    renderWithProviders(<Harness initial={writeRules(source, [rule])} />);
    const original = screen.getByTestId("document").textContent;
    await userEvent.click(screen.getByRole("button", { name: "Авторасстановка" }));
    expect(screen.getByTestId("document").textContent).toBe(original);
    expect(screen.getByRole("button", { name: "Симулировать" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Отменить расстановку" }));
    expect(screen.getByTestId("document").textContent).toBe(original);
    await userEvent.click(screen.getByRole("button", { name: "Авторасстановка" }));
    await userEvent.click(screen.getByRole("button", { name: "Применить расстановку" }));
    expect(screen.getByTestId("document").textContent).not.toBe(original);
    await userEvent.click(screen.getByRole("button", { name: "Вернуть прежнюю расстановку" }));
    expect(screen.getByTestId("document").textContent).toBe(original);
  });
  it("can clear an operation binding explicitly", async () => {
    renderWithProviders(
      <Harness
        initial={writeRules(source, [
          { ...headerTemplate(), binding: { method: "GET", path: "/orders" } },
        ])}
      />,
    );
    await userEvent.selectOptions(screen.getByLabelText("Операция правила"), "");
    await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
    expect(
      JSON.parse(screen.getByTestId("document").textContent!)[EXTENSION].rules[0],
    ).not.toHaveProperty("binding");
  });
  it("clears the in-memory request fixture when switching APIs", async () => {
    renderWithProviders(<Harness initial={writeRules(source, [headerTemplate()])} />);
    await userEvent.click(screen.getByRole("button", { name: "Добавить заголовок запроса" }));
    await fill(screen.getByLabelText("Заголовок запроса 1"), "Authorization");
    await fill(screen.getByLabelText("Значение: заголовок запроса 1"), "Bearer private-fixture");
    expect(screen.getByTestId("document").textContent).not.toContain("private-fixture");
    await userEvent.click(screen.getByRole("button", { name: "Сменить API" }));
    expect(screen.queryByLabelText("Заголовок запроса 1")).not.toBeInTheDocument();
  });
});

import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import { fill } from "@/test/user";
import type { ApiDocument } from "../api-designer/documentModel";
import StateDiagramEditor from "./StateDiagramEditor";
import { orderTemplate, writeDiagrams, EXTENSION } from "./model";
vi.mock("./StateGraph", () => ({ default: () => <div data-testid="state-graph" /> }));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const api: ApiDocument = {
  openapi: "3.1.0",
  info: { title: "Orders", version: "1" },
  paths: { "/orders/pay": { post: { responses: { "200": { description: "OK" } } } } },
};
function Harness({ initial = api }: { initial?: ApiDocument }) {
  const [document, setDocument] = useState(initial);
  return (
    <>
      <StateDiagramEditor
        designId={12}
        document={document}
        blocked={false}
        onChange={setDocument}
      />
      <output data-testid="document">{JSON.stringify(document)}</output>
    </>
  );
}

describe("state diagram authoring", () => {
  it("edits and clears entity settings and values while preserving state IDs", async () => {
    const diagram = orderTemplate();
    renderWithProviders(<Harness initial={writeDiagrams(api, [diagram])} />);
    await userEvent.click(screen.getByLabelText("Исполнять переходы для сущности"));
    await fill(screen.getByLabelText("Семейство сущностей"), "/orders");
    await fill(screen.getByLabelText("Параметр ключа сущности"), "orderId");
    await fill(screen.getByLabelText("Поле состояния"), "status");
    expect(screen.getByText(/Отсутствующее поле состояния/)).toBeInTheDocument();
    await userEvent.click(
      within(screen.getByRole("region", { name: "Состояния" })).getByRole("button", {
        name: /Создан/,
      }),
    );
    await fill(screen.getByLabelText("Значение состояния в данных"), "new");
    let saved = JSON.parse(screen.getByTestId("document").textContent!)[EXTENSION].diagrams[0];
    expect(saved.entity).toEqual({ family: "/orders", keyParam: "orderId", stateField: "status" });
    expect(saved.states[0]).toMatchObject({ id: "created", value: "new" });
    expect(saved.transitions[0].from).toBe("created");
    await userEvent.clear(screen.getByLabelText("Значение состояния в данных"));
    saved = JSON.parse(screen.getByTestId("document").textContent!)[EXTENSION].diagrams[0];
    expect(saved.states[0]).not.toHaveProperty("value");
    await userEvent.selectOptions(screen.getByLabelText("Диаграмма состояний"), diagram.id);
    await userEvent.click(screen.getByLabelText("Исполнять переходы для сущности"));
    saved = JSON.parse(screen.getByTestId("document").textContent!)[EXTENSION].diagrams[0];
    expect(saved).not.toHaveProperty("entity");
  });
  it("uses configured simulation state and raw result data from the pure endpoint", async () => {
    const diagram = {
      ...orderTemplate(),
      id: "order",
      entity: { family: "/orders", keyParam: "orderId", stateField: "status" },
    };
    const dataJSON = '{"status":"paid","amount":9007199254740993}';
    route({
      "POST /api/designs/12/state-diagrams/order/simulate": () =>
        json(200, { stateId: "paid", dataJSON, steps: [], diagnostics: [] }),
    });
    renderWithProviders(<Harness initial={writeDiagrams(api, [diagram])} />);
    expect(screen.getByText(/Состояние берётся из поля/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Начать заново" }));
    expect((await screen.findByLabelText("Данные после перехода")).textContent).toBe(dataJSON);
    expect(
      within(screen.getByRole("region", { name: "Симуляция диаграммы" })).getByText("Оплачен"),
    ).toBeInTheDocument();
  });
  it("applies automatic layout as one document change while preserving state and transition data", async () => {
    const diagram = orderTemplate();
    const initial = writeDiagrams(api, [diagram]);
    const onChange = vi.fn();
    renderWithProviders(
      <StateDiagramEditor designId={12} document={initial} blocked={false} onChange={onChange} />,
    );
    const button = screen.getByRole("button", { name: "Автораскладка" });
    await waitFor(() => expect(button).toBeEnabled());
    await userEvent.click(button);
    expect(onChange).toHaveBeenCalledOnce();
    const result = onChange.mock.calls[0]![0];
    expect(result.paths).toEqual(initial.paths);
    const saved = result[EXTENSION].diagrams[0];
    expect(saved.transitions).toEqual(diagram.transitions);
    expect(saved.initialStateId).toBe(diagram.initialStateId);
    expect(
      saved.states.map(({ x: _x, y: _y, ...state }: { x: number; y: number }) => state),
    ).toEqual(diagram.states.map(({ x: _x, y: _y, ...state }) => state));
    expect(saved.states).not.toEqual(diagram.states);
  });
  it("creates states and binds a transition while retaining API operations", async () => {
    renderWithProviders(<Harness />);
    await userEvent.click(screen.getByRole("button", { name: "Пример заказа" }));
    await userEvent.click(
      within(screen.getByRole("region", { name: "Переходы" })).getByRole("button", {
        name: /Оплатить/,
      }),
    );
    await userEvent.selectOptions(screen.getByLabelText("Операция API"), "post /orders/pay");
    const saved = JSON.parse(screen.getByTestId("document").textContent!);
    expect(saved.paths).toEqual(api.paths);
    expect(saved[EXTENSION].diagrams[0].transitions[0].binding).toEqual({
      method: "post",
      path: "/orders/pay",
    });
    await userEvent.click(screen.getByRole("button", { name: "Добавить состояние" }));
    expect(screen.getByLabelText("Название состояния")).toHaveValue("Состояние 5");
    expect(
      JSON.parse(screen.getByTestId("document").textContent!)[EXTENSION].diagrams[0].states,
    ).toHaveLength(5);
  });
  it("uses the shared simulation endpoint and displays a blocked step", async () => {
    const d = orderTemplate();
    d.id = "order";
    const fetchMock = route({
      "POST /api/designs/12/state-diagrams/order/simulate": () => {
        return json(200, {
          stateId: "created",
          dataJSON: '{"paymentAllowed":false}',
          diagnostics: [],
          steps: [
            {
              transitionId: "pay",
              from: "created",
              to: "created",
              accepted: false,
              reason: "Условие перехода не выполнено",
              responseStatus: 0,
              dataJSON: '{"paymentAllowed":false}',
            },
          ],
        });
      },
    });
    renderWithProviders(<Harness initial={writeDiagrams(api, [d])} />);
    await userEvent.click(screen.getByRole("button", { name: "Оплатить" }));
    expect(await screen.findByText(/отказ — Условие перехода не выполнено/)).toBeInTheDocument();
    const request = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body));
    expect(request?.transitionIds).toEqual(["pay"]);
    expect(request?.diagram).toMatchObject({ id: "order" });
    expect(JSON.parse(String(request?.document)).paths).toEqual(api.paths);
  });
  it("discards an in-flight result after editing the diagram", async () => {
    const d = orderTemplate();
    d.id = "order";
    let finish: ((r: Response) => void) | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            finish = resolve;
          }),
      ),
    );
    renderWithProviders(<Harness initial={writeDiagrams(api, [d])} />);
    await userEvent.click(screen.getByRole("button", { name: "Оплатить" }));
    await waitFor(() => expect(finish).toBeDefined());
    await userEvent.click(screen.getByRole("button", { name: "Добавить состояние" }));
    finish!(json(200, { stateId: "paid", dataJSON: '{"stale":true}', steps: [], diagnostics: [] }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Начать заново" })).toBeEnabled(),
    );
    expect(screen.queryByText(/stale/)).not.toBeInTheDocument();
  });
  it("blocks visual edits for invalid source and unfinished forms", () => {
    renderWithProviders(
      <StateDiagramEditor designId={12} document={api} blocked onChange={vi.fn()} />,
    );
    expect(screen.getByText(/Завершите редактирование формы/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Новая диаграмма" })).not.toBeInTheDocument();
  });
});

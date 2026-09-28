import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
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

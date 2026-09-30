import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { json, route } from "@/test/http";
import SimulationPanel from "./SimulationPanel";
import { headerTemplate, type ResponseRule } from "./model";

const request = {
  query: [],
  headers: [],
  bodyJSON: " 9007199254740993 ",
  entities: [
    {
      family: "/orders",
      idField: "id",
      idType: "integer" as const,
      rows: [{ key: "1", scope: [], dataJSON: ' {"n":9007199254740993} ' }],
    },
  ],
};
const initial: ResponseRule = {
  ...headerTemplate(),
  id: "orders",
  examples: [{ id: "precise", name: "Точные числа", request }],
};
function Harness() {
  const [rule, setRule] = useState(initial);
  return (
    <>
      <SimulationPanel
        designId={12}
        document={JSON.stringify(rule)}
        rule={rule}
        generation="{}"
        pendingForm={false}
        onSelect={() => {}}
        onTrace={() => {}}
        onChangeRule={(next) => {
          setRule(next);
          return true;
        }}
      />
      <output data-testid="rule">{JSON.stringify(rule)}</output>
      <button onClick={() => setRule({ ...initial, id: "other", examples: [] })}>
        Другое правило
      </button>
    </>
  );
}
afterEach(() => vi.unstubAllGlobals());
it("loads a copy and updates a stable named example, preserving all exact fixture text", async () => {
  renderWithProviders(<Harness />);
  await userEvent.selectOptions(screen.getByLabelText("Сохранённый пример"), "precise");
  expect(screen.getByLabelText("JSON запроса")).toHaveValue(" 9007199254740993 ");
  expect(screen.getByLabelText("JSON записи 1.1")).toHaveValue(' {"n":9007199254740993} ');
  await userEvent.clear(screen.getByLabelText("JSON запроса"));
  await fill(screen.getByLabelText("JSON запроса"), "1e999999999999999999999");
  expect(JSON.parse(screen.getByTestId("rule").textContent!).examples[0].request.bodyJSON).toBe(
    " 9007199254740993 ",
  );
  await userEvent.clear(screen.getByLabelText("Название примера"));
  await fill(screen.getByLabelText("Название примера"), "Изменённый пример");
  await userEvent.click(screen.getByRole("button", { name: "Обновить пример" }));
  expect(JSON.parse(screen.getByTestId("rule").textContent!).examples).toEqual([
    {
      id: "precise",
      name: "Изменённый пример",
      request: { ...request, bodyJSON: "1e999999999999999999999" },
    },
  ]);
  expect(screen.getByText(/после сохранения черновика API/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Другое правило" }));
  expect(screen.getByLabelText("Сохранённый пример")).toHaveValue("");
  expect(screen.queryByLabelText("JSON запроса")).not.toBeInTheDocument();
});
it("adds and removes local named examples and rejects empty or oversized names", async () => {
  renderWithProviders(<Harness />);
  expect(screen.getByRole("button", { name: "Добавить пример" })).toBeDisabled();
  await fill(screen.getByLabelText("Название примера"), "😀".repeat(51));
  expect(screen.getByRole("button", { name: "Добавить пример" })).toBeDisabled();
  await userEvent.clear(screen.getByLabelText("Название примера"));
  await fill(screen.getByLabelText("Название примера"), "Пустой запрос");
  await userEvent.click(screen.getByRole("button", { name: "Добавить пример" }));
  const examples = JSON.parse(screen.getByTestId("rule").textContent!).examples;
  expect(examples).toHaveLength(2);
  expect(examples[1]).toMatchObject({ name: "Пустой запрос", request: { query: [], headers: [] } });
  expect(examples[1].id).not.toBe("precise");
  await userEvent.click(screen.getByRole("button", { name: "Удалить пример" }));
  expect(JSON.parse(screen.getByTestId("rule").textContent!).examples).toEqual(initial.examples);
});
it("simulates edited copies and shows nested short-circuit traces with RHS references", async () => {
  const fetch = route({
    "POST /api/designs/12/response-rules/orders/simulate": () =>
      json(200, {
        valid: true,
        diagnostics: [],
        diagnosticsTruncated: false,
        source: { designId: 12, ruleId: "orders", kind: "proposal", documentHash: "hash" },
        inputHash: "input",
        outcome: "fallback",
        totalDelayMs: 0,
        trace: [
          {
            step: 1,
            nodeId: "auth",
            matched: false,
            edgeId: "edge",
            resultCondition: {
              op: "all",
              matched: false,
              shortCircuited: true,
              children: [
                {
                  op: "any",
                  matched: false,
                  children: [
                    {
                      sourceNodeId: "left",
                      pointer: "/amount",
                      op: "less_than",
                      present: true,
                      matched: false,
                      actualJSON: "9007199254740993",
                      expectedJSON: "9007199254740992",
                      valueFrom: { source: "result", nodeId: "right", pointer: "/limit" },
                    },
                  ],
                },
              ],
            },
          },
        ],
      }),
  });
  renderWithProviders(<Harness />);
  await userEvent.selectOptions(screen.getByLabelText("Сохранённый пример"), "precise");
  await userEvent.click(screen.getByRole("button", { name: "Симулировать" }));
  const steps = await screen.findByRole("list", { name: "Шаги симуляции" });
  expect(steps).toHaveTextContent("И · Нет");
  expect(steps).toHaveTextContent("ИЛИ · Нет");
  expect(steps).toHaveTextContent("Оставшиеся условия пропущены");
  expect(steps).toHaveTextContent("Ожидаемый результат: right · /limit");
  expect(within(steps).getByText(/Фактическое JSON:/)).toHaveTextContent("9007199254740993");
  const sent = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body));
  expect(sent.request).toEqual(request);
  expect(sent).not.toHaveProperty("exampleId");
});

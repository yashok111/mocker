import { useState } from "react";
import { expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import RuleInspector from "./RuleInspector";
import type { ResponseRule } from "./model";

function fixture(): ResponseRule {
  return {
    id: "orders",
    name: "Заказы",
    // Saved order is deliberately unrelated to execution order.
    nodes: [
      {
        id: "check",
        type: "condition",
        name: "Проверка",
        x: 0,
        y: 0,
        condition: { in: "body", name: "n", op: "equals", value: "1" },
      },
      {
        id: "future",
        type: "entity_create",
        name: "Позже",
        x: 0,
        y: 0,
        entity: { family: "/orders", data: { source: "body", pointer: "" } },
      },
      {
        id: "read",
        type: "entity_read",
        name: "Заказ",
        x: 0,
        y: 0,
        entity: { family: "/orders", operation: "get", key: { source: "path", name: "id" } },
      },
      { id: "start", type: "start", name: "Запрос", x: 0, y: 0 },
      { id: "end", type: "fallback", name: "Конец", x: 0, y: 0 },
      {
        id: "island",
        type: "entity_create",
        name: "Отдельно",
        x: 0,
        y: 0,
        entity: { family: "/orders", data: { source: "body", pointer: "" } },
      },
    ],
    edges: [
      { id: "e1", from: "start", to: "read", port: "next" },
      { id: "e2", from: "read", to: "check", port: "found" },
      { id: "e3", from: "read", to: "end", port: "missing" },
      { id: "e4", from: "check", to: "future", port: "true" },
      { id: "e5", from: "check", to: "end", port: "false" },
      { id: "e6", from: "future", to: "end", port: "next" },
    ],
  };
}

function Harness({ initial = fixture() }: { initial?: ResponseRule }) {
  const [rule, setRule] = useState(initial);
  const [store] = useState(createFormDraftStore);
  return (
    <>
      <RuleInspector
        rule={rule}
        document={{ paths: {} }}
        selection={{ kind: "node", id: "check" }}
        formStore={store}
        blocked={false}
        onRemove={() => {}}
        onChange={(next) => {
          setRule(next);
          return true;
        }}
      />
      <output data-testid="rule">{JSON.stringify(rule)}</output>
    </>
  );
}
function savedCondition() {
  return JSON.parse(screen.getByTestId("rule").textContent!).nodes.find(
    (node: { id: string }) => node.id === "check",
  );
}

it("edits numeric ordering against another available result without retaining a literal", async () => {
  renderWithProviders(<Harness />);
  await userEvent.selectOptions(screen.getByLabelText("Режим условия"), "result");
  await userEvent.selectOptions(screen.getByLabelText("Узел результата условия"), "read");
  await userEvent.selectOptions(screen.getByLabelText("Сравнение результата"), "greater_or_equal");
  await userEvent.clear(screen.getByLabelText("Ожидаемое значение JSON"));
  await fill(screen.getByLabelText("Ожидаемое значение JSON"), '"10"');
  expect(screen.getByRole("button", { name: "Применить свойства" })).toBeDisabled();
  await userEvent.selectOptions(screen.getByLabelText("Источник ожидаемого значения"), "result");
  await userEvent.selectOptions(screen.getByLabelText("Узел ожидаемого результата"), "read");
  await fill(screen.getByLabelText("JSON Pointer ожидаемого результата"), "/minimum");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(savedCondition().resultCondition).toEqual({
    source: { source: "result", nodeId: "read", pointer: "" },
    op: "greater_or_equal",
    valueFrom: { source: "result", nodeId: "read", pointer: "/minimum" },
  });
  expect(savedCondition().resultCondition).not.toHaveProperty("valueJSON");
});

it("wraps the authored leaf into AND and a nested OR, keeping exact values", async () => {
  renderWithProviders(<Harness />);
  await userEvent.selectOptions(screen.getByLabelText("Режим условия"), "result");
  await userEvent.selectOptions(screen.getByLabelText("Узел результата условия"), "read");
  await userEvent.selectOptions(screen.getByLabelText("Сравнение результата"), "equals");
  await userEvent.clear(screen.getByLabelText("Ожидаемое значение JSON"));
  await fill(screen.getByLabelText("Ожидаемое значение JSON"), "9007199254740993");
  await userEvent.selectOptions(screen.getByLabelText("Структура условия"), "all");
  await userEvent.selectOptions(screen.getByLabelText("Структура условия · 2"), "any");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(savedCondition().resultCondition).toEqual({
    all: [
      {
        source: { source: "result", nodeId: "read", pointer: "" },
        op: "equals",
        valueJSON: "9007199254740993",
      },
      {
        any: [
          { source: { source: "result", nodeId: "read", pointer: "" }, op: "exists" },
          { source: { source: "result", nodeId: "read", pointer: "" }, op: "exists" },
        ],
      },
    ],
  });
  expect(screen.getByRole("button", { name: "Удалить условие 1" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "Добавить условие" }));
  expect(screen.getByRole("button", { name: "Удалить условие 3" })).toBeEnabled();
});

it("blocks an unavailable RHS source inside an otherwise valid group", async () => {
  const rule = fixture();
  rule.nodes[0] = {
    ...rule.nodes[0],
    resultCondition: {
      all: [
        { source: { source: "result", nodeId: "read" }, op: "exists" },
        {
          source: { source: "result", nodeId: "read" },
          op: "equals",
          valueFrom: { source: "result", nodeId: "future" },
        },
      ],
    },
  } as unknown as ResponseRule["nodes"][number];
  delete (rule.nodes[0] as unknown as Record<string, unknown>).condition;
  renderWithProviders(<Harness initial={rule} />);
  expect(screen.getByLabelText("Узел ожидаемого результата · 2")).toHaveValue("future");
  await fill(screen.getByLabelText("JSON Pointer ожидаемого результата · 2"), "/id");
  expect(screen.getByRole("button", { name: "Применить свойства" })).toBeDisabled();
});

it("edits a dominating result and preserves an exact expected number when applied", async () => {
  renderWithProviders(<Harness />);
  await userEvent.selectOptions(screen.getByLabelText("Режим условия"), "result");
  const select = screen.getByLabelText("Узел результата условия");
  expect(
    within(select)
      .getAllByRole("option")
      .map((option) => option.getAttribute("value")),
  ).toEqual(["", "read"]);
  await userEvent.selectOptions(select, "read");
  await fill(screen.getByLabelText("JSON Pointer условия"), "/amount");
  await userEvent.selectOptions(screen.getByLabelText("Сравнение результата"), "equals");
  await userEvent.clear(screen.getByLabelText("Ожидаемое значение JSON"));
  await fill(screen.getByLabelText("Ожидаемое значение JSON"), "9007199254740993");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(savedCondition().resultCondition).toEqual({
    source: { source: "result", nodeId: "read", pointer: "/amount" },
    op: "equals",
    valueJSON: "9007199254740993",
  });
  expect(savedCondition()).not.toHaveProperty("condition");
});

it("removes expected JSON for existence and removes result fields when returning to request mode", async () => {
  renderWithProviders(<Harness />);
  await userEvent.selectOptions(screen.getByLabelText("Режим условия"), "result");
  await userEvent.selectOptions(screen.getByLabelText("Узел результата условия"), "read");
  await userEvent.selectOptions(screen.getByLabelText("Сравнение результата"), "not_equals");
  await userEvent.clear(screen.getByLabelText("Ожидаемое значение JSON"));
  await fill(screen.getByLabelText("Ожидаемое значение JSON"), "null");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(savedCondition().resultCondition).toMatchObject({ op: "not_equals", valueJSON: "null" });
  await userEvent.selectOptions(screen.getByLabelText("Сравнение результата"), "not_exists");
  expect(screen.queryByLabelText("Ожидаемое значение JSON")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(savedCondition().resultCondition).not.toHaveProperty("valueJSON");
  await userEvent.selectOptions(screen.getByLabelText("Режим условия"), "request");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(savedCondition()).not.toHaveProperty("resultCondition");
  expect(savedCondition().condition).toEqual({ in: "header", name: "Authorization", op: "exists" });
});

it("blocks containers and malformed scalar literals until the raw JSON is repaired", async () => {
  renderWithProviders(<Harness />);
  await userEvent.selectOptions(screen.getByLabelText("Режим условия"), "result");
  await userEvent.selectOptions(screen.getByLabelText("Узел результата условия"), "read");
  await userEvent.selectOptions(screen.getByLabelText("Сравнение результата"), "equals");
  for (const input of ["{}", "[]", "1 2", "01"]) {
    await userEvent.clear(screen.getByLabelText("Ожидаемое значение JSON"));
    await fill(screen.getByLabelText("Ожидаемое значение JSON"), input);
    expect(screen.getByRole("button", { name: "Применить свойства" })).toBeDisabled();
    expect(screen.getByLabelText("Ожидаемое значение JSON")).toHaveValue(input);
  }
  await userEvent.clear(screen.getByLabelText("Ожидаемое значение JSON"));
  await fill(screen.getByLabelText("Ожидаемое значение JSON"), "1e999999999999999999999");
  expect(screen.getByRole("button", { name: "Применить свойства" })).toBeEnabled();
});

it.each(["missing", "bypass", "unreachable", "cycle"])(
  "does not offer a producer after a %s graph path",
  async (change) => {
    const rule = fixture();
    if (change === "missing") rule.edges[2]!.to = "check";
    if (change === "bypass")
      rule.edges.push({ id: "bypass", from: "start", to: "check", port: "next" });
    if (change === "unreachable") rule.edges = rule.edges.filter((edge) => edge.from !== "start");
    if (change === "cycle")
      rule.edges.push({ id: "cycle", from: "future", to: "read", port: "next" });
    renderWithProviders(<Harness initial={rule} />);
    await userEvent.selectOptions(screen.getByLabelText("Режим условия"), "result");
    expect(
      within(screen.getByLabelText("Узел результата условия"))
        .getAllByRole("option")
        .map((option) => option.getAttribute("value")),
    ).toEqual([""]);
  },
);

it("retains an unavailable stored source until the user chooses a valid replacement", async () => {
  const rule = fixture();
  rule.nodes[0] = {
    id: "check",
    type: "condition",
    name: "Проверка",
    x: 0,
    y: 0,
    resultCondition: {
      source: { source: "result", nodeId: "deleted", pointer: "/status" },
      op: "equals",
      valueJSON: '"paid"',
    },
  };
  renderWithProviders(<Harness initial={rule} />);
  expect(screen.getByLabelText("Узел результата условия")).toHaveValue("deleted");
  expect(screen.getByText(/Выберите существующий узел сущности/)).toBeInTheDocument();
  expect(savedCondition().resultCondition.source.nodeId).toBe("deleted");
  await userEvent.clear(screen.getByLabelText("JSON Pointer условия"));
  await fill(screen.getByLabelText("JSON Pointer условия"), "/a~1b/~0/0");
  expect(screen.getByRole("button", { name: "Применить свойства" })).toBeDisabled();
  await userEvent.selectOptions(screen.getByLabelText("Узел результата условия"), "read");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(savedCondition().resultCondition).toEqual({
    source: { source: "result", nodeId: "read", pointer: "/a~1b/~0/0" },
    op: "equals",
    valueJSON: '"paid"',
  });
});

it.each(["entity_create", "entity_update", "list"])(
  "offers a preceding %s producer on its successful exit",
  async (type) => {
    const rule = fixture();
    if (type === "entity_create") {
      rule.nodes[2] = {
        id: "read",
        type,
        name: "Создать",
        x: 0,
        y: 0,
        entity: { family: "/orders", data: { source: "body", pointer: "" } },
      };
      rule.edges[1]!.port = "next";
      rule.edges = rule.edges.filter((edge) => edge.id !== "e3");
    } else if (type === "entity_update") {
      rule.nodes[2] = {
        id: "read",
        type,
        name: "Изменить",
        x: 0,
        y: 0,
        entity: {
          family: "/orders",
          key: { source: "path", name: "id" },
          data: { source: "body", pointer: "" },
        },
      };
    } else {
      rule.nodes[2] = {
        id: "read",
        type: "entity_read",
        name: "Список",
        x: 0,
        y: 0,
        entity: { family: "/orders", operation: "list" },
      };
      rule.edges[1]!.port = "next";
      rule.edges = rule.edges.filter((edge) => edge.id !== "e3");
    }
    renderWithProviders(<Harness initial={rule} />);
    await userEvent.selectOptions(screen.getByLabelText("Режим условия"), "result");
    expect(
      within(screen.getByLabelText("Узел результата условия"))
        .getAllByRole("option")
        .map((option) => option.getAttribute("value")),
    ).toEqual(["", "read"]);
  },
);

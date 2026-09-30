import { useState } from "react";
import { expect, it } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import RuleInspector from "./RuleInspector";
import { blankRule, type ResponseRule } from "./model";

function Harness({ type = "entity_create" }: { type?: string }) {
  const [rule, setRule] = useState(
    () =>
      ({
        ...blankRule(),
        nodes: [
          {
            id: "create",
            name: "Создание",
            type: "entity_create",
            x: 0,
            y: 0,
            entity: { family: "/orders", data: { source: "body", pointer: "" } },
          },
          {
            id: "read",
            name: "Чтение",
            type: "entity_read",
            x: 0,
            y: 0,
            entity: { family: "/orders", operation: "get", key: { source: "path", name: "id" } },
          },
          {
            id: "update",
            name: "Изменение",
            type: "entity_update",
            x: 0,
            y: 0,
            entity: {
              family: "/orders",
              key: { source: "path", name: "id" },
              data: { source: "body", pointer: "" },
            },
          },
          {
            id: "response",
            name: "Ответ",
            type: "response",
            x: 0,
            y: 0,
            response: { status: 200, mediaType: "application/json", headers: [], bodyJSON: "{}" },
          },
        ],
        edges: [],
      }) as unknown as ResponseRule,
  );
  const [store] = useState(createFormDraftStore);
  return (
    <>
      <RuleInspector
        rule={rule}
        document={{ paths: { "/orders/{id}": {} } }}
        selection={{
          kind: "node",
          id:
            type === "response"
              ? "response"
              : type === "entity_read"
                ? "read"
                : type === "entity_update"
                  ? "update"
                  : "create",
        }}
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

it("edits exact entity data and distinguishes inherited scope from an explicit root", async () => {
  renderWithProviders(<Harness />);
  await userEvent.selectOptions(screen.getByLabelText("Источник данных сущности"), "literal");
  await userEvent.clear(screen.getByLabelText("JSON: данные сущности"));
  await fill(screen.getByLabelText("JSON: данные сущности"), '{"amount":9007199254740993}');
  await userEvent.selectOptions(screen.getByLabelText("Область сущности"), "explicit");
  expect(screen.getByTestId("rule").textContent).not.toContain("9007199254740993");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(JSON.parse(screen.getByTestId("rule").textContent!).nodes[0].entity).toEqual({
    family: "/orders",
    scope: [],
    data: { source: "literal", valueJSON: '{"amount":9007199254740993}' },
  });
  await userEvent.selectOptions(screen.getByLabelText("Область сущности"), "inherited");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(JSON.parse(screen.getByTestId("rule").textContent!).nodes[0].entity).not.toHaveProperty(
    "scope",
  );
});

it("switches single-record reading to list without retaining a key", async () => {
  renderWithProviders(<Harness type="entity_read" />);
  await userEvent.selectOptions(screen.getByLabelText("Чтение сущностей"), "list");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(JSON.parse(screen.getByTestId("rule").textContent!).nodes[1].entity).toEqual({
    family: "/orders",
    operation: "list",
  });
  expect(screen.queryByLabelText("Источник ключа сущности")).not.toBeInTheDocument();
});

it("selects a previous entity result as response body without a literal body", async () => {
  renderWithProviders(<Harness type="response" />);
  await userEvent.selectOptions(screen.getByLabelText("Источник тела ответа"), "reference");
  await userEvent.selectOptions(screen.getByLabelText("Источник значения тела ответа"), "result");
  await userEvent.selectOptions(screen.getByLabelText("Узел: значение тела ответа"), "create");
  await fill(screen.getByLabelText("JSON Pointer: значение тела ответа"), "/amount");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  const response = JSON.parse(screen.getByTestId("rule").textContent!).nodes[3].response;
  expect(response.bodyFrom).toEqual({ source: "result", nodeId: "create", pointer: "/amount" });
  expect(response).not.toHaveProperty("bodyJSON");
});

it("edits update key and object data from request fields", async () => {
  renderWithProviders(<Harness type="entity_update" />);
  await userEvent.selectOptions(screen.getByLabelText("Источник ключа сущности"), "query");
  await fill(screen.getByLabelText("Имя: ключ сущности"), "orderId");
  await fill(screen.getByLabelText("JSON Pointer: данные сущности"), "/changes");
  await userEvent.click(screen.getByRole("button", { name: "Применить свойства" }));
  expect(JSON.parse(screen.getByTestId("rule").textContent!).nodes[2].entity).toEqual({
    family: "/orders",
    key: { source: "query", name: "orderId" },
    data: { source: "body", pointer: "/changes" },
  });
});

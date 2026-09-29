import { useState } from "react";
import { MantineProvider } from "@mantine/core";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ScenarioDataBindingsEditor } from "./ScenarioDataBindingsEditor";
import { emptyCanvas } from "./canvasModel";
import type { DataBinding, DataFlowAnalysis } from "./types";

const document = {
  ...emptyCanvas(),
  messages: ["source", "target"].map((id) => ({
    id,
    fromId: "a",
    toId: "b",
    kind: "request" as const,
    label: id,
    description: "",
    operation: { contractId: "api", operationKey: id },
  })),
};
const analysis: DataFlowAnalysis = {
  messages: [
    {
      messageId: "source",
      responseFields: [
        { kind: "response", pointer: "/id", type: "integer", required: true },
        { kind: "response", pointer: "/token", type: "string", required: false },
      ],
      requestFields: [],
    },
    {
      messageId: "target",
      responseFields: [],
      requestFields: [
        { kind: "path", name: "id", type: "integer", required: true },
        { kind: "header", name: "Authorization", type: "string", required: false },
      ],
    },
  ],
  bindings: [],
  diagnostics: [],
};
function Harness({ initialBindings = [] }: { initialBindings?: DataBinding[] }) {
  const [bindings, setBindings] = useState<DataBinding[]>(initialBindings);
  return (
    <MantineProvider env="test">
      <ScenarioDataBindingsEditor
        document={document}
        messageId="target"
        bindings={bindings}
        analysis={analysis}
        onChange={setBindings}
        examples={[
          { sourceMessageId: "source", sourcePointer: "/id", valueJson: "9007199254740993" },
          { sourceMessageId: "source", sourcePointer: "/token", valueJson: '" ABC "' },
        ]}
      />
      <output aria-label="Сохранённые связи">{JSON.stringify(bindings)}</output>
    </MantineProvider>
  );
}

describe("ScenarioDataBindingsEditor", () => {
  it("adds, reorders and removes transforms while preserving the binding", async () => {
    const user = userEvent.setup();
    render(
      <Harness
        initialBindings={[
          {
            id: "token",
            sourceMessageId: "source",
            sourcePointer: "/token",
            target: { kind: "header", name: "AUTHORIZATION" },
            prefix: "Bearer ",
          },
        ]}
      />,
    );
    await user.click(screen.getByText("Дополнительно"));
    await user.click(screen.getByRole("button", { name: "Добавить преобразование связи 1" }));
    await user.selectOptions(screen.getByLabelText("Преобразование 1 связи 1"), "lower");
    await user.click(screen.getByRole("button", { name: "Добавить преобразование связи 1" }));
    await user.selectOptions(screen.getByLabelText("Преобразование 2 связи 1"), "trim");
    await user.click(screen.getByRole("button", { name: "Поднять преобразование 2 связи 1" }));
    expect(screen.getByLabelText("Сохранённые связи")).toHaveTextContent(
      '"transforms":[{"kind":"trim"},{"kind":"lower"}]',
    );
    await user.click(screen.getByRole("button", { name: "Удалить преобразование 2 связи 1" }));
    expect(screen.getByLabelText("Сохранённые связи")).toHaveTextContent(
      '"transforms":[{"kind":"trim"}]',
    );
    expect(screen.getByLabelText("Сохранённые связи")).toHaveTextContent('"prefix":"Bearer "');
    expect(screen.getByLabelText("Сохранённые связи")).toHaveTextContent('"name":"AUTHORIZATION"');
    expect(screen.getByText(/Исходное значение прошлого запуска/)).toBeInTheDocument();
  });

  it("stops at eight transforms and allows another after removing one", async () => {
    const user = userEvent.setup();
    render(
      <Harness
        initialBindings={[
          {
            id: "id",
            sourceMessageId: "source",
            sourcePointer: "/id",
            target: { kind: "path", name: "id" },
            transforms: Array.from({ length: 8 }, () => ({ kind: "trim" as const })),
          },
        ]}
      />,
    );
    await user.click(screen.getByText("Дополнительно"));
    expect(screen.getByRole("button", { name: "Добавить преобразование связи 1" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Удалить преобразование 8 связи 1" }));
    expect(screen.getByRole("button", { name: "Добавить преобразование связи 1" })).toBeEnabled();
  });
  it("creates a binding at a known parameter without asking for its destination", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Взять из ответа: id" }));
    await user.selectOptions(screen.getByLabelText("Шаг-источник связи 1"), "source");
    await user.selectOptions(screen.getByLabelText("Поле ответа связи 1"), "pointer:/id");
    expect(screen.getByLabelText("Сохранённые связи")).toHaveTextContent(
      '"target":{"kind":"path","name":"id"}',
    );
    expect(screen.getByText("id берётся из шага «source»")).toBeInTheDocument();
    expect(screen.queryByLabelText("JSON Pointer источника связи 1")).not.toBeVisible();
    await user.click(screen.getByRole("button", { name: "Удалить связь 1" }));
    expect(screen.getByLabelText("Сохранённые связи")).toHaveTextContent("[]");
  });

  it("preserves header spelling and prefixes in advanced settings", async () => {
    const user = userEvent.setup();
    render(
      <Harness
        initialBindings={[
          {
            id: "token",
            sourceMessageId: "source",
            sourcePointer: "/token",
            target: { kind: "header", name: "AUTHORIZATION" },
            prefix: "Bearer ",
          },
        ]}
      />,
    );
    expect(screen.getByText("AUTHORIZATION берётся из шага «source»")).toBeInTheDocument();
    await user.click(screen.getByText("Дополнительно"));
    expect(screen.getByLabelText("Имя назначения связи 1")).toHaveValue("AUTHORIZATION");
    expect(screen.getByLabelText("Префикс связи 1")).toHaveValue("Bearer ");
    fireEvent.change(screen.getByLabelText("JSON Pointer источника связи 1"), {
      target: { value: "/items/0/token" },
    });
    await user.selectOptions(screen.getByLabelText("Тип назначения связи 1"), "body");
    expect(screen.getByLabelText("JSON Pointer назначения связи 1")).toHaveValue("");
    expect(screen.getByLabelText("Сохранённые связи")).not.toHaveTextContent("Bearer");
  });
});

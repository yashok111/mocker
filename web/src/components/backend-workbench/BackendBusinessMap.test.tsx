import { expect, it, vi } from "vitest";
import { useState } from "react";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readFileSync } from "node:fs";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { BackendBusinessMap, BackendBusinessMapEditor } from "./BackendBusinessMap";
import type { BackendBusinessMapDocument } from "@/api/generated/schemas";
const fixture = () =>
  JSON.parse(
    readFileSync("../internal/backendmodel/testdata/diagrams/business_map.json", "utf8"),
  ) as BackendBusinessMapDocument;
vi.mock("./BackendArchitectureGraph", () => ({ BackendArchitectureGraph: () => null }));
it("supports keyboard selection, exact evidence and exact C4 membership", async () => {
  const d = fixture();
  const e = d.payload.elements[2]!;
  e.refs = [
    { kind: "record", recordType: "node", id: "first" },
    { kind: "record", recordType: "node", id: "second" },
  ];
  e.architectureElementId = "member";
  d.payload.architecture = { id: "arch", version: 2, contentHash: "hash" };
  const user = userEvent.setup(),
    select = vi.fn(),
    open = vi.fn(),
    c4 = vi.fn();
  renderWithProviders(
    <BackendBusinessMap
      payload={d.payload}
      gaps={[]}
      selection={{ type: "element", id: e.id }}
      onSelect={select}
      onOpen={open}
      onArchitecture={c4}
      disabled={false}
    />,
  );
  screen.getByRole("button", { name: "OrderPaid" }).focus();
  await user.keyboard("{Enter}");
  expect(select).toHaveBeenCalledWith({ type: "element", id: e.id });
  await user.click(screen.getByRole("button", { name: "Открыть точное основание 2" }));
  expect(open).toHaveBeenCalledWith(e.refs[1]);
  await user.click(screen.getByRole("button", { name: "Открыть C4 элемент" }));
  expect(c4).toHaveBeenCalledWith(d.payload.architecture, "member");
  expect(screen.queryByRole("button", { name: /Исполнить|Симулировать/ })).not.toBeInTheDocument();
});
it("authors cards and links with labelled forms and keeps semantic save explicit", async () => {
  const user = userEvent.setup(),
    save = vi.fn();
  function Editor() {
    const [d, set] = useState(fixture());
    return (
      <BackendBusinessMapEditor
        document={d}
        onChange={set}
        onSave={save}
        onCancel={() => {}}
        busy={false}
      />
    );
  }
  renderWithProviders(<Editor />);
  await fill(screen.getByLabelText("Название карточки"), "Refund");
  await fill(screen.getByLabelText("Основание замысла"), "Explicit intent");
  await user.selectOptions(screen.getByLabelText("Роль карточки"), "command");
  await user.click(screen.getByRole("button", { name: "Добавить карточку" }));
  expect((screen.getByLabelText("Business map JSON") as HTMLTextAreaElement).value).toContain(
    "Refund",
  );
  expect(save).not.toHaveBeenCalled();
  await user.selectOptions(screen.getByLabelText("От элемента"), fixture().payload.elements[0]!.id);
  await user.selectOptions(screen.getByLabelText("К элементу"), fixture().payload.elements[5]!.id);
  await user.selectOptions(screen.getByLabelText("Отношение"), "produces");
  await user.click(screen.getByRole("button", { name: "Добавить связь" }));
  expect(screen.getByRole("alert")).toHaveTextContent("роль");
  await user.selectOptions(screen.getByLabelText("Отношение"), "reads");
  await user.click(screen.getByRole("button", { name: "Добавить связь" }));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  await user.clear(screen.getByLabelText("Business map JSON"));
  expect(screen.getByRole("button", { name: "Сохранить business map" })).toBeDisabled();
});

import { BackendChangeCommandForms } from "./BackendChangeCommandForms";
it("hands an exact implementation ref to an explicit local proposal command without applying", async () => {
  const user = userEvent.setup(),
    add = vi.fn();
  const id = fixture().payload.elements[2]!.id;
  renderWithProviders(
    <BackendChangeCommandForms
      baseSchemaVersion="6"
      implementationRefs={[{ kind: "record", recordType: "node", id }]}
      onAdd={add}
    />,
  );
  await user.click(
    screen.getByRole("button", { name: "Использовать точный узел 1 для переименования" }),
  );
  expect(screen.getByDisplayValue(id)).toBeVisible();
  expect(add).not.toHaveBeenCalled();
});

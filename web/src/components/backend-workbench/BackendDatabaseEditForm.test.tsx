import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendDatabaseEditForm } from "./BackendDatabaseEditForm";
import { proposalNodes } from "./backendProposalTestFixtures";

it("opens the selected column as a NOT NULL draft without adding a command", () => {
  const add = vi.fn();
  renderWithProviders(
    <BackendDatabaseEditForm
      nodes={proposalNodes}
      facetKey="sql"
      initialColumnId="tenant-id"
      onAdd={add}
    />,
  );
  expect(screen.getByLabelText("Колонка")).toHaveValue("tenant-id");
  expect(screen.getByLabelText("Допускает NULL")).toHaveValue("false");
  expect(add).not.toHaveBeenCalled();
});

it("buffers a typed nullable false command through keyboard controls", async () => {
  const add = vi.fn();
  const user = userEvent.setup();
  renderWithProviders(<BackendDatabaseEditForm nodes={proposalNodes} facetKey="sql" onAdd={add} />);
  await user.selectOptions(screen.getByLabelText("Колонка"), "user-id");
  await user.selectOptions(screen.getByLabelText("Допускает NULL"), "false");
  await user.type(
    screen.getByRole("textbox", { name: /Причина изменения/ }),
    "Все заказы должны иметь пользователя",
  );
  await user.click(screen.getByRole("button", { name: "Добавить в буфер" }));
  expect(add).toHaveBeenCalledWith(
    expect.objectContaining({
      type: "alter_column",
      columnId: "user-id",
      nullable: false,
      reason: "Все заказы должны иметь пользователя",
    }),
  );
});

it("keeps ordered FK pairs when moving them with buttons", async () => {
  const add = vi.fn();
  const user = userEvent.setup();
  renderWithProviders(<BackendDatabaseEditForm nodes={proposalNodes} facetKey="sql" onAdd={add} />);
  await user.selectOptions(screen.getByLabelText("Изменение"), "create_fk");
  await user.selectOptions(screen.getByLabelText("Таблица источника"), "orders");
  await user.selectOptions(screen.getByLabelText("Таблица цели"), "users");
  await user.type(screen.getByLabelText("Имя FK"), "orders_users_fk");
  await user.selectOptions(screen.getByLabelText("Исходная колонка 1"), "tenant-id");
  await user.selectOptions(screen.getByLabelText("Целевая колонка 1"), "users-tenant");
  await user.click(screen.getByRole("button", { name: "Добавить пару" }));
  await user.selectOptions(screen.getByLabelText("Исходная колонка 2"), "user-id");
  await user.selectOptions(screen.getByLabelText("Целевая колонка 2"), "users-id");
  await user.click(screen.getByRole("button", { name: "Пара 2 вверх" }));
  await user.type(
    screen.getByRole("textbox", { name: /Причина изменения/ }),
    "Связать заказ с пользователем",
  );
  await user.click(screen.getByRole("button", { name: "Добавить в буфер" }));
  expect(add).toHaveBeenCalledWith(
    expect.objectContaining({
      type: "alter_constraint",
      action: "create",
      tableId: "orders",
      targetTableId: "users",
      columnPairs: [
        { fromColumnId: "user-id", toColumnId: "users-id" },
        { fromColumnId: "tenant-id", toColumnId: "users-tenant" },
      ],
      deferrable: false,
      initiallyDeferred: false,
    }),
  );
});

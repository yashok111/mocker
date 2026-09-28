import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import { renderWithProviders } from "@/test/render";
import { emptyCanvas } from "./canvasModel";
import { ScenarioContractsPanel } from "./ScenarioContractsPanel";

it("shows one modal while editing events from the contracts panel", async () => {
  renderWithProviders(
    <ScenarioContractsPanel
      opened
      onClose={vi.fn()}
      document={emptyCanvas()}
      version={1}
      updates={[]}
      dirty={false}
      pendingForms={false}
      pending={false}
      onCommand={vi.fn()}
      onCreateFromSchema={vi.fn()}
      onChangeDocument={vi.fn()}
      formStore={createFormDraftStore()}
    />,
  );
  const contracts = screen.getByRole("dialog", { name: "Контракты API" });
  await userEvent.click(
    within(contracts).getByRole("button", { name: "Событийные контракты Kafka" }),
  );
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  const events = screen.getByRole("dialog", { name: "События Kafka и AsyncAPI" });
  await userEvent.click(within(events).getByRole("button", { name: "Закрыть" }));
  expect(screen.getByRole("dialog", { name: "Контракты API" })).toBeInTheDocument();
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
});

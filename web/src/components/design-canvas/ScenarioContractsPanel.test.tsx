import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import { renderWithProviders } from "@/test/render";
import { emptyCanvas } from "./canvasModel";
import { ScenarioContractsPanel } from "./ScenarioContractsPanel";

vi.mock("./EventMapGraph", () => ({ default: () => <div data-testid="event-map-graph" /> }));

it("shows one modal while editing events from the contracts panel", async () => {
  renderWithProviders(
    <ScenarioContractsPanel
      opened
      onClose={vi.fn()}
      document={emptyCanvas()}
      baseRevisionId={2}
      version={1}
      updates={[]}
      dirty={false}
      pendingForms={false}
      pending={false}
      onCommand={vi.fn()}
      onCreateFromSchema={vi.fn()}
      onChangeDocument={vi.fn()}
      formStore={createFormDraftStore()}
      getEventMap={vi.fn()}
      analyzeEventMap={vi.fn()}
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

it("opens the map and navigates to the canonical event editor without stacking modals", async () => {
  const document = {
    ...emptyCanvas(),
    formatVersion: 3 as const,
    eventModel: {
      servers: [],
      channels: [
        {
          id: "retry",
          name: "Retry",
          description: "",
          address: "orders.retry",
          serverIds: [],
          messageIds: [],
        },
      ],
      messages: [],
      schemas: [],
      contracts: [],
    },
  };
  renderWithProviders(
    <ScenarioContractsPanel
      opened
      onClose={vi.fn()}
      document={document}
      baseRevisionId={2}
      version={1}
      updates={[]}
      dirty={false}
      pendingForms={false}
      pending={false}
      onCommand={vi.fn()}
      onCreateFromSchema={vi.fn()}
      onChangeDocument={vi.fn()}
      formStore={createFormDraftStore()}
      getEventMap={vi.fn().mockResolvedValue({
        scenarioId: 7,
        version: 1,
        revisionId: 2,
        proposed: false,
        nodes: [
          {
            id: "channel:retry",
            kind: "channel",
            label: "orders.retry",
            locator: { pointer: "/eventModel/channels/0", entityId: "retry" },
          },
        ],
        edges: [],
        diagnostics: [],
        coverage: {
          nodesReturned: 1,
          edgesReturned: 0,
          diagnosticsReturned: 0,
          truncatedReasons: [],
        },
        complete: true,
      })}
      analyzeEventMap={vi.fn()}
    />,
  );
  await userEvent.click(
    within(screen.getByRole("dialog", { name: "Контракты API" })).getByRole("button", {
      name: "Карта событий",
    }),
  );
  const map = screen.getByRole("dialog", { name: "Карта событий" });
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  await userEvent.click(await within(map).findByRole("button", { name: "Topic · orders.retry" }));
  await userEvent.click(within(map).getByRole("button", { name: "Открыть в редакторе событий" }));
  expect(screen.getByRole("dialog", { name: "События Kafka и AsyncAPI" })).toBeInTheDocument();
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect(screen.getByLabelText("Адрес topic")).toHaveValue("orders.retry");
});

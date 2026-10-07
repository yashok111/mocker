import { MantineProvider } from "@mantine/core";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { it, expect, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { makeQueryClient } from "@/test/render";
import { BackendObservations } from "./BackendObservations";
const state = vi.hoisted(() => ({
  scope: {
    pin: { id: "diagram-a", version: 1, contentHash: "a" },
    scopeHash: "scope-a",
    targetHash: "target",
    target: { revisionId: "revision" },
    selectors: [],
  },
  correlate: vi.fn(),
}));
vi.mock("./BackendObservationContext", () => ({
  useObservationScope: () => ({ scope: state.scope }),
}));
vi.mock("@/api/generated/backend-projects/backend-projects", () => {
  const version = {
    setId: "set",
    version: 1,
    contentHash: "hash",
    name: "ObservationFixture",
    recordCount: 1,
    context: { source: { serviceId: "service" }, input: { size: 0 } },
  };
  return {
    listBackendObservations: vi
      .fn()
      .mockResolvedValue({ status: 200, data: { items: [version], nextCursor: "" } }),
    getBackendObservationVersion: vi.fn().mockResolvedValue({ status: 200, data: version }),
    getBackendObservationRecords: vi
      .fn()
      .mockResolvedValue({ status: 200, data: { items: [], nextCursor: "" } }),
    correlateBackendObservations: state.correlate,
    getBackendObservationCorrelation: vi.fn(),
    adaptBackendObservations: vi.fn(),
    importBackendObservations: vi.fn(),
  };
});
it("isolates an in-flight correlation when an exact diagram scope changes", async () => {
  let finish!: (value: unknown) => void;
  state.correlate.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  // BackendObservations mounts BackendScenarioMeasurements (6006fa8), whose
  // job and result reads are React Query hooks, so the panel needs a client.
  const client = makeQueryClient();
  const tree = () => (
    <QueryClientProvider client={client}>
      <MantineProvider env="test">
        <BackendObservations projectId="project" />
      </MantineProvider>
    </QueryClientProvider>
  );
  const view = render(tree());
  fireEvent.click(screen.getByRole("button", { name: "Обновить версии" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Обновить версии" })).not.toBeDisabled(),
  );
  fireEvent.click(screen.getByRole("combobox", { name: "Точная версия наблюдений" }));
  // The measurement panel's NativeSelects add plain <option>s to the page,
  // so the observation version is picked by its own label.
  fireEvent.click(await screen.findByRole("option", { name: /ObservationFixture: v1/ }));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Подготовить точное сопоставление" }),
    ).not.toBeDisabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Подготовить точное сопоставление" }));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить новое сопоставление" }));
  await waitFor(() => expect(state.correlate).toHaveBeenCalledTimes(1));
  state.scope = {
    ...state.scope,
    pin: { id: "diagram-b", version: 1, contentHash: "b" },
    scopeHash: "scope-b",
  };
  view.rerender(tree());
  await act(async () =>
    finish({
      status: 200,
      data: {
        version: 1,
        contentHash: "STALE-CORRELATION",
        sourceCompatible: true,
        input: {},
        rows: [],
        diagramRows: [],
        gaps: [],
      },
    }),
  );
  expect(screen.queryByText(/STALE-CORRELATION/)).not.toBeInTheDocument();
  expect(screen.getByLabelText(/Correlation JSON/)).toHaveValue("");
  expect(screen.getByRole("button", { name: "Сохранить новое сопоставление" })).toBeDisabled();
});

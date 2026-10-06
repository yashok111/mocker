import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BackendModelSearchPanel } from "./BackendModelSearchPanel";
const read = vi.hoisted(() => vi.fn());
vi.mock("./backendGraphReads", () => ({
  readBackendGraph: read,
  readBackendCoverage: async () => ({ snapshots: [] }),
  backendReadQueryKey: (...args: unknown[]) => args,
}));
it("searches the whole exact model and shows model count separately from canvas/page", async () => {
  read.mockImplementation(async (_p, _t, q) => ({
    nodes:
      q.kind === "service"
        ? [{ id: "service", name: "Orders", kind: "service" }]
        : [{ id: "node", name: "orders", kind: "table" }],
    edges: [],
    nextCursor: "",
    total: 42,
  }));
  const onSelect = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <BackendModelSearchPanel
          projectId="p"
          target={{ revisionId: "exact" }}
          onSelect={onSelect}
        />
      </QueryClientProvider>
    </MantineProvider>,
  );
  await screen.findByText(/В модели: 42; на странице: 1/);
  fireEvent.change(screen.getByLabelText("Поиск по всей модели"), { target: { value: "orders" } });
  fireEvent.change(screen.getByLabelText("Сервис в дереве модели"), {
    target: { value: "service" },
  });
  fireEvent.change(screen.getByLabelText("Certainty модели"), { target: { value: "inferred" } });
  fireEvent.click(screen.getByRole("button", { name: "Искать в точной модели" }));
  await waitFor(() =>
    expect(read).toHaveBeenCalledWith(
      "p",
      { revisionId: "exact" },
      expect.objectContaining({ search: "orders", serviceId: "service", certainty: "inferred" }),
      expect.any(AbortSignal),
    ),
  );
  fireEvent.click(await screen.findByRole("button", { name: "orders · table" }));
  expect(onSelect).toHaveBeenCalledWith({ recordType: "node", id: "node" });
  client.clear();
});

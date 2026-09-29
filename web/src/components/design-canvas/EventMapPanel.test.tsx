import { useState } from "react";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import type { DesignScenarioEventMapReport } from "@/api/generated/schemas";
import { emptyCanvas } from "./canvasModel";
import EventMapPanel from "./EventMapPanel";
import type { CanvasDocument } from "./types";

vi.mock("./EventMapGraph", () => ({ default: () => <div data-testid="event-map-graph" /> }));

const report: DesignScenarioEventMapReport = {
  scenarioId: 7,
  version: 4,
  revisionId: 12,
  proposed: false,
  nodes: [
    {
      id: "operation:consume-a",
      kind: "operation",
      label: "Consume A",
      locator: {
        pointer: "/eventModel/contracts/0/operations/0",
        contractId: "events",
        operationId: "consume-a",
      },
      groupId: "billing",
      clientId: "worker-a",
    },
    {
      id: "operation:consume-b",
      kind: "operation",
      label: "Consume B",
      locator: {
        pointer: "/eventModel/contracts/0/operations/1",
        contractId: "events",
        operationId: "consume-b",
      },
      groupId: "shipping",
    },
    {
      id: "channel:retry",
      kind: "channel",
      label: "orders.retry",
      locator: { pointer: "/eventModel/channels/0", entityId: "retry" },
    },
    {
      id: "schema:payload",
      kind: "schema",
      label: "OrderCreated payload",
      locator: { pointer: "/eventModel/schemas/0", entityId: "payload" },
    },
  ],
  edges: [
    {
      id: "retry-a",
      kind: "retry",
      source: "operation:consume-a",
      target: "channel:retry",
      label: "retry",
      locator: {
        pointer: "/eventModel/contracts/0/operations/0/failureRoutes/retryChannelId",
        contractId: "events",
        operationId: "consume-a",
      },
    },
    {
      id: "dlq-b",
      kind: "dead_letter",
      source: "operation:consume-b",
      target: "channel:retry",
      label: "DLQ",
      locator: {
        pointer: "/eventModel/contracts/0/operations/1/failureRoutes/deadLetterChannelId",
        contractId: "events",
        operationId: "consume-b",
      },
    },
  ],
  diagnostics: [
    {
      id: "missing",
      code: "unbound",
      severity: "warning",
      pointer: "/eventModel/contracts/0/operations/0",
      message: "Операция не связана со стрелкой",
      elementId: "operation:consume-a",
    },
  ],
  coverage: { nodesReturned: 4, edgesReturned: 2, diagnosticsReturned: 1, truncatedReasons: [] },
  complete: false,
};

it("uses the saved projection and keeps groups, routes, diagnostics and canonical navigation in the full list", async () => {
  const getEventMap = vi.fn().mockResolvedValue(report);
  const onEditEvent = vi.fn();
  renderWithProviders(
    <EventMapPanel
      opened
      onClose={vi.fn()}
      document={emptyCanvas()}
      baseRevisionId={12}
      dirty={false}
      pendingForms={false}
      getEventMap={getEventMap}
      analyzeEventMap={vi.fn()}
      onEditEvent={onEditEvent}
    />,
  );
  expect(
    await screen.findByRole("button", { name: /Операция события · Consume A/ }),
  ).toBeInTheDocument();
  expect(getEventMap).toHaveBeenCalledTimes(1);
  expect(screen.getByText(/billing · worker-a/)).toBeInTheDocument();
  expect(screen.getByText(/shipping/)).toBeInTheDocument();
  expect(screen.getByText("Операция не связана со стрелкой")).toBeInTheDocument();
  const list = screen.getByRole("region", { name: "Полный список карты событий" });
  await userEvent.click(
    within(list).getByRole("button", { name: /retry · Consume A → orders.retry/ }),
  );
  expect(
    screen.getByText("/eventModel/contracts/0/operations/0/failureRoutes/retryChannelId"),
  ).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Открыть в редакторе событий" }));
  expect(onEditEvent).toHaveBeenCalledWith({
    kind: "event-contract",
    id: "events",
    pointer: "/eventModel/contracts/0/operations/0/failureRoutes/retryChannelId",
  });
  expect(screen.getByTestId("event-map-graph")).toBeInTheDocument();
});

it("requests the exact base revision and refreshes when that revision changes", async () => {
  const getEventMap = vi.fn().mockResolvedValue(report);
  function Harness() {
    const [baseRevisionId, setBaseRevisionId] = useState(12);
    return (
      <>
        <button onClick={() => setBaseRevisionId(13)}>Обновить базовую ревизию</button>
        <EventMapPanel
          opened
          onClose={vi.fn()}
          document={emptyCanvas()}
          baseRevisionId={baseRevisionId}
          dirty={false}
          pendingForms={false}
          getEventMap={getEventMap}
          analyzeEventMap={vi.fn()}
          onEditEvent={vi.fn()}
        />
      </>
    );
  }
  renderWithProviders(<Harness />);
  await waitFor(() => expect(getEventMap).toHaveBeenCalledTimes(1));
  expect(getEventMap.mock.calls[0]![0]).toBe(12);
  await userEvent.click(screen.getByRole("button", { name: "Обновить базовую ревизию" }));
  await waitFor(() => expect(getEventMap).toHaveBeenCalledTimes(2));
  expect(getEventMap.mock.calls[1]![0]).toBe(13);
});

it("uses proposed POST for local edits, aborts stale requests, and pauses for pending raw forms", async () => {
  const finishes: ((value: DesignScenarioEventMapReport) => void)[] = [];
  const analyzeEventMap = vi.fn().mockImplementation(
    () =>
      new Promise<DesignScenarioEventMapReport>((resolve) => {
        finishes.push(resolve);
      }),
  );
  const getEventMap = vi.fn().mockResolvedValue(report);
  const first = { ...emptyCanvas(), title: "First" };
  function Harness() {
    const [document, setDocument] = useState<CanvasDocument>(first);
    const [pendingForms, setPendingForms] = useState(false);
    return (
      <>
        <button onClick={() => setDocument({ ...document, title: "Second" })}>
          Изменить черновик
        </button>
        <button onClick={() => setPendingForms(true)}>Начать правку JSON</button>
        <EventMapPanel
          opened
          onClose={vi.fn()}
          document={document}
          baseRevisionId={12}
          dirty
          pendingForms={pendingForms}
          getEventMap={getEventMap}
          analyzeEventMap={analyzeEventMap}
          onEditEvent={vi.fn()}
        />
      </>
    );
  }
  renderWithProviders(<Harness />);
  expect(analyzeEventMap).toHaveBeenCalledTimes(1);
  const oldSignal = analyzeEventMap.mock.calls[0]![1] as AbortSignal;
  await userEvent.click(screen.getByRole("button", { name: "Изменить черновик" }));
  expect(oldSignal.aborted).toBe(true);
  expect(analyzeEventMap).toHaveBeenCalledTimes(2);
  await act(async () =>
    finishes[1]!({ ...report, proposed: true, revisionId: undefined, nodes: [] }),
  );
  expect(screen.getByText(/Предпросмотр несохранённого черновика/)).toBeInTheDocument();
  await act(async () => finishes[0]!(report));
  expect(
    screen.queryByRole("button", { name: /Операция события · Consume A/ }),
  ).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Начать правку JSON" }));
  expect(screen.getByText(/Незавершённая правка JSON/)).toBeInTheDocument();
  expect(screen.queryByText("Consume A")).not.toBeInTheDocument();
  expect(getEventMap).not.toHaveBeenCalled();
  await waitFor(() => expect(analyzeEventMap.mock.calls[1]![1].aborted).toBe(true));
});

it("shows linked HTTP and lifecycle targets from the embedded contract snapshot", async () => {
  const document: CanvasDocument = {
    ...emptyCanvas(),
    contracts: [
      {
        id: "http",
        name: "Orders HTTP",
        mode: "copy",
        source: { designId: 9, revisionId: 42 },
        document: {
          openapi: "3.1.0",
          info: { title: "Orders", version: "1" },
          paths: {
            "/orders": {
              post: {
                "x-mocker-canvas-operation-id": "create-order",
                summary: "Create order",
                responses: { "200": { description: "OK" } },
              },
            },
          },
          "x-mocker-state-diagrams": {
            formatVersion: 1,
            diagrams: [
              {
                id: "order",
                name: "Order",
                initialStateId: "new",
                states: [{ id: "new", name: "New", x: 0, y: 0, terminal: false }],
                transitions: [
                  {
                    id: "created",
                    name: "Created",
                    from: "new",
                    to: "new",
                    patchJSON: "{}",
                    responseStatus: 200,
                    binding: { method: "POST", path: "/orders" },
                  },
                ],
              },
            ],
          },
        },
      },
    ],
  };
  const linked = {
    ...report,
    nodes: [
      {
        id: "api:create",
        kind: "api_operation" as const,
        label: "Create order",
        locator: {
          pointer: "/eventModel/contracts/0/operations/0/apiLinks/0",
          httpContractId: "http",
          operationKey: "create-order",
          mode: "copy" as const,
          pinnedRevisionId: 42,
        },
      },
      {
        id: "state:created",
        kind: "state_transition" as const,
        label: "Created",
        locator: {
          pointer: "/eventModel/contracts/0/operations/0/stateLinks/0",
          httpContractId: "http",
          diagramId: "order",
          transitionId: "created",
          mode: "copy" as const,
          pinnedRevisionId: 42,
        },
      },
    ],
    edges: [],
  } satisfies DesignScenarioEventMapReport;
  renderWithProviders(
    <EventMapPanel
      opened
      onClose={vi.fn()}
      document={document}
      baseRevisionId={12}
      dirty={false}
      pendingForms={false}
      getEventMap={vi.fn().mockResolvedValue(linked)}
      analyzeEventMap={vi.fn()}
      onEditEvent={vi.fn()}
    />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Операция API · Create order" }));
  expect(screen.getByText(/POST \/orders · Create order/)).toBeInTheDocument();
  expect(screen.getByText(/Копия · API-ревизия 42/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Переход состояния · Created" }));
  expect(screen.getByText(/Order · Created · new → new · POST \/orders/)).toBeInTheDocument();
  expect(screen.getByText(/Текущий черновик API может отличаться/)).toBeInTheDocument();
});

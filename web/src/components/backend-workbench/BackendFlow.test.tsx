import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { act, waitFor } from "@testing-library/react";
import { useState } from "react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendFlow } from "./BackendFlow";

vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => <div>Flow canvas</div> }));
afterEach(() => vi.unstubAllGlobals());
const coverage = {
  coverage: { status: "partial", denominator: null, knownObjects: 2, gaps: ["Unanalysed branch"] },
  snapshots: [],
  inventory: [],
};
const operation = {
  id: "endpoint",
  externalKey: "endpoint",
  kind: "http_operation",
  name: "GET /orders",
  parentId: null,
  attributes: {},
  evidenceIds: [],
};
const node = (id: string) => ({
  ...operation,
  id,
  kind: "flow_step",
  name: id,
  attributes: {
    stepKind: "validation",
    analysisStatus: "partial",
    gaps: [],
    nativeText: "validate(input)",
    transactionContext: { status: "unknown", reason: "No transaction evidence" },
  },
});
const page = (input: Record<string, unknown>, items: Record<string, unknown>) => ({
  projectId: "project",
  revisionId: input.revisionId,
  semanticHash: "a".repeat(64),
  coverage,
  limitations: [],
  truncated: false,
  truncationReasons: [],
  nextCursor: "",
  view: input.view,
  ...items,
});
function route(
  handlers: Record<string, (request: { body: unknown }) => Response | Promise<Response>>,
) {
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    const handler = handlers[`${init?.method ?? "GET"} ${url.split("?")[0]}`];
    return handler
      ? handler({ body: init?.body ? JSON.parse(String(init.body)) : undefined })
      : json(500, { error: "Unrouted" });
  });
}

it("pins independent pages, announces truncation and restores keyboard focus from the inspector", async () => {
  const requests: Record<string, unknown>[] = [];
  route({
    "POST /api/backend-projects/project/flow/query": async ({ body }) => {
      const input = body as Record<string, unknown>;
      requests.push(input);
      if (input.view === "entrypoints")
        return json(
          200,
          page(input, {
            entrypointItems: [
              {
                operation,
                handlerIds: [],
                flowIds: ["flow"],
                unresolvedHandles: [],
                evidenceIds: [],
                limitations: [],
              },
            ],
          }),
        );
      if (input.view === "steps")
        return json(
          200,
          page(input, {
            stepItems: [node(input.cursor ? "step2" : "step1")],
            nextCursor: input.cursor ? "" : "steps-next",
          }),
        );
      if (input.view === "transitions")
        return json(200, page(input, { transitionItems: [], nextCursor: "transitions-next" }));
      return json(
        200,
        page(input, {
          accessItems: [
            {
              accessEdgeId: "access",
              queryId: "query",
              targetId: "table",
              datastoreId: "db",
              facetKey: "sql",
              accessKind: "reads",
              accessMode: "direct",
              relation: "possible",
              entrypointId: "endpoint",
              flowId: "flow",
              pathNodeIds: [],
              pathEdgeIds: [],
              status: "inferred",
              evidenceIds: [],
              limitations: ["Dynamic dispatch"],
            },
          ],
          truncated: true,
          truncationReasons: ["call_hops"],
        }),
      );
    },
    "GET /api/backend-projects/project/revisions/source/nodes/step1": () =>
      json(200, node("step1")),
    "GET /api/backend-projects/project/revisions/source/evidence": () =>
      json(200, { items: [], nextCursor: "" }),
    "POST /api/backend-projects/project/graph/query": () =>
      json(200, { nodes: [], edges: [], nextCursor: "" }),
  });
  renderWithProviders(<BackendFlow projectId="project" revisionId="source" />);
  await userEvent.click(await screen.findByRole("button", { name: "Открыть Flow GET /orders" }));
  expect(await screen.findByText("Возможная связь")).toBeInTheDocument();
  expect(screen.getByText(/Поиск ограничен: call_hops/)).toBeInTheDocument();
  const step = await screen.findByRole("button", { name: "Открыть шаг step1" });
  step.focus();
  await userEvent.keyboard("{Enter}");
  expect(await screen.findByRole("heading", { name: "step1" })).toHaveFocus();
  expect(screen.getByText("validate(input)")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Закрыть инспектор Flow" }));
  expect(step).toHaveFocus();
  await userEvent.click(screen.getByRole("button", { name: "Следующие шаги" }));
  expect(await screen.findByRole("button", { name: "Открыть шаг step2" })).toBeInTheDocument();
  expect(requests.find((input) => input.cursor === "steps-next")).toMatchObject({
    revisionId: "source",
    view: "steps",
    flowId: "flow",
  });
  expect(requests.filter((input) => input.view === "transitions")).toHaveLength(1);
});

it("offers an explicit retry after a failed endpoint page", async () => {
  let attempts = 0;
  route({
    "POST /api/backend-projects/project/flow/query": ({ body }) =>
      ++attempts === 1
        ? json(503, { error: "Unavailable" })
        : json(200, page(body as Record<string, unknown>, { entrypointItems: [] })),
  });
  renderWithProviders(<BackendFlow projectId="project" revisionId="source" />);
  await userEvent.click(
    await screen.findByRole("button", { name: "Повторить загрузку точек входа" }),
  );
  expect(await screen.findByText(/Точки входа не найдены/)).toBeInTheDocument();
  expect(attempts).toBe(2);
});

it("scopes the visible coverage summary to inventory and expands details without hiding runtime gaps or quotas", async () => {
  const inventoryCoverage = {
    ...coverage,
    coverage: { status: "complete", denominator: 2, knownObjects: 2, gaps: [] },
    inventory: [
      {
        category: "files",
        status: "complete",
        knownCount: 1,
        denominator: 1,
        discoverySource: "File discovery proof",
        gaps: [],
      },
    ],
  };
  route({
    "POST /api/backend-projects/project/flow/query": ({ body }) => {
      const input = body as Record<string, unknown>;
      return json(
        200,
        page(input, {
          coverage: inventoryCoverage,
          ...(input.view === "entrypoints"
            ? {
                entrypointItems: [],
                limitations: ["Runtime flow limitation"],
                truncated: true,
                truncationReasons: ["edge_limit"],
              }
            : input.view === "steps"
              ? {
                  stepItems: [
                    {
                      ...node("partial-step"),
                      attributes: {
                        ...node("partial-step").attributes,
                        gaps: ["Runtime step gap"],
                      },
                    },
                  ],
                }
              : { transitionItems: [] }),
        }),
      );
    },
  });
  renderWithProviders(
    <BackendFlow projectId="project" revisionId="source" pin={{ flowId: "flow" }} />,
  );
  expect((await screen.findAllByText(/Покрытие инвентаря ревизии: Полное/))[0]).toBeVisible();
  expect(await screen.findByRole("button", { name: "Открыть шаг partial-step" })).toHaveTextContent(
    "Частичное",
  );
  expect(screen.getByText("Runtime step gap")).toBeVisible();
  expect(screen.getByText("Runtime flow limitation")).toBeVisible();
  expect(screen.getByText(/Поиск ограничен: edge_limit/)).toBeVisible();
  const details = screen.getAllByText("Подробности инвентаря ревизии")[0]!;
  expect(details.closest("details")).not.toHaveAttribute("open");
  expect(screen.getAllByText(/File discovery proof/)[0]).not.toBeVisible();
  await userEvent.click(details);
  expect(details.closest("details")).toHaveAttribute("open");
  expect(screen.getAllByText(/File discovery proof/)[0]).toBeVisible();
  expect(screen.getByText("Runtime step gap")).toBeVisible();
  expect(screen.getByText(/Поиск ограничен: edge_limit/)).toBeVisible();
});

it("cancels late pages when the immutable source selection changes", async () => {
  let finish: (response: Response) => void = () => {};
  let oldSignal: AbortSignal | null | undefined;
  vi.stubGlobal("fetch", async (_url: string, init?: RequestInit) => {
    const input = JSON.parse(String(init?.body)) as Record<string, unknown>;
    if (input.revisionId === "source") {
      oldSignal = init?.signal;
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    }
    return json(
      200,
      page(input, {
        entrypointItems: [
          {
            operation: { ...operation, name: "New endpoint" },
            handlerIds: [],
            flowIds: [],
            unresolvedHandles: [],
            evidenceIds: [],
            limitations: [],
          },
        ],
      }),
    );
  });
  function Harness() {
    const [revision, setRevision] = useState("source");
    return (
      <>
        <button onClick={() => setRevision("new-source")}>Другой источник</button>
        <BackendFlow projectId="project" revisionId={revision} />
      </>
    );
  }
  renderWithProviders(<Harness />);
  await waitFor(() => expect(oldSignal).toBeDefined());
  expect(screen.getByLabelText("Загружаем точек входа")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Другой источник" }));
  expect(
    await screen.findByRole("button", { name: "Открыть Flow New endpoint" }),
  ).toBeInTheDocument();
  expect(oldSignal?.aborted).toBe(true);
  await act(async () =>
    finish(
      json(
        200,
        page(
          { revisionId: "source", view: "entrypoints" },
          {
            entrypointItems: [
              {
                operation,
                handlerIds: [],
                flowIds: [],
                unresolvedHandles: [],
                evidenceIds: [],
                limitations: [],
              },
            ],
          },
        ),
      ),
    ),
  );
  expect(
    screen.queryByRole("button", { name: "Открыть Flow GET /orders" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText(/ревизия new-source/)).toBeInTheDocument();
});

it("keeps long transaction collapse captions shrinkable and wrapped inside a375px workspace", async () => {
  const transactionId = "00000000-0000-0000-0000-000000000003";
  const name = "Orders transaction with a long source name repeated for narrow layout";
  route({
    "POST /api/backend-projects/project/flow/query": ({ body }) => {
      const input = body as Record<string, unknown>;
      return json(
        200,
        page(
          input,
          input.view === "entrypoints"
            ? { entrypointItems: [] }
            : input.view === "steps"
              ? {
                  stepItems: [
                    {
                      ...node("step"),
                      attributes: {
                        ...node("step").attributes,
                        transactionContext: { status: "known", transactionId },
                      },
                    },
                  ],
                }
              : { transitionItems: [] },
        ),
      );
    },
    "POST /api/backend-projects/project/graph/query": () =>
      json(200, {
        nodes: [{ ...operation, id: transactionId, kind: "transaction", name, parentId: "flow" }],
        edges: [],
        nextCursor: "",
      }),
  });
  renderWithProviders(
    <div style={{ width: 375 }}>
      <BackendFlow projectId="project" revisionId="source" pin={{ flowId: "flow" }} />
    </div>,
  );
  const collapse = await screen.findByRole("button", {
    name: `Свернуть транзакцию ${name} · ${transactionId}`,
  });
  expect(collapse).toHaveStyle({ minWidth: "0", maxWidth: "100%", height: "auto" });
  const label = collapse.querySelector(".mantine-Button-label");
  expect(label).toHaveStyle({ whiteSpace: "normal", overflowWrap: "anywhere" });
  expect(collapse).toHaveAccessibleName(`Свернуть транзакцию ${name} · ${transactionId}`);
});

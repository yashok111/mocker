import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackendGraphInventory } from "./BackendGraphInventory";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import type { BackendNode, BackendRevisionCoverage } from "@/api/generated/schemas";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const projectId = "0197aaf9-5555-7000-8000-000000000001";
const revisionId = "0197aaf9-5555-7000-8000-000000000002";
const nodeId = "0197aaf9-5555-7000-8000-000000000003";
const edgeId = "0197aaf9-5555-7000-8000-000000000004";
const snapshotId = "0197aaf9-5555-7000-8000-000000000005";
const repositoryId = "0197aaf9-5555-7000-8000-000000000006";
const evidenceId = "0197aaf9-5555-7000-8000-000000000007";
const hash = "a".repeat(64);
const node: BackendNode = {
  id: nodeId,
  externalKey: "handler-orders",
  kind: "handler",
  name: "ListOrders",
  parentId: null,
  attributes: { language: "Go", description: null },
  evidenceIds: [evidenceId],
};
const coverage: BackendRevisionCoverage = {
  coverage: {
    status: "partial",
    denominator: null,
    knownObjects: 1,
    gaps: ["Jobs were not analyzed"],
  },
  inventory: [
    "files",
    "endpoints",
    "datastores",
    "migrations",
    "producers",
    "consumers",
    "jobs",
    "contracts",
    "tests",
  ].map((category) => ({
    category: category as BackendRevisionCoverage["inventory"][number]["category"],
    status: "partial",
    knownCount: category === "files" ? 1 : 0,
    denominator: null,
    discoverySource: "agent",
    gaps: ["Profile only inventories foundational objects"],
    reason: "",
  })),
  snapshots: [
    {
      id: snapshotId,
      repositoryId,
      manifestHash: hash,
      dirty: true,
      consistency: "unverified",
      capturedAt: "2026-09-30T10:00:00Z",
      files: [{ path: "main.go", contentHash: hash, fileType: "go", analysisStatus: "analyzed" }],
      provider: {
        name: "local-agent",
        version: "1",
        namespace: "demo",
        method: "agent",
        profiles: ["foundation-graph-v1"],
        limitations: ["No runtime dispatch"],
      },
    },
  ],
};
const evidence = {
  id: evidenceId,
  externalKey: "ev-handler",
  subjectId: nodeId,
  method: "agent",
  status: "inferred",
  source: {
    repositoryId,
    snapshotId,
    file: "main.go",
    contentHash: hash,
    startLine: 5,
    endLine: 8,
  },
  explanation: "The route registers this handler",
  snippet: "func ListOrders() {}",
};

function fakeServer(
  overrides: {
    graph?: (input: Record<string, unknown>) => Response;
    evidence?: (subjectId: string) => Response | Promise<Response>;
  } = {},
) {
  const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(url), "http://localhost");
    if (path.pathname.endsWith("/coverage")) return json(200, coverage);
    if (path.pathname.endsWith("/graph/query")) {
      const input = JSON.parse(String(init?.body));
      if (overrides.graph) return overrides.graph(input);
      return json(
        200,
        input.recordType === "nodes"
          ? { nodes: [node], edges: [], nextCursor: "" }
          : { nodes: [], edges: [], nextCursor: "" },
      );
    }
    if (path.pathname.includes("/nodes/")) return json(200, node);
    if (path.pathname.endsWith("/evidence")) {
      if (overrides.evidence) return overrides.evidence(path.searchParams.get("subjectId") ?? "");
      return json(200, { items: [evidence], nextCursor: "" });
    }
    return json(500, { error: { code: "unrouted", message: path.pathname } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

it("inspects source-backed objects and evidence while preserving unknown coverage", async () => {
  const fetchMock = fakeServer();
  renderWithProviders(<BackendGraphInventory projectId={projectId} revisionId={revisionId} />);
  expect(await screen.findByText("Jobs were not analyzed")).toBeInTheDocument();
  expect(screen.getByText("Стабильность не проверена")).toBeInTheDocument();
  expect(screen.queryByText("100%")).not.toBeInTheDocument();
  await userEvent.click(await screen.findByRole("button", { name: "Открыть объект ListOrders" }));
  const inspector = await screen.findByRole("region", { name: "Инспектор объекта" });
  expect(
    await within(inspector).findByText("The route registers this handler"),
  ).toBeInTheDocument();
  expect(within(inspector).getByText("main.go:5–8")).toBeInTheDocument();
  expect(within(inspector).getByText("agent · inferred")).toBeInTheDocument();
  expect(within(inspector).getByText("func ListOrders() {}")).toBeInTheDocument();
  const graphCalls = fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/graph/query"));
  expect(graphCalls.map(([, init]) => JSON.parse(String(init?.body)).revisionId)).toEqual([
    revisionId,
    revisionId,
    revisionId,
  ]);
});

it("resets node pagination and selection when applying a new search", async () => {
  const fetchMock = fakeServer({
    graph: (input) =>
      json(200, {
        nodes: input.recordType === "nodes" ? (input.search ? [] : [node]) : [],
        edges: [],
        nextCursor:
          input.recordType === "nodes" && !input.cursor && !input.search ? "next-page" : "",
      }),
  });
  renderWithProviders(<BackendGraphInventory projectId={projectId} revisionId={revisionId} />);
  await userEvent.click(await screen.findByRole("button", { name: "Следующие объекты" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Предыдущие объекты" })).toBeEnabled(),
  );
  await userEvent.click(screen.getByRole("button", { name: "Открыть объект ListOrders" }));
  await screen.findByRole("region", { name: "Инспектор объекта" });
  await userEvent.type(screen.getByRole("textbox", { name: "Название объекта" }), "missing");
  await userEvent.click(screen.getByRole("button", { name: "Найти объекты" }));
  expect(await screen.findByText("Объекты не найдены")).toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "Инспектор объекта" })).not.toBeInTheDocument();
  const searchInput = fetchMock.mock.calls
    .filter(([url]) => String(url).endsWith("/graph/query"))
    .map(([, init]) => JSON.parse(String(init?.body)))
    .find((input) => input.search === "missing");
  expect(searchInput.cursor).toBe("");
  expect(searchInput.revisionId).toBe(revisionId);
});

it("does not carry object selection or a late evidence response into another revision", async () => {
  let resolveEvidence: ((response: Response) => void) | undefined;
  fakeServer({
    evidence: () =>
      new Promise((resolve) => {
        resolveEvidence = resolve;
      }),
  });
  function Host() {
    const [selected, select] = useState(revisionId);
    return (
      <>
        <button onClick={() => select(snapshotId)}>Сменить ревизию</button>
        <BackendGraphInventory projectId={projectId} revisionId={selected} />
      </>
    );
  }
  renderWithProviders(<Host />);
  await userEvent.click(await screen.findByRole("button", { name: "Открыть объект ListOrders" }));
  await waitFor(() => expect(resolveEvidence).toBeDefined());
  await userEvent.click(screen.getByRole("button", { name: "Сменить ревизию" }));
  resolveEvidence?.(json(200, { items: [evidence], nextCursor: "" }));
  await screen.findByRole("button", { name: "Открыть объект ListOrders" });
  expect(screen.queryByRole("region", { name: "Инспектор объекта" })).not.toBeInTheDocument();
  expect(screen.queryByText("The route registers this handler")).not.toBeInTheDocument();
});

it("shows query errors and retries without presenting an empty graph", async () => {
  let calls = 0;
  fakeServer({
    graph: () =>
      ++calls === 1
        ? json(500, { error: { code: "backend_internal", message: "Граф недоступен" } })
        : json(200, { nodes: [node], edges: [], nextCursor: "" }),
  });
  renderWithProviders(<BackendGraphInventory projectId={projectId} revisionId={revisionId} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("Граф недоступен");
  expect(screen.queryByText("Объекты не найдены")).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Повторить загрузку объектов" }));
  expect(
    await screen.findByRole("button", { name: "Открыть объект ListOrders" }),
  ).toBeInTheDocument();
});

it("opens the evidence of a relationship separately from the node evidence", async () => {
  fakeServer({
    graph: (input) =>
      json(
        200,
        input.recordType === "nodes"
          ? { nodes: [node], edges: [], nextCursor: "" }
          : {
              nodes: [],
              edges: input.from
                ? [
                    {
                      id: edgeId,
                      externalKey: "calls-orders",
                      kind: "calls",
                      from: nodeId,
                      to: snapshotId,
                      attributes: {},
                      evidenceIds: [evidenceId],
                    },
                  ]
                : [],
              nextCursor: "",
            },
      ),
    evidence: (subjectId) =>
      json(200, {
        items: [
          {
            ...evidence,
            subjectId,
            explanation:
              subjectId === edgeId ? "Relationship inferred from call" : evidence.explanation,
          },
        ],
        nextCursor: "",
      }),
  });
  renderWithProviders(<BackendGraphInventory projectId={projectId} revisionId={revisionId} />);
  await userEvent.click(await screen.findByRole("button", { name: "Открыть объект ListOrders" }));
  await userEvent.click(
    await screen.findByRole("button", { name: `Основания связи calls ${edgeId}` }),
  );
  expect(await screen.findByText("Relationship inferred from call")).toBeInTheDocument();
  expect(screen.queryByText(evidence.explanation)).not.toBeInTheDocument();
});

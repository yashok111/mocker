import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendRevisionCompare } from "./BackendRevisionCompare";

const projectId = "0197aaf9-5555-7000-8000-000000000001";
const oldId = "0197aaf9-5555-7000-8000-000000000002";
const newId = "0197aaf9-5555-7000-8000-000000000003";
const edgeId = "0197aaf9-5555-7000-8000-000000000004";
const evidenceId = "0197aaf9-5555-7000-8000-000000000005";
const snapshotId = "0197aaf9-5555-7000-8000-000000000006";
const hash = "a".repeat(64);
const coverage = {
  status: "partial",
  knownObjects: 1,
  denominator: null,
  gaps: ["Only a partial profile"],
};
const pin = (revisionId: string) => ({
  revisionId,
  semanticHash: hash,
  primarySnapshotId: snapshotId,
  manifestHash: hash,
  sourceSnapshotIds: [snapshotId],
});
const removed = {
  recordType: "edge",
  id: edgeId,
  changeKinds: ["removed"],
  changedPaths: [],
  before: { projectId, revisionId: oldId, recordType: "edge", id: edgeId },
  after: null,
  nameBefore: null,
  nameAfter: null,
  keyBefore: "removed-call",
  keyAfter: null,
  freshnessBefore: null,
  freshnessAfter: null,
};
const comparison = {
  from: pin(oldId),
  to: pin(newId),
  comparisonVersion: 1,
  comparisonHash: hash,
  summary: {
    nodes: { added: 0, removed: 0, modified: 0 },
    edges: { added: 0, removed: 1, modified: 0 },
    evidence: { added: 0, removed: 1, modified: 0 },
    sourceChanges: 1,
    identityMappings: 0,
    freshnessChanges: 0,
  },
  coverageBefore: coverage,
  coverageAfter: coverage,
  limitations: ["Structural comparison does not establish runtime behavior"],
  items: [removed],
  nextCursor: "",
};
function server(
  compare?: (input: Record<string, unknown>) => Response | Promise<Response>,
  delayEvidence?: (params: URLSearchParams) => Response | Promise<Response>,
) {
  const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(url), "http://localhost");
    if (path.pathname.endsWith("/revisions/compare"))
      return compare ? compare(JSON.parse(String(init?.body))) : json(200, comparison);
    if (path.pathname.endsWith("/graph/query"))
      return json(200, {
        nodes: [],
        edges: [
          {
            id: edgeId,
            externalKey: "removed-call",
            kind: "calls",
            from: projectId,
            to: snapshotId,
            attributes: {},
            evidenceIds: [evidenceId],
          },
        ],
        nextCursor: "",
      });
    if (path.pathname.endsWith("/evidence"))
      return delayEvidence
        ? delayEvidence(path.searchParams)
        : json(200, {
            items: [
              {
                id: evidenceId,
                externalKey: "old-evidence",
                subjectId: edgeId,
                method: "ast",
                status: "explicit",
                source: {
                  repositoryId: projectId,
                  snapshotId,
                  file: "old.go",
                  contentHash: hash,
                  startLine: 1,
                  endLine: 2,
                },
                explanation: "Old deleted call evidence",
                snippet: "oldCall()",
              },
            ],
            nextCursor: "",
          });
    if (path.pathname.endsWith("/coverage"))
      return json(200, {
        coverage,
        inventory: [],
        snapshots: [
          {
            id: snapshotId,
            repositoryId: projectId,
            manifestHash: hash,
            role: "primary",
            dirty: false,
            consistency: "verified",
            capturedAt: "2026-09-30T10:00:00Z",
            provider: {
              name: "agent",
              version: "1",
              namespace: "repo",
              method: "agent",
              profiles: [],
              limitations: [],
            },
            files: [
              { path: "old.go", contentHash: hash, analysisStatus: "analyzed", fileType: "go" },
            ],
          },
        ],
      });
    return json(500, { error: { code: "unrouted", message: path.pathname } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function compare() {
  renderWithProviders(<BackendRevisionCompare projectId={projectId} initialRevisionId={oldId} />);
  const input = screen.getByRole("textbox", { name: "Ревизия после" });
  await userEvent.clear(input);
  await userEvent.type(input, newId);
  await userEvent.click(screen.getByRole("button", { name: "Сравнить ревизии" }));
}
it("opens a removed relationship and its old evidence using explicit revision and edge ID", async () => {
  const fetchMock = server();
  await compare();
  await userEvent.click(
    await screen.findByRole("button", { name: `Открыть изменение edge ${edgeId}` }),
  );
  const before = await screen.findByRole("region", { name: "До изменения" });
  expect(await within(before).findByText("Old deleted call evidence")).toBeInTheDocument();
  expect(within(before).getByText("oldCall()")).toBeInTheDocument();
  expect(screen.getByText("После: отсутствует")).toBeInTheDocument();
  const edgeCall = fetchMock.mock.calls.find(([url]) => String(url).endsWith("/graph/query"));
  expect(JSON.parse(String(edgeCall?.[1]?.body))).toEqual({
    revisionId: oldId,
    recordType: "edges",
    id: edgeId,
    limit: 1,
  });
  expect(
    fetchMock.mock.calls
      .filter(([url]) => String(url).includes("/evidence"))
      .every(([url]) => String(url).includes(oldId)),
  ).toBe(true);
  expect(screen.getAllByText(/Частичное покрытие/)).toHaveLength(2);
});
it("clears pagination and ignores a delayed old inspector when the explicit pins change", async () => {
  let resolveEvidence: ((response: Response) => void) | undefined;
  const fetchMock = server(
    (input) =>
      json(
        200,
        input.toRevisionId === oldId
          ? { ...comparison, from: pin(oldId), to: pin(oldId), items: [], nextCursor: "" }
          : { ...comparison, nextCursor: input.cursor ? "" : "page2" },
      ),
    () =>
      new Promise((resolve) => {
        resolveEvidence = resolve;
      }),
  );
  await compare();
  await userEvent.click(await screen.findByRole("button", { name: "Следующие изменения" }));
  await userEvent.click(
    await screen.findByRole("button", { name: `Открыть изменение edge ${edgeId}` }),
  );
  await waitFor(() => expect(resolveEvidence).toBeDefined());
  const input = screen.getByRole("textbox", { name: "Ревизия после" });
  await userEvent.clear(input);
  await userEvent.type(input, oldId);
  await userEvent.click(screen.getByRole("button", { name: "Сравнить ревизии" }));
  resolveEvidence?.(json(200, { items: [{ explanation: "Obsolete evidence" }], nextCursor: "" }));
  expect(await screen.findByText("Различий нет")).toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "До изменения" })).not.toBeInTheDocument();
  expect(screen.queryByText("Obsolete evidence")).not.toBeInTheDocument();
  const last = fetchMock.mock.calls
    .filter(([url]) => String(url).endsWith("/revisions/compare"))
    .at(-1);
  expect(JSON.parse(String(last?.[1]?.body))).toMatchObject({
    fromRevisionId: oldId,
    toRevisionId: oldId,
    cursor: "",
  });
});
it("distinguishes pending, read failure and empty comparison, with keyboard controls", async () => {
  let resolve: ((value: Response) => void) | undefined;
  let count = 0;
  server(() =>
    ++count === 1
      ? new Promise((done) => {
          resolve = done;
        })
      : json(200, { ...comparison, items: [] }),
  );
  await compare();
  expect(screen.getByLabelText("Загружаем сравнение")).toBeInTheDocument();
  resolve?.(json(500, { error: { code: "backend_internal", message: "Comparison unavailable" } }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Comparison unavailable");
  expect(screen.queryByText("Различий нет")).not.toBeInTheDocument();
  const retry = screen.getByRole("button", { name: "Повторить загрузку сравнение" });
  retry.focus();
  await userEvent.keyboard("{Enter}");
  expect(await screen.findByText("Различий нет")).toBeInTheDocument();
  expect(retry).not.toBeInTheDocument();
});
it("reads removed evidence by evidenceId and source changes from the pinned manifest", async () => {
  const evidenceItem = {
    ...removed,
    recordType: "evidence",
    id: evidenceId,
    before: { projectId, revisionId: oldId, recordType: "evidence", id: evidenceId },
  };
  const sourceItem = {
    ...removed,
    recordType: "source",
    id: "old.go",
    before: { projectId, revisionId: oldId, recordType: "source", snapshotId, path: "old.go" },
  };
  const fetchMock = server(() => json(200, { ...comparison, items: [evidenceItem, sourceItem] }));
  await compare();
  await userEvent.click(
    await screen.findByRole("button", { name: `Открыть изменение evidence ${evidenceId}` }),
  );
  expect(await screen.findByText("Old deleted call evidence")).toBeInTheDocument();
  const evidenceCall = fetchMock.mock.calls.find(([url]) => String(url).includes("/evidence"));
  const params = new URL(String(evidenceCall?.[0]), "http://localhost").searchParams;
  expect(params.get("evidenceId")).toBe(evidenceId);
  expect(params.has("subjectId")).toBe(false);
  expect(params.has("cursor")).toBe(false);
  await userEvent.click(screen.getByRole("button", { name: "Открыть изменение source old.go" }));
  expect(await screen.findByText("analyzed · go")).toBeInTheDocument();
});

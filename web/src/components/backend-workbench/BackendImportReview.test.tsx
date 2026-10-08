import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendImportReview } from "./BackendImportReview";
import type { BackendImportPreflight } from "@/api/generated/schemas";

const projectId = "0197aaf9-5555-7000-8000-000000000001";
const revisionId = "0197aaf9-5555-7000-8000-000000000002";
const sessionId = "0197aaf9-5555-7000-8000-000000000003";
const committedId = "0197aaf9-5555-7000-8000-000000000004";
const newerId = "0197aaf9-5555-7000-8000-000000000005";
const hash = "a".repeat(64);
const session = {
  id: sessionId,
  projectId,
  baseRevisionId: revisionId,
  repositoryId: projectId,
  snapshotId: sessionId,
  mode: "reconcile",
  graphScope: { profile: "foundation-graph-v1", status: "partial", gaps: ["Jobs not analyzed"] },
  manifestHash: hash,
  manifest: {
    repositoryName: "repo",
    provider: {
      name: "agent",
      version: "1",
      namespace: "repo",
      method: "agent",
      profiles: ["foundation-graph-v1"],
      limitations: [],
    },
    snapshot: {
      dirty: false,
      consistency: "verified",
      capturedAt: "2026-09-30T10:00:00Z",
      files: [],
    },
  },
  inventory: [],
  state: "ready",
  version: 3,
  candidateHash: hash,
  acceptedBatchCount: 1,
  createdAt: "2026-09-30T10:00:00Z",
  updatedAt: "2026-09-30T10:00:00Z",
};
const preview = {
  preflight: undefined as BackendImportPreflight | undefined,
  sessionId,
  version: 3,
  state: "ready",
  candidateHash: hash,
  summary: { nodes: 1, edges: 0, evidence: 1, unresolved: 0 },
  diagnostics: [],
  comparisonSummary: null,
  sourceChangeCount: 0,
  identityDecisionCount: 0,
  deletionDecisionCount: 0,
};
function server(
  options: {
    lost?: boolean;
    replay?: boolean;
    conflict?: boolean;
    preflight?: BackendImportPreflight;
  } = {},
) {
  let status = {
    session: { ...session },
    preview: { ...preview, preflight: options.preflight },
    committedRevisionId: null as string | null,
    acceptedBatches: [],
    nextCursor: "",
  };
  let project = { id: projectId, version: 2, currentRevisionId: revisionId };
  let commits = 0;
  let failRead = false;
  const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    void init;
    const path = new URL(String(url), "http://localhost");
    if (path.pathname === `/api/backend-projects/${projectId}`) return json(200, project);
    if (path.pathname.endsWith("/revisions"))
      return json(200, {
        items: [{ id: revisionId, createdAt: session.createdAt }],
        nextCursor: "",
      });
    if (path.pathname.endsWith("/imports"))
      return json(200, { items: [status.session, { ...session, id: newerId }], nextCursor: "" });
    if (path.pathname.endsWith(`/imports/${sessionId}`))
      return failRead
        ? json(500, { error: { code: "backend_internal", message: "Status unavailable" } })
        : json(200, status);
    if (path.pathname.endsWith("/changes"))
      return json(200, {
        sessionId,
        previewVersion: status.preview.version,
        candidateHash: status.preview.candidateHash,
        recordType: "source",
        items: [],
        nextCursor: "",
      });
    if (path.pathname.endsWith("/preview")) {
      status = {
        ...status,
        session: { ...status.session, state: "ready", version: 5, candidateHash: hash },
        preview: { ...preview, version: 5 },
      };
      return json(200, status.preview);
    }
    if (path.pathname.endsWith("/commit")) {
      commits++;
      if (options.conflict)
        return json(409, { error: { code: "backend_version_conflict", message: "Head changed" } });
      if (commits === 1 && options.lost) {
        if (!options.replay) {
          status = {
            ...status,
            session: { ...status.session, state: "committed", version: 4 },
            committedRevisionId: committedId,
          };
          project = { ...project, version: 4, currentRevisionId: newerId };
        }
        throw new TypeError("response lost");
      }
      return json(200, { project, revision: { id: committedId }, sessionId });
    }
    return json(500, { error: { code: "unrouted", message: path.pathname } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return {
    fetchMock,
    abort() {
      status = {
        ...status,
        session: { ...status.session, state: "aborted", version: 4 },
        preview: null as unknown as typeof preview,
      };
    },
    recover() {
      status = {
        ...status,
        session: { ...status.session, state: "committed", version: 4 },
        committedRevisionId: committedId,
      };
    },
    oldStatus() {
      status = {
        session: { ...session },
        preview: { ...preview },
        committedRevisionId: null,
        acceptedBatches: [],
        nextCursor: "",
      };
    },
    failStatus() {
      failRead = true;
    },
    changeBatch() {
      status = {
        ...status,
        session: {
          ...status.session,
          state: "collecting",
          version: 4,
          candidateHash: null as unknown as string,
        },
        preview: null as unknown as typeof preview,
      };
    },
    changeHead() {
      project = { ...project, version: 3, currentRevisionId: newerId };
    },
    stalePreview() {
      status = { ...status, session: { ...status.session, version: 4 } };
    },
  };
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function open() {
  renderWithProviders(<BackendImportReview projectId={projectId} currentRevisionId={revisionId} />);
  await userEvent.click(await screen.findByRole("button", { name: `Открыть импорт ${sessionId}` }));
  await screen.findByRole("button", { name: "Commit" });
}
it("reads saved preview without writing and disables ready action after a new batch", async () => {
  const { fetchMock, changeBatch } = server();
  await open();
  expect(screen.getByRole("button", { name: "Commit" })).toBeEnabled();
  expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
  changeBatch();
  await userEvent.click(screen.getByRole("button", { name: "Обновить статус импорта" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled());
  expect(screen.getByText(/Требуется новый Preview/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Preview" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Commit" })).toBeEnabled());
  const writes = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
  expect(writes).toHaveLength(1);
  expect(JSON.parse(String(writes[0]?.[1]?.body))).toEqual({
    expectedImportVersion: 4,
    baseRevisionId: revisionId,
  });
});

it("shows unavailable Events separately from ready storage before commit", async () => {
  const { fetchMock } = server({
    preflight: {
      version: "import-preflight-v1",
      basis: "candidate",
      profile: "events-service-v1",
      counts: { nodes: 25228, edges: 100001, evidence: 106398 },
      semanticBytes: 140000000,
      storage: { status: "within_limits", reasons: [], limits: {} },
      consumers: [
        {
          surface: "events",
          status: "blocked",
          reasons: ["edge_limit"],
          remedy: "Keep source complete",
          admission: { maxTotalEdges: 100000 },
          traversal: {},
          response: {},
          concurrency: {},
        },
      ],
    },
  });
  await open();
  expect(await screen.findByText("События и задания: недоступно")).toBeInTheDocument();
  expect(screen.getByText(/100001.*100000/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Commit" })).toBeEnabled();
  expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
});
it("recovers the original committed revision after lost response and newer head", async () => {
  const { fetchMock } = server({ lost: true });
  await open();
  await userEvent.click(screen.getByRole("button", { name: "Commit" }));
  expect(await screen.findByText(`Импорт сохранён в ревизии ${committedId}`)).toBeInTheDocument();
  expect(screen.queryByText(`Импорт сохранён в ревизии ${newerId}`)).not.toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/commit"))).toHaveLength(1);
});
it("replays the entire original payload after uncertain commit", async () => {
  const { fetchMock, changeHead } = server({ lost: true, replay: true });
  await open();
  await userEvent.click(screen.getByRole("button", { name: "Commit" }));
  await screen.findByRole("button", { name: "Повторить исходный Commit" });
  changeHead();
  await userEvent.click(screen.getByRole("button", { name: "Повторить исходный Commit" }));
  await screen.findByText(`Импорт сохранён в ревизии ${committedId}`);
  const posts = fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/commit"));
  expect(posts).toHaveLength(2);
  expect(posts[1]?.[1]?.body).toBe(posts[0]?.[1]?.body);
  expect(JSON.parse(String(posts[0]?.[1]?.body))).toMatchObject({
    expectedVersion: 2,
    expectedImportVersion: 3,
    candidateHash: hash,
  });
});
it("does not enable a commit from stale preview cache for a new session version", async () => {
  const { stalePreview } = server();
  stalePreview();
  await open();
  expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled();
});
it("invalidates old ready preview after head change and requires explicit base selection", async () => {
  const { changeHead } = server();
  await open();
  changeHead();
  await userEvent.click(screen.getByRole("button", { name: "Обновить статус импорта" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled());
  expect(screen.getByRole("button", { name: "Preview" })).toBeDisabled();
});
it("rereads after conflict and disables the old commit until a new explicit preview", async () => {
  const { fetchMock } = server({ conflict: true });
  await open();
  await userEvent.click(screen.getByRole("button", { name: "Commit" }));
  await screen.findByText(/Head changed/);
  expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled();
  expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/commit"))).toHaveLength(1);
});

it("disables a cached ready commit when fresh status reread fails", async () => {
  const { failStatus } = server();
  await open();
  failStatus();
  await userEvent.click(screen.getByRole("button", { name: "Обновить статус импорта" }));
  await screen.findByText(/Status unavailable/);
  expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled();
});

it("holds session selection while commit is uncertain and releases it after receipt reread", async () => {
  const { recover } = server({ lost: true, replay: true });
  await open();
  await userEvent.click(screen.getByRole("button", { name: "Commit" }));
  await screen.findByRole("button", { name: "Повторить исходный Commit" });
  expect(screen.getByRole("button", { name: `Открыть импорт ${newerId}` })).toBeDisabled();
  recover();
  await userEvent.click(screen.getByRole("button", { name: "Обновить статус импорта" }));
  await screen.findByText(`Импорт сохранён в ревизии ${committedId}`);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: `Открыть импорт ${newerId}` })).toBeEnabled(),
  );
});
it("rejects an older successful status after observing a later batch version", async () => {
  const { changeBatch, oldStatus } = server();
  await open();
  changeBatch();
  await userEvent.click(screen.getByRole("button", { name: "Обновить статус импорта" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled());
  oldStatus();
  await userEvent.click(screen.getByRole("button", { name: "Обновить статус импорта" }));
  await screen.findByText(/ready · версия 3/);
  expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled();
});

it("invalidates the displayed commit when choosing another exact Preview base", async () => {
  const { fetchMock } = server();
  await open();
  const input = screen.getByRole("textbox", { name: "Базовая ревизия Preview" });
  await userEvent.clear(input);
  await userEvent.type(input, newerId);
  expect(screen.getByRole("button", { name: "Commit" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "Preview" }));
  const post = fetchMock.mock.calls.find(([url]) => String(url).endsWith("/preview"));
  expect(JSON.parse(String(post?.[1]?.body))).toEqual({
    expectedImportVersion: 3,
    baseRevisionId: newerId,
  });
});

it("releases the uncertain session lock when a fresh status confirms abort", async () => {
  const { abort } = server({ lost: true, replay: true });
  await open();
  await userEvent.click(screen.getByRole("button", { name: "Commit" }));
  await screen.findByRole("button", { name: "Повторить исходный Commit" });
  expect(screen.getByRole("button", { name: `Открыть импорт ${newerId}` })).toBeDisabled();
  abort();
  await userEvent.click(screen.getByRole("button", { name: "Обновить статус импорта" }));
  await screen.findByText(/aborted · версия 4/);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: `Открыть импорт ${newerId}` })).toBeEnabled(),
  );
  expect(
    screen.queryByRole("button", { name: "Повторить исходный Commit" }),
  ).not.toBeInTheDocument();
});

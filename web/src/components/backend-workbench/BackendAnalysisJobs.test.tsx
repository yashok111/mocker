import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor, render, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { routeTree } from "@/routeTree.gen";
import { makeQueryClient, renderWithProviders } from "@/test/render";
import { authResponseFixture } from "@/test/fixtures";
import { BackendAnalysisJobs } from "./BackendAnalysisJobs";
import { analysisTestDetail, analysisTestManifest } from "./backendAnalysisTestFixtures";
import { changeTestID, changeNextID, changeHash } from "./backendChangeTestFixtures";
import { analysisRecoveryKey } from "./backendAnalysisRecovery";
vi.mock("@antv/x6", () => ({ Graph: vi.fn() }));
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
it("mounts source analysis from the actual project route without opening proposal editor", async () => {
  const project = {
    id: changeTestID,
    name: "Project analysis",
    version: 1,
    repositories: [],
    capabilities: [],
    currentRevisionId: changeTestID,
    createdAt: "2026-10-04T00:00:00Z",
    updatedAt: "2026-10-04T00:00:00Z",
  };
  const revision = {
    id: changeTestID,
    projectId: changeTestID,
    schemaVersion: "5",
    semanticHash: changeHash,
    sourceSnapshotIds: [],
    artifactPins: [],
    coverage: { status: "partial", knownObjects: 0, denominator: null, gaps: [] },
    summary: "baseline",
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      const path = url.pathname;
      if (path === "/api/me") return json(authResponseFixture());
      if (path === `/api/backend-projects/${changeTestID}`) return json(project);
      if (path === `/api/backend-projects/${changeTestID}/revisions/${changeTestID}`)
        return json(revision);
      if (path.endsWith("/revisions")) {
        const limit = Number(url.searchParams.get("limit") ?? 100);
        if (!Number.isInteger(limit) || limit < 1 || limit > 100)
          return json(
            { error: { code: "backend_invalid", message: "limit must be between 1 and 100" } },
            400,
          );
        return json({
          items: [revision, { ...revision, id: changeNextID, summary: "older source" }],
          nextCursor: "",
        });
      }
      if (path.endsWith("/analyses")) return json({ items: [], nextCursor: "" });
      return json({ error: { code: "not_found", message: "not part of scenario" } }, 404);
    }),
  );
  const client = makeQueryClient(),
    router = createRouter({
      routeTree,
      context: { queryClient: client },
      history: createMemoryHistory({ initialEntries: [`/backend-projects/${changeTestID}`] }),
    });
  render(
    <QueryClientProvider client={client}>
      <MantineProvider>
        <ModalsProvider>
          <RouterProvider router={router as never} />
        </ModalsProvider>
      </MantineProvider>
    </QueryClientProvider>,
  );
  await userEvent.setup().click(await screen.findByRole("button", { name: "Задания анализа" }));
  expect(await screen.findByRole("button", { name: "Анализировать источник" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Добавить команду" })).toBeNull();
  const before = screen.getByLabelText("Источник до анализа");
  expect(await within(before).findByRole("option", { name: /older source/ })).toBeInTheDocument();
  await userEvent.setup().selectOptions(before, changeNextID);
  expect(before).toHaveValue(changeNextID);
});
it("real start, list, get, results, cancel and retry preserve selected immutable publication", async () => {
  let detail = analysisTestDetail();
  detail.job = { ...detail.job, status: "running", resultVersion: 1 };
  const calls: { path: string; body?: string; method: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = new URL(String(input), "http://localhost"),
        method = init?.method ?? "GET";
      calls.push({ path: u.pathname, body: init?.body as string | undefined, method });
      if (method === "POST") {
        const raw = sessionStorage.getItem(analysisRecoveryKey(changeTestID));
        expect(raw).not.toBeNull();
        expect(JSON.parse(raw!).body).toBe(init?.body);
        if (u.pathname.endsWith("/cancel")) {
          detail = { ...detail, job: { ...detail.job, status: "cancelled", resultVersion: 2 } };
          return json(detail.job);
        }
        if (u.pathname.endsWith("/retry"))
          return json({ ...detail.job, id: changeNextID, status: "queued" }, 202);
        return json(detail.job, 202);
      }
      if (u.pathname.endsWith("/revisions")) {
        const limit = Number(u.searchParams.get("limit") ?? 100);
        return limit >= 1 && limit <= 100
          ? json({ items: [], nextCursor: "" })
          : json(
              { error: { code: "backend_invalid", message: "limit must be between 1 and 100" } },
              400,
            );
      }
      if (u.pathname.endsWith("/results"))
        return json({
          manifest: {
            ...analysisTestManifest(),
            resultVersion: Number(u.searchParams.get("resultVersion")),
          },
          section: u.searchParams.get("section"),
          items: [],
          nextCursor: "",
        });
      if (u.pathname.endsWith("/analyses")) return json({ items: [detail.job], nextCursor: "" });
      return json({ ...detail, job: { ...detail.job, id: u.pathname.split("/").at(-1) } });
    }),
  );
  renderWithProviders(
    <BackendAnalysisJobs projectId={changeTestID} sourceRevisionId={changeTestID} />,
  );
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Анализировать источник" }));
  await screen.findByLabelText("Неизменяемая версия отчёта");
  await user.selectOptions(screen.getByLabelText("Неизменяемая версия отчёта"), "1");
  await screen.findByText("Отчёт · версия 1");
  await user.click(screen.getByRole("button", { name: "Отменить задание на сервере" }));
  await screen.findByText(/Задание: cancelled/);
  expect(screen.getByLabelText("Неизменяемая версия отчёта")).toHaveValue("1");
  await user.click(screen.getByRole("button", { name: "Новый анализ тех же данных" }));
  await waitFor(() => expect(screen.getByLabelText("Задание анализа")).toHaveValue(changeNextID));
  expect(calls.filter((x) => x.method === "POST").map((x) => x.path)).toEqual([
    `/api/backend-projects/${changeTestID}/analyses`,
    `/api/backend-projects/${changeTestID}/analyses/${changeTestID}/cancel`,
    `/api/backend-projects/${changeTestID}/analyses/${changeTestID}/retry`,
  ]);
});

it("shows revision history failures and lets the user retry loading older sources", async () => {
  let unavailable = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname.endsWith("/revisions")) {
        const limit = Number(url.searchParams.get("limit") ?? 100);
        if (limit > 100 || limit < 1)
          return json(
            { error: { code: "backend_invalid", message: "limit must be between 1 and 100" } },
            400,
          );
        if (unavailable)
          return json({ error: { code: "unavailable", message: "history unavailable" } }, 503);
        return json({ items: [{ id: changeNextID, summary: "older source" }], nextCursor: "" });
      }
      return json({ items: [], nextCursor: "" });
    }),
  );
  renderWithProviders(
    <BackendAnalysisJobs projectId={changeTestID} sourceRevisionId={changeTestID} />,
  );
  const retry = await screen.findByRole("button", {
    name: "Повторить загрузку истории исходных ревизий",
  });
  expect(screen.getByRole("alert")).toHaveTextContent("history unavailable");
  unavailable = false;
  await userEvent.setup().click(retry);
  expect(
    await within(screen.getByLabelText("Источник до анализа")).findByRole("option", {
      name: /older source/,
    }),
  ).toBeInTheDocument();
});

import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackendProjectsPage } from "./BackendProjectsPage";
import { BackendProjectPage } from "./BackendProjectPage";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";

// Canvas behavior has its own graph suite; keep these project workflows
// independent of X6's browser rendering and package entry point.
vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => <div>ER canvas</div> }));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const id = "0197aaf9-5555-7000-8000-000000000001";
const rid = "0197aaf9-5555-7000-8000-000000000002";
const project = {
  id,
  name: "Заказы",
  version: 1,
  currentRevisionId: rid,
  repositories: [],
  capabilities: [],
  createdAt: "2026-09-30T10:00:00Z",
  updatedAt: "2026-09-30T10:00:00Z",
};
const revision = {
  id: rid,
  projectId: id,
  parentRevisionId: null,
  schemaVersion: "1",
  semanticHash: "a".repeat(64),
  sourceSnapshotIds: [],
  artifactPins: [],
  coverage: {
    status: "partial",
    denominator: null,
    knownObjects: 0,
    gaps: ["Source inventory has not been imported; total object count is unknown."],
  },
  author: "system",
  summary: "Initial empty model; source inventory pending",
  createdAt: project.createdAt,
};

it("does not carry a rename draft into another project on a parameter change", async () => {
  const otherId = "0197aaf9-5555-7000-8000-000000000003";
  const fetchMock = route({
    [`GET /api/backend-projects/${id}`]: () => json(200, project),
    [`GET /api/backend-projects/${id}/revisions/${rid}`]: () => json(200, revision),
    [`GET /api/backend-projects/${otherId}`]: () =>
      json(200, { ...project, id: otherId, name: "Другой проект" }),
    [`GET /api/backend-projects/${otherId}/revisions/${rid}`]: () =>
      json(200, { ...revision, projectId: otherId }),
  });
  function Host() {
    const [selected, setSelected] = useState(id);
    return (
      <>
        <button onClick={() => setSelected(otherId)}>Сменить проект</button>
        <BackendProjectPage projectId={selected} />
      </>
    );
  }
  renderInRouter(<Host />);
  await userEvent.click(await screen.findByRole("button", { name: "Переименовать" }));
  const input = screen.getByRole("textbox", { name: "Название проекта" });
  await userEvent.clear(input);
  await userEvent.type(input, "Черновик первого проекта");
  await userEvent.click(screen.getByRole("button", { name: "Сменить проект" }));
  await screen.findByRole("heading", { name: "Другой проект" });
  expect(screen.queryByRole("textbox", { name: "Название проекта" })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Переименовать" }));
  expect(screen.getByRole("textbox", { name: "Название проекта" })).toHaveValue("Другой проект");
  expect(fetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
});

it("creates a backend project and retries a lost response with the original request", async () => {
  let attempts = 0;
  const fetchMock = route({
    "GET /api/backend-projects/capabilities": () => json(200, { limits: { maxNameLength: 200 } }),
    "GET /api/backend-projects": () => json(200, { items: [], nextCursor: "" }),
    "POST /api/backend-projects": () =>
      ++attempts === 1
        ? json(500, { error: { code: "backend_internal", message: "Ответ недоступен" } })
        : json(201, project),
  });
  renderInRouter(<BackendProjectsPage />);
  expect(await screen.findByText("Бэкенд-проектов пока нет")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Новый бэкенд-проект" }));
  const dialog = await screen.findByRole("dialog");
  await userEvent.type(within(dialog).getByRole("textbox", { name: "Название проекта" }), "Заказы");
  await userEvent.click(within(dialog).getByRole("button", { name: "Создать проект" }));
  expect(await within(dialog).findByText(/Ответ недоступен/)).toBeInTheDocument();
  expect(within(dialog).getByRole("textbox", { name: "Название проекта" })).toBeDisabled();
  await userEvent.click(within(dialog).getByRole("button", { name: "Повторить запрос" }));
  expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
  const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
  expect(posts).toHaveLength(2);
  const input = JSON.parse(String(posts[0]?.[1]?.body));
  expect(input.name).toBe("Заказы");
  expect(input.idempotencyKey).toMatch(/^[a-f\d-]{36}$/);
  expect(posts[1]?.[1]?.body).toBe(posts[0]?.[1]?.body);
  expect(fetchMock.mock.calls.some(([url]) => String(url).includes("workspaces"))).toBe(false);
});

it("shows list failures without implying there are no projects", async () => {
  route({
    "GET /api/backend-projects/capabilities": () => json(200, { limits: { maxNameLength: 200 } }),
    "GET /api/backend-projects": () =>
      json(500, { error: { code: "backend_internal", message: "Список недоступен" } }),
  });
  renderInRouter(<BackendProjectsPage />);
  expect(await screen.findByRole("alert")).toHaveTextContent("Список недоступен");
  expect(screen.queryByText("Бэкенд-проектов пока нет")).not.toBeInTheDocument();
});

it("shows unknown source coverage for the initial model", async () => {
  route({
    [`GET /api/backend-projects/${id}`]: () => json(200, project),
    [`GET /api/backend-projects/${id}/revisions/${rid}`]: () => json(200, revision),
    [`GET /api/backend-projects/${id}/revisions`]: () =>
      json(200, { items: [revision], nextCursor: "" }),
  });
  renderInRouter(<BackendProjectPage projectId={id} />);
  expect(await screen.findByRole("heading", { name: "Заказы" })).toBeInTheDocument();
  expect(await screen.findByText("Покрытие неизвестно")).toBeInTheDocument();
  expect(screen.getByText(/Исходный код ещё не импортирован/)).toBeInTheDocument();
  expect(screen.queryByText("100%")).not.toBeInTheDocument();
  expect(screen.getByText("a".repeat(64))).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "История ревизий" }));
  expect(await screen.findByRole("button", { name: `Открыть ревизию ${rid}` })).toBeInTheDocument();
});

it("preserves the rename draft on conflict and requires explicit reload and resubmit", async () => {
  let conflicted = false;
  let calls = 0;
  const fetchMock = route({
    [`GET /api/backend-projects/${id}`]: () =>
      json(200, conflicted ? { ...project, name: "Чужое название", version: 2 } : project),
    [`GET /api/backend-projects/${id}/revisions/${rid}`]: () => json(200, revision),
    [`POST /api/backend-projects/${id}/commands`]: () => {
      calls++;
      conflicted = true;
      return calls === 1
        ? json(409, {
            error: {
              code: "backend_version_conflict",
              message: "Проект изменился",
              currentVersion: 2,
              retryable: false,
            },
          })
        : json(200, { ...project, name: "Доставка", version: 3 });
    },
    "GET /api/backend-projects": () => json(200, { items: [], nextCursor: "" }),
  });
  renderInRouter(<BackendProjectPage projectId={id} />);
  await userEvent.click(await screen.findByRole("button", { name: "Переименовать" }));
  const input = screen.getByRole("textbox", { name: "Название проекта" });
  await userEvent.clear(input);
  await userEvent.type(input, "Доставка");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить название" }));
  await screen.findByText(/Проект изменился/);
  expect(input).toHaveValue("Доставка");
  expect(calls).toBe(1);
  await userEvent.click(screen.getByRole("button", { name: "Загрузить текущую версию" }));
  await screen.findByRole("heading", { name: "Чужое название" });
  expect(input).toHaveValue("Доставка");
  expect(calls).toBe(1);
  await userEvent.click(screen.getByRole("button", { name: "Сохранить название" }));
  await waitFor(() => expect(calls).toBe(2));
  const posts = fetchMock.mock.calls
    .filter(([, init]) => init?.method === "POST")
    .map(([, init]) => JSON.parse(String(init?.body)));
  expect(posts[0].expectedVersion).toBe(1);
  expect(posts[1].expectedVersion).toBe(2);
  expect(posts[1].idempotencyKey).not.toBe(posts[0].idempotencyKey);
  expect(posts[1].commands).toEqual([{ type: "rename_project", name: "Доставка" }]);
});

import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackendProjectsPage } from "./BackendProjectsPage";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { project } from "./explorer/testFixtures";
afterEach(() => vi.unstubAllGlobals());
it("opens an existing project without exposing creation or rename forms", async () => {
  const fetcher = route({
    "GET /api/backend-projects": () => json(200, { items: [project], nextCursor: "" }),
  });
  renderInRouter(<BackendProjectsPage />);
  const item = await screen.findByRole("button", { name: /Orders/ });
  expect(
    screen.queryByRole("button", { name: /Создать|Новый|Импортировать|Переименовать/ }),
  ).not.toBeInTheDocument();
  await userEvent.click(item);
  expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
  expect(fetcher.mock.calls.every(([, init]) => init?.method === "GET")).toBe(true);
});
it("shows list failures without implying there are no projects", async () => {
  route({
    "GET /api/backend-projects": () =>
      json(503, { error: { code: "internal", message: "Unavailable" } }),
  });
  renderInRouter(<BackendProjectsPage />);
  expect(await screen.findByRole("alert")).toBeVisible();
  expect(screen.queryByText("Бэкенд-проектов пока нет")).not.toBeInTheDocument();
});
it("explains empty projects without adding a setup form", async () => {
  route({ "GET /api/backend-projects": () => json(200, { items: [], nextCursor: "" }) });
  renderInRouter(<BackendProjectsPage />);
  expect(await screen.findByText("Бэкенд-проектов пока нет")).toBeVisible();
  expect(screen.getByText(/Попросите агента создать проект/)).toBeVisible();
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});
it("keeps server pagination reachable", async () => {
  route({
    "GET /api/backend-projects": () => json(200, { items: [project], nextCursor: "next" }),
    "GET /api/backend-projects?cursor=next": () =>
      json(200, { items: [{ ...project, name: "Второй проект" }], nextCursor: "" }),
  });
  renderInRouter(<BackendProjectsPage />);
  await userEvent.click(await screen.findByRole("button", { name: "Следующая страница" }));
  expect(await screen.findByText("Второй проект")).toBeVisible();
  await userEvent.click(screen.getByRole("button", { name: "Предыдущая страница" }));
  expect(await screen.findByText("Orders")).toBeVisible();
});

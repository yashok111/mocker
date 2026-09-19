import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DesignsPage } from "./DesignsPage";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("DesignsPage", () => {
  it("lists projects and creates a blank project through the HTTP contract", async () => {
    let created = false;
    const fetchMock = route({
      "GET /api/designs": () =>
        json(200, {
          designs: created
            ? [
                {
                  id: 12,
                  name: "Заказы API",
                  version: 1,
                  draftWorkspaceId: 31,
                  publishedWorkspaceId: 32,
                  draftUrl: "http://orders-draft.mock.local",
                  publishedUrl: "http://orders.mock.local",
                  draftRevisionId: 41,
                  publishedRevisionId: null,
                  latestReviewId: null,
                  createdAt: 1_795_000_000,
                  updatedAt: 1_795_000_000,
                },
              ]
            : [],
        }),
      "GET /api/workspaces?all=1": () => json(200, []),
      "POST /api/designs": () => {
        created = true;
        return json(201, detailFixture());
      },
    });

    renderInRouter(<DesignsPage />);

    expect(await screen.findByText("Проектов пока нет")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Новый проект" }));
    const dialog = await screen.findByRole("dialog", { name: "Новый проект API" });
    await userEvent.type(
      within(dialog).getByRole("textbox", { name: /Название проекта/ }),
      "Заказы API",
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Создать проект" }));

    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
    const createCall = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/designs" && init?.method === "POST",
    );
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({ name: "Заказы API" });
  });

  it("imports a project from a document without dropping the authored text", async () => {
    const authored = JSON.stringify({
      openapi: "3.1.0",
      info: { title: "Catalog", version: "1", "x-owner": "analysts" },
      paths: {},
    });
    const fetchMock = route({
      "GET /api/designs": () => json(200, { designs: [] }),
      "GET /api/workspaces?all=1": () => json(200, []),
      "POST /api/designs": () => json(201, detailFixture()),
    });
    renderInRouter(<DesignsPage />);

    await userEvent.click(await screen.findByRole("button", { name: "Новый проект" }));
    const dialog = await screen.findByRole("dialog", { name: "Новый проект API" });
    await userEvent.type(
      within(dialog).getByRole("textbox", { name: /Название проекта/ }),
      "Каталог API",
    );
    await userEvent.click(within(dialog).getByRole("tab", { name: "Из документа" }));
    const documentInput = within(dialog).getByRole("textbox", { name: "OpenAPI JSON или YAML" });
    documentInput.focus();
    await userEvent.paste(authored);
    await userEvent.click(within(dialog).getByRole("button", { name: "Создать проект" }));

    const createCall = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/designs" && init?.method === "POST",
    );
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      name: "Каталог API",
      document: authored,
    });
  });

  it("seeds an isolated project from an existing workspace", async () => {
    const fetchMock = route({
      "GET /api/designs": () => json(200, { designs: [] }),
      "GET /api/workspaces?all=1": () =>
        json(200, [
          {
            id: 7,
            name: "Продажи",
            slug: "sales",
            baseURL: "http://sales.mock.local",
            enabled: true,
            editVersion: 1,
            specId: null,
            createdAt: 1,
            updatedAt: 1,
          },
        ]),
      "POST /api/designs": () => json(201, detailFixture()),
    });
    renderInRouter(<DesignsPage />);

    await userEvent.click(await screen.findByRole("button", { name: "Новый проект" }));
    const dialog = await screen.findByRole("dialog", { name: "Новый проект API" });
    await userEvent.type(
      within(dialog).getByRole("textbox", { name: /Название проекта/ }),
      "Продажи API",
    );
    await userEvent.click(within(dialog).getByRole("tab", { name: "Из воркспейса" }));
    await userEvent.selectOptions(
      within(dialog).getByRole("combobox", { name: /Исходный воркспейс/ }),
      "7",
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Создать проект" }));

    const createCall = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/designs" && init?.method === "POST",
    );
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      name: "Продажи API",
      workspaceId: 7,
    });
  });
});

function detailFixture() {
  const document = JSON.stringify(
    { openapi: "3.1.0", info: { title: "Заказы API", version: "0.1.0" }, paths: {} },
    null,
    2,
  );
  return {
    design: {
      id: 12,
      name: "Заказы API",
      version: 1,
      draftWorkspaceId: 31,
      publishedWorkspaceId: 32,
      draftUrl: "http://orders-draft.mock.local",
      publishedUrl: "http://orders.mock.local",
      draftRevisionId: 41,
      publishedRevisionId: null,
      latestReviewId: null,
      createdAt: 1_795_000_000,
      updatedAt: 1_795_000_000,
    },
    draft: {
      id: 41,
      designId: 12,
      version: 1,
      hash: "sha256:first",
      source: "ui",
      summary: "Проект создан",
      changeSetId: null,
      createdAt: 1_795_000_000,
      document,
    },
    published: null,
    revisions: [],
    changeSets: [],
    reviews: [],
    releases: [],
  };
}

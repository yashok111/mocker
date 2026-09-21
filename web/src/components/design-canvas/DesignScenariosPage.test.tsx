import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { emptyCanvas } from "./canvasModel";
import { DesignScenariosPage } from "./DesignScenariosPage";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("DesignScenariosPage", () => {
  it("lists server scenarios and opens the selected editor", async () => {
    route({
      "GET /api/design-scenarios": () =>
        json(200, {
          scenarios: [
            {
              id: 12,
              name: "Оформление заказа",
              version: 4,
              draftRevisionId: 18,
              createdAt: 1_700_000_000,
              updatedAt: 1_700_000_100,
            },
          ],
        }),
    });
    renderInRouter(<DesignScenariosPage />);

    expect(await screen.findByText("Оформление заказа")).toBeInTheDocument();
    expect(screen.getByText("черновик · v4")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /Оформление заказа/ }));
    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
  });

  it("creates a blank scenario and opens its editor", async () => {
    const fetchMock = route({
      "GET /api/design-scenarios": () => json(200, { scenarios: [] }),
      "POST /api/design-scenarios": () =>
        json(201, {
          scenario: {
            id: 24,
            name: "Оплата заказа",
            version: 1,
            draftRevisionId: 31,
            createdAt: 1_700_000_000,
            updatedAt: 1_700_000_000,
          },
          draft: {
            id: 31,
            scenarioId: 24,
            version: 1,
            hash: "hash",
            source: "ui",
            summary: "",
            createdAt: 1_700_000_000,
            document: { ...emptyCanvas(), title: "Оплата заказа" },
            formDrafts: {},
          },
          revisions: [],
          diagnostics: [],
          contractUpdates: [],
        }),
    });
    renderInRouter(<DesignScenariosPage />);

    await userEvent.click(await screen.findByRole("button", { name: "Новый сценарий" }));
    await userEvent.type(
      screen.getByRole("textbox", { name: "Название сценария" }),
      "Оплата заказа",
    );
    await userEvent.click(screen.getByRole("button", { name: "Создать сценарий" }));

    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
    const write = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === "/api/design-scenarios" && init?.method === "POST",
    );
    expect(JSON.parse(String(write?.[1]?.body))).toMatchObject({
      document: { formatVersion: 1, title: "Оплата заказа" },
      formDrafts: {},
    });
  });
});

import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { json, route } from "@/test/http";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import type { SchemaModel } from "@/api/generated/schemas";
import SchemaModelEditor from "./SchemaModelEditor";
import { DRAFT_KEY } from "./form";
vi.mock("./SchemaGraph", () => ({ default: () => <div data-testid="schema-graph" /> }));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const document =
  '{"openapi":"3.1.0","info":{"title":"Local","version":"1"},"paths":{},"x-local":true}';
const model: SchemaModel = {
  schemas: [
    {
      name: "Order",
      pointer: "/components/schemas/Order",
      schemaJSON: '{"type":"object","properties":{"id":{"type":"integer"}}}',
      type: "object",
      description: "",
      properties: [
        {
          name: "id",
          pointer: "/components/schemas/Order/properties/id",
          schemaJSON: '{"type":"integer","x-keep":true}',
          type: "integer",
          required: false,
        },
      ],
      x: 40,
      y: 40,
    },
    {
      name: "User",
      pointer: "/components/schemas/User",
      schemaJSON: '{"type":"object"}',
      type: "object",
      description: "",
      properties: [],
      x: 440,
      y: 40,
    },
  ],
  references: [
    {
      sourceSchema: "Order",
      sourceProperty: "user",
      targetSchema: "User",
      pointer: "/components/schemas/Order/properties/user/$ref",
      ref: "#/components/schemas/User",
    },
  ],
  operations: [
    { method: "get", path: "/orders", pointer: "/paths/~1orders/get", schemas: ["Order", "User"] },
  ],
};
function response(nextDocument = document, diagnostics: unknown[] = []) {
  return json(200, { document: nextDocument, model, diagnostics, valid: diagnostics.length === 0 });
}
function Harness({
  store = createFormDraftStore(),
}: {
  store?: ReturnType<typeof createFormDraftStore>;
}) {
  const [buffer, setBuffer] = useState(document);
  return (
    <>
      <SchemaModelEditor
        designId={12}
        document={buffer}
        blocked={false}
        formStore={store}
        onChange={setBuffer}
      />
      <output data-testid="buffer">{buffer}</output>
      <button onClick={() => setBuffer(document + " ")}>Внешнее изменение</button>
    </>
  );
}
describe("schema model editor", () => {
  it("previews unsaved text and submits constraints plus unknown keywords without persisting", async () => {
    const fetchMock = route({
      "POST /api/designs/12/schema-model/preview": () => response(document + " "),
    });
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: "Поле Order.id" }));
    await userEvent.click(screen.getByText("Ограничения и примеры"));
    await fill(screen.getByLabelText("Минимум"), "1");
    await fill(screen.getByLabelText("Пример (JSON)"), "42");
    await userEvent.click(screen.getByLabelText("Обязательное поле"));
    expect(store.getSnapshot().dirty).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() => expect(store.getSnapshot().dirty).toBe(false));
    const bodies = fetchMock.mock.calls.map(([, options]) => JSON.parse(String(options?.body)));
    expect(bodies[0]).toEqual({ document });
    const submitted = bodies.find((body) => body.commands);
    expect(submitted.document).toBe(document);
    expect(submitted.commands[0]).toMatchObject({
      kind: "upsert_property",
      schemaName: "Order",
      propertyName: "id",
      required: true,
    });
    expect(JSON.parse(submitted.commands[0].schemaJSON)).toEqual({
      type: "integer",
      "x-keep": true,
      minimum: 1,
      example: 42,
    });
    expect(screen.getByTestId("buffer").textContent).toBe(document + " ");
  });
  it("keeps invalid input across remounts and refuses selection changes until discarded", async () => {
    route({ "POST /api/designs/12/schema-model/preview": () => response() });
    const store = createFormDraftStore();
    const view = renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: "Поле Order.id" }));
    await userEvent.click(screen.getByText("Расширенный JSON схемы"));
    await userEvent.clear(screen.getByLabelText("JSON схемы"));
    await fill(screen.getByLabelText("JSON схемы"), "{");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    expect(store.getSnapshot().invalid).toBe(true);
    view.unmount();
    renderWithProviders(<Harness store={store} />);
    await screen.findByRole("button", { name: "Поле Order.id" });
    expect(screen.getByLabelText("JSON схемы")).toHaveValue("{");
    await userEvent.click(screen.getByRole("button", { name: "User" }));
    expect(screen.getByText(/Примените изменения или сбросьте/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Сбросить ввод" }));
    expect(store.getSnapshot().dirty).toBe(false);
  });
  it("ignores a command reply after newer input and retains that input", async () => {
    let finish: ((response: Response) => void) | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((_url: RequestInfo | URL, options?: RequestInit) => {
        const body = JSON.parse(String(options?.body));
        return body.commands
          ? new Promise<Response>((resolve) => {
              finish = resolve;
            })
          : Promise.resolve(response());
      }),
    );
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: "User" }));
    await fill(screen.getByLabelText("Имя схемы"), "Customer");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() => expect(finish).toBeDefined());
    await fill(screen.getByLabelText("Имя схемы"), "Newest");
    finish!(response("stale"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Применить" })).toBeEnabled());
    expect(screen.getByTestId("buffer").textContent).toBe(document);
    expect(store.get(DRAFT_KEY)?.source).toContain("Newest");
  });
  it("reports actionable deletion errors and lists transitive API consumers", async () => {
    const fetchMock = route({
      "POST /api/designs/12/schema-model/preview": () => {
        const body = JSON.parse(String(fetchMock.mock.calls.at(-1)?.[1]?.body));
        return body.commands
          ? json(400, {
              error: {
                code: "design_invalid",
                message: "invalid API document",
                details: {
                  diagnostics: [
                    {
                      pointer: "/components/schemas/Order/properties/user/$ref",
                      message: "/commands/0: Удаление нарушит ссылку; сначала измените потребителя",
                      severity: "error",
                    },
                  ],
                },
              },
            })
          : response();
      },
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await screen.findByRole("button", { name: "User" }));
    expect(screen.getByText("GET /orders")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Удалить схему" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "/components/schemas/Order/properties/user/$ref",
    );
    expect(screen.getByRole("alert")).toHaveTextContent("сначала измените потребителя");
    expect(screen.getByTestId("buffer").textContent).toBe(document);
  });
  it("shows server diagnostics from the initial preview and retains a generic fallback", async () => {
    let diagnostics: unknown = [
      { pointer: "/components/schemas", message: "Превышен предел числа схем", severity: "error" },
    ];
    route({
      "POST /api/designs/12/schema-model/preview": () =>
        json(400, {
          error: {
            code: "design_invalid",
            message: "invalid API document",
            details: { diagnostics },
          },
        }),
    });
    renderWithProviders(<Harness />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "/components/schemas: Превышен предел числа схем",
    );
    diagnostics = [{ pointer: 42, message: null }];
    await userEvent.click(screen.getByRole("button", { name: "Повторить" }));
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("invalid API document"),
    );
  });
  it("keeps form input after a preview command fails with diagnostic details", async () => {
    const fetchMock = route({
      "POST /api/designs/12/schema-model/preview": () => {
        const body = JSON.parse(String(fetchMock.mock.calls.at(-1)?.[1]?.body));
        return body.commands
          ? json(400, {
              error: {
                code: "design_invalid",
                message: "invalid API document",
                details: {
                  diagnostics: [
                    {
                      pointer: "/commands/0",
                      message: "Исправьте поле перед применением",
                      severity: "error",
                    },
                  ],
                },
              },
            })
          : response();
      },
    });
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: "Поле Order.id" }));
    await userEvent.click(screen.getByLabelText("Обязательное поле"));
    const pending = store.get(DRAFT_KEY)?.source;
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    expect(
      await screen.findByText("/commands/0: Исправьте поле перед применением"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Обязательное поле")).toBeChecked();
    expect(store.get(DRAFT_KEY)?.source).toBe(pending);
    expect(store.getSnapshot().dirty).toBe(true);
    expect(screen.getByTestId("buffer").textContent).toBe(document);
  });
  it("requires a fresh draft when the source document changed independently", async () => {
    route({ "POST /api/designs/12/schema-model/preview": () => response() });
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: "User" }));
    await fill(screen.getByLabelText("Имя схемы"), "Customer");
    await userEvent.click(screen.getByRole("button", { name: "Внешнее изменение" }));
    await userEvent.click(await screen.findByRole("button", { name: "Применить" }));
    expect(screen.getByText(/Документ изменился/)).toBeInTheDocument();
    expect(store.getSnapshot().dirty).toBe(true);
  });
});

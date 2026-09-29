import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { json, route } from "@/test/http";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import ResourceMapEditor from "./ResourceMapEditor";

vi.mock("./ResourceGraph", () => ({ default: () => <div data-testid="resource-graph" /> }));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const document =
  '{"openapi":"3.1.0","info":{"title":"Local","version":"1"},"paths":{"/orders":{"get":{"summary":"List","x-mocker-canvas-operation-id":"op-1"}},"/payments":{"post":{"summary":"Pay","x-mocker-canvas-operation-id":"op-2"}}},"x-local":true}';
const resources = [
  {
    id: "orders",
    name: "/orders",
    service: "",
    description: "",
    operationKeys: ["op-1"],
    x: 40,
    y: 40,
    inferred: true,
  },
  {
    id: "payments",
    name: "/payments",
    service: "",
    description: "",
    operationKeys: ["op-2"],
    x: 340,
    y: 40,
    inferred: true,
  },
];
const operations = [
  { key: "op-1", method: "get", path: "/orders", summary: "List", schemas: ["Order"] },
  { key: "op-2", method: "post", path: "/payments", summary: "Pay", schemas: [] },
];
function preview(nextDocument = document, overrides: Record<string, unknown> = {}) {
  return json(200, {
    document: nextDocument,
    model: { resources, operations, relations: [], diagnostics: [], ...overrides },
    valid: true,
    diagnostics: [],
  });
}
function Harness({
  store = createFormDraftStore(),
  onScenario = () => {},
  onSchema = () => {},
  onAddOperation = () => {},
}: {
  store?: ReturnType<typeof createFormDraftStore>;
  onScenario?: (id: number) => void;
  onSchema?: (name: string) => void;
  onAddOperation?: () => void;
}) {
  const [buffer, setBuffer] = useState(document);
  return (
    <>
      <ResourceMapEditor
        designId={12}
        document={buffer}
        blocked={false}
        formStore={store}
        onChange={setBuffer}
        onOperation={() => {}}
        onSchema={onSchema}
        onScenario={onScenario}
        onAddOperation={onAddOperation}
      />
      <output data-testid="buffer">{buffer}</output>
      <button onClick={() => setBuffer(document + " ")}>Внешнее изменение</button>
    </>
  );
}
describe("resource map editor", () => {
  it("offers operation creation for an empty contract", async () => {
    route({
      "POST /api/designs/12/resource-map/preview": () =>
        preview(document, { resources: [], operations: [] }),
      "GET /api/designs/12/resource-map": () =>
        json(200, {
          designId: 12,
          version: 1,
          revisionId: 41,
          model: { resources: [], operations: [], relations: [], diagnostics: [] },
          scenarioUsages: [],
          usagesTruncated: false,
        }),
    });
    const onAddOperation = vi.fn();
    renderWithProviders(<Harness onAddOperation={onAddOperation} />);
    await userEvent.click(await screen.findByRole("button", { name: "Добавить операцию" }));
    expect(onAddOperation).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Добавить ресурс" })).toBeInTheDocument();
  });
  it("shows inferred resources and uses the unsaved document for preview", async () => {
    const fetchMock = route({
      "POST /api/designs/12/resource-map/preview": () => preview(),
      "GET /api/designs/12/resource-map": () =>
        json(200, {
          designId: 12,
          version: 1,
          revisionId: 41,
          model: { resources, operations, relations: [], diagnostics: [] },
          scenarioUsages: [],
          usagesTruncated: false,
        }),
    });
    renderWithProviders(<Harness />);
    expect(await screen.findByRole("button", { name: /\/orders/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /\/payments/ })).toBeInTheDocument();
    const request = fetchMock.mock.calls.find(
      ([url, init]) => String(url).endsWith("/preview") && init?.method === "POST",
    );
    expect(JSON.parse(String(request?.[1]?.body)).document).toBe(document);
  });
  it("edits a resource, moves an operation, and adds a labeled relation using preview commands", async () => {
    const fetchMock = route({
      "POST /api/designs/12/resource-map/preview": () => preview(document + " "),
      "GET /api/designs/12/resource-map": () =>
        json(200, {
          designId: 12,
          version: 1,
          revisionId: 41,
          model: { resources, operations, relations: [], diagnostics: [] },
          scenarioUsages: [],
          usagesTruncated: false,
        }),
    });
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: /\/orders/ }));
    await userEvent.clear(screen.getByLabelText("Название ресурса"));
    await fill(screen.getByLabelText("Название ресурса"), "Заказы");
    await fill(screen.getByLabelText("Сервис"), "billing");
    expect(store.getSnapshot().dirty).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() => expect(store.getSnapshot().dirty).toBe(false));
    expect(
      fetchMock.mock.calls
        .map(([, init]) => JSON.parse(String(init?.body || "{}")))
        .find((body) => body.commands)?.commands,
    ).toEqual([
      {
        kind: "upsert_resource",
        resource: expect.objectContaining({ id: "orders", name: "Заказы", service: "billing" }),
      },
    ]);
    await userEvent.click(screen.getByRole("button", { name: /\/payments/ }));
    await userEvent.selectOptions(screen.getByLabelText("Ресурс для POST /payments"), "orders");
    await waitFor(() =>
      expect(
        fetchMock.mock.calls
          .map(([, init]) => JSON.parse(String(init?.body || "{}")))
          .some((body) => body.commands?.[0]?.kind === "assign_operation"),
      ).toBe(true),
    );
    await userEvent.click(screen.getByRole("button", { name: "Добавить связь" }));
    await userEvent.selectOptions(screen.getByLabelText("Связанный ресурс"), "payments");
    await fill(screen.getByLabelText("Подпись связи"), "платит");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls
          .map(([, init]) => JSON.parse(String(init?.body || "{}")))
          .some((body) => body.commands?.[0]?.kind === "upsert_relation"),
      ).toBe(true),
    );
  });
  it("preserves pending input across remount and blocks unrelated actions", async () => {
    route({
      "POST /api/designs/12/resource-map/preview": () => preview(),
      "GET /api/designs/12/resource-map": () =>
        json(200, {
          designId: 12,
          version: 1,
          revisionId: 41,
          model: { resources, operations, relations: [], diagnostics: [] },
          scenarioUsages: [],
          usagesTruncated: false,
        }),
    });
    const store = createFormDraftStore();
    const view = renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: /\/orders/ }));
    await userEvent.clear(screen.getByLabelText("Название ресурса"));
    await fill(screen.getByLabelText("Название ресурса"), "Pending");
    view.unmount();
    renderWithProviders(<Harness store={store} />);
    expect(await screen.findByLabelText("Название ресурса")).toHaveValue("Pending");
    await userEvent.click(screen.getByRole("button", { name: /\/payments/ }));
    expect(
      screen.getByText(/Примените изменения или сбросьте ввод перед выбором/),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Сбросить ввод" }));
    expect(store.getSnapshot().dirty).toBe(false);
  });
  it("reveals resource and relation actions immediately after creation", async () => {
    let created: (typeof resources)[number] | undefined;
    let relation:
      | { id: string; fromResourceId: string; toResourceId: string; label: string }
      | undefined;
    const fetchMock = route({
      "POST /api/designs/12/resource-map/preview": () => {
        const body = JSON.parse(String(fetchMock.mock.calls.at(-1)?.[1]?.body));
        if (body.commands?.[0]?.kind === "upsert_resource")
          created = { ...body.commands[0].resource, inferred: false };
        if (body.commands?.[0]?.kind === "upsert_relation") relation = body.commands[0].relation;
        return preview(document + (created ? " " : ""), {
          resources: created ? [...resources, created] : resources,
          relations: relation ? [relation] : [],
        });
      },
      "GET /api/designs/12/resource-map": () =>
        json(200, {
          designId: 12,
          version: 1,
          revisionId: 41,
          model: { resources, operations, relations: [], diagnostics: [] },
          scenarioUsages: [],
          usagesTruncated: false,
        }),
    });
    renderWithProviders(<Harness />);
    await userEvent.click(await screen.findByRole("button", { name: "Добавить ресурс" }));
    await fill(screen.getByLabelText("Название ресурса"), "Возвраты");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    expect(
      await screen.findByRole("button", { name: "Вернуть автогруппировку" }),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Добавить связь" }));
    await userEvent.selectOptions(screen.getByLabelText("Связанный ресурс"), "orders");
    await fill(screen.getByLabelText("Подпись связи"), "создаёт");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    expect(await screen.findByRole("button", { name: "Удалить связь" })).toBeInTheDocument();
  });
  it("opens referenced schemas and saved scenario usages with pinned revisions", async () => {
    route({
      "POST /api/designs/12/resource-map/preview": () => preview(),
      "GET /api/designs/12/resource-map": () =>
        json(200, {
          designId: 12,
          version: 1,
          revisionId: 41,
          model: { resources, operations, relations: [], diagnostics: [] },
          scenarioUsages: [
            {
              scenarioId: 7,
              scenarioName: "Возврат",
              revisionId: 13,
              messageId: "m-1",
              operationKey: "op-1",
              contractRevisionId: 41,
              mode: "copy",
            },
          ],
          usagesTruncated: true,
        }),
    });
    const onSchema = vi.fn();
    const onScenario = vi.fn();
    renderWithProviders(<Harness onSchema={onSchema} onScenario={onScenario} />);
    await userEvent.click(await screen.findByRole("button", { name: /\/orders/ }));
    await userEvent.click(screen.getByRole("button", { name: "Схема Order" }));
    expect(onSchema).toHaveBeenCalledWith("Order");
    const usage = await screen.findByRole("button", {
      name: /Возврат · копия · ревизия API 41 · ревизия сценария 13/,
    });
    await userEvent.click(usage);
    expect(onScenario).toHaveBeenCalledWith(7);
    expect(screen.getByText(/Список сценариев сокращён/)).toBeInTheDocument();
  });
  it("keeps invalid form values after a rejected command and allows retry", async () => {
    const fetchMock = route({
      "POST /api/designs/12/resource-map/preview": () => {
        const body = JSON.parse(String(fetchMock.mock.calls.at(-1)?.[1]?.body));
        return body.commands
          ? json(400, { error: { code: "bad_request", message: "Сервис недопустим" } })
          : preview();
      },
      "GET /api/designs/12/resource-map": () =>
        json(200, {
          designId: 12,
          version: 1,
          revisionId: 41,
          model: { resources, operations, relations: [], diagnostics: [] },
          scenarioUsages: [],
          usagesTruncated: false,
        }),
    });
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: /\/orders/ }));
    await fill(screen.getByLabelText("Сервис"), "bad service");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    expect(await screen.findByText(/Сервис недопустим/)).toBeInTheDocument();
    expect(screen.getByLabelText("Сервис")).toHaveValue("bad service");
    expect(store.getSnapshot().invalid).toBe(true);
    expect(screen.getByTestId("buffer").textContent).toBe(document);
  });
  it("ignores an old initial preview after the document changes", async () => {
    let finish: ((response: Response) => void) | undefined;
    let previews = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        if (String(input).endsWith("/resource-map"))
          return Promise.resolve(
            json(200, {
              designId: 12,
              version: 1,
              revisionId: 41,
              model: { resources, operations, relations: [], diagnostics: [] },
              scenarioUsages: [],
              usagesTruncated: false,
            }),
          );
        previews++;
        return previews === 1
          ? new Promise<Response>((resolve) => {
              finish = resolve;
            })
          : Promise.resolve(preview(document + " ", { resources: [resources[1]] }));
      }),
    );
    renderWithProviders(<Harness />);
    await waitFor(() => expect(finish).toBeDefined());
    await userEvent.click(screen.getByRole("button", { name: "Внешнее изменение" }));
    expect(await screen.findByRole("button", { name: /\/payments/ })).toBeInTheDocument();
    finish!(preview());
    expect(screen.queryByRole("button", { name: /\/orders/ })).not.toBeInTheDocument();
  });
  it("discards an in-flight command reply after reset", async () => {
    let finish: ((response: Response) => void) | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).endsWith("/resource-map"))
          return Promise.resolve(
            json(200, {
              designId: 12,
              version: 1,
              revisionId: 41,
              model: { resources, operations, relations: [], diagnostics: [] },
              scenarioUsages: [],
              usagesTruncated: false,
            }),
          );
        const body = JSON.parse(String(init?.body));
        return body.commands
          ? new Promise<Response>((resolve) => {
              finish = resolve;
            })
          : Promise.resolve(preview());
      }),
    );
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: /Ресурс \/orders/ }));
    await userEvent.clear(screen.getByLabelText("Название ресурса"));
    await fill(screen.getByLabelText("Название ресурса"), "Discard me");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() => expect(finish).toBeDefined());
    await userEvent.click(screen.getByRole("button", { name: "Сбросить ввод" }));
    finish!(preview("stale"));
    await waitFor(() => expect(store.getSnapshot().dirty).toBe(false));
    expect(screen.getByTestId("buffer").textContent).toBe(document);
    expect(screen.getByLabelText("Название ресурса")).toHaveValue("/orders");
  });
  it("keeps the newer command busy when an older discarded command settles", async () => {
    const finish: Array<(response: Response) => void> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).endsWith("/resource-map"))
          return Promise.resolve(
            json(200, {
              designId: 12,
              version: 1,
              revisionId: 41,
              model: { resources, operations, relations: [], diagnostics: [] },
              scenarioUsages: [],
              usagesTruncated: false,
            }),
          );
        const body = JSON.parse(String(init?.body));
        return body.commands
          ? new Promise<Response>((resolve) => {
              finish.push(resolve);
            })
          : Promise.resolve(preview());
      }),
    );
    renderWithProviders(<Harness />);
    await userEvent.click(await screen.findByRole("button", { name: /Ресурс \/orders/ }));
    await userEvent.clear(screen.getByLabelText("Название ресурса"));
    await fill(screen.getByLabelText("Название ресурса"), "First");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() => expect(finish).toHaveLength(1));
    await userEvent.click(screen.getByRole("button", { name: "Сбросить ввод" }));
    await userEvent.clear(screen.getByLabelText("Название ресурса"));
    await fill(screen.getByLabelText("Название ресурса"), "Second");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() => expect(finish).toHaveLength(2));
    await act(async () => finish[0]!(preview("stale")));
    expect(screen.getByRole("button", { name: "Применить" })).toBeDisabled();
    await act(async () => finish[1]!(preview(document + " ")));
    await waitFor(() => expect(screen.getByTestId("buffer").textContent).toBe(document + " "));
  });
  it("keeps form values on preview failure and ignores a stale command reply", async () => {
    let finish: ((response: Response) => void) | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith("/resource-map"))
          return Promise.resolve(
            json(200, {
              designId: 12,
              version: 1,
              revisionId: 41,
              model: { resources, operations, relations: [], diagnostics: [] },
              scenarioUsages: [],
              usagesTruncated: false,
            }),
          );
        const body = JSON.parse(String(init?.body));
        return body.commands
          ? new Promise<Response>((resolve) => {
              finish = resolve;
            })
          : Promise.resolve(preview());
      }),
    );
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: /\/orders/ }));
    await userEvent.clear(screen.getByLabelText("Название ресурса"));
    await fill(screen.getByLabelText("Название ресурса"), "First");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() => expect(finish).toBeDefined());
    await userEvent.clear(screen.getByLabelText("Название ресурса"));
    await fill(screen.getByLabelText("Название ресурса"), "Newest");
    finish!(preview("stale"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Применить" })).toBeEnabled());
    expect(screen.getByTestId("buffer").textContent).toBe(document);
    expect(store.serialize()).toContain("Newest");
  });
});

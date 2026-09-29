import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import ResourceMapEditor from "./ResourceMapEditor";
import type { Model } from "./model";

vi.mock("./ResourceGraph", () => ({
  default: ({ model, disabled }: { model: Model; disabled: boolean }) => (
    <output data-testid="graph" data-disabled={disabled}>
      {model.resources[0]?.x}
    </output>
  ),
}));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const document = '{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{}}';
const normalized = document + "\n";
const arranged = document + "\n\n";
const model: Model = {
  resources: [
    {
      id: "orders",
      name: "Заказы",
      service: "",
      description: "",
      operationKeys: [],
      x: 40,
      y: 40,
      inferred: true,
    },
  ],
  operations: [],
  relations: [],
  diagnostics: [],
};
const result = (source = normalized, x = 40) =>
  json(200, {
    document: source,
    model: { ...model, resources: [{ ...model.resources[0], x }] },
    valid: true,
    diagnostics: [],
  });
function requests(layout: () => Response | Promise<Response>) {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).endsWith("/resource-map"))
      return Promise.resolve(
        json(200, {
          designId: 12,
          version: 1,
          revisionId: 41,
          model,
          scenarioUsages: [],
          usagesTruncated: false,
        }),
      );
    const body = JSON.parse(String(init?.body));
    return Promise.resolve(
      body.commands ? layout() : result(body.document === document ? normalized : body.document),
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}
function Harness({
  store = createFormDraftStore(),
}: {
  store?: ReturnType<typeof createFormDraftStore>;
}) {
  const [buffer, setBuffer] = useState(document);
  const [pending, setPending] = useState(false);
  const [mounted, setMounted] = useState(true);
  return (
    <>
      {mounted && (
        <ResourceMapEditor
          designId={12}
          document={buffer}
          blocked={false}
          formStore={store}
          onChange={setBuffer}
          onOperation={() => {}}
          onSchema={() => {}}
          onLayoutPendingChange={setPending}
        />
      )}
      <output data-testid="buffer">{buffer}</output>
      <button disabled={pending}>Сохранить</button>
      <button onClick={() => setBuffer(document + " ")}>Изменить документ</button>
      <button onClick={() => setMounted((value) => !value)}>Переключить вкладку</button>
    </>
  );
}

describe("resource layout proposal", () => {
  it("uses normalized input and changes the buffer only when the displayed proposal is applied", async () => {
    const fetchMock = requests(() => result(arranged, 480));
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await userEvent.click(await screen.findByRole("button", { name: /Ресурс Заказы/ }));
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    await screen.findByRole("button", { name: "Применить расположение" });
    const body = fetchMock.mock.calls
      .map(([, init]) => JSON.parse(String(init?.body || "{}")))
      .find((body) => body.commands);
    expect(body).toEqual({ document: normalized, commands: [{ kind: "auto_layout" }] });
    expect(screen.getByTestId("buffer").textContent).toBe(document);
    expect(screen.getByTestId("graph")).toHaveTextContent("480");
    expect(screen.getByTestId("graph")).toHaveAttribute("data-disabled", "true");
    expect(screen.getByLabelText("Название ресурса")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();
    expect(store.getSnapshot().dirty).toBe(false);
    expect(fetchMock.mock.calls.every(([, init]) => init?.method !== "PUT")).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Применить расположение" }));
    expect(screen.getByTestId("buffer").textContent).toBe(arranged);
    expect(screen.getByLabelText("X")).toHaveValue("480");
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  });

  it("cancels the proposal without changing the document or prior selection", async () => {
    requests(() => result(arranged, 480));
    renderWithProviders(<Harness />);
    await userEvent.click(await screen.findByRole("button", { name: /Ресурс Заказы/ }));
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    await screen.findByRole("button", { name: "Применить расположение" });
    await userEvent.click(screen.getByRole("button", { name: "Отменить" }));
    expect(screen.getByTestId("buffer").textContent).toBe(document);
    expect(screen.getByTestId("graph")).toHaveTextContent("40");
    expect(screen.getByLabelText("Название ресурса")).toHaveValue("Заказы");
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  });

  it("keeps a newer request blocked when a cancelled older request settles", async () => {
    const finish: Array<(response: Response) => void> = [];
    requests(() => new Promise((resolve) => finish.push(resolve)));
    renderWithProviders(<Harness />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Расставить ресурсы" })).toBeEnabled(),
    );
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    await userEvent.click(screen.getByRole("button", { name: "Отменить" }));
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    await waitFor(() => expect(finish).toHaveLength(2));
    await act(async () => finish[0]!(result("stale", 900)));
    expect(
      screen.queryByRole("button", { name: "Применить расположение" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Добавить ресурс" })).toBeDisabled();
    await act(async () => finish[1]!(result(arranged, 480)));
    await userEvent.click(await screen.findByRole("button", { name: "Применить расположение" }));
    expect(screen.getByTestId("buffer").textContent).toBe(arranged);
  });

  it("invalidates an in-flight proposal when the document changes", async () => {
    let finish!: (response: Response) => void;
    requests(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    renderWithProviders(<Harness />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Расставить ресурсы" })).toBeEnabled(),
    );
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    await userEvent.click(screen.getByRole("button", { name: "Изменить документ" }));
    await act(async () => finish(result("stale", 900)));
    expect(
      screen.queryByRole("button", { name: "Применить расположение" }),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId("buffer").textContent).toBe(document + " ");
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  });

  it("keeps layout ownership when an earlier discarded form command completes", async () => {
    const finish: Array<(response: Response) => void> = [];
    requests(() => new Promise((resolve) => finish.push(resolve)));
    renderWithProviders(<Harness />);
    await userEvent.click(await screen.findByRole("button", { name: /Ресурс Заказы/ }));
    await userEvent.clear(screen.getByLabelText("Название ресурса"));
    await userEvent.type(screen.getByLabelText("Название ресурса"), "Discard me");
    await userEvent.click(screen.getByRole("button", { name: "Применить" }));
    await waitFor(() => expect(finish).toHaveLength(1));
    await userEvent.click(screen.getByRole("button", { name: "Сбросить ввод" }));
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    await waitFor(() => expect(finish).toHaveLength(2));
    await act(async () => finish[0]!(result("stale", 900)));
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Добавить ресурс" })).toBeDisabled();
    expect(screen.getByTestId("buffer").textContent).toBe(document);
    await act(async () => finish[1]!(result(arranged, 480)));
    await userEvent.click(await screen.findByRole("button", { name: "Применить расположение" }));
    expect(screen.getByTestId("buffer").textContent).toBe(arranged);
  });

  it("clears the save guard on unmount and ignores the late response without leaving form drafts", async () => {
    let finish!: (response: Response) => void;
    requests(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const store = createFormDraftStore();
    renderWithProviders(<Harness store={store} />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Расставить ресурсы" })).toBeEnabled(),
    );
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    await userEvent.click(screen.getByRole("button", { name: "Переключить вкладку" }));
    await act(async () => finish(result("stale", 900)));
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
    expect(screen.getByTestId("buffer").textContent).toBe(document);
    expect(store.getSnapshot().dirty).toBe(false);
  });

  it("preserves a pending form and its validation error instead of starting a layout", async () => {
    const fetchMock = requests(() => result());
    const store = createFormDraftStore();
    const draft = {
      source: JSON.stringify({
        kind: "resource",
        id: "orders",
        name: "Pending",
        service: "",
        description: "",
        x: "bad",
        y: "40",
      }),
      propertySource: document,
      error: "Invalid coordinate",
    };
    store.set("/x-mocker-resource-map/editor", draft);
    renderWithProviders(<Harness store={store} />);
    await screen.findByTestId("graph");
    expect(screen.getByRole("button", { name: "Расставить ресурсы" })).toBeDisabled();
    expect(store.get("/x-mocker-resource-map/editor")).toEqual(draft);
    expect(
      fetchMock.mock.calls.some(([, init]) => JSON.parse(String(init?.body || "{}")).commands),
    ).toBe(false);
  });

  it("allows retry after a rejected layout without modifying the buffer", async () => {
    let fails = true;
    requests(() =>
      fails
        ? json(400, { error: { code: "bad_request", message: "Расстановка недоступна" } })
        : result(arranged, 480),
    );
    renderWithProviders(<Harness />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Расставить ресурсы" })).toBeEnabled(),
    );
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    expect(await screen.findByText(/Расстановка недоступна/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
    expect(screen.getByTestId("buffer").textContent).toBe(document);
    fails = false;
    await userEvent.click(screen.getByRole("button", { name: "Расставить ресурсы" }));
    expect(
      await screen.findByRole("button", { name: "Применить расположение" }),
    ).toBeInTheDocument();
  });
});

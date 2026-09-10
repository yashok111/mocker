import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fill } from "@/test/user";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import {
  mergedOperationViewFixture,
  mergedStatusViewFixture,
  operationViewFixture,
  overrideDocViewFixture,
  sessionListViewFixture,
  workspaceFixture,
} from "@/test/fixtures";
import { OperationEditor } from "./OperationEditor";
import { OperationsPage } from "./OperationsPage";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const WS = 7;
const OPKEY = "GET%20%2Fpets";
const URL = `/api/workspaces/${WS}/operations/${OPKEY}`;
const STATUSES = [mergedStatusViewFixture({ selector: "200", httpStatus: 200, isDefault: true })];

function mountOperation(headers: Record<string, string> = {}, body: unknown = { ok: true }) {
  const doc = overrideDocViewFixture({
    opKey: OPKEY,
    responses: { "200": { mode: "pinned", body, headers } },
  });
  let removed = false;
  const fetchMock = route({
    [`GET ${URL}`]: () =>
      removed
        ? json(404, { error: { code: "not_found", message: "no override" } })
        : json(200, doc),
    [`DELETE ${URL}`]: () => {
      removed = true;
      return json(200, { revision: 10 });
    },
    [`PUT ${URL}`]: () => json(200, { revision: 11, editVersion: 2 }),
  });
  renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);
  return fetchMock;
}

describe("operation editor drafts", () => {
  it("saves the generated producer despite an invalid pinned draft, then validates that draft on return", async () => {
    const fetchMock = mountOperation();
    const body = await screen.findByTestId("operation-status-body-200");
    await userEvent.clear(body);
    await fill(body, "{broken");
    expect(screen.getByTestId("operation-save")).toBeDisabled();

    await userEvent.selectOptions(screen.getByTestId("operation-status-mode-200"), "generated");
    expect(screen.queryByTestId("operation-status-body-200")).toBeNull();
    expect(screen.queryByText(/JSON невалиден/)).toBeNull();
    expect(screen.getByTestId("operation-save")).toBeEnabled();
    await userEvent.click(screen.getByTestId("operation-save"));
    await screen.findByText("Сохранено, ревизия воркспейса 11");
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    expect(JSON.parse(String(put?.[1]?.body)).responses["200"]).toMatchObject({
      mode: "generated",
      body: { ok: true },
    });

    await userEvent.selectOptions(screen.getByTestId("operation-status-mode-200"), "pinned");
    expect(screen.getByTestId("operation-status-body-200")).toHaveValue("{broken");
    expect(screen.getByText(/JSON невалиден/)).toBeInTheDocument();
    expect(screen.getByTestId("operation-save")).toBeDisabled();
    await userEvent.clear(screen.getByTestId("operation-status-body-200"));
    await fill(screen.getByTestId("operation-status-body-200"), '{"fixed": true}');
    expect(screen.getByTestId("operation-save")).toBeEnabled();
  });

  it("discards reset headers so the next PUT contains only newly entered headers", async () => {
    const fetchMock = mountOperation({ "x-stale": "discarded" });
    await screen.findByTestId("operation-status-header-name-0-200");
    await userEvent.click(screen.getByTestId("operation-reset"));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByTestId("operation-reset-confirm"),
    );
    await screen.findByText("Сброшено, ревизия воркспейса 10");
    await userEvent.selectOptions(screen.getByTestId("operation-status-mode-200"), "pinned");
    expect(screen.queryByTestId("operation-status-header-name-0-200")).toBeNull();

    await userEvent.click(screen.getByTestId("operation-status-header-add-200"));
    await fill(screen.getByTestId("operation-status-header-name-0-200"), "x-new");
    await fill(screen.getByTestId("operation-status-header-value-0-200"), "retained");
    await userEvent.click(screen.getByTestId("operation-save"));
    await screen.findByText("Сохранено, ревизия воркспейса 11");
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    expect(JSON.parse(String(put?.[1]?.body)).responses["200"].headers).toEqual({
      "x-new": "retained",
    });
  });

  it("clears incomplete text and unnamed header rows on reset even when the stored body is already empty", async () => {
    mountOperation({}, {});
    const body = await screen.findByTestId("operation-status-body-200");
    await userEvent.clear(body);
    await fill(body, "{");
    await userEvent.click(screen.getByTestId("operation-status-header-add-200"));
    await userEvent.click(screen.getByTestId("operation-reset"));
    await userEvent.click(within(await screen.findByRole("dialog")).getByTestId("dialog-cancel"));
    expect(screen.getByTestId("operation-status-body-200")).toHaveValue("{");
    expect(screen.getByTestId("operation-status-header-name-0-200")).toHaveValue("");

    await userEvent.click(screen.getByTestId("operation-reset"));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByTestId("operation-reset-confirm"),
    );
    await screen.findByText("Сброшено, ревизия воркспейса 10");
    await userEvent.selectOptions(screen.getByTestId("operation-status-mode-200"), "pinned");
    expect(screen.getByTestId("operation-status-body-200")).toHaveValue("{}");
    expect(screen.queryByTestId("operation-status-header-name-0-200")).toBeNull();
    expect(screen.getByTestId("operation-save")).toBeEnabled();
  });

  it("removing one status discards its headers and raw draft while preserving another status's incomplete JSON", async () => {
    const doc = overrideDocViewFixture({
      opKey: OPKEY,
      activeStatus: undefined,
      responses: {
        "200": { mode: "pinned", body: {}, headers: { "x-stale": "discarded" } },
        "404": { mode: "pinned", body: { kept: true } },
      },
    });
    const savedDoc = {
      ...doc,
      revision: 11,
      editVersion: 2,
      responses: {
        "200": { mode: "pinned", body: {}, headers: { "x-new": "retained" } },
        "404": { mode: "pinned", body: { kept: true } },
      },
    };
    let saved = false;
    const fetchMock = route({
      [`GET ${URL}`]: () => json(200, saved ? savedDoc : doc),
      [`PUT ${URL}`]: () => {
        saved = true;
        return json(200, { revision: 11, editVersion: 2 });
      },
    });
    renderInRouter(
      <OperationEditor
        workspaceId={WS}
        opKey={OPKEY}
        statuses={[
          ...STATUSES,
          mergedStatusViewFixture({ selector: "404", httpStatus: 404, isDefault: false }),
        ]}
      />,
    );
    await userEvent.clear(await screen.findByTestId("operation-status-body-200"));
    await fill(screen.getByTestId("operation-status-body-200"), "{discarded");
    await userEvent.click(screen.getByTestId("operation-status-tab-404"));
    await userEvent.clear(screen.getByTestId("operation-status-body-404"));
    await fill(screen.getByTestId("operation-status-body-404"), '{ "unfinished":');
    await userEvent.click(screen.getByTestId("operation-status-tab-200"));
    await userEvent.click(screen.getByTestId("operation-status-remove-200"));
    await userEvent.selectOptions(screen.getByTestId("operation-status-mode-200"), "pinned");
    expect(screen.queryByTestId("operation-status-header-name-0-200")).toBeNull();
    expect(screen.getByTestId("operation-status-body-200")).toHaveValue("{}");
    await userEvent.click(screen.getByTestId("operation-status-header-add-200"));
    await fill(screen.getByTestId("operation-status-header-name-0-200"), "x-new");
    await fill(screen.getByTestId("operation-status-header-value-0-200"), "retained");

    await userEvent.click(screen.getByTestId("operation-status-tab-404"));
    expect(screen.getByTestId("operation-status-body-404")).toHaveValue('{ "unfinished":');
    expect(screen.getByTestId("operation-save")).toBeDisabled();
    await userEvent.clear(screen.getByTestId("operation-status-body-404"));
    await fill(screen.getByTestId("operation-status-body-404"), '{"kept": true}');
    expect(screen.getByTestId("operation-save")).toBeEnabled();
    await userEvent.click(screen.getByTestId("operation-save"));
    await screen.findByText("Сохранено, ревизия воркспейса 11");
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    expect(JSON.parse(String(put?.[1]?.body)).responses).toEqual({
      "200": { mode: "pinned", body: {}, headers: { "x-new": "retained" } },
      "404": { mode: "pinned", body: { kept: true } },
    });
    await waitFor(() => expect(screen.queryByTestId("operation-dirty")).toBeNull());
  });

  it("asks before losing incomplete JSON on an operation switch, preserves it on cancel and discards on confirmation", async () => {
    const first = mergedOperationViewFixture({
      method: "GET",
      path: "/pets",
      opKey: OPKEY,
      statuses: STATUSES,
    });
    const second = mergedOperationViewFixture({
      method: "POST",
      path: "/orders",
      opKey: "POST%20%2Forders",
      statuses: STATUSES,
    });
    route({
      [`GET /api/workspaces/${WS}`]: () => json(200, workspaceFixture({ id: WS, specId: 1 })),
      [`GET /api/workspaces/${WS}/operations`]: () => json(200, [first, second]),
      "GET /api/specs/1/operations?limit=500": () =>
        json(200, [
          operationViewFixture({ method: "GET", path: "/pets" }),
          operationViewFixture({ id: 2, method: "POST", path: "/orders" }),
        ]),
      [`GET /api/workspaces/${WS}/session`]: () =>
        json(200, sessionListViewFixture({ directives: [] })),
      [`GET ${URL}`]: () =>
        json(
          200,
          overrideDocViewFixture({
            opKey: OPKEY,
            responses: { "200": { mode: "pinned", body: { ok: true } } },
          }),
        ),
      [`GET /api/workspaces/${WS}/operations/POST%20%2Forders`]: () =>
        json(404, { error: { code: "not_found", message: "no override" } }),
    });
    renderInRouter(<OperationsPage id={WS} initialOpKey={OPKEY} />);
    const body = await screen.findByTestId("operation-status-body-200");
    await userEvent.clear(body);
    await fill(body, '{ "unfinished":');
    expect(screen.getByTestId("operation-save")).toBeDisabled();
    expect(screen.getByTestId("operation-dirty")).toBeInTheDocument();
    const orders = screen
      .getAllByTestId("operation-row")
      .find((row) => row.textContent?.includes("/orders"))!;
    await userEvent.click(orders);
    const dialog = await screen.findByRole("dialog");
    expect(screen.getByTestId("operation-editor")).toHaveTextContent("GET /pets");
    await userEvent.click(within(dialog).getByTestId("dialog-cancel"));
    expect(screen.getByTestId("operation-status-body-200")).toHaveValue('{ "unfinished":');
    await userEvent.click(orders);
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByTestId("operations-discard-confirm"),
    );
    await waitFor(() =>
      expect(screen.getByTestId("operation-editor")).toHaveTextContent("POST /orders"),
    );
    expect(screen.queryByTestId("operation-dirty")).toBeNull();
  });
});

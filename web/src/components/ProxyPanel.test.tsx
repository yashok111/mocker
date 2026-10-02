import { useState } from "react";
import { getGetWorkspaceProxyQueryKey } from "@/api/generated/proxy/proxy";
import { afterEach, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ProxyPanel } from "./ProxyPanel";
import { makeQueryClient, renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import { fill } from "@/test/user";
const config = {
  version: 2,
  mode: "off",
  upstream: "https://api.example.com",
  timeoutSeconds: 15,
  forwardAuth: false,
  forwardCookies: false,
  overwrite: "last",
  captureEntities: false,
  operations: {},
};
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
it("saves mode and operation policy with the loaded version", async () => {
  const fetchMock = route({
    "GET /api/workspaces/7/proxy": () =>
      json(200, { config, allowedOrigins: ["https://api.example.com"] }),
    "GET /api/workspaces/7/proxy/recordings": () => json(200, { items: [] }),
    "GET /api/workspaces/7/operations": () =>
      json(200, [
        { method: "GET", path: "/users/{id}", statuses: [], opKey: "GET%20%2Fusers%2F%7Bid%7D" },
      ]),
    "PUT /api/workspaces/7/proxy": () =>
      json(200, {
        config: { ...config, mode: "record", version: 3 },
        allowedOrigins: ["https://api.example.com"],
      }),
  });
  renderWithProviders(<ProxyPanel id={7} slug="demo" />);
  fireEvent.change(await screen.findByLabelText("Режим прокси"), { target: { value: "record" } });
  fireEvent.change(await screen.findByLabelText("Режим GET /users/{id}"), {
    target: { value: "mock" },
  });
  await userEvent.click(screen.getByRole("button", { name: "Сохранить прокси" }));
  await waitFor(() =>
    expect(
      JSON.parse(
        String(fetchMock.mock.calls.find(([, init]) => init?.method === "PUT")?.[1]?.body),
      ),
    ).toMatchObject({ version: 2, mode: "record", operations: { "GET /users/{id}": "mock" } }),
  );
  expect(await screen.findByText("Настройки прокси сохранены")).toBeInTheDocument();
});
it("inspects redacted recordings and clears with slug and version", async () => {
  let cleared = false;
  const fetchMock = route({
    "GET /api/workspaces/7/proxy": () => json(200, { config, allowedOrigins: [] }),
    "GET /api/workspaces/7/operations": () => json(200, []),
    "GET /api/workspaces/7/proxy/recordings": () =>
      json(200, {
        items: cleared
          ? []
          : [
              {
                id: 5,
                method: "GET",
                path: "/users/1",
                status: 200,
                contentType: "application/json",
                bodyText: '{"id":9007199254740993,"token":"[redacted]"}',
                redacted: true,
                createdAt: 1,
                updatedAt: 1,
              },
            ],
      }),
    "POST /api/workspaces/7/proxy/recordings/clear": () => {
      cleared = true;
      return json(200, { ok: true });
    },
  });
  renderWithProviders(<ProxyPanel id={7} slug="demo" />);
  await userEvent.click(await screen.findByRole("button", { name: "Открыть запись 5" }));
  expect(await screen.findByText(/\[redacted\]/)).toHaveTextContent("9007199254740993");
  await userEvent.click(screen.getByRole("button", { name: "Закрыть запись" }));
  await userEvent.click(screen.getByRole("button", { name: "Очистить записи" }));
  await fill(screen.getByLabelText("Slug для подтверждения"), "demo");
  await userEvent.click(screen.getByRole("button", { name: "Подтвердить очистку" }));
  await waitFor(() =>
    expect(
      JSON.parse(
        String(fetchMock.mock.calls.find(([, init]) => init?.method === "POST")?.[1]?.body),
      ),
    ).toEqual({ version: 2, confirmSlug: "demo" }),
  );
});
it("shows fetch errors with retry", async () => {
  route({
    "GET /api/workspaces/7/proxy": () =>
      json(500, { error: { code: "internal", message: "broken" } }),
    "GET /api/workspaces/7/operations": () => json(200, []),
    "GET /api/workspaces/7/proxy/recordings": () => json(200, { items: [] }),
  });
  renderWithProviders(<ProxyPanel id={7} slug="demo" />);
  expect(
    await screen.findByRole("button", { name: "Повторить загрузку прокси" }),
  ).toBeInTheDocument();
});

it("resets drafts and selected recordings when switching cached workspaces", async () => {
  const initial = { ...config, version: 0, upstream: "" };
  const view = { config: initial, allowedOrigins: ["https://api.example.com"] };
  const client = makeQueryClient();
  client.setDefaultOptions({
    queries: { retry: false, gcTime: Infinity, staleTime: Infinity },
    mutations: { retry: false },
  });
  for (const id of [1, 2])
    client.setQueryData(getGetWorkspaceProxyQueryKey(id), {
      status: 200,
      data: view,
      headers: new Headers(),
    });
  const fetchMock = route({
    "GET /api/workspaces/1/proxy": () => json(200, view),
    "GET /api/workspaces/2/proxy": () => json(200, view),
    "GET /api/workspaces/1/operations": () => json(200, []),
    "GET /api/workspaces/2/operations": () => json(200, []),
    "GET /api/workspaces/1/proxy/recordings": () =>
      json(200, {
        items: [
          {
            id: 5,
            method: "GET",
            path: "/users",
            status: 200,
            contentType: "application/json",
            bodyText: '{"workspace":"A"}',
            redacted: false,
            createdAt: 1,
            updatedAt: 1,
          },
        ],
      }),
    "GET /api/workspaces/2/proxy/recordings": () => json(200, { items: [] }),
    "PUT /api/workspaces/2/proxy": () => json(200, { ...view, config: { ...initial, version: 1 } }),
  });
  function Harness() {
    const [id, setID] = useState(1);
    return (
      <>
        <button onClick={() => setID(2)}>Switch workspace</button>
        <ProxyPanel id={id} slug={String(id)} />
      </>
    );
  }
  renderWithProviders(<Harness />, { queryClient: client });
  fireEvent.change(await screen.findByLabelText("Адрес upstream"), {
    target: { value: "https://api.example.com" },
  });
  fireEvent.change(screen.getByLabelText("Режим прокси"), { target: { value: "record" } });
  await userEvent.click(await screen.findByRole("button", { name: "Открыть запись 5" }));
  fireEvent.click(screen.getByText("Switch workspace"));
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(await screen.findByLabelText("Адрес upstream")).toHaveValue("");
  expect(screen.getByLabelText("Режим прокси")).toHaveValue("off");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить прокси" }));
  await waitFor(() =>
    expect(
      JSON.parse(
        String(
          fetchMock.mock.calls.find(
            ([url, init]) => String(url) === "/api/workspaces/2/proxy" && init?.method === "PUT",
          )?.[1]?.body,
        ),
      ),
    ).toMatchObject({ version: 0, mode: "off", upstream: "" }),
  );
});

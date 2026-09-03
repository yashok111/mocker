import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConnectPanel } from "./ConnectPanel";
import { renderWithProviders } from "@/test/render";
import { serverConfigFixture, workspaceFixture } from "@/test/fixtures";

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function route(handlers: Record<string, () => Response | Promise<Response>>) {
  const fn = vi.fn<(...args: FetchArgs) => Promise<Response>>(async (input, init) => {
    const key = `${(init?.method ?? "GET").toUpperCase()} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) {
      throw new TypeError(`Failed to fetch: ${key}`);
    }
    return handler();
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

const config = serverConfigFixture();
const workspace = workspaceFixture({
  id: 7,
  slug: "alex",
  url: "http://alex.mock.corp.internal:8080",
});

// The health URL the panel must build: ws.url + the reserved prefix AS
// CONFIGURED. Written out here rather than assembled from the same constants
// the component uses, so a hard-coded /__mocker in the UI fails this file.
const healthURL = "http://alex.mock.corp.internal:8080/__test-prefix/health";
const trafficURL = "/api/workspaces/7/traffic";
const probeURL = "/api/workspaces/7/probe";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ConnectPanel", () => {
  it("shows the address the server sent, never one rebuilt from the slug", async () => {
    route({ [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }) });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    expect(screen.getByTestId("connect-address-copy-input")).toHaveValue(
      "http://alex.mock.corp.internal:8080",
    );
  });

  it("renders all four recipes", async () => {
    route({ [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }) });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    for (const id of ["env", "apiBase", "devtools", "curl"]) {
      expect(screen.getByTestId(`connect-recipe-${id}`)).toBeInTheDocument();
    }
  });

  it("reports the rate the server computed", async () => {
    route({ [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 12, dropped: 0 }) });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    // findByText, not findByTestId: the rate line exists immediately, showing
    // "Считаем…" — asserting on the element the moment it appears would race
    // the request it is waiting for.
    expect(await screen.findByText("Сюда пришло 12 запросов за минуту")).toBeInTheDocument();
  });

  it("degrades the rate line alone when the traffic poll fails", async () => {
    route({
      [`GET ${trafficURL}`]: () => json(500, { error: { code: "internal", message: "boom" } }),
    });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    expect(await screen.findByText("Не удалось получить данные о трафике")).toBeInTheDocument();
    // Everything else this panel offers still works.
    expect(screen.getByTestId("connect-address-copy-input")).toBeInTheDocument();
    expect(screen.getByTestId("connect-probe-button")).toBeEnabled();
  });

  it("renders no probe result at all before «Проверить» is pressed", async () => {
    route({ [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }) });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    // Absent from the DOM, not merely empty: an empty result region reads as
    // "checked, nothing to say".
    expect(screen.queryByTestId("connect-probe-result")).not.toBeInTheDocument();
  });

  it("probes the CONFIGURED reserved prefix, not a hard-coded one", async () => {
    const fetchMock = route({
      [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
      [`GET ${healthURL}`]: () =>
        json(200, { ok: true, workspace: "alex", revision: 4, spec: null }),
    });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    await userEvent.click(screen.getByTestId("connect-probe-button"));

    const result = await screen.findByTestId("connect-probe-result");
    expect(result).toHaveAttribute("data-probe-kind", "ok");
    expect(screen.getByTestId("connect-probe-workspace")).toHaveTextContent("alex");
    expect(screen.getByTestId("connect-probe-revision")).toHaveTextContent("4");
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain(healthURL);
  });

  it("calls out a wildcard/proxy mix-up when another workspace answers", async () => {
    route({
      [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
      [`GET ${healthURL}`]: () =>
        json(200, { ok: true, workspace: "someone-else", revision: 1, spec: null }),
    });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    await userEvent.click(screen.getByTestId("connect-probe-button"));

    const result = await screen.findByTestId("connect-probe-result");
    expect(result).toHaveAttribute("data-probe-kind", "wrong-workspace");
    expect(result).toHaveTextContent("someone-else");
  });

  it("names three causes for a bare network failure, because there is no way to tell them apart", async () => {
    route({
      [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
      // No handler for the health URL: route() throws a TypeError, exactly
      // what a browser reports for a CORS/DNS/TLS failure.
    });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    await userEvent.click(screen.getByTestId("connect-probe-button"));

    const result = await screen.findByTestId("connect-probe-result");
    expect(result).toHaveAttribute("data-probe-kind", "network-error");
    expect(result).toHaveTextContent("DNS");
    expect(result).toHaveTextContent("сертификат");
    expect(result).toHaveTextContent("больше не существует");
  });

  it("reports an HTTP failure with the status the mock actually returned", async () => {
    route({
      [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
      [`GET ${healthURL}`]: () => json(503, { error: { code: "internal", message: "not ready" } }),
    });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    await userEvent.click(screen.getByTestId("connect-probe-button"));

    const result = await screen.findByTestId("connect-probe-result");
    expect(result).toHaveAttribute("data-probe-kind", "http-error");
    expect(result).toHaveTextContent("503");
  });

  it("shows the server's own probe result alongside the browser's", async () => {
    route({
      [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
      [`GET ${healthURL}`]: () =>
        json(200, { ok: true, workspace: "alex", revision: 4, spec: null }),
      [`POST ${probeURL}`]: () => json(200, { kind: "ok", workspace: "alex", revision: 4 }),
    });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    await userEvent.click(screen.getByTestId("connect-probe-button"));

    const serverResult = await screen.findByTestId("connect-server-probe-result");
    expect(serverResult).toHaveAttribute("data-probe-kind", "ok");
    expect(screen.getByTestId("connect-server-probe-revision")).toHaveTextContent("4");
    // No diagnosis banner when both sides agree.
    expect(screen.queryByTestId("connect-probe-diagnosis")).not.toBeInTheDocument();
  });

  it("names the missing root CA when the server sees the workspace but the browser doesn't", async () => {
    route({
      [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
      // No handler for healthURL: the browser's own fetch fails, exactly like
      // the "three causes" test above.
      [`POST ${probeURL}`]: () => json(200, { kind: "ok", workspace: "alex", revision: 4 }),
    });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    await userEvent.click(screen.getByTestId("connect-probe-button"));

    const browserResult = await screen.findByTestId("connect-probe-result");
    expect(browserResult).toHaveAttribute("data-probe-kind", "network-error");
    const serverResult = await screen.findByTestId("connect-server-probe-result");
    expect(serverResult).toHaveAttribute("data-probe-kind", "ok");
    expect(screen.getByTestId("connect-probe-diagnosis")).toHaveTextContent("сертификат");
  });

  it("reports the server-side call itself failing, distinctly from a target it could not reach", async () => {
    route({
      [`GET ${trafficURL}`]: () => json(200, { rows: [], rate1m: 0, dropped: 0 }),
      [`GET ${healthURL}`]: () =>
        json(200, { ok: true, workspace: "alex", revision: 4, spec: null }),
      [`POST ${probeURL}`]: () =>
        json(404, { error: { code: "not_found", message: "workspace not found" } }),
    });
    renderWithProviders(<ConnectPanel workspace={workspace} config={config} />);

    await userEvent.click(screen.getByTestId("connect-probe-button"));

    const serverResult = await screen.findByTestId("connect-server-probe-result");
    expect(serverResult).toHaveAttribute("data-probe-kind", "call-error");
  });
});

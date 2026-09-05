import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fill } from "@/test/user";
import { StreamConnectionsPage } from "./StreamConnectionsPage";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import type { StreamConnectionView } from "@/api/generated/schemas";

const WS = 7;
const LIST = `GET /api/workspaces/${WS}/connections`;
// A20: the page polls GET /api/stream/stats beside the list; every stub
// below answers it so the strip renders rather than reporting an unrouted 500.
const STATS = "GET /api/stream/stats";
// The top level is the ADMIN feed's registry; `mock` is the plane this page
// lists, and the strip must read THAT one (the numbers differ on purpose).
const statsView = () =>
  json(200, {
    open: 5,
    cap: 64,
    refusedCap: 9,
    refusedUnsupported: 0,
    coalescedNudges: 0,
    byWorkspace: [{ workspaceId: WS, open: 5 }],
    mock: {
      open: 3,
      cap: 200,
      refusedCap: 1,
      refusedUnsupported: 0,
      coalescedNudges: 0,
      byWorkspace: [
        { workspaceId: WS, open: 2 },
        { workspaceId: 99, open: 1 },
      ],
    },
  });

function conn(overrides: Partial<StreamConnectionView> = {}): StreamConnectionView {
  return {
    id: 3,
    endpointId: 11,
    path: "/events",
    kind: "sse",
    remoteAddr: "10.0.0.5:51234",
    openedAt: "2026-09-02T10:00:00Z",
    frames: 4,
    pushed: 0,
    skipped: 0,
    framesIn: 0,
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("StreamConnectionsPage", () => {
  it("renders its outer marker in every state and the empty state with the cap", async () => {
    route({ [LIST]: () => json(200, { open: 0, cap: 200, connections: [] }), [STATS]: statsView });
    renderWithProviders(<StreamConnectionsPage id={WS} />);
    expect(screen.getByTestId("connections-page")).toBeInTheDocument();
    expect(await screen.findByTestId("connections-empty")).toBeInTheDocument();
    expect(screen.getByTestId("connections-open")).toHaveTextContent("Открыто 0 из 200");
    // A20: the process-wide strip beside the workspace's own list.
    const strip = await screen.findByTestId("stream-stats");
    await waitFor(() => expect(strip).toHaveTextContent("открыто 3, из них этого воркспейса 2"));
    expect(strip).toHaveTextContent("ещё 1 воркспейс");
    expect(strip).toHaveTextContent("лимит 200 на воркспейс, отказов по лимиту с запуска 1");
    expect(strip).toHaveTextContent("живой трафик панели: открыто 5 из 64");
  });

  it("lists connections and says when the cap is reached", async () => {
    route({
      [STATS]: statsView,
      [LIST]: () =>
        json(200, {
          open: 2,
          cap: 2,
          connections: [conn(), conn({ id: 4, kind: "ws", framesIn: 7, pushed: 1 })],
        }),
    });
    renderWithProviders(<StreamConnectionsPage id={WS} />);
    const rows = await screen.findAllByTestId("connection-row");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("SSE");
    expect(rows[1]).toHaveTextContent("WebSocket");
    expect(rows[1]).toHaveTextContent("7");
    expect(
      screen.getByText("лимит достигнут — новые рукопожатия получают 503"),
    ).toBeInTheDocument();
  });

  it("closes a connection through DELETE and refetches the list", async () => {
    let listed = 0;
    const fetchMock = route({
      [STATS]: statsView,
      [LIST]: () => {
        listed += 1;
        return json(200, { open: 1, cap: 200, connections: [conn()] });
      },
      [`DELETE /api/workspaces/${WS}/connections/3`]: () => new Response(null, { status: 204 }),
    });
    renderWithProviders(<StreamConnectionsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("connection-close"));
    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(1);
    });
    await waitFor(() => expect(listed).toBeGreaterThanOrEqual(2));
  });

  it("pushes a frame with an event name on sse and shows the frame id", async () => {
    const fetchMock = route({
      [STATS]: statsView,
      [LIST]: () => json(200, { open: 1, cap: 200, connections: [conn()] }),
      [`POST /api/workspaces/${WS}/connections/3/frames`]: () =>
        json(200, { connectionId: 3, frameId: 5 }),
    });
    renderWithProviders(<StreamConnectionsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("connection-push-toggle"));
    await fill(screen.getByTestId("connection-push-event"), "alert");
    await userEvent.clear(screen.getByTestId("connection-push-data"));
    await fill(screen.getByTestId("connection-push-data"), '{"level":"red"}');
    await userEvent.click(screen.getByTestId("connection-push-submit"));
    expect(await screen.findByTestId("connection-pushed")).toHaveTextContent("id кадра 5");
    const post = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
    expect(JSON.parse(String(post?.[1]?.body))).toEqual({ event: "alert", data: { level: "red" } });
  });

  it("shows the server's own words on a 504 push_timeout and never resends on its own", async () => {
    const fetchMock = route({
      [STATS]: statsView,
      [LIST]: () => json(200, { open: 1, cap: 200, connections: [conn({ kind: "ws" })] }),
      [`POST /api/workspaces/${WS}/connections/3/frames`]: () =>
        json(504, {
          error: {
            code: "push_timeout",
            message: "the frame stays queued and may still be written; do not resend blindly",
          },
        }),
    });
    renderWithProviders(<StreamConnectionsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("connection-push-toggle"));
    // A ws row offers no event field: the server refuses one by name.
    expect(screen.queryByTestId("connection-push-event")).toBeNull();
    await userEvent.click(screen.getByTestId("connection-push-submit"));
    expect(await screen.findByTestId("connection-error")).toHaveTextContent(
      "do not resend blindly",
    );
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(1);
    expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({ data: {} });
  });

  it("refuses malformed JSON in the frame without a request", async () => {
    const fetchMock = route({
      [STATS]: statsView,
      [LIST]: () => json(200, { open: 1, cap: 200, connections: [conn()] }),
    });
    renderWithProviders(<StreamConnectionsPage id={WS} />);
    await userEvent.click(await screen.findByTestId("connection-push-toggle"));
    await userEvent.clear(screen.getByTestId("connection-push-data"));
    await fill(screen.getByTestId("connection-push-data"), "{nope");
    await userEvent.click(screen.getByTestId("connection-push-submit"));
    expect(await screen.findByText(/JSON невалиден/)).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
  });
});

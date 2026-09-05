import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TrafficPage } from "./TrafficPage";
import { makeQueryClient, renderInRouter } from "@/test/render";
import {
  trafficListViewFixture,
  trafficPollViewFixture,
  trafficRowFixture,
  workspaceFixture,
} from "@/test/fixtures";
import { json, route } from "@/test/http";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import {
  getGetOperationOverrideQueryKey,
  getListWorkspaceOperationsQueryKey,
} from "@/api/generated/operations/operations.ts";
import { getListEndpointsQueryKey } from "@/api/generated/endpoints/endpoints.ts";
import { setUnauthorizedHandler } from "@/api/client";

const WS = 7;

// FakeEventSource stands in for the browser's EventSource (jsdom has none):
// it records the URL it was opened with, the listeners the screen attached,
// and whether close() was called, and lets a test push a `traffic` frame or
// an `error` event through exactly the surface the screen consumes (P6a
// D19). Tests that do not stub it run with EventSource undefined, which the
// screen treats as "no stream in this browser" — a permanent fallback with
// no reason beside the badge.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  onerror: (() => void) | null = null;
  closed = false;
  private listeners: Record<string, Array<(event: MessageEvent<string>) => void>> = {};

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void {
    (this.listeners[type] ??= []).push(listener);
  }

  close(): void {
    this.closed = true;
  }

  emitTraffic(view: unknown): void {
    for (const listener of this.listeners["traffic"] ?? []) {
      listener(new MessageEvent<string>("traffic", { data: JSON.stringify(view) }));
    }
  }

  emitError(): void {
    this.onerror?.();
  }
}

function stubEventSource(): typeof FakeEventSource {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  return FakeEventSource;
}

function streamResponse(): Response {
  // What a successful handshake looks like to the probe: 200,
  // text/event-stream, a body the probe cancels at once.
  return new Response("", { status: 200, headers: { "Content-Type": "text/event-stream" } });
}

// Every render fires THREE queries (tail limit=50, rate limit=1, poll
// since=<cursor>&limit=200 once the cursor is seeded) — a test that only
// stubs the one route it cares about hangs on the other two instead of
// asserting anything, so this helper always wires all three, with an empty
// answer as the harmless default for whichever ones a given test does not
// care about.
function baseRoutes(
  opts: {
    tail?: () => Response;
    rate?: () => Response;
    poll?: (since: number) => Response;
  } = {},
) {
  const handlers: Record<string, () => Response> = {
    [`GET /api/workspaces/${WS}/traffic?limit=50`]:
      opts.tail ?? (() => json(200, trafficListViewFixture({ rows: [] }))),
    [`GET /api/workspaces/${WS}/traffic?limit=1`]:
      opts.rate ?? (() => json(200, trafficListViewFixture({ rows: [] }))),
  };
  return handlers;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  setUnauthorizedHandler(null);
});

describe("TrafficPage", () => {
  it("renders its outer marker and says it is loading before the tail answers", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<TrafficPage id={WS} />);

    expect(await screen.findByTestId("traffic-page")).toBeInTheDocument();
    expect(screen.getByText("Загрузка…")).toBeInTheDocument();
  });

  it("offers a retry when the tail fails, in Russian, without dropping the outer marker", async () => {
    let calls = 0;
    route({
      ...baseRoutes(),
      [`GET /api/workspaces/${WS}/traffic?limit=50`]: () => {
        calls += 1;
        return calls === 1
          ? json(500, { error: { code: "internal", message: "db is down" } })
          : json(200, trafficListViewFixture({ rows: [] }));
      },
    });
    renderInRouter(<TrafficPage id={WS} />);

    expect(await screen.findByTestId("traffic-page")).toBeInTheDocument();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Ошибка на сервере. Попробуйте ещё раз");
    expect(alert).not.toHaveTextContent("db is down");

    await userEvent.click(screen.getByTestId("traffic-retry"));
    expect(await screen.findByTestId("traffic-empty")).toBeInTheDocument();
  });

  it("says nothing is recorded yet when the tail is empty", async () => {
    route({
      ...baseRoutes(),
      [`GET /api/workspaces/${WS}/traffic/poll?since=0&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 0 })),
    });
    renderInRouter(<TrafficPage id={WS} />);

    expect(await screen.findByTestId("traffic-empty")).toBeInTheDocument();
  });

  it("seeds the poll cursor from max(rows[].id) — the list carries no lastId — and merges a duplicate row instead of doubling it", async () => {
    const rowOld = trafficRowFixture({ id: 3, path: "/pets/3" });
    route({
      ...baseRoutes({
        tail: () => json(200, trafficListViewFixture({ rows: [rowOld], rate1m: 2 })),
      }),
      // The poll must be asked "since=3" (max id of the tail), not since=0 and
      // not the tail's own absent lastId.
      [`GET /api/workspaces/${WS}/traffic/poll?since=3&limit=200`]: () =>
        json(
          200,
          trafficPollViewFixture({
            rows: [rowOld, trafficRowFixture({ id: 4, path: "/pets/4" })],
            lastId: 4,
          }),
        ),
    });
    renderInRouter(<TrafficPage id={WS} />);

    // The poll (id 4) resolves after the tail (id 3) has already rendered —
    // wait for its row specifically before counting.
    expect(await screen.findByText("/pets/4")).toBeInTheDocument();
    expect(screen.getByText("/pets/3")).toBeInTheDocument();
    // Two distinct ids merged, not three — id 3 arrived from both the tail
    // AND the poll and must collapse to one row.
    expect(screen.getAllByTestId("traffic-row")).toHaveLength(2);
  });

  it("words the dropped counter as a process-wide fact, not as this list being incomplete", async () => {
    route({
      ...baseRoutes({
        tail: () => json(200, trafficListViewFixture({ rows: [], dropped: 12 })),
      }),
      [`GET /api/workspaces/${WS}/traffic/poll?since=0&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 0, dropped: 12 })),
    });
    renderInRouter(<TrafficPage id={WS} />);

    const banner = await screen.findByTestId("traffic-dropped-banner");
    expect(banner).toHaveTextContent("12");
    expect(banner).toHaveTextContent("за всё время работы процесса");
    // Must not claim THIS workspace's list is missing rows — dropped is a
    // process-wide lifetime counter (§3.5 fact 2), not scoped to this screen.
    expect(banner).not.toHaveTextContent("этот воркспейс");
  });

  describe("«Создать правку из этого ответа» — disabled for the reason the server would refuse", () => {
    const cases: Array<[string, Partial<ReturnType<typeof trafficRowFixture>>]> = [
      ["matched no operation", { matchedKind: "none", matchedId: null }],
      ["matched a custom endpoint, not an operation", { matchedKind: "endpoint", matchedId: 9 }],
      ["matched an operation but carries no id", { matchedKind: "operation", matchedId: null }],
      ["truncated", { matchedKind: "operation", matchedId: 1, truncated: true }],
      ["redacted", { matchedKind: "operation", matchedId: 1, notes: "redacted" }],
      [
        "suppressed — the auth/login row",
        { matchedKind: "operation", matchedId: 1, notes: "suppressed" },
      ],
      ["204 has no body to pin", { matchedKind: "operation", matchedId: 1, status: 204 }],
      ["205 has no body to pin", { matchedKind: "operation", matchedId: 1, status: 205 }],
    ];

    for (const [label, overrides] of cases) {
      it(`disables it when the row ${label}`, async () => {
        const row = trafficRowFixture({ id: 1, ...overrides });
        route({
          ...baseRoutes({ tail: () => json(200, trafficListViewFixture({ rows: [row] })) }),
          [`GET /api/workspaces/${WS}/traffic/poll?since=1&limit=200`]: () =>
            json(200, trafficPollViewFixture({ rows: [], lastId: 1 })),
        });
        renderInRouter(<TrafficPage id={WS} />);

        expect(await screen.findByTestId("traffic-to-override")).toBeDisabled();
        expect(screen.getByTestId("traffic-override-reason")).toBeInTheDocument();
      });
    }

    it("must NOT forge the 'redacted' token from a note that merely contains it as a substring", async () => {
      // notes is a comma-separated token list matched as WHOLE entries
      // (internal/traffic/traffic.go:158-165) — free text must not be able to
      // forge a token by embedding it inside another word.
      const row = trafficRowFixture({
        id: 1,
        matchedKind: "operation",
        matchedId: 1,
        notes: "not-redacted-really",
      });
      route({
        ...baseRoutes({ tail: () => json(200, trafficListViewFixture({ rows: [row] })) }),
        [`GET /api/workspaces/${WS}/traffic/poll?since=1&limit=200`]: () =>
          json(200, trafficPollViewFixture({ rows: [], lastId: 1 })),
      });
      renderInRouter(<TrafficPage id={WS} />);

      expect(await screen.findByTestId("traffic-to-override")).toBeEnabled();
    });
  });

  it("«Создать endpoint» has no matchedKind requirement — enabled even when the row matched nothing", async () => {
    const row = trafficRowFixture({
      id: 1,
      matchedKind: "none",
      matchedId: null,
      truncated: false,
      notes: "",
    });
    route({
      ...baseRoutes({ tail: () => json(200, trafficListViewFixture({ rows: [row] })) }),
      [`GET /api/workspaces/${WS}/traffic/poll?since=1&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 1 })),
    });
    renderInRouter(<TrafficPage id={WS} />);

    expect(await screen.findByTestId("traffic-to-endpoint")).toBeEnabled();
    // «Создать правку» stays disabled for the same row — the two predicates
    // are not the same.
    expect(screen.getByTestId("traffic-to-override")).toBeDisabled();
  });

  it("disables «Создать endpoint» on a suppressed row too — the login row is exactly the tempting click", async () => {
    const row = trafficRowFixture({
      id: 1,
      method: "POST",
      path: "/auth/login",
      matchedKind: "none",
      matchedId: null,
      notes: "suppressed",
    });
    route({
      ...baseRoutes({ tail: () => json(200, trafficListViewFixture({ rows: [row] })) }),
      [`GET /api/workspaces/${WS}/traffic/poll?since=1&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 1 })),
    });
    renderInRouter(<TrafficPage id={WS} />);

    const button = await screen.findByTestId("traffic-to-endpoint");
    expect(button).toBeDisabled();
    expect(screen.getByTestId("traffic-endpoint-reason")).toHaveTextContent("suppressed");
  });

  it("creates an override and invalidates the workspace, the operations list and that opKey's doc", async () => {
    const row = trafficRowFixture({ id: 1, matchedKind: "operation", matchedId: 5 });
    const opKey = "GET%20%2Fpets%2F%7BpetId%7D";
    route({
      ...baseRoutes({ tail: () => json(200, trafficListViewFixture({ rows: [row] })) }),
      [`GET /api/workspaces/${WS}/traffic/poll?since=1&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 1 })),
      [`POST /api/workspaces/${WS}/traffic/1/to-override`]: () =>
        json(200, { opKey, status: 200, revision: 4 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<TrafficPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("traffic-to-override"));

    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
      expect(keys).toContainEqual(getListWorkspaceOperationsQueryKey(WS));
      // opKey is already percent-encoded by the server; it must round-trip
      // VERBATIM into the query key, never re-encoded (§3.3).
      expect(keys).toContainEqual(getGetOperationOverrideQueryKey(WS, opKey));
    });
  });

  it("creates an endpoint from a request and invalidates the workspace and the endpoints list", async () => {
    const row = trafficRowFixture({ id: 2, matchedKind: "none", matchedId: null });
    route({
      ...baseRoutes({ tail: () => json(200, trafficListViewFixture({ rows: [row] })) }),
      [`GET /api/workspaces/${WS}/traffic/poll?since=2&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 2 })),
      [`POST /api/workspaces/${WS}/traffic/2/to-endpoint`]: () =>
        json(201, { id: 9, method: "GET", path: "/custom/from-traffic", revision: 5 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<TrafficPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("traffic-to-endpoint"));

    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
      expect(keys).toContainEqual(getListEndpointsQueryKey(WS));
    });
  });

  it("asks before clearing and does nothing if dismissed", async () => {
    const row = trafficRowFixture({ id: 1 });
    const fetchMock = vi.fn(
      route({
        ...baseRoutes({ tail: () => json(200, trafficListViewFixture({ rows: [row] })) }),
        [`GET /api/workspaces/${WS}/traffic/poll?since=1&limit=200`]: () =>
          json(200, trafficPollViewFixture({ rows: [], lastId: 1 })),
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    renderInRouter(<TrafficPage id={WS} />);

    await userEvent.click(await screen.findByTestId("traffic-clear"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByText("Отмена"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(0);
    });
    // Confirmation dismissed: the row that was there before is still there.
    expect(screen.getByTestId("traffic-row")).toBeInTheDocument();
  });

  it("clears on confirmation and does not let a poll that resolves AFTER the clear re-add the deleted row", async () => {
    const row = trafficRowFixture({ id: 1, path: "/pets/1" });
    let cleared = false;
    let deleteCalls = 0;
    let resolveStalePoll: ((res: Response) => void) | null = null;
    const stalePoll = new Promise<Response>((resolve) => {
      resolveStalePoll = resolve;
    });

    // A hand-rolled dispatcher, not http.ts's route(): this test needs ONE
    // request (the poll dispatched before the clear) to stay pending across
    // the clear resolving, which route()'s synchronous handlers cannot do.
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const method = (init?.method ?? "GET").toUpperCase();
      const url = new URL(String(input), "http://localhost");
      if (method === "DELETE" && url.pathname === `/api/workspaces/${WS}/traffic`) {
        cleared = true;
        deleteCalls += 1;
        return Promise.resolve(json(200, { deleted: 1 }));
      }
      if (method === "GET" && url.pathname === `/api/workspaces/${WS}/traffic/poll`) {
        // Exactly the poll dispatched from the PRE-clear cursor (since=1) is
        // the deferred one; anything else (the post-clear reseed at since=0)
        // answers immediately and empty, like a server with nothing new.
        if (url.searchParams.get("since") === "1" && !cleared) {
          return stalePoll;
        }
        return Promise.resolve(
          json(200, trafficPollViewFixture({ rows: [], lastId: 0, dropped: 0 })),
        );
      }
      if (method === "GET" && url.pathname === `/api/workspaces/${WS}/traffic`) {
        return Promise.resolve(
          json(
            200,
            trafficListViewFixture({
              rows: cleared ? [] : [row],
              rate1m: cleared ? 0 : 1,
              dropped: 0,
            }),
          ),
        );
      }
      return Promise.resolve(
        json(500, { error: { code: "internal", message: `unrouted ${method} ${String(input)}` } }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);

    renderInRouter(<TrafficPage id={WS} />);
    expect(await screen.findAllByTestId("traffic-row")).toHaveLength(1);

    await userEvent.click(screen.getByTestId("traffic-clear"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("traffic-clear-confirm"));

    await waitFor(() => expect(deleteCalls).toBe(1));
    await waitFor(() => expect(screen.getByTestId("traffic-empty")).toBeInTheDocument());

    // NOW let the request that was already on the wire before the clear
    // resolve, carrying the very row the clear just deleted.
    resolveStalePoll!(json(200, trafficPollViewFixture({ rows: [row], lastId: 1, dropped: 0 })));
    // Give the resolved promise's microtasks (and any react-query bookkeeping
    // around the now-orphaned generation's cache entry) a turn to run.
    await new Promise((resolve) => setTimeout(resolve, 20));

    expect(screen.queryByTestId("traffic-row")).not.toBeInTheDocument();
    expect(screen.getByTestId("traffic-empty")).toBeInTheDocument();
  });
  describe("transport (P6a D19): the stream, the fallback, and the return", () => {
    it("goes live on the stream's first frame: merges its rows, badge «живой поток», dropped from the frame", async () => {
      const ES = stubEventSource();
      route({
        ...baseRoutes({
          tail: () =>
            json(
              200,
              trafficListViewFixture({ rows: [trafficRowFixture({ id: 3, path: "/pets/3" })] }),
            ),
        }),
        [`GET /api/workspaces/${WS}/traffic/poll?since=3&limit=200`]: () =>
          json(200, trafficPollViewFixture({ rows: [], lastId: 3 })),
      });
      renderInRouter(<TrafficPage id={WS} />);

      expect(await screen.findByText("/pets/3")).toBeInTheDocument();
      // Before the first frame the badge says the poll is the feed.
      expect(screen.getByTestId("traffic-transport")).toHaveTextContent("опрос каждые 2 с");
      await waitFor(() => expect(ES.instances).toHaveLength(1));
      // Opened from the tail's cursor, never from zero.
      expect(ES.instances[0]!.url).toBe(`/api/workspaces/${WS}/traffic/stream?since=3`);

      act(() => {
        ES.instances[0]!.emitTraffic(
          trafficPollViewFixture({
            rows: [
              trafficRowFixture({ id: 4, path: "/pets/4" }),
              trafficRowFixture({ id: 5, path: "/pets/5" }),
            ],
            lastId: 5,
            dropped: 9,
          }),
        );
      });
      expect(await screen.findByText("/pets/5")).toBeInTheDocument();
      expect(screen.getByText("/pets/4")).toBeInTheDocument();
      expect(screen.getByText("/pets/3")).toBeInTheDocument();
      expect(screen.getAllByTestId("traffic-row")).toHaveLength(3);
      const badge = screen.getByTestId("traffic-transport");
      expect(badge).toHaveTextContent("живой поток");
      expect(badge).toHaveAttribute("data-transport", "live");
      expect(screen.queryByTestId("traffic-transport-reason")).not.toBeInTheDocument();
      // The frame's own dropped counter is the one shown while live.
      expect(screen.getByTestId("traffic-dropped-banner")).toHaveTextContent("9");
    });

    it("falls back on a refused handshake, keeps polling, and shows the server's own reason", async () => {
      const ES = stubEventSource();
      const capRefusal = {
        error: {
          code: "service_unavailable",
          message: "the process-wide stream connection cap is reached",
        },
      };
      let polls = 0;
      route({
        ...baseRoutes(),
        [`GET /api/workspaces/${WS}/traffic/poll?since=0&limit=200`]: () => {
          polls += 1;
          return json(
            200,
            trafficPollViewFixture({
              rows: [trafficRowFixture({ id: 8, path: "/via-poll" })],
              lastId: 8,
            }),
          );
        },
        // The probe is issued from whatever cursor the poll has reached by
        // then — 0 or 8 depending on which answers first — and both spell
        // the same refusal.
        [`GET /api/workspaces/${WS}/traffic/stream?since=0`]: () => json(503, capRefusal),
        [`GET /api/workspaces/${WS}/traffic/stream?since=8`]: () => json(503, capRefusal),
      });
      renderInRouter(<TrafficPage id={WS} />);

      await waitFor(() => expect(ES.instances).toHaveLength(1));
      act(() => {
        ES.instances[0]!.emitError();
      });

      const badge = await screen.findByTestId("traffic-transport");
      expect(badge).toHaveTextContent("опрос каждые 2 с");
      await waitFor(() =>
        expect(screen.getByTestId("traffic-transport-reason")).toHaveTextContent(
          "the process-wide stream connection cap is reached",
        ),
      );
      // The refused stream was closed by the screen, not left to the
      // browser's silent retry.
      expect(ES.instances[0]!.closed).toBe(true);
      // And the poll is the feed: its rows land.
      expect(await screen.findByText("/via-poll")).toBeInTheDocument();
      expect(polls).toBeGreaterThanOrEqual(1);
    });

    it("after being live, a dropped stream falls back and comes back only on a replacement's first frame", async () => {
      const ES = stubEventSource();
      let polls = 0;
      route({
        ...baseRoutes(),
        [`GET /api/workspaces/${WS}/traffic/poll?since=0&limit=200`]: () => {
          polls += 1;
          return json(200, trafficPollViewFixture({ rows: [], lastId: 0 }));
        },
        [`GET /api/workspaces/${WS}/traffic/poll?since=2&limit=200`]: () => {
          polls += 1;
          return json(200, trafficPollViewFixture({ rows: [], lastId: 2 }));
        },
        // A dropped connection: the probe's handshake is ACCEPTED (200), so
        // there is no reason to show — and no badge restored on its word.
        [`GET /api/workspaces/${WS}/traffic/stream?since=2`]: () => streamResponse(),
      });
      renderInRouter(<TrafficPage id={WS} streamRetryMs={30} />);

      await waitFor(() => expect(ES.instances).toHaveLength(1));
      act(() => {
        ES.instances[0]!.emitTraffic(
          trafficPollViewFixture({
            rows: [trafficRowFixture({ id: 2, path: "/live-row" })],
            lastId: 2,
          }),
        );
      });
      await waitFor(() =>
        expect(screen.getByTestId("traffic-transport")).toHaveAttribute("data-transport", "live"),
      );
      const pollsWhileLive = polls;

      act(() => {
        ES.instances[0]!.emitError();
      });
      await waitFor(() =>
        expect(screen.getByTestId("traffic-transport")).toHaveAttribute(
          "data-transport",
          "fallback",
        ),
      );
      expect(screen.queryByTestId("traffic-transport-reason")).not.toBeInTheDocument();
      // The poll resumes from the cursor the stream had advanced to.
      await waitFor(() => expect(polls).toBeGreaterThan(pollsWhileLive));

      // A replacement stream is opened on the retry interval, from that
      // same cursor; the badge stays on the fallback until IT delivers.
      await waitFor(() => expect(ES.instances).toHaveLength(2));
      expect(ES.instances[1]!.url).toBe(`/api/workspaces/${WS}/traffic/stream?since=2`);
      expect(screen.getByTestId("traffic-transport")).toHaveAttribute("data-transport", "fallback");
      act(() => {
        ES.instances[1]!.emitTraffic(
          trafficPollViewFixture({
            rows: [trafficRowFixture({ id: 3, path: "/back-live" })],
            lastId: 3,
          }),
        );
      });
      await waitFor(() =>
        expect(screen.getByTestId("traffic-transport")).toHaveAttribute("data-transport", "live"),
      );
      expect(await screen.findByText("/back-live")).toBeInTheDocument();
      expect(screen.getByText("/live-row")).toBeInTheDocument();
    });

    it("a probe answered 401 goes where every other dead session goes, and shows no reason", async () => {
      const ES = stubEventSource();
      const unauthorized = vi.fn();
      setUnauthorizedHandler(unauthorized);
      route({
        ...baseRoutes(),
        [`GET /api/workspaces/${WS}/traffic/poll?since=0&limit=200`]: () =>
          json(200, trafficPollViewFixture({ rows: [], lastId: 0 })),
        [`GET /api/workspaces/${WS}/traffic/stream?since=0`]: () =>
          json(401, { error: { code: "unauthorized", message: "authentication required" } }),
      });
      renderInRouter(<TrafficPage id={WS} />);

      await waitFor(() => expect(ES.instances).toHaveLength(1));
      act(() => {
        ES.instances[0]!.emitError();
      });
      await waitFor(() => expect(unauthorized).toHaveBeenCalledTimes(1));
      expect(screen.queryByTestId("traffic-transport-reason")).not.toBeInTheDocument();
    });

    it("a clear closes the stream and reopens it from the new tail's cursor", async () => {
      const ES = stubEventSource();
      let tails = 0;
      route({
        ...baseRoutes({
          tail: () => {
            tails += 1;
            return json(
              200,
              trafficListViewFixture({
                rows: tails === 1 ? [trafficRowFixture({ id: 6, path: "/old" })] : [],
              }),
            );
          },
        }),
        [`GET /api/workspaces/${WS}/traffic/poll?since=6&limit=200`]: () =>
          json(200, trafficPollViewFixture({ rows: [], lastId: 6 })),
        [`GET /api/workspaces/${WS}/traffic/poll?since=0&limit=200`]: () =>
          json(200, trafficPollViewFixture({ rows: [], lastId: 0 })),
        [`DELETE /api/workspaces/${WS}/traffic`]: () => json(200, { deleted: 1 }),
      });
      renderInRouter(<TrafficPage id={WS} />);

      expect(await screen.findByText("/old")).toBeInTheDocument();
      await waitFor(() => expect(ES.instances).toHaveLength(1));
      expect(ES.instances[0]!.url).toBe(`/api/workspaces/${WS}/traffic/stream?since=6`);

      await userEvent.click(screen.getByTestId("traffic-clear"));
      await userEvent.click(await screen.findByTestId("traffic-clear-confirm"));

      expect(await screen.findByTestId("traffic-empty")).toBeInTheDocument();
      await waitFor(() => expect(ES.instances).toHaveLength(2));
      expect(ES.instances[0]!.closed).toBe(true);
      expect(ES.instances[1]!.url).toBe(`/api/workspaces/${WS}/traffic/stream?since=0`);
    });
  });

  // A21 (U4): the «Совпадение» cell is a link into the editor that answered.
  it("links an operation row to the operations tab by opId and an endpoint row by endpointId", async () => {
    route({
      ...baseRoutes({
        tail: () =>
          json(
            200,
            trafficListViewFixture({
              rows: [
                trafficRowFixture({
                  id: 1,
                  path: "/pets/1",
                  matchedKind: "operation",
                  matchedId: 5,
                }),
                trafficRowFixture({
                  id: 2,
                  path: "/custom",
                  matchedKind: "endpoint",
                  matchedId: 9,
                }),
                trafficRowFixture({
                  id: 3,
                  path: "/nowhere",
                  matchedKind: "none",
                  matchedId: null,
                }),
              ],
            }),
          ),
      }),
      [`GET /api/workspaces/${WS}/traffic/poll?since=3&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 3 })),
    });
    renderInRouter(<TrafficPage id={WS} />);
    const links = await screen.findAllByTestId("traffic-match-link");
    // Newest first on screen; the set is what matters here.
    expect(links.map((a) => a.getAttribute("href")).sort()).toEqual(
      [`/workspaces/${WS}/operations?opId=5`, `/workspaces/${WS}/endpoints?endpointId=9`].sort(),
    );
    expect(links.some((a) => a.textContent === "операция #5")).toBe(true);
    // The click goes through the router (the memory router's catch-all
    // renders for the operations route); the ?opId= resolution itself is
    // OperationsPage's test.
    await userEvent.click(links.find((a) => a.textContent === "операция #5")!);
    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
  });

  // A21 (U5): the empty state names the mock's address and the overview; the
  // filters narrow the rows already held.
  it("names the address in the empty state and filters rows by path, method and status class", async () => {
    route({
      ...baseRoutes({
        tail: () =>
          json(
            200,
            trafficListViewFixture({
              rows: [
                trafficRowFixture({ id: 1, method: "GET", path: "/pets/1", status: 200 }),
                trafficRowFixture({ id: 2, method: "POST", path: "/orders", status: 500 }),
              ],
            }),
          ),
      }),
      [`GET /api/workspaces/${WS}/traffic/poll?since=2&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 2 })),
    });
    renderInRouter(<TrafficPage id={WS} />);
    await screen.findByText("/orders");
    expect(screen.getAllByTestId("traffic-row")).toHaveLength(2);
    await userEvent.selectOptions(screen.getByTestId("traffic-filter-status"), "5");
    expect(screen.getAllByTestId("traffic-row")).toHaveLength(1);
    expect(screen.getByTestId("traffic-filter-count")).toHaveTextContent("показано 1 из 2");
    await userEvent.selectOptions(screen.getByTestId("traffic-filter-status"), "");
    await userEvent.type(screen.getByTestId("traffic-filter-path"), "pets");
    expect(screen.getAllByTestId("traffic-row")).toHaveLength(1);
    expect(screen.getByText("/pets/1")).toBeInTheDocument();
  });

  it("points the empty state at the workspace address and the overview", async () => {
    route({
      ...baseRoutes(),
      [`GET /api/workspaces/${WS}`]: () =>
        json(200, workspaceFixture({ id: WS, url: "http://alex.mock.local" })),
    });
    renderInRouter(<TrafficPage id={WS} />);
    const empty = await screen.findByTestId("traffic-empty");
    await waitFor(() => expect(empty).toHaveTextContent("http://alex.mock.local"));
    expect(screen.getByTestId("traffic-empty-overview-link")).toHaveAttribute(
      "href",
      `/workspaces/${WS}`,
    );
  });

  it("marks a truncated row (A21, G14)", async () => {
    route({
      ...baseRoutes({
        tail: () =>
          json(
            200,
            trafficListViewFixture({
              rows: [trafficRowFixture({ id: 1, path: "/big", truncated: true })],
            }),
          ),
      }),
      [`GET /api/workspaces/${WS}/traffic/poll?since=1&limit=200`]: () =>
        json(200, trafficPollViewFixture({ rows: [], lastId: 1 })),
    });
    renderInRouter(<TrafficPage id={WS} />);
    expect(await screen.findByTestId("traffic-truncated")).toHaveTextContent("обрезано");
  });
});

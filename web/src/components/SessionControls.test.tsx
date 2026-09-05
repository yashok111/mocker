import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SessionControls } from "./SessionControls";
import { makeQueryClient, renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { directiveFixture, sessionListViewFixture } from "@/test/fixtures";
import { getListSessionDirectivesQueryKey } from "@/api/generated/session/session.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";

const WS = 7;
const LIST = `GET /api/workspaces/${WS}/session`;
const SET = `POST /api/workspaces/${WS}/session`;
const CLEAR = `DELETE /api/workspaces/${WS}/session`;

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("SessionControls", () => {
  it("renders its outer marker before the directive list answers", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<SessionControls id={WS} target={null} />);

    expect(await screen.findByTestId("session-controls")).toBeInTheDocument();
  });

  it("says these directives are RAM-only and never touch the workspace revision", async () => {
    route({ [LIST]: () => json(200, sessionListViewFixture({ directives: [] })) });
    renderInRouter(<SessionControls id={WS} target={null} />);

    const controls = await screen.findByTestId("session-controls");
    expect(controls).toHaveTextContent("памяти");
    expect(controls).toHaveTextContent("не двигают ревизию");
  });

  it("shows '*' as the target when none is selected, and the operation when one is", async () => {
    route({ [LIST]: () => json(200, sessionListViewFixture({ directives: [] })) });
    // Two independent mounts rather than a rerender: renderInRouter bakes
    // `ui` into the memory route's component closure at router-creation
    // time, so rerender()ing the outer tree would not actually push new
    // props through the router — it would just swap out the whole
    // QueryClientProvider/RouterProvider wrapper this component needs.
    const withoutTarget = renderInRouter(<SessionControls id={WS} target={null} />);
    expect(
      await within(withoutTarget.container).findByTestId("session-controls"),
    ).toHaveTextContent("весь воркспейс");

    const withTarget = renderInRouter(
      <SessionControls id={WS} target={{ method: "GET", path: "/pets/{petId}" }} />,
    );
    expect(await within(withTarget.container).findByTestId("session-controls")).toHaveTextContent(
      "GET /pets/{petId}",
    );
  });

  it("lists directives currently in force, including remaining fail count", async () => {
    route({
      [LIST]: () =>
        json(
          200,
          sessionListViewFixture({
            directives: [
              directiveFixture({ target: "*", action: "fail", status: 500, n: 2, once: false }),
              directiveFixture({
                target: { method: "GET", path: "/pets" },
                action: "status",
                status: 503,
              }),
            ],
          }),
        ),
    });
    renderInRouter(<SessionControls id={WS} target={null} />);

    const list = await screen.findByTestId("session-directives-list");
    expect(list).toHaveTextContent("сломать 500");
    expect(list).toHaveTextContent("осталось 2");
    expect(list).toHaveTextContent("отвечать 503");
  });

  it("lists a delay and a pause directive the server returns, per §A's wire shape", async () => {
    route({
      [LIST]: () =>
        json(
          200,
          sessionListViewFixture({
            directives: [
              // §A: status is ABSENT on delay|pause — the fixture mirrors that
              // rather than leaving the "status" default (500) on the wire.
              directiveFixture({
                target: { method: "GET", path: "/widgets" },
                action: "delay",
                status: undefined,
                ms: 300,
              }),
              directiveFixture({ target: "*", action: "pause", status: undefined }),
            ],
          }),
        ),
    });
    renderInRouter(<SessionControls id={WS} target={null} />);

    const list = await screen.findByTestId("session-directives-list");
    expect(list).toHaveTextContent("задержка 300 мс");
    expect(list).toHaveTextContent("пауза до очистки");
  });

  it("sends target '*' when the prop is null, and asks to fail exactly once", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, sessionListViewFixture({ directives: [] })),
      [SET]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<SessionControls id={WS} target={null} />);

    await screen.findByTestId("session-controls");
    await userEvent.click(screen.getByTestId("session-fail-next"));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
    });
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    const sent: unknown = JSON.parse(String(posts[0]?.[1]?.body));
    expect(sent).toEqual({ target: "*", action: "fail", status: 500, once: true });
  });

  it("sends the operation target, not '*', when one is selected", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, sessionListViewFixture({ directives: [] })),
      [SET]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<SessionControls id={WS} target={{ method: "GET", path: "/pets/{petId}" }} />);

    await screen.findByTestId("session-controls");
    await userEvent.click(screen.getByTestId("session-force-503"));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
    });
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    const sent: unknown = JSON.parse(String(posts[0]?.[1]?.body));
    expect(sent).toEqual({
      target: { method: "GET", path: "/pets/{petId}" },
      action: "status",
      status: 503,
    });
  });

  it("sends a delay directive with target '*' and the ms the operator set — no status field", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, sessionListViewFixture({ directives: [] })),
      [SET]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<SessionControls id={WS} target={null} />);

    await screen.findByTestId("session-controls");
    await userEvent.click(screen.getByTestId("session-delay"));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
    });
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    const sent: unknown = JSON.parse(String(posts[0]?.[1]?.body));
    // §A: ms is REQUIRED for delay and status must be ABSENT — the 300 here
    // is the control's own default, exercising the "operator changed nothing"
    // path the same way the fail-next test does for its default status.
    expect(sent).toEqual({ target: "*", action: "delay", ms: 300 });
  });

  it("sends a pause directive on the selected operation target — no status, no ms", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, sessionListViewFixture({ directives: [] })),
      [SET]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    renderInRouter(<SessionControls id={WS} target={{ method: "GET", path: "/pets/{petId}" }} />);

    await screen.findByTestId("session-controls");
    await userEvent.click(screen.getByTestId("session-pause"));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
    });
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    const sent: unknown = JSON.parse(String(posts[0]?.[1]?.body));
    // §A: pause rejects any non-zero status AND any non-zero ms — both must
    // be absent from the wire body, not merely zero.
    expect(sent).toEqual({ target: { method: "GET", path: "/pets/{petId}" }, action: "pause" });
  });

  it("invalidates ONLY the session directive list, never the workspace query, on set", async () => {
    route({
      [LIST]: () => json(200, sessionListViewFixture({ directives: [] })),
      [SET]: () => json(200, sessionListViewFixture({ directives: [] })),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<SessionControls id={WS} target={null} />, { queryClient });

    await screen.findByTestId("session-controls");
    await userEvent.click(screen.getByTestId("session-force-503"));

    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListSessionDirectivesQueryKey(WS));
    });
    // §3.9: live-state mutations must NOT bump the workspace query — a
    // directive never moves the workspace revision.
    const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
    expect(keys).not.toContainEqual(getGetWorkspaceQueryKey(WS));
  });

  it("clears all directives only after confirming, and invalidates the list", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, sessionListViewFixture({ directives: [directiveFixture()] })),
      [CLEAR]: () => json(200, { cleared: 1 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<SessionControls id={WS} target={null} />, { queryClient });

    await screen.findByTestId("session-directives-list");
    await userEvent.click(screen.getByTestId("session-clear-all"));

    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("session-clear-confirm"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(1);
    });
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListSessionDirectivesQueryKey(WS));
    });
  });

  it("does nothing on cancel", async () => {
    const fetchMock = route({
      [LIST]: () => json(200, sessionListViewFixture({ directives: [directiveFixture()] })),
    });
    renderInRouter(<SessionControls id={WS} target={null} />);

    await screen.findByTestId("session-directives-list");
    await userEvent.click(screen.getByTestId("session-clear-all"));

    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByText("Отмена"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(0);
    });
  });

  // A21 (G8): any status, N requests, one ✕ per directive.
  it("sends n for a count above one, any forced status, and clears one directive by target+action", async () => {
    const fetchMock = route({
      [LIST]: () =>
        json(
          200,
          sessionListViewFixture({
            directives: [
              directiveFixture({
                target: { method: "GET", path: "/pets" },
                action: "delay",
                ms: 300,
              }),
            ],
          }),
        ),
      [SET]: () => json(200, sessionListViewFixture({ directives: [] })),
      [CLEAR]: () => json(200, { cleared: 1 }),
    });
    renderInRouter(<SessionControls id={WS} target={null} />);
    await screen.findByTestId("session-directives-list");

    const count = screen.getByTestId("session-fail-count");
    await userEvent.clear(count);
    await userEvent.type(count, "3");
    await userEvent.click(screen.getByTestId("session-fail-next"));
    const force = screen.getByTestId("session-force-status");
    await userEvent.clear(force);
    await userEvent.type(force, "418");
    await userEvent.click(screen.getByTestId("session-force-503"));
    await userEvent.click(screen.getByTestId("session-directive-clear-delay-GET /pets"));

    await waitFor(() => {
      const posts = fetchMock.mock.calls
        .filter(([, init]) => init?.method === "POST")
        .map(([, init]) => JSON.parse(String(init?.body)) as unknown);
      expect(posts).toEqual([
        { target: "*", action: "fail", status: 500, n: 3 },
        { target: "*", action: "status", status: 418 },
      ]);
      const del = fetchMock.mock.calls.find(([, init]) => init?.method === "DELETE");
      expect(JSON.parse(String(del?.[1]?.body))).toEqual({
        target: { method: "GET", path: "/pets" },
        action: "delay",
      });
    });
  });
});

import { afterEach, expect, it, vi } from "vitest";
import { act, fireEvent, screen } from "@testing-library/react";
import { renderInRouter } from "@/test/render";
import { json } from "@/test/http";
import { exactIDs, isReplayList } from "@/test/backendExact";
import { BackendProjectPage } from "./BackendProjectPage";
vi.mock("./BackendFlowGraph", () => ({ BackendFlowGraph: () => null }));
vi.mock("./BackendDatabaseGraph", () => ({ BackendDatabaseGraph: () => null }));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
// The replay panel sits in a <details> on every project page and is mounted
// eagerly (it restores a pending attempt from localStorage; the diagram's
// prepare button moves focus into it), so its four lists are read once on
// mount whether or not the operator ever opens it. Its runs list used to
// re-read every 2 s from then on, closed or open: one request every two
// seconds for as long as any project page stayed on screen.
const runsReads = (fetcher: { mock: { calls: unknown[][] } }) =>
  fetcher.mock.calls.filter(([input]) => String(input).endsWith("/replay/runs")).length;
async function elapse(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}
it("re-reads the replay runs list only while its panel is open", async () => {
  // Installed before the mount so the interval React Query schedules is a
  // fake one; shouldAdvanceTime keeps findBy*'s own polling alive.
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const fetcher = vi.fn(async (input: RequestInfo | URL) =>
    isReplayList(String(input)) ? json(200, []) : json(500, { error: { message: "unrouted" } }),
  );
  vi.stubGlobal("fetch", fetcher);
  renderInRouter(
    <BackendProjectPage
      projectId={exactIDs.project}
      sourcePin={{ changeProposalId: exactIDs.project }}
    />,
  );
  await screen.findByText("Укажите точные идентификаторы предложения и черновика.");
  await elapse(0);
  // The eager first load stays.
  expect(runsReads(fetcher)).toBe(1);

  await elapse(2000 * 4);
  expect(runsReads(fetcher)).toBe(1);

  const summary = screen.getByText("Orders replay и тестовые профили");
  fireEvent.click(summary);
  expect(summary.closest("details")).toHaveAttribute("open");
  await elapse(2000 * 3);
  const whileOpen = runsReads(fetcher);
  expect(whileOpen).toBeGreaterThanOrEqual(3);

  fireEvent.click(summary);
  expect(summary.closest("details")).not.toHaveAttribute("open");
  await elapse(2000 * 4);
  expect(runsReads(fetcher)).toBe(whileOpen);
});

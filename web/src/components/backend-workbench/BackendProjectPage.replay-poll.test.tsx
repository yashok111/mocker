import { afterEach, expect, it, vi } from "vitest";
import { act, screen } from "@testing-library/react";
import { renderInRouter } from "@/test/render";
import { BackendProjectPage } from "./BackendProjectPage";
import { projectId, revisionId, workspaceHTTP } from "./explorer/testFixtures";

vi.mock("./explorer/ExploreCanvas", () => ({ ExploreCanvas: () => null }));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  sessionStorage.clear();
  localStorage.clear();
});

// Exploration no longer mounts the replay authoring panel. Keep the original
// regression guard against background replay reads on an idle project page.
it("does not load or poll replay runs while exploring a project", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const fetcher = workspaceHTTP();
  renderInRouter(<BackendProjectPage projectId={projectId} sourcePin={{ revisionId }} />);
  expect(await screen.findByRole("heading", { name: "Обзор из исходников" })).toBeVisible();
  const replayReads = () =>
    fetcher.mock.calls.filter(([input]) => String(input).includes("/replay/"));
  expect(replayReads()).toHaveLength(0);

  await act(async () => {
    await vi.advanceTimersByTimeAsync(8000);
  });
  expect(replayReads()).toHaveLength(0);
  expect(screen.queryByText("Orders replay и тестовые профили")).not.toBeInTheDocument();
});

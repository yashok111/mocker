import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendChangeEditor } from "./BackendChangeEditor";
import { changeTestDetail, changeTestID } from "./backendChangeTestFixtures";
vi.mock("./BackendEffectiveViews", () => ({ BackendEffectiveViews: () => null }));
vi.mock("./BackendChangeRebase", () => ({ BackendChangeRebase: () => null }));
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
it("reaches archive and unarchive from current editor, locks edits, retains immutable draft", async () => {
  const detail = changeTestDetail(),
    posts: Record<string, unknown>[] = [];
  let version = 1;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST" && String(input).endsWith("/lifecycle")) {
        const body = JSON.parse(String(init.body));
        posts.push(body);
        return new Response(
          JSON.stringify({
            proposal: {
              ...detail.proposal,
              version: ++version,
              status: body.action === "archive" ? "archived" : "draft",
            },
            revision: detail.revision,
          }),
        );
      }
      return new Response(JSON.stringify({ items: [], nextCursor: "" }));
    }),
  );
  renderWithProviders(
    <BackendChangeEditor projectId={changeTestID} detail={detail} onSaved={() => {}} />,
  );
  const u = userEvent.setup();
  await u.click(screen.getByRole("button", { name: "Архивировать предложение" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Вернуть в черновик" })).toBeEnabled(),
  );
  expect(screen.queryByRole("button", { name: "Добавить команду" })).not.toBeInTheDocument();
  await u.click(screen.getByRole("button", { name: "Вернуть в черновик" }));
  await waitFor(() => expect(posts).toHaveLength(2));
  expect(posts.map((p) => [p.action, p.expectedVersion, p.proposalRevisionId])).toEqual([
    ["archive", 1, detail.revision.id],
    ["unarchive", 2, detail.revision.id],
  ]);
});

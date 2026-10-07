import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderInRouter } from "@/test/render";
import { BackendProjectPage } from "./BackendProjectPage";
import { projectId, revisionId, newerId, workspaceHTTP } from "./explorer/testFixtures";
import type { BackendWorkspaceSearch } from "./backendWorkspaceSearch";
vi.mock("./explorer/ExploreCanvas", () => ({ ExploreCanvas: () => <div>Карта</div> }));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  sessionStorage.clear();
});
it("allows a read-only workspace to change the exact target without prompting", async () => {
  const fetcher = workspaceHTTP();
  const confirm = vi.fn();
  vi.stubGlobal("confirm", confirm);
  function Harness() {
    const [pin, setPin] = useState<BackendWorkspaceSearch>({ revisionId });
    return (
      <>
        <button
          onClick={() => setPin({ changeProposalId: projectId, proposalRevisionId: newerId })}
        >
          Открыть предложение
        </button>
        <BackendProjectPage projectId={projectId} sourcePin={pin} onSourceNavigate={setPin} />
      </>
    );
  }
  renderInRouter(<Harness />);
  await screen.findByText("Карта");
  await userEvent.click(screen.getByRole("button", { name: "Открыть предложение" }));
  await screen.findByRole("button", { name: "Предложение" });
  expect(confirm).not.toHaveBeenCalled();
  expect(
    fetcher.mock.calls
      .filter(([, init]) => init?.method === "POST")
      .every(([url]) => String(url).endsWith("/explore/query")),
  ).toBe(true);
});

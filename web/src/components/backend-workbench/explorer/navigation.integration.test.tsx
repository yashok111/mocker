import { afterEach, expect, it } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { renderInRouter } from "@/test/render";
import { useExplorerNavigation } from "./navigation";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { useEffect, useState } from "react";

afterEach(() => {
  cleanup();
  sessionStorage.clear();
});
it("restores the most recent imperative camera update after switching tabs", async () => {
  function Journey() {
    const location = useLocation();
    const navigate = useNavigate();
    // Route matches can commit one render after the router's location subscription.
    const [search, setSearch] = useState(location.search as BackendWorkspaceSearch);
    // Deliberately model the router/route-props commit gap that caused the regression.
    // oxlint-disable-next-line react/set-state-in-effect
    useEffect(() => setSearch(location.search as BackendWorkspaceSearch), [location.search]);
    const nav = useExplorerNavigation(
      "p",
      "r",
      search,
      (next, replace) => void navigate({ search: next as never, replace }),
      { revisionId: "r" },
    );
    return (
      <>
        <output aria-label="Camera">{JSON.stringify(nav.presentation.camera ?? null)}</output>
        <button onClick={() => nav.updateCamera({ x: 20, y: 30, zoom: 1.2 })}>Zoom</button>
        <button onClick={() => nav.switchView("data")}>Data</button>
        <button onClick={() => nav.switchView("structure")}>Structure</button>
        <p>{search.wbView ?? "structure"}</p>
      </>
    );
  }
  renderInRouter(<Journey />);
  await screen.findByText("structure");
  await userEvent.click(screen.getByRole("button", { name: "Zoom" }));
  await userEvent.click(screen.getByRole("button", { name: "Data" }));
  await screen.findByText("data");
  await userEvent.click(screen.getByRole("button", { name: "Structure" }));
  await waitFor(() =>
    expect(screen.getByLabelText("Camera")).toHaveTextContent('{"x":20,"y":30,"zoom":1.2}'),
  );
});

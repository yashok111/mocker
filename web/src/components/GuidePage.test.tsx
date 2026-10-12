import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { GuidePage, guideForServer } from "./GuidePage";
import guideSource from "../../../docs/USER-GUIDE.md?raw";
import { renderWithProviders } from "@/test/render";

// The guide makes no request, so there is nothing to stub: the assertions
// are that the markdown really compiled in, that the table of contents is
// built from its second-level headings, and that each entry points at an
// id the body actually carries — the one thing a broken slug would lose.

describe("GuidePage", () => {
  it("renders the manual with a table of contents wired to heading ids", () => {
    renderWithProviders(<GuidePage />);

    expect(screen.getByTestId("guide-page")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 1, name: "Руководство по mocker" }),
    ).toBeInTheDocument();

    const toc = within(screen.getByTestId("guide-toc"));
    const links = toc.getAllByRole("link");
    expect(links.length).toBeGreaterThanOrEqual(5);

    const body = screen.getByTestId("guide-body");
    for (const link of links) {
      const id = link.getAttribute("href")?.slice(1);
      expect(id, `link ${link.textContent}`).toBeTruthy();
      expect(body.querySelector(`h2[id="${id}"]`), `heading #${id}`).not.toBeNull();
    }
  });

  it("names the agent's own way in", () => {
    renderWithProviders(<GuidePage />);
    expect(screen.getByTestId("guide-page")).toHaveTextContent("get_guide");
  });

  // The Backend Workbench is frozen by default (2026-10-12): its fenced
  // passages of USER-GUIDE.md are left out unless the server enables it.
  it("leaves the Backend Workbench passages out unless the server enables it", () => {
    const frozen = guideForServer(guideSource, false);
    const enabled = guideForServer(guideSource, true);
    for (const text of [frozen, enabled]) {
      expect(text).not.toContain("backend-workbench -->");
    }
    for (const marker of ["Backend Workbench", "mocker-backend-import", "query_backend_lineage"]) {
      expect(frozen).not.toContain(marker);
      expect(enabled).toContain(marker);
    }
    for (const kept of [
      "## 7. Агент и MCP",
      "--skill mocker -a claude-code",
      "Разрушающие действия",
      "## 10. Чего mocker не делает",
    ]) {
      expect(frozen).toContain(kept);
    }

    renderWithProviders(<GuidePage />);
    expect(screen.getByTestId("guide-body")).not.toHaveTextContent("Backend Workbench");
  });
});

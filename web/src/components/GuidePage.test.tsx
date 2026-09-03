import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { GuidePage } from "./GuidePage";
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
});

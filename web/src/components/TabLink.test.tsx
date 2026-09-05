import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TabLink, tabPath } from "./TabLink";
import { renderInRouter } from "@/test/render";

// A21 (U3): one link pattern for every «на вкладке X» in prose — an href
// for the browser and the router's own navigate for the click.
describe("TabLink", () => {
  it("builds the tab paths, the overview being the bare workspace route", () => {
    expect(tabPath(7, "overview")).toBe("/workspaces/7");
    expect(tabPath(7, "resources")).toBe("/workspaces/7/resources");
  });

  it("renders an href and navigates through the router on click", async () => {
    renderInRouter(
      <TabLink id={7} tab="scenarios" testId="t">
        «Сценарии»
      </TabLink>,
    );
    const link = await screen.findByTestId("t");
    expect(link).toHaveAttribute("href", "/workspaces/7/scenarios");
    await userEvent.click(link);
    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
  });
});

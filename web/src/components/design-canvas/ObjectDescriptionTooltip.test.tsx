import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "@/test/render";
import { ObjectDescriptionTooltip } from "./ObjectDescriptionTooltip";

const bounds = { left: 40, top: 50, width: 160, height: 52 };

describe("object description tooltip", () => {
  it("renders the description as visible plain text and removes it on unmount", async () => {
    const view = renderWithProviders(
      <ObjectDescriptionTooltip
        bounds={bounds}
        description={"Сервис <API> & данные\nВторая строка"}
      />,
    );
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toBeVisible();
    expect(tooltip.textContent).toBe("Сервис <API> & данные\nВторая строка");
    expect(tooltip.querySelector("api")).toBeNull();
    view.unmount();
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  });

  it("does not show an empty description", () => {
    renderWithProviders(<ObjectDescriptionTooltip bounds={bounds} description="   " />);
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  });
});

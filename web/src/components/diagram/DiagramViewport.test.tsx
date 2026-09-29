import type { Graph } from "@antv/x6";
import { createRef } from "react";
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import DiagramViewport from "./DiagramViewport";

describe("DiagramViewport", () => {
  it("allows explicit fitting repeatedly after manual zoom", async () => {
    let scale = 1.5;
    const graphRef = createRef<Graph>();
    graphRef.current = {
      zoomToFit: () => {
        scale = 1;
      },
    } as unknown as Graph;
    renderWithProviders(
      <DiagramViewport hostRef={createRef<HTMLElement>()} graphRef={graphRef} zoomLabel="карту" />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Вместить" }));
    expect(scale).toBe(1);
    scale = 0.4;
    await userEvent.click(screen.getByRole("button", { name: "Вместить" }));
    expect(scale).toBe(1);
  });
  it("keeps controls accessible when the graph has an alternative accessible list", () => {
    const hostRef = createRef<HTMLElement>();
    renderWithProviders(
      <DiagramViewport
        hostRef={hostRef}
        graphRef={createRef<Graph>()}
        ariaHidden
        zoomLabel="карту"
      />,
    );

    expect(hostRef.current).toHaveAttribute("aria-hidden", "true");
    expect(screen.queryByRole("figure")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Увеличить карту" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Уменьшить карту" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Вместить" })).toBeVisible();
  });

  it("exposes a named figure for graphs with an accessible description", () => {
    const hostRef = createRef<HTMLElement>();
    renderWithProviders(
      <DiagramViewport
        hostRef={hostRef}
        graphRef={createRef<Graph>()}
        ariaLabel="Модель схем. Выбор схем доступен в списке."
        zoomLabel="модель"
      />,
    );

    expect(screen.getByRole("figure", { name: /Модель схем/ })).toBe(hostRef.current);
  });
});

import type { Graph } from "@antv/x6";
import { describe, expect, it, vi } from "vitest";
import { installCanvasWheelZoom, normalizeWheelDelta, wheelZoomScale } from "./canvasZoom";

describe("canvas wheel zoom", () => {
  it("makes a small trackpad gesture noticeable without a large jump", () => {
    let scale = 0.72;
    for (let i = 0; i < 6; i++) scale = wheelZoomScale(scale, -1);
    expect(scale).toBeGreaterThan(0.72 * 1.04);
    expect(scale).toBeLessThan(0.72 * 1.06);
  });

  it("responds to the gesture magnitude and reverses small movements", () => {
    expect(wheelZoomScale(1, -10)).toBeGreaterThan(wheelZoomScale(1, -1));
    expect(wheelZoomScale(wheelZoomScale(1, -10), 10)).toBeCloseTo(1, 12);
    expect(wheelZoomScale(1, 0)).toBe(1);
  });

  it("makes a wheel notch responsive while capping large bursts at twenty percent", () => {
    expect(wheelZoomScale(1, -100)).toBeGreaterThan(1.15);
    expect(wheelZoomScale(1, -2000)).toBeLessThan(1.2);
    expect(wheelZoomScale(1, 2000)).toBeGreaterThan(0.8);
  });

  it("respects the available scale range", () => {
    expect(wheelZoomScale(1.99, -50)).toBe(2);
    expect(wheelZoomScale(0.36, 50)).toBe(0.35);
  });

  it("normalizes pixel, line and page units", () => {
    expect(normalizeWheelDelta(16, 0, 800)).toBe(normalizeWheelDelta(1, 1, 800));
    expect(normalizeWheelDelta(800, 0, 800)).toBe(normalizeWheelDelta(1, 2, 800));
  });

  it.each([false, true])(
    "zooms wheel events with ctrlKey=%s and releases the handler on cleanup",
    async (ctrlKey) => {
      const container = document.createElement("div");
      let scale = 1;
      const graph = {
        zoom: vi.fn((next?: number) => {
          if (next !== undefined) scale = next;
          return scale;
        }),
        clientToGraph: vi.fn((point: { x: number; y: number }) => point),
      };
      const cleanup = installCanvasWheelZoom(graph as unknown as Graph, container);
      try {
        const event = new WheelEvent("wheel", {
          deltaY: -10,
          ctrlKey,
          clientX: 120,
          clientY: 180,
          cancelable: true,
        });
        // happy-dom's WheelEvent omits the MouseEvent fields.
        Object.defineProperties(event, {
          ctrlKey: { value: ctrlKey },
          clientX: { value: 120 },
          clientY: { value: 180 },
        });
        container.dispatchEvent(event);
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        expect(event.defaultPrevented).toBe(true);
        expect(scale).toBeGreaterThan(1);
        expect(graph.zoom).toHaveBeenLastCalledWith(scale, {
          absolute: true,
          center: { x: 120, y: 180 },
        });
      } finally {
        cleanup();
      }
      const afterCleanup = scale;
      container.dispatchEvent(new WheelEvent("wheel", { deltaY: -100, ctrlKey }));
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      expect(scale).toBe(afterCleanup);
    },
  );
});

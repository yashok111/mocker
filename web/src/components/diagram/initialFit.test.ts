import { describe, expect, it } from "vitest";
import { createInitialFit } from "./initialFit";

function scene() {
  let count = 0;
  let camera = { scale: 0.7, x: 48, y: 19 };
  let fits = 0;
  const host = { clientWidth: 600, clientHeight: 400 };
  const graph = {
    getNodes: () => Array.from({ length: count }),
    zoomToFit: () => {
      fits++;
      camera = { scale: 1, x: 0, y: 0 };
    },
  };
  return {
    host,
    fit: createInitialFit(graph, host, 36),
    load: () => {
      count = 2;
    },
    move: () => {
      camera = { scale: 1.4, x: -90, y: 62 };
    },
    camera: () => camera,
    fits: () => fits,
  };
}

describe("initial diagram fit", () => {
  it("waits for loaded nodes before consuming the first fit", () => {
    const s = scene();
    expect(s.fit()).toBe(false);
    expect(s.fits()).toBe(0);
    s.load();
    expect(s.fit()).toBe(true);
    expect(s.fits()).toBe(1);
  });

  it.each(["clientWidth", "clientHeight"] as const)("waits for positive %s", (dimension) => {
    const s = scene();
    s.load();
    s.host[dimension] = 0;
    expect(s.fit()).toBe(false);
    s.host[dimension] = 300;
    expect(s.fit()).toBe(true);
    expect(s.fits()).toBe(1);
  });

  it("preserves manual zoom and pan on later resizes and model updates", () => {
    const s = scene();
    s.load();
    s.fit();
    s.move();
    s.host.clientWidth = 320;
    s.host.clientHeight = 200;
    s.fit();
    s.load();
    s.fit();
    expect(s.camera()).toEqual({ scale: 1.4, x: -90, y: 62 });
    expect(s.fits()).toBe(1);
  });

  it("fits each selected diagram, including switching back after a hidden diagram", () => {
    const s = scene();
    s.load();
    s.fit("a");
    s.move();
    expect(s.fit("b")).toBe(true);
    s.host.clientWidth = 0;
    expect(s.fit("c")).toBe(false);
    s.host.clientWidth = 600;
    expect(s.fit("b")).toBe(true);
    expect(s.fits()).toBe(3);
  });
});

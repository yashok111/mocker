import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { exampleCanvas } from "./canvasModel";
import { exportSequenceImage } from "./sequenceImageExport";

const state = vi.hoisted(() => ({
  cells: [] as Array<{ id: string; attrs?: Record<string, unknown> }>,
  box: { x: 0, y: 0, width: 700, height: 400 },
  svg: vi.fn(),
  png: vi.fn(),
  dispose: vi.fn(),
}));
vi.mock("@antv/x6", () => ({
  Export: class {},
  Graph: class {
    use() {}
    createNode(value: unknown) {
      return value;
    }
    createEdge(value: unknown) {
      return value;
    }
    resetCells(cells: typeof state.cells) {
      state.cells = cells;
    }
    getContentBBox() {
      return state.box;
    }
    toSVGAsync = state.svg;
    toPNGAsync = state.png;
    dispose = state.dispose;
  },
}));

beforeEach(() => {
  vi.clearAllMocks();
  state.box = { x: 0, y: 0, width: 700, height: 400 };
  state.svg.mockResolvedValue('<svg xmlns="http://www.w3.org/2000/svg"><text>Клиент</text></svg>');
  state.png.mockResolvedValue("data:image/png;base64,iVBORw==");
});
afterEach(() => vi.useRealTimers());

describe("exportSequenceImage", () => {
  it("exports the snapshot without editor overlays and releases its graph", async () => {
    const doc = exampleCanvas();
    const blob = await exportSequenceImage(doc, "svg");
    expect(blob.type).toBe("image/svg+xml;charset=utf-8");
    expect(await blob.text()).toContain("Клиент");
    expect(state.cells.some((cell) => cell.id === "participant:client")).toBe(true);
    expect(state.cells.some((cell) => /^(highlight|execution):/.test(cell.id))).toBe(false);
    expect(state.dispose).toHaveBeenCalledOnce();
    expect(document.querySelector("[data-sequence-export]")).toBeNull();
  });

  it("rejects PNG that exceeds raster budget and leaves SVG available", async () => {
    state.box.width = 10_000;
    await expect(exportSequenceImage(exampleCanvas(), "png")).rejects.toThrow("SVG");
    expect(state.png).not.toHaveBeenCalled();
    await expect(exportSequenceImage(exampleCanvas(), "svg")).resolves.toBeInstanceOf(Blob);
  });

  it("decodes PNG bytes without a network fetch", async () => {
    const blob = await exportSequenceImage(exampleCanvas(), "png");
    expect(blob.type).toBe("image/png");
    expect([...new Uint8Array(await blob.arrayBuffer())]).toEqual([137, 80, 78, 71]);
  });

  it("times out a renderer and releases resources", async () => {
    vi.useFakeTimers();
    state.svg.mockReturnValue(new Promise(() => {}));
    const pending = exportSequenceImage(exampleCanvas(), "svg");
    const check = expect(pending).rejects.toThrow("время");
    await vi.advanceTimersByTimeAsync(15_000);
    await check;
    expect(state.dispose).toHaveBeenCalledOnce();
    expect(document.querySelector("[data-sequence-export]")).toBeNull();
  });
});

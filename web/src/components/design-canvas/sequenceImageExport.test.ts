import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { exampleCanvas } from "./canvasModel";
import { exportSequenceImage } from "./sequenceImageExport";

const state = vi.hoisted(() => ({
  cells: [] as Array<{
    id: string;
    height?: number;
    attrs?: Record<string, Record<string, unknown>>;
  }>,
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
  it("uses identical nested alt projection for SVG and PNG with complete wrapped conditions", async () => {
    const doc = exampleCanvas();
    doc.formatVersion = 2;
    const first = doc.messages[0]!;
    const second = doc.messages[1]!;
    const condition = "Проверить разрешение пользователя и доступность выбранного ресурса "
      .repeat(5)
      .trim();
    doc.fragments = [
      {
        id: "nested",
        kind: "loop",
        label: "Retry",
        fromMessageId: first.id,
        toMessageId: first.id,
        parentFragmentId: "choice",
        parentBranchId: "yes",
      },
      {
        id: "choice",
        kind: "alt",
        label: "Route",
        fromMessageId: first.id,
        toMessageId: second.id,
        branches: [
          { id: "yes", label: condition, fromMessageId: first.id, toMessageId: first.id },
          { id: "no", label: "else", fromMessageId: second.id, toMessageId: second.id },
        ],
      },
    ];
    await exportSequenceImage(doc, "svg");
    const svgCells = structuredClone(state.cells);
    const branch = svgCells.find(({ id }) => id === "fragment-branch:choice:yes")!;
    expect(String(branch.attrs!.label!.text)).toContain("\n");
    expect(String(branch.attrs!.label!.text).replace(/\n/g, " ")).toBe(condition);
    expect(svgCells.some(({ id }) => id === "fragment:nested")).toBe(true);
    expect(
      svgCells.find(({ id }) => id === "fragment-branch:choice:no")!.attrs!.separator!
        .strokeDasharray,
    ).toBe("6 4");
    await exportSequenceImage(doc, "png");
    expect(state.cells).toEqual(svgCells);
  });

  it("preserves wrapped participant and message text in the export projection", async () => {
    const doc = exampleCanvas();
    const participant = doc.participants[0]!;
    participant.name = "Очень длинное название участника сценария для проверки экспорта";
    const message = doc.messages[0]!;
    message.label = "Проверить текущую сессию пользователя и получить все доступные задания";
    await exportSequenceImage(doc, "svg");
    const header = state.cells.find((cell) => cell.id === `participant:${participant.id}`)!;
    const card = state.cells.find((cell) => cell.id === `message-label:${message.id}`)!;
    const name = String(header.attrs!.name!.text);
    const label = String(card.attrs!.label!.text);
    expect(name).toContain("\n");
    expect(name.replace(/\n/g, " ")).toBe(participant.name);
    expect(header.height).toBeGreaterThan(52);
    expect(label).toContain("\n");
    expect(label.replace(/\n/g, " ")).toBe(message.label);
    expect(card.height).toBeGreaterThan(28);
  });

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

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDiagramWebp } from "./exportSchemaDiagram";

const smallSvg =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="-10 -20 200 100"><rect width="200" height="100" fill="green"/></svg>';
let encodedType = "image/webp";
let imageFails = false;
let canvases: HTMLCanvasElement[];
let imageSources: string[];
let paint: {
  fillStyle: string;
  fillRect: ReturnType<typeof vi.fn>;
  drawImage: ReturnType<typeof vi.fn>;
};

beforeEach(() => {
  encodedType = "image/webp";
  imageFails = false;
  canvases = [];
  imageSources = [];
  paint = { fillStyle: "", fillRect: vi.fn(), drawImage: vi.fn() };
  // The test DOM cannot decode SVG images or encode canvas pixels. Mock those
  // browser boundaries, preserving the real sizing and validation.
  vi.stubGlobal(
    "Image",
    class {
      src = "";
      async decode(): Promise<void> {
        imageSources.push(this.src);
        if (imageFails) throw new Error("decode failed");
      }
    },
  );
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(
    function (this: HTMLCanvasElement) {
      canvases.push(this);
      return paint as unknown as CanvasRenderingContext2D;
    },
  );
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((callback, type) => {
    expect(type).toBe("image/webp");
    callback(encodedType === "" ? null : new Blob(["encoded"], { type: encodedType }));
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("createDiagramWebp", () => {
  it("rasterizes the complete viewBox at 2x on white using a CSP-compatible image source", async () => {
    const blob = await createDiagramWebp(smallSvg);
    expect(blob.type).toBe("image/webp");
    expect(canvases[0]?.width).toBe(400);
    expect(canvases[0]?.height).toBe(200);
    expect(paint.fillStyle).toBe("#ffffff");
    expect(paint.fillRect).toHaveBeenCalledWith(0, 0, 400, 200);
    expect(paint.drawImage).toHaveBeenCalledWith(expect.anything(), 0, 0, 400, 200);
    expect(imageSources[0]).toMatch(/^data:image\/svg\+xml/);
    const image = new DOMParser().parseFromString(
      decodeURIComponent(imageSources[0]!.split(",")[1]!),
      "image/svg+xml",
    ).documentElement;
    expect(image.getAttribute("width")).toBe("400");
    expect(image.getAttribute("height")).toBe("200");
    expect(image.getAttribute("viewBox")).toBe("-10 -20 200 100");
  });

  it.each([
    [20_000, 10_000],
    [100_000, 100],
  ])("bounds large %s × %s diagrams without changing their proportions", async (width, height) => {
    await createDiagramWebp(
      `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${width} ${height}"/>`,
    );
    const canvas = canvases[0]!;
    expect(canvas.width).toBeLessThanOrEqual(8192);
    expect(canvas.height).toBeLessThanOrEqual(8192);
    expect(canvas.width * canvas.height).toBeLessThanOrEqual(16_000_000);
    expect(Math.abs(canvas.height - (canvas.width * height) / width)).toBeLessThan(1);
    const image = new DOMParser().parseFromString(
      decodeURIComponent(imageSources[0]!.split(",")[1]!),
      "image/svg+xml",
    ).documentElement;
    expect(Number(image.getAttribute("width"))).toBe(canvas.width);
    expect(Number(image.getAttribute("height"))).toBe(canvas.height);
  });

  it.each(["image/png", ""])("rejects an encoder returning %s instead of WebP", async (type) => {
    encodedType = type;
    await expect(createDiagramWebp(smallSvg)).rejects.toThrow();
  });

  it("reports SVG decoding failures without attempting to encode an empty image", async () => {
    imageFails = true;
    await expect(createDiagramWebp(smallSvg)).rejects.toThrow();
    expect(canvases).toHaveLength(0);
  });

  it.each([
    "<svg",
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 0 100"/>',
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 NaN 100"/>',
  ])("rejects invalid SVG dimensions: %s", async (svg) => {
    await expect(createDiagramWebp(svg)).rejects.toThrow();
    expect(imageSources).toHaveLength(0);
  });
});

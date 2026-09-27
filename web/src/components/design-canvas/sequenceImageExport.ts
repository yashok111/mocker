import { Export, Graph } from "@antv/x6";
import { renderSequenceProjection } from "./sequenceProjection";
import type { CanvasDocument } from "./types";

const TIMEOUT_MS = 15_000;
const PNG_RATIO = 2;
const PADDING = 24;

/** Render a saved snapshot in an isolated graph, independent of live viewport or selection. */
export async function exportSequenceImage(
  doc: CanvasDocument,
  format: "svg" | "png",
): Promise<Blob> {
  const container = document.createElement("div");
  container.dataset.sequenceExport = "true";
  container.setAttribute("aria-hidden", "true");
  container.style.cssText =
    "position:fixed;left:-100000px;top:0;width:1024px;height:768px;pointer-events:none";
  document.body.appendChild(container);
  let graph: Graph | undefined;
  let finished = false;
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function render(): Promise<Blob> {
    await document.fonts?.ready;
    if (finished) throw new Error("Истекло время подготовки изображения");
    graph = new Graph({
      container,
      width: 1024,
      height: 768,
      async: false,
      background: { color: "#ffffff" },
      interacting: false,
    });
    graph.use(new Export());
    renderSequenceProjection(graph, doc, null, true, undefined, undefined);
    const box = graph.getContentBBox({ useCellGeometry: false });
    if (!box || !Number.isFinite(box.width) || !Number.isFinite(box.height)) {
      throw new Error("Не удалось определить размер диаграммы");
    }
    const viewBox = {
      x: box.x - PADDING,
      y: box.y - PADDING,
      width: Math.max(1, box.width) + PADDING * 2,
      height: Math.max(1, box.height) + PADDING * 2,
    };
    if (format === "svg") {
      const svg = await graph.toSVGAsync({ preserveDimensions: true, copyStyles: true, viewBox });
      return new Blob([svg], { type: "image/svg+xml;charset=utf-8" });
    }
    const width = Math.ceil(viewBox.width * PNG_RATIO);
    const height = Math.ceil(viewBox.height * PNG_RATIO);
    if (width > 16384 || height > 16384 || width * height > 32_000_000) {
      throw new Error("Диаграмма слишком большая для PNG. Скачайте SVG.");
    }
    const data = await graph.toPNGAsync({
      backgroundColor: "#ffffff",
      ratio: PNG_RATIO,
      viewBox,
      padding: 0,
      width: viewBox.width,
      height: viewBox.height,
      copyStyles: true,
    });
    const prefix = "data:image/png;base64,";
    if (!data.startsWith(prefix)) throw new Error("Не удалось создать PNG. Попробуйте SVG.");
    const decoded = atob(data.slice(prefix.length));
    return new Blob([Uint8Array.from(decoded, (c) => c.charCodeAt(0))], { type: "image/png" });
  }

  try {
    return await Promise.race([
      render(),
      new Promise<never>((_, reject) => {
        timer = setTimeout(
          () => reject(new Error("Истекло время подготовки изображения. Попробуйте SVG.")),
          TIMEOUT_MS,
        );
      }),
    ]);
  } finally {
    finished = true;
    clearTimeout(timer);
    graph?.dispose();
    container.remove();
  }
}

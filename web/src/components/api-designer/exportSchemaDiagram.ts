const MAX_SIDE = 8192;
const MAX_PIXELS = 16_000_000;

export async function createDiagramWebp(svg: string): Promise<Blob> {
  const parsed = new DOMParser().parseFromString(svg, "image/svg+xml");
  const root = parsed.documentElement;
  const viewBox =
    root
      .getAttribute("viewBox")
      ?.trim()
      .split(/[\s,]+/)
      .map(Number) ?? [];
  const [, , width = 0, height = 0] = viewBox;
  if (
    parsed.querySelector("parsererror") ||
    root.localName !== "svg" ||
    root.namespaceURI !== "http://www.w3.org/2000/svg" ||
    viewBox.length !== 4 ||
    !viewBox.every(Number.isFinite) ||
    width <= 0 ||
    height <= 0
  ) {
    throw new Error("Invalid SVG dimensions");
  }

  const scale = Math.min(
    2,
    MAX_SIDE / width,
    MAX_SIDE / height,
    Math.sqrt(MAX_PIXELS / width / height),
  );
  const outputWidth = Math.max(1, Math.floor(width * scale));
  const outputHeight = Math.max(1, Math.floor(height * scale));
  // Bound image decoding as well as the canvas. Keep the original viewBox so
  // negative origins and the entire diagram survive the change in resolution.
  root.setAttribute("width", String(outputWidth));
  root.setAttribute("height", String(outputHeight));
  root.style.removeProperty("max-width");
  const image = new Image();
  // The app's img-src CSP permits data: but not blob: URLs.
  image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(new XMLSerializer().serializeToString(root))}`;
  await image.decode();

  const canvas = document.createElement("canvas");
  canvas.width = outputWidth;
  canvas.height = outputHeight;
  const context = canvas.getContext("2d");
  if (!context) throw new Error("Canvas is unavailable");
  context.fillStyle = "#ffffff";
  context.fillRect(0, 0, outputWidth, outputHeight);
  context.drawImage(image, 0, 0, outputWidth, outputHeight);

  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (blob) => {
        // Unsupported encoders can silently return PNG instead of WebP.
        if (blob?.type === "image/webp") resolve(blob);
        else reject(new Error("WebP encoding is unavailable"));
      },
      "image/webp",
      1,
    );
  });
}

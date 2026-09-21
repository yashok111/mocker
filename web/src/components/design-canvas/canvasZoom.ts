import type { Graph } from "@antv/x6";

export function normalizeWheelDelta(delta: number, mode: number, pageHeight: number): number {
  return delta * (mode === 1 ? 16 : mode === 2 ? pageHeight : 1);
}

export function wheelZoomScale(scale: number, deltaPixels: number): number {
  const exponent = Math.max(-0.18, Math.min(0.18, -deltaPixels * 0.008));
  return Math.max(0.35, Math.min(2, scale * Math.exp(exponent)));
}

// X6's built-in wheel handler uses the sign of delta and a minimum 5% step.
// Pinch events need their actual magnitude and one viewport update per frame.
export function installCanvasWheelZoom(graph: Graph, container: HTMLElement): () => void {
  let frame: number | undefined;
  let pendingDelta = 0;
  let clientX = 0;
  let clientY = 0;

  const flush = () => {
    frame = undefined;
    const scale = graph.zoom();
    const target = wheelZoomScale(scale, pendingDelta);
    pendingDelta = 0;
    if (target === scale) return;
    const center = graph.clientToGraph({ x: clientX, y: clientY });
    graph.zoom(target, { absolute: true, center });
  };

  const onWheel = (event: WheelEvent) => {
    if (event.deltaY === 0) return;
    event.preventDefault();
    pendingDelta += normalizeWheelDelta(event.deltaY, event.deltaMode, container.clientHeight);
    clientX = event.clientX;
    clientY = event.clientY;
    frame ??= requestAnimationFrame(flush);
  };

  container.addEventListener("wheel", onWheel, { passive: false });
  return () => {
    container.removeEventListener("wheel", onWheel);
    if (frame !== undefined) cancelAnimationFrame(frame);
  };
}

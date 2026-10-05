import { useEffect, useRef } from "react";
import { Graph } from "@antv/x6";
import { renderSequenceProjection } from "../design-canvas/sequenceProjection";
import { layoutSequence } from "../design-canvas/sequenceLayout";
import type { CanvasDocument } from "../design-canvas/types";
export function BackendInteractionGraph({ document }: { document: CanvasDocument }) {
  const container = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!container.current) return;
    const geometry = layoutSequence(document);
    const graph = new Graph({
      container: container.current,
      width: Math.max(320, container.current.clientWidth),
      height: Math.min(600, geometry.height),
      interacting: false,
      background: { color: "#f8faf8" },
    });
    renderSequenceProjection(graph, document, null, true, undefined, undefined);
    graph.zoomToFit({ padding: 20, maxScale: 1 });
    const observer = new ResizeObserver(() => {
      if (container.current) {
        graph.resize(Math.max(320, container.current.clientWidth), Math.min(600, geometry.height));
        graph.zoomToFit({ padding: 20, maxScale: 1 });
      }
    });
    observer.observe(container.current);
    return () => {
      observer.disconnect();
      graph.dispose();
    };
  }, [document]);
  return (
    <div
      aria-hidden="true"
      ref={container}
      style={{ minWidth: 0, width: "100%", height: 400, overflow: "hidden" }}
    />
  );
}

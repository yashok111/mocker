import type { CellAttrs, EdgeLabel, GraphManual } from "@antv/x6";
import { theme } from "@/theme/mantine";

// X6 measures wrapping with canvas, where "inherit" is not a valid font family.
// Use the actual SVG font so measured line widths match what the user sees.
export const diagramFontFamily = theme.fontFamily ?? "sans-serif";

export function diagramOptions(minScale = 0.15) {
  return {
    autoResize: true,
    background: { color: "#f5f7f4" },
    grid: { visible: true, size: 20, type: "dot", args: { color: "#d0d9cf", thickness: 1 } },
    panning: { enabled: true, modifiers: "shift", eventTypes: ["leftMouseDown"] },
    mousewheel: { enabled: true, modifiers: ["ctrl", "meta"], minScale, maxScale: 2 },
  } satisfies Partial<GraphManual>;
}

type EdgeLabelOptions = {
  position?: EdgeLabel["position"];
  maxWidth?: number;
  maxHeight?: number;
  fill?: string;
};

export function diagramEdgeLabel(text: string, options: EdgeLabelOptions = {}): EdgeLabel {
  return {
    position: options.position ?? 0.5,
    attrs: {
      label: {
        text,
        fill: "#314638",
        fontSize: 11,
        fontFamily: diagramFontFamily,
        textWrap: {
          width: options.maxWidth ?? 180,
          height: options.maxHeight ?? 65,
          ellipsis: true,
        },
      },
      body: {
        fill: options.fill ?? "#ffffff",
        stroke: "#c7d3c8",
        rx: 6,
        ry: 6,
        refX: -10,
        refY: -6,
        refWidth: 20,
        refHeight: 12,
      },
    },
  };
}

export function diagramCardBody(selected = false): CellAttrs[string] {
  return {
    fill: "#ffffff",
    stroke: selected ? "#315b3a" : "#abbcac",
    strokeWidth: selected ? 2 : 1,
    rx: 10,
    ry: 10,
  };
}

export function diagramEdgeLine(selected = false): CellAttrs[string] {
  return {
    stroke: selected ? "#245e39" : "#66816c",
    strokeWidth: selected ? 3 : 1.5,
    targetMarker: { name: "block", width: 8, height: 6 },
  };
}

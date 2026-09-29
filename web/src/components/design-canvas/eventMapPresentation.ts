import { diagramFontFamily } from "../diagram/presentation";
import { canvasTextMeasurer, wrapCanvasText } from "./canvasText";
import { diagnosticGlyph, type DiagnosticSeverity } from "./eventMapDiagnostics";
import type { DiagramLayoutInput } from "../diagram/elkLayout";
import { measureDiagramLabel } from "../diagram/elkX6";
import type { GraphSubset } from "./eventMapSubset";

export function eventMapPresentation(
  subset: GraphSubset,
  markers: Map<string, DiagnosticSeverity>,
) {
  const nodeMeasure = canvasTextMeasurer(12, 600, diagramFontFamily);
  const nodeLabels = new Map<string, string>();
  const input: DiagramLayoutInput = {
    nodes: subset.nodes.map((node) => {
      const glyph = diagnosticGlyph(markers.get(node.id));
      const text = `${glyph ? `${glyph} ` : ""}${node.label}${node.groupId ? `\n${node.groupId}${node.clientId ? ` · ${node.clientId}` : ""}` : ""}`;
      const lines = wrapCanvasText(text, 216, nodeMeasure);
      const visible = lines.slice(0, 5);
      if (lines.length > visible.length) visible[4] = `${visible[4]}…`;
      nodeLabels.set(node.id, visible.join("\n"));
      return { id: node.id, width: 240, height: Math.max(64, visible.length * 16 + 24) };
    }),
    edges: subset.edges.map((edge) => {
      const glyph = diagnosticGlyph(markers.get(edge.id));
      const labeled =
        glyph ||
        ["retry", "dead_letter", "payload", "key", "headers", "send", "receive"].includes(
          edge.kind,
        );
      const text = `${glyph ? `${glyph} ` : ""}${edge.kind === "retry" ? "retry" : edge.kind === "dead_letter" ? "DLQ" : edge.label || edge.kind}`;
      return {
        id: edge.id,
        source: edge.source,
        target: edge.target,
        ...(labeled ? { label: measureDiagramLabel(text, 168, 3) } : {}),
      };
    }),
  };
  return { input, nodeLabels };
}

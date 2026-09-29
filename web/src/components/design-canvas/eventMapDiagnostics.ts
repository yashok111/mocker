import type { DesignScenarioEventMapDiagnostic } from "@/api/generated/schemas";

export type DiagnosticSeverity = DesignScenarioEventMapDiagnostic["severity"];

const rank: Record<DiagnosticSeverity, number> = { info: 1, warning: 2, error: 3 };

export function diagnosticMarkers(diagnostics: DesignScenarioEventMapDiagnostic[]) {
  const markers = new Map<string, DiagnosticSeverity>();
  for (const diagnostic of diagnostics) {
    if (!diagnostic.elementId) continue;
    const current = markers.get(diagnostic.elementId);
    if (!current || rank[diagnostic.severity] > rank[current])
      markers.set(diagnostic.elementId, diagnostic.severity);
  }
  return markers;
}

export function diagnosticGlyph(severity?: DiagnosticSeverity): string {
  if (severity === "error") return "⛔";
  if (severity === "warning") return "⚠";
  if (severity === "info") return "ⓘ";
  return "";
}

export function diagnosticColor(severity?: DiagnosticSeverity): string | undefined {
  if (severity === "error") return "#b5484d";
  if (severity === "warning") return "#a16e2f";
  if (severity === "info") return "#4775a0";
  return undefined;
}

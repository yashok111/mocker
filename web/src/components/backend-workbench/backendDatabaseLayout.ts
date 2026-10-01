import type {
  BackendDatabaseRelationshipItem,
  BackendDatabaseTableItem,
} from "@/api/generated/schemas";
import type { DiagramLayoutInput } from "../diagram/elkLayout";
import { measureDiagramLabel } from "../diagram/elkX6";
export function buildDatabaseScene(
  tables: BackendDatabaseTableItem[],
  relationships: BackendDatabaseRelationshipItem[],
) {
  const visible = tables.slice(0, 200);
  const ids = new Set(visible.map((table) => table.tableId));
  const eligible = relationships.filter(
    (edge) =>
      ids.has(edge.sourceTableId) && edge.targetTableId !== null && ids.has(edge.targetTableId),
  );
  const input: DiagramLayoutInput = {
    nodes: visible.map((table) => ({ id: table.tableId, width: 250, height: 96 })),
    edges: eligible.slice(0, 600).map((edge) => ({
      id: edge.edgeId,
      source: edge.sourceTableId,
      target: edge.targetTableId!,
      label: measureDiagramLabel(
        `${edge.status} · ${cardinalityText(edge.targetCardinality.min, edge.targetCardinality.max)}`,
      ),
    })),
  };
  return {
    input,
    tables: visible,
    relationships: eligible.slice(0, 600),
    excludedTables: tables.length - visible.length,
    excludedEdges: Math.max(0, eligible.length - 600),
    excludedEndpoints: relationships.length - eligible.length,
  };
}

export function cardinalityText(min: number | null, max: "1" | "many" | null) {
  return `${min === null ? "неизвестно" : min}…${max === null ? "неизвестно" : max === "many" ? "много" : max}`;
}

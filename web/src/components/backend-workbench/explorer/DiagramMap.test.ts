import { expect, it } from "vitest";
import type { BackendArchitectureDocument, BackendDiagramRow } from "@/api/generated/schemas";
import { mapDiagram } from "./DiagramMap";
import { revisionId } from "./testFixtures";

const origin = { kind: "authored", reason: "Fixture" } as const;
const doc: BackendArchitectureDocument = {
  format: "backend-diagram-v1",
  kind: "architecture",
  target: { revisionId },
  payload: {
    primarySystemId: "system",
    links: [],
    elements: [
      { id: "system", role: "software_system", label: "Platform" },
      { id: "app", role: "application", label: "Backend", parentId: "system" },
      { id: "operation", role: "component", label: "Operation", parentId: "app" },
      { id: "handler", role: "component", label: "Handler", parentId: "app" },
      { id: "isolated", role: "component", label: "Unconnected component", parentId: "app" },
      { id: "external", role: "software_system", label: "External system" },
    ].map((e) => ({
      ...e,
      origin,
      refs: [],
      responsibility: "",
      technology: "",
    })) as BackendArchitectureDocument["payload"]["elements"],
  },
};
const rows: BackendDiagramRow[] = [
  ...doc.payload.elements
    .filter((e) => e.id !== "app")
    .map((data) => ({ rowType: "architecture_element" as const, data })),
  {
    rowType: "architecture_link",
    data: {
      id: "call",
      from: "operation",
      to: "handler",
      relation: "calls",
      label: "Calls",
      refs: [],
      origin,
    },
  },
];
it("hides only the disconnected parent system in a component projection", () => {
  const result = mapDiagram(doc, rows, "app");
  expect(result.nodes.map((n) => n.id)).toEqual(["operation", "handler", "isolated", "external"]);
  expect(result.edges.map((e) => e.id)).toEqual(["call"]);
  expect(result.total).toBe(4);
});
it.each(["from", "to"] as const)(
  "keeps a parent system with an actual %s relationship",
  (direction) => {
    const link: BackendDiagramRow = {
      rowType: "architecture_link",
      data: {
        id: "system-call",
        from: direction === "from" ? "system" : "operation",
        to: direction === "to" ? "system" : "operation",
        relation: "calls",
        label: "Calls",
        refs: [],
        origin,
      },
    };
    expect(mapDiagram(doc, [...rows, link], "app").nodes.some((n) => n.id === "system")).toBe(true);
  },
);
it("keeps the system on its own architecture level", () => {
  expect(mapDiagram(doc, rows, "system").nodes.find((n) => n.id === "system")?.boundary).toBe(true);
});

import { expect, it } from "vitest";
import { readFileSync } from "node:fs";
import type { BackendBusinessMapDocument } from "@/api/generated/schemas";
import { businessMapGraph } from "./backendBusinessMapGraph";
const fixture = () =>
  JSON.parse(
    readFileSync("../internal/backendmodel/testdata/diagrams/business_map.json", "utf8"),
  ) as BackendBusinessMapDocument;
it("retains explicit cyclic links and identities without inferring layout causality", () => {
  const d = fixture();
  d.payload.links[3]!.to = d.payload.elements[1]!.id;
  const before = JSON.stringify(d);
  const a = businessMapGraph(d.payload);
  expect(a.links.map((l) => l.id)).toEqual(d.payload.links.map((l) => l.id));
  expect(a.elements).toHaveLength(8);
  expect(JSON.stringify(d)).toBe(before);
  const b = businessMapGraph({ ...d.payload, elements: [...d.payload.elements].reverse() });
  expect(b).toEqual(a);
});
it("keeps one event for two transport refs and preserves question role", () => {
  const d = fixture();
  d.payload.elements[2]!.refs = [
    { kind: "record", recordType: "node", id: "message-a" },
    { kind: "record", recordType: "node", id: "message-b" },
  ];
  const graph = businessMapGraph(d.payload);
  expect(graph.elements.filter((e) => e.id === d.payload.elements[2]!.id)).toHaveLength(1);
  expect(graph.elements.find((e) => e.id === d.payload.elements[7]!.id)?.role).toBe("question");
});

import { businessMapArchitectureSearch } from "./backendBusinessMapNavigation";
import type { BackendDiagramVersion } from "@/api/generated/schemas";
it("navigates to the exact C4 component level instead of a context-level nonmember", () => {
  const arch = {
    pin: { id: "arch", version: 3, contentHash: "hash" },
    targetHash: "target",
    document: {
      kind: "architecture",
      payload: {
        primarySystemId: "system",
        elements: [{ id: "component", role: "component", parentId: "app" }],
      },
    },
  } as BackendDiagramVersion;
  expect(businessMapArchitectureSearch(arch, "target", "component")).toEqual({
    diagramId: "arch",
    diagramVersion: 3,
    diagramHash: "hash",
    diagramLevel: "components",
    diagramRoot: "app",
    diagramSelection: "element:component",
  });
  expect(() => businessMapArchitectureSearch(arch, "foreign")).toThrow();
  expect(() => businessMapArchitectureSearch(arch, "target", "missing")).toThrow();
});

it("filters canvas cards and explicit links by origin, search and role", () => {
  const d = fixture();
  expect(
    businessMapGraph(d.payload, { search: "missing-value", origin: "all" }).elements,
  ).toHaveLength(0);
  expect(
    businessMapGraph(d.payload, { search: "", origin: "source_assertion" }).elements,
  ).toHaveLength(0);
  const events = businessMapGraph(d.payload, { search: "business_event", origin: "all" });
  expect(events.elements.map((e) => e.id)).toEqual([
    d.payload.elements[2]!.id,
    d.payload.elements[6]!.id,
  ]);
  expect(events.links).toHaveLength(0);
});

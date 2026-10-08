import { expect, it } from "vitest";
import type { BackendDiagramVersion } from "@/api/generated/schemas";
import {
  architectureDestinations,
  explicitArchitectureSearch,
  architectureSearch,
} from "./architectureNavigation";
import { nodeId, revisionId } from "./testFixtures";

const pin = { id: nodeId, version: 3, contentHash: "a".repeat(64) };
it("follows the authored exact diagram pin and flow target without inherited filters", () => {
  expect(
    explicitArchitectureSearch({
      format: "architecture-navigation-v1",
      kind: "diagram",
      label: "Details",
      diagram: pin,
      level: "components",
      rootId: "app",
      focusId: "component",
    }),
  ).toEqual(architectureSearch(pin, "components", "app", "component"));
  expect(
    explicitArchitectureSearch({
      format: "architecture-navigation-v1",
      kind: "flow",
      label: "Flow",
      flowId: nodeId,
      target: { revisionId },
    }),
  ).toEqual({ revisionId, wbView: "scenarios", wbMode: "flow", flowId: nodeId });
});

it("discovers architecture destinations through exact membership", () => {
  const element = {
    responsibility: "",
    technology: "",
    origin: { kind: "authored", reason: "Explicit mapping" },
    refs: [],
  } as const;
  const diagram = {
    pin,
    document: {
      format: "backend-diagram-v1",
      target: { revisionId },
      kind: "architecture",
      payload: {
        primarySystemId: "system",
        links: [],
        elements: [
          { id: "system", role: "software_system", label: "System", refs: [] },
          {
            id: "app",
            role: "application",
            parentId: "system",
            label: "App",
            refs: [],
            membership: { format: "exact-node-members-v1", nodeIds: [nodeId] },
          },
        ].map((e) => ({ ...element, ...e })),
      },
    },
  } as Pick<BackendDiagramVersion, "pin" | "document">;
  expect(architectureDestinations(diagram, [nodeId]).map((d) => d.search)).toEqual([
    architectureSearch(pin, "components", "app"),
  ]);
  expect(architectureDestinations(diagram, ["absent"])).toEqual([]);
});

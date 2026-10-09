import { afterEach, expect, it, vi } from "vitest";
import { readArchitectureChoices } from "./architectureReads";
import { projectId, revisionId, workspaceHTTP } from "./testFixtures";
import { json } from "@/test/http";
import type { BackendDiagramVersion, BackendArchitectureNavigation } from "@/api/generated/schemas";

const pin = (id: string) => ({ id, version: 1, contentHash: "a".repeat(64) });
const rootPin = pin("0197aaf9-5555-7000-8000-000000000010");
const childPin = pin("0197aaf9-5555-7000-8000-000000000011");
const viewId = "0197aaf9-5555-7000-8000-000000000012";
const nav = {
  format: "architecture-navigation-v1",
  kind: "diagram",
  label: "Операции и сценарии области",
  diagram: childPin,
  level: "components",
  rootId: "app",
} as const;
function diagram(
  p: typeof rootPin,
  navigation: BackendArchitectureNavigation[] = [],
): BackendDiagramVersion {
  return {
    projectId,
    pin: p,
    targetHash: "b".repeat(64),
    author: "test",
    createdAt: "2026-10-09T00:00:00Z",
    gaps: [],
    provenance: { format: "backend-diagram-provenance-v1", action: "create", elements: [] },
    provenanceHash: "c".repeat(64),
    document: {
      format: "backend-diagram-v1",
      kind: "architecture",
      target: { revisionId },
      payload: {
        primarySystemId: "system",
        links: [],
        elements: [
          { id: "system", role: "software_system", label: "Образовательная платформа" },
          { id: "app", role: "application", parentId: "system", label: "Backend" },
          { id: "component", role: "component", parentId: "app", label: "Достижения", navigation },
        ].map((e) => ({
          ...e,
          responsibility: "",
          technology: "",
          refs: [],
          origin: { kind: "authored", reason: "Fixture" },
        })),
      },
    },
  } as BackendDiagramVersion;
}
function setup(navigation: BackendArchitectureNavigation[] = [nav]) {
  const docs = [diagram(rootPin, navigation), diagram(childPin)];
  workspaceHTTP((path) => {
    if (path.endsWith("/diagrams"))
      return json(200, {
        catalogVersion: 1,
        items: docs.map((d) => ({
          id: d.pin.id,
          pin: d.pin,
          kind: "architecture",
          target: d.document.target,
          targetHash: d.targetHash,
        })),
        nextCursor: "",
      });
    for (const doc of docs)
      if (path.endsWith(`/diagrams/${doc.pin.id}/versions/1`)) return json(200, doc);
    if (path.endsWith("/diagram-views"))
      return json(200, { catalogVersion: 1, items: [{ id: viewId, version: 1 }], nextCursor: "" });
    if (path.endsWith(`/diagram-views/${viewId}/versions/1`))
      return json(200, {
        id: viewId,
        version: 1,
        name: "Достижения — полная область",
        state: { diagram: rootPin, level: "components", rootId: "app" },
      });
    return undefined;
  });
}
afterEach(() => vi.unstubAllGlobals());
it("separates named entry views from nested diagrams and opens the authored exact destination", async () => {
  setup();
  const choices = await readArchitectureChoices(
    projectId,
    { revisionId },
    undefined,
    [],
    new AbortController().signal,
  );
  expect(choices).toHaveLength(2);
  expect(choices[0]?.name).toBe("Достижения — полная область");
  expect(choices[1]).toMatchObject({
    name: "Достижения — Операции и сценарии области",
    nested: true,
    search: {
      diagramId: childPin.id,
      diagramVersion: 1,
      diagramHash: childPin.contentHash,
      diagramLevel: "components",
      diagramRoot: "app",
    },
  });
});
it("does not borrow names or hide diagrams through a stale navigation pin", async () => {
  setup([{ ...nav, diagram: { ...childPin, version: 2 } }]);
  const choices = await readArchitectureChoices(
    projectId,
    { revisionId },
    undefined,
    [],
    new AbortController().signal,
  );
  expect(choices).toHaveLength(2);
  expect(choices[1]?.name).not.toBe("Достижения — Операции и сценарии области");
  expect(choices[1]).not.toHaveProperty("nested", true);
});

it("includes a named companion in the same complete scheme list", async () => {
  const d = diagram(childPin);
  d.document = {
    format: "backend-diagram-v1",
    kind: "interactions",
    target: { revisionId },
    payload: { scopeRefs: [], participants: [], steps: [], branches: [], order: [] },
  };
  workspaceHTTP((path) =>
    path.endsWith("/diagrams")
      ? json(200, {
          catalogVersion: 1,
          items: [
            {
              id: childPin.id,
              pin: childPin,
              kind: "interactions",
              name: "Получить ссылку",
              target: { revisionId },
              targetHash: d.targetHash,
            },
          ],
          nextCursor: "",
        })
      : path.endsWith(`/diagrams/${childPin.id}/versions/1`)
        ? json(200, d)
        : path.endsWith("/diagram-views")
          ? json(200, {
              catalogVersion: 1,
              items: [{ id: viewId, version: 1, kind: "interactions" }],
              nextCursor: "",
            })
          : path.endsWith(`/diagram-views/${viewId}/versions/1`)
            ? json(200, {
                id: viewId,
                version: 1,
                name: "Литрес — получить ссылку",
                state: { diagram: childPin },
              })
            : undefined,
  );
  const result = await readArchitectureChoices(
    projectId,
    { revisionId },
    undefined,
    [],
    new AbortController().signal,
  );
  expect(result).toHaveLength(1);
  expect(result[0]).toMatchObject({
    name: "Литрес — получить ссылку",
    search: { diagramViewId: viewId, diagramViewVersion: 1, wbView: "scenarios" },
  });
});

it("keeps a companion mapped through step refs when a responsibility has multiple members", async () => {
  const d = diagram(childPin);
  d.document = {
    format: "backend-diagram-v1",
    kind: "interactions",
    target: { revisionId },
    payload: {
      scopeRefs: [],
      participants: [],
      steps: [
        {
          id: "step",
          label: "Meaningful action",
          kind: "action",
          from: "actor",
          branchPath: [],
          origin: { kind: "authored", reason: "Source mapping" },
          refs: [{ kind: "record", recordType: "node", id: "member-one" }],
        },
      ],
      branches: [],
      order: [],
    },
  };
  workspaceHTTP((path) =>
    path.endsWith("/diagrams")
      ? json(200, {
          catalogVersion: 1,
          items: [
            {
              id: childPin.id,
              pin: childPin,
              kind: "interactions",
              name: "Scenario",
              target: { revisionId },
              targetHash: d.targetHash,
            },
          ],
          nextCursor: "",
        })
      : path.endsWith(`/diagrams/${childPin.id}/versions/1`)
        ? json(200, d)
        : path.endsWith("/diagram-views")
          ? json(200, { catalogVersion: 1, items: [], nextCursor: "" })
          : undefined,
  );
  const result = await readArchitectureChoices(
    projectId,
    { revisionId },
    undefined,
    ["member-one", "member-two"],
    new AbortController().signal,
  );
  expect(result).toHaveLength(1);
  expect(result[0]?.search.diagramId).toBe(childPin.id);
});

import { describe, expect, it } from "vitest";
import { prepareDiagramReplay } from "./backendDiagramReplay";
import type {
  BackendDiagramVersion,
  BackendDiagramScope,
  BackendDiagramScopeInput,
  BackendReplayTemplate,
} from "@/api/generated/schemas";
const pin = { id: "diagram", version: 2, contentHash: "hash" };
const endpoint = { kind: "record" as const, recordType: "node" as const, id: "endpoint" };
const template = {
  format: "backend-replay-v1",
  fixtureHash: "fixture",
  failurePoint: "orders.persist.after_payment",
  artifactPins: [],
  excludedIds: [],
  steps: [
    { id: "first", kind: "order_first", scope: "actual_fixture" },
    { id: "retry", kind: "order_retry", scope: "actual_fixture" },
  ],
  assertions: [{ id: "assert", kind: "orders", stepId: "journal", expected: 1 }],
} as BackendReplayTemplate;
function fixture(kind: string) {
  const row = { id: "semantic", refs: [endpoint], label: "Misleading label" };
  const payload =
    kind === "architecture"
      ? { elements: [row], links: [] }
      : kind === "interactions"
        ? { participants: [], steps: [row], branches: [], order: [] }
        : kind === "lifecycle"
          ? { states: [row], transitions: [], rules: [] }
          : { elements: [row], links: [] };
  return {
    pin,
    projectId: "project",
    targetHash: "target",
    document: { kind, target: { revisionId: "revision" }, payload },
  } as unknown as BackendDiagramVersion;
}
const input = {
  pin,
  selectors: [{ kind: "semantic", id: "semantic" }],
} as BackendDiagramScopeInput;
const scope = {
  pin,
  selectors: input.selectors,
  target: { revisionId: "revision" },
  targetHash: "target",
  scopeHash: "scope",
  sourceRefs: [endpoint],
  members: [],
  gaps: [],
  truncated: false,
} as BackendDiagramScope;
const nodes = new Map([
  ["endpoint", { kind: "http_operation", attributes: { method: "POST", path: "/orders" } }],
]);
describe("diagram replay preparation", () => {
  for (const kind of ["architecture", "interactions", "lifecycle", "business_map"])
    it(`prepares exact supported endpoint for ${kind}`, () => {
      const out = prepareDiagramReplay(fixture(kind), input, scope, nodes, template);
      expect(out.package.diagramBindings?.map((b) => b.elementId)).toEqual([
        "semantic",
        "semantic",
      ]);
      expect(out.package.diagramScopeHash).toBe("scope");
      expect(out.package.diagramBindings?.[0]?.diagram).toEqual(pin);
    });
  it("does not trust labels or unrelated endpoints", () => {
    const out = prepareDiagramReplay(
      fixture("architecture"),
      input,
      scope,
      new Map([
        ["endpoint", { kind: "http_operation", attributes: { method: "GET", path: "/orders" } }],
      ]),
      template,
    );
    expect(out.package.diagramBindings).toEqual([]);
    expect(out.package.excludedIds).toContain("endpoint");
  });
  it("maps projected refs to original unique semantic links, never projected IDs", () => {
    const d = fixture("architecture");
    if (d.document.kind !== "architecture") throw Error();
    d.document.payload.elements = [];
    d.document.payload.links = [{ id: "original", refs: [endpoint] } as never];
    const i = {
      pin,
      selectors: [
        {
          kind: "architecture_relation",
          id: "projected",
          projection: { policy: "architecture-v1", level: "containers", rootId: "root" },
        },
      ],
    } as BackendDiagramScopeInput;
    const out = prepareDiagramReplay(d, i, { ...scope, selectors: i.selectors }, nodes, template);
    expect(out.package.diagramBindings?.map((b) => b.elementId)).toEqual(["original", "original"]);
    expect(out.package.diagramScope).toEqual(i);
  });
  it("refuses stale or incomplete resolved scopes", () => {
    for (const bad of [
      { ...scope, truncated: true },
      { ...scope, pin: { ...pin, version: 3 } },
      { ...scope, targetHash: "other" },
    ])
      expect(() =>
        prepareDiagramReplay(fixture("architecture"), input, bad, nodes, template),
      ).toThrow();
  });
 it("keeps unsupported artifact refs visible and preserves historical pins",()=>{
 const d=fixture("architecture"); if(d.document.kind!=="architecture")throw Error();
 const artifact={kind:"artifact",rowId:"artifact-row",locator:{}} as never;
 d.document.payload.elements[0]!.refs.push(artifact);
 const out=prepareDiagramReplay(d,input,{...scope,sourceRefs:[endpoint,artifact]},nodes,template);
 d.pin={...pin,version:3};
 expect(out.package.excludedIds).toContain("artifact-row");
 expect(out.package.diagramBindings?.[0]?.diagram.version).toBe(2);
 });

});

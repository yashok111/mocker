import type {
  BackendDiagramRef,
  BackendDiagramScope,
  BackendDiagramScopeInput,
  BackendDiagramVersion,
  BackendReplayPackage,
  BackendReplayTemplate,
} from "@/api/generated/schemas";
import {
  resolveBackendDiagramScope,
  getBackendReplayTemplate,
} from "@/api/generated/backend-projects/backend-projects";
import { readBackendNode } from "./backendGraphReads";
import { sameDiagramPin } from "./backendDiagramReads";
export type ReplayPreparation = {
  projectId: string;
  package: Omit<BackendReplayPackage, "profile">;
  excluded: { id: string; reason: string }[];
};
type EndpointRecord = { kind: string; attributes: unknown };
type SemanticRow = { id: string; refs?: BackendDiagramRef[] };
const refKey = (ref: BackendDiagramRef) => JSON.stringify(ref);
function rows(diagram: BackendDiagramVersion): SemanticRow[] {
  const d = diagram.document;
  switch (d.kind) {
    case "architecture":
      return [...d.payload.elements, ...d.payload.links];
    case "interactions":
      return [
        ...d.payload.participants,
        ...d.payload.steps,
        ...d.payload.branches,
        ...d.payload.order,
      ];
    case "lifecycle":
      return [...d.payload.states, ...d.payload.transitions, ...d.payload.rules];
    case "business_map":
      return [...d.payload.elements, ...d.payload.links];
  }
}
export function prepareDiagramReplay(
  diagram: BackendDiagramVersion,
  input: BackendDiagramScopeInput,
  scope: BackendDiagramScope,
  nodes: Map<string, EndpointRecord>,
  template: BackendReplayTemplate,
): ReplayPreparation {
  if (
    !sameDiagramPin(diagram.pin, input.pin) ||
    !sameDiagramPin(scope.pin, input.pin) ||
    scope.targetHash !== diagram.targetHash ||
    JSON.stringify(scope.target) !== JSON.stringify(diagram.document.target) ||
    scope.truncated ||
    scope.gaps.length
  )
    throw new Error("Exact complete diagram scope is required.");
  if (JSON.stringify(scope.selectors) !== JSON.stringify(input.selectors))
    throw new Error("Resolved selectors changed.");
  const excluded: { id: string; reason: string }[] = [];
  const exclude = (id: string, reason: string) => {
    if (!excluded.some((x) => x.id === id)) excluded.push({ id, reason });
  };
  const semantic = rows(diagram);
  const scoped = new Set(scope.sourceRefs.map(refKey));
  const selected = new Map<string, SemanticRow>();
  for (const selector of input.selectors) {
    if (selector.kind === "semantic") {
      const row = semantic.find((x) => x.id === selector.id);
      if (!row) throw new Error("Selected semantic member is absent from exact diagram.");
      selected.set(row.id, row);
    } else {
      const candidates =
        diagram.document.kind === "architecture" ? diagram.document.payload.links : [];
      for (const ref of scope.sourceRefs) {
        const matches = candidates.filter((row) => row.refs.some((r) => refKey(r) === refKey(ref)));
        if (matches.length === 1) selected.set(matches[0]!.id, matches[0]!);
        else
          exclude(
            ref.kind === "record" ? ref.id : ref.rowId,
            "Projection has no unique original semantic link for this source ref.",
          );
      }
      if (!scope.sourceRefs.length)
        exclude(selector.id, "Projection contains no endpoint source refs.");
    }
  }
  const bindings: NonNullable<BackendReplayPackage["diagramBindings"]> = [];
  for (const row of selected.values()) {
    let supported = false;
    if (!row.refs?.length) exclude(row.id, "Selected member has no endpoint references.");
    for (const ref of row.refs ?? []) {
      if (!scoped.has(refKey(ref))) continue;
      const id = ref.kind === "record" ? ref.id : ref.rowId;
      if (ref.kind !== "record" || ref.recordType !== "node") {
        exclude(
          id,
          "Only source node endpoint references are supported; artifact/edge references are excluded.",
        );
        continue;
      }
      const node = nodes.get(id);
      const attrs = node?.attributes as { method?: unknown; path?: unknown } | undefined;
      if (node?.kind !== "http_operation" || attrs?.method !== "POST" || attrs.path !== "/orders") {
        exclude(id, "Reference is not a verified POST /orders endpoint at this exact target.");
        continue;
      }
      supported = true;
    }
    if (supported)
      for (const step of template.steps.filter(
        (x) => x.kind === "order_first" || x.kind === "order_retry",
      ))
        bindings.push({
          diagram: structuredClone(diagram.pin),
          elementId: row.id,
          stepId: step.id,
          assertionIds: template.assertions.map((a) => a.id),
        });
    else exclude(row.id, "Selected semantic member has no supported Orders endpoint.");
  }
  if (bindings.length > 1000) throw new Error("Replay binding limit exceeded.");
  return {
    projectId: diagram.projectId,
    package: {
      ...structuredClone(template),
      target: structuredClone(scope.target),
      targetHash: scope.targetHash,
      diagramScope: structuredClone(input),
      diagramScopeHash: scope.scopeHash,
      diagramBindings: bindings,
      excludedIds: excluded.map((x) => x.id),
    },
    excluded,
  };
}
// These are the only network dependencies: exact graph reads, scope resolution and template read.
export async function readDiagramReplayPreparation(
  projectId: string,
  diagram: BackendDiagramVersion,
  input: BackendDiagramScopeInput,
): Promise<ReplayPreparation> {
  if (projectId !== diagram.projectId) throw new Error("Diagram belongs to another project.");
  const response = await resolveBackendDiagramScope(projectId, input);
  if (response.status !== 200) throw new Error("Unable to resolve exact diagram scope.");
  const scope = response.data;
  if (scope.truncated || scope.gaps.length)
    throw new Error("Incomplete diagram scope cannot prepare verified replay.");
  if (scope.sourceRefs.length > 1000)
    throw new Error("Selected replay scope exceeds the preparation limit.");
  const nodes = new Map<string, EndpointRecord>();
  const signal = new AbortController().signal;
  for (const ref of scope.sourceRefs) {
    if (ref.kind !== "record" || ref.recordType !== "node" || nodes.has(ref.id)) continue;
    try {
      const value = await readBackendNode(projectId, scope.target, ref.id, signal);
      if ("pins" in value && value.pins.targetHash !== scope.targetHash)
        throw new Error("Target mismatch");
      const node = "node" in value ? value.node : value;
      if ("kind" in node && "attributes" in node)
        nodes.set(ref.id, { kind: node.kind, attributes: node.attributes });
    } catch {
      /* A missing/incompatible endpoint remains an explicit exclusion. */
    }
  }
  const template = await getBackendReplayTemplate(projectId);
  if (template.status !== 200) throw new Error("Unable to read fixed replay template.");
  return prepareDiagramReplay(diagram, input, scope, nodes, template.data);
}

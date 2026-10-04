import type { BackendChangeRebaseConflict } from "@/api/generated/schemas";
import { backendChangeSchemas } from "./backendChangeSchema";
import { resolveChangeSchema } from "./backendChangeFormModel";
import type { ChangeSchema } from "./backendChangeSchemaTypes";
/** Select the exact property schema, not the broad replacement-value union. */
export function rebaseReplacementSchema(
  conflict: BackendChangeRebaseConflict,
  recordKind?: string,
): ChangeSchema | undefined {
  const selector = conflict.selector;
  if (selector.kind === "record") return;
  if (selector.kind === "artifact") return backendChangeSchemas.BackendChangeRebaseArtifactValue;
  if (selector.kind === "criterion") return backendChangeSchemas.BackendChangeCriterion;
  if (selector.kind === "identity") return { type: ["string", "null"] };
  if (selector.kind === "edge_name") return { type: "string" };
  if (selector.kind !== "property") return;
  const property = selector.property;
  if (property.kind !== "source") return { type: ["string", "null"] };
  const source = property.source;
  if (source.kind === "name") return { type: "string" };
  if (source.kind === "parent") return { type: ["string", "null"], format: "uuid" };
  if (source.kind === "edge_endpoints")
    return {
      type: "object",
      additionalProperties: false,
      required: ["from", "to"],
      properties: {
        from: { type: "string", format: "uuid" },
        to: { type: "string", format: "uuid" },
      },
    };
  if (source.kind === "flow_ports")
    return {
      type: "array",
      items: { $ref: "#/components/schemas/BackendFlowPort" },
      maxItems: 500,
    };
  if (source.kind === "mapping_sources")
    return {
      type: "array",
      items: { $ref: "#/components/schemas/BackendLineageValueRef" },
      maxItems: 64,
      uniqueItems: true,
    };
  if (source.kind === "mapping_destination")
    return { $ref: "#/components/schemas/BackendLineageValueRef" };
  if (source.kind === "representation_selector")
    return resolveChangeSchema(backendChangeSchemas.BackendRepresentationFieldAttributes!)
      .properties?.selector;
  if (!recordKind) return;
  const branches =
    conflict.object.recordType === "node"
      ? backendChangeSchemas.BackendChangeNodeUpdate!.oneOf!
      : backendChangeSchemas.BackendChangeProposalCommand!.oneOf!.filter(
          (b) => b.properties?.type?.const === "upsert_edge",
        );
  const branch = branches.find(
    (b) => b.properties?.kind?.const === recordKind && b.properties.attributes,
  );
  if (!branch?.properties?.attributes) return;
  let attributes = resolveChangeSchema(branch.properties.attributes);
  if (source.kind === "relational_facet") {
    const facets = attributes.properties?.facets;
    if (!facets) return;
    const item = resolveChangeSchema(facets).additionalProperties;
    if (!item || typeof item !== "object") return;
    attributes = resolveChangeSchema(item);
  }
  return attributes.properties?.[source.group];
}

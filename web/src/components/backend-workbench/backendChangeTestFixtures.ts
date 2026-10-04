import type { BackendChangeProposalDetail } from "@/api/generated/schemas";
import { changeEditableSchema, resolveChangeSchema } from "./backendChangeFormModel";
import type { ChangeJSON, ChangeObject, ChangeSchema } from "./backendChangeSchemaTypes";
export const changeTestID = "0197aaf9-5555-7000-8000-000000000001";
export const changeDraftID = "0197aaf9-5555-7000-8000-000000000002";
export const changeNextID = "0197aaf9-5555-7000-8000-000000000003";
export const changeHash = "a".repeat(64);
export function changeTestDetail(): BackendChangeProposalDetail {
  const time = "2026-10-03T00:00:00Z";
  return {
    proposal: {
      id: changeTestID,
      projectId: changeTestID,
      name: "Desired model",
      version: 1,
      status: "draft",
      currentDraftRevisionId: changeDraftID,
      currentDraftHash: changeHash,
      createdAt: time,
      updatedAt: time,
    },
    revision: {
      id: changeDraftID,
      proposalId: changeTestID,
      parentRevisionId: null,
      documentVersion: "proposal-graph-v1",
      baseRevisionId: changeTestID,
      baseSemanticHash: changeHash,
      baseSchemaVersion: "6",
      sourceSnapshotIds: [],
      sourceVector: { documentVersion: "source-vector-v1", partitions: [], snapshots: [] },
      artifactPins: [],
      artifactContext: {
        documentVersion: "",
        sourceContentHash: changeHash,
        sourceSemanticHash: changeHash,
        apiBindings: [],
        editorBindings: [],
      },
      delta: {
        created: [],
        removed: [],
        properties: [],
        identityIntents: [],
        artifactIntents: [],
        edgeNames: [],
      },
      criteria: [],
      semanticHash: changeHash,
      acceptedBatchRevisionId: changeDraftID,
      author: "user",
      summary: "Created",
      createdAt: time,
    },
    history: [
      {
        id: changeDraftID,
        parentRevisionId: null,
        semanticHash: changeHash,
        author: "user",
        summary: "Created",
        createdAt: time,
      },
    ],
    nextCursor: "",
    baseOutdated: false,
    currentSourceRevisionId: changeTestID,
    currentSourceSemanticHash: changeHash,
  };
}
export function changeFixtureValue(input: ChangeSchema, depth = 0): ChangeJSON {
  if (depth > 32) throw new Error("Fixture nesting");
  const schema = resolveChangeSchema(input);
  if ("const" in schema) return schema.const!;
  if (schema.enum) return schema.enum[0]!;
  if (schema.oneOf || schema.anyOf)
    return changeFixtureValue((schema.oneOf ?? schema.anyOf)![0]!, depth + 1);
  if (schema.type === "object" || schema.properties) {
    let object: ChangeObject = {};
    for (const key of schema.required ?? [])
      object[key] = changeFixtureValue(schema.properties![key]!, depth + 1);
    const adjusted = changeEditableSchema(schema, object);
    object = {};
    for (const key of adjusted.required ?? [])
      object[key] = changeFixtureValue(adjusted.properties![key]!, depth + 1);
    return object;
  }
  if (schema.type === "array")
    return Array.from({ length: schema.minItems ?? 0 }, () =>
      changeFixtureValue(schema.items!, depth + 1),
    );
  if (schema.type === "boolean") return false;
  if (schema.type === "integer" || schema.type === "number") return schema.minimum ?? 1;
  if (schema.type === "null" || (Array.isArray(schema.type) && schema.type.includes("null")))
    return null;
  if (schema.format === "uuid") return changeTestID;
  if (schema.pattern?.includes("64}")) return changeHash;
  if (schema.pattern?.includes("[1-9]")) return "1";
  return "example";
}

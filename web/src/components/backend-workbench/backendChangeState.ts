import type {
  BackendChangeProposalCommand,
  BackendEffectiveIdentity,
} from "@/api/generated/schemas";
import { changeValueKey } from "./backendChangeFormModel";
import type { ChangeJSON } from "./backendChangeSchemaTypes";

export const changeIdentityKey = (target: BackendEffectiveIdentity["target"]) =>
  changeValueKey(target as unknown as ChangeJSON);
function createdRecord(
  command: BackendChangeProposalCommand,
): { id: string; recordType: "node" | "edge" } | null {
  switch (command.type) {
    case "create_node":
      return { id: command.id, recordType: "node" };
    case "upsert_edge":
      return { id: command.id, recordType: "edge" };
    case "alter_constraint":
      return command.action === "create" ? { id: command.constraintId, recordType: "node" } : null;
    case "alter_index":
      return command.action === "create" ? { id: command.indexId, recordType: "node" } : null;
    case "set_field_mapping":
      return { id: command.mappingId, recordType: "node" };
    default:
      return null;
  }
}
export function changePendingIdentities(
  baseline: BackendEffectiveIdentity[],
  commands: BackendChangeProposalCommand[],
): BackendEffectiveIdentity[] {
  const result = structuredClone(baseline);
  const idOf = (identity: BackendEffectiveIdentity) =>
    identity.target.kind === "source_identity" ? identity.target.source.id : identity.target.id;
  for (const command of commands) {
    if (command.type === "remove_node" || command.type === "remove_edge") {
      for (let index = result.length - 1; index >= 0; index--)
        if (idOf(result[index]!) === command.id) result.splice(index, 1);
    }
    if (command.type === "map_identity") {
      const identity = result.find(
        (item) => changeIdentityKey(item.target) === changeIdentityKey(command.target),
      );
      if (identity) identity.externalKey = command.newExternalKey;
    }
    const created = createdRecord(command);
    if (created && !result.some((identity) => idOf(identity) === created.id))
      result.push({
        target: { kind: "intent_identity", ...created },
        externalKey: null,
        origin: {
          recordType: created.recordType,
          subjectId: created.id,
          selector: { kind: "intent_identity", ...created },
          kind: "intent",
          commandId: command.commandId,
          reason: command.reason,
          sourceClaims: [],
          evidenceIds: [],
        },
      });
  }
  return result;
}

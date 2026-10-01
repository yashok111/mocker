import type {
  ApplyBackendProposalCommandsRequest,
  CreateBackendProposalRequest,
} from "@/api/generated/schemas";
import { parseBrowserSafeJson } from "@/api/preciseJson";

type RecoveryRequest = ApplyBackendProposalCommandsRequest | CreateBackendProposalRequest;

// Only a request awaiting acknowledgement is stored. Local unsaved buffers are
// transient. Recovery never sends a request automatically.
export function readProposalRecovery(
  key: string,
  kind: "create" | "apply",
): RecoveryRequest | null {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return null;
    const input: unknown = parseBrowserSafeJson(raw);
    if (
      typeof input !== "object" ||
      input === null ||
      !("idempotencyKey" in input) ||
      typeof input.idempotencyKey !== "string"
    )
      return null;
    if (kind === "apply") {
      if (
        !("expectedVersion" in input) ||
        typeof input.expectedVersion !== "number" ||
        !Number.isSafeInteger(input.expectedVersion) ||
        input.expectedVersion < 1 ||
        !("draftRevisionId" in input) ||
        typeof input.draftRevisionId !== "string" ||
        !("candidateHash" in input) ||
        typeof input.candidateHash !== "string" ||
        !("commands" in input) ||
        !Array.isArray(input.commands)
      )
        return null;
    } else if (
      !["name", "baseRevisionId", "repositoryId", "datastoreId", "facetKey"].every(
        (field) => field in input && typeof (input as Record<string, unknown>)[field] === "string",
      )
    )
      return null;
    return input as RecoveryRequest;
  } catch {
    return null;
  }
}

export function writeProposalRecovery(key: string, input: RecoveryRequest | null) {
  try {
    if (input) sessionStorage.setItem(key, JSON.stringify(input));
    else sessionStorage.removeItem(key);
  } catch {
    // A storage-disabled browser still retains the exact request in component
    // state for retries while this editor remains open.
  }
}

import type { BackendMaterializationOwner, BackendReadTarget } from "@/api/generated/schemas";
export function materializationArtifactLink(
  projectId: string,
  target: BackendReadTarget,
  owner: BackendMaterializationOwner,
) {
  const { pin, namespace } = owner.pin;
  if (
    namespace.scope !== "local" ||
    !/^\d+$/.test(pin.id) ||
    !/^\d+$/.test(pin.revisionId) ||
    !("contentHash" in pin) ||
    !pin.contentHash ||
    !/^[a-f0-9]{64}$/.test(pin.contentHash)
  )
    return undefined;
  const path =
    pin.kind === "api_design"
      ? "designs"
      : pin.kind === "design_scenario"
        ? "design-scenarios"
        : undefined;
  if (!path) return undefined;
  const params = new URLSearchParams({
    pinnedRevisionId: pin.revisionId,
    pinnedHash: pin.contentHash,
    returnProjectId: projectId,
  });
  if (target.revisionId) params.set("returnRevisionId", target.revisionId);
  if (target.changeProposal) {
    params.set("returnChangeProposalId", target.changeProposal.proposalId);
    params.set("returnProposalRevisionId", target.changeProposal.proposalRevisionId);
  }
  return `/${path}/${pin.id}?${params}`;
}

export function diagramArtifactLink(
  projectId: string,
  target: BackendReadTarget,
  ref: import("@/api/generated/schemas").BackendDiagramRef,
) {
  if (ref.kind === "record") return undefined;
  if (ref.kind === "namespaced_artifact" && ref.namespacedLocator.namespace.scope !== "local")
    return undefined;
  const locator = ref.kind === "artifact" ? ref.locator : ref.namespacedLocator.locator;
  const pin = locator.pin;
  if (
    !("contentHash" in pin) ||
    !pin.contentHash ||
    !/^\d+$/.test(pin.id) ||
    !/^\d+$/.test(pin.revisionId) ||
    !Number.isSafeInteger(Number(pin.id)) ||
    !Number.isSafeInteger(Number(pin.revisionId))
  )
    return undefined;
  const params = new URLSearchParams({
    pinnedRevisionId: pin.revisionId,
    pinnedHash: pin.contentHash,
    projectionView: locator.view,
    returnProjectId: projectId,
  });
  if (target.revisionId) params.set("returnRevisionId", target.revisionId);
  if (target.changeProposal) {
    params.set("returnChangeProposalId", target.changeProposal.proposalId);
    params.set("returnProposalRevisionId", target.changeProposal.proposalRevisionId);
  }
  if (locator.embedded) params.set("embeddedContractId", locator.embedded.contractId);
  return `/${pin.kind === "api_design" ? "designs" : "design-scenarios"}/${pin.id}?${params}`;
}

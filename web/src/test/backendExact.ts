import type {
  BackendReadTarget,
  BackendSourceAssertionItem,
  BackendEffectiveGraphPins,
  BackendSourceReadContext,
} from "@/api/generated/schemas";
export const exactIDs = {
  project: "0197aaf9-5555-7000-8000-000000000001",
  revision: "0197aaf9-5555-7000-8000-000000000002",
  node: "0197aaf9-5555-7000-8000-000000000003",
  evidence: "0197aaf9-5555-7000-8000-000000000004",
  repository: "0197aaf9-5555-7000-8000-000000000005",
  snapshot: "0197aaf9-5555-7000-8000-000000000006",
};
export const exactHash = "a".repeat(64);
export const exactCandidate = {
  importCandidate: { importId: exactIDs.project, importVersion: 7, candidateHash: exactHash },
} satisfies BackendReadTarget;
export function exactEnvelope(target: BackendReadTarget = exactCandidate) {
  const viewSchemaVersion = target.changeProposal
    ? "proposal-graph-v1"
    : target.importCandidate
      ? "import-candidate-v1"
      : "6";
  const pins: BackendEffectiveGraphPins = {
    targetHash: exactHash,
    viewSchemaVersion,
    structuralSchemaVersion: "6",
    effectiveSemanticHash: exactHash,
    baseRevisionId: exactIDs.revision,
    baseSemanticHash: exactHash,
    sourceVectorHash: exactHash,
    sourceSnapshotIds: [exactIDs.snapshot],
    artifactPins: [],
    artifactContext: null,
  };
  return { target, viewSchemaVersion, pins };
}
export function exactClaim(providerNamespace = "compiler"): BackendSourceAssertionItem {
  const freshness = {
    status: "current",
    confirmedSnapshotId: exactIDs.snapshot,
    reasons: [],
  } as const;
  return {
    assertion: {
      recordType: "node",
      recordId: exactIDs.node,
      owner: {
        repositoryId: exactIDs.repository,
        providerNamespace,
        profile: "foundation-graph-v1",
      },
      externalKey: "handler",
      assertionHash: exactHash,
      payload: {
        recordType: "node",
        kind: "handler",
        name: "<em>Orders</em>",
        parentId: null,
        attributes: { language: "Go" },
      },
      evidenceIds: [exactIDs.evidence],
      dependencyClaims: [],
      freshness: { ...freshness, reasons: [] },
      fieldCurrentness: [],
    },
    currentness: {
      recordType: "node",
      recordId: exactIDs.node,
      repositoryId: exactIDs.repository,
      providerNamespace,
      assertionHash: exactHash,
      own: { ...freshness, reasons: [] },
      dependency: { ...freshness, reasons: [] },
      fields: [],
    },
    selections: [],
    conflicts: [],
  };
}
export function exactSource(): BackendSourceReadContext {
  return {
    sourceContentHash: exactHash,
    sourceVector: { documentVersion: "source-vector-v1", partitions: [], snapshots: [] },
    identities: ["compiler", "reflection"].map((providerNamespace) => ({
      recordType: "node",
      id: exactIDs.node,
      repositoryId: exactIDs.repository,
      providerNamespace,
      externalKey: "handler",
      assertionHash: exactHash,
    })),
    assertionRefs: [],
    selections: [],
    currentness: [],
    legacyProofBases: [],
  };
}
export const exactCoverage = {
  coverage: {
    status: "partial",
    denominator: null,
    knownObjects: 1,
    gaps: ["Delivery not proven"],
  },
  inventory: [],
  snapshots: [],
};
export function exactNode() {
  return {
    id: exactIDs.node,
    kind: "handler",
    name: "Orders",
    attributes: { language: "Go" },
    parentId: null,
    evidenceIds: [exactIDs.evidence],
    source: exactSource(),
  };
}

/**
 * isReplayList names the four replay lists the project page reads on mount:
 * BackendReplay (be06f56) sits in a <details> on BackendProjectPage and its
 * targets, profiles, packages and runs are bare arrays in api/openapi.json,
 * not the `{ items, nextCursor }` page every other backend list returns. A
 * page-shaped catch-all answer makes `profiles.data.find` throw and React
 * unmounts the whole project page, so a fixture routes these first.
 */
export function isReplayList(path: string): boolean {
  return /\/api\/backend-projects\/[^/]+\/replay\/(targets|profiles|packages|runs)$/.test(path);
}

import type { BackendNode, BackendProposalDetail } from "@/api/generated/schemas";

const common = {
  sourceKind: "sql" as const,
  dialect: "postgresql" as const,
  analysisStatus: "complete" as const,
  gaps: [],
  evidenceIds: [],
  sourceSnapshotId: "snapshot",
  freshness: { status: "current" as const, confirmedSnapshotId: "snapshot", reasons: [] },
};
export const proposalNodes: BackendNode[] = [
  {
    id: "db",
    externalKey: "db",
    kind: "datastore",
    name: "Orders",
    parentId: null,
    evidenceIds: [],
    attributes: {
      relational: {
        facets: {
          sql: {
            ...common,
            qualifiedName: "orders",
            databaseName: "orders",
            nativeDefinition: null,
          },
        },
      },
    },
  },
  ...["orders", "users"].map((id): BackendNode => ({
    id,
    externalKey: id,
    kind: "table",
    name: id,
    parentId: "db",
    evidenceIds: [],
    attributes: {
      facets: {
        sql: {
          ...common,
          qualifiedName: `public.${id}`,
          nativeDefinition: null,
          columnsStatus: "complete",
          constraintsStatus: "complete",
        },
      },
    },
  })),
  ...(
    [
      ["user-id", "orders", "user_id"],
      ["tenant-id", "orders", "tenant_id"],
      ["users-id", "users", "id"],
      ["users-tenant", "users", "tenant_id"],
    ] as const
  ).map(([id, parentId, name]): BackendNode => ({
    id,
    externalKey: id,
    kind: "column",
    name,
    parentId,
    evidenceIds: [],
    attributes: {
      facets: {
        sql: {
          ...common,
          nativeType: { status: "known", value: "bigint" },
          typeFamily: { status: "known", value: "integer" },
          nullable: { status: "known", value: true },
          defaultExpression: { status: "known", value: null },
          generatedExpression: { status: "known", value: null },
          identity: { status: "unknown", reason: "not declared" },
          ordinal: { status: "known", value: 1 },
        },
      },
    },
  })),
];
export const proposalDetail: BackendProposalDetail = {
  proposal: {
    id: "proposal",
    projectId: "project",
    version: 1,
    name: "Required users",
    status: "draft",
    baseRevisionId: "base",
    baseSemanticHash: "a".repeat(64),
    repositoryId: "repository",
    datastoreId: "db",
    facetKey: "sql",
    draftRevisionId: "draft",
    draftHash: "b".repeat(64),
    createdAt: "2026-10-01T00:00:00Z",
    updatedAt: "2026-10-01T00:00:00Z",
  },
  revision: {
    id: "draft",
    proposalId: "proposal",
    parentRevisionId: null,
    documentVersion: "proposal-relational-v1",
    semanticHash: "b".repeat(64),
    baseRevisionId: "base",
    baseSemanticHash: "a".repeat(64),
    sourceSnapshotIds: ["snapshot"],
    artifactPins: [],
    commands: [],
    overlays: [],
    criteria: [],
    author: "user",
    summary: "Created",
    createdAt: "2026-10-01T00:00:00Z",
  },
  history: [
    {
      id: "draft",
      parentRevisionId: null,
      semanticHash: "b".repeat(64),
      author: "user",
      summary: "Created",
      createdAt: "2026-10-01T00:00:00Z",
    },
  ],
  nextCursor: "",
  lastApplyReceipt: null,
  baseOutdated: false,
  currentSourceRevisionId: "base",
  currentSourceSemanticHash: "a".repeat(64),
  effectiveGraphHash: "c".repeat(64),
};

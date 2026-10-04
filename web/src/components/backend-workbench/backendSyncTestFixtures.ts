import { vi } from "vitest";
import { json } from "@/test/http";
import type {
  BackendComposedImportSession,
  BackendImportPreviewResponse,
  BackendSourceAssertionConflict,
  BeginBackendImportRequest,
  PutBackendImportBatchRequest,
} from "@/api/generated/schemas";
import { parseSourceInventory, sourceProfiles } from "./backendSourceInputs";

export const syncIds = {
  project: "0197aaf9-5555-7000-8000-000000000001",
  revision: "0197aaf9-5555-7000-8000-000000000002",
  repository: "0197aaf9-5555-7000-8000-000000000003",
  snapshot: "0197aaf9-5555-7000-8000-000000000004",
  incoming: "0197aaf9-5555-7000-8000-000000000005",
  session: "0197aaf9-5555-7000-8000-000000000006",
  node: "0197aaf9-5555-7000-8000-000000000007",
  proof: "0197aaf9-5555-7000-8000-000000000008",
  committed: "0197aaf9-5555-7000-8000-000000000009",
  later: "0197aaf9-5555-7000-8000-000000000010",
};
export const syncHash = "a".repeat(64);
export const syncManifest = {
  repositoryName: "education",
  provider: {
    name: "collector",
    version: "1",
    namespace: "ast",
    method: "ast" as const,
    profiles: [...sourceProfiles],
    limitations: [],
  },
  snapshot: {
    dirty: false,
    consistency: "verified" as const,
    capturedAt: "2026-10-03T10:00:00Z",
    files: [
      {
        path: "src/main.go",
        contentHash: syncHash,
        fileType: "go",
        analysisStatus: "analyzed" as const,
      },
    ],
  },
};
export const syncInventory = parseSourceInventory(
  [
    "files",
    "endpoints",
    "datastores",
    "migrations",
    "producers",
    "consumers",
    "jobs",
    "contracts",
    "tests",
  ].map((category) => ({
    category,
    status: "complete",
    knownCount: category === "files" ? 1 : 0,
    denominator: category === "files" ? 1 : 0,
    discoverySource: "synthetic collector fixture",
    gaps: [],
    reason: "",
  })),
);
export const syncProject = {
  id: syncIds.project,
  name: "Fixture",
  version: 3,
  currentRevisionId: syncIds.revision,
  repositories: [
    {
      id: syncIds.repository,
      projectId: syncIds.project,
      name: "education",
      createdAt: syncManifest.snapshot.capturedAt,
    },
  ],
  capabilities: [],
  createdAt: syncManifest.snapshot.capturedAt,
  updatedAt: syncManifest.snapshot.capturedAt,
};
export function syncSnapshot(schema = "6") {
  return {
    ...syncManifest.snapshot,
    id: syncIds.snapshot,
    repositoryId: syncIds.repository,
    manifestHash: syncHash,
    provider: {
      ...syncManifest.provider,
      profiles: schema === "5" ? sourceProfiles.slice(0, 5) : [...sourceProfiles],
    },
    role: schema === "5" ? "primary" : "active_source",
  };
}
export function syncSession(): BackendComposedImportSession {
  return {
    id: syncIds.session,
    projectId: syncIds.project,
    baseRevisionId: syncIds.revision,
    repositoryId: syncIds.repository,
    snapshotId: syncIds.incoming,
    manifestHash: syncHash,
    manifest: structuredClone(syncManifest),
    inventory: syncInventory,
    mode: "composed",
    profile: "composed-source-v1",
    sourceScope: { kind: "reconcile", repositoryId: syncIds.repository, providerNamespace: "ast" },
    scopeStatus: { status: "complete", gaps: [] },
    syncPolicy: "whole-source-v1",
    state: "collecting",
    version: 1,
    candidateHash: null,
    acceptedBatchCount: 0,
    createdAt: syncManifest.snapshot.capturedAt,
    updatedAt: syncManifest.snapshot.capturedAt,
  };
}
export function syncCommands(externalKey = "handler") {
  return [
    {
      op: "upsert_node",
      node: {
        externalKey,
        kind: "handler",
        name: "Handler",
        attributes: {},
        evidenceKeys: ["own-proof"],
      },
    },
    {
      op: "upsert_evidence",
      evidence: {
        externalKey: "own-proof",
        subjectType: "node",
        subjectKey: externalKey,
        method: "ast",
        status: "explicit",
        explanation: "Synthetic fixture proof",
        source: {
          repositoryId: syncIds.repository,
          snapshotId: syncIds.incoming,
          file: "src/main.go",
          contentHash: syncHash,
          startLine: 1,
          endLine: 2,
        },
      },
    },
  ];
}
export function syncAssertion(namespace = "ast", hash = "c".repeat(64)) {
  return {
    recordType: "node" as const,
    recordId: syncIds.node,
    externalKey: "handler",
    owner: {
      repositoryId: syncIds.repository,
      providerNamespace: namespace,
      profile: "foundation-graph-v1",
    },
    assertionHash: hash,
    payload: {
      recordType: "node" as const,
      kind: "handler" as const,
      name: "Handler",
      attributes: {},
    },
    evidenceIds: [syncIds.proof],
    dependencyClaims: [],
    freshness: { status: "current" as const, confirmedSnapshotId: syncIds.snapshot, reasons: [] },
    fieldCurrentness: [],
  };
}
export function syncConflict(hash = "e".repeat(64)): BackendSourceAssertionConflict {
  return {
    recordType: "node",
    id: syncIds.node,
    property: { kind: "name" },
    conflictHash: hash,
    contenders: ["ast", "other"].map((providerNamespace, i) => ({
      owner: {
        repositoryId: syncIds.repository,
        providerNamespace,
        profile: "foundation-graph-v1",
      },
      assertionHash: String(i + 1).repeat(64),
      value: { present: true, value: i ? "Other" : "Original" },
      evidenceIds: [syncIds.proof],
      currentness: {
        recordType: "node",
        recordId: syncIds.node,
        repositoryId: syncIds.repository,
        providerNamespace,
        assertionHash: String(i + 1).repeat(64),
        own: { status: "current", confirmedSnapshotId: syncIds.snapshot, reasons: [] },
        dependency: { status: "current", confirmedSnapshotId: syncIds.snapshot, reasons: [] },
        fields: [],
      },
    })),
  };
}

export function sourceSyncServer(
  options: {
    schema?: "5" | "6";
    existing?: boolean;
    lostBegin?: boolean;
    lostBatch?: boolean;
    lostCommit?: boolean;
    lostAbort?: boolean;
    conflicts?: boolean;
    paged?: boolean;
    metadataConflict?: boolean;
    batchLimit?: boolean;
    closureLimit?: boolean;
  } = {},
) {
  const schema = options.schema ?? "6";
  let project = structuredClone(syncProject);
  let session = syncSession();
  let preview: BackendImportPreviewResponse | null = null;
  let committedRevisionId: string | null = null;
  let failStatus = false,
    decisions = 0,
    commits = 0,
    begins = 0;
  let beginReceipt = structuredClone(session);
  const batchReceipts = new Map<string, unknown>(),
    commitReceipts = new Map<string, unknown>(),
    abortReceipts = new Map<string, unknown>();
  const calls: { path: string; method: string; body: string }[] = [];
  const snapshot = syncSnapshot(schema);
  const revision = {
    id: syncIds.revision,
    projectId: syncIds.project,
    parentRevisionId: null,
    schemaVersion: schema,
    semanticHash: syncHash,
    sourceSnapshotIds: [syncIds.snapshot],
    artifactPins: [],
    coverage: { status: "complete", gaps: [], limitations: [] },
    author: "fixture",
    summary: "Synthetic source fixture",
    createdAt: syncManifest.snapshot.capturedAt,
  };
  const partition = {
    repositoryId: syncIds.repository,
    providerNamespace: "ast",
    snapshotId: syncIds.snapshot,
    provider: snapshot.provider,
    inventory: syncInventory,
    scopeStatus: { status: "complete", gaps: [] },
  };
  const coverage = {
    coverage: revision.coverage,
    snapshots: [snapshot],
    inventory: syncInventory,
    reconciliationGaps: [],
    ...(schema === "6"
      ? {
          viewSchemaVersion: "6",
          source: {
            sourceVector: {
              documentVersion: "source-vector-v1",
              partitions: [partition],
              snapshots: [snapshot],
            },
            sourceContentHash: syncHash,
            identities: [],
            assertionRefs: [],
            selections: [],
            currentness: [],
            legacyProofBases: [],
          },
        }
      : {}),
  };
  const error = (status: number, code: string, message: string) =>
    json(status, { error: { code, message } });
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost"),
      path = url.pathname,
      method = init?.method ?? "GET",
      body = typeof init?.body === "string" ? init.body : "";
    calls.push({ path: path + url.search, method, body });
    if (path === `/api/backend-projects/${syncIds.project}`) return json(200, project);
    if (path === `/api/backend-projects/${syncIds.project}/revisions/${syncIds.revision}`)
      return json(200, revision);
    if (path.endsWith(`/revisions/${syncIds.revision}/coverage`)) return json(200, coverage);
    if (path.endsWith("/imports") && method === "POST") {
      const data = JSON.parse(body) as BeginBackendImportRequest;
      begins++;
      if (begins === 1) {
        session = {
          ...syncSession(),
          manifest: data.manifest,
          inventory: data.inventory,
          sourceScope: data.sourceScope!,
          scopeStatus: data.scopeStatus!,
          syncPolicy: data.syncPolicy!,
          ...(data.profileExtension
            ? {
                profileExtension:
                  data.profileExtension as BackendComposedImportSession["profileExtension"],
              }
            : {}),
        };
        beginReceipt = structuredClone(session);
        if (options.lostBegin) throw new TypeError("lost Begin response");
      }
      return json(200, beginReceipt);
    }
    if (path.endsWith("/imports"))
      return json(200, { items: options.existing ? [session] : [], nextCursor: "" });
    if (path.endsWith(`/imports/${syncIds.session}`))
      return failStatus
        ? error(503, "backend_unavailable", "Status unavailable")
        : json(200, { session, preview, committedRevisionId, acceptedBatches: [], nextCursor: "" });
    if (path.includes("/batches/")) {
      const batchId = path.split("/").at(-1)!;
      if (batchReceipts.has(batchId)) return json(200, batchReceipts.get(batchId));
      if (options.batchLimit)
        return error(
          413,
          "backend_import_limit",
          "Incremental affected scope exceeds 100000 visits; use whole-source-v1",
        );
      const data = JSON.parse(body) as PutBackendImportBatchRequest;
      if (data.expectedImportVersion !== session.version)
        return error(409, "backend_import_version_conflict", "Import changed");
      decisions += data.commands.filter((command) => command.op === "resolve_assertion").length;
      session = {
        ...session,
        version: session.version + 1,
        state: "collecting",
        candidateHash: null,
        acceptedBatchCount: session.acceptedBatchCount + 1,
      };
      preview = null;
      const receipt = {
        batchId,
        acceptedVersion: session.version,
        payloadHash: data.payloadHash,
        identities: [{ recordType: "node", externalKey: "handler", id: syncIds.node }],
      };
      batchReceipts.set(batchId, receipt);
      if (options.lostBatch) {
        failStatus = true;
        throw new TypeError("lost batch response");
      }
      return json(200, receipt);
    }
    if (path.endsWith("/preview")) {
      if (options.closureLimit)
        return error(
          413,
          "backend_import_limit",
          "Incremental affected scope exceeds 100000 visits; use whole-source-v1",
        );
      const bodyVersion = (JSON.parse(body) as { expectedImportVersion: number })
        .expectedImportVersion;
      if (bodyVersion !== session.version)
        return error(409, "backend_import_version_conflict", "Import changed");
      session = {
        ...session,
        version: session.version + 1,
        state: options.conflicts && decisions < 2 ? "needs_resolution" : "ready",
      };
      session.candidateHash =
        session.state === "ready" ? session.version.toString(16).padStart(64, "b") : null;
      preview = {
        sessionId: session.id,
        version: session.version,
        state: session.state === "ready" ? "ready" : "needs_resolution",
        candidateHash: session.candidateHash,
        modelSchemaVersion: "6",
        summary: { nodes: 1, edges: 0, evidence: 1, unresolved: 0 },
        diagnostics: [],
        affectedScope: {
          affected: [{ recordType: "node", id: syncIds.node }],
          validationDependencies: [],
          foreignDependencies: [],
          untouchedCount: 0,
          gaps: [],
          availabilityChanges: [],
        },
      };
      return json(200, preview);
    }
    if (path.endsWith("/changes")) {
      const recordType = url.searchParams.get("recordType"),
        cursor = url.searchParams.get("cursor");
      return json(200, {
        sessionId: session.id,
        previewVersion: Number(url.searchParams.get("previewVersion")),
        candidateHash: preview?.candidateHash ?? null,
        recordType,
        items:
          recordType === "assertion_conflict"
            ? [
                {
                  recordType,
                  assertionConflict: {
                    ...syncConflict(decisions ? "f".repeat(64) : "e".repeat(64)),
                    ...(cursor ? { id: syncIds.later } : {}),
                  },
                },
              ]
            : [],
        nextCursor: options.paged && recordType === "assertion_conflict" && !cursor ? "page-2" : "",
      });
    }
    if (path.endsWith("/assertions")) {
      const candidate = path.includes("/candidate/");
      if (!candidate && schema === "5")
        return error(422, "backend_unsupported_scope", "Source5 assertions unavailable");
      if (candidate && session.state !== "ready")
        return error(409, "backend_import_preview_conflict", "Candidate not ready");
      const a = syncAssertion();
      const currentness = {
        recordType: a.recordType,
        recordId: a.recordId,
        repositoryId: a.owner.repositoryId,
        providerNamespace: a.owner.providerNamespace,
        assertionHash: a.assertionHash,
        own: a.freshness,
        dependency: a.freshness,
        fields: [],
      };
      return json(200, {
        documentVersion: "source-assertions-v1",
        viewSchemaVersion: candidate ? "import-candidate-v1" : "6",
        target: candidate
          ? {
              importCandidate: {
                importId: session.id,
                importVersion: Number(url.searchParams.get("importVersion")),
                candidateHash: url.searchParams.get("candidateHash"),
              },
            }
          : { revisionId: syncIds.revision },
        pins: {
          baseRevisionId: syncIds.revision,
          baseSemanticHash: syncHash,
          targetHash: syncHash,
          effectiveSemanticHash: syncHash,
          sourceVectorHash: syncHash,
          viewSchemaVersion: candidate ? "import-candidate-v1" : "6",
          structuralSchemaVersion: "6",
          sourceSnapshotIds: [snapshot.id],
          artifactPins: [],
          artifactContext: null,
        },
        basis: "source",
        items: [{ assertion: a, currentness, selections: [], conflicts: [] }],
        nextCursor: "",
      });
    }
    if (path.endsWith("/commit")) {
      const data = JSON.parse(body) as { idempotencyKey: string; expectedVersion: number };
      if (commitReceipts.has(data.idempotencyKey))
        return json(200, commitReceipts.get(data.idempotencyKey));
      commits++;
      if (options.metadataConflict && commits === 1) {
        project = { ...project, version: project.version + 1 };
        return error(409, "backend_version_conflict", "Metadata version changed");
      }
      if (data.expectedVersion !== project.version)
        return error(409, "backend_version_conflict", "Project changed");
      committedRevisionId = syncIds.committed;
      project = { ...project, version: project.version + 1, currentRevisionId: syncIds.committed };
      session = { ...session, state: "committed", version: session.version + 1 };
      const receipt = {
        project,
        revision: { ...revision, id: syncIds.committed, schemaVersion: "6" },
        sessionId: session.id,
      };
      commitReceipts.set(data.idempotencyKey, receipt);
      if (options.lostCommit) {
        failStatus = true;
        throw new TypeError("lost Commit response");
      }
      return json(200, receipt);
    }
    if (path.endsWith("/abort")) {
      const data = JSON.parse(body) as { idempotencyKey: string };
      if (abortReceipts.has(data.idempotencyKey))
        return json(200, abortReceipts.get(data.idempotencyKey));
      session = { ...session, state: "aborted", version: session.version + 1 };
      abortReceipts.set(data.idempotencyKey, structuredClone(session));
      if (options.lostAbort) {
        failStatus = true;
        throw new TypeError("lost Abort response");
      }
      return json(200, session);
    }
    return error(500, "unrouted", path);
  });
  vi.stubGlobal("fetch", fetchMock);
  return {
    fetchMock,
    calls,
    session: () => session,
    project: () => project,
    recover: () => {
      failStatus = false;
    },
    advanceProject: () => {
      project = { ...project, version: project.version + 5, currentRevisionId: syncIds.later };
    },
  };
}

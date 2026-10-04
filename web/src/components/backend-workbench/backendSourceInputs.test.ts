import { expect, it } from "vitest";
import { parseBrowserSafeJson } from "@/api/preciseJson";
import {
  buildSourceBegin,
  parseSourceManifest,
  parseSourceInventory,
  parseCollectorBatch,
  sourceFileChanges,
  sourceProfiles,
} from "./backendSourceInputs";

const repositoryId = "0197aaf9-5555-7000-8000-000000000001";
const snapshotId = "0197aaf9-5555-7000-8000-000000000002";
const revisionId = "0197aaf9-5555-7000-8000-000000000003";
const hash = "a".repeat(64);
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
        contentHash: hash,
        fileType: "go",
        analysisStatus: "analyzed" as const,
      },
    ],
  },
};
export const syncInventory = [
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
  discoverySource: "collector manifest",
  gaps: [],
  reason: "",
}));

it("constructs every source scope without carrying fields from another tag", () => {
  for (const scope of [
    { kind: "add_repository" },
    { kind: "add_provider", repositoryId },
    { kind: "reconcile", repositoryId, providerNamespace: "ast" },
    {
      kind: "migrate_provider",
      repositoryId,
      fromProviderNamespace: "ast",
      fromSnapshotId: snapshotId,
      reason: "Reviewed migration",
    },
  ] as const) {
    const manifest = structuredClone(syncManifest);
    if (scope.kind === "add_provider" || scope.kind === "migrate_provider")
      manifest.provider.namespace = "new-provider";
    const request = buildSourceBegin({
      projectVersion: 3,
      baseRevisionId: revisionId,
      baseSchema: "6",
      sourceScope: scope,
      scopeStatus: { status: "complete", gaps: [] },
      policy: "whole-source-v1",
      extension: false,
      manifest: parseSourceManifest(manifest),
      inventory: parseSourceInventory(syncInventory),
      partitions: [{ repositoryId, snapshotId, provider: syncManifest.provider }],
      idempotencyKey: "stable-begin",
    });
    expect(request.sourceScope).toEqual(scope);
    expect(request).not.toHaveProperty("repositoryId");
    expect(request).not.toHaveProperty("changeManifest");
    expect(request).not.toHaveProperty("graphScope");
  }
});

it("requires explicit retained five-profile extension and forbids incremental extension", () => {
  const input = {
    projectVersion: 3,
    baseRevisionId: revisionId,
    baseSchema: "6",
    sourceScope: { kind: "reconcile" as const, repositoryId, providerNamespace: "ast" },
    scopeStatus: { status: "complete" as const, gaps: [] },
    policy: "whole-source-v1" as const,
    extension: false,
    manifest: parseSourceManifest(syncManifest),
    inventory: parseSourceInventory(syncInventory),
    partitions: [
      {
        repositoryId,
        snapshotId,
        provider: { ...syncManifest.provider, profiles: sourceProfiles.slice(0, 5) },
      },
    ],
    idempotencyKey: "extension",
  };
  expect(() => buildSourceBegin(input)).toThrow(/расширение/i);
  expect(buildSourceBegin({ ...input, extension: true }).profileExtension).toEqual({
    fromProfile: "events-service-v1",
    toProfile: "composed-source-v1",
  });
  expect(() =>
    buildSourceBegin({
      ...input,
      extension: true,
      policy: "incremental-source-v1",
      changeManifest: { scope: "affected-subgraph", files: [], affectedRoots: [] },
    }),
  ).toThrow(/incremental/i);
  expect(() =>
    buildSourceBegin({
      ...input,
      extension: true,
      manifest: { ...input.manifest, provider: { ...input.manifest.provider, version: "2" } },
    }),
  ).toThrow(/провайдер/i);
});

it("rejects unsafe and malformed upload values without changing hashes", () => {
  expect(() =>
    parseSourceManifest({
      ...syncManifest,
      snapshot: {
        ...syncManifest.snapshot,
        files: [{ ...syncManifest.snapshot.files[0], path: "../secret" }],
      },
    }),
  ).toThrow();
  expect(() =>
    parseSourceInventory(
      parseBrowserSafeJson(
        JSON.stringify(syncInventory).replace('"knownCount":1', '"knownCount":9007199254740993'),
      ),
    ),
  ).toThrow();
  expect(() =>
    parseCollectorBatch({
      commands: [
        {
          op: "upsert_node",
          node: {
            externalKey: "x",
            kind: "handler",
            name: "x",
            parentKey: "old",
            attributes: {},
            evidenceKeys: [],
          },
        },
      ],
    }),
  ).toThrow();
  expect(parseSourceManifest(syncManifest).snapshot.files[0]?.contentHash).toBe(hash);
  expect(
    sourceFileChanges(syncManifest.snapshot.files, [
      { ...syncManifest.snapshot.files[0]!, contentHash: "b".repeat(64) },
    ]),
  ).toEqual([
    { kind: "modified", path: "src/main.go", beforeHash: hash, afterHash: "b".repeat(64) },
  ]);
});

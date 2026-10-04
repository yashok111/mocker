import { afterEach, expect, it, vi } from "vitest";
import { json } from "@/test/http";
import type { BackendImportPreviewResponse } from "@/api/generated/schemas";
import { loadSyncAssertions, loadSyncBase } from "./backendSyncReads";
import {
  sourceSyncServer,
  syncAssertion,
  syncHash,
  syncIds,
  syncSession,
} from "./backendSyncTestFixtures";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

async function fixture(schema: "5" | "6" = "5") {
  const server = sourceSyncServer({ schema });
  const base = await loadSyncBase(syncIds.project, syncIds.revision);
  const session = {
    ...syncSession(),
    state: "ready" as const,
    version: 4,
    candidateHash: "b".repeat(64),
    acceptedBatchCount: 1,
  };
  const preview: BackendImportPreviewResponse = {
    sessionId: session.id,
    version: session.version,
    state: "ready",
    candidateHash: session.candidateHash,
    modelSchemaVersion: "6",
    summary: { nodes: 1, edges: 0, evidence: 1, unresolved: 0 },
    diagnostics: [],
  };
  const page = {
    target:
      schema === "5"
        ? {
            importCandidate: {
              importId: session.id,
              importVersion: session.version,
              candidateHash: session.candidateHash,
            },
          }
        : { revisionId: base.revision.id },
    pins: { baseRevisionId: base.revision.id, baseSemanticHash: base.revision.semanticHash },
    items: [
      syncAssertion(),
      syncAssertion("incoming"),
      { ...syncAssertion(), owner: { ...syncAssertion().owner, repositoryId: syncIds.later } },
    ].map((assertion) => ({ assertion })),
    nextCursor: "next",
  };
  server.fetchMock.mockImplementation(async () => json(200, page));
  return { server, base, session, preview, page };
}

it.each(["add_provider", "migrate_provider"] as const)(
  "recovers resumed source5 %s using exact candidate pins and only baseline owners",
  async (kind) => {
    const { server, base, session, preview } = await fixture();
    session.manifest.provider.namespace = "incoming";
    session.sourceScope =
      kind === "add_provider"
        ? { kind, repositoryId: syncIds.repository }
        : {
            kind,
            repositoryId: syncIds.repository,
            fromProviderNamespace: "ast",
            fromSnapshotId: syncIds.snapshot,
            reason: "Migration",
          };
    const result = await loadSyncAssertions(base, session, preview, "node", "old-page");
    expect(result.items.map((a) => a.owner.providerNamespace)).toEqual(["ast"]);
    expect(result.nextCursor).toBe("next");
    expect(String(server.fetchMock.mock.calls.at(-1)?.[0])).toContain(
      `/imports/${syncIds.session}/candidate/assertions?`,
    );
    expect(String(server.fetchMock.mock.calls.at(-1)?.[0])).toContain("importVersion=4");
    expect(String(server.fetchMock.mock.calls.at(-1)?.[0])).toContain("cursor=old-page");
  },
);

it("requires a baseline hash source after same-provider source5 reconciliation", async () => {
  const { server, base, session, preview } = await fixture();
  server.fetchMock.mockClear();
  await expect(loadSyncAssertions(base, session, preview, "node", "")).rejects.toThrow(
    "прежний assertionHash",
  );
  expect(server.fetchMock).not.toHaveBeenCalled();
});

it("requires explicit READY bootstrap and does not read candidate assertions during NEEDS_RESOLUTION", async () => {
  const { server, base, session, preview } = await fixture();
  session.acceptedBatchCount = 0;
  session.state = "needs_resolution" as typeof session.state;
  server.fetchMock.mockClear();
  await expect(
    loadSyncAssertions(
      base,
      session,
      { ...preview, state: "needs_resolution", candidateHash: null },
      "node",
      "",
    ),
  ).rejects.toThrow("READY");
  expect(server.fetchMock).not.toHaveBeenCalled();
});

it.each(["target", "baseRevisionId", "baseSemanticHash"] as const)(
  "rejects a wrong %s pin in a source5 bootstrap response",
  async (pin) => {
    const { base, session, preview, page } = await fixture();
    session.acceptedBatchCount = 0;
    if (pin === "target" && page.target.importCandidate)
      page.target.importCandidate.candidateHash = "d".repeat(64);
    else if (pin === "baseRevisionId") page.pins.baseRevisionId = syncIds.later;
    else if (pin === "baseSemanticHash") page.pins.baseSemanticHash = "d".repeat(64);
    await expect(loadSyncAssertions(base, session, preview, "node", "")).rejects.toThrow(/друг/);
  },
);

it("never offers a cross-repository identity for add_repository", async () => {
  const { server, base, session, preview } = await fixture();
  session.sourceScope = { kind: "add_repository" };
  server.fetchMock.mockClear();
  await expect(loadSyncAssertions(base, session, preview, "node", "")).rejects.toThrow(
    "новом репозитории",
  );
  expect(server.fetchMock).not.toHaveBeenCalled();
});

it("reads source6 baseline assertions by immutable revision even after uploads", async () => {
  const { server, base, session, preview } = await fixture("6");
  const result = await loadSyncAssertions(base, session, preview, "node", "");
  expect(result.items.map((a) => a.assertionHash)).toEqual(["c".repeat(64)]);
  expect(String(server.fetchMock.mock.calls.at(-1)?.[0])).toContain(
    `/revisions/${syncIds.revision}/assertions?`,
  );
  expect(base.revision.semanticHash).toBe(syncHash);
});

// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import {
  applyBackendProposalCommands,
  queryBackendGraph,
} from "./generated/backend-projects/backend-projects";
import type { QueryBackendGraphRequest } from "./generated/schemas";

afterEach(() => vi.unstubAllGlobals());

it("sends an exact proposal target through the generated client", async () => {
  const fetch = vi
    .fn()
    .mockResolvedValue(new Response('{"nodes":[],"edges":[],"nextCursor":""}', { status: 200 }));
  vi.stubGlobal("fetch", fetch);
  const input: QueryBackendGraphRequest = {
    proposal: { proposalId: "proposal", proposalRevisionId: "draft" },
    recordType: "nodes",
  };
  await queryBackendGraph("project", input);
  expect(fetch).toHaveBeenCalledWith(
    "/api/backend-projects/project/graph/query",
    expect.objectContaining({ body: JSON.stringify(input) }),
  );
});

it("refuses an unsafe proposal version before sending edits", async () => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  await expect(
    applyBackendProposalCommands("project", "proposal", {
      expectedVersion: Number.MAX_SAFE_INTEGER + 1,
      draftRevisionId: "draft",
      candidateHash: "a".repeat(64),
      idempotencyKey: "save",
      commands: [
        {
          type: "alter_column",
          commandId: "required",
          reason: "Required",
          columnId: "column",
          nullable: false,
        },
      ],
    }),
  ).rejects.toThrow(/без потери точности/);
  expect(fetch).not.toHaveBeenCalled();
});

it("refuses an unsafe nested number in a proposal response", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(
        new Response(
          '{"revision":{"overlays":[{"values":{"ordinal":{"status":"known","value":9007199254740993}}}]}}',
          { status: 200 },
        ),
      ),
  );
  await expect(
    queryBackendGraph("project", {
      proposal: { proposalId: "proposal", proposalRevisionId: "draft" },
      recordType: "nodes",
    }),
  ).rejects.toThrow(/без потери точности/);
});

// @ts-expect-error Exactly one pinned target is required.
const missing: QueryBackendGraphRequest = { recordType: "nodes" };
// @ts-expect-error Source and proposal targets cannot be combined.
const mixed: QueryBackendGraphRequest = {
  revisionId: "source",
  proposal: { proposalId: "proposal", proposalRevisionId: "draft" },
  recordType: "nodes",
};
void missing;
void mixed;

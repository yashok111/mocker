import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import type { BackendProposalNodeRead } from "@/api/generated/schemas";
import { BackendDatabaseInspector } from "./BackendDatabaseInspector";
import { proposalNodes } from "./backendProposalTestFixtures";

const api = vi.hoisted(() => ({
  getBackendProposalNode: vi.fn(),
  getBackendProposalEvidence: vi.fn(),
  getBackendProposalCoverage: vi.fn(),
  queryBackendGraph: vi.fn(),
  getBackendNode: vi.fn(),
  getBackendEvidence: vi.fn(),
}));
vi.mock("@/api/generated/backend-projects/backend-projects", () => api);
it("shows source nullable and saved NOT NULL intent with their distinct origins", async () => {
  const basis = {
    revisionId: "0197aaf9-5555-7000-8000-000000000021",
    semanticHash: "a".repeat(64),
    subjectId: "user-id",
    facetKey: "sql",
  };
  const response: BackendProposalNodeRead = {
    viewSchemaVersion: "proposal-relational-v1",
    proposalPins: {
      proposalId: "0197aaf9-5555-7000-8000-000000000022",
      proposalRevisionId: "0197aaf9-5555-7000-8000-000000000023",
      proposalSemanticHash: "b".repeat(64),
      baseRevisionId: "0197aaf9-5555-7000-8000-000000000021",
      baseSemanticHash: "a".repeat(64),
      repositoryId: "repository",
      datastoreId: "db",
      facetKey: "sql",
      effectiveGraphHash: "c".repeat(64),
    },
    proposalProjection: {
      id: "user-id",
      kind: "column",
      name: "user_id",
      parentId: "orders",
      sourceRecord: proposalNodes.find((node) => node.id === "user-id")!,
      effectiveFacet: {
        origin: "proposal",
        proposalId: "0197aaf9-5555-7000-8000-000000000022",
        proposalRevisionId: "0197aaf9-5555-7000-8000-000000000023",
        facetKey: "sql",
        subjectId: "user-id",
        base: basis,
        values: {
          nativeType: { status: "known", value: "bigint" },
          typeFamily: { status: "known", value: "integer" },
          nullable: { status: "known", value: false },
          defaultExpression: { status: "known", value: null },
          generatedExpression: { status: "known", value: null },
          identity: { status: "unknown", reason: "not declared" },
          ordinal: { status: "known", value: 1 },
        },
        propertyOrigins: {
          "/nullable": {
            kind: "intent",
            commandId: "required",
            reason: "Require users",
            evidenceIds: [],
          },
        },
        basisEvidenceIds: [],
        limitations: ["Runtime unverified"],
      },
    },
  };
  api.getBackendProposalNode.mockResolvedValue({
    status: 200,
    data: response,
    headers: new Headers(),
  });
  api.getBackendProposalEvidence.mockResolvedValue({
    status: 200,
    data: {
      items: [],
      nextCursor: "",
      viewSchemaVersion: "proposal-relational-v1",
      proposalPins: response.proposalPins,
    },
    headers: new Headers(),
  });
  api.getBackendProposalCoverage.mockResolvedValue({
    status: 200,
    data: {
      viewSchemaVersion: "proposal-relational-v1",
      proposalPins: response.proposalPins,
      coverage: {
        status: "partial",
        denominator: null,
        knownObjects: 7,
        gaps: ["Writers not inspected"],
      },
      inventory: [],
      snapshots: [],
      staleCounts: { nodes: 0, edges: 0, evidence: 0 },
    },
    headers: new Headers(),
  });
  renderWithProviders(
    <BackendDatabaseInspector
      context={{
        projectId: "project",
        revisionId: "0197aaf9-5555-7000-8000-000000000021",
        datastoreId: "db",
        facetKey: "sql",
        proposal: {
          proposalId: "0197aaf9-5555-7000-8000-000000000022",
          proposalRevisionId: "0197aaf9-5555-7000-8000-000000000023",
        },
      }}
      selection={{ type: "node", id: "user-id" }}
      onSelect={vi.fn()}
      onClose={vi.fn()}
    />,
  );
  expect(await screen.findByText("Желаемое NOT NULL")).toBeInTheDocument();
  expect(screen.getByText("Require users")).toBeInTheDocument();
  expect(screen.getByText("Источник допускает NULL")).toBeInTheDocument();
  expect(api.getBackendProposalNode).toHaveBeenCalledWith(
    "project",
    "0197aaf9-5555-7000-8000-000000000022",
    "0197aaf9-5555-7000-8000-000000000023",
    "user-id",
    expect.anything(),
  );
  expect(api.getBackendNode).not.toHaveBeenCalled();
  expect(await screen.findByText("Writers not inspected")).toBeInTheDocument();
  expect(api.getBackendProposalCoverage).toHaveBeenCalledWith(
    "project",
    "0197aaf9-5555-7000-8000-000000000022",
    "0197aaf9-5555-7000-8000-000000000023",
    expect.anything(),
  );
});

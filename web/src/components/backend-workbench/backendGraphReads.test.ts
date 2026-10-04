import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BackendReadTarget } from "@/api/generated/schemas";
import {
  readBackendAssertions,
  readBackendCoverage,
  readBackendGraph,
  readBackendNode,
  readBackendEvidence,
} from "./backendGraphReads";

const api = vi.hoisted(() => ({
  queryBackendGraph: vi.fn(),
  getBackendNode: vi.fn(),
  getBackendProposalNode: vi.fn(),
  getBackendChangeProposalNode: vi.fn(),
  getBackendImportCandidateNode: vi.fn(),
  getBackendEvidence: vi.fn(),
  getBackendProposalEvidence: vi.fn(),
  getBackendChangeProposalEvidence: vi.fn(),
  getBackendImportCandidateEvidence: vi.fn(),
  getBackendCoverage: vi.fn(),
  getBackendProposalCoverage: vi.fn(),
  getBackendChangeProposalCoverage: vi.fn(),
  getBackendImportCandidateCoverage: vi.fn(),
  getBackendAssertions: vi.fn(),
  getBackendChangeProposalAssertions: vi.fn(),
  getBackendImportCandidateAssertions: vi.fn(),
}));
vi.mock("@/api/generated/backend-projects/backend-projects", () => api);
beforeEach(() => vi.resetAllMocks());

const project = "0197aaf9-5555-7000-8000-000000000001";
const revision = "0197aaf9-5555-7000-8000-000000000002";
const nodeId = "0197aaf9-5555-7000-8000-000000000003";
const hash = "a".repeat(64);
const source: BackendReadTarget = { revisionId: revision };
const legacy: BackendReadTarget = {
  proposal: { proposalId: project, proposalRevisionId: revision },
};
const full: BackendReadTarget = {
  changeProposal: { proposalId: project, proposalRevisionId: revision },
};
const candidate: BackendReadTarget = {
  importCandidate: { importId: project, importVersion: 7, candidateHash: hash },
};

function envelope(target: BackendReadTarget) {
  const viewSchemaVersion =
    "changeProposal" in target ? "proposal-graph-v1" : "import-candidate-v1";
  return {
    target,
    viewSchemaVersion,
    pins: {
      targetHash: hash,
      viewSchemaVersion,
      structuralSchemaVersion: "6",
      effectiveSemanticHash: hash,
      baseRevisionId: revision,
      baseSemanticHash: hash,
      sourceVectorHash: hash,
      sourceSnapshotIds: [],
      artifactPins: [],
      artifactContext: null,
    },
  };
}

describe("shared exact graph reads", () => {
  it("rejects a page containing a different exact record or relationship endpoint", async () => {
    api.queryBackendGraph.mockResolvedValue({
      status: 200,
      data: {
        ...envelope(candidate),
        nodes: [{ id: project, kind: "handler" }],
        edges: [],
        nextCursor: "",
      },
    });
    await expect(
      readBackendGraph(
        project,
        candidate,
        { recordType: "nodes", id: nodeId },
        new AbortController().signal,
      ),
    ).rejects.toThrow();
    api.queryBackendGraph.mockResolvedValue({
      status: 200,
      data: {
        ...envelope(candidate),
        nodes: [],
        edges: [{ id: project, kind: "calls", from: project, to: revision }],
        nextCursor: "",
      },
    });
    await expect(
      readBackendGraph(
        project,
        candidate,
        { recordType: "edges", from: nodeId },
        new AbortController().signal,
      ),
    ).rejects.toThrow();
  });
  it("dispatches each node target explicitly and keeps candidate GET pins", async () => {
    const signal = new AbortController().signal;
    const record = {
      id: nodeId,
      name: "Handler",
      kind: "handler",
      attributes: {},
      evidenceIds: [],
    };
    api.getBackendNode.mockResolvedValue({ status: 200, data: record });
    api.getBackendProposalNode.mockResolvedValue({
      status: 200,
      data: {
        viewSchemaVersion: "proposal-relational-v1",
        proposalPins: legacy.proposal,
        proposalProjection: record,
      },
    });
    api.getBackendChangeProposalNode.mockResolvedValue({
      status: 200,
      data: { ...envelope(full), node: record },
    });
    api.getBackendImportCandidateNode.mockResolvedValue({
      status: 200,
      data: { ...envelope(candidate), node: record },
    });
    for (const target of [source, legacy, full, candidate]) {
      await readBackendNode(project, target, nodeId, signal);
    }
    expect(api.getBackendNode).toHaveBeenCalledWith(project, revision, nodeId, { signal });
    expect(api.getBackendProposalNode).toHaveBeenCalledWith(project, project, revision, nodeId, {
      signal,
    });
    expect(api.getBackendChangeProposalNode).toHaveBeenCalledWith(
      project,
      project,
      revision,
      nodeId,
      { signal },
    );
    expect(api.getBackendImportCandidateNode).toHaveBeenCalledWith(
      project,
      project,
      nodeId,
      { importVersion: 7, candidateHash: hash },
      { signal },
    );
  });

  it("rejects source-only or wrong-node responses to a full target", async () => {
    const signal = new AbortController().signal;
    api.getBackendChangeProposalNode.mockResolvedValue({ status: 200, data: { id: nodeId } });
    await expect(readBackendNode(project, full, nodeId, signal)).rejects.toThrow();
    api.getBackendChangeProposalNode.mockResolvedValue({
      status: 200,
      data: { ...envelope(full), node: { id: project } },
    });
    await expect(readBackendNode(project, full, nodeId, signal)).rejects.toThrow();
    expect(api.getBackendNode).not.toHaveBeenCalled();
  });

  it("does not expose a response arriving after cancellation", async () => {
    const controller = new AbortController();
    api.getBackendNode.mockImplementation(async () => {
      controller.abort();
      return { status: 200, data: { id: nodeId } };
    });
    await expect(
      readBackendNode(project, source, nodeId, controller.signal),
    ).rejects.toHaveProperty("name", "AbortError");
  });

  it("sends exact graph and assertion selectors, never converts a staged ID to a revision", async () => {
    const signal = new AbortController().signal;
    api.queryBackendGraph.mockResolvedValue({
      status: 200,
      data: { ...envelope(candidate), nodes: [], edges: [], nextCursor: "" },
    });
    await readBackendGraph(
      project,
      candidate,
      { recordType: "nodes", search: "Desired", limit: 1 },
      signal,
    );
    expect(api.queryBackendGraph).toHaveBeenCalledWith(
      project,
      { ...candidate, recordType: "nodes", search: "Desired", limit: 1 },
      { signal },
    );
    api.getBackendImportCandidateAssertions.mockResolvedValue({
      status: 200,
      data: {
        ...envelope(candidate),
        documentVersion: "source-assertions-v1",
        basis: "candidate",
        items: [],
        nextCursor: "",
      },
    });
    await readBackendAssertions(
      project,
      candidate,
      { id: nodeId, providerNamespace: "compiler", limit: 1 },
      signal,
    );
    expect(api.getBackendImportCandidateAssertions).toHaveBeenCalledWith(
      project,
      project,
      {
        importVersion: 7,
        candidateHash: hash,
        id: nodeId,
        providerNamespace: "compiler",
        limit: 1,
      },
      { signal },
    );
    expect(api.getBackendAssertions).not.toHaveBeenCalled();
  });

  it("keeps coverage on the requested full target", async () => {
    const signal = new AbortController().signal;
    api.getBackendChangeProposalCoverage.mockResolvedValue({
      status: 200,
      data: { coverage: { status: "complete" } },
    });
    await expect(readBackendCoverage(project, full, signal)).rejects.toThrow();
    api.getBackendChangeProposalCoverage.mockResolvedValue({
      status: 200,
      data: { ...envelope(full), coverage: { status: "partial" } },
    });
    await expect(readBackendCoverage(project, full, signal)).resolves.toHaveProperty(
      "target",
      full,
    );
    expect(api.getBackendCoverage).not.toHaveBeenCalled();
  });

  it("rejects assertions from another provider or subject even with matching page pins", async () => {
    api.getBackendImportCandidateAssertions.mockResolvedValue({
      status: 200,
      data: {
        ...envelope(candidate),
        documentVersion: "source-assertions-v1",
        basis: "candidate",
        items: [
          {
            assertion: {
              recordType: "node",
              recordId: nodeId,
              owner: { repositoryId: project, providerNamespace: "other" },
            },
          },
        ],
        nextCursor: "",
      },
    });
    await expect(
      readBackendAssertions(
        project,
        candidate,
        { recordType: "node", id: nodeId, providerNamespace: "compiler" },
        new AbortController().signal,
      ),
    ).rejects.toThrow();
  });

  it("rejects foreign historical proof bases and mismatched full baseline pins", async () => {
    const proof = { id: nodeId, subjectId: project };
    api.getBackendChangeProposalEvidence.mockResolvedValue({
      status: 200,
      data: {
        ...envelope(full),
        items: [proof],
        nextCursor: "",
        source: {
          legacyProofBases: [{ projectId: revision, evidenceId: nodeId, recordId: project }],
        },
      },
    });
    await expect(
      readBackendEvidence(project, full, { subjectId: project }, new AbortController().signal),
    ).rejects.toThrow();
    api.getBackendChangeProposalEvidence.mockResolvedValue({
      status: 200,
      data: {
        ...envelope(full),
        items: [proof],
        nextCursor: "",
        baselineEvidence: [
          { evidenceId: nodeId, subjectId: project, revisionId: project, semanticHash: hash },
        ],
      },
    });
    await expect(
      readBackendEvidence(project, full, { subjectId: project }, new AbortController().signal),
    ).rejects.toThrow();
  });
});

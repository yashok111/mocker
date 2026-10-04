import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { exactEnvelope, exactHash, exactIDs, exactNode } from "@/test/backendExact";
import { BackendDesiredIdentities } from "./BackendDesiredIdentities";
afterEach(() => vi.unstubAllGlobals());
it("keeps all qualified baseline identities beside their separately intended keys", async () => {
  const target = {
    changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
  };
  const identities = ["compiler", "reflection"].map((providerNamespace) => ({
    target: {
      kind: "source_identity",
      source: {
        recordType: "node",
        id: exactIDs.node,
        repositoryId: exactIDs.repository,
        providerNamespace,
        externalKey: "old",
        assertionHash: exactHash,
      },
    },
    externalKey: providerNamespace === "compiler" ? "new" : "old",
    origin: {
      kind: providerNamespace === "compiler" ? "intent" : "source",
      sourceClaims: [],
      evidenceIds: [],
    },
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json(200, {
        ...exactEnvelope(target),
        nodes: [exactNode()],
        edges: [],
        identities,
        nextCursor: "",
      }),
    ),
  );
  renderWithProviders(
    <BackendDesiredIdentities
      projectId={exactIDs.project}
      target={target}
      recordType="node"
      id={exactIDs.node}
    />,
  );
  expect(await screen.findByText("compiler · old → new")).toBeInTheDocument();
  expect(screen.getByText("reflection · old → old")).toBeInTheDocument();
});

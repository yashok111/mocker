import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import {
  exactCandidate,
  exactEnvelope,
  exactHash,
  exactIDs,
  exactSource,
} from "@/test/backendExact";
import { BackendExactEvidence } from "./BackendExactEvidence";
afterEach(() => vi.unstubAllGlobals());
it("labels metadata-only historical proof and opens its exact original evidence", async () => {
  const proof = {
    id: exactIDs.evidence,
    subjectId: exactIDs.node,
    externalKey: "evidence",
    method: "agent",
    status: "inferred",
    explanation: "Metadata observation",
    source: {
      repositoryId: exactIDs.repository,
      snapshotId: exactIDs.snapshot,
      file: "handler.go",
      contentHash: exactHash,
      startLine: 3,
      endLine: 7,
    },
  };
  const basis = {
    documentVersion: "legacy-proof-basis-v1",
    basisHash: exactHash,
    sourceSchemaVersion: "5",
    projectId: exactIDs.project,
    sourceRevisionId: exactIDs.revision,
    sourceSemanticHash: exactHash,
    recordType: "node",
    recordId: exactIDs.node,
    evidenceId: exactIDs.evidence,
    revisionDocumentHash: exactHash,
    sourceDocumentHash: exactHash,
    subjectDocumentHash: exactHash,
    evidenceDocumentHash: exactHash,
    support: "historical_metadata",
  };
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname.endsWith(`/revisions/${exactIDs.revision}`))
      return json(200, {
        id: exactIDs.revision,
        projectId: exactIDs.project,
        schemaVersion: "5",
        semanticHash: exactHash,
      });
    return json(
      200,
      url.pathname.includes("/candidate/")
        ? {
            ...exactEnvelope(),
            items: [proof],
            nextCursor: "",
            source: { ...exactSource(), legacyProofBases: [basis] },
          }
        : { items: [{ ...proof, explanation: "Original source5 bytes" }], nextCursor: "" },
    );
  });
  vi.stubGlobal("fetch", fetcher);
  renderWithProviders(
    <BackendExactEvidence
      projectId={exactIDs.project}
      target={exactCandidate}
      subjectId={exactIDs.node}
    />,
  );
  expect(await screen.findByText("Историческое свидетельство о метаданных")).toBeInTheDocument();
  expect(screen.getByText(/legacy_metadata_only/)).toBeInTheDocument();
  await userEvent.click(
    screen.getByRole("button", { name: `Открыть исходное свидетельство ${exactIDs.evidence}` }),
  );
  expect(await screen.findByText("Original source5 bytes")).toBeInTheDocument();
  const url = new URL(String(fetcher.mock.calls.at(-1)?.[0]), "http://localhost");
  expect(url.pathname).toContain(`/revisions/${exactIDs.revision}/evidence`);
  expect(url.searchParams.get("evidenceId")).toBe(exactIDs.evidence);
  expect(url.searchParams.has("subjectId")).toBe(false);
  expect(url.searchParams.has("cursor")).toBe(false);
});

it("refuses navigation when the historical revision hash differs from its retained basis", async () => {
  const proof = {
    id: exactIDs.evidence,
    subjectId: exactIDs.node,
    externalKey: "e",
    method: "agent",
    status: "inferred",
    explanation: "Historical",
    source: {
      repositoryId: exactIDs.repository,
      snapshotId: exactIDs.snapshot,
      file: "handler.go",
      contentHash: exactHash,
    },
  };
  const basis = {
    documentVersion: "legacy-proof-basis-v1",
    basisHash: exactHash,
    sourceSchemaVersion: "5",
    projectId: exactIDs.project,
    sourceRevisionId: exactIDs.revision,
    sourceSemanticHash: exactHash,
    recordType: "node",
    recordId: exactIDs.node,
    evidenceId: exactIDs.evidence,
    support: "historical_metadata",
  };
  const fetcher = vi.fn(async (input: RequestInfo | URL) =>
    String(input).includes("/candidate/")
      ? json(200, {
          ...exactEnvelope(),
          items: [proof],
          nextCursor: "",
          source: { ...exactSource(), legacyProofBases: [basis] },
        })
      : json(200, {
          id: exactIDs.revision,
          projectId: exactIDs.project,
          schemaVersion: "5",
          semanticHash: "b".repeat(64),
        }),
  );
  vi.stubGlobal("fetch", fetcher);
  renderWithProviders(
    <BackendExactEvidence
      projectId={exactIDs.project}
      target={exactCandidate}
      subjectId={exactIDs.node}
    />,
  );
  await userEvent.click(
    await screen.findByRole("button", {
      name: `Открыть исходное свидетельство ${exactIDs.evidence}`,
    }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(/ревизия.*свидетельств/);
  expect(
    fetcher.mock.calls.some(([url]) =>
      String(url).includes(`/revisions/${exactIDs.revision}/evidence`),
    ),
  ).toBe(false);
});

import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { exactEnvelope, exactIDs, exactSource } from "@/test/backendExact";
import { BackendAPIFields } from "./BackendAPIFields";
afterEach(() => vi.unstubAllGlobals());
vi.mock("./BackendAPIArtifacts", () => ({ BackendAPIArtifacts: () => null }));
it("lists operation fields from the exact full draft and preserves the API value identity", async () => {
  const target = {
    changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
  };
  const fetcher = vi.fn(async () =>
    json(200, {
      ...exactEnvelope(target),
      nodes: [
        {
          id: exactIDs.node,
          kind: "api_field",
          name: "response.total",
          parentId: exactIDs.repository,
          attributes: {
            direction: "response",
            location: "body",
            responseStatus: "200",
            mediaType: "application/json",
            selector: [{ kind: "property", name: "total" }],
          },
          evidenceIds: [],
          source: exactSource(),
        },
      ],
      edges: [],
      nextCursor: "",
    }),
  );
  vi.stubGlobal("fetch", fetcher);
  renderWithProviders(
    <BackendAPIFields
      projectId={exactIDs.project}
      target={target}
      operationId={exactIDs.repository}
      onValueSelect={vi.fn()}
    />,
  );
  expect(
    await screen.findByRole("button", { name: "Открыть точное значение response.total" }),
  ).toBeInTheDocument();
  expect(fetcher.mock.calls).toHaveLength(1);
});

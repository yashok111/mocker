import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderInRouter } from "@/test/render";
import { json } from "@/test/http";
import { BackendProjectPage } from "./BackendProjectPage";
import { materializationArtifactLink } from "./explorer/artifactLinks";
import { projectId, revisionId, newerId, workspaceHTTP } from "./explorer/testFixtures";
import type { BackendMaterializationOwner } from "@/api/generated/schemas";
vi.mock("./explorer/ExploreCanvas", () => ({ ExploreCanvas: () => <div>Карта</div> }));
afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
  localStorage.clear();
});
const owner: BackendMaterializationOwner = {
  targetKey: "api",
  version: 3,
  pin: {
    namespace: { scope: "local", installationId: projectId },
    pin: { kind: "api_design", id: "7", revisionId: "11", contentHash: "a".repeat(64) },
  },
};
it("keeps materialized artifacts on their immutable revision after current heads advance", () => {
  const href = materializationArtifactLink(projectId, { revisionId }, owner)!;
  const url = new URL(href, "http://localhost");
  expect(url.pathname).toBe("/designs/7");
  expect(url.searchParams.get("pinnedRevisionId")).toBe("11");
  expect(url.searchParams.get("pinnedHash")).toBe("a".repeat(64));
  expect(url.searchParams.get("returnRevisionId")).toBe(revisionId);
});
it("retains exact proposal provenance in artifact return links", () => {
  const href = materializationArtifactLink(
    projectId,
    { changeProposal: { proposalId: projectId, proposalRevisionId: newerId } },
    owner,
  )!;
  expect(href).toContain(`returnProposalRevisionId=${newerId}`);
  expect(href).not.toContain("returnRevisionId=");
});
it("does not substitute owner heads for historical receipts missing exact artifact hashes", () => {
  const old: BackendMaterializationOwner = {
    ...owner,
    pin: { ...owner.pin, pin: { kind: "api_design", id: "7", revisionId: "11" } },
  };
  expect(materializationArtifactLink(projectId, { revisionId }, old)).toBeUndefined();
});
it("retains an uncertain legacy materialization request without issuing apply or preview", async () => {
  const raw = JSON.stringify({
    idempotencyKey: "original-key",
    candidateHash: "a".repeat(64),
    reason: "old request",
  });
  localStorage.setItem(`mocker-materialization-v1:${projectId}`, raw);
  const fetcher = workspaceHTTP();
  renderInRouter(<BackendProjectPage projectId={projectId} sourcePin={{ revisionId }} />);
  expect(await screen.findByRole("button", { name: "Восстановление" })).toBeVisible();
  expect(localStorage.getItem(`mocker-materialization-v1:${projectId}`)).toBe(raw);
  expect(
    fetcher.mock.calls.some(
      ([url]) =>
        String(url).includes("/materializations/apply") ||
        String(url).includes("/materializations/preview"),
    ),
  ).toBe(false);
});
it("reads persisted plan and receipt in a fresh browser without original request JSON", async () => {
  const id = newerId;
  const fetcher = workspaceHTTP((path) =>
    path.endsWith(`/materializations/${id}`)
      ? json(200, {
          id,
          projectId,
          createdAt: "2026-10-06T10:00:00Z",
          preview: {
            input: {
              reason: "Orders contract",
              target: { revisionId },
              targets: [{ key: "api", name: "Orders API" }],
            },
            effects: [],
            coverage: [],
            diagnostics: [],
          },
          receipt: {
            id,
            projectId,
            author: "agent",
            owners: [owner],
            coverage: [],
            equivalence: "structural_projection",
          },
        })
      : undefined,
  );
  renderInRouter(
    <BackendProjectPage
      projectId={projectId}
      sourcePin={{ revisionId, wbPanel: "changes", wbResult: id, wbResultKind: "materialization" }}
    />,
  );
  expect(await screen.findByRole("heading", { name: "Orders contract" })).toBeVisible();
  expect(screen.getByRole("link", { name: "Открыть артефакт" })).toHaveAttribute(
    "href",
    expect.stringContaining("pinnedRevisionId=11"),
  );
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
});

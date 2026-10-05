import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendImpactReport } from "./BackendImpactReport";
import { analysisTestDetail, analysisTestManifest } from "./backendAnalysisTestFixtures";
import {
  changeTestDetail,
  changeTestID,
  changeNextID,
  changeHash,
} from "./backendChangeTestFixtures";
import type {
  BackendAnalysisJobDetail,
  BackendAnalysisResultRecord,
} from "@/api/generated/schemas";
export function conformanceFixture(required = true, outcome = "unverified") {
  const proposal = changeTestDetail();
  proposal.proposal.status = "ready";
  proposal.revision.criteria = [
    {
      key: "runtime",
      kind: "runtime_check",
      targetIds: [],
      required,
      description: "Observe behavior",
    },
  ];
  const old = analysisTestDetail();
  const detail: BackendAnalysisJobDetail = {
    ...old,
    job: { ...old.job, kind: "conformance" },
    input: {
      documentVersion: "backend-analysis-context-v2",
      kind: "conformance",
      limits: old.input.limits,
      observationMode: "none",
      ruleSetVersion: "b43-rules/v1",
      traversalVersion: "b42-traversal/v1",
      payload: {
        changeProposal: {
          proposalId: proposal.proposal.id,
          proposalRevisionId: proposal.revision.id,
        },
        baseRevisionId: proposal.revision.baseRevisionId,
        basePins: old.input.beforePins,
        draftPins: old.input.afterPins,
        baseSource: old.input.beforeSource,
        draftSource: old.input.afterSource,
        evidencePins: [],
        resultRevisionId: changeNextID,
        resultPins: old.input.beforePins,
        resultSource: old.input.beforeSource,
        identityMap: [],
        testAttachments: [],
      },
    },
  };
  const item = {
    id: "criterion-runtime",
    object: { recordType: "node", id: changeTestID },
    kind: "runtime_check",
    certainty: "unknown",
    service: "",
    direction: "both",
    depth: 0,
    detail: {
      documentVersion: "backend-b43-result-v1",
      type: "conformance_criterion",
      criterionKey: "runtime",
      criterionKind: "runtime_check",
      required,
      outcome,
      reason: "Execution is not performed",
      basis: [],
      proposalObject: null,
      sourceObject: null,
      deletionBasis: [],
    },
  } as BackendAnalysisResultRecord;
  return { proposal, detail, item };
}
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
function setup(required = true) {
  const f = conformanceFixture(required),
    posts: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = new URL(String(input), "http://localhost");
      if (init?.method === "POST") {
        const body = JSON.parse(String(init.body));
        posts.push(body);
        return new Response(
          JSON.stringify({
            proposal: {
              ...f.proposal.proposal,
              status: "implemented",
              version: 2,
              implementedReference: {
                report: body.report,
                proposalRevisionId: f.proposal.revision.id,
                draftHash: changeHash,
                resultRevisionId: changeNextID,
                resultSemanticHash: changeHash,
                exceptions: body.exceptions,
                behaviorStatus: "unverified",
              },
            },
            revision: f.proposal.revision,
          }),
        );
      }
      return new Response(
        JSON.stringify({
          manifest: analysisTestManifest(),
          section: u.searchParams.get("section"),
          items: u.searchParams.get("section") === "checks" ? [f.item] : [],
          nextCursor: "",
        }),
      );
    }),
  );
  renderWithProviders(
    <BackendImpactReport
      projectId={changeTestID}
      detail={f.detail}
      resultVersion={2}
      proposal={f.proposal}
    />,
  );
  return posts;
}
it("shows required runtime outcome as a blocker, never a passed exception", async () => {
  setup();
  expect(await screen.findByText(/runtime · runtime_check/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Отметить реализованным" })).toBeDisabled();
  expect(screen.getByText(/Обязательные критерии не подтверждены/)).toBeInTheDocument();
});
it("sends optional annotation separately while runtime remains unverified", async () => {
  const posts = setup(false),
    u = userEvent.setup();
  await screen.findByText(/runtime · runtime_check/);
  await u.click(screen.getByLabelText("Добавить исключение runtime"));
  await u.type(screen.getByLabelText("Автор исключения runtime"), "Reviewer");
  await u.type(screen.getByLabelText("Причина исключения runtime"), "External execution pending");
  await u.click(screen.getByRole("button", { name: "Отметить реализованным" }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({
    action: "implemented",
    resultRevisionId: changeNextID,
    exceptions: [
      { criterionKey: "runtime", author: "Reviewer", reason: "External execution pending" },
    ],
    report: { resultVersion: 2 },
  });
  expect(screen.getByText(/Выполнение приложения: не проверено/)).toBeInTheDocument();
});

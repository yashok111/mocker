import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendAnalysisJobs } from "./BackendAnalysisJobs";
import {
  changeTestDetail,
  changeTestID,
  changeNextID,
  changeDraftID,
  changeHash,
} from "./backendChangeTestFixtures";
import { analysisTestDetail } from "./backendAnalysisTestFixtures";
import { analysisRecoveryKey } from "./backendAnalysisRecovery";
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
function setup(dirty = false, preview = false) {
  const calls: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST") {
        const body = JSON.parse(String(init.body));
        calls.push(body);
        expect(JSON.parse(sessionStorage.getItem(analysisRecoveryKey(changeTestID))!).body).toBe(
          init.body,
        );
        return new Response(
          JSON.stringify({
            ...analysisTestDetail().job,
            kind: body.kind,
            status: "queued",
            resultVersion: undefined,
          }),
          { status: 202 },
        );
      }
      return new Response(
        JSON.stringify(
          url.endsWith("/analyses/" + changeTestID)
            ? analysisTestDetail()
            : { items: [], nextCursor: "" },
        ),
      );
    }),
  );
  const proposal = changeTestDetail();
  proposal.revision.criteria = [
    {
      key: "test",
      kind: "test_attachment",
      targetIds: [],
      description: "Pinned test",
      required: false,
      attachment: {
        kind: "source",
        revisionId: changeTestID,
        repositoryId: changeTestID,
        snapshotId: changeTestID,
        file: "test.go",
        contentHash: changeHash,
        startLine: 1,
        endLine: 3,
      },
    },
  ];
  const target = preview
    ? {
        commandPreview: {
          changeProposal: { proposalId: changeTestID, proposalRevisionId: changeDraftID },
          expectedVersion: 1,
          commands: [],
          candidateHash: changeHash,
        },
      }
    : { changeProposal: { proposalId: changeTestID, proposalRevisionId: changeDraftID } };
  renderWithProviders(
    <BackendAnalysisJobs
      projectId={changeTestID}
      sourceRevisionId={changeTestID}
      proposal={proposal}
      target={target}
      dirty={dirty}
    />,
  );
  return calls;
}
it("starts saved-only package from reachable Jobs with exact draft", async () => {
  const calls = setup();
  await userEvent.setup().click(screen.getByRole("button", { name: "Собрать пакет изменений" }));
  await waitFor(() =>
    expect(calls[0]).toMatchObject({
      kind: "change_package",
      changeProposal: { proposalId: changeTestID, proposalRevisionId: changeDraftID },
    }),
  );
  expect(calls[0]).not.toHaveProperty("target");
});
it.each([
  [true, false],
  [false, true],
])("refuses dirty or preview substitution (%s,%s)", (dirty, preview) => {
  setup(dirty, preview);
  expect(screen.getByRole("button", { name: "Собрать пакет изменений" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Проверить соответствие" })).toBeDisabled();
});
it("sends exact result revision, typed mapping with reason, and selected pinned attachment", async () => {
  const calls = setup(),
    u = userEvent.setup();
  await u.type(screen.getByLabelText("Ревизия реализованного источника"), changeNextID);
  await u.click(screen.getByRole("button", { name: "Добавить соответствие узлов" }));
  await u.type(screen.getByLabelText("UUID узла предложения 1"), changeDraftID);
  await u.type(screen.getByLabelText("UUID узла источника 1"), changeNextID);
  await u.type(screen.getByLabelText("Основание соответствия 1"), "Exact import identity");
  await u.click(screen.getByLabelText("Приложить test"));
  await u.click(screen.getByRole("button", { name: "Проверить соответствие" }));
  await waitFor(() =>
    expect(calls[0]).toMatchObject({
      kind: "conformance",
      resultRevisionId: changeNextID,
      identityMap: [
        {
          proposalNodeId: changeDraftID,
          sourceNodeId: changeNextID,
          reason: "Exact import identity",
        },
      ],
      testAttachments: [
        { criterionKey: "test", attachment: { revisionId: changeTestID, file: "test.go" } },
      ],
    }),
  );
});
it("sends explicit endpoint removal and exact optional full-proposal intent", async () => {
  const calls = setup(),
    u = userEvent.setup();
  await u.type(screen.getByLabelText("UUID endpoint до"), changeDraftID);
  await u.type(screen.getByLabelText("Ревизия endpoint после"), changeNextID);
  await u.click(screen.getByLabelText("Endpoint удалён — после отсутствует (null)"));
  await u.click(screen.getByLabelText("Сравнить с сохранённым намерением предложения"));
  await u.click(screen.getByRole("button", { name: "Обзор endpoint" }));
  await waitFor(() =>
    expect(calls[0]).toMatchObject({
      kind: "endpoint_review",
      fromRevisionId: changeTestID,
      toRevisionId: changeNextID,
      beforeEndpointId: changeDraftID,
      afterEndpointId: null,
      changeProposal: { proposalId: changeTestID, proposalRevisionId: changeDraftID },
    }),
  );
});

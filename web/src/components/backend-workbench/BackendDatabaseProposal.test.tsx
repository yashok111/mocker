import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useState } from "react";
import { act, cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { ApiFailure } from "@/api/client";
import { BackendDatabaseProposal } from "./BackendDatabaseProposal";
import { proposalDetail, proposalNodes } from "./backendProposalTestFixtures";
import type { BackendProposalPreview } from "@/api/generated/schemas";

const api = vi.hoisted(() => ({
  listBackendProposals: vi.fn(),
  createBackendProposal: vi.fn(),
  getBackendProposal: vi.fn(),
  previewBackendProposalCommands: vi.fn(),
  applyBackendProposalCommands: vi.fn(),
  queryBackendGraph: vi.fn(),
}));
vi.mock("@/api/generated/backend-projects/backend-projects", () => api);
const ok = (data: unknown) => ({ data, status: 200, headers: new Headers() });
const preview: BackendProposalPreview = {
  proposalId: "proposal",
  baseRevisionId: "base",
  baseSemanticHash: "a".repeat(64),
  draftRevisionId: "draft",
  draftHash: "b".repeat(64),
  expectedVersion: 1,
  candidateHash: "d".repeat(64),
  candidateGraphHash: "e".repeat(64),
  changes: [
    {
      type: "alter_column",
      commandId: "command",
      subjectId: "user-id",
      before: { nullable: { status: "known", value: true } },
      after: { nullable: { status: "known", value: false } },
      generatedIds: {},
    },
  ],
  criteria: [
    {
      key: "data",
      kind: "existing_data",
      targetIds: ["user-id"],
      description: "Check existing NULLs",
      origin: "required",
      status: "unverified",
      commandId: "command",
    },
  ],
  diagnostics: [],
  limitations: ["Runtime unverified"],
};
beforeEach(() => {
  sessionStorage.clear();
  api.listBackendProposals.mockResolvedValue(
    ok({ items: [proposalDetail.proposal], nextCursor: "" }),
  );
  api.createBackendProposal.mockResolvedValue(ok(proposalDetail));
  api.getBackendProposal.mockResolvedValue(ok(proposalDetail));
  api.previewBackendProposalCommands.mockResolvedValue(ok(preview));
  api.queryBackendGraph.mockResolvedValue(
    ok({
      nodes: proposalNodes,
      edges: [],
      nextCursor: "",
      proposalProjection: { nodes: [], edges: [] },
    }),
  );
  api.applyBackendProposalCommands.mockResolvedValue(
    ok({
      proposal: {
        ...proposalDetail.proposal,
        version: 2,
        draftRevisionId: "saved",
        draftHash: preview.candidateHash,
      },
      revision: {
        ...proposalDetail.revision,
        id: "saved",
        semanticHash: preview.candidateHash,
        criteria: preview.criteria,
      },
      changes: preview.changes,
      criteria: preview.criteria,
      candidateGraphHash: preview.candidateGraphHash,
    }),
  );
});
afterEach(() => {
  vi.clearAllMocks();
  vi.restoreAllMocks();
  sessionStorage.clear();
});
it("uses the latest callbacks on unmount without clearing the selection on rerender", async () => {
  const context = { projectId: "project", revisionId: "base", datastoreId: "db", facetKey: "sql" };
  const oldView = vi.fn(),
    oldDirty = vi.fn(),
    newView = vi.fn(),
    newDirty = vi.fn();
  function Harness() {
    const [latest, setLatest] = useState(false);
    return (
      <>
        <button onClick={() => setLatest(true)}>Replace callbacks</button>
        <BackendDatabaseProposal
          context={context}
          repositoryId="repository"
          onView={latest ? newView : oldView}
          onDirty={latest ? newDirty : oldDirty}
        />
      </>
    );
  }
  const rendered = renderWithProviders(<Harness />);
  await screen.findByRole("option", { name: "Required users" });
  await userEvent.selectOptions(screen.getByLabelText("Предложение изменений"), "proposal");
  await screen.findByLabelText("Колонка");
  await userEvent.click(screen.getByRole("button", { name: "Replace callbacks" }));
  expect(screen.getByLabelText("Предложение изменений")).toHaveValue("proposal");
  expect(newView).not.toHaveBeenCalledWith(null);
  oldView.mockClear();
  oldDirty.mockClear();
  rendered.unmount();
  expect(newView).toHaveBeenLastCalledWith(null);
  expect(newDirty).toHaveBeenLastCalledWith(false);
  expect(oldView).not.toHaveBeenCalled();
  expect(oldDirty).not.toHaveBeenCalled();
});
async function edit() {
  const user = userEvent.setup();
  renderWithProviders(
    <BackendDatabaseProposal
      context={{ projectId: "project", revisionId: "base", datastoreId: "db", facetKey: "sql" }}
      repositoryId="repository"
    />,
  );
  await screen.findByRole("option", { name: "Required users" });
  await user.selectOptions(await screen.findByLabelText("Предложение изменений"), "proposal");
  await user.selectOptions(await screen.findByLabelText("Колонка"), "user-id");
  await user.selectOptions(screen.getByLabelText("Допускает NULL"), "false");
  await user.type(screen.getByRole("textbox", { name: /Причина изменения/ }), "Require users");
  await user.click(screen.getByRole("button", { name: "Добавить в буфер" }));
  return user;
}
it("previews intent separately, then saves the exact pinned command", async () => {
  const user = await edit();
  expect(api.applyBackendProposalCommands).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "Предпросмотр" }));
  expect(await screen.findByText("Check existing NULLs")).toBeInTheDocument();
  expect(screen.getByText("Желаемое NOT NULL")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Сохранить предложение" }));
  await waitFor(() =>
    expect(api.applyBackendProposalCommands).toHaveBeenCalledWith(
      "project",
      "proposal",
      expect.objectContaining({
        expectedVersion: 1,
        draftRevisionId: "draft",
        candidateHash: "d".repeat(64),
        commands: [
          expect.objectContaining({ type: "alter_column", columnId: "user-id", nullable: false }),
        ],
      }),
      expect.anything(),
    ),
  );
  expect(await screen.findByText("Предложение сохранено")).toBeInTheDocument();
});
it("retains identical request and key after an uncertain save response", async () => {
  api.applyBackendProposalCommands.mockRejectedValueOnce(new TypeError("response lost"));
  const user = await edit();
  await user.click(screen.getByRole("button", { name: "Предпросмотр" }));
  await user.click(await screen.findByRole("button", { name: "Сохранить предложение" }));
  await user.click(await screen.findByRole("button", { name: "Повторить сохранение" }));
  await screen.findByText("Предложение сохранено");
  expect(api.applyBackendProposalCommands.mock.calls[1]![2]).toEqual(
    api.applyBackendProposalCommands.mock.calls[0]![2],
  );
});
it("ignores a preview response after the buffer changes", async () => {
  let resolve!: (value: unknown) => void;
  api.previewBackendProposalCommands.mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  const user = await edit();
  await user.click(screen.getByRole("button", { name: "Предпросмотр" }));
  await user.click(screen.getByRole("button", { name: "Удалить команду 1" }));
  await act(async () => {
    resolve(ok(preview));
  });
  expect(screen.queryByRole("button", { name: "Сохранить предложение" })).not.toBeInTheDocument();
});
it("requires explicit reconciliation after a CAS conflict", async () => {
  api.applyBackendProposalCommands.mockRejectedValueOnce(
    new ApiFailure("changed", 409, "backend_proposal_version_conflict"),
  );
  const user = await edit();
  await user.click(screen.getByRole("button", { name: "Предпросмотр" }));
  await user.click(await screen.findByRole("button", { name: "Сохранить предложение" }));
  expect(await screen.findByRole("button", { name: "Перечитать предложение" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Сохранить предложение" })).not.toBeInTheDocument();
});

it("recovers an uncertain save after leaving and reopening the proposal", async () => {
  api.applyBackendProposalCommands.mockRejectedValueOnce(new TypeError("response lost"));
  const user = await edit();
  await user.click(screen.getByRole("button", { name: "Предпросмотр" }));
  await user.click(await screen.findByRole("button", { name: "Сохранить предложение" }));
  await screen.findByRole("button", { name: "Повторить сохранение" });
  const original = api.applyBackendProposalCommands.mock.calls[0]![2];
  cleanup();
  renderWithProviders(
    <BackendDatabaseProposal
      context={{ projectId: "project", revisionId: "base", datastoreId: "db", facetKey: "sql" }}
      repositoryId="repository"
    />,
  );
  await screen.findByRole("option", { name: "Required users" });
  await user.selectOptions(screen.getByLabelText("Предложение изменений"), "proposal");
  await user.click(await screen.findByRole("button", { name: "Повторить сохранение" }));
  await screen.findByText("Предложение сохранено");
  expect(api.applyBackendProposalCommands.mock.calls[1]![2]).toEqual(original);
});

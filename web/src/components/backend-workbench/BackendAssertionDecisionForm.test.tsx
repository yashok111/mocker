import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import type { BackendImportCommand } from "@/api/generated/schemas";
import { renderWithProviders } from "@/test/render";
import {
  BackendAssertionDecisionForm,
  BackendClaimIdentityForm,
  sourceAssertionAddress,
} from "./BackendAssertionDecisionForm";
import {
  syncAssertion,
  syncCommands,
  syncConflict,
  syncIds,
  syncSession,
} from "./backendSyncTestFixtures";

afterEach(() => vi.restoreAllMocks());

it("limits claim targets to the offered repository, record type and kind and uses only owned proof", async () => {
  const user = userEvent.setup(),
    onClaim = vi.fn(),
    good = syncAssertion();
  const commands = syncCommands() as BackendImportCommand[];
  const proof = commands[1];
  if (proof?.op !== "upsert_evidence") throw new Error("Missing fixture proof");
  const foreign = structuredClone(proof),
    otherSubject = structuredClone(proof),
    stale = structuredClone(proof);
  foreign.evidence.externalKey = "foreign-proof";
  foreign.evidence.source!.repositoryId = syncIds.later;
  otherSubject.evidence.externalKey = "other-subject";
  otherSubject.evidence.subjectKey = "other";
  stale.evidence.externalKey = "stale-proof";
  stale.evidence.source!.contentHash = "f".repeat(64);
  renderWithProviders(
    <BackendClaimIdentityForm
      session={syncSession()}
      commands={[...commands, foreign, otherSubject, stale]}
      offers={[
        good,
        { ...good, owner: { ...good.owner, repositoryId: syncIds.later } },
        { ...good, payload: { ...good.payload, kind: "service" } },
        { ...good, recordType: "edge" },
      ]}
      loading={false}
      nextCursor=""
      onLoad={vi.fn()}
      onClaim={onClaim}
    />,
  );
  await user.selectOptions(screen.getByLabelText("Входящий объект"), "0");
  expect(screen.getByLabelText("Точное утверждение базы").querySelectorAll("option")).toHaveLength(
    2,
  );
  expect(screen.queryByLabelText("foreign-proof")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("other-subject")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("stale-proof")).not.toBeInTheDocument();
  await user.selectOptions(
    screen.getByLabelText("Точное утверждение базы"),
    JSON.stringify(sourceAssertionAddress(good)),
  );
  await user.click(screen.getByLabelText("own-proof"));
  await user.type(screen.getByLabelText(/Причина общей идентичности/), "Same handler");
  await user.click(screen.getByRole("button", { name: "Добавить claim_identity в пакет" }));
  expect(onClaim).toHaveBeenCalledWith({
    op: "claim_identity",
    claimIdentity: {
      decisionId: expect.any(String),
      recordType: "node",
      externalKey: "handler",
      target: sourceAssertionAddress(good),
      evidenceKeys: ["own-proof"],
      reason: "Same handler",
    },
  });
});

it("cannot silently select the same list index on a different assertion page", async () => {
  const user = userEvent.setup(),
    onClaim = vi.fn(),
    good = syncAssertion();
  const props = {
    session: syncSession(),
    commands: syncCommands() as BackendImportCommand[],
    offers: [good],
    loading: false,
    nextCursor: "next",
    onLoad: vi.fn(),
    onClaim,
  };
  function Pages() {
    const [more, setMore] = useState(false);
    return (
      <BackendClaimIdentityForm
        {...props}
        offers={more ? [syncAssertion("other", "d".repeat(64))] : props.offers}
        onLoad={() => setMore(true)}
      />
    );
  }
  renderWithProviders(<Pages />);
  await user.selectOptions(screen.getByLabelText("Входящий объект"), "0");
  await user.selectOptions(
    screen.getByLabelText("Точное утверждение базы"),
    JSON.stringify(sourceAssertionAddress(good)),
  );
  await user.click(screen.getByLabelText("own-proof"));
  await user.type(screen.getByLabelText(/Причина общей идентичности/), "Reviewed first page");
  expect(screen.getByRole("button", { name: "Добавить claim_identity в пакет" })).toBeEnabled();
  await user.click(screen.getByRole("button", { name: "Ещё утверждения базы" }));
  expect(screen.getByRole("button", { name: "Добавить claim_identity в пакет" })).toBeDisabled();
  expect(onClaim).not.toHaveBeenCalled();
});

it("submits the exact offered conflict property, hash and contender without a replacement value", async () => {
  const user = userEvent.setup(),
    onResolve = vi.fn(),
    conflict = syncConflict();
  renderWithProviders(
    <BackendAssertionDecisionForm conflict={conflict} previewVersion={7} onResolve={onResolve} />,
  );
  expect(screen.getByText('"Original"')).toBeInTheDocument();
  expect(screen.getByText('"Other"')).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText(`Утверждение для ${syncIds.node}`), "1");
  await user.type(screen.getByLabelText(/Причина выбора утверждения/), "Reviewed other owner");
  await user.click(screen.getByRole("button", { name: "Отправить выбор утверждения" }));
  expect(onResolve).toHaveBeenCalledWith({
    op: "resolve_assertion",
    resolution: {
      decisionId: expect.any(String),
      recordType: "node",
      id: syncIds.node,
      property: { kind: "name" },
      conflictHash: conflict.conflictHash,
      select: {
        repositoryId: syncIds.repository,
        providerNamespace: "other",
        assertionHash: "2".repeat(64),
      },
      reason: "Reviewed other owner",
    },
  });
  expect(screen.getAllByRole("textbox")).toHaveLength(1);
});

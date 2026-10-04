import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { BackendChangeRebase } from "./BackendChangeRebase";
import {
  changeTestDetail,
  changeNextID,
  changeTestID,
  changeHash,
} from "./backendChangeTestFixtures";
import { hashBackendJSON } from "./backendImportHash";
import { analysisRecoveryKey, inspectAnalysisRecovery } from "./backendAnalysisRecovery";
import type { PreviewBackendChangeProposalRebaseRequest } from "@/api/generated/schemas";
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});
it("requires explicit reasoned B/O/N choice, invalidates candidate on change, and applies exact new baseline receipt", async () => {
  const detail = changeTestDetail();
  detail.revision.baseSchemaVersion = "5";
  const sourceVector = {
      documentVersion: "source-vector-v1" as const,
      partitions: [],
      snapshots: [],
    },
    sourceVectorHash = await hashBackendJSON(sourceVector);
  const coverage = { status: "partial", knownObjects: 0, denominator: null, gaps: [] };
  const source = {
    id: changeNextID,
    projectId: changeTestID,
    schemaVersion: "5",
    semanticHash: changeHash,
    sourceSnapshotIds: [],
    artifactPins: [],
    coverage,
    summary: "new source",
  };
  const conflict = {
    id: changeHash,
    object: { recordType: "node", id: changeTestID },
    selector: { kind: "property", property: { kind: "source", source: { kind: "name" } } },
    base: { presence: "value", value: "base" },
    ours: { presence: "value", value: "ours" },
    newSource: { presence: "value", value: "new source" },
  };
  const writes: string[] = [];
  let previewInput: PreviewBackendChangeProposalRebaseRequest | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      const path = url.pathname;
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
      if (path.endsWith("/rebase-preview")) {
        previewInput = body;
        return json({
          proposalId: detail.proposal.id,
          proposalRevisionId: detail.revision.id,
          expectedVersion: 1,
          oldBaseRevisionId: detail.revision.baseRevisionId,
          oldBaseSemanticHash: changeHash,
          newBaseRevisionId: changeNextID,
          newBaseSemanticHash: changeHash,
          conflicts: body.resolutions.length ? [] : [conflict],
          diagnostics: [],
          semanticHash: body.resolutions.length ? changeHash : null,
          candidateHash: body.resolutions.length ? changeHash : null,
          sourcePins: {
            revisionId: changeNextID,
            semanticHash: changeHash,
            contentHash: changeHash,
            sourceVectorHash,
            sourceSnapshotIds: [],
          },
          artifactPins: [],
        });
      }
      if (path.endsWith("/rebase")) {
        writes.push(String(init?.body));
        expect(inspectAnalysisRecovery(analysisRecoveryKey(changeTestID)).attempt?.body).toBe(
          init?.body,
        );
        return json({
          proposal: { ...detail.proposal, version: 2, currentDraftRevisionId: changeNextID },
          revision: {
            ...detail.revision,
            id: changeNextID,
            parentRevisionId: detail.revision.id,
            baseRevisionId: changeNextID,
            sourceVector,
            rebase: {
              protocol: "backend-change-rebase-v1",
              input: previewInput,
              candidateHash: changeHash,
            },
          },
          semanticHash: changeHash,
          changes: [],
        });
      }
      if (path.endsWith("/revisions")) {
        const limit = Number(url.searchParams.get("limit") ?? 100);
        if (!Number.isInteger(limit) || limit < 1 || limit > 100)
          return new Response(
            JSON.stringify({
              error: { code: "backend_invalid", message: "limit must be between 1 and 100" },
            }),
            { status: 400 },
          );
        return json({ items: [source], nextCursor: "" });
      }
      if (path.endsWith("/coverage")) return json({ coverage, inventory: [], snapshots: [] });
      return json(source);
    }),
  );
  const saved = vi.fn(),
    user = userEvent.setup();
  renderWithProviders(
    <BackendChangeRebase projectId={changeTestID} detail={detail} onSaved={saved} />,
  );
  await screen.findByRole("option", { name: /new source/ });
  await user.selectOptions(screen.getByLabelText("Новая база (N)"), changeNextID);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Проверить перенос" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Проверить перенос" }));
  const choice = await screen.findByLabelText("Решение конфликта");
  expect(choice).toHaveValue("");
  expect(screen.getByRole("button", { name: "Применить перенос" })).toBeDisabled();
  expect(screen.getByText("B — исходное значение и присутствие")).toBeInTheDocument();
  await user.selectOptions(choice, "keep_proposal");
  await user.click(screen.getByRole("button", { name: "Проверить перенос" }));
  expect(screen.getByRole("button", { name: "Применить перенос" })).toBeDisabled();
  await fill(screen.getByLabelText(/Причина решения/), "Preserve intentional name");
  await user.click(screen.getByRole("button", { name: "Проверить перенос" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Применить перенос" })).toBeEnabled(),
  );
  await user.clear(screen.getByLabelText(/Причина решения/));
  await fill(screen.getByLabelText(/Причина решения/), "Reconsidered reason");
  expect(screen.getByRole("button", { name: "Применить перенос" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Проверить перенос" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Применить перенос" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Применить перенос" }));
  await waitFor(() => expect(saved).toHaveBeenCalledOnce());
  expect(saved.mock.calls[0]![0].revision.baseRevisionId).toBe(changeNextID);
  expect(JSON.parse(writes[0]!).resolutions[0].reason).toBe("Reconsidered reason");
});

it("reconciles current draft after apply409 and requires a new preview before another key", async () => {
  const { BackendAnalysisRecoveryProvider } = await import("./backendAnalysisRecovery");
  const { BackendAnalysisRecoveryNotice } = await import("./BackendAnalysisRecoveryNotice");
  const detail = changeTestDetail();
  detail.revision.baseSchemaVersion = "5";
  const latest = {
    ...detail,
    proposal: { ...detail.proposal, version: 3, currentDraftRevisionId: changeNextID },
    revision: { ...detail.revision, id: changeNextID },
  };
  const coverage = { status: "partial", knownObjects: 0, denominator: null, gaps: [] };
  const source = {
    id: changeNextID,
    projectId: changeTestID,
    schemaVersion: "5",
    semanticHash: changeHash,
    sourceSnapshotIds: [],
    artifactPins: [],
    coverage,
    summary: "new source",
  };
  const vectorHash = await hashBackendJSON(detail.revision.sourceVector);
  const requests: { path: string; body?: PreviewBackendChangeProposalRebaseRequest }[] = [];
  let previews = 0;
  vi.stubGlobal("confirm", () => true);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      const path = url.pathname;
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      requests.push({ path, body });
      const json = (value: unknown, status = 200) =>
        new Response(JSON.stringify(value), { status });
      if (path.endsWith("/rebase-preview")) {
        previews++;
        return json({
          proposalId: detail.proposal.id,
          proposalRevisionId: body.proposalRevisionId,
          expectedVersion: body.expectedVersion,
          oldBaseRevisionId: detail.revision.baseRevisionId,
          oldBaseSemanticHash: changeHash,
          newBaseRevisionId: changeNextID,
          newBaseSemanticHash: changeHash,
          conflicts:
            previews === 1
              ? [
                  {
                    id: changeHash,
                    object: { recordType: "node", id: changeTestID },
                    selector: {
                      kind: "property",
                      property: { kind: "source", source: { kind: "name" } },
                    },
                    base: { presence: "value", value: "base" },
                    ours: { presence: "value", value: "ours" },
                    newSource: { presence: "value", value: "source" },
                  },
                ]
              : [],
          diagnostics: [],
          semanticHash: previews === 1 ? null : changeHash,
          candidateHash: previews === 1 ? null : changeHash,
          sourcePins: {
            revisionId: changeNextID,
            semanticHash: changeHash,
            contentHash: changeHash,
            sourceVectorHash: vectorHash,
            sourceSnapshotIds: [],
          },
          artifactPins: [],
        });
      }
      if (path.endsWith("/rebase"))
        return json({ error: { code: "version_conflict", message: "proposal changed" } }, 409);
      if (path.endsWith(`/change-proposals/${detail.proposal.id}`)) return json(latest);
      if (path.endsWith("/revisions")) {
        const limit = Number(url.searchParams.get("limit") ?? 100);
        if (!Number.isInteger(limit) || limit < 1 || limit > 100)
          return new Response(
            JSON.stringify({
              error: { code: "backend_invalid", message: "limit must be between 1 and 100" },
            }),
            { status: 400 },
          );
        return json({ items: [source], nextCursor: "" });
      }
      if (path.endsWith("/coverage")) return json({ coverage, inventory: [], snapshots: [] });
      return json(source);
    }),
  );
  function EditorHarness() {
    const [current, setCurrent] = useState(detail);
    return (
      <BackendAnalysisRecoveryProvider projectId={changeTestID}>
        <BackendAnalysisRecoveryNotice projectId={changeTestID} />
        <BackendChangeRebase
          key={current.revision.id}
          projectId={changeTestID}
          detail={current}
          onSaved={setCurrent}
        />
      </BackendAnalysisRecoveryProvider>
    );
  }
  renderWithProviders(<EditorHarness />);
  const user = userEvent.setup();
  await screen.findByRole("option", { name: /new source/ });
  await user.selectOptions(screen.getByLabelText("Новая база (N)"), changeNextID);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Проверить перенос" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Проверить перенос" }));
  await user.selectOptions(await screen.findByLabelText("Решение конфликта"), "keep_proposal");
  await fill(screen.getByLabelText(/Причина решения/), "Keep old intent for reference");
  await user.click(screen.getByRole("button", { name: "Проверить перенос" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Применить перенос" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Применить перенос" }));
  await screen.findByText(/Конфликт: исходный запрос/);
  await user.click(screen.getByRole("button", { name: "Сверено — разрешить новый предпросмотр" }));
  await waitFor(() =>
    expect(inspectAnalysisRecovery(analysisRecoveryKey(changeTestID)).attempt).toBeNull(),
  );
  expect(screen.getByRole("button", { name: "Применить перенос" })).toBeDisabled();
  expect(previews).toBe(2);
  await user.click(screen.getByText("Решения до конфликта (справочно)"));
  expect(screen.getByText(/Keep old intent for reference/)).toBeInTheDocument();
  expect(requests.some((r) => r.path.endsWith(`/change-proposals/${detail.proposal.id}`))).toBe(
    true,
  );
  await user.click(screen.getByRole("button", { name: "Проверить перенос" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Применить перенос" })).toBeEnabled(),
  );
  expect(requests.filter((r) => r.path.endsWith("/rebase-preview")).at(-1)?.body).toMatchObject({
    expectedVersion: 3,
    proposalRevisionId: changeNextID,
    resolutions: [],
  });
});

it.each(["read failure", "concurrent replacement"] as const)(
  "preserves exact recovery on reconciliation %s",
  async (mode) => {
    const { BackendAnalysisRecoveryProvider, makeAnalysisAttempt, writeAnalysisRecovery } =
      await import("./backendAnalysisRecovery");
    const { BackendAnalysisRecoveryNotice } = await import("./BackendAnalysisRecoveryNotice");
    const detail = changeTestDetail(),
      key = analysisRecoveryKey(changeTestID);
    const original = {
      ...makeAnalysisAttempt(
        "rebase",
        { projectId: changeTestID, proposalId: detail.proposal.id },
        {
          expectedVersion: 1,
          proposalRevisionId: detail.revision.id,
          newBaseRevisionId: changeNextID,
          identityResolutions: [],
          resolutions: [],
          repairCommands: [],
          candidateHash: changeHash,
          idempotencyKey: "original",
        },
        {
          newBaseSemanticHash: changeHash,
          sourceVectorHash: changeHash,
          sourceSnapshotIds: [],
          candidateSemanticHash: changeHash,
        },
      ),
      phase: "conflict" as const,
    };
    const raw = writeAnalysisRecovery(key, original, null);
    const replacement = makeAnalysisAttempt(
      "start",
      { projectId: changeTestID },
      {
        kind: "impact",
        fromRevisionId: changeTestID,
        target: { revisionId: changeNextID },
        scope: {},
        limits: {},
        observationMode: "none",
        idempotencyKey: "other-request",
      },
    );
    vi.stubGlobal("confirm", () => true);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        if (mode === "read failure")
          return new Response(
            JSON.stringify({ error: { code: "internal_error", message: "read failed" } }),
            { status: 500 },
          );
        writeAnalysisRecovery(key, replacement, raw);
        return new Response(JSON.stringify(detail), { status: 200 });
      }),
    );
    renderWithProviders(
      <BackendAnalysisRecoveryProvider projectId={changeTestID}>
        <BackendAnalysisRecoveryNotice projectId={changeTestID} />
      </BackendAnalysisRecoveryProvider>,
    );
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Сверено — разрешить новый предпросмотр" }));
    if (mode === "read failure") await screen.findByText("read failed");
    else await screen.findByText(/Запись восстановления изменилась/);
    expect(inspectAnalysisRecovery(key).attempt?.body).toBe(
      mode === "read failure" ? original.body : replacement.body,
    );
    expect(screen.queryByText(/Текущее предложение перечитано/)).toBeNull();
  },
);

it("shows failed revision history and retries without hiding the unavailable new-base choices", async () => {
  let unavailable = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      const limit = Number(url.searchParams.get("limit") ?? 100);
      if (limit > 100 || limit < 1)
        return new Response(
          JSON.stringify({
            error: { code: "backend_invalid", message: "limit must be between 1 and 100" },
          }),
          { status: 400 },
        );
      if (unavailable)
        return new Response(
          JSON.stringify({ error: { code: "unavailable", message: "history unavailable" } }),
          { status: 503 },
        );
      return new Response(
        JSON.stringify({
          items: [{ id: changeNextID, schemaVersion: "5", summary: "older new base" }],
          nextCursor: "",
        }),
        { status: 200 },
      );
    }),
  );
  renderWithProviders(
    <BackendChangeRebase projectId={changeTestID} detail={changeTestDetail()} onSaved={vi.fn()} />,
  );
  const retry = await screen.findByRole("button", {
    name: "Повторить загрузку истории исходных ревизий",
  });
  expect(screen.getByRole("alert")).toHaveTextContent("history unavailable");
  unavailable = false;
  await userEvent.setup().click(retry);
  expect(await screen.findByRole("option", { name: /older new base/ })).toBeInTheDocument();
});

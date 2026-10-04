import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import {
  BackendAnalysisRecoveryProvider,
  useBackendAnalysisRecovery,
  makeAnalysisAttempt,
  analysisRecoveryKey,
  inspectAnalysisRecovery,
} from "./backendAnalysisRecovery";
import { BackendAnalysisRecoveryNotice } from "./BackendAnalysisRecoveryNotice";
import { changeTestID, changeNextID } from "./backendChangeTestFixtures";
import { analysisTestDetail } from "./backendAnalysisTestFixtures";
function Start() {
  const r = useBackendAnalysisRecovery(changeTestID);
  return (
    <button
      disabled={r.blocked}
      onClick={() =>
        void r.execute(
          makeAnalysisAttempt(
            "start",
            { projectId: changeTestID },
            {
              kind: "impact",
              fromRevisionId: changeTestID,
              target: { revisionId: changeNextID },
              scope: {},
              limits: {},
              observationMode: "none",
              idempotencyKey: "original",
            },
          ),
        )
      }
    >
      Start test
    </button>
  );
}
function Surface() {
  return (
    <BackendAnalysisRecoveryProvider projectId={changeTestID}>
      <Start />
      <BackendAnalysisRecoveryNotice projectId={changeTestID} />
    </BackendAnalysisRecoveryProvider>
  );
}
beforeEach(() => {
  const map = new Map<string, string>();
  vi.stubGlobal("sessionStorage", {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => {
      map.set(key, value);
    },
    removeItem: (key: string) => {
      map.delete(key);
    },
    clear: () => map.clear(),
    key: (i: number) => [...map.keys()][i] ?? null,
    get length() {
      return map.size;
    },
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  sessionStorage.clear();
});
it("lost start survives remount and replays original stored bytes, then clears only after accepted receipt", async () => {
  const bodies: string[] = [];
  let lost = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_input, init) => {
      const body = String(init?.body);
      bodies.push(body);
      expect(inspectAnalysisRecovery(analysisRecoveryKey(changeTestID)).attempt?.body).toBe(body);
      if (lost) throw new TypeError("lost response");
      return new Response(JSON.stringify(analysisTestDetail().job), { status: 202 });
    }),
  );
  const user = userEvent.setup();
  const mounted = renderWithProviders(<Surface />);
  await user.click(screen.getByText("Start test"));
  await screen.findByText(/Исход запроса неизвестен/);
  expect(screen.getByText("Start test")).toBeDisabled();
  mounted.unmount();
  lost = false;
  renderWithProviders(<Surface />);
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await waitFor(() => expect(sessionStorage.getItem(analysisRecoveryKey(changeTestID))).toBeNull());
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toBe(bodies[0]);
});
it("blocks corrupt and unavailable storage before every network send", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  sessionStorage.setItem(analysisRecoveryKey(changeTestID), "bad");
  const mounted = renderWithProviders(<Surface />);
  expect(screen.getByText("Start test")).toBeDisabled();
  expect(fetcher).not.toHaveBeenCalled();
  mounted.unmount();
  sessionStorage.clear();
  vi.spyOn(sessionStorage, "setItem").mockImplementation(() => {
    throw new Error("quota");
  });
  renderWithProviders(<Surface />);
  await userEvent.setup().click(screen.getByText("Start test"));
  await screen.findByText(/Не удалось сохранить или очистить/);
  expect(fetcher).not.toHaveBeenCalled();
});
it("keeps accepted phase and known job when cleanup fails and never offers replay after reload", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(analysisTestDetail().job), { status: 202 })),
  );
  vi.spyOn(sessionStorage, "removeItem").mockImplementation(() => {
    throw new Error("cleanup");
  });
  const mounted = renderWithProviders(<Surface />);
  await userEvent.setup().click(screen.getByText("Start test"));
  await screen.findByText(/Ответ подтверждён/);
  const stored = inspectAnalysisRecovery(analysisRecoveryKey(changeTestID));
  expect(stored.attempt?.phase).toBe("accepted");
  expect(stored.attempt?.acceptance.jobId).toBe(changeTestID);
  mounted.unmount();
  renderWithProviders(<Surface />);
  expect(screen.queryByRole("button", { name: "Повторить исходный запрос" })).toBeNull();
  expect(screen.getByRole("button", { name: "Завершить очистку" })).toBeInTheDocument();
});
it("409 preserves exact original request until deliberate reconciliation", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ error: { code: "conflict", message: "changed" } }), {
          status: 409,
        }),
    ),
  );
  renderWithProviders(<Surface />);
  await userEvent.setup().click(screen.getByText("Start test"));
  await screen.findByText(/Конфликт: исходный запрос/);
  const stored = inspectAnalysisRecovery(analysisRecoveryKey(changeTestID));
  expect(stored.attempt?.input.idempotencyKey).toBe("original");
  expect(stored.attempt?.phase).toBe("conflict");
  expect(screen.queryByRole("button", { name: "Повторить исходный запрос" })).toBeNull();
});

it("awaits changed-base vector verification before acceptance or cleanup", async () => {
  const { changeTestDetail, changeHash } = await import("./backendChangeTestFixtures");
  const detail = changeTestDetail();
  function Rebase() {
    const r = useBackendAnalysisRecovery(changeTestID);
    return (
      <button
        onClick={() =>
          void r.execute(
            makeAnalysisAttempt(
              "rebase",
              { projectId: changeTestID, proposalId: changeTestID },
              {
                expectedVersion: 1,
                proposalRevisionId: detail.revision.id,
                newBaseRevisionId: changeNextID,
                identityResolutions: [],
                resolutions: [],
                repairCommands: [],
                candidateHash: changeHash,
                idempotencyKey: "rebase-original",
              },
              {
                newBaseSemanticHash: changeHash,
                sourceVectorHash: "f".repeat(64),
                sourceSnapshotIds: [],
                candidateSemanticHash: changeHash,
              },
            ),
          )
        }
      >
        Rebase test
      </button>
    );
  }
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            proposal: { ...detail.proposal, currentDraftRevisionId: changeNextID, version: 2 },
            revision: {
              ...detail.revision,
              id: changeNextID,
              parentRevisionId: detail.revision.id,
              baseRevisionId: changeNextID,
              rebase: { candidateHash: changeHash },
            },
            semanticHash: changeHash,
            changes: [],
          }),
          { status: 200 },
        ),
    ),
  );
  renderWithProviders(
    <BackendAnalysisRecoveryProvider projectId={changeTestID}>
      <Rebase />
      <BackendAnalysisRecoveryNotice projectId={changeTestID} />
    </BackendAnalysisRecoveryProvider>,
  );
  await userEvent.setup().click(screen.getByText("Rebase test"));
  await screen.findByText(/Rebase не подтвердил/);
  expect(inspectAnalysisRecovery(analysisRecoveryKey(changeTestID)).attempt?.phase).toBe("unknown");
  expect(
    inspectAnalysisRecovery(analysisRecoveryKey(changeTestID)).attempt?.input.idempotencyKey,
  ).toBe("rebase-original");
});

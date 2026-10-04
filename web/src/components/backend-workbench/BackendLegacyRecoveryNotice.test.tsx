import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendLegacyRecoveryNotice } from "./BackendLegacyRecoveryNotice";
import {
  changeTestDetail,
  changeTestID,
  changeNextID,
  changeHash,
} from "./backendChangeTestFixtures";
import {
  changeCreateRecoveryKey,
  makeChangeAttempt,
  writeChangeRecovery,
  inspectChangeRecovery,
} from "./backendChangeRecovery";
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  sessionStorage.clear();
});
it.each(["create", "apply"] as const)(
  "does not carry acceptance of request A onto unknown %s request B in the same slot",
  async (kind) => {
    const detail = changeTestDetail();
    const key =
      kind === "create"
        ? changeCreateRecoveryKey(changeTestID)
        : `backend-change-attempt:${changeTestID}:${detail.proposal.id}`;
    const attempt = (idempotencyKey: string) =>
      kind === "create"
        ? makeChangeAttempt("create", {
            name: idempotencyKey,
            baseRevisionId: detail.revision.baseRevisionId,
            idempotencyKey,
          })
        : makeChangeAttempt("apply", {
            expectedVersion: 1,
            proposalRevisionId: detail.revision.id,
            commands: [
              {
                type: "rename",
                recordType: "node",
                id: changeTestID,
                name: idempotencyKey,
                commandId: changeNextID,
                reason: "reason",
              },
            ],
            candidateHash: changeHash,
            idempotencyKey,
          });
    const a = attempt("A"),
      b = attempt("B"),
      sent: string[] = [];
    writeChangeRecovery(key, a, null);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method !== "POST") return new Response(JSON.stringify(detail), { status: 200 });
        sent.push(String(init.body));
        if (JSON.parse(String(init.body)).idempotencyKey === "B")
          throw new TypeError("lost B response");
        return new Response(
          JSON.stringify(
            kind === "create"
              ? detail
              : {
                  proposal: {
                    ...detail.proposal,
                    version: 2,
                    currentDraftRevisionId: changeNextID,
                  },
                  revision: {
                    ...detail.revision,
                    id: changeNextID,
                    parentRevisionId: detail.revision.id,
                  },
                  semanticHash: changeHash,
                  changes: [],
                },
          ),
          { status: 200 },
        );
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<BackendLegacyRecoveryNotice projectId={changeTestID} />);
    await user.click(
      screen.getByRole("button", { name: "Восстановить исходную операцию предложения" }),
    );
    await waitFor(() => expect(sessionStorage.getItem(key)).toBeNull());
    act(() => {
      writeChangeRecovery(key, b, null);
    });
    expect(screen.queryByText("Ответ подтверждён; очистка записи не завершена.")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Завершить восстановление предложения" }),
    ).toBeNull();
    await user.click(
      screen.getByRole("button", { name: "Восстановить исходную операцию предложения" }),
    );
    await screen.findByText("lost B response");
    expect(inspectChangeRecovery(key).attempt?.body).toBe(b.body);
    expect(sent).toEqual([a.body, b.body]);
    expect(
      screen.getByRole("button", { name: "Восстановить исходную операцию предложения" }),
    ).toBeEnabled();
  },
);

it("forgets accepted cleanup state when the exact stored request changes", async () => {
  const values = new Map<string, string>();
  vi.stubGlobal("sessionStorage", {
    get length() {
      return values.size;
    },
    key: (i: number) => [...values.keys()][i] ?? null,
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: () => {
      throw new Error("cleanup denied");
    },
    clear: () => values.clear(),
  });
  const detail = changeTestDetail(),
    key = changeCreateRecoveryKey(changeTestID);
  const a = makeChangeAttempt("create", {
      name: "A",
      baseRevisionId: changeTestID,
      idempotencyKey: "A",
    }),
    b = makeChangeAttempt("create", {
      name: "B",
      baseRevisionId: changeTestID,
      idempotencyKey: "B",
    });
  // A pre-versioned v1 slot is normalized by write/readback before replay.
  sessionStorage.setItem(key, JSON.stringify({ kind: a.kind, body: a.body, phase: a.phase }));
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(detail), { status: 200 })),
  );
  renderWithProviders(<BackendLegacyRecoveryNotice projectId={changeTestID} />);
  const user = userEvent.setup();
  await user.click(
    screen.getByRole("button", { name: "Восстановить исходную операцию предложения" }),
  );
  await screen.findByText("Ответ подтверждён; очистка записи не завершена.");
  const original = sessionStorage.getItem(key);
  expect(JSON.parse(original!).version).toBe(1);
  act(() => {
    writeChangeRecovery(key, b, original);
  });
  expect(screen.queryByText("Ответ подтверждён; очистка записи не завершена.")).toBeNull();
  expect(screen.queryByRole("button", { name: "Завершить восстановление предложения" })).toBeNull();
  expect(
    screen.getByRole("button", { name: "Восстановить исходную операцию предложения" }),
  ).toBeEnabled();
  expect(inspectChangeRecovery(key).attempt?.body).toBe(b.body);
});

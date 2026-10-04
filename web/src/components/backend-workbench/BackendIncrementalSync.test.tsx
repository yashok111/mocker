import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendIncrementalSync } from "./BackendIncrementalSync";
import {
  sourceSyncServer,
  syncCommands,
  syncIds,
  syncInventory,
  syncManifest,
} from "./backendSyncTestFixtures";
import { hashBackendImportCommands } from "./backendImportHash";
import { importRecoveryKey } from "./backendImportRecovery";
import type { BackendImportCommand, PutBackendImportBatchRequest } from "@/api/generated/schemas";

beforeEach(() => localStorage.clear());
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it("restores an unknown Begin after restart without sending until explicit retry", async () => {
  const server = sourceSyncServer({ lostBegin: true }),
    user = userEvent.setup();
  const first = renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await begin(user);
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  const original = server.calls.find(
    (call) => call.method === "POST" && call.path.endsWith("/imports"),
  )!;
  first.unmount();
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  expect(
    server.calls.filter((call) => call.method === "POST" && call.path.endsWith("/imports")),
  ).toEqual([original]);
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByLabelText("Пакет коллектора JSON");
  const requests = server.calls.filter(
    (call) => call.method === "POST" && call.path.endsWith("/imports"),
  );
  expect(requests).toHaveLength(2);
  expect(requests[1]).toEqual(original);
  expect(localStorage.length).toBe(0);
});

it.each(["batch", "decision", "commit", "abort"] as const)(
  "restores an unknown %s with exact IDs/body after restart",
  async (kind) => {
    const server = sourceSyncServer({
      existing: true,
      lostBatch: kind === "batch" || kind === "decision",
      lostCommit: kind === "commit",
      lostAbort: kind === "abort",
      conflicts: kind === "decision",
    });
    const user = userEvent.setup();
    const first = renderWithProviders(
      <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
    );
    if (kind === "batch") await upload(user);
    if (kind === "decision") {
      await preview(user);
      await user.selectOptions(
        await screen.findByLabelText(`Утверждение для ${syncIds.node}`),
        "1",
      );
      await user.type(screen.getByLabelText(/Причина выбора утверждения/), "Durable exact choice");
      await user.click(screen.getByRole("button", { name: "Отправить выбор утверждения" }));
    }
    if (kind === "commit") {
      await preview(user);
      await commit(user);
    }
    if (kind === "abort") {
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Прервать импорт" })).toBeEnabled(),
      );
      await user.click(screen.getByRole("button", { name: "Прервать импорт" }));
      await user.click(screen.getByRole("button", { name: "Подтвердить отмену импорта" }));
    }
    await screen.findByRole("button", { name: "Повторить исходный запрос" });
    const matches = (call: { path: string }) =>
      kind === "batch" || kind === "decision"
        ? call.path.includes("/batches/")
        : call.path.endsWith(`/${kind}`);
    const original = server.calls.find(matches)!;
    first.unmount();
    // The project entry point recovers the known session as well as its request.
    renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
    await screen.findByRole("button", { name: "Повторить исходный запрос" });
    expect(server.calls.filter(matches)).toEqual([original]);
    await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
    await waitFor(() => expect(server.calls.filter(matches)).toHaveLength(2));
    expect(server.calls.filter(matches)[1]).toEqual(original);
    if (kind === "commit") await screen.findByText(/Импорт сохранён в неизменяемой ревизии/);
    else if (kind === "abort") await screen.findByText(/Импорт отменён/);
    else await screen.findByText(/Принятый пакет: версия/);
    expect(localStorage.length).toBe(0);
  },
);

it("refuses dispatch when browser recovery cannot be stored and retains the unsent request", async () => {
  const server = sourceSyncServer(),
    user = userEvent.setup();
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  const save = vi.spyOn(localStorage, "setItem").mockImplementation(() => {
    throw new DOMException("Quota exceeded", "QuotaExceededError");
  });
  await begin(user);
  await screen.findByRole("button", { name: "Сохранить и отправить исходный запрос" });
  expect(
    server.calls.filter((call) => call.method === "POST" && call.path.endsWith("/imports")),
  ).toHaveLength(0);
  save.mockRestore();
  await user.click(screen.getByRole("button", { name: "Сохранить и отправить исходный запрос" }));
  await screen.findByLabelText("Пакет коллектора JSON");
  expect(
    server.calls.filter((call) => call.method === "POST" && call.path.endsWith("/imports")),
  ).toHaveLength(1);
});

it("clears the external candidate immediately when a newer session is admitted despite failed project reread", async () => {
  const server = sourceSyncServer({ existing: true }),
    user = userEvent.setup(),
    candidate = vi.fn();
  renderWithProviders(
    <BackendIncrementalSync
      projectId={syncIds.project}
      initialSessionId={syncIds.session}
      onCandidateChange={candidate}
    />,
  );
  await preview(user);
  await user.click(await screen.findByRole("button", { name: "Открыть точный кандидат" }));
  expect(candidate.mock.calls.at(-1)?.[0]).toMatchObject({ importCandidate: { importVersion: 2 } });
  const original = server.fetchMock.getMockImplementation()!;
  await original(`/api/backend-projects/${syncIds.project}/imports/${syncIds.session}/preview`, {
    method: "POST",
    body: JSON.stringify({ expectedImportVersion: 2, baseRevisionId: syncIds.revision }),
  });
  server.fetchMock.mockImplementation(async (input, init) =>
    new URL(String(input), "http://localhost").pathname ===
    `/api/backend-projects/${syncIds.project}`
      ? new Response(
          JSON.stringify({ error: { code: "unavailable", message: "Project read unavailable" } }),
          { status: 503 },
        )
      : original(input, init),
  );
  await user.click(screen.getByRole("button", { name: "Перечитать сессию и проект" }));
  await screen.findAllByText("ready · версия 3");
  await screen.findByText(/Project read unavailable/);
  expect(candidate).toHaveBeenLastCalledWith(null);
});

it("keeps invalid browser recovery quarantined until an explicit confirmed discard", async () => {
  const server = sourceSyncServer(),
    user = userEvent.setup();
  const key = importRecoveryKey(syncIds.project),
    raw = '{"version":9007199254740993}';
  localStorage.setItem(key, raw);
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await screen.findByText(/Сохранённая попытка повреждена/);
  await screen.findByRole("heading", { name: "Настройка источника" });
  expect(
    screen.queryByRole("button", { name: "Повторить исходный запрос" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("Область синхронизации")).toBeDisabled();
  expect(localStorage.getItem(key)).toBe(raw);
  await user.click(screen.getByRole("button", { name: "Удалить локальную запись восстановления" }));
  expect(localStorage.getItem(key)).toBe(raw);
  await user.click(
    screen.getByRole("button", { name: "Подтвердить удаление записи восстановления" }),
  );
  await waitFor(() => expect(screen.getByLabelText("Область синхронизации")).toBeEnabled());
  expect(localStorage.getItem(key)).toBeNull();
  expect(server.calls.every((call) => call.method === "GET")).toBe(true);
});

it("blocks dispatch if storage cannot be read and recovers only after an explicit reread", async () => {
  const server = sourceSyncServer(),
    user = userEvent.setup();
  const read = vi.spyOn(localStorage, "getItem").mockImplementation(() => {
    throw new DOMException("Denied", "SecurityError");
  });
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await screen.findByRole("button", { name: "Повторить чтение восстановления" });
  await screen.findByRole("heading", { name: "Настройка источника" });
  expect(screen.getByLabelText("Область синхронизации")).toBeDisabled();
  read.mockRestore();
  await user.click(screen.getByRole("button", { name: "Повторить чтение восстановления" }));
  await waitFor(() => expect(screen.getByLabelText("Область синхронизации")).toBeEnabled());
  expect(server.calls.every((call) => call.method === "GET")).toBe(true);
});

it("requires an explicit switch before replaying recovery bound to a different selected session", async () => {
  const server = sourceSyncServer({ existing: true, lostBatch: true }),
    user = userEvent.setup();
  const first = renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
  );
  await upload(user);
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  first.unmount();
  renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.later} />,
  );
  await screen.findByRole("button", { name: "Открыть сохранённое восстановление" });
  expect(screen.getByRole("button", { name: "Повторить исходный запрос" })).toBeDisabled();
  expect(server.calls.some((call) => call.path.includes(`/imports/${syncIds.later}`))).toBe(false);
  await user.click(screen.getByRole("button", { name: "Открыть сохранённое восстановление" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Повторить исходный запрос" })).toBeEnabled(),
  );
  expect(server.calls.filter((call) => call.path.includes("/batches/"))).toHaveLength(1);
});

it("persists before dispatch and ignores a late receipt from an unmounted browser component", async () => {
  const server = sourceSyncServer(),
    user = userEvent.setup(),
    original = server.fetchMock.getMockImplementation()!;
  const key = importRecoveryKey(syncIds.project);
  let release: (() => void) | undefined,
    held = false;
  server.fetchMock.mockImplementation(async (input, init) => {
    if (init?.method === "POST" && String(input).endsWith("/imports")) {
      expect(JSON.parse(localStorage.getItem(key)!).body).toBe(init.body);
      const response = await original(input, init);
      if (!held) {
        held = true;
        await new Promise<void>((resolve) => {
          release = resolve;
        });
      }
      return response;
    }
    return original(input, init);
  });
  const first = renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await begin(user);
  await waitFor(() => expect(release).toBeDefined());
  first.unmount();
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  await act(async () => {
    release!();
  });
  expect(localStorage.getItem(key)).not.toBeNull();
  expect(
    server.calls.filter((call) => call.method === "POST" && call.path.endsWith("/imports")),
  ).toHaveLength(1);
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByLabelText("Пакет коллектора JSON");
  expect(localStorage.getItem(key)).toBeNull();
});

it("keeps a confirmed receipt when clearing storage fails and retries cleanup without resending", async () => {
  const server = sourceSyncServer(),
    user = userEvent.setup();
  const remove = vi.spyOn(localStorage, "removeItem").mockImplementation(() => {
    throw new DOMException("Denied", "SecurityError");
  });
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await begin(user);
  await screen.findByRole("button", { name: "Повторить очистку восстановления" });
  expect(screen.getByLabelText("Пакет коллектора JSON")).toBeDisabled();
  expect(localStorage.getItem(importRecoveryKey(syncIds.project))).not.toBeNull();
  remove.mockRestore();
  await user.click(screen.getByRole("button", { name: "Повторить очистку восстановления" }));
  await waitFor(() => expect(screen.getByLabelText("Пакет коллектора JSON")).toBeEnabled());
  expect(
    server.calls.filter((call) => call.method === "POST" && call.path.endsWith("/imports")),
  ).toHaveLength(1);
  expect(localStorage.getItem(importRecoveryKey(syncIds.project))).toBeNull();
});

it("keeps the in-memory unknown request when its durable slot disappears before retry", async () => {
  const server = sourceSyncServer({ lostBegin: true }),
    user = userEvent.setup();
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await begin(user);
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  const original = server.calls.find(
    (call) => call.method === "POST" && call.path.endsWith("/imports"),
  )!;
  localStorage.removeItem(importRecoveryKey(syncIds.project));
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByRole("button", { name: "Повторить чтение восстановления" });
  expect(
    server.calls.filter((call) => call.method === "POST" && call.path.endsWith("/imports")),
  ).toHaveLength(1);
  await user.click(screen.getByRole("button", { name: "Повторить чтение восстановления" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Повторить исходный запрос" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByLabelText("Пакет коллектора JSON");
  expect(
    server.calls.filter((call) => call.method === "POST" && call.path.endsWith("/imports")),
  ).toEqual([original, original]);
});
async function begin(user: ReturnType<typeof userEvent.setup>, manifest = syncManifest) {
  await screen.findByRole("heading", { name: "Настройка источника" });
  await user.selectOptions(screen.getByLabelText("Полнота выбранной области"), "complete");
  await user.upload(
    screen.getByLabelText("Манифест JSON"),
    new File([JSON.stringify(manifest)], "manifest.json", { type: "application/json" }),
  );
  await user.upload(
    screen.getByLabelText("Inventory JSON — девять категорий"),
    new File([JSON.stringify(syncInventory)], "inventory.json", { type: "application/json" }),
  );
  await waitFor(() =>
    expect(screen.getByLabelText("Я проверил область, манифест и inventory")).toBeEnabled(),
  );
  await user.click(screen.getByLabelText("Я проверил область, манифест и inventory"));
  await user.click(screen.getByRole("button", { name: "Начать синхронизацию" }));
}
async function upload(user: ReturnType<typeof userEvent.setup>, commands = syncCommands()) {
  await user.upload(
    await screen.findByLabelText("Пакет коллектора JSON"),
    new File([JSON.stringify({ commands })], "batch.json", { type: "application/json" }),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Отправить пакет" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Отправить пакет" }));
}
async function preview(user: ReturnType<typeof userEvent.setup>) {
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Выполнить Preview" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Выполнить Preview" }));
}
async function commit(user: ReturnType<typeof userEvent.setup>) {
  await waitFor(() =>
    expect(screen.getByLabelText("Я проверил этот Preview и версию проекта")).toBeEnabled(),
  );
  await user.click(screen.getByLabelText("Я проверил этот Preview и версию проекта"));
  await user.click(screen.getByRole("button", { name: "Подтвердить Commit" }));
}

it("runs reachable begin/upload/preview/exact-inspector/commit with the protocol command hash", async () => {
  const server = sourceSyncServer(),
    user = userEvent.setup(),
    onCandidateChange = vi.fn(),
    onCommitted = vi.fn();
  renderWithProviders(
    <BackendIncrementalSync
      projectId={syncIds.project}
      onCandidateChange={onCandidateChange}
      onCommitted={onCommitted}
    />,
  );
  await begin(user);
  await upload(user);
  await screen.findByText("Принятый пакет: версия 2");
  const batch = server.calls.find((call) => call.path.includes("/batches/"))!;
  const request = JSON.parse(batch.body) as PutBackendImportBatchRequest;
  expect(request.payloadHash).toBe(await hashBackendImportCommands(request.commands));
  expect(request.commands).toEqual(syncCommands());
  await preview(user);
  await user.click(await screen.findByRole("button", { name: "Открыть точный кандидат" }));
  expect(onCandidateChange).toHaveBeenLastCalledWith({
    importCandidate: {
      importId: syncIds.session,
      importVersion: 3,
      candidateHash: server.session().candidateHash,
    },
  });
  await commit(user);
  await screen.findByText(/Импорт сохранён в неизменяемой ревизии/);
  expect(screen.getByRole("region", { name: "Результат синхронизации" })).toHaveFocus();
  expect(onCandidateChange).toHaveBeenLastCalledWith(null);
  await user.click(screen.getByRole("button", { name: "Открыть сохранённую ревизию" }));
  expect(onCommitted).toHaveBeenCalledWith(syncIds.committed);
  expect(screen.queryByRole("button", { name: "Подтвердить Commit" })).not.toBeInTheDocument();
});

it("retries a lost Begin with its original captured scope, body, key and CAS", async () => {
  const server = sourceSyncServer({ lostBegin: true }),
    user = userEvent.setup();
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await begin(user);
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  expect(screen.getByLabelText("Область синхронизации")).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByLabelText("Пакет коллектора JSON");
  const requests = server.calls.filter(
    (call) => call.path.endsWith("/imports") && call.method === "POST",
  );
  expect(requests).toHaveLength(2);
  expect(requests[1]?.body).toBe(requests[0]?.body);
});

it("retains an independent batch ID/hash/body across a lost response", async () => {
  const server = sourceSyncServer({ existing: true, lostBatch: true }),
    user = userEvent.setup();
  renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
  );
  await upload(user);
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  expect(screen.getByLabelText("Пакет коллектора JSON")).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByText("Принятый пакет: версия 2");
  const requests = server.calls.filter((call) => call.path.includes("/batches/"));
  expect(requests).toHaveLength(2);
  expect(requests[1]).toEqual(requests[0]);
  server.recover();
  await user.click(screen.getByRole("button", { name: "Перечитать сессию и проект" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Выполнить Preview" })).toBeEnabled(),
  );
});

it("replays a lost Commit without substituting the current head for its historical receipt", async () => {
  const server = sourceSyncServer({ existing: true, lostCommit: true }),
    user = userEvent.setup(),
    onCommitted = vi.fn();
  renderWithProviders(
    <BackendIncrementalSync
      projectId={syncIds.project}
      initialSessionId={syncIds.session}
      onCommitted={onCommitted}
    />,
  );
  await preview(user);
  await commit(user);
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  server.advanceProject();
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByText(/Импорт сохранён в неизменяемой ревизии/);
  const requests = server.calls.filter((call) => call.path.endsWith("/commit"));
  expect(requests).toHaveLength(2);
  expect(requests[1]?.body).toBe(requests[0]?.body);
  await user.click(screen.getByRole("button", { name: "Открыть сохранённую ревизию" }));
  expect(onCommitted).toHaveBeenCalledWith(syncIds.committed);
  expect(onCommitted).not.toHaveBeenCalledWith(syncIds.later);
});

it("requires explicit reload and a newly reviewed project CAS after metadata-only 409", async () => {
  const server = sourceSyncServer({ existing: true, metadataConflict: true }),
    user = userEvent.setup();
  renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
  );
  await preview(user);
  await commit(user);
  await screen.findByText(/Получен 409/);
  expect(screen.getByRole("button", { name: "Подтвердить Commit" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Перечитать сессию и проект" }));
  await preview(user);
  await commit(user);
  await screen.findByText(/Импорт сохранён в неизменяемой ревизии/);
  const requests = server.calls
    .filter((call) => call.path.endsWith("/commit"))
    .map((call) => JSON.parse(call.body) as { expectedVersion: number; idempotencyKey: string });
  expect(requests.map((body) => body.expectedVersion)).toEqual([3, 4]);
  expect(requests[0]?.idempotencyKey).not.toBe(requests[1]?.idempotencyKey);
});

it("retries abort exactly and keeps terminal controls closed", async () => {
  const server = sourceSyncServer({ existing: true, lostAbort: true }),
    user = userEvent.setup();
  renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Прервать импорт" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Прервать импорт" }));
  await user.click(screen.getByRole("button", { name: "Подтвердить отмену импорта" }));
  await screen.findByRole("button", { name: "Повторить исходный запрос" });
  await user.click(screen.getByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByText(/Импорт отменён/);
  expect(screen.getByRole("region", { name: "Результат синхронизации" })).toHaveFocus();
  const requests = server.calls.filter((call) => call.path.endsWith("/abort"));
  expect(requests).toHaveLength(2);
  expect(requests[0]?.body).toBe(requests[1]?.body);
  expect(screen.queryByRole("button", { name: "Выполнить Preview" })).not.toBeInTheDocument();
});

it("uses saved NEEDS_RESOLUTION pages and requires a fresh decision for a new conflict pin", async () => {
  const server = sourceSyncServer({ existing: true, conflicts: true, paged: true }),
    user = userEvent.setup();
  renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
  );
  await preview(user);
  await screen.findByLabelText(`Утверждение для ${syncIds.node}`);
  expect(server.calls.some((call) => call.path.includes("/candidate/"))).toBe(false);
  await user.click(screen.getByRole("button", { name: "Следующие решения синхронизации" }));
  await screen.findByLabelText(`Утверждение для ${syncIds.later}`);
  expect(
    server.calls.some(
      (call) => call.path.includes("cursor=page-2") && call.path.includes("previewVersion=2"),
    ),
  ).toBe(true);
  await user.click(screen.getByRole("button", { name: "Предыдущие решения синхронизации" }));
  await user.selectOptions(await screen.findByLabelText(`Утверждение для ${syncIds.node}`), "1");
  await user.type(screen.getByLabelText(/Причина выбора утверждения/), "Reviewed existing value");
  await user.click(screen.getByRole("button", { name: "Отправить выбор утверждения" }));
  await waitFor(() =>
    expect(screen.queryByLabelText(`Утверждение для ${syncIds.node}`)).not.toBeInTheDocument(),
  );
  await preview(user);
  await screen.findByText(/После пересчёта конфликт изменился/);
  expect(screen.getByLabelText(`Утверждение для ${syncIds.node}`)).toHaveValue("");
  const requests = server.calls.filter((call) => call.path.includes("/batches/"));
  expect(requests).toHaveLength(1);
  const request = JSON.parse(requests[0]!.body) as PutBackendImportBatchRequest;
  expect(request.commands[0]).toMatchObject({
    op: "resolve_assertion",
    resolution: {
      conflictHash: "e".repeat(64),
      select: { providerNamespace: "other", assertionHash: "2".repeat(64) },
      property: { kind: "name" },
    },
  });
  expect(screen.queryByLabelText(/replacement/i)).not.toBeInTheDocument();
});

it("shows closure-limit refusal without truncating or switching the requested policy", async () => {
  const server = sourceSyncServer({ existing: true, closureLimit: true }),
    user = userEvent.setup();
  renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
  );
  await preview(user);
  await screen.findByText(/100000 visits/);
  expect(server.calls.filter((call) => call.path.endsWith("/preview"))).toHaveLength(1);
  expect(
    server.calls.filter((call) => call.path.endsWith("/imports") && call.method === "POST"),
  ).toHaveLength(0);
  expect(screen.queryByRole("button", { name: "Подтвердить Commit" })).not.toBeInTheDocument();
});

it("bootstraps source5 explicitly, offers only the old owner, then sends claim before its own upsert/proof", async () => {
  const server = sourceSyncServer({ schema: "5" }),
    user = userEvent.setup();
  renderWithProviders(<BackendIncrementalSync projectId={syncIds.project} />);
  await screen.findByRole("heading", { name: "Настройка источника" });
  await user.selectOptions(screen.getByLabelText("Область синхронизации"), "add_provider");
  await user.click(screen.getByLabelText("Явно расширить events-service-v1 до composed-source-v1"));
  await begin(user, {
    ...syncManifest,
    provider: { ...syncManifest.provider, namespace: "new-provider" },
  });
  await preview(user);
  await user.upload(
    await screen.findByLabelText("Пакет коллектора JSON"),
    new File([JSON.stringify({ commands: syncCommands("incoming") })], "own.json", {
      type: "application/json",
    }),
  );
  await user.selectOptions(await screen.findByLabelText("Входящий объект"), "0");
  await user.click(screen.getByRole("button", { name: "Загрузить утверждения базы" }));
  await waitFor(() =>
    expect(
      screen.getByLabelText("Точное утверждение базы").querySelectorAll("option"),
    ).toHaveLength(2),
  );
  const option = screen.getByLabelText("Точное утверждение базы").querySelectorAll("option")[1]!;
  await user.selectOptions(screen.getByLabelText("Точное утверждение базы"), option.value);
  await user.click(screen.getByLabelText("own-proof"));
  await user.type(screen.getByLabelText(/Причина общей идентичности/), "Same existing handler");
  await user.click(screen.getByRole("button", { name: "Добавить claim_identity в пакет" }));
  await user.click(screen.getByRole("button", { name: "Отправить пакет" }));
  await screen.findByText("Принятый пакет: версия 3");
  const request = JSON.parse(
    server.calls.find((call) => call.path.includes("/batches/"))!.body,
  ) as { commands: BackendImportCommand[] };
  expect(request.commands[0]).toMatchObject({
    op: "claim_identity",
    claimIdentity: {
      externalKey: "incoming",
      target: { providerNamespace: "ast", expectedId: syncIds.node, assertionHash: "c".repeat(64) },
      evidenceKeys: ["own-proof"],
    },
  });
  expect(request.commands[1]?.op).toBe("upsert_node");
  expect(
    server.calls
      .filter((call) => call.path.includes("/assertions"))
      .every(
        (call) =>
          call.path.includes("/candidate/assertions") && call.path.includes("importVersion=2"),
      ),
  ).toBe(true);
  const requestBegin = JSON.parse(
    server.calls.find((call) => call.method === "POST" && call.path.endsWith("/imports"))!.body,
  ) as { inventory: unknown[]; profileExtension: unknown };
  expect(requestBegin.inventory).toHaveLength(9);
  expect(requestBegin.profileExtension).toEqual({
    fromProfile: "events-service-v1",
    toProfile: "composed-source-v1",
  });
});

it("ignores a delayed old status after a new batch and preview without restoring its candidate", async () => {
  const server = sourceSyncServer({ existing: true }),
    user = userEvent.setup(),
    onCandidateChange = vi.fn();
  renderWithProviders(
    <BackendIncrementalSync
      projectId={syncIds.project}
      initialSessionId={syncIds.session}
      onCandidateChange={onCandidateChange}
    />,
  );
  await preview(user);
  await user.click(await screen.findByRole("button", { name: "Открыть точный кандидат" }));
  const oldTarget = onCandidateChange.mock.calls.at(-1)?.[0];
  const original = server.fetchMock.getMockImplementation()!;
  let holdNext = true,
    release: (() => void) | undefined;
  server.fetchMock.mockImplementation(async (input, init) => {
    const response = await original(input, init);
    if (holdNext && String(input).endsWith(`/imports/${syncIds.session}?limit=100`)) {
      holdNext = false;
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    }
    return response;
  });
  await user.click(screen.getByRole("button", { name: "Перечитать сессию и проект" }));
  await waitFor(() => expect(release).toBeDefined());
  await upload(user);
  await screen.findByText("Принятый пакет: версия 3");
  await preview(user);
  await user.click(await screen.findByRole("button", { name: "Открыть точный кандидат" }));
  const latest = onCandidateChange.mock.calls.at(-1)?.[0];
  expect(latest).toMatchObject({ importCandidate: { importVersion: 4 } });
  expect(latest).not.toEqual(oldTarget);
  await act(async () => {
    release!();
  });
  expect(onCandidateChange).toHaveBeenLastCalledWith(latest);
  expect(screen.getAllByText("ready · версия 4")).toHaveLength(2);
  expect(screen.getByRole("button", { name: "Выполнить Preview" })).toBeEnabled();
});

it("retries a lost assertion decision with the original decision ID, conflict pin, batch ID and hash", async () => {
  const server = sourceSyncServer({ existing: true, conflicts: true, lostBatch: true }),
    user = userEvent.setup();
  renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
  );
  await preview(user);
  await user.selectOptions(await screen.findByLabelText(`Утверждение для ${syncIds.node}`), "1");
  await user.type(screen.getByLabelText(/Причина выбора утверждения/), "Reviewed decision");
  await user.click(screen.getByRole("button", { name: "Отправить выбор утверждения" }));
  await user.click(await screen.findByRole("button", { name: "Повторить исходный запрос" }));
  await screen.findByText("Принятый пакет: версия 3");
  const attempts = server.calls.filter((call) => call.path.includes("/batches/"));
  expect(attempts).toHaveLength(2);
  expect(attempts[0]).toEqual(attempts[1]);
  const body = JSON.parse(attempts[1]!.body) as PutBackendImportBatchRequest;
  expect(body.payloadHash).toBe(await hashBackendImportCommands(body.commands));
  expect(body.commands[0]).toMatchObject({
    op: "resolve_assertion",
    resolution: {
      decisionId: expect.any(String),
      conflictHash: "e".repeat(64),
      select: { providerNamespace: "other" },
    },
  });
});

it("clears an obsolete assertion read and allows a new selection after preview changes", async () => {
  const server = sourceSyncServer({ existing: true }),
    user = userEvent.setup();
  renderWithProviders(
    <BackendIncrementalSync projectId={syncIds.project} initialSessionId={syncIds.session} />,
  );
  await user.upload(
    await screen.findByLabelText("Пакет коллектора JSON"),
    new File([JSON.stringify({ commands: syncCommands() })], "own.json", {
      type: "application/json",
    }),
  );
  await user.selectOptions(await screen.findByLabelText("Входящий объект"), "0");
  const original = server.fetchMock.getMockImplementation()!;
  let release: (() => void) | undefined;
  server.fetchMock.mockImplementation(async (input, init) => {
    const response = await original(input, init);
    if (String(input).includes("/assertions?"))
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    return response;
  });
  await user.click(screen.getByRole("button", { name: "Загрузить утверждения базы" }));
  await waitFor(() => expect(release).toBeDefined());
  await preview(user);
  await screen.findByText("Сохранённый Preview");
  await act(async () => {
    release!();
  });
  expect(screen.getByRole("button", { name: "Загрузить утверждения базы" })).toBeEnabled();
  expect(screen.getByLabelText("Точное утверждение базы").querySelectorAll("option")).toHaveLength(
    1,
  );
});

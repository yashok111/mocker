import { StrictMode } from "react";
import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders, makeQueryClient } from "@/test/render";
import { BackendChangeEditor } from "./BackendChangeEditor";
import { BackendChangeProposals } from "./BackendChangeProposals";
import {
  changeCreateRecoveryKey,
  makeChangeAttempt,
  writeChangeRecovery,
} from "./backendChangeRecovery";
import {
  changeDraftID,
  changeHash,
  changeNextID,
  changeTestDetail,
  changeTestID,
} from "./backendChangeTestFixtures";

vi.mock("./BackendEffectiveViews", () => ({
  BackendEffectiveViews: ({
    target,
  }: {
    target: { changeProposal: { proposalRevisionId: string } };
  }) => <div data-testid="exact-draft">{target.changeProposal.proposalRevisionId}</div>,
}));
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
function setup(
  options: {
    loseApply?: boolean;
    loseRestore?: boolean;
    conflict?: boolean;
    badPreview?: boolean;
    loseCreate?: boolean;
  } = {},
) {
  let current = changeTestDetail();
  const history = new Map([[current.revision.id, current.revision]]);
  const attempts: string[] = [];
  const restores: string[] = [];
  const creates: string[] = [];
  const previews: Record<string, unknown>[] = [];
  const gets: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      const path = url.pathname;
      const body = init?.body ? JSON.parse(String(init?.body)) : {};
      if (path.endsWith("/graph/query")) {
        const revisionId = body.changeProposal.proposalRevisionId;
        return response({
          nodes: [],
          edges: [],
          identities: [],
          nextCursor: "",
          viewSchemaVersion: "proposal-graph-v1",
          target: { changeProposal: body.changeProposal },
          pins: {
            targetHash: changeHash,
            baseRevisionId: changeTestID,
            baseSemanticHash: changeHash,
            viewSchemaVersion: "proposal-graph-v1",
            structuralSchemaVersion: "6",
            effectiveSemanticHash: changeHash,
          },
          readRevision: revisionId,
        });
      }
      if (path.endsWith("/change-proposals") && init?.method === "GET")
        return response({ items: [current.proposal], nextCursor: "" });
      if (path.endsWith("/change-proposals") && init?.method === "POST") {
        creates.push(String(init?.body));
        if (options.loseCreate && creates.length === 1) throw new TypeError("Lost create response");
        return response(current);
      }
      if (path.endsWith("/preview")) {
        previews.push(body);
        return response({
          proposalId: changeTestID,
          expectedVersion: body.expectedVersion,
          proposalRevisionId: options.badPreview ? changeNextID : body.proposalRevisionId,
          draftHash: changeHash,
          baseRevisionId: changeTestID,
          baseSemanticHash: changeHash,
          documentVersion: "proposal-graph-v1",
          changes: [],
          criteria: [],
          diagnostics: [],
          semanticHash: changeHash,
          candidateHash: "b".repeat(64),
        });
      }
      if (path.endsWith("/commands")) {
        attempts.push(String(init?.body));
        if (options.conflict)
          return response(
            { error: { code: "backend_change_conflict", message: "Draft changed" } },
            409,
          );
        const next = {
          ...current.revision,
          id: changeNextID,
          parentRevisionId: current.revision.id,
        };
        current = {
          ...current,
          proposal: { ...current.proposal, version: 2, currentDraftRevisionId: changeNextID },
          revision: next,
          history: [
            {
              id: changeNextID,
              parentRevisionId: changeDraftID,
              semanticHash: changeHash,
              summary: "Applied",
              author: "user",
              createdAt: next.createdAt,
            },
            ...current.history,
          ],
        };
        history.set(changeNextID, next);
        if (options.loseApply && attempts.length === 1) throw new TypeError("Lost apply response");
        return response({
          proposal: current.proposal,
          revision: current.revision,
          changes: [],
          semanticHash: changeHash,
        });
      }
      if (path.endsWith("/restore")) {
        restores.push(String(init?.body));
        const restoredId = "0197aaf9-5555-7000-8000-000000000004";
        const revision = {
          ...history.get(body.restoreRevisionId)!,
          id: restoredId,
          parentRevisionId: current.revision.id,
        };
        current = {
          ...current,
          proposal: { ...current.proposal, version: 3, currentDraftRevisionId: restoredId },
          revision,
        };
        history.set(restoredId, revision);
        if (options.loseRestore && restores.length === 1)
          throw new TypeError("Lost restore response");
        return response({
          proposal: current.proposal,
          revision,
          changes: [],
          semanticHash: changeHash,
        });
      }
      if (path.endsWith(`/change-proposals/${changeTestID}`)) {
        const selected = url.searchParams.get("proposalRevisionId");
        gets.push(selected ?? "discover");
        return response({
          ...current,
          revision: selected ? history.get(selected) : current.revision,
        });
      }
      return response({ error: { code: "unexpected", message: path } }, 500);
    }),
  );
  return {
    attempts,
    restores,
    creates,
    previews,
    gets,
    get current() {
      return current;
    },
    advance() {
      current = {
        ...current,
        proposal: { ...current.proposal, version: 2, currentDraftRevisionId: changeNextID },
        revision: { ...current.revision, id: changeNextID },
      };
      history.set(changeNextID, current.revision);
    },
  };
}
function addRename(name = "Desired name") {
  fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
    target: { value: "Clearer name" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "UUID объекта" }), {
    target: { value: changeTestID },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Название" }), { target: { value: name } });
  fireEvent.click(screen.getByRole("button", { name: "Добавить команду" }));
}
async function preview() {
  fireEvent.click(screen.getByRole("button", { name: "Проверить изменения" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Сохранить предложение" })).toBeEnabled(),
  );
}

describe("full proposal exact attempt lifecycle", () => {
  it("retains exact apply bytes and command IDs after a lost response", async () => {
    const api = setup({ loseApply: true });
    const onSaved = vi.fn();
    renderWithProviders(
      <BackendChangeEditor
        projectId={changeTestID}
        detail={changeTestDetail()}
        onSaved={onSaved}
      />,
    );
    addRename();
    await preview();
    fireEvent.click(screen.getByRole("button", { name: "Сохранить предложение" }));
    fireEvent.click(await screen.findByRole("button", { name: "Повторить тот же запрос" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(api.attempts).toHaveLength(2);
    expect(api.attempts[1]).toBe(api.attempts[0]);
    expect(JSON.parse(api.attempts[0]!).proposalRevisionId).toBe(changeDraftID);
    expect(screen.getByTestId("exact-draft")).toHaveTextContent(changeNextID);
    addRename("Second name");
    await preview();
    expect((api.previews[1]!.commands as { commandId: string }[])[0]!.commandId).not.toBe(
      (api.previews[0]!.commands as { commandId: string }[])[0]!.commandId,
    );
  });
  it("persists an unknown attempt across unmount and never sends it automatically", async () => {
    const api = setup({ loseApply: true });
    const rendered = renderWithProviders(
      <BackendChangeEditor
        projectId={changeTestID}
        detail={changeTestDetail()}
        onSaved={vi.fn()}
      />,
    );
    addRename();
    await preview();
    fireEvent.click(screen.getByRole("button", { name: "Сохранить предложение" }));
    await screen.findByRole("button", { name: "Повторить тот же запрос" });
    rendered.unmount();
    renderWithProviders(
      <BackendChangeEditor projectId={changeTestID} detail={api.current} onSaved={vi.fn()} />,
    );
    expect(api.attempts).toHaveLength(1);
    fireEvent.click(await screen.findByRole("button", { name: "Повторить тот же запрос" }));
    await waitFor(() => expect(api.attempts).toHaveLength(2));
    expect(api.attempts[1]).toBe(api.attempts[0]);
  });
  it("locks a 409 until explicit reread and reconciliation without regenerating IDs", async () => {
    const api = setup({ conflict: true });
    renderWithProviders(
      <BackendChangeEditor
        projectId={changeTestID}
        detail={changeTestDetail()}
        onSaved={vi.fn()}
      />,
    );
    addRename();
    await preview();
    fireEvent.click(screen.getByRole("button", { name: "Сохранить предложение" }));
    await screen.findByText(/Старый запрос заблокирован/);
    expect(screen.queryByRole("button", { name: "Повторить тот же запрос" })).toBeNull();
    api.advance();
    fireEvent.click(screen.getByRole("button", { name: "Загрузить для сверки" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Продолжить с локальными командами" }),
    );
    expect(screen.getByRole("button", { name: "Сохранить предложение" })).toBeDisabled();
    await preview();
    const first = JSON.parse(api.attempts[0]!);
    expect((api.previews[1]!.commands as { commandId: string }[])[0]!.commandId).toBe(
      first.commands[0].commandId,
    );
    expect(api.previews[1]!.proposalRevisionId).toBe(changeNextID);
    expect(api.attempts).toHaveLength(1);
  });
  it("local undo restores order and invalidates preview", async () => {
    const api = setup();
    renderWithProviders(
      <BackendChangeEditor
        projectId={changeTestID}
        detail={changeTestDetail()}
        onSaved={vi.fn()}
      />,
    );
    addRename("First");
    addRename("Second");
    await preview();
    fireEvent.click(screen.getByRole("button", { name: "Убрать команду 1" }));
    expect(screen.getByRole("button", { name: "Сохранить предложение" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Отменить локальное действие" }));
    await preview();
    expect(api.previews[1]!.commands).toEqual(api.previews[0]!.commands);
  });
  it("refuses a preview with different immutable pins", async () => {
    setup({ badPreview: true });
    renderWithProviders(
      <BackendChangeEditor
        projectId={changeTestID}
        detail={changeTestDetail()}
        onSaved={vi.fn()}
      />,
    );
    addRename();
    fireEvent.click(screen.getByRole("button", { name: "Проверить изменения" }));
    await screen.findByText("Предпросмотр относится к другой ревизии");
    expect(screen.getByRole("button", { name: "Сохранить предложение" })).toBeDisabled();
  });
  it("restores only an explicitly selected historical draft and retries identical bytes", async () => {
    const api = setup({ loseRestore: true });
    api.advance();
    const detail = api.current;
    detail.history = [
      {
        id: changeDraftID,
        parentRevisionId: null,
        semanticHash: changeHash,
        author: "user",
        summary: "Original",
        createdAt: detail.revision.createdAt,
      },
    ];
    const onSaved = vi.fn();
    renderWithProviders(
      <BackendChangeEditor projectId={changeTestID} detail={detail} onSaved={onSaved} />,
    );
    fireEvent.change(screen.getByRole("combobox", { name: "Ревизия для восстановления" }), {
      target: { value: changeDraftID },
    });
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Создать новый черновик с выбранным состоянием" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Восстановить выбранную ревизию" }));
    fireEvent.click(await screen.findByRole("button", { name: "Повторить тот же запрос" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(api.restores[1]).toBe(api.restores[0]);
    expect(JSON.parse(api.restores[0]!)).toMatchObject({
      proposalRevisionId: changeNextID,
      restoreRevisionId: changeDraftID,
      expectedVersion: 2,
    });
  });
  it("shows historical exact graph as read only", () => {
    setup();
    const detail = changeTestDetail();
    detail.proposal.currentDraftRevisionId = changeNextID;
    renderWithProviders(
      <BackendChangeEditor projectId={changeTestID} detail={detail} onSaved={vi.fn()} />,
    );
    expect(screen.getByTestId("exact-draft")).toHaveTextContent(changeDraftID);
    expect(screen.queryByRole("button", { name: "Добавить команду" })).toBeNull();
    expect(screen.getByText("Исторический черновик")).toBeInTheDocument();
  });
  it("creates with the chosen source5 base and repeats unknown creation exactly", async () => {
    const api = setup({ loseCreate: true });
    api.current.revision.baseSchemaVersion = "5";
    renderWithProviders(
      <BackendChangeProposals
        projectId={changeTestID}
        baseRevisionId={changeTestID}
        baseSchemaVersion="5"
      />,
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Название предложения" }), {
      target: { value: "Desired" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Создать предложение" }));
    fireEvent.click(await screen.findByRole("button", { name: "Повторить создание" }));
    await screen.findByTestId("exact-draft");
    expect(api.creates[1]).toBe(api.creates[0]);
    expect(JSON.parse(api.creates[0]!).baseRevisionId).toBe(changeTestID);
  });
  it("marks unfinished form input dirty until explicitly discarded", () => {
    setup();
    const onDirty = vi.fn();
    renderWithProviders(
      <BackendChangeEditor
        projectId={changeTestID}
        detail={changeTestDetail()}
        onSaved={vi.fn()}
        onDirty={onDirty}
      />,
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
      target: { value: "Not yet queued" },
    });
    expect(onDirty).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByRole("button", { name: "Отменить ввод команды" }));
    expect(onDirty).toHaveBeenLastCalledWith(false);
  });
  it("loads an explicitly selected historical draft without following the head", async () => {
    const api = setup();
    api.advance();
    const latest = api.current;
    latest.history.unshift({
      id: changeNextID,
      parentRevisionId: changeDraftID,
      semanticHash: changeHash,
      author: "user",
      summary: "Advanced",
      createdAt: latest.revision.createdAt,
    });
    renderWithProviders(
      <BackendChangeProposals
        projectId={changeTestID}
        baseRevisionId={changeTestID}
        baseSchemaVersion="6"
      />,
    );
    await screen.findByRole("option", { name: /Desired model/ });
    fireEvent.change(screen.getByRole("combobox", { name: "Полное предложение" }), {
      target: { value: changeTestID },
    });
    await waitFor(() => expect(screen.getByTestId("exact-draft")).toHaveTextContent(changeNextID));
    fireEvent.change(screen.getByRole("combobox", { name: "История черновика" }), {
      target: { value: changeDraftID },
    });
    await waitFor(() => expect(screen.getByTestId("exact-draft")).toHaveTextContent(changeDraftID));
    expect(api.gets.at(-1)).toBe(changeDraftID);
    expect(screen.queryByRole("button", { name: "Добавить команду" })).toBeNull();
  });
});

it("blocks Create dispatch until recovery storage succeeds", async () => {
  const api = setup({ loseCreate: true });
  const originalStorage = sessionStorage;
  const setItem = vi.fn<(key: string, value: string) => void>(() => {
    throw new DOMException("quota", "QuotaExceededError");
  });
  vi.stubGlobal("sessionStorage", {
    getItem: originalStorage.getItem.bind(originalStorage),
    setItem,
    removeItem: originalStorage.removeItem.bind(originalStorage),
    clear: originalStorage.clear.bind(originalStorage),
    key: originalStorage.key.bind(originalStorage),
    get length() {
      return originalStorage.length;
    },
  });
  renderWithProviders(
    <BackendChangeProposals
      projectId={changeTestID}
      baseRevisionId={changeTestID}
      baseSchemaVersion="6"
    />,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Название предложения" }), {
    target: { value: "Must persist" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Создать предложение" }));
  await screen.findByRole("button", { name: "Повторить создание" });
  expect(api.creates).toHaveLength(0);
  setItem.mockImplementation(originalStorage.setItem.bind(originalStorage));
  fireEvent.click(screen.getByRole("button", { name: "Повторить создание" }));
  await waitFor(() => expect(api.creates).toHaveLength(1));
  expect(sessionStorage.length).toBe(1);
});

it("recovers Create from its original source after the project selects a newer base", async () => {
  const api = setup({ loseCreate: true });
  const first = renderWithProviders(
    <BackendChangeProposals
      projectId={changeTestID}
      baseRevisionId={changeTestID}
      baseSchemaVersion="6"
    />,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Название предложения" }), {
    target: { value: "Original base" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Создать предложение" }));
  await screen.findByRole("button", { name: "Повторить создание" });
  first.unmount();
  renderWithProviders(
    <BackendChangeProposals
      projectId={changeTestID}
      baseRevisionId={changeNextID}
      baseSchemaVersion="6"
    />,
  );
  fireEvent.click(await screen.findByRole("button", { name: "Повторить создание" }));
  await screen.findByRole("combobox", { name: "История черновика" });
  expect(api.creates[1]).toBe(api.creates[0]);
  expect(JSON.parse(api.creates[1]!).baseRevisionId).toBe(changeTestID);
});

function strictPanel() {
  return (
    <StrictMode>
      <QueryClientProvider client={makeQueryClient()}>
        <MantineProvider env="test">
          <ModalsProvider>
            <BackendChangeProposals
              projectId={changeTestID}
              baseRevisionId={changeTestID}
              baseSchemaVersion="6"
            />
          </ModalsProvider>
        </MantineProvider>
      </QueryClientProvider>
    </StrictMode>
  );
}
it.each([false, true])("handles Create under root StrictMode; lost response %s", async (lost) => {
  const api = setup({ loseCreate: lost });
  render(strictPanel());
  fireEvent.change(screen.getByRole("textbox", { name: "Название предложения" }), {
    target: { value: "Strict create" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Создать предложение" }));
  if (lost) fireEvent.click(await screen.findByRole("button", { name: "Повторить создание" }));
  await screen.findByRole("combobox", { name: "История черновика" });
  expect(api.creates).toHaveLength(lost ? 2 : 1);
  if (lost) expect(api.creates[1]).toBe(api.creates[0]);
});
function unavailableStorage(method: "getItem" | "setItem" | "removeItem") {
  const original = sessionStorage;
  const denied = vi.fn<(...args: string[]) => never>(() => {
    throw new DOMException("blocked", "SecurityError");
  });
  const storage = {
    getItem: original.getItem.bind(original),
    setItem: original.setItem.bind(original),
    removeItem: original.removeItem.bind(original),
    key: original.key.bind(original),
    clear: original.clear.bind(original),
    get length() {
      return original.length;
    },
  };
  Object.assign(storage, { [method]: denied });
  vi.stubGlobal("sessionStorage", storage);
  return () => Object.assign(storage, { [method]: original[method].bind(original) });
}
it("reports unreadable recovery and blocks mutations until storage reread", async () => {
  const api = setup();
  const allow = unavailableStorage("getItem");
  renderWithProviders(
    <BackendChangeProposals
      projectId={changeTestID}
      baseRevisionId={changeTestID}
      baseSchemaVersion="6"
    />,
  );
  expect(screen.getByRole("button", { name: "Создать предложение" })).toBeDisabled();
  expect(screen.getByText(/Не удалось прочитать восстановление/)).toBeInTheDocument();
  expect(api.creates).toHaveLength(0);
  allow();
  fireEvent.click(screen.getByRole("button", { name: "Перечитать восстановление" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Название предложения" }), {
    target: { value: "Now stored" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Создать предложение" }));
  await screen.findByRole("combobox", { name: "История черновика" });
  expect(api.creates).toHaveLength(1);
});
it("preserves an invalid record until explicit confirmed discard", () => {
  const api = setup();
  const key = changeCreateRecoveryKey(changeTestID);
  sessionStorage.setItem(key, "corrupt");
  renderWithProviders(
    <BackendChangeProposals
      projectId={changeTestID}
      baseRevisionId={changeTestID}
      baseSchemaVersion="6"
    />,
  );
  expect(screen.getByRole("button", { name: "Создать предложение" })).toBeDisabled();
  expect(sessionStorage.getItem(key)).toBe("corrupt");
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: "Удалить локальную запись; это не отменяет принятый сервером запрос",
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Удалить запись после проверки" }));
  expect(sessionStorage.getItem(key)).toBeNull();
  expect(api.creates).toHaveLength(0);
});
it("discovers the previous per-base Create slot without moving the captured base", async () => {
  const api = setup();
  const input = { name: "Old slot", baseRevisionId: changeTestID, idempotencyKey: "old-key" };
  const attempt = makeChangeAttempt("create", input);
  writeChangeRecovery(`backend-change-create:${changeTestID}:${changeTestID}`, attempt);
  renderWithProviders(
    <BackendChangeProposals
      projectId={changeTestID}
      baseRevisionId={changeNextID}
      baseSchemaVersion="6"
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Повторить создание" }));
  await screen.findByRole("combobox", { name: "История черновика" });
  expect(api.creates[0]).toBe(attempt.body);
  expect(sessionStorage.length).toBe(0);
});
it("does not resend a confirmed Create when clearing storage fails", async () => {
  const api = setup();
  const allow = unavailableStorage("removeItem");
  renderWithProviders(
    <BackendChangeProposals
      projectId={changeTestID}
      baseRevisionId={changeTestID}
      baseSchemaVersion="6"
    />,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Название предложения" }), {
    target: { value: "Confirmed" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Создать предложение" }));
  await screen.findByRole("combobox", { name: "История черновика" });
  expect(api.creates).toHaveLength(1);
  expect(screen.queryByRole("button", { name: "Повторить создание" })).toBeNull();
  allow();
  fireEvent.click(screen.getByRole("button", { name: "Удалить подтверждённую запись" }));
  expect(sessionStorage.length).toBe(0);
  expect(api.creates).toHaveLength(1);
});
it("blocks Apply before dispatch when recovery cannot be stored", async () => {
  const api = setup();
  renderWithProviders(
    <BackendChangeEditor projectId={changeTestID} detail={changeTestDetail()} onSaved={vi.fn()} />,
  );
  addRename();
  await preview();
  const allow = unavailableStorage("setItem");
  fireEvent.click(screen.getByRole("button", { name: "Сохранить предложение" }));
  await screen.findByRole("button", { name: "Повторить тот же запрос" });
  expect(api.attempts).toHaveLength(0);
  allow();
  fireEvent.click(screen.getByRole("button", { name: "Повторить тот же запрос" }));
  await waitFor(() => expect(api.attempts).toHaveLength(1));
  expect(JSON.parse(api.attempts[0]!).commands).toEqual(api.previews[0]!.commands);
});
it("blocks Restore before dispatch when recovery cannot be stored", async () => {
  const api = setup();
  api.advance();
  const detail = api.current;
  detail.history = [
    {
      id: changeDraftID,
      parentRevisionId: null,
      semanticHash: changeHash,
      author: "user",
      summary: "Original",
      createdAt: detail.revision.createdAt,
    },
  ];
  renderWithProviders(
    <BackendChangeEditor projectId={changeTestID} detail={detail} onSaved={vi.fn()} />,
  );
  fireEvent.change(screen.getByRole("combobox", { name: "Ревизия для восстановления" }), {
    target: { value: changeDraftID },
  });
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Создать новый черновик с выбранным состоянием" }),
  );
  const allow = unavailableStorage("setItem");
  fireEvent.click(screen.getByRole("button", { name: "Восстановить выбранную ревизию" }));
  await screen.findByRole("button", { name: "Повторить тот же запрос" });
  expect(api.restores).toHaveLength(0);
  allow();
  fireEvent.click(screen.getByRole("button", { name: "Повторить тот же запрос" }));
  await waitFor(() => expect(api.restores).toHaveLength(1));
  expect(JSON.parse(api.restores[0]!)).toMatchObject({
    proposalRevisionId: changeNextID,
    restoreRevisionId: changeDraftID,
    expectedVersion: 2,
  });
});

it("restores commands and requires reconciliation when rereading a conflict", () => {
  const api = setup();
  const pending = makeChangeAttempt("apply", {
    expectedVersion: 1,
    proposalRevisionId: changeDraftID,
    candidateHash: "b".repeat(64),
    idempotencyKey: "conflicted-apply",
    commands: [
      {
        type: "rename",
        commandId: changeNextID,
        recordType: "node",
        id: changeTestID,
        name: "Captured name",
        reason: "Captured local command",
      },
    ],
  });
  writeChangeRecovery(`backend-change-attempt:${changeTestID}:${changeTestID}`, {
    ...pending,
    phase: "conflict",
  });
  const allow = unavailableStorage("getItem");
  renderWithProviders(
    <BackendChangeEditor projectId={changeTestID} detail={changeTestDetail()} onSaved={vi.fn()} />,
  );
  allow();
  fireEvent.click(screen.getByRole("button", { name: "Перечитать восстановление" }));
  expect(screen.queryByRole("button", { name: "Повторить тот же запрос" })).toBeNull();
  expect(screen.getByRole("button", { name: "Загрузить для сверки" })).toBeInTheDocument();
  expect(screen.getByText(/Порядок локальных команд \(1\/100\)/)).toBeInTheDocument();
  expect(api.attempts).toHaveLength(0);
});

it("keeps the selected saved report applicable after Ready and still detects subsequent real edits", async () => {
  const { BackendAnalysisRecoveryProvider } = await import("./backendAnalysisRecovery");
  const { analysisTestDetail, analysisTestManifest } =
    await import("./backendAnalysisTestFixtures");
  setup();
  const existingFetch = globalThis.fetch;
  const detail = changeTestDetail();
  detail.proposal.version = 3;
  const analysis = analysisTestDetail(),
    manifest = analysisTestManifest();
  const writes: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost"),
        path = url.pathname;
      if (path.endsWith("/lifecycle")) {
        const body = JSON.parse(String(init?.body));
        writes.push(body);
        return response({
          proposal: {
            ...detail.proposal,
            version: 4,
            status: "ready",
            readyReference: { report: body.report, acknowledgedGapIds: body.acknowledgedGapIds },
          },
          revision: detail.revision,
          semanticHash: detail.revision.semanticHash,
          changes: [],
        });
      }
      if (path.endsWith("/analyses")) return response({ items: [analysis.job], nextCursor: "" });
      if (path.endsWith(`/analyses/${analysis.job.id}`)) return response(analysis);
      if (path.endsWith("/results"))
        return response({
          manifest,
          section: url.searchParams.get("section"),
          items: [],
          nextCursor: "",
        });
      if (path.endsWith("/revisions")) return response({ items: [], nextCursor: "" });
      return existingFetch(input, init);
    }),
  );
  const saved = vi.fn();
  renderWithProviders(
    <BackendAnalysisRecoveryProvider projectId={changeTestID}>
      <BackendChangeEditor projectId={changeTestID} detail={detail} onSaved={saved} />
    </BackendAnalysisRecoveryProvider>,
  );
  await screen.findByRole("option", { name: /impact · completed/ });
  fireEvent.change(screen.getByLabelText("Задание анализа"), {
    target: { value: analysis.job.id },
  });
  fireEvent.change(await screen.findByLabelText("Неизменяемая версия отчёта"), {
    target: { value: "2" },
  });
  const ready = await screen.findByRole("button", { name: "Отметить черновик готовым" });
  await waitFor(() => expect(ready).toBeEnabled());
  fireEvent.click(ready);
  await waitFor(() =>
    expect(saved).toHaveBeenCalledWith(
      expect.objectContaining({
        proposal: expect.objectContaining({ version: 4, status: "ready" }),
        revision: expect.objectContaining({
          id: detail.revision.id,
          semanticHash: detail.revision.semanticHash,
        }),
      }),
    ),
  );
  expect(writes).toHaveLength(1);
  expect(writes[0]).toMatchObject({ expectedVersion: 3, proposalRevisionId: detail.revision.id });
  expect(await screen.findByText("Черновик уже готов.")).toBeInTheDocument();
  expect(screen.queryByText(/Есть несохранённые изменения\. Сначала/)).toBeNull();
  expect(screen.getByLabelText("Неизменяемая версия отчёта")).toHaveValue("2");
  expect(screen.getByTestId("exact-draft")).toHaveTextContent(detail.revision.id);
  fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
    target: { value: "Actual edit after ready" },
  });
  expect(await screen.findByText(/Есть несохранённые изменения\. Сначала/)).toBeInTheDocument();
  expect(screen.queryByText("Черновик уже готов.")).toBeNull();
});

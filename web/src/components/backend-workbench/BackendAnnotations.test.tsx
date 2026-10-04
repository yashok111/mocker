import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendAnnotations } from "./BackendAnnotations";

const projectId = "0197aaf9-5555-7000-8000-000000000001";
const revisionId = "0197aaf9-5555-7000-8000-000000000002";
const nodeId = "0197aaf9-5555-7000-8000-000000000003";
const noteId = "0197aaf9-5555-7000-8000-000000000004";
const project = {
  id: projectId,
  name: "Orders",
  version: 7,
  currentRevisionId: revisionId,
  repositories: [],
  capabilities: [],
  createdAt: "2026-10-03T00:00:00Z",
  updatedAt: "2026-10-03T00:00:00Z",
};
const selection = { recordType: "node" as const, id: nodeId, revisionId };
const note = {
  id: noteId,
  target: { recordType: "node", id: nodeId },
  body: "<img src=x onerror=alert(1)>",
  author: "reviewer",
  createdAt: project.createdAt,
  updatedAt: project.updatedAt,
  targetStatus: "orphaned",
};
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  sessionStorage.clear();
});
function serve(
  command: (input: RequestInit | undefined) => Response | Promise<Response>,
  items: unknown[] = [],
) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input, init) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path.endsWith("/commands")) return command(init);
      if (path.endsWith("/annotations"))
        return json(200, { projectId, projectVersion: 7, items, nextCursor: "" });
      if (path === `/api/backend-projects/${projectId}`) return json(200, project);
      return json(500, { error: { code: "unexpected", message: path } });
    }),
  );
}
it("holds the exact metadata attempt while its response is uncertain", async () => {
  const attempts: string[] = [];
  serve((init) => {
    attempts.push(String(init?.body));
    if (attempts.length === 1) throw new TypeError("lost response");
    return json(200, { ...project, version: 8 });
  });
  const onNavigate = vi.fn();
  renderWithProviders(
    <BackendAnnotations projectId={projectId} selection={selection} onNavigate={onNavigate} />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Добавить заметку" }));
  await userEvent.type(screen.getByRole("textbox", { name: "Текст заметки" }), " Boundary ");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
  await userEvent.click(await screen.findByRole("button", { name: "Повторить тот же запрос" }));
  await waitFor(() => expect(attempts).toHaveLength(2));
  expect(attempts[1]).toBe(attempts[0]);
  expect(JSON.parse(attempts[0]!)).toMatchObject({
    expectedVersion: 7,
    commands: [
      { type: "create_annotation", body: " Boundary ", target: { recordType: "node", id: nodeId } },
    ],
  });
  expect(onNavigate).not.toHaveBeenCalled();
});
it("edits orphan text without changing its binding and renders only plain text", async () => {
  const attempts: string[] = [];
  serve(
    (init) => {
      attempts.push(String(init?.body));
      return json(200, { ...project, version: 8 });
    },
    [note],
  );
  const onNavigate = vi.fn();
  renderWithProviders(<BackendAnnotations projectId={projectId} onNavigate={onNavigate} />);
  expect(await screen.findByText(note.body)).toBeInTheDocument();
  expect(document.querySelector("img")).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "Редактировать заметку" }));
  const body = screen.getByRole("textbox", { name: "Текст заметки" });
  await userEvent.clear(body);
  await userEvent.type(body, "Still useful");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
  await waitFor(() => expect(attempts).toHaveLength(1));
  expect(JSON.parse(attempts[0]!).commands).toEqual([
    { type: "update_annotation", annotationId: noteId, target: note.target, body: "Still useful" },
  ]);
});
it("requires explicit reload and review after CAS conflict, preserving form text", async () => {
  const attempts: string[] = [];
  serve((init) => {
    attempts.push(String(init?.body));
    return json(409, {
      error: {
        code: "backend_version_conflict",
        message: "Changed",
        currentVersion: 8,
        retryable: false,
      },
    });
  });
  renderWithProviders(
    <BackendAnnotations projectId={projectId} selection={selection} onNavigate={vi.fn()} />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Добавить заметку" }));
  await userEvent.type(screen.getByRole("textbox", { name: "Текст заметки" }), "Unsaved note");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
  const reload = await screen.findByRole("button", { name: "Перечитать проект и заметки" });
  expect(screen.getByRole("button", { name: "Сохранить заметку" })).toBeDisabled();
  await userEvent.click(reload);
  expect(screen.getByRole("textbox", { name: "Текст заметки" })).toHaveValue("Unsaved note");
  expect(attempts).toHaveLength(1);
});
it("creates a historical binding and confirms removal with keyboard accessible controls", async () => {
  const attempts: string[] = [];
  serve(
    (init) => {
      attempts.push(String(init?.body));
      return json(200, { ...project, version: 8 });
    },
    [note],
  );
  renderWithProviders(
    <BackendAnnotations projectId={projectId} selection={selection} onNavigate={vi.fn()} />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Добавить заметку" }));
  expect(screen.getByRole("textbox", { name: "Текст заметки" })).toHaveFocus();
  await userEvent.type(screen.getByRole("textbox", { name: "Текст заметки" }), "Historic");
  await userEvent.click(screen.getByRole("checkbox", { name: "Привязать к выбранной ревизии" }));
  await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
  await waitFor(() => expect(attempts).toHaveLength(1));
  expect(JSON.parse(attempts[0]!).commands[0].target.revisionId).toBe(revisionId);
  await userEvent.click(await screen.findByRole("button", { name: "Удалить заметку" }));
  const dialog = screen.getByRole("dialog", { name: "Удалить заметку?" });
  await userEvent.click(within(dialog).getByRole("button", { name: "Удалить" }));
  await waitFor(() => expect(attempts).toHaveLength(2));
  expect(JSON.parse(attempts[1]!).commands).toEqual([
    { type: "remove_annotation", annotationId: noteId },
  ]);
});

it("keeps a lost response available after remount without sending automatically", async () => {
  const attempts: string[] = [];
  serve((init) => {
    attempts.push(String(init?.body));
    throw new TypeError("lost response");
  });
  const first = renderWithProviders(
    <BackendAnnotations projectId={projectId} selection={selection} onNavigate={vi.fn()} />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Добавить заметку" }));
  await userEvent.type(screen.getByRole("textbox", { name: "Текст заметки" }), "Retain request");
  await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
  await screen.findByRole("button", { name: "Повторить тот же запрос" });
  first.unmount();
  renderWithProviders(
    <BackendAnnotations projectId={projectId} selection={selection} onNavigate={vi.fn()} />,
  );
  await screen.findByRole("button", { name: "Повторить тот же запрос" });
  expect(attempts).toHaveLength(1);
  await userEvent.click(screen.getByRole("button", { name: "Повторить тот же запрос" }));
  await waitFor(() => expect(attempts).toHaveLength(2));
  expect(attempts[0]).toBe(attempts[1]);
});
it("retains an invalid rebind draft for correction and rejects UTF8 overflow", async () => {
  const attempts: string[] = [];
  serve(
    (init) => {
      attempts.push(String(init?.body));
      return json(404, {
        error: { code: "backend_not_found", message: "Not found", retryable: false },
      });
    },
    [note],
  );
  renderWithProviders(<BackendAnnotations projectId={projectId} onNavigate={vi.fn()} />);
  await userEvent.click(await screen.findByRole("button", { name: "Редактировать заметку" }));
  const id = screen.getByRole("textbox", { name: "ID цели" });
  await userEvent.clear(id);
  await userEvent.type(id, revisionId);
  await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
  expect(
    await screen.findByText("Заметка или выбранная цель не найдена. Проверьте объект и ревизию."),
  ).toBeInTheDocument();
  expect(screen.getByRole("textbox", { name: "Текст заметки" })).toHaveValue(note.body);
  expect(JSON.parse(attempts[0]!).commands[0].target.id).toBe(revisionId);
  const body = screen.getByRole("textbox", { name: "Текст заметки" });
  await userEvent.clear(body);
  await userEvent.click(body);
  await userEvent.paste("界".repeat(5462));
  expect(screen.getByRole("button", { name: "Сохранить заметку" })).toBeDisabled();
});
it("navigates an exact historical target and blocks an unsafe project version", async () => {
  const target = { ...note.target, revisionId };
  const onNavigate = vi.fn();
  serve(() => json(200, project), [{ ...note, target, targetStatus: "historical" }]);
  const rendered = renderWithProviders(
    <BackendAnnotations projectId={projectId} onNavigate={onNavigate} />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Открыть цель" }));
  expect(onNavigate).toHaveBeenCalledWith(target);
  rendered.unmount();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          `{"projectId":"${projectId}","projectVersion":9007199254740993,"items":[],"nextCursor":""}`,
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
  renderWithProviders(
    <BackendAnnotations projectId={projectId} selection={selection} onNavigate={onNavigate} />,
  );
  await screen.findAllByRole("alert");
  expect(screen.getByRole("button", { name: "Добавить заметку" })).toBeDisabled();
});

it("pins the version when removal is confirmed instead of adopting a background refresh", async () => {
  const attempts: string[] = [];
  serve(
    (init) => {
      attempts.push(String(init?.body));
      return json(200, { ...project, version: 9 });
    },
    [note],
  );
  const rendered = renderWithProviders(
    <BackendAnnotations projectId={projectId} onNavigate={vi.fn()} />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Удалить заметку" }));
  rendered.queryClient.setQueryData(["backend-annotations", projectId, {}, ""], {
    projectId,
    projectVersion: 8,
    items: [{ ...note, body: "other editor" }],
    nextCursor: "",
  });
  await userEvent.click(
    within(screen.getByRole("dialog", { name: "Удалить заметку?" })).getByRole("button", {
      name: "Удалить",
    }),
  );
  await waitFor(() => expect(attempts).toHaveLength(1));
  expect(JSON.parse(attempts[0]!).expectedVersion).toBe(7);
});
it("restarts cursor pagination after metadata changes while retaining an edit draft", async () => {
  let firstPages = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname.endsWith("/annotations")) {
        if (url.searchParams.has("cursor"))
          return json(409, {
            error: {
              code: "backend_annotation_page_conflict",
              message: "Changed",
              currentVersion: 8,
              retryable: false,
            },
          });
        firstPages++;
        return json(200, {
          projectId,
          projectVersion: firstPages === 1 ? 7 : 8,
          items: [note],
          nextCursor: "next",
        });
      }
      return json(200, project);
    }),
  );
  renderWithProviders(<BackendAnnotations projectId={projectId} onNavigate={vi.fn()} />);
  await userEvent.click(await screen.findByRole("button", { name: "Следующие заметки" }));
  expect(
    await screen.findByText("Список заметок изменился. Просмотр начат с первой страницы."),
  ).toBeInTheDocument();
  await waitFor(() => expect(firstPages).toBeGreaterThan(1));
  expect(screen.getByRole("button", { name: "Предыдущие заметки" })).toBeDisabled();
});

it("defaults a historical selection to its exact revision and uses intersecting filters", async () => {
  const requests: URL[] = [];
  const historic = "0197aaf9-5555-7000-8000-000000000005";
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => {
      const url = new URL(String(input), "http://localhost");
      requests.push(url);
      if (url.pathname.endsWith("/annotations"))
        return json(200, { projectId, projectVersion: 7, items: [], nextCursor: "" });
      return json(200, project);
    }),
  );
  renderWithProviders(
    <BackendAnnotations
      projectId={projectId}
      selection={{ ...selection, revisionId: historic }}
      onNavigate={vi.fn()}
    />,
  );
  await userEvent.selectOptions(
    screen.getByRole("combobox", { name: "Показать заметки" }),
    "selected",
  );
  await userEvent.click(
    screen.getByRole("checkbox", { name: "Только привязанные к выбранной ревизии" }),
  );
  await waitFor(() =>
    expect(
      requests.some(
        (url) =>
          url.searchParams.get("targetId") === nodeId &&
          url.searchParams.get("recordType") === "node" &&
          url.searchParams.get("revisionId") === historic,
      ),
    ).toBe(true),
  );
  await userEvent.click(await screen.findByRole("button", { name: "Добавить заметку" }));
  expect(screen.getByRole("checkbox", { name: "Привязать к выбранной ревизии" })).toBeChecked();
  expect(screen.getByRole("textbox", { name: "ID ревизии" })).toHaveValue(historic);
  await userEvent.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Добавить заметку" })).toHaveFocus(),
  );
});

it.each(["create_annotation", "update_annotation"] as const)(
  "reopens the retained %s draft after closing and reconciling a conflict",
  async (type) => {
    const attempts: string[] = [];
    const boundNote = {
      ...note,
      target: { ...note.target, revisionId },
      targetStatus: "historical",
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input, init) => {
        const path = new URL(String(input), "http://localhost").pathname;
        if (path.endsWith("/commands")) {
          attempts.push(String(init?.body));
          if (attempts.length === 1)
            return json(409, {
              error: {
                code: "backend_version_conflict",
                message: "Changed",
                currentVersion: 8,
                retryable: false,
              },
            });
          return json(200, { ...project, version: 9 });
        }
        if (path.endsWith("/annotations"))
          return json(200, {
            projectId,
            projectVersion: attempts.length ? 8 : 7,
            items: type === "update_annotation" ? [boundNote] : [],
            nextCursor: "",
          });
        return json(200, { ...project, version: attempts.length ? 8 : 7 });
      }),
    );
    renderWithProviders(
      <BackendAnnotations projectId={projectId} selection={selection} onNavigate={vi.fn()} />,
    );
    await userEvent.click(
      await screen.findByRole("button", {
        name: type === "create_annotation" ? "Добавить заметку" : "Редактировать заметку",
      }),
    );
    const body = screen.getByRole("textbox", { name: "Текст заметки" });
    await userEvent.clear(body);
    await userEvent.type(body, "Original unsaved draft");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
    await screen.findByRole("button", { name: "Перечитать проект и заметки" });
    await userEvent.click(screen.getByRole("button", { name: "Закрыть" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Перечитать проект и заметки" }));
    await userEvent.click(
      await screen.findByRole("button", { name: "Продолжить после сравнения" }),
    );
    const restored = await screen.findByRole("textbox", { name: "Текст заметки" });
    expect(restored).toHaveValue("Original unsaved draft");
    expect(restored).toBeEnabled();
    await userEvent.type(restored, " corrected");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
    await waitFor(() => expect(attempts).toHaveLength(2));
    const first = JSON.parse(attempts[0]!);
    const next = JSON.parse(attempts[1]!);
    expect(next.expectedVersion).toBe(8);
    expect(next.idempotencyKey).not.toBe(first.idempotencyKey);
    expect(next.commands).toEqual([
      { ...first.commands[0], body: "Original unsaved draft corrected" },
    ]);
  },
);

it.each([
  { type: "create_annotation", status: 400, code: "backend_invalid" },
  { type: "update_annotation", status: 404, code: "backend_not_found" },
  { type: "create_annotation", status: 413, code: "backend_too_large" },
] as const)(
  "restores an editable $type draft after remount and a definitive $status retry response",
  async ({ type, status, code }) => {
    const attempts: string[] = [];
    serve(
      (init) => {
        attempts.push(String(init?.body));
        if (attempts.length === 1) throw new TypeError("lost response");
        if (attempts.length === 2)
          return json(status, { error: { code, message: "Rejected", retryable: false } });
        return json(200, { ...project, version: 8 });
      },
      type === "update_annotation" ? [note] : [],
    );
    const firstMount = renderWithProviders(
      <BackendAnnotations projectId={projectId} selection={selection} onNavigate={vi.fn()} />,
    );
    await userEvent.click(
      await screen.findByRole("button", {
        name: type === "create_annotation" ? "Добавить заметку" : "Редактировать заметку",
      }),
    );
    const body = screen.getByRole("textbox", { name: "Текст заметки" });
    await userEvent.clear(body);
    await userEvent.type(body, "Draft with a rejected target");
    const target = screen.getByRole("textbox", { name: "ID цели" });
    await userEvent.clear(target);
    await userEvent.type(target, revisionId);
    await userEvent.click(screen.getByRole("checkbox", { name: "Привязать к выбранной ревизии" }));
    await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
    await screen.findByRole("button", { name: "Повторить тот же запрос" });
    firstMount.unmount();
    renderWithProviders(<BackendAnnotations projectId={projectId} onNavigate={vi.fn()} />);
    await userEvent.click(await screen.findByRole("button", { name: "Повторить тот же запрос" }));
    await waitFor(() => expect(attempts).toHaveLength(2));
    expect(attempts[1]).toBe(attempts[0]);
    const restored = await screen.findByRole("textbox", { name: "Текст заметки" });
    expect(restored).toBeEnabled();
    expect(restored).toHaveValue("Draft with a rejected target");
    expect(screen.getByRole("textbox", { name: "ID цели" })).toHaveValue(revisionId);
    expect(screen.getByRole("textbox", { name: "ID ревизии" })).toHaveValue(revisionId);
    expect(within(screen.getByRole("dialog")).getByRole("alert")).toBeInTheDocument();
    expect(sessionStorage.getItem(`backend-annotation-attempt:${projectId}`)).toBeNull();
    await userEvent.type(restored, " corrected");
    await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
    await waitFor(() => expect(attempts).toHaveLength(3));
    const first = JSON.parse(attempts[0]!);
    const corrected = JSON.parse(attempts[2]!);
    expect(corrected.expectedVersion).toBe(first.expectedVersion);
    expect(corrected.idempotencyKey).not.toBe(first.idempotencyKey);
    expect(corrected.commands).toEqual([
      { ...first.commands[0], body: "Draft with a rejected target corrected" },
    ]);
  },
);

it("focuses a surviving control after the deleted note disappears from refreshed results", async () => {
  let deleted = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path.endsWith("/commands")) {
        deleted = true;
        return json(200, { ...project, version: 8 });
      }
      if (path.endsWith("/annotations"))
        return json(200, {
          projectId,
          projectVersion: deleted ? 8 : 7,
          items: deleted ? [] : [note],
          nextCursor: "",
        });
      return json(200, { ...project, version: deleted ? 8 : 7 });
    }),
  );
  renderWithProviders(<BackendAnnotations projectId={projectId} onNavigate={vi.fn()} />);
  const trigger = await screen.findByRole("button", { name: "Удалить заметку" });
  await userEvent.click(trigger);
  await userEvent.click(
    within(screen.getByRole("dialog", { name: "Удалить заметку?" })).getByRole("button", {
      name: "Удалить",
    }),
  );
  await screen.findByText("Заметок пока нет");
  expect(trigger).not.toBeInTheDocument();
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Добавить заметку" })).toHaveFocus(),
  );
  await userEvent.keyboard("{Enter}");
  expect(await screen.findByRole("textbox", { name: "Текст заметки" })).toHaveFocus();
});

it("returns focus to the retained note action when deletion is cancelled", async () => {
  const mutate = vi.fn(() => json(200, project));
  serve(mutate, [note]);
  renderWithProviders(<BackendAnnotations projectId={projectId} onNavigate={vi.fn()} />);
  const trigger = await screen.findByRole("button", { name: "Удалить заметку" });
  await userEvent.click(trigger);
  await userEvent.keyboard("{Escape}");
  await waitFor(() => expect(trigger).toHaveFocus());
  expect(screen.getByText(note.body)).toBeInTheDocument();
  expect(mutate).not.toHaveBeenCalled();
});

it("retains a known conflict after remount without presenting it as an unknown write", async () => {
  const attempts: string[] = [];
  serve((init) => {
    attempts.push(String(init?.body));
    return json(409, {
      error: {
        code: "backend_version_conflict",
        message: "Changed",
        currentVersion: 8,
        retryable: false,
      },
    });
  });
  const first = renderWithProviders(
    <BackendAnnotations projectId={projectId} selection={selection} onNavigate={vi.fn()} />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Добавить заметку" }));
  await userEvent.type(
    screen.getByRole("textbox", { name: "Текст заметки" }),
    "Known conflict draft",
  );
  await userEvent.click(screen.getByRole("button", { name: "Сохранить заметку" }));
  await screen.findByRole("button", { name: "Перечитать проект и заметки" });
  first.unmount();
  renderWithProviders(<BackendAnnotations projectId={projectId} onNavigate={vi.fn()} />);
  expect(
    await screen.findByRole("button", { name: "Перечитать проект и заметки" }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Повторить тот же запрос" })).not.toBeInTheDocument();
  expect(attempts).toHaveLength(1);
});

it("reconciles the pending deletion identity after closing an unrelated create draft", async () => {
  const attempts: string[] = [];
  const rereadIDs: string[] = [];
  const currentBody = "Changed by another reviewer before removal";
  let deleted = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input, init) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname.endsWith("/commands")) {
        attempts.push(String(init?.body));
        if (attempts.length === 1)
          return json(409, {
            error: {
              code: "backend_version_conflict",
              message: "Changed",
              currentVersion: 8,
              retryable: false,
            },
          });
        deleted = true;
        return json(200, { ...project, version: 9 });
      }
      if (url.pathname.endsWith("/annotations")) {
        const annotationID = url.searchParams.get("annotationId");
        if (annotationID) rereadIDs.push(annotationID);
        return json(200, {
          projectId,
          projectVersion: attempts.length ? 8 : 7,
          items:
            deleted || (annotationID && annotationID !== noteId)
              ? []
              : [{ ...note, body: attempts.length ? currentBody : note.body }],
          nextCursor: "",
        });
      }
      return json(200, { ...project, version: attempts.length ? 8 : 7 });
    }),
  );
  renderWithProviders(
    <BackendAnnotations projectId={projectId} selection={selection} onNavigate={vi.fn()} />,
  );
  await userEvent.click(await screen.findByRole("button", { name: "Добавить заметку" }));
  await userEvent.type(
    screen.getByRole("textbox", { name: "Текст заметки" }),
    "Unrelated unsaved create draft",
  );
  await userEvent.click(screen.getByRole("button", { name: "Закрыть" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  await userEvent.click(screen.getByRole("button", { name: "Удалить заметку" }));
  await userEvent.click(
    within(screen.getByRole("dialog", { name: "Удалить заметку?" })).getByRole("button", {
      name: "Удалить",
    }),
  );
  await userEvent.click(await screen.findByRole("button", { name: "Перечитать проект и заметки" }));
  await waitFor(() => expect(rereadIDs).toEqual([noteId]));
  expect(await screen.findByText(`Сохранённый текст: ${currentBody}`)).toBeInTheDocument();
  expect(
    screen.queryByText("Заметка отсутствует в перечитанном состоянии."),
  ).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Продолжить после сравнения" }));
  expect(screen.queryByRole("textbox", { name: "Текст заметки" })).not.toBeInTheDocument();
  await userEvent.click(
    within(screen.getByRole("dialog", { name: "Удалить заметку?" })).getByRole("button", {
      name: "Удалить",
    }),
  );
  await waitFor(() => expect(attempts).toHaveLength(2));
  const first = JSON.parse(attempts[0]!);
  const reconciled = JSON.parse(attempts[1]!);
  expect(first.commands).toEqual([{ type: "remove_annotation", annotationId: noteId }]);
  expect(reconciled.commands).toEqual(first.commands);
  expect(reconciled.expectedVersion).toBe(8);
  expect(reconciled.idempotencyKey).not.toBe(first.idempotencyKey);
  expect(await screen.findByText("Заметок пока нет")).toBeInTheDocument();
});

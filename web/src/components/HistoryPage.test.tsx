import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HistoryPage } from "./HistoryPage";
import { makeQueryClient, renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import { getListCheckpointsQueryKey } from "@/api/generated/checkpoints/checkpoints.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import { workspaceFixture } from "@/test/fixtures";
import type { CheckpointSummaryView } from "@/api/generated/schemas";

// No shared fixture exists for CheckpointSummaryView (new in this slice, and
// test/fixtures.ts belongs to a different file-ownership lane — the P2c
// context's §F) — this tiny builder is local on purpose, the same way
// ScenariosPage.test.tsx keeps its own scenario fixtures local rather than
// reaching into a shared file it does not own.
function checkpointFixture(overrides: Partial<CheckpointSummaryView> = {}): CheckpointSummaryView {
  return {
    id: 1,
    kind: "manual",
    label: "перед деплоем",
    createdAt: 1_700_000_000,
    createdBy: 1,
    hasData: true,
    ...overrides,
  };
}

const WS = 7;
const LIST = `GET /api/workspaces/${WS}/checkpoints`;
const CREATE = `POST /api/workspaces/${WS}/checkpoints`;
const WORKSPACE = `GET /api/workspaces/${WS}`;
const ROLLBACK = `POST /api/workspaces/${WS}/rollback/1`;
const RESET = `POST /api/workspaces/${WS}/reset-overrides`;
const RESET_DATA = `POST /api/workspaces/${WS}/reset-data`;
const DELETE_CP = `DELETE /api/workspaces/${WS}/checkpoints/1`;

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("HistoryPage", () => {
  it("renders its outer marker and says it is loading before the list answers", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<HistoryPage id={WS} />);

    expect(await screen.findByTestId("history-page")).toBeInTheDocument();
    expect(screen.getByText("Загрузка…")).toBeInTheDocument();
  });

  it("offers a retry when the list fails, in Russian, without dropping the outer marker", async () => {
    let calls = 0;
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => {
        calls += 1;
        return calls === 1
          ? json(500, { error: { code: "internal", message: "db is down" } })
          : json(200, { checkpoints: [] });
      },
    });
    renderInRouter(<HistoryPage id={WS} />);

    // The outer marker survives the error branch — it sits outside the
    // four-state switch, not inside the success branch alone (§I / obs 17).
    expect(await screen.findByTestId("history-page")).toBeInTheDocument();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Ошибка на сервере. Попробуйте ещё раз");
    expect(alert).not.toHaveTextContent("db is down");

    await userEvent.click(screen.getByTestId("history-retry"));
    expect(await screen.findByTestId("history-empty")).toBeInTheDocument();
  });

  it("explains that there is nothing yet when the list is empty", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    expect(await screen.findByTestId("history-empty")).toHaveTextContent("Чекпойнтов пока нет");
  });

  it("lists kind, label, date and the creating user for each row", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () =>
        json(200, {
          checkpoints: [
            checkpointFixture({ id: 1, kind: "manual", label: "релиз 1", createdBy: 3 }),
            checkpointFixture({
              id: 2,
              kind: "pre-destructive",
              label: "перед откатом к точке 1",
              createdBy: null,
            }),
            // "auto" (P2d's debounce trigger, SIG-AUTO) is the third legal
            // kind the column has always accepted, and this is the first
            // slice that ever writes it — a row of this kind now reaches
            // this screen and needs a Russian word, not the bare enum.
            checkpointFixture({ id: 3, kind: "auto", label: "правка операции", createdBy: 3 }),
          ],
        }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    const rows = await screen.findAllByTestId("checkpoint-row");
    expect(rows).toHaveLength(3);
    const manualRow = rows.find((r) => r.textContent?.includes("релиз 1"));
    if (!manualRow) throw new Error("manual row not found");
    expect(within(manualRow).getByTestId("checkpoint-kind")).toHaveTextContent("ручной");
    expect(manualRow).toHaveTextContent("пользователь #3");

    const preRow = rows.find((r) => r.textContent?.includes("перед откатом к точке 1"));
    if (!preRow) throw new Error("pre-destructive row not found");
    expect(within(preRow).getByTestId("checkpoint-kind")).toHaveTextContent("перед действием");
    // createdBy: null renders nothing extra rather than "пользователь #null".
    expect(preRow).not.toHaveTextContent("пользователь #null");

    const autoRow = rows.find((r) => r.textContent?.includes("правка операции"));
    if (!autoRow) throw new Error("auto row not found");
    const autoKind = within(autoRow).getByTestId("checkpoint-kind");
    expect(autoKind).toHaveTextContent("авто");
    // The raw enum string must not leak into the badge alongside the label.
    expect(autoKind).not.toHaveTextContent("auto");
  });

  it("refuses an empty label without calling the server", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await screen.findByTestId("checkpoint-create-form");
    await userEvent.click(screen.getByTestId("checkpoint-create-submit"));

    expect(await screen.findByText("Укажите метку точки")).toBeInTheDocument();
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(0);
  });

  it("refuses a label over 200 runes, counted by code point, without calling the server", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await screen.findByTestId("checkpoint-create-form");
    // C14 / obs 18(c): Cyrillic, because with plain ASCII a byte cap and a
    // rune cap would agree and this test would prove nothing about which one
    // the client actually applies.
    const tooLong = "п".repeat(201);
    await userEvent.type(screen.getByTestId("checkpoint-create-label"), tooLong);
    await userEvent.click(screen.getByTestId("checkpoint-create-submit"));

    expect(await screen.findByText("Не длиннее 200 символов")).toBeInTheDocument();
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(0);
  });

  it("creates a checkpoint and invalidates only the checkpoint list, not the workspace (C12)", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [] }),
      [CREATE]: () => json(201, checkpointFixture({ id: 9, label: "перед экспериментом" })),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<HistoryPage id={WS} />, { queryClient });

    await screen.findByTestId("checkpoint-create-form");
    await userEvent.type(screen.getByTestId("checkpoint-create-label"), "перед экспериментом");
    await userEvent.click(screen.getByTestId("checkpoint-create-submit"));

    expect(await screen.findByTestId("checkpoint-created")).toHaveTextContent(
      "перед экспериментом",
    );
    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(1);
    expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({ label: "перед экспериментом" });

    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListCheckpointsQueryKey(WS));
      // C12: a manual checkpoint never bumps revision — nothing served
      // changed, so the workspace query has nothing stale to invalidate.
      expect(keys).not.toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("does not show the scenario warning on the create form when no scenario is active", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", scenarioId: null })),
      [LIST]: () => json(200, { checkpoints: [] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await screen.findByTestId("checkpoint-create-form");
    expect(screen.queryByTestId("checkpoint-create-scenario-warning")).not.toBeInTheDocument();
  });

  it("warns on the create form, before any click, that the snapshot is workspace-layer only while a scenario is active (C8)", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", scenarioId: 4 })),
      [LIST]: () => json(200, { checkpoints: [] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    const warning = await screen.findByTestId("checkpoint-create-scenario-warning");
    expect(warning).toHaveTextContent("только слой воркспейса");
  });

  it("asks before resetting, naming custom endpoints and the auth preset, and does not call the server on cancel", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("reset-overrides-button"));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("свои эндпоинт");
    expect(dialog).toHaveTextContent("пресет авторизации");
    expect(dialog).toHaveTextContent("перестанет логиниться");
    expect(screen.queryByTestId("reset-scenario-warning")).not.toBeInTheDocument();

    await userEvent.click(within(dialog).getByText("Отмена"));
    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(0);
    });
  });

  it("adds the masking warning to the reset confirmation while a scenario is active (C8)", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", scenarioId: 4 })),
      [LIST]: () => json(200, { checkpoints: [] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("reset-overrides-button"));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByTestId("reset-scenario-warning")).toHaveTextContent("замаскирована");
  });

  it("resets on confirmation, invalidates the checkpoint list and the workspace, and reports changed:true", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [] }),
      [RESET]: () => json(200, { revision: 5, scenarioActive: false, changed: true }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<HistoryPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("reset-overrides-button"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("reset-confirm-submit"));

    expect(await screen.findByTestId("reset-result")).toHaveTextContent("Сброшено");
    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
    });
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListCheckpointsQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("reports changed:false as a no-op rather than claiming something was reset (C9)", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [] }),
      [RESET]: () => json(200, { revision: 5, scenarioActive: false, changed: false }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("reset-overrides-button"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("reset-confirm-submit"));

    expect(await screen.findByTestId("reset-result")).toHaveTextContent("нечего");
  });

  it("asks before rolling back, naming basePath and the signing key, and does not call the server on cancel", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("точка A");
    expect(dialog).toHaveTextContent("basePath");
    expect(dialog).toHaveTextContent("signingKey");
    expect(screen.queryByTestId("rollback-scenario-warning")).not.toBeInTheDocument();

    await userEvent.click(within(dialog).getByText("Отмена"));
    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(0);
    });
  });

  // Clause 34a, D14.3: a rollback now CREATES resource configuration its own
  // undo point cannot remove, so the two paragraphs below say exactly what
  // is and is not restored, and the undo sentence stops promising the
  // whole action is reversible. Asserted by CONTENT, not presence.
  it("says on the rollback confirmation what happens to resource configuration and data (P3b, D9)", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByTestId("rollback-resources-warning")).toHaveTextContent(
      "Откат всегда возвращает КОНФИГУРАЦИЮ ресурсов, записанную в этой точке: какие семейства " +
        "подтверждены и как они настроены. С флажком «вернуть и данные ресурсов» он восстанавливает " +
        "и сами записи из этой точки — семейство, отклонённое после неё, вернётся подтверждённым и " +
        "заполненным. Без флажка записи он не трогает — ни возвращает, ни удаляет: подтверждённое " +
        "после точки семейство останется подтверждённым, а отклонённое после неё вернётся " +
        "подтверждённым, но пустым.",
    );
  });

  it("no longer promises the rollback confirmation is fully undoable, now that it can create a resource confirm nothing removes (P3b, D14.3)", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByTestId("rollback-undo-note")).toHaveTextContent(
      "Перед откатом сохраняется точка текущего состояния — настройки, правки, endpoint'ы и записи " +
        "ресурсов можно вернуть, откатившись на неё с тем же флажком. Ресурс, который этот откат " +
        "сконфигурировал заново, останется подтверждённым: убрать его можно только отклонением.",
    );
  });

  it("adds the masking warning to the rollback confirmation while a scenario is active (C8)", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", scenarioId: 4 })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByTestId("rollback-scenario-warning")).toHaveTextContent(
      "замаскирована",
    );
  });

  it("rolls back on confirmation and invalidates the checkpoint list and the workspace", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
      [ROLLBACK]: () => json(200, { revision: 8, scenarioActive: false }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<HistoryPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("checkpoint-rollback-confirm"));

    await waitFor(() => {
      const rollbacks = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === `/api/workspaces/${WS}/rollback/1` && init?.method === "POST",
      );
      expect(rollbacks).toHaveLength(1);
      // P3d/D8: unchecked now sends a body rather than none — { restoreData:
      // false } — inverted from the pre-P3d assertion that the body was
      // undefined, since RollbackModalBody always calls onSubmit with an
      // explicit restoreData.
      expect(JSON.parse(String(rollbacks[0]?.[1]?.body))).toEqual({ restoreData: false });
    });
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListCheckpointsQueryKey(WS));
      expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("names the checkpoint when a rollback fails", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
      [ROLLBACK]: () => json(409, { error: { code: "conflict", message: "concurrent edit" } }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("checkpoint-rollback-confirm"));

    expect(await screen.findByRole("alert")).toHaveTextContent("«точка A»");
  });

  // D11 property 8, observed against the real Mantine modal (D8's own
  // requirement) rather than argued about: the checkbox, the confirmSlug
  // gate in front of it, and describeApiFailureDetailed on the refusal.
  describe("rollback restoreData checkbox (P3d, property 8)", () => {
    it("renders the checkbox present and DISABLED, with a hint, when the row's hasData is false", async () => {
      route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
        [LIST]: () =>
          json(200, {
            checkpoints: [checkpointFixture({ id: 1, label: "точка A", hasData: false })],
          }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
      const dialog = await screen.findByRole("dialog");
      const checkbox = within(dialog).getByTestId("rollback-restore-data");
      expect(checkbox).toBeInTheDocument();
      expect(checkbox).toBeDisabled();
      expect(within(dialog).getByTestId("rollback-restore-data-hint")).toHaveTextContent(
        "нет сохранённых записей ресурсов",
      );
    });

    it("does not render the disabled hint when the row's hasData is true", async () => {
      route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
        [LIST]: () =>
          json(200, {
            checkpoints: [checkpointFixture({ id: 1, label: "точка A", hasData: true })],
          }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
      const dialog = await screen.findByRole("dialog");
      expect(within(dialog).getByTestId("rollback-restore-data")).not.toBeDisabled();
      expect(screen.queryByTestId("rollback-restore-data-hint")).not.toBeInTheDocument();
    });

    it("refuses to submit when the box is checked and the slug field is empty — no request is sent", async () => {
      const fetchMock = route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", slug: "alex" })),
        [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
      const dialog = await screen.findByRole("dialog");
      await userEvent.click(within(dialog).getByTestId("rollback-restore-data"));
      await userEvent.click(within(dialog).getByTestId("checkpoint-rollback-confirm"));

      expect(await screen.findByText("Укажите слаг воркспейса")).toBeInTheDocument();
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(0);
    });

    it("refuses to submit when the box is checked and the typed slug does not match the workspace's own — no request is sent", async () => {
      // A field PRE-FILLED from the workspace already in hand would satisfy
      // this test while destroying the point D7 gives the slug — the field
      // starts empty and this test types a WRONG value into it, never the
      // right one, so a submitted body carrying the right slug cannot make
      // this observation the way a pre-filled field could.
      const fetchMock = route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", slug: "alex" })),
        [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
      const dialog = await screen.findByRole("dialog");
      await userEvent.click(within(dialog).getByTestId("rollback-restore-data"));
      // The field must not already hold the workspace's slug.
      expect(within(dialog).getByTestId("rollback-confirm-slug")).toHaveValue("");
      await userEvent.type(within(dialog).getByTestId("rollback-confirm-slug"), "wrong-slug");
      await userEvent.click(within(dialog).getByTestId("checkpoint-rollback-confirm"));

      expect(
        await screen.findByText("Слаг не совпадает со слагом этого воркспейса"),
      ).toBeInTheDocument();
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(0);
    });

    it("submits both restoreData and the typed slug when the box is checked and the slug matches", async () => {
      const fetchMock = route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", slug: "alex" })),
        [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
        [ROLLBACK]: () => json(200, { revision: 8, scenarioActive: false, dataRestored: true }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
      const dialog = await screen.findByRole("dialog");
      await userEvent.click(within(dialog).getByTestId("rollback-restore-data"));
      await userEvent.type(within(dialog).getByTestId("rollback-confirm-slug"), "alex");
      await userEvent.click(within(dialog).getByTestId("checkpoint-rollback-confirm"));

      await waitFor(() => {
        const rollbacks = fetchMock.mock.calls.filter(
          ([url, init]) =>
            String(url) === `/api/workspaces/${WS}/rollback/1` && init?.method === "POST",
        );
        expect(rollbacks).toHaveLength(1);
        expect(JSON.parse(String(rollbacks[0]?.[1]?.body))).toEqual({
          restoreData: true,
          confirmSlug: "alex",
        });
      });
    });

    it("shows the server's own message on a refusal, since the 413 is unpredictable from the row's hasData (D8)", async () => {
      route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", slug: "alex" })),
        [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
        [ROLLBACK]: () =>
          json(413, {
            error: {
              code: "too_large",
              message: "the current entity population is too large to snapshot",
            },
          }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.click(await screen.findByTestId("checkpoint-rollback"));
      const dialog = await screen.findByRole("dialog");
      await userEvent.click(within(dialog).getByTestId("rollback-restore-data"));
      await userEvent.type(within(dialog).getByTestId("rollback-confirm-slug"), "alex");
      await userEvent.click(within(dialog).getByTestId("checkpoint-rollback-confirm"));

      expect(await screen.findByRole("alert")).toHaveTextContent(
        "the current entity population is too large to snapshot",
      );
    });
  });

  it("asks before deleting, says plainly that it cannot be undone, and does not call the server on cancel", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("checkpoint-delete"));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("точка A");
    // SIG-DELCP: delete writes no safety-net checkpoint of its own — the
    // confirmation must say the deletion has no undo, unlike rollback/reset.
    expect(dialog).toHaveTextContent("безвозвратно");
    expect(dialog).toHaveTextContent("нет отмены");

    await userEvent.click(within(dialog).getByText("Отмена"));
    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
      expect(deletes).toHaveLength(0);
    });
  });

  it("deletes on confirmation and invalidates only the checkpoint list, never the workspace (SIG-DELCP)", async () => {
    const fetchMock = route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
      [DELETE_CP]: () => new Response(null, { status: 204 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<HistoryPage id={WS} />, { queryClient });

    await userEvent.click(await screen.findByTestId("checkpoint-delete"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("checkpoint-delete-confirm"));

    await waitFor(() => {
      const deletes = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url) === `/api/workspaces/${WS}/checkpoints/1` && init?.method === "DELETE",
      );
      expect(deletes).toHaveLength(1);
    });
    await waitFor(() => {
      const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
      expect(keys).toContainEqual(getListCheckpointsQueryKey(WS));
      // Delete bumps no revision (SIG-DELCP) — nothing about the workspace
      // itself changed, so unlike rollback there is nothing stale there.
      expect(keys).not.toContainEqual(getGetWorkspaceQueryKey(WS));
    });
  });

  it("names the checkpoint when a delete fails", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A" })] }),
      [DELETE_CP]: () => json(404, { error: { code: "not_found", message: "no such checkpoint" } }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    await userEvent.click(await screen.findByTestId("checkpoint-delete"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("checkpoint-delete-confirm"));

    expect(await screen.findByRole("alert")).toHaveTextContent("«точка A»");
  });

  // Clause 34a, D14.3: the screen intro is replaced too — it used to define a
  // checkpoint without mentioning resources at all.
  it("describes what a checkpoint holds, including confirmed resources and the entities carve-out, in the screen intro (P3b, D14.3)", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
      [LIST]: () => json(200, { checkpoints: [] }),
    });
    renderInRouter(<HistoryPage id={WS} />);

    expect(await screen.findByTestId("history-intro")).toHaveTextContent(
      "Чекпойнт — снимок слоя воркспейса: настройки, правки операций, свои эндпоинты и " +
        "подтверждённые ресурсы. При откате можно вернуть и сами записи ресурсов — флажком «вернуть " +
        "и данные ресурсов», если эта точка их сохранила. Откат и сброс правок сохраняют свою " +
        "собственную точку прямо перед тем, как что-то стереть, так что их можно отменить откатом " +
        "на неё. Сброс ДАННЫХ ресурсов — нет: он необратим.",
    );
  });

  describe("ResetDataCard (P3b)", () => {
    it("renders the card's irreversibility warning verbatim (clause 34a, D14.3)", async () => {
      route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [] }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      expect(await screen.findByTestId("reset-data-warning")).toHaveTextContent(
        "Это НЕОБРАТИМО: записи, созданные через POST, будут удалены. В отличие от отката и " +
          "сброса правок, «сбросить данные ресурсов» не сохраняет свою собственную точку перед " +
          "тем, как стереть — если не сохранить чекпойнт вручную заранее, восстановить записи " +
          "будет нечем. «Заполнить заново» запишет то, что даёт текущая конфигурация воркспейса, " +
          "а не то, что было при подтверждении, и сбросит счётчик идентификаторов на размер новой " +
          "популяции — идентификатор, который клиент уже получал и удалял, может быть выдан снова. " +
          "«Очистить» оставит коллекции пустыми и НЕ сбросит счётчик — следующая запись получит " +
          "следующий номер, а не первый.",
      );
    });

    it("submits the selected mode and the typed slug, and reports a deleted count", async () => {
      const fetchMock = route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [] }),
        [RESET_DATA]: () => json(200, { changed: true, deleted: 7, skipped: [] }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.click(await screen.findByText("Очистить"));
      await userEvent.type(await screen.findByTestId("reset-data-slug"), "alex");
      await userEvent.click(await screen.findByTestId("reset-data-submit"));

      await waitFor(() => {
        const posts = fetchMock.mock.calls.filter(
          ([url, init]) =>
            String(url) === `/api/workspaces/${WS}/reset-data` && init?.method === "POST",
        );
        expect(posts).toHaveLength(1);
        expect(JSON.parse(String(posts[0]?.[1]?.body))).toEqual({
          mode: "clear",
          confirmSlug: "alex",
        });
      });
      expect(await screen.findByTestId("reset-data-result")).toHaveTextContent(
        "Удалено записей: 7",
      );
    });

    it("reports nothing-changed rather than a deleted count when changed is false", async () => {
      route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [] }),
        [RESET_DATA]: () => json(200, { changed: false, deleted: 0, skipped: [] }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.type(await screen.findByTestId("reset-data-slug"), "alex");
      await userEvent.click(await screen.findByTestId("reset-data-submit"));

      expect(await screen.findByTestId("reset-data-result")).toHaveTextContent(
        "Ничего не изменилось",
      );
    });

    // Clause 34a: over a response carrying one skip of each reason, the
    // four "{routeFamily} — пропущено: …" lines are the only place an
    // operator learns why their data did not come back.
    it("names every skipped family and its reason, one line per enum value", async () => {
      route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [] }),
        [RESET_DATA]: () =>
          json(200, {
            changed: true,
            deleted: 3,
            skipped: [
              { routeFamily: "/gone", reason: "stranded" },
              { routeFamily: "/big", reason: "over_caps" },
              { routeFamily: "/broken", reason: "population_failed" },
              { routeFamily: "/orgs/{}/users", reason: "group_skipped" },
            ],
          }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      await userEvent.type(await screen.findByTestId("reset-data-slug"), "alex");
      await userEvent.click(await screen.findByTestId("reset-data-submit"));

      const result = await screen.findByTestId("reset-data-result");
      expect(result).toHaveTextContent("/gone — пропущено: семейства нет в текущей спеке");
      expect(result).toHaveTextContent("/big — пропущено: не помещается в лимиты");
      expect(result).toHaveTextContent("/broken — пропущено: не удалось сгенерировать записи");
      expect(result).toHaveTextContent(
        "/orgs/{}/users — пропущено: пропущено вместе с родителем или потомком, которого не удалось заполнить",
      );
    });

    it("shows the server's own message on a confirmSlug refusal", async () => {
      route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [] }),
        [RESET_DATA]: () =>
          json(409, {
            error: { code: "confirm_slug_mismatch", message: "confirmSlug does not match" },
          }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      // A21 (U10): a slug that does not match what the screen knows never
      // reaches the server — the button is disabled until it does. The
      // server can still refuse (a slug changed elsewhere), and its own
      // words are what the screen shows then.
      await userEvent.type(await screen.findByTestId("reset-data-slug"), "wrong");
      expect(screen.getByTestId("reset-data-submit")).toBeDisabled();
      await userEvent.clear(screen.getByTestId("reset-data-slug"));
      await userEvent.type(screen.getByTestId("reset-data-slug"), "alex");
      await userEvent.click(screen.getByTestId("reset-data-submit"));

      expect(await screen.findByRole("alert")).toHaveTextContent("confirmSlug does not match");
    });

    it("clears the form and any prior result or failure on cancel", async () => {
      route({
        [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex" })),
        [LIST]: () => json(200, { checkpoints: [] }),
        [RESET_DATA]: () => json(200, { changed: true, deleted: 1, skipped: [] }),
      });
      renderInRouter(<HistoryPage id={WS} />);

      const slugField = await screen.findByTestId("reset-data-slug");
      await userEvent.type(slugField, "alex");
      await userEvent.click(await screen.findByTestId("reset-data-submit"));
      expect(await screen.findByTestId("reset-data-result")).toBeInTheDocument();

      await userEvent.click(await screen.findByTestId("reset-data-cancel"));

      expect(screen.queryByTestId("reset-data-result")).not.toBeInTheDocument();
      expect(screen.getByTestId("reset-data-slug")).toHaveValue("");
    });
  });

  // A21 (G7): the id and the data flag on the row, the rollback's own result.
  it("shows the checkpoint id and «с данными ресурсов» on the row, and reports what a rollback restored", async () => {
    route({
      [WORKSPACE]: () => json(200, workspaceFixture({ id: WS, name: "Alex", slug: "alex" })),
      [LIST]: () =>
        json(200, { checkpoints: [checkpointFixture({ id: 1, label: "точка A", hasData: true })] }),
      [ROLLBACK]: () => json(200, { revision: 9, scenarioActive: false, dataRestored: false }),
    });
    renderInRouter(<HistoryPage id={WS} />);
    const row = await screen.findByTestId("checkpoint-row");
    expect(row).toHaveTextContent("#1 ·");
    expect(row).toHaveTextContent("с данными ресурсов");

    await userEvent.click(within(row).getByTestId("checkpoint-rollback"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("rollback-restore-data"));
    await userEvent.type(within(dialog).getByTestId("rollback-confirm-slug"), "alex");
    await userEvent.click(within(dialog).getByTestId("checkpoint-rollback-confirm"));
    expect(await screen.findByTestId("rollback-result")).toHaveTextContent("ревизия 9");
    expect(screen.getByTestId("rollback-result")).toHaveTextContent("данные ресурсов не трогались");
  });
});

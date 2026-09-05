import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient } from "@tanstack/react-query";
import { notifications } from "@mantine/notifications";
import { SpecsPage } from "./SpecsPage";
import { makeQueryClient, renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import {
  operationViewFixture,
  reportViewFixture,
  specImportViewFixture,
  specViewFixture,
  warningViewFixture,
} from "@/test/fixtures";
import {
  getListResourceSuggestionsQueryKey,
  getListWorkspaceResourcesQueryKey,
} from "@/api/generated/resources/resources.ts";

// notifications.show renders into a portal main.tsx mounts (<Notifications
// />) and the test render helpers do not — mocking the module is the only
// way to observe what a screen asked it to show, the same reason
// AuthPresetPanel.test.tsx spies on invalidateQueries instead of reading the
// query cache's own state.
vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.mocked(notifications.show).mockClear();
});

// jsonFile builds a real File the way a browser hands one to onDrop —
// react-dropzone drives its hidden <input type="file"> through a plain
// change event, so userEvent.upload against that input exercises the same
// path a real drag-and-drop would.
function jsonFile(name: string, body: unknown): File {
  return new File([JSON.stringify(body)], name, { type: "application/json" });
}

describe("SpecsPage", () => {
  it("shows an empty list as an empty state, not an error", async () => {
    route({ "GET /api/specs": () => json(200, []) });
    renderInRouter(<SpecsPage />);

    expect(await screen.findByTestId("specs-empty")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("lists name, version, format, base path and an abbreviated hash", async () => {
    route({
      "GET /api/specs": () =>
        json(200, [
          specViewFixture({
            id: 1,
            name: "Petstore",
            version: "1.0.0",
            format: "oas31",
            basePath: "/v1",
            hash: "deadbeefcafefeed",
            createdAt: 1_700_000_000,
          }),
        ]),
    });
    renderInRouter(<SpecsPage />);

    const row = await screen.findByTestId("spec-row");
    expect(row).toHaveTextContent("Petstore");
    expect(row).toHaveTextContent("1.0.0");
    expect(row).toHaveTextContent("oas31");
    expect(row).toHaveTextContent("/v1");
    // The full hash never appears — only its abbreviation, so a change in
    // that behaviour (e.g. someone "fixing" it to show the whole thing) goes
    // red here instead of silently wrapping every row onto two lines.
    expect(row).not.toHaveTextContent("deadbeefcafefeed");
    expect(row).toHaveTextContent("deadbeefcafe");
  });

  it("uploads a .yaml file as-is: A8 lifted the client-side refusal, the server converts", async () => {
    const fetchMock = route({
      "GET /api/specs": () => json(200, []),
      "POST /api/specs": () =>
        json(201, specImportViewFixture({ id: 9, name: "openapi", report: reportViewFixture() })),
    });
    renderInRouter(<SpecsPage />);

    await screen.findByTestId("spec-import-form");
    const yamlFile = new File(
      ["openapi: 3.0.0\ninfo: {title: y, version: '1'}\npaths: {}\n"],
      "openapi.yaml",
      { type: "application/x-yaml" },
    );
    await userEvent.upload(screen.getByTestId("spec-file-input"), yamlFile);
    expect(screen.queryByTestId("spec-yaml-warning")).not.toBeInTheDocument();
    await screen.findByTestId("spec-import-name");
    await userEvent.click(screen.getByTestId("spec-import-submit"));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(posts).toHaveLength(1);
      const body = JSON.parse(String(posts[0]?.[1]?.body)) as { document: string; source: string };
      expect(body.source).toBe("upload");
      expect(body.document.startsWith("openapi: 3.0.0")).toBe(true);
    });
  });

  it("imports a dropped .json file with an editable, file-derived name", async () => {
    const fetchMock = route({
      "GET /api/specs": () => json(200, []),
      "POST /api/specs": () =>
        json(
          201,
          specImportViewFixture({
            id: 9,
            name: "petstore",
            report: reportViewFixture({ operations: 3, degraded: 0, warnings: [] }),
          }),
        ),
    });
    renderInRouter(<SpecsPage />);

    await screen.findByTestId("spec-import-form");
    const input = screen.getByTestId("spec-file-input");
    await userEvent.upload(input, jsonFile("petstore.json", { openapi: "3.1.0" }));

    const nameField = await screen.findByTestId("spec-import-name");
    expect(nameField).toHaveValue("petstore");

    await userEvent.click(screen.getByTestId("spec-import-submit"));

    expect(await screen.findByTestId("spec-import-result")).toHaveTextContent("импортирована");
    // A21 (G3): the next step is on the result itself.
    expect(screen.getByTestId("spec-import-create-workspace")).toBeInTheDocument();
    expect(await screen.findByTestId("spec-report")).toHaveTextContent("операций: 3");

    const posts = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(posts).toHaveLength(1);
    const sentBody = JSON.parse(String(posts[0]?.[1]?.body));
    expect(sentBody.source).toBe("upload");
    expect(sentBody.name).toBe("petstore");
  });

  it("says plainly when the same bytes were already imported, instead of adding a second row", async () => {
    route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 1, name: "Petstore" })]),
      "POST /api/specs": () =>
        json(
          200,
          specImportViewFixture({ id: 1, name: "Petstore", duplicate: true, report: null }),
        ),
    });
    renderInRouter(<SpecsPage />);

    await screen.findByTestId("spec-import-form");
    await userEvent.upload(
      screen.getByTestId("spec-file-input"),
      jsonFile("petstore.json", { openapi: "3.1.0" }),
    );
    await userEvent.click(await screen.findByTestId("spec-import-submit"));

    expect(await screen.findByTestId("spec-import-result")).toHaveTextContent(
      "уже был импортирован",
    );
    // Still exactly one row: the duplicate response must not read as a
    // second spec having been added.
    expect(await screen.findAllByTestId("spec-row")).toHaveLength(1);
  });

  it("shows the server's own sentence for an unsupported-format refusal", async () => {
    route({
      "GET /api/specs": () => json(200, []),
      "POST /api/specs": () =>
        json(400, {
          error: {
            code: "bad_request",
            message: "unsupported format: Swagger 2.0 and YAML input land in a later phase",
          },
        }),
    });
    renderInRouter(<SpecsPage />);

    await screen.findByTestId("spec-import-form");
    await userEvent.upload(
      screen.getByTestId("spec-file-input"),
      jsonFile("swagger.json", { swagger: "2.0" }),
    );
    await userEvent.click(await screen.findByTestId("spec-import-submit"));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Swagger 2.0 and YAML input land in a later phase");
  });

  it("invalidates the specs list after a successful import", async () => {
    const fetchMock = route({
      "GET /api/specs": () => json(200, []),
      "POST /api/specs": () => json(201, specImportViewFixture({ id: 9, report: null })),
    });
    renderInRouter(<SpecsPage />);

    await screen.findByTestId("spec-import-form");
    await userEvent.upload(
      screen.getByTestId("spec-file-input"),
      jsonFile("petstore.json", { openapi: "3.1.0" }),
    );
    await userEvent.click(await screen.findByTestId("spec-import-submit"));
    await screen.findByTestId("spec-import-result");

    await waitFor(() => {
      const listCalls = fetchMock.mock.calls.filter(
        ([url, init]) => String(url) === "/api/specs" && (init?.method ?? "GET") === "GET",
      );
      // Once on mount, once more because the mutation invalidated the key —
      // deleting that invalidateQueries call would leave this at 1 forever.
      expect(listCalls.length).toBeGreaterThanOrEqual(2);
    });
  });

  it("shows 'нет отчёта' for a spec with no stored report, not an error", async () => {
    route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "GET /api/specs/4": () => json(200, specViewFixture({ id: 4, name: "Petstore" })),
      "GET /api/specs/4/report": () =>
        json(404, { error: { code: "not_found", message: "no report stored" } }),
    });
    renderInRouter(<SpecsPage />);

    await userEvent.click(await screen.findByTestId("spec-row"));

    expect(await screen.findByTestId("spec-report-missing")).toHaveTextContent("нет отчёта");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows the stored report's warnings, by pointer/code/message", async () => {
    route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "GET /api/specs/4": () => json(200, specViewFixture({ id: 4, name: "Petstore" })),
      "GET /api/specs/4/report": () =>
        json(
          200,
          reportViewFixture({
            operations: 7,
            degraded: 2,
            warnings: [
              warningViewFixture({ pointer: "/paths/~1pets/get", code: "x", message: "why" }),
            ],
          }),
        ),
    });
    renderInRouter(<SpecsPage />);

    await userEvent.click(await screen.findByTestId("spec-row"));

    const report = await screen.findByTestId("spec-report");
    expect(report).toHaveTextContent("операций: 7");
    expect(report).toHaveTextContent("с потерей точности: 2");
    expect(screen.getByTestId("spec-report-warnings")).toHaveTextContent("/paths/~1pets/get");
    expect(screen.getByTestId("spec-report-warnings")).toHaveTextContent("why");
  });

  it("fetches a spec's own record for the detail view, not the stale list row", async () => {
    const fetchMock = route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Old name" })]),
      "GET /api/specs/4": () => json(200, specViewFixture({ id: 4, name: "Fresh name" })),
      "GET /api/specs/4/report": () => json(404, { error: { code: "not_found", message: "none" } }),
    });
    renderInRouter(<SpecsPage />);

    await userEvent.click(await screen.findByTestId("spec-row"));

    // The detail heading shows the freshly-fetched name, proving useGetSpec
    // was actually called rather than the list row being reused.
    expect(await screen.findByTestId("spec-detail-name")).toHaveTextContent("Fresh name");
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain("/api/specs/4");
  });

  it("loads operations only on demand, and flags an exactly-500 answer as capped", async () => {
    const fetchMock = route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "GET /api/specs/4": () => json(200, specViewFixture({ id: 4, name: "Petstore" })),
      "GET /api/specs/4/report": () => json(404, { error: { code: "not_found", message: "none" } }),
      "GET /api/specs/4/operations?limit=500": () =>
        json(
          200,
          Array.from({ length: 500 }, (_, i) =>
            operationViewFixture({ id: i + 1, path: `/x/${i}` }),
          ),
        ),
    });
    renderInRouter(<SpecsPage />);

    await userEvent.click(await screen.findByTestId("spec-row"));
    await screen.findByTestId("spec-load-operations");

    const opsCallsBefore = fetchMock.mock.calls.filter(([url]) =>
      String(url).includes("/operations"),
    );
    expect(opsCallsBefore).toHaveLength(0);

    await userEvent.click(screen.getByTestId("spec-load-operations"));

    expect(await screen.findByTestId("spec-operations-capped")).toBeInTheDocument();
    expect(await screen.findAllByTestId("spec-operation-row")).toHaveLength(500);
  });

  it("deletes behind a confirmation and shows the server's own 409 message", async () => {
    const fetchMock = route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "GET /api/specs/4": () => json(200, specViewFixture({ id: 4, name: "Petstore" })),
      "GET /api/specs/4/report": () => json(404, { error: { code: "not_found", message: "none" } }),
      "DELETE /api/specs/4": () =>
        json(409, { error: { code: "conflict", message: "still attached to workspace bob" } }),
    });
    renderInRouter(<SpecsPage />);

    await userEvent.click(await screen.findByTestId("spec-row"));
    await userEvent.click(await screen.findByTestId("spec-delete"));

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Petstore");
    await userEvent.click(within(dialog).getByTestId("spec-delete-confirm"));

    expect(await screen.findByTestId("spec-delete-error")).toHaveTextContent(
      "still attached to workspace bob",
    );
    const deletes = fetchMock.mock.calls.filter(([, init]) => init?.method === "DELETE");
    expect(deletes).toHaveLength(1);
  });

  it("invalidates the specs list and closes the detail panel after a successful delete", async () => {
    const fetchMock = route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "GET /api/specs/4": () => json(200, specViewFixture({ id: 4, name: "Petstore" })),
      "GET /api/specs/4/report": () => json(404, { error: { code: "not_found", message: "none" } }),
      "DELETE /api/specs/4": () => json(204, undefined),
    });
    renderInRouter(<SpecsPage />);

    await userEvent.click(await screen.findByTestId("spec-row"));
    await userEvent.click(await screen.findByTestId("spec-delete"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("spec-delete-confirm"));

    await waitFor(() => {
      const listCalls = fetchMock.mock.calls.filter(
        ([url, init]) => String(url) === "/api/specs" && (init?.method ?? "GET") === "GET",
      );
      expect(listCalls.length).toBeGreaterThanOrEqual(2);
    });
    // The detail panel closes: selection was cleared by onDeleted.
    await waitFor(() => expect(screen.queryByTestId("spec-detail")).not.toBeInTheDocument());
  });

  // P24: the button asks before it acts, and reports what the call answered.
  it("asks before rederiving, and reports the response's own generation and counts", async () => {
    const fetchMock = route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "POST /api/specs/4/rederive": () =>
        json(200, {
          changed: true,
          generation: 2,
          added: ["/orgs/{orgId}/teams"],
          removed: ["/legacy"],
        }),
    });
    renderInRouter(<SpecsPage />);

    await userEvent.click(await screen.findByTestId("spec-rederive"));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Пересобрать ресурсы");

    // No request is sent until the modal is confirmed.
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);

    await userEvent.click(within(dialog).getByTestId("spec-rederive-confirm"));

    await waitFor(() => {
      expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
    });
    await waitFor(() => expect(notifications.show).toHaveBeenCalledTimes(1));
    const shown = vi.mocked(notifications.show).mock.calls[0]?.[0] as { message: string };
    expect(shown.message).toContain("2");
    expect(shown.message).toContain("1");
  });

  it("reports «Изменений нет», with no counts, when a rederive changes nothing", async () => {
    route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "POST /api/specs/4/rederive": () =>
        json(200, { changed: false, generation: 1, added: [], removed: [] }),
    });
    renderInRouter(<SpecsPage />);

    await userEvent.click(await screen.findByTestId("spec-rederive"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("spec-rederive-confirm"));

    await waitFor(() =>
      expect(notifications.show).toHaveBeenCalledWith(
        expect.objectContaining({ message: "Изменений нет" }),
      ),
    );
  });

  it("shows the server's own message on a refusal and invalidates nothing", async () => {
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "POST /api/specs/4/rederive": () =>
        json(409, { error: { code: "conflict", message: "stale_generation" } }),
    });
    renderInRouter(<SpecsPage />, { queryClient });

    await userEvent.click(await screen.findByTestId("spec-rederive"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("spec-rederive-confirm"));

    expect(await screen.findByTestId("spec-rederive-error")).toHaveTextContent("stale_generation");
    expect(invalidateSpy).not.toHaveBeenCalled();
    expect(notifications.show).not.toHaveBeenCalled();
  });

  // D8.2: invalidation is by predicate, not by key — /specs holds no
  // workspace id, so it cannot build getListWorkspaceResourcesQueryKey(id)
  // for any particular workspace. Two workspace ids in the cache at once is
  // the point: a producer who invalidates a key built from a single id would
  // pass a one-workspace fixture and leave the other stale.
  it("invalidates every open workspace roster by predicate, plus the spec's own suggestions", async () => {
    // A plain makeQueryClient() has gcTime: 0 — right for every other test
    // here, wrong for this one: setQueryData below creates two queries with
    // no active observer, and a 0 gcTime schedules them for garbage
    // collection almost immediately, so a later getQueryState read would see
    // `undefined` regardless of whether the real invalidation ran. This test
    // needs the rows to still be IN the cache when it checks isInvalidated,
    // so it builds its own client with the library's default retention.
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } },
    });
    queryClient.setQueryData(getListWorkspaceResourcesQueryKey(11), []);
    queryClient.setQueryData(getListWorkspaceResourcesQueryKey(22), []);
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    route({
      "GET /api/specs": () => json(200, [specViewFixture({ id: 4, name: "Petstore" })]),
      "POST /api/specs/4/rederive": () =>
        json(200, {
          changed: true,
          generation: 2,
          added: ["/orgs/{orgId}/teams"],
          removed: ["/legacy"],
        }),
    });
    renderInRouter(<SpecsPage />, { queryClient });

    await userEvent.click(await screen.findByTestId("spec-rederive"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("spec-rederive-confirm"));

    await waitFor(() => expect(invalidateSpy).toHaveBeenCalled());

    // The real assertion: react-query's own filter-combination semantics,
    // not a hand-invoked copy of the predicate — a second field slipped into
    // the same invalidateQueries call (e.g. a stray queryKey prefix) would
    // AND together with predicate and silently no-op the real invalidation
    // while a replayed-predicate-only check stayed green.
    await waitFor(() => {
      expect(queryClient.getQueryState(getListWorkspaceResourcesQueryKey(11))?.isInvalidated).toBe(
        true,
      );
      expect(queryClient.getQueryState(getListWorkspaceResourcesQueryKey(22))?.isInvalidated).toBe(
        true,
      );
    });

    const predicateCall = invalidateSpy.mock.calls.find(
      (call) => typeof (call[0] as { predicate?: unknown })?.predicate === "function",
    );
    expect(predicateCall).toBeDefined();
    const predicate = (
      predicateCall![0] as { predicate: (q: { queryKey: readonly unknown[] }) => boolean }
    ).predicate;
    expect(predicate({ queryKey: ["/api/workspaces/11/operations"] })).toBe(false);

    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: getListResourceSuggestionsQueryKey(4) }),
    );
  });
});

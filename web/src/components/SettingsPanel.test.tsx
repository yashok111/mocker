import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SettingsPanel } from "./SettingsPanel";
import { renderWithProviders, makeQueryClient } from "@/test/render";
import { json, route } from "@/test/http";
import { specViewFixture, workspaceFixture } from "@/test/fixtures";
import type { Settings } from "@/api/generated/schemas";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// A distinctive workspace: every field SettingsPanel does NOT render carries
// a value nothing in this file's assertions would produce by accident, so a
// PATCH body that happens to equal it proves the field survived untouched
// rather than having been coincidentally rebuilt to the same shape.
function distinctiveWorkspace() {
  const settings: Settings = {
    seed: 1,
    basePath: "/distinctive-base-path",
    listSize: 3,
    nullRate: 0.1,
    envelope: null,
    identity: {
      id: "distinctive-identity-id",
      name: "Alex",
      email: "alex@example.com",
      roles: ["user"],
      org: { id: "distinctive-org-id", name: "Distinctive Org", type: "company" },
    },
    auth: {
      jwtTtlSec: 3600,
      alg: "HS256",
      signingKey: "distinctive-signing-key",
      requireHeader: false,
    },
    cors: { mode: "list", credentials: false },
    validateRequests: true,
    delayMs: 0,
    notFoundBody: { distinctive: "not-found-marker" },
  };
  return workspaceFixture({ settings });
}

// Wraps whatever route() installed so a test can also capture the JSON body
// of PATCH requests, since route()'s own handlers are argument-less.
function captureBody(onBody: (body: unknown) => void) {
  const previous = globalThis.fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "PATCH" && init.body) {
        onBody(JSON.parse(String(init.body)));
      }
      return previous(input, init);
    }),
  );
}

describe("SettingsPanel", () => {
  it("renders the fields the serving path reads, sourced from workspace.settings", () => {
    const workspace = distinctiveWorkspace();
    route({ "GET /api/specs": () => json(200, []) });
    renderWithProviders(<SettingsPanel workspace={workspace} />);

    expect(screen.getByLabelText("Название")).toHaveValue(workspace.name);
    expect(screen.getByLabelText("Имя")).toHaveValue(workspace.settings.identity.name);
    expect(screen.getByLabelText("E-mail")).toHaveValue(workspace.settings.identity.email);
    expect(screen.getByLabelText("Роли")).toHaveValue(workspace.settings.identity.roles.join(", "));
  });

  it("does not render a control for validateRequests", () => {
    const workspace = distinctiveWorkspace();
    route({ "GET /api/specs": () => json(200, []) });
    renderWithProviders(<SettingsPanel workspace={workspace} />);

    expect(screen.queryByText(/validateRequests/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/валидац/i)).not.toBeInTheDocument();
  });

  it("edits the signing key through a masked PasswordInput", () => {
    const workspace = distinctiveWorkspace();
    route({ "GET /api/specs": () => json(200, []) });
    renderWithProviders(<SettingsPanel workspace={workspace} />);

    const input = screen.getByLabelText("Ключ подписи JWT");
    expect(input).toHaveAttribute("type", "password");
  });

  it("PATCHes the whole settings object with only the edited field changed", async () => {
    const user = userEvent.setup();
    const workspace = distinctiveWorkspace();
    let body: unknown;
    route({
      "GET /api/specs": () => json(200, []),
      "PATCH /api/workspaces/1": () => json(200, workspace),
    });
    captureBody((b) => {
      body = b;
    });
    renderWithProviders(<SettingsPanel workspace={workspace} />);

    // Edit exactly one rendered field: seed, the determinism knob.
    const seedInput = screen.getByLabelText("Seed");
    await user.clear(seedInput);
    await user.type(seedInput, "42");
    await user.click(screen.getByTestId("settings-submit"));

    await screen.findByTestId("settings-saved");

    const expected = {
      name: workspace.name,
      settings: { ...workspace.settings, seed: 42 },
      // A3: the expectation this screen sends is the version sitting beside
      // the document it read — workspace.editVersion, never re-fetched.
      editVersion: workspace.editVersion,
    };
    // A single deep-equal against "workspace.settings with only seed
    // changed" is what proves basePath, cors, notFoundBody, identity.id,
    // identity.org and validateRequests all survived untouched — rebuilding
    // Settings from the six rendered fields instead of from workspace.settings
    // would fail this assertion by dropping every one of them.
    expect(body).toEqual(expected);
  });

  it("preserves auth.signingKey (unedited) exactly, the third instance of the wholesale-PATCH trap", async () => {
    const user = userEvent.setup();
    const workspace = distinctiveWorkspace();
    let body: unknown;
    route({
      "GET /api/specs": () => json(200, []),
      "PATCH /api/workspaces/1": () => json(200, workspace),
    });
    captureBody((b) => {
      body = b;
    });
    renderWithProviders(<SettingsPanel workspace={workspace} />);

    // Edit a field elsewhere in the form (jwtTtlSec) without touching
    // signingKey — a naive rebuild from only-rendered-fields would still get
    // this one right (it IS rendered), so the real trap this pins is the
    // hidden fields covered by the test above; this test pins that editing
    // one auth.* field never wipes its siblings within auth either.
    const ttlInput = screen.getByLabelText("TTL токена, сек");
    await user.clear(ttlInput);
    await user.type(ttlInput, "7200");
    await user.click(screen.getByTestId("settings-submit"));

    await screen.findByTestId("settings-saved");

    expect((body as { settings: Settings }).settings.auth).toEqual({
      ...workspace.settings.auth,
      jwtTtlSec: 7200,
    });
  });

  it("attaches a spec by id without sending name or settings, and invalidates operations + auth-preset", async () => {
    const user = userEvent.setup();
    const workspace = distinctiveWorkspace();
    const spec = specViewFixture({ id: 9, name: "Petstore", version: "2.0.0" });
    let body: unknown;
    route({
      "GET /api/specs": () => json(200, [spec]),
      "PATCH /api/workspaces/1": () => json(200, { ...workspace, specId: 9 }),
    });
    captureBody((b) => {
      body = b;
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderWithProviders(<SettingsPanel workspace={workspace} />, { queryClient });

    const select = await screen.findByRole("combobox", { name: "Привязать спеку" });
    await user.click(select);
    await user.click(await screen.findByText("Petstore (v2.0.0)"));

    await screen.findByTestId("settings-spec-attached");

    // Attaching a spec is a standalone action: it must not resend name or
    // the whole settings object, only specId — plus A3's required editVersion,
    // which travels with every write from this screen regardless of which
    // action triggered it.
    expect(body).toEqual({ specId: 9, editVersion: workspace.editVersion });

    const invalidatedKeys = invalidateSpy.mock.calls
      .map((call) => (call[0] as { queryKey?: readonly unknown[] })?.queryKey?.[0])
      .filter((key): key is string => typeof key === "string");
    expect(invalidatedKeys).toContain("/api/workspaces/1");
    expect(invalidatedKeys).toContain("/api/workspaces/1/operations");
    expect(invalidatedKeys).toContain("/api/workspaces/1/auth-preset");
  });

  it("does not invalidate operations or auth-preset for a plain settings save (no specId change)", async () => {
    const user = userEvent.setup();
    const workspace = distinctiveWorkspace();
    route({
      "GET /api/specs": () => json(200, []),
      "PATCH /api/workspaces/1": () => json(200, workspace),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderWithProviders(<SettingsPanel workspace={workspace} />, { queryClient });

    await user.click(screen.getByTestId("settings-submit"));
    await screen.findByTestId("settings-saved");

    const invalidatedKeys = invalidateSpy.mock.calls
      .map((call) => (call[0] as { queryKey?: readonly unknown[] })?.queryKey?.[0])
      .filter((key): key is string => typeof key === "string");
    expect(invalidatedKeys).toEqual(["/api/workspaces/1"]);
  });

  // A3/property 7 (UI half): the 409's `details` must reach this screen
  // (not parsed away in client.ts), the operator must see the translation,
  // and the "Загрузить актуальную версию" affordance must adopt the
  // conflict's own name/settings/editVersion so the RETRY carries the
  // fresh token rather than looping on the stale one.
  it("shows the edit_conflict affordance and retries with the conflict's own editVersion", async () => {
    const user = userEvent.setup();
    const workspace = distinctiveWorkspace();
    let patchCount = 0;
    let lastBody: { editVersion?: number; settings?: { basePath?: string } } | undefined;
    route({
      "GET /api/specs": () => json(200, []),
      "PATCH /api/workspaces/1": () => {
        patchCount += 1;
        if (patchCount === 1) {
          return json(409, {
            error: {
              code: "edit_conflict",
              message: "stale",
              details: {
                name: "Renamed elsewhere",
                // A21 (review B3): a field this form has no control for changed
                // elsewhere; the retry must carry the conflict's value, not the
                // stale prop's.
                settings: { ...workspace.settings, basePath: "/changed-elsewhere" },
                editVersion: 42,
              },
            },
          });
        }
        return json(200, workspace);
      },
    });
    const original = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "PATCH" && init.body) {
          lastBody = JSON.parse(String(init.body)) as {
            editVersion?: number;
            settings?: { basePath?: string };
          };
        }
        return original(input, init);
      }),
    );
    renderWithProviders(<SettingsPanel workspace={workspace} />);

    await user.click(screen.getByTestId("settings-submit"));
    const conflict = await screen.findByTestId("settings-edit-conflict");
    expect(lastBody?.editVersion).toBe(workspace.editVersion);

    expect(conflict).toHaveTextContent("Кто-то другой изменил это, пока вы редактировали");
    await user.click(screen.getByTestId("settings-conflict-reload"));

    // The form field adopted the conflict's own name.
    expect(screen.getByLabelText("Название")).toHaveValue("Renamed elsewhere");

    await user.click(screen.getByTestId("settings-submit"));
    await screen.findByTestId("settings-saved");

    expect(lastBody?.editVersion).toBe(42);
    expect(lastBody?.settings?.basePath).toBe("/changed-elsewhere");
    expect(patchCount).toBe(2);
  });

  it("says detaching a spec is not possible, and offers no control for it", () => {
    const workspace = distinctiveWorkspace();
    route({ "GET /api/specs": () => json(200, []) });
    renderWithProviders(<SettingsPanel workspace={workspace} />);

    expect(screen.getByText(/Отвязать спеку через этот экран пока нельзя/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /отвяз/i })).not.toBeInTheDocument();
  });

  // A21 (G12): a RE-bind asks; the first bind (the test above) does not.
  it("asks before replacing an already bound spec, and patches on confirm", async () => {
    const user = userEvent.setup();
    const workspace = { ...distinctiveWorkspace(), specId: 1 };
    let body: unknown;
    route({
      "GET /api/specs": () =>
        json(200, [
          specViewFixture({ id: 1, name: "Old", version: "1" }),
          specViewFixture({ id: 9, name: "Petstore", version: "2.0.0" }),
        ]),
      "PATCH /api/workspaces/1": () => json(200, { ...workspace, specId: 9 }),
    });
    captureBody((b) => {
      body = b;
    });
    renderWithProviders(<SettingsPanel workspace={workspace} />);
    const select = await screen.findByRole("combobox", { name: "Привязать спеку" });
    await user.click(select);
    await user.click(await screen.findByText("Petstore (v2.0.0)"));
    expect(body).toBeUndefined();
    await user.click(await screen.findByTestId("settings-spec-rebind-confirm"));
    await screen.findByTestId("settings-spec-attached");
    expect(body).toEqual({ specId: 9, editVersion: workspace.editVersion });
  });

  // A21 (G1): the four settings the panel preserved and never showed.
  it("shows and edits basePath, its values, CORS and the 404 body, and sends them on the wire", async () => {
    const user = userEvent.setup();
    const workspace = distinctiveWorkspace();
    let body: { settings?: Settings } | undefined;
    route({
      "GET /api/specs": () => json(200, []),
      "PATCH /api/workspaces/1": () => json(200, workspace),
    });
    captureBody((b) => {
      body = b as { settings?: Settings };
    });
    renderWithProviders(<SettingsPanel workspace={workspace} />);

    const basePath = await screen.findByTestId("settings-base-path");
    expect(basePath).toHaveValue("/distinctive-base-path");
    expect(screen.getByTestId("settings-cors-mode")).toHaveValue("list");
    expect(screen.getByTestId("settings-not-found-body")).toHaveValue(
      JSON.stringify({ distinctive: "not-found-marker" }, null, 2),
    );
    // The values box appears only once the base path carries a {param}.
    expect(screen.queryByTestId("settings-base-path-values")).toBeNull();
    await user.clear(basePath);
    await user.type(basePath, "/tenants/{{t}");
    await user.type(await screen.findByTestId("settings-base-path-values"), "acme\nglobex");
    await user.selectOptions(screen.getByTestId("settings-cors-mode"), "off");
    await user.click(screen.getByTestId("settings-submit"));
    await screen.findByTestId("settings-saved");

    expect(body?.settings).toMatchObject({
      basePath: "/tenants/{t}",
      basePathValues: ["acme", "globex"],
      cors: { mode: "off", credentials: false },
      notFoundBody: { distinctive: "not-found-marker" },
      identity: { id: "distinctive-identity-id", org: workspace.settings.identity.org },
    });
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthPresetPanel } from "./AuthPresetPanel";
import { renderInRouter, makeQueryClient } from "@/test/render";
import { json, route } from "@/test/http";
import { authPresetProposalFixture, bindingFixture } from "@/test/fixtures";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const WS_ID = 7;

describe("AuthPresetPanel", () => {
  it("shows the loading state before the proposal arrives", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<AuthPresetPanel id={WS_ID} />);

    expect(await screen.findByText("Загрузка…")).toBeInTheDocument();
  });

  it("shows a retry control when the preview fails", async () => {
    route({
      [`GET /api/workspaces/${WS_ID}/auth-preset`]: () =>
        json(500, { error: { code: "internal", message: "boom" } }),
    });
    renderInRouter(<AuthPresetPanel id={WS_ID} />);

    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Повторить" })).toBeInTheDocument();
  });

  it("renders every binding checked by default, alongside notes and the sample JWT", async () => {
    const proposal = authPresetProposalFixture({
      bindings: [
        bindingFixture({ method: "POST", path: "/auth/login", dataPath: "token" }),
        bindingFixture({ method: "POST", path: "/auth/refresh", dataPath: "accessToken" }),
      ],
      notes: ["identity alias skipped: no matching field"],
    });
    route({
      [`GET /api/workspaces/${WS_ID}/auth-preset`]: () => json(200, proposal),
    });
    renderInRouter(<AuthPresetPanel id={WS_ID} />);

    // A silent skip reads as "nothing to do" — notes must be on screen, not
    // just present in the fetched payload.
    expect(await screen.findByText(/identity alias skipped/)).toBeInTheDocument();
    expect(screen.getByTestId("auth-preset-sample-jwt")).toHaveTextContent(proposal.sampleJwt);

    const checkboxes = screen.getAllByRole("checkbox");
    expect(checkboxes).toHaveLength(2);
    for (const box of checkboxes) {
      expect(box).toBeChecked();
    }
    expect(screen.getByTestId("auth-preset-apply")).toHaveTextContent("Применить (2)");
  });

  it("handles a workspace with no spec without looking broken", async () => {
    const emptyProposal = authPresetProposalFixture({
      bindings: [],
      schemes: [],
      authPaths: [],
      notes: ["workspace has no spec attached; nothing to propose"],
    });
    route({
      [`GET /api/workspaces/${WS_ID}/auth-preset`]: () => json(200, emptyProposal),
    });
    renderInRouter(<AuthPresetPanel id={WS_ID} />);

    expect(await screen.findByTestId("auth-preset-empty")).toBeInTheDocument();
    expect(screen.getByText(/nothing to propose/)).toBeInTheDocument();
    // Nothing to approve means no apply control at all, rather than one that
    // would always be disabled.
    expect(screen.queryByTestId("auth-preset-apply")).not.toBeInTheDocument();
  });

  it("disables Apply once every binding is unchecked, and never sends an empty selection", async () => {
    const user = userEvent.setup();
    const proposal = authPresetProposalFixture({ bindings: [bindingFixture()] });
    const fetchMock = route({
      [`GET /api/workspaces/${WS_ID}/auth-preset`]: () => json(200, proposal),
    });
    renderInRouter(<AuthPresetPanel id={WS_ID} />);

    const checkbox = await screen.findByRole("checkbox");
    const applyButton = screen.getByTestId("auth-preset-apply");
    expect(applyButton).toBeEnabled();

    await user.click(checkbox);
    expect(applyButton).toBeDisabled();

    // Clicking a disabled Mantine Button fires no onClick — confirmed by
    // asserting the mutation never reaches fetch at all: only the preview
    // GET was ever called.
    await user.click(applyButton);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("applies only the checked subset and reports how many were applied", async () => {
    const user = userEvent.setup();
    const keep = bindingFixture({ method: "POST", path: "/auth/login", dataPath: "token" });
    const drop = bindingFixture({ method: "POST", path: "/auth/refresh", dataPath: "accessToken" });
    const proposal = authPresetProposalFixture({ bindings: [keep, drop] });
    let applyBody: unknown;
    route({
      [`GET /api/workspaces/${WS_ID}/auth-preset`]: () => json(200, proposal),
      [`POST /api/workspaces/${WS_ID}/auth-preset`]: () => json(200, { applied: 1, revision: 3 }),
    });
    // Reading the body requires intercepting fetch ourselves, since route()'s
    // handlers take no arguments — patch the stub after route() installs it.
    const original = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "POST") {
          applyBody = JSON.parse(String(init.body));
        }
        return original(input, init);
      }),
    );
    renderInRouter(<AuthPresetPanel id={WS_ID} />);

    const checkboxes = await screen.findAllByRole("checkbox");
    expect(checkboxes).toHaveLength(2);
    // Uncheck the second binding ("drop") — only "keep" should be applied.
    await user.click(checkboxes.at(1)!);
    await user.click(screen.getByTestId("auth-preset-apply"));

    expect(await screen.findByTestId("auth-preset-applied")).toHaveTextContent(
      "Применено привязок: 1",
    );
    // A3/D12: editVersions is sent back UNFILTERED — the exact map the
    // preceding GET returned, not narrowed to the selected subset (the
    // server itself scopes the check to the submitted bindings' opKeys;
    // preset_handlers.go's own comment is the citation this mirrors).
    expect(applyBody).toEqual({ bindings: [keep], editVersions: proposal.editVersions });
  });

  it("invalidates the workspace, operations list and open override docs after apply", async () => {
    const user = userEvent.setup();
    const proposal = authPresetProposalFixture({ bindings: [bindingFixture()] });
    route({
      [`GET /api/workspaces/${WS_ID}/auth-preset`]: () => json(200, proposal),
      [`POST /api/workspaces/${WS_ID}/auth-preset`]: () => json(200, { applied: 1, revision: 4 }),
    });
    const queryClient = makeQueryClient();
    // Seed a cache entry shaped like an open override doc, so the predicate
    // invalidation has something real to prove it matched — an assertion
    // against an empty cache would pass even if the predicate matched
    // nothing at all. gcTime is 0 in tests, so this must be read back before
    // the mutation runs rather than after (an inactive query with no
    // observer is collected almost immediately).
    const overrideDocKey = [`/api/workspaces/${WS_ID}/operations/GET%20%2Fpets`];
    queryClient.setQueryData(overrideDocKey, { stale: true });
    expect(queryClient.getQueryData(overrideDocKey)).toEqual({ stale: true });
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");

    renderInRouter(<AuthPresetPanel id={WS_ID} />, { queryClient });

    await user.click(await screen.findByTestId("auth-preset-apply"));
    await screen.findByTestId("auth-preset-applied");

    const invalidatedKeys = invalidateSpy.mock.calls
      .map((call) => (call[0] as { queryKey?: readonly unknown[] })?.queryKey?.[0])
      .filter((key): key is string => typeof key === "string");
    expect(invalidatedKeys).toContain(`/api/workspaces/${WS_ID}`);
    expect(invalidatedKeys).toContain(`/api/workspaces/${WS_ID}/operations`);

    // The predicate-only call is what reaches an override doc without the
    // caller knowing its opKey up front — proven directly against the same
    // key the seeded cache entry above used, rather than through cache state
    // that gcTime: 0 makes unreliable to read back after the fact.
    const predicateCall = invalidateSpy.mock.calls.find(
      (call) => typeof (call[0] as { predicate?: unknown })?.predicate === "function",
    );
    expect(predicateCall).toBeDefined();
    // Non-null assertion rather than an optional chain: the expect above has
    // already thrown if this is undefined, so `?.` would only be reachable in
    // a state the assertion rules out — and it would then throw a TypeError on
    // the property access anyway, replacing a readable failure with a stack.
    const predicate = (
      predicateCall![0] as { predicate: (q: { queryKey: readonly unknown[] }) => boolean }
    ).predicate;
    expect(predicate({ queryKey: overrideDocKey })).toBe(true);
    expect(predicate({ queryKey: [`/api/workspaces/${WS_ID}/operations`] })).toBe(false);
    expect(predicate({ queryKey: [`/api/workspaces/${WS_ID}`] })).toBe(false);
  });

  it("names how many notes there are, and does not render 412 of them flat", async () => {
    // The number is the real one: the customer's 130-operation document
    // produces 31 bindings and 412 notes. Before a dress rehearsal against it
    // this panel rendered every note as a flat list item, which buries the
    // rows the operator came to approve under a wall of yellow. The COUNT must
    // stay visible — a silent skip reads as "nothing to do" — while the detail
    // collapses.
    const notes = Array.from({ length: 412 }, (_, i) => `property ${i} skipped — not an auth path`);
    route({
      [`GET /api/workspaces/${WS_ID}/auth-preset`]: () =>
        json(200, authPresetProposalFixture({ notes })),
    });
    renderInRouter(<AuthPresetPanel id={WS_ID} />);

    const alert = await screen.findByTestId("auth-preset-notes");
    expect(alert).toHaveTextContent("Пропущено: 412");
    // The cap is an explicit slice, not a measured one, so it is observable
    // under jsdom: five items on screen out of 412, and a toggle that names
    // the real number. A flat render would put 412 <li> in the document.
    expect(within(alert).getAllByRole("listitem")).toHaveLength(5);
    const toggle = within(alert).getByTestId("auth-preset-notes-toggle");
    expect(toggle).toHaveTextContent("Показать все 412");

    await userEvent.setup().click(toggle);
    expect(within(alert).getAllByRole("listitem")).toHaveLength(412);
  });

  // A3/D12/property 7: the preset's conflict is NOT the four single-object
  // screens' "current document" affordance — its details carry staleVersions
  // (identities and numbers only), so the correct UI response is a LIST of
  // the opKeys that moved, plus a reload that re-fetches the proposal (the
  // only way this route's caller can get a fresh editVersions map, since the
  // conflict payload deliberately never carries the documents themselves).
  // Requiring "the current document" here — the vacuous, easier-to-write
  // affordance — is exactly what D10 rules out by name; this test would pass
  // a "reload" button that shows nothing about WHICH rows moved just as
  // happily as a correct one unless it also asserts the opKey text.
  it("shows the preset's staleVersions as a list of opKeys, not a document, and reloads on demand", async () => {
    const proposal = authPresetProposalFixture({
      bindings: [bindingFixture({ method: "POST", path: "/auth/login", dataPath: "token" })],
      editVersions: { "POST%20%2Fauth%2Flogin": 1 },
    });
    let getCount = 0;
    route({
      [`GET /api/workspaces/${WS_ID}/auth-preset`]: () => {
        getCount += 1;
        return json(200, proposal);
      },
      [`POST /api/workspaces/${WS_ID}/auth-preset`]: () =>
        json(409, {
          error: {
            code: "edit_conflict",
            message: "some rows moved",
            details: { staleVersions: { "POST%20%2Fauth%2Flogin": 4 } },
          },
        }),
    });
    renderInRouter(<AuthPresetPanel id={WS_ID} />);

    await userEvent.click(await screen.findByTestId("auth-preset-apply"));

    const conflict = await screen.findByTestId("auth-preset-conflict");
    expect(conflict).toHaveTextContent("Кто-то другой изменил это, пока вы редактировали");
    // The opKey must be shown decoded ("POST /auth/login"), not the raw
    // percent-encoded wire key — and the version that moved, both proving
    // this is the LIST affordance rather than a silently-swallowed 409.
    const stale = within(conflict).getByTestId("auth-preset-conflict-stale");
    expect(stale).toHaveTextContent("POST /auth/login");
    expect(stale).toHaveTextContent("4");

    expect(getCount).toBe(1);
    await userEvent.click(within(conflict).getByTestId("auth-preset-conflict-reload"));
    expect(getCount).toBe(2);
  });
});

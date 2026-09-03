import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { OperationEditor } from "./OperationEditor";
import { makeQueryClient, renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import {
  mergedStatusViewFixture,
  overrideDocViewFixture,
  recipeFixture,
  variantFixture,
} from "@/test/fixtures";
import {
  getGetOperationOverrideQueryKey,
  getListWorkspaceOperationsQueryKey,
} from "@/api/generated/operations/operations.ts";
import { getGetWorkspaceQueryKey } from "@/api/generated/workspaces/workspaces.ts";
import type { MergedStatusView, OverrideDocView } from "@/api/generated/schemas";

const WS = 7;
const OPKEY = "GET%20%2Fpets%2F%7BpetId%7D";
const GET = `GET /api/workspaces/${WS}/operations/${OPKEY}`;
const PUT = `PUT /api/workspaces/${WS}/operations/${OPKEY}`;
const DEL = `DELETE /api/workspaces/${WS}/operations/${OPKEY}`;

const STATUSES: MergedStatusView[] = [
  mergedStatusViewFixture({ selector: "200", httpStatus: 200, isDefault: true }),
  mergedStatusViewFixture({ selector: "404", httpStatus: 404, isDefault: false }),
];

// A COMPLETE document: several status keys, one carrying recipes,
// schemaPatch, headers, mediaType, bodyEncoding and a when[], plus
// operation-level listSize/delayMs/failDirective/validateReq — everything
// §3.3 says a naive rebuild-from-rendered-fields would silently drop.
function fullDoc(): OverrideDocView {
  return overrideDocViewFixture({
    opKey: OPKEY,
    overrideOn: true,
    routeOff: false,
    activeStatus: 200,
    responses: {
      "200": {
        mode: "pinned",
        when: [{ in: "query", name: "debug", op: "equals", value: "1" }],
        body: { ok: true, id: 42 },
        bodyEncoding: "",
        mediaType: "application/json",
        headers: { "x-trace-id": "abc123" },
        schemaPatch: [{ op: "replace", path: "/ok", value: false }],
        recipes: { token: recipeFixture({ kind: "jwt", field: "token" }) },
      },
      "404": { mode: "generated", mediaType: "application/json" },
    },
    listSize: { min: 2, max: 6 },
    delayMs: 250,
    failDirective: { kind: "network_error" },
    validateReq: true,
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("OperationEditor", () => {
  it("renders its outer marker before the override document answers", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

    expect(await screen.findByTestId("operation-editor")).toBeInTheDocument();
    expect(screen.getByText("Загрузка…")).toBeInTheDocument();
  });

  it("starts from an empty document on a 404, instead of showing an error", async () => {
    route({
      [GET]: () =>
        json(404, { error: { code: "not_found", message: "no override for this operation" } }),
    });
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

    // findByTestId, not getByTestId: fields === null for one tick after the
    // 404 settles (§3.3 — the seeding effect commits a render after the
    // query does), during which this content has not painted yet.
    expect(await screen.findByTestId("operation-override-on")).not.toBeChecked();
    expect(screen.getByTestId("operation-route-off")).not.toBeChecked();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it(
    "PUTs the whole document with only the ONE edited field changed — everything else, " +
      "including recipes, schemaPatch, headers and the un-rendered failDirective/validateReq, " +
      "survives byte-for-byte",
    async () => {
      const doc = fullDoc();
      let sentBody: unknown;
      route({
        [GET]: () => json(200, doc),
        [PUT]: () => {
          // captured below via a wrapping fetch, not here — route()'s
          // handlers take no arguments to read the request from.
          return json(200, { ...doc, delayMs: 999, revision: 9 });
        },
      });
      // Wrap the stub route() just installed so the PUT body can be
      // inspected: route()'s handlers are argument-less by design, so
      // capturing the request has to happen at the fetch layer itself.
      const inner = globalThis.fetch;
      vi.stubGlobal(
        "fetch",
        vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
          if (init?.method === "PUT" && init.body) {
            sentBody = JSON.parse(String(init.body));
          }
          return inner(input, init);
        }),
      );

      renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

      // findByTestId, not getByTestId: fields === null for one tick after
      // the GET settles, before which none of the content below exists yet.
      const delayInput = await screen.findByTestId("operation-delay-ms");
      // Edit EXACTLY ONE field: the operation-level delay.
      await userEvent.clear(delayInput);
      await userEvent.type(delayInput, "999");
      await userEvent.click(screen.getByTestId("operation-save"));

      await screen.findByTestId("operation-editor-saved");

      const expected = {
        overrideOn: doc.overrideOn,
        routeOff: doc.routeOff,
        activeStatus: doc.activeStatus,
        responses: doc.responses,
        listSize: doc.listSize,
        delayMs: 999,
        failDirective: doc.failDirective,
        validateReq: doc.validateReq,
        // A3: the expectation sent is the version behind the document this
        // screen's own GET returned, adopted at seed time and never
        // re-fetched at submit time.
        editVersion: doc.editVersion,
      };
      // One deep-equal against "the fetched document with only delayMs
      // changed" is what proves recipes, schemaPatch, headers, when[],
      // bodyEncoding on "200", the untouched "404" tab, and the two fields
      // this editor never even renders a control for (failDirective,
      // validateReq) all round-tripped exactly — a rebuild from only the
      // fields this screen shows would fail this assertion by dropping
      // every one of them.
      expect(sentBody).toEqual(expected);
    },
  );

  it("shows the server's own refusal message, not the generic translation", async () => {
    route({
      [GET]: () => json(200, fullDoc()),
      [PUT]: () =>
        json(400, {
          error: {
            code: "bad_request",
            message:
              'responses[200]: mediaType "text/html" is browser-executable and cannot be stored',
          },
        }),
    });
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

    await userEvent.click(await screen.findByTestId("operation-save"));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("browser-executable and cannot be stored");
  });

  // A3/property 7 (UI half) and property 8: an edit_conflict must reach this
  // screen carrying `details` (not parsed away in client.ts), the operator
  // must SEE a translation rather than the generic Russian fallback, the
  // "Загрузить актуальную версию" affordance must adopt the conflict's own
  // document AND its editVersion (not re-fetch), and the retry that follows
  // must carry that new editVersion — never the stale one that just failed.
  // A DeadSiblingCAS-shaped bug (details silently dropped, or the reload
  // button doing nothing, or the retry resending the old token) fails this
  // test and no other bar in this tree would catch it.
  it("shows the edit_conflict affordance, adopts the conflict's document and version, and retries with the new one", async () => {
    const doc = fullDoc();
    const conflictDoc = {
      overrideOn: false,
      routeOff: false,
      activeStatus: 404,
      responses: doc.responses,
      listSize: doc.listSize,
      delayMs: 7,
      failDirective: doc.failDirective,
      validateReq: doc.validateReq,
      editVersion: 99,
    };
    let putCount = 0;
    let lastPutBody: { editVersion?: number } | undefined;
    route({
      [GET]: () => json(200, doc),
      [PUT]: () => {
        putCount += 1;
        if (putCount === 1) {
          return json(409, {
            error: {
              code: "edit_conflict",
              message: "editVersion no longer matches",
              details: conflictDoc,
            },
          });
        }
        return json(200, { ...conflictDoc, revision: 5 });
      },
    });
    const original = globalThis.fetch;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "PUT" && init.body) {
          lastPutBody = JSON.parse(String(init.body)) as { editVersion?: number };
        }
        return original(input, init);
      }),
    );
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

    await userEvent.click(await screen.findByTestId("operation-save"));
    expect(lastPutBody?.editVersion).toBe(doc.editVersion);

    const conflictAlert = await screen.findByTestId("operation-edit-conflict");
    // The translation (errors.ts) must be visible, not the generic fallback.
    expect(conflictAlert).toHaveTextContent("Кто-то другой изменил это, пока вы редактировали");

    await userEvent.click(within(conflictAlert).getByTestId("operation-conflict-reload"));

    // The form adopted the conflict's own document without a second GET —
    // only one GET ever happened (the initial mount's).
    await waitFor(() => {
      expect(screen.getByTestId("operation-active-status")).toHaveValue("404");
    });

    await userEvent.click(screen.getByTestId("operation-save"));
    await screen.findByTestId("operation-editor-saved");

    // The retry sent the CONFLICT's editVersion (99), never the original
    // stale one (doc.editVersion) and never a value fetched fresh at submit
    // time — this is D10's "the vacuous implementation adds a request, the
    // correct one adds a field" pinned as a runnable assertion.
    expect(lastPutBody?.editVersion).toBe(99);
    expect(putCount).toBe(2);
  });

  it("invalidates the workspace, the operations list and this override doc on save", async () => {
    const doc = fullDoc();
    route({
      [GET]: () => json(200, doc),
      [PUT]: () => json(200, { ...doc, revision: 3 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />, {
      queryClient,
    });

    await userEvent.click(await screen.findByTestId("operation-save"));
    await screen.findByTestId("operation-editor-saved");

    const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
    expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    expect(keys).toContainEqual(getListWorkspaceOperationsQueryKey(WS));
    expect(keys).toContainEqual(getGetOperationOverrideQueryKey(WS, OPKEY));
  });

  it("treats a 204 reset (nothing to remove) as success, not a crash", async () => {
    route({
      [GET]: () => json(200, fullDoc()),
      [DEL]: () => new Response(null, { status: 204 }),
    });
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

    await userEvent.click(await screen.findByTestId("operation-reset"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("operation-reset-confirm"));

    // customFetch (§3.1) leaves `data` undefined on a 204 — reading
    // res.data.revision unconditionally would throw here, taking the whole
    // component down instead of rendering this message.
    expect(await screen.findByTestId("operation-editor-saved")).toHaveTextContent(
      "Сбрасывать было нечего",
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("reports the moved revision on a 200 reset, and invalidates the same three keys", async () => {
    route({
      [GET]: () => json(200, fullDoc()),
      [DEL]: () => json(200, { revision: 11 }),
    });
    const queryClient = makeQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />, {
      queryClient,
    });

    await userEvent.click(await screen.findByTestId("operation-reset"));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByTestId("operation-reset-confirm"));

    expect(await screen.findByTestId("operation-editor-saved")).toHaveTextContent("11");
    const keys = invalidateSpy.mock.calls.map((call) => call[0]?.queryKey);
    expect(keys).toContainEqual(getGetWorkspaceQueryKey(WS));
    expect(keys).toContainEqual(getListWorkspaceOperationsQueryKey(WS));
    expect(keys).toContainEqual(getGetOperationOverrideQueryKey(WS, OPKEY));
  });

  it("shows recipes read-only, with no control that edits them", async () => {
    route({ [GET]: () => json(200, fullDoc()) });
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

    await screen.findByTestId("operation-editor");
    // "200" is the default active tab (isDefault: true on the fixture).
    const recipes = await screen.findByTestId("operation-status-recipes-200");
    expect(recipes).toHaveTextContent("token: jwt");
    // No input, textarea or button inside the recipes card — only the
    // status's own body/mediaType controls, elsewhere in the panel, are
    // editable.
    expect(within(recipes).queryByRole("textbox")).not.toBeInTheDocument();
    expect(within(recipes).queryByRole("button")).not.toBeInTheDocument();
  });

  it("does not render a control for failDirective or validateReq", async () => {
    route({ [GET]: () => json(200, fullDoc()) });
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

    await screen.findByTestId("operation-save");
    expect(screen.queryByText(/failDirective/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/validate/i)).not.toBeInTheDocument();
  });

  it(
    "PUTs one status's edited body while that SAME status's own recipes, headers, " +
      "schemaPatch, mediaType and when survive — the other regression shape from §3.3: " +
      "rebuilding a Variant inside updateVariant/StatusPanel from only the fields this " +
      "editor renders, rather than rebuilding the whole document at save time",
    async () => {
      const doc = fullDoc();
      let sentBody: unknown;
      route({
        [GET]: () => json(200, doc),
        [PUT]: () => json(200, { ...doc, revision: 9 }),
      });
      const inner = globalThis.fetch;
      vi.stubGlobal(
        "fetch",
        vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
          if (init?.method === "PUT" && init.body) {
            sentBody = JSON.parse(String(init.body));
          }
          return inner(input, init);
        }),
      );

      renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

      // "200" is the default active tab (isDefault: true on the fixture) and
      // is pinned, carrying recipes/headers/schemaPatch/mediaType/when.
      const bodyInput = await screen.findByTestId("operation-status-body-200");
      await userEvent.clear(bodyInput);
      await userEvent.click(bodyInput);
      // paste, not type: the new body is valid JSON and userEvent.type would
      // otherwise parse its braces as special-key syntax.
      await userEvent.paste('{"ok":true,"id":99}');
      await userEvent.click(screen.getByTestId("operation-save"));

      await screen.findByTestId("operation-editor-saved");

      const sentResponses = (sentBody as { responses: Record<string, unknown> }).responses;
      expect(sentResponses["200"]).toEqual({
        ...doc.responses["200"],
        body: { ok: true, id: 99 },
      });
      // The untouched "404" tab round-trips exactly, same as the
      // operation-level test above.
      expect(sentResponses["404"]).toEqual(doc.responses["404"]);
    },
  );

  it(
    "offers a status pinned only by traffic/session as a selectable active status, even " +
      "though the spec (STATUSES) never declares it, and pre-selects it when the document " +
      "already stores it",
    async () => {
      const doc = overrideDocViewFixture({
        opKey: OPKEY,
        overrideOn: true,
        activeStatus: 500,
        responses: {
          "200": variantFixture(),
          "500": variantFixture({ mode: "pinned", body: { error: "boom" } }),
        },
      });
      route({ [GET]: () => json(200, doc) });
      renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

      const select = (await screen.findByTestId("operation-active-status")) as HTMLSelectElement;
      // Not "не задан" — the document itself names 500 as the active status,
      // so the select must show it selected, not fall back silently.
      expect(select.value).toBe("500");
      expect(within(select).getByRole("option", { name: "500" })).toBeInTheDocument();
    },
  );

  it("disables «Сохранить» while any status's body textarea holds invalid JSON", async () => {
    route({ [GET]: () => json(200, fullDoc()) });
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

    const bodyInput = await screen.findByTestId("operation-status-body-200");
    const saveButton = screen.getByTestId("operation-save");
    expect(saveButton).toBeEnabled();

    await userEvent.type(bodyInput, "not valid json");

    expect(await screen.findByText(/JSON невалиден/)).toBeInTheDocument();
    expect(saveButton).toBeDisabled();
  });

  it(
    "does not show a stale body draft after «Сбросить к спеке» — the panel is not " +
      "remounted (same selector key), so the draft must be cleared explicitly",
    async () => {
      route({
        [GET]: () => json(200, fullDoc()),
        [DEL]: () => new Response(null, { status: 204 }),
      });
      renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />);

      const bodyInput = await screen.findByTestId("operation-status-body-200");
      await userEvent.clear(bodyInput);
      await userEvent.click(bodyInput);
      await userEvent.paste('{"ok":true,"id":777}');
      // The draft holds the operator's own raw text verbatim, not a
      // reformatted echo of it.
      expect(bodyInput).toHaveValue('{"ok":true,"id":777}');

      await userEvent.click(screen.getByTestId("operation-reset"));
      const dialog = await screen.findByRole("dialog");
      await userEvent.click(within(dialog).getByTestId("operation-reset-confirm"));
      await screen.findByTestId("operation-editor-saved");

      // The reset cleared responses to {}, so "200" now renders in
      // "generated" mode — switch it back to "pinned" to bring the body
      // textarea back without remounting StatusPanel.
      await userEvent.selectOptions(screen.getByTestId("operation-status-mode-200"), "pinned");
      const freshBodyInput = await screen.findByTestId("operation-status-body-200");
      // The pre-reset draft (id: 777) must be gone — this is what would
      // silently stay behind without the reconciliation effect.
      expect(freshBodyInput).toHaveValue("{}");
    },
  );

  it("keeps a local edit when the override document is refetched underneath it", async () => {
    // The invariant this pins used to be enforced by a "run once per mount"
    // effect that seeded state from the query and then refused to fire again.
    // The document is now DERIVED during render — local edits if there are
    // any, the server's answer otherwise — so nothing structural stops a
    // refetch from overwriting the screen. This is the test that would go red
    // if it did, and the component's own save invalidates this exact query,
    // so the sequence below is what happens on every save.
    let served = 0;
    route({
      [GET]: () => {
        served += 1;
        // The second answer differs in the field edited below, so a screen
        // that followed the server would be visibly wrong rather than
        // coincidentally right.
        return json(200, { ...fullDoc(), delayMs: served === 1 ? 250 : 777 });
      },
    });
    const queryClient = makeQueryClient();
    renderInRouter(<OperationEditor workspaceId={WS} opKey={OPKEY} statuses={STATUSES} />, {
      queryClient,
    });

    const delayInput = await screen.findByTestId("operation-delay-ms");
    expect(delayInput).toHaveValue("250");

    await userEvent.clear(delayInput);
    await userEvent.type(delayInput, "999");
    expect(delayInput).toHaveValue("999");

    await queryClient.invalidateQueries({
      queryKey: getGetOperationOverrideQueryKey(WS, OPKEY),
    });
    // Wait for the refetch to actually land — asserting before it does would
    // pass against a component that overwrites the edit a tick later.
    await waitFor(() => expect(served).toBe(2));

    expect(screen.getByTestId("operation-delay-ms")).toHaveValue("999");
  });
});

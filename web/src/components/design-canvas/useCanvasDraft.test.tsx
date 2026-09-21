import { useEffect, useState } from "react";
import {
  Link,
  RouterProvider,
  createBrowserHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { act, fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderInRouter, renderWithProviders } from "@/test/render";
import { exampleCanvas } from "./canvasModel";
import { useCanvasDraft, type CanvasDraftController } from "./useCanvasDraft";
import type { CanvasDocument } from "./types";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function linkedDocument(): CanvasDocument {
  const document = exampleCanvas();
  document.title = "Original";
  document.contracts[0] = {
    ...document.contracts[0]!,
    mode: "linked",
    source: { designId: 7, revisionId: 11, version: 1 },
  };
  return document;
}

function canonicalDocument(submitted: CanvasDocument): CanvasDocument {
  const canonical = structuredClone(submitted);
  canonical.contracts[0]!.source = { designId: 7, revisionId: 12, version: 2 };
  return canonical;
}

async function renderDraft(document = linkedDocument(), formDrafts = "{}", browserHistory = false) {
  let current: CanvasDraftController;
  let setPendingWrite: (pending: boolean) => void;
  let setShortcutsEnabled: (enabled: boolean) => void;
  function DraftHarness() {
    const [pendingWrite, setPending] = useState(false);
    const [shortcutsEnabled, setShortcuts] = useState(true);
    const draft = useCanvasDraft(
      { document, formDrafts: { all: formDrafts }, saved: true, error: "" },
      { pendingWrite, shortcutsEnabled },
    );
    useEffect(() => {
      current = draft;
      setPendingWrite = setPending;
      setShortcutsEnabled = setShortcuts;
    }, [draft]);
    return (
      <>
        <output>{draft.document.title}</output>
        <input aria-label="Dialog text" defaultValue="Native undo" />
        <Link to={"/elsewhere" as never}>Leave canvas</Link>
      </>
    );
  }
  let destroyHistory = () => {};
  if (browserHistory) {
    const history = createBrowserHistory();
    const rootRoute = createRootRoute();
    const canvasRoute = createRoute({
      getParentRoute: () => rootRoute,
      path: "/",
      component: DraftHarness,
    });
    const otherRoute = createRoute({
      getParentRoute: () => rootRoute,
      path: "/elsewhere",
      component: () => <div data-testid="test-router-elsewhere" />,
    });
    const router = createRouter({
      routeTree: rootRoute.addChildren([canvasRoute, otherRoute]),
      history,
    });
    renderWithProviders(<RouterProvider router={router as never} />);
    destroyHistory = () => history.destroy();
  } else {
    renderInRouter(<DraftHarness />);
  }
  await screen.findByText(document.title);
  return {
    get draft() {
      return current!;
    },
    setPendingWrite: (pending: boolean) => act(() => setPendingWrite(pending)),
    setShortcutsEnabled: (enabled: boolean) => act(() => setShortcutsEnabled(enabled)),
    destroyHistory,
  };
}

describe("useCanvasDraft server acknowledgement", () => {
  it("suspends canvas undo and redo shortcuts without intercepting native text undo", async () => {
    const editor = await renderDraft();
    act(() => editor.draft.update((document) => ({ ...document, title: "First edit" })));
    act(() => editor.draft.update((document) => ({ ...document, title: "Second edit" })));
    act(() => editor.draft.undo());
    editor.setShortcutsEnabled(false);

    for (const modifier of [{ metaKey: true }, { ctrlKey: true }]) {
      for (const shiftKey of [false, true]) {
        fireEvent.keyDown(window, { key: "z", shiftKey, ...modifier });
        expect(editor.draft.document.title).toBe("First edit");
        expect(
          fireEvent.keyDown(screen.getByRole("textbox"), {
            key: "z",
            shiftKey,
            ...modifier,
          }),
        ).toBe(true);
      }
    }

    editor.setShortcutsEnabled(true);
    fireEvent.keyDown(window, { key: "z", ctrlKey: true, shiftKey: true });
    expect(editor.draft.document.title).toBe("Second edit");
    fireEvent.keyDown(window, { key: "z", metaKey: true });
    expect(editor.draft.document.title).toBe("First edit");
    expect(fireEvent.keyDown(screen.getByRole("textbox"), { key: "z", ctrlKey: true })).toBe(true);
    expect(editor.draft.document.title).toBe("First edit");
  });

  it("preserves undo and redo history while adopting server-assigned contract pins", async () => {
    const editor = await renderDraft();
    act(() => editor.draft.update((document) => ({ ...document, title: "Submitted" })));
    const submitted = editor.draft.document;
    act(() => editor.draft.update((document) => ({ ...document, title: "Later" })));
    act(() => editor.draft.undo());

    act(() =>
      editor.draft.acknowledgeServerSave(submitted, canonicalDocument(submitted), "{}", "{}"),
    );

    expect(editor.draft.saved).toBe(true);
    expect(editor.draft.canUndo).toBe(true);
    expect(editor.draft.canRedo).toBe(true);
    act(() => editor.draft.redo());
    expect(editor.draft.document.title).toBe("Later");
    expect(editor.draft.document.contracts[0]!.source).toEqual({
      designId: 7,
      revisionId: 12,
      version: 2,
    });
    expect(editor.draft.dirty).toBe(true);
    act(() => editor.draft.undo());
    expect(editor.draft.document.title).toBe("Submitted");
    expect(editor.draft.saved).toBe(true);
    act(() => editor.draft.undo());
    expect(editor.draft.document.title).toBe("Original");
    expect(editor.draft.document.contracts[0]!.source?.revisionId).toBe(12);
    expect(editor.draft.dirty).toBe(true);
  });

  it("keeps an undo to the old baseline dirty after the pending save succeeds", async () => {
    const editor = await renderDraft();
    act(() => editor.draft.update((document) => ({ ...document, title: "Submitted" })));
    const submitted = editor.draft.document;
    act(() => editor.draft.undo());
    expect(editor.draft.dirty).toBe(false);

    act(() =>
      editor.draft.acknowledgeServerSave(submitted, canonicalDocument(submitted), "{}", "{}"),
    );

    expect(editor.draft.document.title).toBe("Original");
    expect(editor.draft.document.contracts[0]!.source?.revisionId).toBe(12);
    expect(editor.draft.dirty).toBe(true);
    act(() => editor.draft.redo());
    expect(editor.draft.document.title).toBe("Submitted");
    expect(editor.draft.saved).toBe(true);
  });

  it("keeps edits and unfinished API buffers made while a save is pending", async () => {
    const editor = await renderDraft();
    act(() => editor.draft.update((document) => ({ ...document, title: "Submitted" })));
    const submitted = editor.draft.document;
    const buffer = { source: "{unfinished", propertySource: "", error: "Invalid JSON" };
    act(() => {
      editor.draft.update((document) => ({ ...document, title: "Later" }));
      editor.draft.formStore.set("/canvas-contract/api/schema", buffer);
    });

    act(() =>
      editor.draft.acknowledgeServerSave(submitted, canonicalDocument(submitted), "{}", "{}"),
    );

    expect(editor.draft.document.title).toBe("Later");
    expect(editor.draft.document.contracts[0]!.source?.revisionId).toBe(12);
    expect(editor.draft.formStore.get("/canvas-contract/api/schema")).toBe(buffer);
    expect(editor.draft.dirty).toBe(true);
    expect(editor.draft.pendingForms).toBe(true);
  });

  it("acknowledges saved API buffers without replacing their field state", async () => {
    const editor = await renderDraft();
    const buffer = { source: "{unfinished", propertySource: "", error: "Invalid JSON" };
    act(() => editor.draft.formStore.set("/canvas-contract/api/schema", buffer));
    const submittedForms = editor.draft.serializeFormDrafts();
    const submitted = editor.draft.document;

    act(() =>
      editor.draft.acknowledgeServerSave(submitted, submitted, submittedForms, submittedForms),
    );

    expect(editor.draft.formStore.get("/canvas-contract/api/schema")).toBe(buffer);
    expect(editor.draft.saved).toBe(true);
    expect(editor.draft.pendingForms).toBe(true);
  });

  it("adopts canonical API buffers only if their submitted contents remain current", async () => {
    const editor = await renderDraft();
    const canonicalBuffer = JSON.stringify({
      "/canvas-contract/api/schema": { source: "{server", propertySource: "" },
    });
    const submitted = editor.draft.document;

    act(() => editor.draft.acknowledgeServerSave(submitted, submitted, "{}", canonicalBuffer));

    expect(editor.draft.formStore.get("/canvas-contract/api/schema")?.source).toBe("{server");
    expect(editor.draft.saved).toBe(true);
  });

  it("blocks navigation and beforeunload while a clean draft has a pending write", async () => {
    const editor = await renderDraft(linkedDocument(), "{}", true);
    const confirm = vi.fn(() => false);
    vi.stubGlobal("confirm", confirm);
    editor.setPendingWrite(true);

    const pendingUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(pendingUnload);
    expect(pendingUnload.defaultPrevented).toBe(true);
    await userEvent.click(screen.getByRole("link", { name: "Leave canvas" }));
    expect(screen.getByRole("status")).toHaveTextContent("Original");
    expect(screen.queryByTestId("test-router-elsewhere")).not.toBeInTheDocument();
    expect(confirm).toHaveBeenCalledOnce();

    editor.setPendingWrite(false);
    const settledUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(settledUnload);
    expect(settledUnload.defaultPrevented).toBe(false);
    await userEvent.click(screen.getByRole("link", { name: "Leave canvas" }));
    expect(await screen.findByTestId("test-router-elsewhere")).toBeInTheDocument();
    expect(confirm).toHaveBeenCalledOnce();
    editor.destroyHistory();
    window.history.replaceState(null, "", "/");
  });
});

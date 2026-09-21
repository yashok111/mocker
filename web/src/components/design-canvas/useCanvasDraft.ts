import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { useBlocker } from "@tanstack/react-router";
import { createFormDraftStore, type FormDraftStore } from "../api-designer/forms/formDraftStore";
import { exampleCanvas } from "./canvasModel";
import { rebaseCanvasDocument } from "./canvasRebase";
import { parseSavedCanvas, serializeSavedCanvas, STORAGE_KEY } from "./canvasStorage";
import type { CanvasDocument } from "./types";

export interface CanvasDraftInitial {
  document: CanvasDocument;
  formDrafts: Record<string, string>;
  baseline?: {
    document: CanvasDocument;
    formDrafts: Record<string, string>;
  };
  saved: boolean;
  error: string;
}

export interface CanvasDraftOptions {
  pendingWrite?: boolean;
  shortcutsEnabled?: boolean;
}

function readInitial(): CanvasDraftInitial {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved !== null) return { ...parseSavedCanvas(saved), saved: true, error: "" };
  } catch {
    return {
      document: exampleCanvas(),
      formDrafts: {},
      saved: false,
      error:
        "Не удалось прочитать сохранённый сценарий. Исходная запись остаётся в браузере до нового сохранения.",
    };
  }
  return { document: exampleCanvas(), formDrafts: {}, saved: false, error: "" };
}

export function scopeFormDrafts(store: FormDraftStore, id: string): FormDraftStore {
  const prefix = `/canvas-contract/${id}`;
  return {
    ...store,
    get: (pointer) => store.get(prefix + pointer),
    set: (pointer, draft) => store.set(prefix + pointer, draft),
    remove: (pointer) => store.remove(prefix + pointer),
    removeTree: (pointer) => store.removeTree(prefix + pointer),
    moveTree: (from, to) => store.moveTree(prefix + from, prefix + to),
    clear: () => store.removeTree(prefix),
  };
}

export function useCanvasDraft(
  initialOverride?: CanvasDraftInitial,
  { pendingWrite = false, shortcutsEnabled = true }: CanvasDraftOptions = {},
) {
  const [initial] = useState(() => initialOverride ?? readInitial());
  const [history, setHistory] = useState<{
    past: CanvasDocument[];
    present: CanvasDocument;
    future: CanvasDocument[];
  }>({ past: [], present: initial.document, future: [] });
  const [formStore] = useState(() => {
    const store = createFormDraftStore();
    const saved = (initial.formDrafts as Record<string, string>).all;
    if (saved) store.hydrate(saved);
    return store;
  });
  const formState = useSyncExternalStore(
    formStore.subscribe,
    formStore.getSnapshot,
    formStore.getSnapshot,
  );
  const fingerprint = JSON.stringify([history.present, formStore.serialize()]);
  const [savedFingerprint, setSavedFingerprint] = useState(() =>
    initial.baseline
      ? JSON.stringify([initial.baseline.document, initial.baseline.formDrafts.all ?? "{}"])
      : fingerprint,
  );
  const [hasSaved, setHasSaved] = useState(initial.saved);
  const [error, setError] = useState(initial.error);
  const dirty = fingerprint !== savedFingerprint;
  const shouldProtectNavigation = dirty || pendingWrite;

  useBlocker({
    shouldBlockFn: () =>
      shouldProtectNavigation &&
      !window.confirm("В сценарии есть несохранённые изменения. Покинуть канвас?"),
    enableBeforeUnload: shouldProtectNavigation,
    withResolver: false,
  });

  const update = useCallback(
    (change: CanvasDocument | ((document: CanvasDocument) => CanvasDocument)) => {
      setHistory((state) => {
        const next = typeof change === "function" ? change(state.present) : change;
        if (next === state.present || JSON.stringify(next) === JSON.stringify(state.present))
          return state;
        return { past: [...state.past.slice(-59), state.present], present: next, future: [] };
      });
    },
    [],
  );

  const undo = useCallback(() => {
    if (formStore.getSnapshot().dirty) return;
    setHistory((state) => {
      const previous = state.past.at(-1);
      return previous === undefined
        ? state
        : {
            past: state.past.slice(0, -1),
            present: previous,
            future: [state.present, ...state.future],
          };
    });
  }, [formStore]);
  const redo = useCallback(() => {
    if (formStore.getSnapshot().dirty) return;
    setHistory((state) => {
      const next = state.future[0];
      return next === undefined
        ? state
        : { past: [...state.past, state.present], present: next, future: state.future.slice(1) };
    });
  }, [formStore]);

  useEffect(() => {
    if (!shortcutsEnabled) return;
    const handle = (event: KeyboardEvent) => {
      const target = event.target;
      if (
        target instanceof HTMLElement &&
        target.closest("input,textarea,select,[contenteditable=true]")
      )
        return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "z") {
        event.preventDefault();
        if (event.shiftKey) redo();
        else undo();
      }
    };
    window.addEventListener("keydown", handle);
    return () => window.removeEventListener("keydown", handle);
  }, [undo, redo, shortcutsEnabled]);

  const save = () => {
    try {
      localStorage.setItem(
        STORAGE_KEY,
        serializeSavedCanvas(history.present, { all: formStore.serialize() }),
      );
      setSavedFingerprint(fingerprint);
      setHasSaved(true);
      setError("");
    } catch {
      setHasSaved(false);
      setError(
        "Не удалось сохранить сценарий в браузере. Проверьте доступность и свободное место локального хранилища.",
      );
    }
  };

  const markSavedFingerprint = useCallback((nextFingerprint: string) => {
    setSavedFingerprint(nextFingerprint);
    setHasSaved(true);
    setError("");
  }, []);

  const acknowledgeServerSave = useCallback(
    (
      submitted: CanvasDocument,
      canonical: CanvasDocument,
      submittedFormDrafts: string,
      canonicalFormDrafts: string,
    ) => {
      const currentFormDrafts = formStore.serialize();
      if (currentFormDrafts === submittedFormDrafts && currentFormDrafts !== canonicalFormDrafts) {
        formStore.hydrate(canonicalFormDrafts);
      }
      // A save is not an edit: keep both sides of history while advancing API pins.
      setHistory((state) => ({
        past: state.past.map((document) => rebaseCanvasDocument(submitted, document, canonical)),
        present: rebaseCanvasDocument(submitted, state.present, canonical),
        future: state.future.map((document) =>
          rebaseCanvasDocument(submitted, document, canonical),
        ),
      }));
      setSavedFingerprint(JSON.stringify([canonical, canonicalFormDrafts]));
      setHasSaved(true);
      setError("");
    },
    [formStore],
  );

  const replaceFromServer = useCallback(
    (document: CanvasDocument, serializedDrafts: string) => {
      formStore.clear();
      formStore.hydrate(serializedDrafts);
      setHistory({ past: [], present: document, future: [] });
      setSavedFingerprint(JSON.stringify([document, serializedDrafts]));
      setHasSaved(true);
      setError("");
    },
    [formStore],
  );

  const rebaseFromServer = useCallback(
    (
      document: CanvasDocument,
      serializedDrafts: string,
      baselineDocument: CanvasDocument,
      baselineSerializedDrafts: string,
    ) => {
      formStore.clear();
      formStore.hydrate(serializedDrafts);
      setHistory({ past: [], present: document, future: [] });
      setSavedFingerprint(JSON.stringify([baselineDocument, baselineSerializedDrafts]));
      setHasSaved(true);
      setError("");
    },
    [formStore],
  );

  const reset = (document: CanvasDocument) => {
    if (
      (dirty || formState.dirty) &&
      !window.confirm("Заменить текущий сценарий? Несохранённые изменения будут потеряны.")
    )
      return false;
    formStore.clear();
    setHistory({ past: [], present: document, future: [] });
    setError("");
    return true;
  };

  return {
    document: history.present,
    update,
    undo,
    redo,
    reset,
    save,
    formStore,
    canUndo: history.past.length > 0 && !formState.dirty,
    canRedo: history.future.length > 0 && !formState.dirty,
    pendingForms: formState.dirty,
    dirty,
    error,
    setError,
    saved: hasSaved && !dirty,
    fingerprint,
    serializeFormDrafts: formStore.serialize,
    markSavedFingerprint,
    acknowledgeServerSave,
    replaceFromServer,
    rebaseFromServer,
  };
}

export type CanvasDraftController = ReturnType<typeof useCanvasDraft>;

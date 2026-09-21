export interface FormFieldDraft {
  source: string;
  propertySource: string;
  error?: string;
}

export interface FormDraftSnapshot {
  dirty: boolean;
  invalid: boolean;
}

export interface FormDraftStore {
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => FormDraftSnapshot;
  get: (pointer: string) => FormFieldDraft | undefined;
  set: (pointer: string, draft: FormFieldDraft) => void;
  remove: (pointer: string) => void;
  removeTree: (pointer: string) => void;
  moveTree: (from: string, to: string) => void;
  clear: () => void;
  serialize: () => string;
  hydrate: (serialized: string) => void;
}

// Kept above field/form lifetimes. Pending buffers are independent of the last
// valid document and can therefore survive selection changes and server polls.
export function createFormDraftStore(): FormDraftStore {
  const drafts = new Map<string, FormFieldDraft>();
  const listeners = new Set<() => void>();
  let invalidSerialized: string | null = null;
  let snapshot: FormDraftSnapshot = { dirty: false, invalid: false };
  const notify = () => {
    snapshot = {
      dirty: drafts.size > 0 || invalidSerialized !== null,
      invalid:
        invalidSerialized !== null ||
        [...drafts.values()].some((draft) => draft.error !== undefined),
    };
    for (const listener of listeners) listener();
  };
  return {
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getSnapshot: () => snapshot,
    get: (pointer) => drafts.get(pointer),
    set(pointer, draft) {
      invalidSerialized = null;
      drafts.set(pointer, draft);
      notify();
    },
    remove(pointer) {
      if (drafts.delete(pointer)) notify();
    },
    removeTree(pointer) {
      let changed = false;
      for (const key of drafts.keys()) {
        if (key === pointer || key.startsWith(`${pointer}/`)) {
          drafts.delete(key);
          changed = true;
        }
      }
      if (changed) notify();
    },
    moveTree(from, to) {
      if (from === to) return;
      const moved = [...drafts].filter(([key]) => key === from || key.startsWith(`${from}/`));
      for (const [key] of moved) drafts.delete(key);
      for (const [key, draft] of moved) drafts.set(`${to}${key.slice(from.length)}`, draft);
      if (moved.length > 0) notify();
    },
    clear() {
      if (drafts.size === 0 && invalidSerialized === null) return;
      drafts.clear();
      invalidSerialized = null;
      notify();
    },
    serialize: () => invalidSerialized ?? JSON.stringify(Object.fromEntries(drafts)),
    hydrate(serialized) {
      let parsed: unknown;
      try {
        parsed = JSON.parse(serialized);
      } catch {
        drafts.clear();
        invalidSerialized = serialized;
        notify();
        return;
      }
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        drafts.clear();
        invalidSerialized = serialized;
        notify();
        return;
      }
      const entries = Object.entries(parsed);
      if (
        entries.some(([, value]) => {
          if (typeof value !== "object" || value === null || Array.isArray(value)) return true;
          const draft = value as Record<string, unknown>;
          return typeof draft.source !== "string" || typeof draft.propertySource !== "string";
        })
      ) {
        drafts.clear();
        invalidSerialized = serialized;
        notify();
        return;
      }
      drafts.clear();
      invalidSerialized = null;
      for (const [pointer, value] of entries) {
        const draft = value as Record<string, unknown>;
        drafts.set(pointer, {
          source: draft.source as string,
          propertySource: draft.propertySource as string,
          ...(typeof draft.error === "string" ? { error: draft.error } : {}),
        });
      }
      notify();
    },
  };
}

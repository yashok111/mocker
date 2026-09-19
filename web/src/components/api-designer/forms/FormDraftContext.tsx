import { createContext, useContext, useState, useSyncExternalStore } from "react";
import type { ReactNode } from "react";
import { createFormDraftStore, type FormDraftStore } from "./formDraftStore";

const FormDraftContext = createContext<FormDraftStore | undefined>(undefined);

export function FormDraftProvider({
  store,
  children,
}: {
  store: FormDraftStore;
  children: ReactNode;
}) {
  return <FormDraftContext value={store}>{children}</FormDraftContext>;
}

export function useFormDraftStore() {
  const context = useContext(FormDraftContext);
  const [fallback] = useState(createFormDraftStore);
  return context ?? fallback;
}

export function useFormFieldDraft(pointer: string) {
  const store = useFormDraftStore();
  useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
  return { store, draft: store.get(pointer) };
}

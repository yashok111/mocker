import { useRef, useState } from "react";
import {
  discoverChangeCreateRecovery,
  inspectChangeRecovery,
  writeChangeRecovery,
  type ChangeAttempt,
  type ChangeRecoverySlot,
} from "./backendChangeRecovery";

export function useBackendChangeRecovery(key: string, createProjectId?: string) {
  const discover = () =>
    (createProjectId
      ? discoverChangeCreateRecovery(createProjectId)
      : [inspectChangeRecovery(key)]
    ).map((slot) => {
      if (
        slot.attempt &&
        (createProjectId ? slot.attempt.kind !== "create" : slot.attempt.kind === "create")
      )
        return {
          ...slot,
          attempt: null,
          error: new Error("Операция в записи восстановления не соответствует этому редактору."),
        };
      return slot;
    });
  const [initial] = useState(discover);
  const [options, setOptions] = useState(initial);
  const [slot, setSlotState] = useState<ChangeRecoverySlot>(() =>
    initial.length === 1
      ? initial[0]!
      : {
          key,
          raw: null,
          attempt: null,
          error: new Error("Найдено несколько незавершённых созданий. Выберите исходный запрос."),
        },
  );
  const state = useRef(slot);
  const [cleanupPending, setCleanupPending] = useState(false);
  const [unsent, setUnsent] = useState(false);
  function adopt(next: ChangeRecoverySlot) {
    state.current = next;
    setSlotState(next);
  }
  function persist(attempt: ChangeAttempt): boolean {
    const previous = state.current;
    try {
      const raw = writeChangeRecovery(previous.key, attempt, previous.raw);
      adopt({ ...previous, raw, attempt, error: null });
      setUnsent(false);
      return true;
    } catch (cause) {
      adopt({
        ...previous,
        attempt,
        error: cause instanceof Error ? cause : new Error("Не удалось сохранить восстановление"),
      });
      setUnsent(previous.raw === null);
      return false;
    }
  }
  function clear(confirmed = false): boolean {
    const previous = state.current;
    try {
      writeChangeRecovery(previous.key, null, previous.raw);
      const next = discover();
      setOptions(next);
      adopt(
        next.length === 1
          ? next[0]!
          : {
              key,
              raw: null,
              attempt: null,
              error: new Error("Выберите оставшийся запрос восстановления."),
            },
      );
      setCleanupPending(false);
      setUnsent(false);
      return true;
    } catch (cause) {
      adopt({
        ...previous,
        attempt: confirmed ? null : previous.attempt,
        error: new Error(
          confirmed
            ? "Ответ сервера подтверждён, но запись восстановления не удалена. Очистите её перед новыми изменениями."
            : "Не удалось удалить восстановление. Запись сохранена.",
          { cause },
        ),
      });
      setCleanupPending(confirmed);
      return false;
    }
  }
  function reload() {
    const next = discover();
    setOptions(next);
    const selected =
      next.find((item) => item.key === state.current.key) ??
      (next.length === 1 ? next[0] : undefined);
    if (!selected) {
      adopt({ key, raw: null, attempt: null, error: new Error("Выберите запрос восстановления.") });
      return null;
    }
    if (cleanupPending && selected.raw === state.current.raw) {
      adopt({ ...selected, attempt: null, error: state.current.error });
      return null;
    }
    const memory = state.current.attempt;
    adopt({
      ...selected,
      attempt:
        selected.attempt ?? (selected.raw === null && memory?.phase === "unknown" ? memory : null),
    });
    setCleanupPending(false);
    return state.current.attempt;
  }
  return {
    attempt: slot.attempt,
    error: slot.error,
    raw: slot.raw,
    options,
    selectedKey: slot.key,
    cleanupPending,
    unsent,
    persist,
    clear,
    reload,
    select: (selected: string) => {
      const value = options.find((item) => item.key === selected);
      if (value) {
        adopt(value);
        setCleanupPending(false);
      }
    },
    blocked: !!slot.error || cleanupPending,
  };
}

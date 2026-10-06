import { parseBrowserSafeJson } from "@/api/preciseJson";
export type ReplayAttempt = {
  projectId: string;
  action: "connect" | "save" | "start" | "revoke";
  body: unknown;
};
const storageKey = (projectId: string) => `mocker:backend-replay:v1:${projectId}`;
export function readReplayAttempt(
  storage: Pick<Storage, "getItem">,
  projectId: string,
): ReplayAttempt | null {
  const raw = storage.getItem(storageKey(projectId));
  if (raw === null) return null;
  const value = parseBrowserSafeJson(raw) as ReplayAttempt | null;
  if (
    !value ||
    value.projectId !== projectId ||
    !["connect", "save", "start", "revoke"].includes(value.action) ||
    !value.body ||
    typeof value.body !== "object"
  )
    throw new Error(
      "Сохранённый replay-запрос повреждён. Сохраните его для ручного восстановления.",
    );
  return value;
}
export interface ReplayLocks {
  request<T>(name: string, work: () => T | Promise<T>): Promise<T>;
}
function locked<T>(projectId: string, locks: ReplayLocks | undefined, work: () => T): Promise<T> {
  if (!locks)
    return Promise.reject(
      new Error("Web Locks is required to safely persist replay requests across tabs."),
    );
  return locks.request(storageKey(projectId), work);
}
export function writeReplayAttempt(
  storage: Pick<Storage, "getItem" | "setItem">,
  projectId: string,
  value: ReplayAttempt,
  locks: ReplayLocks | undefined = navigator.locks,
): Promise<void> {
  return locked(projectId, locks, () => {
    const raw = JSON.stringify(value);
    const previous = storage.getItem(storageKey(projectId));
    if (previous !== null && previous !== raw)
      throw new Error("Another replay request is pending for this project.");
    storage.setItem(storageKey(projectId), raw);
    if (storage.getItem(storageKey(projectId)) !== raw)
      throw new Error("Unable to durably save replay request.");
  });
}
export function clearReplayAttempt(
  storage: Pick<Storage, "getItem" | "removeItem">,
  projectId: string,
  completed: ReplayAttempt,
  locks: ReplayLocks | undefined = navigator.locks,
): Promise<boolean> {
  return locked(projectId, locks, () => {
    if (storage.getItem(storageKey(projectId)) !== JSON.stringify(completed)) return false;
    storage.removeItem(storageKey(projectId));
    return true;
  });
}

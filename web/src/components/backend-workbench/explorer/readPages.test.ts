import { expect, it, vi } from "vitest";
import { readPages } from "./readPages";
it("loads every transport page without a display count cap", async () => {
  const load = vi.fn(async (cursor: string) => {
    const index = Number(cursor || 0);
    return {
      values: Array.from({ length: 100 }, (_, n) => index * 100 + n),
      hash: "pinned",
      nextCursor: index < 12 ? String(index + 1) : "",
    };
  });
  const pages = await readPages(load, new AbortController().signal, (p) => p.hash);
  expect(pages.flatMap((p) => p.values)).toHaveLength(1300);
  expect(load).toHaveBeenCalledTimes(13);
});
it("refuses a repeated cursor and a changed immutable page identity", async () => {
  const signal = new AbortController().signal;
  await expect(readPages(async () => ({ nextCursor: "same" }), signal)).rejects.toThrow(/повторил/);
  await expect(
    readPages(
      async (cursor) => ({ nextCursor: cursor ? "" : "two", hash: cursor ? "new" : "old" }),
      signal,
      (p) => p.hash,
    ),
  ).rejects.toThrow(/Версия/);
});
it("stops after cancellation instead of publishing a partial scope", async () => {
  const abort = new AbortController();
  const load = vi.fn(async () => {
    abort.abort();
    return { nextCursor: "more" };
  });
  await expect(readPages(load, abort.signal)).rejects.toThrow();
  expect(load).toHaveBeenCalledOnce();
});

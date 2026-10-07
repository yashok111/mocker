/** Transport pages never define the number of objects visible in a workspace. */
export async function readPages<T extends { nextCursor?: string }>(
  load: (cursor: string) => Promise<T>,
  signal: AbortSignal,
  identity?: (page: T) => string,
): Promise<T[]> {
  const pages: T[] = [];
  const seen = new Set<string>();
  let cursor = "";
  let expected: string | undefined;
  do {
    signal.throwIfAborted();
    const page = await load(cursor);
    signal.throwIfAborted();
    const key = identity?.(page);
    if (expected !== undefined && key !== expected)
      throw new Error("Версия изменилась между страницами ответа.");
    expected = key;
    pages.push(page);
    cursor = page.nextCursor ?? "";
    if (cursor && seen.has(cursor))
      throw new Error("Сервер повторил страницу. Полный набор объектов недоступен.");
    seen.add(cursor);
  } while (cursor);
  return pages;
}

export function uniqueBy<T>(items: T[], key: (item: T) => string): T[] {
  return [...new Map(items.map((item) => [key(item), item])).values()];
}

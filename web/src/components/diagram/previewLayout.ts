export async function previewLayoutCommands<C, R extends { document: string }>(
  document: string,
  commands: readonly C[],
  preview: (document: string, commands: C[], signal?: AbortSignal) => Promise<R>,
  signal?: AbortSignal,
): Promise<R> {
  if (!commands.length) throw new Error("Нет элементов для расстановки.");
  let result: R | undefined;
  for (let start = 0; start < commands.length; start += 100) {
    signal?.throwIfAborted();
    result = await preview(document, commands.slice(start, start + 100), signal);
    signal?.throwIfAborted();
    document = result.document;
  }
  return result!;
}

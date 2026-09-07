/**
 * jsonLocation turns JSON.parse's own SyntaxError ("Unexpected token j in
 * JSON at position 5") into "строка N, столбец M": a body must be validated in
 * the browser AND say WHERE it is broken, and a byte offset into a multi-line
 * textarea is not something a person can use without counting characters by
 * hand.
 *
 * It lives here rather than in VariantEditor.tsx (which exported it) because
 * it is not about a response variant: CustomEndpointsPage kept a byte-identical
 * private copy, and five further sites — the stream editor's frame data, the
 * connections tab's push payload, the not-found body in settings and both
 * resource-entity editors — hand-rolled the coarser `err.message` instead, so
 * the same malformed JSON read as a position offset on one screen and as a
 * line/column on another.
 *
 * The fallback is not a hypothetical: V8 names a position for a STRUCTURAL
 * error ("Expected ':' after property name in JSON at position 18") but not
 * for an unexpected token ("Unexpected token 'o', …\" is not valid JSON",
 * measured on node v24.17.0), and the latter is what a half-typed body usually
 * produces. When there is no position to read, the raw message is shown —
 * exactly what every one of the five adopting sites showed before.
 */
export function jsonLocation(text: string, err: unknown): string {
  const message = err instanceof Error ? err.message : String(err);
  const match = /position (\d+)/.exec(message);
  if (!match) {
    return message;
  }
  const pos = Number(match[1]);
  const before = text.slice(0, pos);
  const line = before.split("\n").length;
  const column = pos - before.lastIndexOf("\n");
  return `строка ${line}, столбец ${column}`;
}

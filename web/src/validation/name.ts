import { type } from "arktype";

export const MAX_NAME_RUNES = 64;

// runeLength counts Unicode code points, not UTF-16 code units:
// [...value].length iterates by code point the same way the server's own
// []rune(name) check does (internal/auth's validateName). value.length alone
// would count an emoji as two and reject a name the server would happily
// accept.
export function runeLength(value: string): number {
  return [...value].length;
}

export function hasControlChar(value: string): boolean {
  for (const ch of value) {
    const code = ch.codePointAt(0);
    if (code !== undefined && code < 0x20) {
      return true;
    }
  }
  return false;
}

// userName mirrors internal/auth's validateName exactly — trimmed, non-empty,
// at most 64 runes, no control characters — so a name the server would accept
// never gets rejected here, and one it would refuse never wastes a round trip
// finding that out. api/openapi.json states the same four rules on UserView.name;
// this is their executable copy, and the two must not drift apart.
// Note it validates the TRIMMED value but does not itself return one: a
// narrow is a predicate, and making this a pipe instead would widen the
// schema's inferred input to `unknown` and break react-hook-form's field
// typing. Callers trim once more before sending, exactly as the server does.
export const userName = type("string").narrow((value, ctx) => {
  const trimmed = value.trim();
  if (trimmed === "") {
    return ctx.reject({ problem: "Введите имя" });
  }
  if (runeLength(trimmed) > MAX_NAME_RUNES) {
    return ctx.reject({ problem: `Имя слишком длинное (максимум ${MAX_NAME_RUNES} символов)` });
  }
  if (hasControlChar(trimmed)) {
    return ctx.reject({ problem: "Имя содержит недопустимые символы" });
  }
  return true;
});

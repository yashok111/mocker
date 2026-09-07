import { describe, expect, it } from "vitest";
import { jsonLocation } from "./json";

function parseError(text: string): unknown {
  try {
    JSON.parse(text);
  } catch (err) {
    return err;
  }
  throw new Error("expected the document to be invalid");
}

describe("jsonLocation", () => {
  it("turns a byte offset into the line and column a person can find", () => {
    const text = '{\n  "a": 1,\n  "b"\n}';
    const located = jsonLocation(text, parseError(text));
    // A Russian string in the product, reproduced as data.
    expect(located).toMatch(/^строка \d+, столбец \d+$/);
    expect(located).toContain("строка 4");
  });

  it("falls back to the engine's own message when it names no position", () => {
    // Not hypothetical: V8 names a position for a structural error but NOT for
    // an unexpected token ("Unexpected token 'o', …\" is not valid JSON",
    // measured on node v24.17.0), and that is the error a half-typed body
    // usually produces. The fallback is the raw message — exactly what the five
    // sites that adopted this helper showed before, never an empty string.
    const text = '{\n  "a": 1,\n  "b": oops\n}';
    expect(jsonLocation(text, parseError(text))).toContain("is not valid JSON");
    expect(jsonLocation("{}", new Error("нечто иное"))).toBe("нечто иное");
    expect(jsonLocation("{}", "не Error")).toBe("не Error");
  });
});

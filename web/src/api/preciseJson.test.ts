import { describe, expect, it } from "vitest";
import { BrowserJsonPrecisionError, parseBrowserSafeJson } from "./preciseJson";

const precisionError =
  "Документ содержит числа, которые браузер не может сохранить без потери точности. Используйте MCP для работы с этим документом.";

describe("parseBrowserSafeJson", () => {
  it("rejects an integer that JSON.parse would round", () => {
    expect(() => parseBrowserSafeJson('{"value":9007199254740993}')).toThrow(
      BrowserJsonPrecisionError,
    );
    expect(() => parseBrowserSafeJson('{"value":9007199254740993}')).toThrow(precisionError);
  });

  it("ignores number-like text inside JSON strings", () => {
    const source = '{"value":"9007199254740993","escaped":"\\\"1e400"}';

    expect(parseBrowserSafeJson(source)).toEqual({
      value: "9007199254740993",
      escaped: '"1e400',
    });
  });

  it("accepts equivalent plain and scientific spellings without rounding", () => {
    const source = '{"plain":9007199254740992,"scientific":9.007199254740992e15,"scaled":1.2300e2}';

    expect(parseBrowserSafeJson(source)).toEqual({
      plain: 9007199254740992,
      scientific: 9007199254740992,
      scaled: 123,
    });
  });

  it("rejects a rounded scientific value and a non-finite value", () => {
    expect(() => parseBrowserSafeJson('{"value":9.007199254740993e15}')).toThrow(
      BrowserJsonPrecisionError,
    );
    expect(() => parseBrowserSafeJson('{"value":9.007199254740993e15}')).toThrow(precisionError);
    expect(() => parseBrowserSafeJson('{"value":1e400}')).toThrow(BrowserJsonPrecisionError);
    expect(() => parseBrowserSafeJson('{"value":1e400}')).toThrow(precisionError);
  });
});

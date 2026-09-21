import { describe, expect, it } from "vitest";

import { isCanvasColor, readableCanvasTextColor } from "./canvasColors";

function luminance(hex: string) {
  const channels = [1, 3, 5].map((offset) => {
    const channel = Number.parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
  });
  return channels[0]! * 0.2126 + channels[1]! * 0.7152 + channels[2]! * 0.0722;
}

describe("canvas colors", () => {
  it.each(["#abcdef", "#ABCDEF", "#Ab12Cd", "#000000", "#ffffff"])(
    "accepts an opaque six-digit hex color %s",
    (color) => expect(isCanvasColor(color)).toBe(true),
  );

  it.each([
    undefined,
    null,
    123,
    "",
    "red",
    "#fff",
    "#11223344",
    "#gg0000",
    " #abcdef",
    "#abcdef\n",
  ])("rejects unsupported colors: %s", (color) => expect(isCanvasColor(color)).toBe(false));

  it("keeps a preferred foreground only when it is readable", () => {
    expect(readableCanvasTextColor("#ffffff", "#25332f")).toBe("#25332f");
    expect(readableCanvasTextColor("#111111", "#69756e")).toBe("#ffffff");
  });

  it("maintains 4.5:1 contrast across dark, midtone, and light card fills", () => {
    for (let red = 0; red <= 255; red += 17) {
      for (let green = 0; green <= 255; green += 17) {
        for (let blue = 0; blue <= 255; blue += 17) {
          const background = `#${[red, green, blue].map((c) => c.toString(16).padStart(2, "0")).join("")}`;
          const foreground = readableCanvasTextColor(background, "#087f70");
          const levels = [luminance(background), luminance(foreground)].sort((a, b) => a - b);
          expect((levels[1]! + 0.05) / (levels[0]! + 0.05)).toBeGreaterThanOrEqual(4.5);
        }
      }
    }
  });
});

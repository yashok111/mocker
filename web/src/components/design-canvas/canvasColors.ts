export function isCanvasColor(value: unknown): value is string {
  return typeof value === "string" && value.length === 7 && /^#[\da-f]{6}$/i.test(value);
}

function relativeLuminance(color: string) {
  const channels = [1, 3, 5].map((offset) => {
    const channel = Number.parseInt(color.slice(offset, offset + 2), 16) / 255;
    return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
  });
  return channels[0]! * 0.2126 + channels[1]! * 0.7152 + channels[2]! * 0.0722;
}

/** Retain the usual foreground when readable, otherwise use contrasting white or black. */
export function readableCanvasTextColor(background: string, preferred = "#25332f") {
  const luminance = relativeLuminance(background);
  const preferredLuminance = relativeLuminance(preferred);
  const preferredContrast =
    (Math.max(luminance, preferredLuminance) + 0.05) /
    (Math.min(luminance, preferredLuminance) + 0.05);
  if (preferredContrast >= 4.5) return preferred;
  return (luminance + 0.05) / 0.05 >= 1.05 / (luminance + 0.05) ? "#000000" : "#ffffff";
}

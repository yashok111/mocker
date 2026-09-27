export const CANVAS_TEXT_FONT_FAMILY = "Arial, Helvetica, sans-serif";
export const CANVAS_MONOSPACE_FONT_FAMILY = "ui-monospace, SFMono-Regular, Menlo, monospace";
export type MeasureText = (text: string) => number;

export function canvasTextMeasurer(
  fontSize: number,
  fontWeight: number,
  fontFamily = CANVAS_TEXT_FONT_FAMILY,
): MeasureText {
  const context = globalThis.document?.createElement("canvas").getContext("2d");
  if (!context) return (text) => Array.from(text).length * fontSize * 0.6;
  context.font = `${fontWeight} ${fontSize}px ${fontFamily}`;
  return (text) => context.measureText(text).width;
}

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

export function wrapCanvasText(name: string, width: number, measure: MeasureText): string[] {
  return name.split(/\r\n|\r|\n/).flatMap((paragraph) => {
    const lines: string[] = [];
    let line = "";
    for (const word of paragraph.split(/\s+/).filter(Boolean)) {
      const candidate = line ? `${line} ${word}` : word;
      if (measure(candidate) <= width) {
        line = candidate;
        continue;
      }
      if (line) lines.push(line);
      line = "";
      // A token wider than the card must also wrap, without splitting emoji or accents.
      for (const { segment } of graphemes.segment(word)) {
        if (line && measure(line + segment) > width) {
          lines.push(line);
          line = "";
        }
        line += segment;
      }
    }
    lines.push(line);
    return lines;
  });
}

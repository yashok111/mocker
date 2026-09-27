export const PARTICIPANT_NAME_WIDTH = 120;
export const PARTICIPANT_NAME_LINE_HEIGHT = 16;
export const PARTICIPANT_NAME_FONT_FAMILY = "Arial, Helvetica, sans-serif";

type MeasureText = (text: string) => number;

export function participantNameMeasurer(): MeasureText {
  const context = globalThis.document?.createElement("canvas").getContext("2d");
  if (!context) return (text) => Array.from(text).length * 7.2;
  context.font = `650 12px ${PARTICIPANT_NAME_FONT_FAMILY}`;
  return (text) => context.measureText(text).width;
}

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

export function wrapParticipantName(name: string, measure: MeasureText): string[] {
  return name.split(/\r\n|\r|\n/).flatMap((paragraph) => {
    const lines: string[] = [];
    let line = "";
    for (const word of paragraph.split(/\s+/).filter(Boolean)) {
      const candidate = line ? `${line} ${word}` : word;
      if (measure(candidate) <= PARTICIPANT_NAME_WIDTH) {
        line = candidate;
        continue;
      }
      if (line) lines.push(line);
      line = "";
      // A token wider than the card must also wrap, without splitting emoji or accents.
      for (const { segment } of graphemes.segment(word)) {
        if (line && measure(line + segment) > PARTICIPANT_NAME_WIDTH) {
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

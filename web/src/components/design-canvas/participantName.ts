import {
  CANVAS_TEXT_FONT_FAMILY,
  canvasTextMeasurer,
  wrapCanvasText,
  type MeasureText,
} from "./canvasText";

export const PARTICIPANT_NAME_WIDTH = 120;
export const PARTICIPANT_NAME_LINE_HEIGHT = 16;
export const PARTICIPANT_NAME_FONT_FAMILY = CANVAS_TEXT_FONT_FAMILY;

export function participantNameMeasurer(): MeasureText {
  return canvasTextMeasurer(12, 650);
}

export function wrapParticipantName(name: string, measure: MeasureText): string[] {
  return wrapCanvasText(name, PARTICIPANT_NAME_WIDTH, measure);
}

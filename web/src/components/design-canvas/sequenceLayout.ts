import { resolveOperation } from "./canvasModel";
import { canvasTextMeasurer, wrapCanvasText, CANVAS_MONOSPACE_FONT_FAMILY } from "./canvasText";
import {
  participantNameMeasurer,
  wrapParticipantName,
  PARTICIPANT_NAME_LINE_HEIGHT,
} from "./participantName";
import type { CanvasDocument } from "./types";

const MIN_WIDTH = 720;
const MIN_HEIGHT = 360;
const SIDE_PADDING = 64;
const HEADER_TOP = 40;
const HEADER_WIDTH = 160;
const HEADER_HEIGHT = 52;
const PARTICIPANT_GAP = 96;
const FIRST_MESSAGE_Y = 148;
const MESSAGE_GAP = 72;
const MESSAGE_LABEL_HEIGHT = 28;
const MESSAGE_LABEL_GAP = 6;
const MESSAGE_LABEL_PADDING_X = 37;
const MESSAGE_LIFELINE_GAP = 12;
export const MESSAGE_LABEL_LINE_HEIGHT = 14;
export const MESSAGE_OPERATION_LINE_HEIGHT = 12;
const SELF_CALL_WIDTH = 56;
const SELF_CALL_HEIGHT = 34;
const FRAGMENT_PADDING_X = 44;
const CONTENT_GAP = 16;
const FRAGMENT_HEADER_HEIGHT = 32;
const FRAGMENT_BOTTOM_PADDING = 16;

export interface SequencePoint {
  x: number;
  y: number;
}

export interface ParticipantLayout {
  id: string;
  index: number;
  x: number;
  nameLines: string[];
  header: { x: number; y: number; width: number; height: number };
  lifeline: { source: SequencePoint; target: SequencePoint };
}

export interface MessageLayout {
  id: string;
  index: number;
  rowY: number;
  source: SequencePoint;
  target: SequencePoint;
  vertices: SequencePoint[];
  labelLines: string[];
  operationLines: string[];
  label: { x: number; y: number; width: number; height: number };
  note?: { x: number; y: number; width: number; height: number };
}

export interface FragmentLayout {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface SequenceLayout {
  width: number;
  height: number;
  participants: ParticipantLayout[];
  messages: MessageLayout[];
  fragments: FragmentLayout[];
}

export function layoutSequence(document: CanvasDocument): SequenceLayout {
  const participantX = new Map<string, number>();
  const measureName = participantNameMeasurer();
  const measureLabel = canvasTextMeasurer(11, 550);
  const measureOperation = canvasTextMeasurer(9, 650, CANVAS_MONOSPACE_FONT_FAMILY);
  let horizontalOffset = 0;
  const participants = document.participants.map((participant, index): ParticipantLayout => {
    horizontalOffset += participant.offsetX ?? 0;
    const headerX = SIDE_PADDING + index * (HEADER_WIDTH + PARTICIPANT_GAP) + horizontalOffset;
    const x = headerX + HEADER_WIDTH / 2;
    const nameLines = wrapParticipantName(participant.name, measureName);
    const headerHeight = HEADER_HEIGHT + (nameLines.length - 1) * PARTICIPANT_NAME_LINE_HEIGHT;
    participantX.set(participant.id, x);
    return {
      id: participant.id,
      index,
      x,
      nameLines,
      header: { x: headerX, y: HEADER_TOP, width: HEADER_WIDTH, height: headerHeight },
      lifeline: {
        source: { x, y: HEADER_TOP + headerHeight },
        target: { x, y: MIN_HEIGHT - 40 },
      },
    };
  });

  const messageIndices = new Map(document.messages.map((message, index) => [message.id, index]));
  const ranges = document.fragments.flatMap((fragment, order) => {
    const from = messageIndices.get(fragment.fromMessageId);
    const to = messageIndices.get(fragment.toMessageId);
    if (from === undefined || to === undefined) return [];
    return [
      { fragment, order, from: Math.min(from, to), to: Math.max(from, to), top: 0, bottom: 0 },
    ];
  });
  // Outer frames open first; equal ranges nest in document order.
  ranges.sort((a, b) => a.from - b.from || b.to - a.to || a.order - b.order);
  let contentBottom = Math.max(
    HEADER_TOP + HEADER_HEIGHT,
    ...participants.map(({ lifeline }) => lifeline.source.y),
  );
  let previousRowY = FIRST_MESSAGE_Y - MESSAGE_GAP;
  const messages: MessageLayout[] = [];

  for (const [index, message] of document.messages.entries()) {
    const fromX = participantX.get(message.fromId);
    const toX = participantX.get(message.toId);
    if (fromX === undefined || toX === undefined) continue;
    const operation = message.operation ? resolveOperation(document, message.operation) : null;
    const operationLabel = operation
      ? `${operation.location.method.toUpperCase()} ${operation.location.path}`
      : message.operation
        ? "API-связь недоступна"
        : "";
    const isNote = message.kind === "note";
    const isSelfCall = !isNote && message.fromId === message.toId;
    const nextX = participants.find(({ x }) => x > fromX)?.x;
    const availableWidth =
      (isSelfCall
        ? (nextX ?? fromX + HEADER_WIDTH + PARTICIPANT_GAP) - fromX
        : Math.abs(toX - fromX)) -
      2 * MESSAGE_LIFELINE_GAP;
    const naturalWidth = Math.max(
      96,
      ...message.label
        .split(/\r\n|\r|\n/)
        .map((line) => measureLabel(line) + MESSAGE_LABEL_PADDING_X),
      ...operationLabel
        .split(/\r\n|\r|\n/)
        .map((line) => measureOperation(line) + MESSAGE_LABEL_PADDING_X),
    );
    const labelWidth = isNote
      ? Math.max(
          96,
          Array.from(message.label).length * 7.2 + 44,
          operationLabel ? Array.from(operationLabel).length * 6.4 + 38 : 0,
        )
      : Math.min(naturalWidth, availableWidth);
    const labelLines = isNote
      ? [message.label]
      : wrapCanvasText(message.label, labelWidth - MESSAGE_LABEL_PADDING_X, measureLabel);
    const operationLines = !operationLabel
      ? []
      : isNote
        ? [operationLabel]
        : wrapCanvasText(operationLabel, labelWidth - MESSAGE_LABEL_PADDING_X, measureOperation);
    const labelHeight =
      MESSAGE_LABEL_HEIGHT +
      (labelLines.length - 1) * MESSAGE_LABEL_LINE_HEIGHT +
      (operationLines.length
        ? 14 + (operationLines.length - 1) * MESSAGE_OPERATION_LINE_HEIGHT
        : 0);
    const noteHeight = operationLabel ? 58 : 46;
    const topExtent = isNote ? noteHeight / 2 : labelHeight + MESSAGE_LABEL_GAP;
    const opening = ranges.filter((range) => range.from === index);
    const rowY = Math.max(
      FIRST_MESSAGE_Y,
      previousRowY + MESSAGE_GAP,
      contentBottom + CONTENT_GAP + opening.length * FRAGMENT_HEADER_HEIGHT + topExtent,
    );
    for (const [depth, range] of opening.entries()) {
      range.top = rowY - topExtent - (opening.length - depth) * FRAGMENT_HEADER_HEIGHT;
    }
    const targetY = isSelfCall ? rowY + SELF_CALL_HEIGHT : rowY;
    const labelX = isSelfCall ? fromX + MESSAGE_LIFELINE_GAP : (fromX + toX - labelWidth) / 2;
    const geometry: MessageLayout = {
      id: message.id,
      index,
      rowY,
      source: { x: fromX, y: rowY },
      target: { x: toX, y: targetY },
      labelLines,
      operationLines,
      vertices: isSelfCall
        ? [
            { x: fromX + SELF_CALL_WIDTH, y: rowY },
            { x: fromX + SELF_CALL_WIDTH, y: targetY },
          ]
        : [],
      label: {
        x: labelX,
        y: rowY - labelHeight - MESSAGE_LABEL_GAP,
        width: labelWidth,
        height: labelHeight,
      },
      ...(isNote
        ? { note: { x: toX + 18, y: rowY - noteHeight / 2, width: labelWidth, height: noteHeight } }
        : {}),
    };
    messages.push(geometry);
    previousRowY = rowY;
    contentBottom = isNote ? rowY + noteHeight / 2 : targetY + 4;
    // Close inner frames first and reserve their borders before the next row.
    for (const range of ranges.filter((range) => range.to === index).reverse()) {
      contentBottom += FRAGMENT_BOTTOM_PADDING;
      range.bottom = contentBottom;
    }
  }

  const fragmentBounds = new Map<string, FragmentLayout>();
  for (const range of [...ranges].reverse()) {
    const contained = messages.filter(({ index }) => index >= range.from && index <= range.to);
    if (contained.length === 0 || range.bottom === 0) continue;
    let left = Infinity;
    let right = -Infinity;
    for (const message of contained) {
      const card = message.note ?? message.label;
      left = Math.min(
        left,
        message.source.x - FRAGMENT_PADDING_X,
        message.target.x - FRAGMENT_PADDING_X,
        card.x - CONTENT_GAP,
      );
      right = Math.max(
        right,
        message.source.x + FRAGMENT_PADDING_X,
        message.target.x + FRAGMENT_PADDING_X,
        card.x + card.width + CONTENT_GAP,
        ...message.vertices.map(({ x }) => x + CONTENT_GAP),
      );
    }
    for (const inner of fragmentBounds.values()) {
      if (inner.y > range.top && inner.y + inner.height < range.bottom) {
        left = Math.min(left, inner.x - CONTENT_GAP);
        right = Math.max(right, inner.x + inner.width + CONTENT_GAP);
      }
    }
    fragmentBounds.set(range.fragment.id, {
      id: range.fragment.id,
      x: left,
      y: range.top,
      width: Math.max(176, right - left, Array.from(range.fragment.label).length * 7.2 + 84),
      height: range.bottom - range.top,
    });
  }
  const fragments = document.fragments.flatMap(({ id }) => {
    const geometry = fragmentBounds.get(id);
    return geometry ? [geometry] : [];
  });
  const height = Math.max(MIN_HEIGHT, contentBottom + 80);
  for (const participant of participants) participant.lifeline.target.y = height - 40;
  const width = Math.max(
    MIN_WIDTH,
    ...participants.map(({ header }) => header.x + header.width + SIDE_PADDING),
    ...messages.map(({ label, note }) => (note ?? label).x + (note ?? label).width + SIDE_PADDING),
    ...fragments.map(({ x, width }) => x + width + SIDE_PADDING),
  );
  return { width, height, participants, messages, fragments };
}

export function participantIndexAtX(layout: SequenceLayout, x: number): number {
  if (layout.participants.length === 0) return 0;
  return layout.participants.reduce(
    (closest, participant) =>
      Math.abs(participant.x - x) < Math.abs(layout.participants[closest]!.x - x)
        ? participant.index
        : closest,
    0,
  );
}

export function messageIndexAtY(layout: SequenceLayout, y: number): number {
  if (layout.messages.length === 0) return 0;
  let closest = layout.messages[0]!;
  for (const message of layout.messages.slice(1)) {
    if (Math.abs(message.rowY - y) < Math.abs(closest.rowY - y)) closest = message;
  }
  return closest.index;
}

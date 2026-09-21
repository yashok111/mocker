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
const SELF_CALL_WIDTH = 56;
const SELF_CALL_HEIGHT = 34;
const FRAGMENT_PADDING_X = 44;
const FRAGMENT_TOP_PADDING = 64;
const FRAGMENT_BOTTOM_PADDING = 30;

export interface SequencePoint {
  x: number;
  y: number;
}

export interface ParticipantLayout {
  id: string;
  index: number;
  x: number;
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

export function layoutSequence(_document: CanvasDocument): SequenceLayout {
  const participantX = new Map<string, number>();
  const contentWidth =
    _document.participants.length === 0
      ? MIN_WIDTH
      : SIDE_PADDING * 2 +
        HEADER_WIDTH * _document.participants.length +
        PARTICIPANT_GAP * (_document.participants.length - 1);
  const height = Math.max(
    MIN_HEIGHT,
    FIRST_MESSAGE_Y + Math.max(0, _document.messages.length - 1) * MESSAGE_GAP + 80,
  );

  const participants = _document.participants.map((participant, index): ParticipantLayout => {
    const headerX = SIDE_PADDING + index * (HEADER_WIDTH + PARTICIPANT_GAP);
    const x = headerX + HEADER_WIDTH / 2;
    participantX.set(participant.id, x);
    return {
      id: participant.id,
      index,
      x,
      header: { x: headerX, y: HEADER_TOP, width: HEADER_WIDTH, height: HEADER_HEIGHT },
      lifeline: {
        source: { x, y: HEADER_TOP + HEADER_HEIGHT },
        target: { x, y: height - 40 },
      },
    };
  });

  const messages = _document.messages.flatMap((message, index): MessageLayout[] => {
    const fromX = participantX.get(message.fromId);
    const toX = participantX.get(message.toId);
    if (fromX === undefined || toX === undefined) {
      return [];
    }

    const rowY = FIRST_MESSAGE_Y + index * MESSAGE_GAP;
    const isSelfCall = message.fromId === message.toId;
    const targetY = isSelfCall ? rowY + SELF_CALL_HEIGHT : rowY;
    const labelWidth = Math.max(96, Array.from(message.label).length * 7.2 + 44);
    const midpointX = isSelfCall ? fromX + SELF_CALL_WIDTH / 2 : (fromX + toX) / 2;
    const note =
      message.kind === "note"
        ? { x: toX + 18, y: rowY - 23, width: labelWidth, height: 46 }
        : undefined;

    return [
      {
        id: message.id,
        index,
        rowY,
        source: { x: fromX, y: rowY },
        target: { x: toX, y: targetY },
        vertices: isSelfCall
          ? [
              { x: fromX + SELF_CALL_WIDTH, y: rowY },
              { x: fromX + SELF_CALL_WIDTH, y: targetY },
            ]
          : [],
        label: {
          x: midpointX - labelWidth / 2,
          y: rowY - 22,
          width: labelWidth,
          height: 28,
        },
        ...(note ? { note } : {}),
      },
    ];
  });

  const messageById = new Map(messages.map((message) => [message.id, message]));
  const fragments = _document.fragments.flatMap((fragment): FragmentLayout[] => {
    const first = messageById.get(fragment.fromMessageId);
    const last = messageById.get(fragment.toMessageId);
    if (!first || !last) {
      return [];
    }

    const fromIndex = Math.min(first.index, last.index);
    const toIndex = Math.max(first.index, last.index);
    const participantXs = _document.messages
      .slice(fromIndex, toIndex + 1)
      .flatMap((message) => [participantX.get(message.fromId), participantX.get(message.toId)])
      .filter((x): x is number => x !== undefined);
    if (participantXs.length === 0) {
      return [];
    }

    const minX = Math.min(...participantXs);
    const maxX = Math.max(...participantXs);
    const y = Math.min(first.rowY, last.rowY) - FRAGMENT_TOP_PADDING;
    const bottom = Math.max(
      first.target.y,
      last.target.y,
      Math.max(
        ...messages
          .filter(({ index }) => index >= fromIndex && index <= toIndex)
          .map(({ target }) => target.y),
      ),
    );

    return [
      {
        id: fragment.id,
        x: minX - FRAGMENT_PADDING_X,
        y,
        width: Math.max(176, maxX - minX + FRAGMENT_PADDING_X * 2),
        height: bottom + FRAGMENT_BOTTOM_PADDING - y,
      },
    ];
  });

  return { width: Math.max(MIN_WIDTH, contentWidth), height, participants, messages, fragments };
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

import {
  buildCanvasContract,
  previewCanvasContract,
  type ConversionResult,
  type ConversionRow,
} from "./canvasContractConversion";
import type { CanvasDocument } from "./types";

export interface ContractGroup {
  participantId: string;
  name: string;
  document: CanvasDocument;
  rows: ConversionRow[];
}

export function previewCanvasContractGroups(doc: CanvasDocument): ContractGroup[] {
  const receivers = new Set(
    doc.messages.filter((message) => message.kind === "request").map((message) => message.toId),
  );
  return doc.participants.flatMap((participant) => {
    if (!receivers.has(participant.id)) return [];
    const requests = doc.messages.filter(
      (message) => message.kind === "request" && message.toId === participant.id,
    );
    const requestIds = new Set(requests.map(({ id }) => id));
    const responses = doc.messages.filter(
      (message) =>
        message.kind === "response" &&
        message.replyToId !== undefined &&
        requestIds.has(message.replyToId),
    );
    // Keep all original contracts so source references resolve without losing shared components.
    const document = { ...doc, messages: [...requests, ...responses] };
    const rows = previewCanvasContract(document).rows;
    return rows.length
      ? [{ participantId: participant.id, name: participant.name, document, rows }]
      : [];
  });
}

export function buildCanvasContractGroups(
  groups: ContractGroup[],
  names: string[],
  contractIds: string[],
): ConversionResult[] {
  return groups.flatMap((group, index) =>
    group.rows.some((row) => row.included)
      ? [
          buildCanvasContract(
            group.document,
            group.rows,
            names[index] ?? "",
            contractIds[index] ?? "",
          ),
        ]
      : [],
  );
}

import type { ApiDocument } from "../api-designer/documentModel";
import type { EventBinding, EventModel } from "./eventTypes";

export type ParticipantKind =
  | "user"
  | "client"
  | "service"
  | "external"
  | "database"
  | "queue"
  | "other";
export type MessageKind = "request" | "response" | "event" | "note";

export interface CanvasParticipant {
  id: string;
  name: string;
  kind: ParticipantKind;
  description: string;
  color?: string;
  offsetX?: number;
}

export const MAX_PARTICIPANT_OFFSET_X = 2000;

export interface OperationBinding {
  contractId: string;
  operationKey: string;
}

export type JSONValue =
  | null
  | boolean
  | number
  | string
  | JSONValue[]
  | { [key: string]: JSONValue };

export const CANVAS_EXECUTION_LIMITS = {
  entries: 100,
  name: 100,
  key: 256,
  value: 50_000,
  body: 1_048_576,
  pointer: 2_000,
  stepTimeoutMs: 30_000,
  reportBody: 20 * 1_048_576,
} as const;

export interface CanvasExecution {
  variables: Record<string, string>;
}

export interface CanvasStepExecution {
  enabled: boolean;
  pathParams: Record<string, string>;
  query: Record<string, string>;
  headers: Record<string, string>;
  body: string;
  expectedStatus?: number;
  assertions: { pointer: string; equals: JSONValue }[];
  extract: { name: string; pointer: string }[];
}

export interface CanvasMessage {
  id: string;
  fromId: string;
  toId: string;
  kind: MessageKind;
  label: string;
  description: string;
  color?: string;
  arrowColor?: string;
  replyToId?: string;
  operation?: OperationBinding;
  eventBindings?: EventBinding[];
  execution?: CanvasStepExecution;
}

export interface CanvasFragmentBranch {
  id: string;
  label: string;
  fromMessageId: string;
  toMessageId: string;
  execution?: CanvasBranchExecution;
}

export interface CanvasExecutionCondition {
  variable: string;
  operator: "equals" | "not_equals" | "exists" | "not_exists";
  value?: string;
}

export interface CanvasBranchExecution {
  condition?: CanvasExecutionCondition;
  otherwise?: boolean;
}

export interface CanvasFragmentExecution {
  condition?: CanvasExecutionCondition;
  iterations?: number;
}

export interface CanvasFragment {
  id: string;
  kind: "opt" | "loop" | "alt";
  label: string;
  fromMessageId: string;
  toMessageId: string;
  parentFragmentId?: string;
  parentBranchId?: string;
  branches?: CanvasFragmentBranch[];
  execution?: CanvasFragmentExecution;
}

export interface CanvasContract {
  id: string;
  name: string;
  document: ApiDocument;
  mode?: "copy" | "linked";
  source?: { designId: number; revisionId: number; version?: number };
}

export interface CanvasDocument {
  formatVersion: 1 | 2 | 3;
  title: string;
  participants: CanvasParticipant[];
  messages: CanvasMessage[];
  fragments: CanvasFragment[];
  contracts: CanvasContract[];
  execution?: CanvasExecution;
  eventModel?: EventModel;
}

export type CanvasSelection = {
  kind: "participant" | "message" | "fragment";
  id: string;
} | null;

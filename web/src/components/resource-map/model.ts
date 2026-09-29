export type Resource = {
  id: string;
  name: string;
  service: string;
  description: string;
  operationKeys: string[];
  x: number;
  y: number;
  inferred: boolean;
};
export type Relation = {
  id: string;
  fromResourceId: string;
  toResourceId: string;
  label: string;
};
export type Operation = {
  key: string;
  method: string;
  path: string;
  summary: string;
  schemas: string[];
  sourcePointer?: string;
};
export type Diagnostic = {
  code: string;
  severity: string;
  message: string;
  resourceId?: string;
  operationKey?: string;
  relationId?: string;
};
export type Model = {
  resources: Resource[];
  operations: Operation[];
  relations: Relation[];
  diagnostics: Diagnostic[];
};
export type Command =
  | { kind: "auto_layout" }
  | { kind: "upsert_resource"; resource: Omit<Resource, "inferred"> }
  | { kind: "remove_resource"; resourceId: string }
  | { kind: "assign_operation"; operationKey: string; resourceId: string }
  | { kind: "upsert_relation"; relation: Relation }
  | { kind: "remove_relation"; relationId: string }
  | { kind: "move_resource"; resourceId: string; x: number; y: number };
export type ScenarioUsage = {
  scenarioId: number;
  scenarioName: string;
  revisionId: number;
  messageId: string;
  operationKey: string;
  contractRevisionId?: number | null;
  mode: string;
};
export type Preview = {
  document: string;
  model: Model;
  valid: boolean;
  diagnostics: { pointer: string; severity: string; message: string }[];
};
export type SavedMap = {
  designId: number;
  version: number;
  revisionId: number;
  model: Model;
  scenarioUsages: ScenarioUsage[];
  usagesTruncated: boolean;
};
export function editableResource(resource: Resource): Omit<Resource, "inferred"> {
  const { inferred: _inferred, ...editable } = resource;
  void _inferred;
  return editable;
}

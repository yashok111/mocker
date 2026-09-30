import type {
  ResponseRuleResultCondition,
  ResponseRuleResultSource,
} from "@/api/generated/schemas";
import { resultConditionOpNames } from "./model";

export type ResultLeaf = Extract<ResponseRuleResultCondition, { source: ResponseRuleResultSource }>;
// Generated schemas unroll the four levels. The recursive editor uses the same
// leaves and enforces that bound before constructing a generated root value.
export type EditableResultCondition =
  | ResultLeaf
  | { all: EditableResultCondition[] }
  | { any: EditableResultCondition[] };
export function conditionChildren(
  value: EditableResultCondition,
): EditableResultCondition[] | undefined {
  return "all" in value ? value.all : "any" in value ? value.any : undefined;
}
export function conditionLeaves(value: EditableResultCondition): ResultLeaf[] {
  const children = conditionChildren(value);
  return children ? children.flatMap(conditionLeaves) : [value as ResultLeaf];
}
export function conditionDepth(value: EditableResultCondition): number {
  const children = conditionChildren(value);
  return children ? 1 + Math.max(...children.map(conditionDepth)) : 1;
}
export function conditionReferences(value: EditableResultCondition): ResponseRuleResultSource[] {
  return conditionLeaves(value).flatMap((leaf) => [
    leaf.source,
    ...("valueFrom" in leaf ? [leaf.valueFrom] : []),
  ]);
}
export function conditionSummary(value: EditableResultCondition): string {
  const children = conditionChildren(value);
  if (children) return `(${children.map(conditionSummary).join("all" in value ? " И " : " ИЛИ ")})`;
  const leaf = value as ResultLeaf;
  const reference = (source: ResponseRuleResultSource) =>
    `${source.nodeId}${source.pointer ? ` ${source.pointer}` : " · весь JSON"}`;
  return `Результат: ${reference(leaf.source)}\n${resultConditionOpNames[leaf.op]}${"valueJSON" in leaf ? ` ${leaf.valueJSON}` : "valueFrom" in leaf ? ` результат ${reference(leaf.valueFrom)}` : ""}`;
}

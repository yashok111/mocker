import type { SchemaModelCommand, SchemaModelSchema } from "@/api/generated/schemas";
import { hasUnsafeJsonNumber } from "../api-designer/jsonNumberPrecision";

export const DRAFT_KEY = "/@schema-model";
export type Selection = { schema: string; property?: string; create?: boolean };
export type Form = {
  selection: Selection;
  name: string;
  json: string;
  edits: Record<string, string>;
  required: boolean;
  target: string;
  referenceChanged: boolean;
  array: boolean;
  x: string;
  y: string;
};
export function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
export function parseSchema(json: string): Record<string, unknown> | boolean {
  const value: unknown = JSON.parse(json);
  if (!record(value) && typeof value !== "boolean")
    throw new Error("Схема должна быть объектом или boolean.");
  if (hasUnsafeJsonNumber(json))
    throw new Error(
      "Число нельзя безопасно изменить в браузере. Используйте MCP для точных чисел.",
    );
  return value;
}
export function objectCompatible(json: string): boolean {
  try {
    const value = parseSchema(json);
    return record(value) && !value.$ref && (value.type === undefined || value.type === "object");
  } catch {
    return false;
  }
}
export function makeForm(selection: Selection, schema?: SchemaModelSchema): Form {
  const property = schema?.properties.find((p) => p.name === selection.property);
  const json = selection.create
    ? selection.property === undefined
      ? '{"type":"object","properties":{}}'
      : '{"type":"string"}'
    : (property?.schemaJSON ?? schema?.schemaJSON ?? "{}");
  let target = "";
  let array = property?.type === "array";
  try {
    const value = parseSchema(json);
    if (record(value)) {
      array = value.type === "array";
      const ref = array && record(value.items) ? value.items.$ref : value.$ref;
      // Only complete component references can be retargeted without dropping a
      // JSON Pointer suffix. Suffixed and external refs remain raw until chosen.
      if (typeof ref === "string") {
        const decoded = decodeURIComponent(ref);
        if (/^#\/components\/schemas\/[^/]+$/.test(decoded))
          target = decoded
            .slice("#/components/schemas/".length)
            .replaceAll("~1", "/")
            .replaceAll("~0", "~");
      }
    }
  } catch {
    /* Keep invalid/unsafe source in the raw field for correction. */
  }
  return {
    selection,
    name: selection.create ? "" : (selection.property ?? selection.schema),
    json,
    edits: {},
    required: property?.required ?? false,
    target,
    referenceChanged: false,
    array,
    x: String(schema?.x ?? 40),
    y: String(schema?.y ?? 40),
  };
}
export function formCommands(form: Form): SchemaModelCommand[] {
  const { selection: s } = form;
  const name = form.name.trim();
  if (!name) throw new Error("Укажите имя.");
  const value = parseSchema(form.json);
  if (Object.keys(form.edits).length && !record(value))
    throw new Error("Boolean-схема редактируется через JSON.");
  if (record(value))
    for (const [key, text] of Object.entries(form.edits)) {
      if (!text.trim()) {
        delete value[key];
        continue;
      }
      if (["enum", "example", "examples", "default"].includes(key)) {
        if (hasUnsafeJsonNumber(text))
          throw new Error("Число нельзя безопасно изменить в браузере.");
        const parsed: unknown = JSON.parse(text);
        if ((key === "enum" || key === "examples") && !Array.isArray(parsed))
          throw new Error(`${key}: укажите JSON-массив.`);
        value[key] = parsed;
      } else if (
        [
          "minimum",
          "maximum",
          "exclusiveMinimum",
          "exclusiveMaximum",
          "multipleOf",
          "minLength",
          "maxLength",
          "minItems",
          "maxItems",
        ].includes(key)
      ) {
        const number: unknown = JSON.parse(text);
        if (typeof number !== "number" || !Number.isFinite(number) || hasUnsafeJsonNumber(text))
          throw new Error(`${key}: укажите точное конечное число.`);
        if (
          (key.startsWith("minL") || key.startsWith("maxL") || key.endsWith("Items")) &&
          (!Number.isInteger(number) || number < 0)
        )
          throw new Error(`${key}: укажите целое неотрицательное число.`);
        if (key === "multipleOf" && number <= 0)
          throw new Error("multipleOf должно быть больше нуля.");
        value[key] = number;
      } else value[key] = text;
    }
  const schemaJSON = Object.keys(form.edits).length ? JSON.stringify(value) : form.json;
  const commands: SchemaModelCommand[] = [];
  if (s.property !== undefined) {
    commands.push({
      kind: "upsert_property",
      schemaName: s.schema,
      propertyName: s.create ? name : s.property,
      schemaJSON,
      required: form.required,
    });
    if (!s.create && name !== s.property)
      commands.push({
        kind: "rename_property",
        schemaName: s.schema,
        propertyName: s.property,
        newName: name,
      });
    if (form.referenceChanged && form.target)
      commands.push({
        kind: "set_reference",
        schemaName: s.schema,
        propertyName: name,
        targetSchema: form.target,
        array: form.array,
      });
  } else {
    commands.push({
      kind: s.create ? "create_schema" : "replace_schema",
      schemaName: s.create ? name : s.schema,
      schemaJSON,
    });
    if (!s.create && name !== s.schema)
      commands.push({ kind: "rename_schema", schemaName: s.schema, newName: name });
    const x = Number(form.x),
      y = Number(form.y);
    if (
      !form.x.trim() ||
      !form.y.trim() ||
      !Number.isFinite(x) ||
      !Number.isFinite(y) ||
      Math.abs(x) > 100000 ||
      Math.abs(y) > 100000
    )
      throw new Error("Координаты должны быть числами от −100000 до 100000.");
    commands.push({ kind: "move_schema", schemaName: name, x, y });
  }
  return commands;
}

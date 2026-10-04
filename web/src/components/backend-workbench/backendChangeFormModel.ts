import type { BackendChangeProposalCommand } from "@/api/generated/schemas";
import { backendChangeSchemas } from "./backendChangeSchema";
import type { ChangeJSON, ChangeObject, ChangeSchema } from "./backendChangeSchemaTypes";

export const isChangeObject = (value: ChangeJSON): value is ChangeObject =>
  value !== null && typeof value === "object" && !Array.isArray(value);
export function resolveChangeSchema(schema: ChangeSchema): ChangeSchema {
  if (!schema.$ref) return schema;
  const name = schema.$ref.split("/").at(-1)!;
  const result = backendChangeSchemas[name];
  if (!result) throw new Error(`Неизвестная схема ${name}`);
  return result;
}
const source6Only = new Set([
  "domain_entity",
  "dto",
  "api_schema",
  "representation_field",
  "representation_selector",
]);
export function changeSchemaForProfile(input: ChangeSchema, profile = "6"): ChangeSchema {
  const schema = resolveChangeSchema(input);
  if (profile !== "5") return schema;
  const filtered = { ...schema };
  if (schema.enum) filtered.enum = schema.enum.filter((value) => !source6Only.has(String(value)));
  for (const key of ["oneOf", "anyOf"] as const) {
    if (schema[key])
      filtered[key] = schema[key]!.filter(
        (branch) => !source6Only.has(String(resolveChangeSchema(branch).properties?.kind?.const)),
      );
  }
  return filtered;
}
export function changeValueKey(value: ChangeJSON): string {
  if (Array.isArray(value)) return `[${value.map(changeValueKey).join(",")}]`;
  if (isChangeObject(value))
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${changeValueKey(value[key]!)}`)
      .join(",")}}`;
  return JSON.stringify(value);
}
export function changeSchemaIssues(
  input: ChangeSchema,
  value: ChangeJSON,
  profile = "6",
): string[] {
  let remaining = 100_000;
  function check(
    inputSchema: ChangeSchema,
    current: ChangeJSON,
    path: string,
    depth: number,
  ): string[] {
    if (--remaining < 0 || depth > 48) return [`${path}: форма слишком сложна`];
    const schema = changeSchemaForProfile(inputSchema, profile);
    const errors: string[] = [];
    const fail = (text: string) => errors.push(`${path}: ${text}`);
    if ("const" in schema && changeValueKey(current) !== changeValueKey(schema.const!))
      fail("выберите фиксированное значение");
    if (
      schema.enum &&
      !schema.enum.some((item) => changeValueKey(item) === changeValueKey(current))
    )
      fail("выберите значение из списка");
    if (schema.type) {
      const allowed = Array.isArray(schema.type) ? schema.type : [schema.type];
      const valid = allowed.some((type) =>
        type === "null"
          ? current === null
          : type === "array"
            ? Array.isArray(current)
            : type === "object"
              ? isChangeObject(current)
              : type === "integer"
                ? typeof current === "number" && Number.isSafeInteger(current)
                : type === "number"
                  ? typeof current === "number" && Number.isFinite(current)
                  : typeof current === type,
      );
      if (!valid) return [...errors, `${path}: требуется ${allowed.join(" / ")}`];
    }
    for (const [mode, alternatives] of [
      ["oneOf", schema.oneOf],
      ["anyOf", schema.anyOf],
    ] as const) {
      if (!alternatives) continue;
      const results = alternatives.map((branch) => check(branch, current, path, depth + 1));
      const matched = results.filter((items) => items.length === 0).length;
      if (!matched || (mode === "oneOf" && matched !== 1)) {
        const closest = results.reduce((left, right) =>
          left.length <= right.length ? left : right,
        );
        errors.push(...closest.slice(0, 5));
        if (!closest.length) fail("варианты формы пересекаются");
      }
    }
    if (schema.not && check(schema.not, current, path, depth + 1).length === 0)
      fail("недопустимое сочетание полей");
    for (const part of schema.allOf ?? []) errors.push(...check(part, current, path, depth + 1));
    if (schema.if) {
      const condition = check(schema.if, current, path, depth + 1).length === 0;
      const branch = condition ? schema.then : schema.else;
      if (branch) errors.push(...check(branch, current, path, depth + 1));
    }
    if (typeof current === "string") {
      const length = [...current].length;
      if (length < (schema.minLength ?? 0)) fail("значение обязательно");
      if (schema.maxLength !== undefined && length > schema.maxLength)
        fail(`не более ${schema.maxLength} символов`);
      if (schema.pattern && !new RegExp(schema.pattern, "u").test(current))
        fail("неверный формат значения");
      if (
        schema.format === "uuid" &&
        !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(current)
      )
        fail("укажите точный UUID");
    }
    if (typeof current === "number") {
      if (
        !Number.isFinite(current) ||
        (!Number.isSafeInteger(current) && schema.type === "integer")
      )
        fail("число нельзя передать без потери точности");
      if (schema.minimum !== undefined && current < schema.minimum)
        fail(`минимум ${schema.minimum}`);
      if (schema.maximum !== undefined && current > schema.maximum)
        fail(`максимум ${schema.maximum}`);
    }
    if (Array.isArray(current)) {
      if (current.length < (schema.minItems ?? 0))
        fail(`добавьте хотя бы ${schema.minItems} элементов`);
      if (schema.maxItems !== undefined && current.length > schema.maxItems)
        fail(`не более ${schema.maxItems} элементов`);
      if (schema.uniqueItems && new Set(current.map(changeValueKey)).size !== current.length)
        fail("элементы должны различаться");
      if (schema.items)
        current.forEach((item, index) =>
          errors.push(...check(schema.items!, item, `${path}[${index + 1}]`, depth + 1)),
        );
    }
    if (isChangeObject(current)) {
      for (const name of schema.required ?? [])
        if (!Object.hasOwn(current, name)) fail(`заполните ${name}`);
      if (schema.minProperties !== undefined && Object.keys(current).length < schema.minProperties)
        fail(`добавьте хотя бы ${schema.minProperties} полей`);
      if (schema.maxProperties !== undefined && Object.keys(current).length > schema.maxProperties)
        fail(`не более ${schema.maxProperties} полей`);
      for (const [name, item] of Object.entries(current)) {
        if (schema.propertyNames)
          errors.push(...check(schema.propertyNames, name, `${path}.${name}`, depth + 1));
        const child =
          schema.properties && Object.hasOwn(schema.properties, name)
            ? schema.properties[name]
            : undefined;
        if (child) errors.push(...check(child, item, `${path}.${name}`, depth + 1));
        else if (schema.additionalProperties === false)
          fail(`поле ${name} не входит в этот вариант`);
        else if (typeof schema.additionalProperties === "object")
          errors.push(...check(schema.additionalProperties, item, `${path}.${name}`, depth + 1));
      }
    }
    return errors;
  }
  return [...new Set(check(input, value, "Команда", 0))].slice(0, 30);
}

export function changeSchemaDefault(input: ChangeSchema): ChangeJSON {
  const schema = resolveChangeSchema(input);
  if ("const" in schema) return structuredClone(schema.const!);
  if (schema.enum?.length) return structuredClone(schema.enum[0]!);
  const alternatives = schema.oneOf ?? schema.anyOf;
  if (alternatives) return changeSchemaDefault(alternatives[0]!);
  if (
    schema.type === "object" ||
    schema.properties ||
    typeof schema.additionalProperties === "object"
  ) {
    return Object.fromEntries(
      (schema.required ?? []).map((name) => [
        name,
        changeSchemaDefault(schema.properties?.[name] ?? { type: "string" }),
      ]),
    );
  }
  if (schema.type === "array")
    return Array.from({ length: Math.min(schema.minItems ?? 0, 100) }, () =>
      changeSchemaDefault(schema.items ?? { type: "string" }),
    );
  if (schema.type === "boolean") return false;
  if (schema.type === "integer" || schema.type === "number") return schema.minimum ?? 0;
  if (schema.type === "null" || (Array.isArray(schema.type) && schema.type.includes("null")))
    return null;
  return "";
}
export function changeVariantIndex(alternatives: ChangeSchema[], value: ChangeJSON): number {
  const valid = alternatives.findIndex((branch) => changeSchemaIssues(branch, value).length === 0);
  if (valid >= 0) return valid;
  if (isChangeObject(value)) {
    const tagged = alternatives.findIndex((input) => {
      const branch = resolveChangeSchema(input);
      const tags = Object.entries(branch.properties ?? {}).filter(([, field]) => "const" in field);
      return tags.length > 0 && tags.every(([key, field]) => value[key] === field.const);
    });
    if (tagged >= 0) return tagged;
  }
  return 0;
}
export function changeEditableSchema(input: ChangeSchema, value: ChangeJSON): ChangeSchema {
  const schema = resolveChangeSchema(input);
  const result: ChangeSchema = {
    ...schema,
    properties: { ...schema.properties },
    required: [...(schema.required ?? [])],
  };
  for (const condition of schema.allOf ?? []) {
    if (!condition.if) continue;
    const branch =
      changeSchemaIssues(condition.if, value).length === 0 ? condition.then : condition.else;
    if (!branch) continue;
    for (const [key, field] of Object.entries(branch.properties ?? {}))
      result.properties![key] = { ...result.properties?.[key], ...field };
    result.required!.push(...(branch.required ?? []));
    const forbidden = branch.not?.required;
    if (forbidden?.length === 1) delete result.properties![forbidden[0]!];
  }
  return result;
}
export function parseChangeCommand(
  value: ChangeObject,
  profile = "6",
): BackendChangeProposalCommand {
  const issues = changeSchemaIssues(
    backendChangeSchemas.BackendChangeProposalCommand!,
    value,
    profile,
  );
  if (issues.length) throw new Error(issues.join("; "));
  return value as BackendChangeProposalCommand;
}

export function completeChangeObject(input: ChangeSchema, value: ChangeObject): ChangeObject {
  const original = resolveChangeSchema(input);
  const schema = changeEditableSchema(original, value);
  const result = { ...value };
  for (const key of schema.required ?? []) {
    if (!Object.hasOwn(result, key))
      result[key] = changeSchemaDefault(schema.properties?.[key] ?? { type: "string" });
  }
  for (const key of Object.keys(original.properties ?? {})) {
    if (!Object.hasOwn(schema.properties ?? {}, key)) delete result[key];
  }
  return result;
}

export type ApiDocument = Record<string, unknown>;

export type DocumentSelection =
  | { kind: "operation"; path: string; method: string }
  | { kind: "schema"; name: string }
  | { kind: "document" };

export interface OperationLocation {
  path: string;
  method: string;
}

export type OperationRef = OperationLocation;
export type JsonPointer = string;

export const HTTP_METHODS = [
  "get",
  "post",
  "put",
  "patch",
  "delete",
  "head",
  "options",
  "trace",
] as const;

const METHOD_SET = new Set<string>(HTTP_METHODS);

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function escapeJsonPointerToken(token: string): string {
  return token.replaceAll("~", "~0").replaceAll("/", "~1");
}

export function unescapeJsonPointerToken(token: string): string {
  return token.replaceAll("~1", "/").replaceAll("~0", "~");
}

function pointerTokens(pointer: JsonPointer): string[] {
  if (pointer === "") return [];
  if (!pointer.startsWith("/")) throw new Error(`Некорректный JSON Pointer: ${pointer}`);
  return pointer.slice(1).split("/").map(unescapeJsonPointerToken);
}

export function getAtJsonPointer(root: unknown, pointer: JsonPointer): unknown {
  let current = root;
  for (const token of pointerTokens(pointer)) {
    if (Array.isArray(current)) {
      const index = Number(token);
      if (!Number.isInteger(index) || index < 0) return undefined;
      current = current[index];
    } else if (isRecord(current)) {
      current = current[token];
    } else {
      return undefined;
    }
  }
  return current;
}

function nextContainer(token: string): Record<string, unknown> | unknown[] {
  return /^(0|[1-9]\d*)$/.test(token) ? [] : {};
}

function immutableSet(current: unknown, tokens: string[], value: unknown): unknown {
  const [token, ...rest] = tokens;
  if (token === undefined) return value;

  if (Array.isArray(current)) {
    const index = Number(token);
    if (!Number.isInteger(index) || index < 0) {
      throw new Error(`Индекс массива в JSON Pointer должен быть целым: ${token}`);
    }
    const copy = [...current];
    const child = copy[index] ?? (rest[0] === undefined ? undefined : nextContainer(rest[0]));
    copy[index] = immutableSet(child, rest, value);
    return copy;
  }

  const object = isRecord(current) ? current : {};
  const child = object[token] ?? (rest[0] === undefined ? undefined : nextContainer(rest[0]));
  return { ...object, [token]: immutableSet(child, rest, value) };
}

export function setAtJsonPointer(
  document: ApiDocument,
  pointer: JsonPointer,
  value: unknown,
): ApiDocument {
  const result = immutableSet(document, pointerTokens(pointer), value);
  if (!isRecord(result)) throw new Error("Корень документа OpenAPI должен быть объектом");
  return result;
}

function immutableRemove(current: unknown, tokens: string[]): unknown {
  const [token, ...rest] = tokens;
  if (token === undefined) return undefined;
  if (Array.isArray(current)) {
    const index = Number(token);
    if (!Number.isInteger(index) || index < 0 || index >= current.length) return current;
    const copy = [...current];
    if (rest.length === 0) copy.splice(index, 1);
    else copy[index] = immutableRemove(copy[index], rest);
    return copy;
  }
  if (!isRecord(current) || !(token in current)) return current;
  const copy = { ...current };
  if (rest.length === 0) delete copy[token];
  else copy[token] = immutableRemove(copy[token], rest);
  return copy;
}

export function removeAtJsonPointer(document: ApiDocument, pointer: JsonPointer): ApiDocument {
  const result = immutableRemove(document, pointerTokens(pointer));
  if (!isRecord(result)) throw new Error("Нельзя удалить корень документа OpenAPI");
  return result;
}

export function operationPointer(location: OperationLocation): JsonPointer {
  return `/paths/${escapeJsonPointerToken(location.path)}/${location.method.toLowerCase()}`;
}

export function schemaPointer(name: string): JsonPointer {
  return `/components/schemas/${escapeJsonPointerToken(name)}`;
}

export function listOperations(document: ApiDocument): OperationRef[] {
  const paths = document.paths;
  if (!isRecord(paths)) return [];
  const operations: OperationRef[] = [];
  for (const [path, pathItem] of Object.entries(paths)) {
    if (!isRecord(pathItem)) continue;
    for (const method of Object.keys(pathItem)) {
      const normalized = method.toLowerCase();
      if (METHOD_SET.has(normalized) && isRecord(pathItem[method])) {
        operations.push({ path, method: normalized });
      }
    }
  }
  return operations;
}

export function getOperation(
  document: ApiDocument,
  location: OperationLocation,
): Record<string, unknown> | undefined {
  const value = getAtJsonPointer(document, operationPointer(location));
  return isRecord(value) ? value : undefined;
}

export function updateOperation(
  document: ApiDocument,
  location: OperationLocation,
  update: (operation: Record<string, unknown>) => Record<string, unknown>,
): ApiDocument {
  const operation = getOperation(document, location);
  if (!operation) {
    throw new Error(`Операция ${location.method.toUpperCase()} ${location.path} не найдена`);
  }
  return setAtJsonPointer(document, operationPointer(location), update(operation));
}

function assertOperationTargetFree(document: ApiDocument, target: OperationLocation): void {
  if (getOperation(document, target)) {
    throw new Error(`Операция ${target.method.toUpperCase()} ${target.path} уже существует`);
  }
}

function pathTemplateParameters(path: string): string[] {
  const names: string[] = [];
  for (const match of path.matchAll(/\{([^{}]+)\}/g)) {
    const name = match[1];
    if (name !== undefined && !names.includes(name)) names.push(name);
  }
  return names;
}

function pathItemParameters(document: ApiDocument, path: string): Record<string, unknown>[] {
  const value = getAtJsonPointer(document, `/paths/${escapeJsonPointerToken(path)}/parameters`);
  return Array.isArray(value) ? value.filter(isRecord) : [];
}

function resolveParameter(
  document: ApiDocument,
  value: Record<string, unknown>,
): Record<string, unknown> {
  let parameter = value;
  const visited = new Set<string>();
  while (typeof parameter.$ref === "string" && parameter.$ref.startsWith("#/")) {
    if (visited.has(parameter.$ref)) break;
    visited.add(parameter.$ref);
    let pointer: string;
    try {
      pointer = decodeURIComponent(parameter.$ref.slice(1));
    } catch {
      break;
    }
    const resolved = getAtJsonPointer(document, pointer);
    if (!isRecord(resolved)) break;
    parameter = resolved;
  }
  return parameter;
}

function parameterKey(document: ApiDocument, value: unknown): string | undefined {
  if (!isRecord(value)) return undefined;
  const parameter = resolveParameter(document, value);
  return typeof parameter.in === "string" && typeof parameter.name === "string"
    ? `${parameter.in}:${parameter.name}`
    : typeof value.$ref === "string"
      ? `$ref:${value.$ref}`
      : undefined;
}

function alignOperationPathParameters(
  document: ApiDocument,
  operation: Record<string, unknown>,
  sourcePath: string | undefined,
  targetPath: string,
): Record<string, unknown> {
  if (sourcePath === targetPath) return operation;
  const sourceNames = sourcePath === undefined ? [] : pathTemplateParameters(sourcePath);
  const targetNames = pathTemplateParameters(targetPath);
  const explicit = Array.isArray(operation.parameters) ? operation.parameters : [];
  const overridden = new Set(explicit.map((parameter) => parameterKey(document, parameter)));
  // Materialize inherited parameters at operation level so moving to another
  // Path Item preserves the source contract and its operation-level overrides.
  const effective = [
    ...explicit,
    ...pathItemParameters(document, sourcePath ?? "").filter(
      (parameter) => !overridden.has(parameterKey(document, parameter)),
    ),
  ];
  const aligned = effective.flatMap((rawParameter) => {
    if (!isRecord(rawParameter)) return [rawParameter];
    const parameter = resolveParameter(document, rawParameter);
    if (parameter.in !== "path") return [rawParameter];
    const oldName = typeof parameter.name === "string" ? parameter.name : "";
    const sourceIndex = sourceNames.indexOf(oldName);
    const targetName =
      sourceIndex >= 0
        ? targetNames[sourceIndex]
        : targetNames.includes(oldName)
          ? oldName
          : undefined;
    if (targetName === undefined) return [];
    const result: Record<string, unknown> = {
      ...parameter,
      name: targetName,
      in: "path",
      required: true,
    };
    delete result.$ref;
    return [result];
  });
  const declared = new Set(aligned.map((parameter) => parameterKey(document, parameter)));
  const inheritedAtTarget = new Set(
    pathItemParameters(document, targetPath).map((parameter) => parameterKey(document, parameter)),
  );
  for (const name of targetNames) {
    if (declared.has(`path:${name}`) || inheritedAtTarget.has(`path:${name}`)) continue;
    aligned.push({ name, in: "path", required: true, schema: { type: "string" } });
  }
  const next = { ...operation };
  if (aligned.length === 0) delete next.parameters;
  else next.parameters = aligned;
  return next;
}

export function createOperation(
  document: ApiDocument,
  location: OperationLocation,
  operation: Record<string, unknown> = { responses: { default: { description: "Ответ" } } },
): ApiDocument {
  assertOperationTargetFree(document, location);
  return setAtJsonPointer(
    document,
    operationPointer(location),
    alignOperationPathParameters(document, operation, undefined, location.path),
  );
}

function cloneJson<T>(value: T): T {
  return structuredClone(value);
}

export function copyOperation(
  document: ApiDocument,
  source: OperationLocation,
  target: OperationLocation,
): ApiDocument {
  const operation = getOperation(document, source);
  if (!operation) {
    throw new Error(`Операция ${source.method.toUpperCase()} ${source.path} не найдена`);
  }
  assertOperationTargetFree(document, target);
  return setAtJsonPointer(
    document,
    operationPointer(target),
    alignOperationPathParameters(document, cloneJson(operation), source.path, target.path),
  );
}

function pathItemHasContent(document: ApiDocument, path: string): boolean {
  const item = getAtJsonPointer(document, `/paths/${escapeJsonPointerToken(path)}`);
  return isRecord(item) && Object.keys(item).length > 0;
}

export function deleteOperation(document: ApiDocument, location: OperationLocation): ApiDocument {
  if (!getOperation(document, location)) return document;
  let next = removeAtJsonPointer(document, operationPointer(location));
  if (!pathItemHasContent(next, location.path)) {
    next = removeAtJsonPointer(next, `/paths/${escapeJsonPointerToken(location.path)}`);
  }
  return next;
}

export function renameOperation(
  document: ApiDocument,
  source: OperationLocation,
  target: OperationLocation,
): ApiDocument {
  if (source.path === target.path && source.method.toLowerCase() === target.method.toLowerCase()) {
    return document;
  }
  const operation = getOperation(document, source);
  if (!operation) {
    throw new Error(`Операция ${source.method.toUpperCase()} ${source.path} не найдена`);
  }
  assertOperationTargetFree(document, target);
  const aligned = alignOperationPathParameters(document, operation, source.path, target.path);
  const withoutSource = deleteOperation(document, source);
  return setAtJsonPointer(withoutSource, operationPointer(target), aligned);
}

export function listSchemas(document: ApiDocument): string[] {
  const schemas = getAtJsonPointer(document, "/components/schemas");
  return isRecord(schemas) ? Object.keys(schemas) : [];
}

export function getSchema(
  document: ApiDocument,
  name: string,
): Record<string, unknown> | undefined {
  const schema = getAtJsonPointer(document, schemaPointer(name));
  return isRecord(schema) ? schema : undefined;
}

export function updateSchema(
  document: ApiDocument,
  name: string,
  update: (schema: Record<string, unknown>) => Record<string, unknown>,
): ApiDocument {
  const schema = getSchema(document, name);
  if (!schema) throw new Error(`Схема "${name}" не найдена`);
  return setAtJsonPointer(document, schemaPointer(name), update(schema));
}

export function createSchema(
  document: ApiDocument,
  name: string,
  schema: Record<string, unknown> = { type: "object", properties: {} },
): ApiDocument {
  if (getAtJsonPointer(document, schemaPointer(name)) !== undefined)
    throw new Error(`Схема "${name}" уже существует`);
  return setAtJsonPointer(document, schemaPointer(name), schema);
}

export function deleteSchema(document: ApiDocument, name: string): ApiDocument {
  if (!getSchema(document, name)) return document;
  return removeAtJsonPointer(document, schemaPointer(name));
}

function localSchemaRef(name: string): string {
  return `#/components/schemas/${escapeJsonPointerToken(name)}`;
}

function schemaReferenceSuffix(value: unknown, name: string): string | undefined {
  if (typeof value !== "string" || !value.startsWith("#/")) return undefined;
  let pointer: string;
  try {
    pointer = decodeURIComponent(value.slice(1));
  } catch {
    return undefined;
  }
  const tokens = pointerTokens(pointer);
  if (tokens[0] !== "components" || tokens[1] !== "schemas" || tokens[2] !== name) return undefined;
  return tokens
    .slice(3)
    .map((token) => `/${escapeJsonPointerToken(token)}`)
    .join("");
}

function rewriteRef(value: unknown, from: string, to: string): unknown {
  if (Array.isArray(value)) {
    let changed = false;
    const items = value.map((item) => {
      const next = rewriteRef(item, from, to);
      changed ||= next !== item;
      return next;
    });
    return changed ? items : value;
  }
  if (!isRecord(value)) return value;
  let changed = false;
  const next: Record<string, unknown> = {};
  for (const [key, child] of Object.entries(value)) {
    const suffix = key === "$ref" ? schemaReferenceSuffix(child, from) : undefined;
    const replacement =
      suffix !== undefined ? `${localSchemaRef(to)}${suffix}` : rewriteRef(child, from, to);
    changed ||= replacement !== child;
    next[key] = replacement;
  }
  return changed ? next : value;
}

export function renameSchema(document: ApiDocument, from: string, to: string): ApiDocument {
  if (from === to) return document;
  const schema = getSchema(document, from);
  if (!schema) throw new Error(`Схема "${from}" не найдена`);
  if (getAtJsonPointer(document, schemaPointer(to)) !== undefined)
    throw new Error(`Схема "${to}" уже существует`);

  const moved = setAtJsonPointer(
    removeAtJsonPointer(document, schemaPointer(from)),
    schemaPointer(to),
    schema,
  );
  const rewritten = rewriteRef(moved, from, to);
  if (!isRecord(rewritten)) throw new Error("Корень документа OpenAPI должен быть объектом");
  return rewritten;
}

export function schemaUsage(document: ApiDocument, name: string): JsonPointer[] {
  const result: JsonPointer[] = [];
  const visit = (value: unknown, pointer: string): void => {
    if (Array.isArray(value)) {
      value.forEach((child, index) => visit(child, `${pointer}/${index}`));
      return;
    }
    if (!isRecord(value)) return;
    for (const [key, child] of Object.entries(value)) {
      const childPointer = `${pointer}/${escapeJsonPointerToken(key)}`;
      if (key === "$ref" && schemaReferenceSuffix(child, name) !== undefined)
        result.push(childPointer);
      else visit(child, childPointer);
    }
  };
  visit(document, "");
  return result;
}

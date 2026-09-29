import {
  HTTP_METHODS,
  isRecord,
  type ApiDocument,
  type OperationLocation,
} from "../api-designer/documentModel";

const OPERATION_KEY = "x-mocker-canvas-operation-id";
export const CANVAS_ALIAS_KEYS = "x-mocker-canvas-operation-ids";
const MAX_PATH_ITEM_DEPTH = 128;
const METHOD_SET = new Set<string>(HTTP_METHODS);

export interface CanvasOperation {
  location: OperationLocation;
  operation: Record<string, unknown>;
  pathMetadata: Record<string, unknown>;
  key: unknown;
  inherited: boolean;
}

/** Only local JSON Pointer references are followed; malformed tokens are rejected. */
export function canvasLocalReference(document: ApiDocument, reference: string): unknown {
  if (reference === "#") return document;
  if (!reference.startsWith("#/")) return undefined;
  let pointer: string;
  try {
    pointer = decodeURIComponent(reference.slice(1));
  } catch {
    return undefined;
  }
  if (!pointer.startsWith("/")) return undefined;
  let current: unknown = document;
  for (const escaped of pointer.slice(1).split("/")) {
    const token = escaped.replaceAll("~1", "/").replaceAll("~0", "~");
    if (Array.isArray(current)) {
      if (!/^[+-]?\d+$/.test(token)) return undefined;
      current = current[Number(token)];
    } else if (isRecord(current) && Object.hasOwn(current, token)) current = current[token];
    else return undefined;
  }
  return current;
}

function parameterIdentity(document: ApiDocument, parameter: unknown): string | undefined {
  let value: unknown = parameter;
  const visited = new Set<unknown>();
  for (let depth = 0; depth < MAX_PATH_ITEM_DEPTH && isRecord(value); depth++) {
    if (visited.has(value)) return undefined;
    visited.add(value);
    if (typeof value.$ref !== "string")
      return typeof value.in === "string" && typeof value.name === "string"
        ? JSON.stringify([value.in, value.name])
        : undefined;
    value = canvasLocalReference(document, value.$ref);
  }
  return undefined;
}

function mergedParameters(
  document: ApiDocument,
  items: Record<string, unknown>[],
  operation: Record<string, unknown>,
): unknown[] {
  const parameters: unknown[] = [];
  const positions = new Map<string, number>();
  for (const source of [...items.toReversed(), operation]) {
    if (!Array.isArray(source.parameters)) continue;
    for (const parameter of source.parameters) {
      const identity = parameterIdentity(document, parameter);
      const previous = identity === undefined ? undefined : positions.get(identity);
      if (previous === undefined) {
        if (identity !== undefined) positions.set(identity, parameters.length);
        parameters.push(structuredClone(parameter));
      } else parameters[previous] = structuredClone(parameter);
    }
  }
  return parameters;
}

function pathItemChain(
  document: ApiDocument,
  item: Record<string, unknown>,
): Record<string, unknown>[] {
  const chain: Record<string, unknown>[] = [];
  const visited = new Set<unknown>();
  let current: unknown = item;
  for (let depth = 0; depth < MAX_PATH_ITEM_DEPTH && isRecord(current); depth++) {
    if (visited.has(current)) break;
    if (
      depth > 0 &&
      Object.keys(current).some(
        (field) =>
          !METHOD_SET.has(field) &&
          !field.startsWith("x-") &&
          !["$ref", "summary", "description", "servers", "parameters"].includes(field),
      )
    )
      break;
    visited.add(current);
    chain.push(current);
    current =
      typeof current.$ref === "string" ? canvasLocalReference(document, current.$ref) : undefined;
  }
  return chain;
}

function projectMethod(
  document: ApiDocument,
  path: string,
  method: string,
  chain: Record<string, unknown>[],
): CanvasOperation | undefined {
  const index = chain.findIndex((item) => Object.hasOwn(item, method));
  if (index < 0) return undefined;
  const source = chain[index]![method];
  if (!isRecord(source)) return undefined;
  const operation = structuredClone(source);
  const parameters = mergedParameters(document, chain, source);
  if (parameters.length) operation.parameters = parameters;
  const pathMetadata: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
  for (const item of chain.toReversed()) {
    for (const [field, value] of Object.entries(item)) {
      if (METHOD_SET.has(field) || ["$ref", CANVAS_ALIAS_KEYS, "parameters"].includes(field))
        continue;
      pathMetadata[field] = structuredClone(value);
    }
  }
  const concrete = chain[0]!;
  const hasAliasContainer = Object.hasOwn(concrete, CANVAS_ALIAS_KEYS);
  const aliases = isRecord(concrete[CANVAS_ALIAS_KEYS]) ? concrete[CANVAS_ALIAS_KEYS] : null;
  const inherited = index > 0;
  return {
    location: { path, method },
    operation,
    pathMetadata,
    key:
      inherited && hasAliasContainer && aliases === null
        ? null
        : inherited && aliases !== null && Object.hasOwn(aliases, method)
          ? aliases[method]
          : source[OPERATION_KEY],
    inherited,
  };
}

/** Read-only effective operations at concrete consumer paths. Every result owns its data. */
export function listCanvasOperations(document: ApiDocument): CanvasOperation[] {
  if (!isRecord(document.paths)) return [];
  const operations: CanvasOperation[] = [];
  for (const [path, value] of Object.entries(document.paths)) {
    if (!isRecord(value)) continue;
    const chain = pathItemChain(document, value);
    for (const method of HTTP_METHODS) {
      const projected = projectMethod(document, path, method, chain);
      if (projected) operations.push(projected);
    }
  }
  return operations;
}

export function getCanvasOperation(
  document: ApiDocument,
  location: OperationLocation,
): CanvasOperation | undefined {
  if (!isRecord(document.paths)) return undefined;
  const pathItem = document.paths[location.path];
  if (!isRecord(pathItem)) return undefined;
  const method = location.method.toLowerCase();
  if (!METHOD_SET.has(method)) return undefined;
  return projectMethod(document, location.path, method, pathItemChain(document, pathItem));
}

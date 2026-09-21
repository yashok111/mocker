import {
  HTTP_METHODS,
  escapeJsonPointerToken,
  isRecord,
  operationPointer,
  type ApiDocument,
  type OperationLocation,
} from "../api-designer/documentModel";
import { OPERATION_KEY } from "./canvasModel";
import type { ConversionIssue } from "./canvasContractConversion";
import type { CanvasContract } from "./types";

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (isRecord(value))
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`)
      .join(",")}}`;
  return JSON.stringify(value) ?? "null";
}

export function mergeEntries(
  target: Record<string, unknown>,
  source: Record<string, unknown>,
  path: string,
  errors: ConversionIssue[],
  rowId?: string,
): void {
  for (const [key, value] of Object.entries(source)) {
    if (Object.hasOwn(target, key) && canonical(target[key]) !== canonical(value)) {
      errors.push({
        ...(rowId === undefined ? {} : { rowId }),
        message: `Несовместимые определения ${path}/${key}: исправьте исходные контракты или исключите один из вызовов`,
      });
    } else {
      Object.defineProperty(target, key, {
        value: structuredClone(value),
        enumerable: true,
        configurable: true,
        writable: true,
      });
    }
  }
}

export function mergeContractDocuments(
  sources: CanvasContract[],
  name: string,
  keepRootInheritance: boolean,
  errors: ConversionIssue[],
): ApiDocument {
  const first = sources[0];
  if (first === undefined)
    return { openapi: "3.1.0", info: { title: name, version: "1.0.0" }, paths: {} };
  const normalized = sources.map((source) => {
    const copy = structuredClone(source);
    inheritReusablePaths(copy.document);
    return copy;
  });
  const document = normalized[0]!.document;
  for (const source of normalized.slice(1)) {
    for (const [key, value] of Object.entries(source.document)) {
      if (["paths", "security", "servers"].includes(key)) continue;
      if (key === "info" && isRecord(value)) {
        const targetInfo = isRecord(document.info) ? document.info : {};
        const { title: _title, version: _version, ...metadata } = value;
        mergeEntries(targetInfo, metadata, "info", errors);
        document.info = targetInfo;
      } else if (key === "components" && isRecord(value)) {
        const components = isRecord(document.components) ? document.components : {};
        for (const [category, entries] of Object.entries(value)) {
          if (isRecord(entries) && !category.startsWith("x-")) {
            const targetEntries =
              Object.hasOwn(components, category) && isRecord(components[category])
                ? components[category]
                : {};
            mergeEntries(targetEntries, entries, `components/${category}`, errors);
            Object.defineProperty(components, category, {
              value: targetEntries,
              enumerable: true,
              configurable: true,
              writable: true,
            });
          } else mergeEntries(components, { [category]: entries }, "components", errors);
        }
        document.components = components;
      } else if (key === "tags" && Array.isArray(value)) {
        const tags = Array.isArray(document.tags) ? document.tags : [];
        for (const tag of value) {
          const previous = isRecord(tag)
            ? tags.find((candidate) => isRecord(candidate) && candidate.name === tag.name)
            : undefined;
          if (previous !== undefined && canonical(previous) !== canonical(tag))
            errors.push({
              message: `Несовместимые определения тега ${isRecord(tag) ? String(tag.name) : ""}`,
            });
          else if (previous === undefined) tags.push(structuredClone(tag));
        }
        document.tags = tags;
      } else if (key === "webhooks" && isRecord(value)) {
        const webhooks = isRecord(document.webhooks) ? document.webhooks : {};
        mergeEntries(webhooks, value, "webhooks", errors);
        document.webhooks = webhooks;
      } else mergeEntries(document, { [key]: value }, "OpenAPI", errors);
    }
  }
  document.info = { ...(isRecord(document.info) ? document.info : {}), title: name };
  document.paths = {};
  if (!keepRootInheritance) {
    delete document.security;
    delete document.servers;
  }
  return document;
}

export function resolveLocalReference(document: ApiDocument, reference: string): unknown {
  if (!reference.startsWith("#")) return undefined;
  let fragment: string;
  try {
    fragment = decodeURIComponent(reference.slice(1));
  } catch {
    return undefined;
  }
  if (fragment === "") return document;
  if (!fragment.startsWith("/")) {
    let found: unknown;
    let count = 0;
    walkDocument(document, (object) => {
      if (object.$anchor === fragment || object.$dynamicAnchor === fragment) {
        found = object;
        count++;
      }
    });
    return count === 1 ? found : undefined;
  }
  let current: unknown = document;
  for (const escaped of fragment.slice(1).split("/")) {
    if (/~(?![01])/.test(escaped)) return undefined;
    const token = escaped.replaceAll("~1", "/").replaceAll("~0", "~");
    if (Array.isArray(current))
      current = /^(?:0|[1-9]\d*)$/.test(token) ? current[Number(token)] : undefined;
    else if (isRecord(current) && Object.hasOwn(current, token)) current = current[token];
    else return undefined;
  }
  return current;
}

const MAP_FIELDS = new Set([
  "paths",
  "webhooks",
  "components",
  "schemas",
  "properties",
  "patternProperties",
  "$defs",
  "definitions",
  "dependentSchemas",
  "responses",
  "content",
  "links",
  "callbacks",
  "examples",
  "headers",
  "encoding",
  "securitySchemes",
  "requestBodies",
  "pathItems",
  "parameters",
]);

function walkDocument(
  value: unknown,
  visit: (object: Record<string, unknown>) => void,
  field = "",
): void {
  if (Array.isArray(value)) {
    for (const child of value) walkDocument(child, visit);
    return;
  }
  if (!isRecord(value)) return;
  visit(value);
  for (const [key, child] of Object.entries(value)) {
    // These fields carry user data, not OpenAPI references.
    if (
      !MAP_FIELDS.has(field) &&
      (["example", "value", "default", "enum", "const"].includes(key) ||
        key.startsWith("x-") ||
        (key === "examples" && Array.isArray(child)))
    )
      continue;
    walkDocument(child, visit, MAP_FIELDS.has(field) && field !== "components" ? "" : key);
  }
}

export function validateMergedReferences(document: ApiDocument, errors: ConversionIssue[]): void {
  const reported = new Set<string>();
  const components = isRecord(document.components) ? document.components : {};
  const schemes = isRecord(components.securitySchemes) ? components.securitySchemes : {};
  const operationIds = new Set<string>();
  const report = (message: string) => {
    if (!reported.has(message)) {
      reported.add(message);
      errors.push({ message });
    }
  };
  for (const { operation } of authoredOperations(document)) {
    if (typeof operation.operationId !== "string") continue;
    if (operationIds.has(operation.operationId))
      report(
        `Повторяющийся operationId ${operation.operationId}: исправьте его в исходном API или исключите вызов`,
      );
    operationIds.add(operation.operationId);
  }
  walkDocument(document, (object) => {
    for (const property of ["$ref", "$dynamicRef", "operationRef"]) {
      const reference = object[property];
      if (
        typeof reference === "string" &&
        reference.startsWith("#") &&
        resolveLocalReference(document, reference) === undefined
      ) {
        report(
          `Потерянная локальная ссылка ${reference}: включите связанную операцию или исправьте исходный контракт`,
        );
      }
    }
    if (Array.isArray(object.security)) {
      for (const requirement of object.security) {
        if (!isRecord(requirement)) continue;
        for (const scheme of Object.keys(requirement))
          if (!Object.hasOwn(schemes, scheme))
            report(`Схема авторизации ${scheme} отсутствует в components/securitySchemes`);
      }
    }
    // A Link's operationId points to an operation and must survive filtering.
    if (isRecord(object.links)) {
      for (const link of Object.values(object.links)) {
        if (
          isRecord(link) &&
          typeof link.operationId === "string" &&
          !operationIds.has(link.operationId)
        )
          report(`Связанная operationId ${link.operationId} отсутствует в выбранных операциях`);
      }
    }
  });
}

function withoutCanvasKeys(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(withoutCanvasKeys);
  if (!isRecord(value)) return value;
  return Object.fromEntries(
    Object.entries(value)
      .filter(([key]) => key !== OPERATION_KEY)
      .map(([key, child]) => [key, withoutCanvasKeys(child)]),
  );
}

interface AuthoredOperation {
  pointer: string;
  operation: Record<string, unknown>;
}

function authoredOperations(document: ApiDocument): AuthoredOperation[] {
  const operations: AuthoredOperation[] = [];
  const scanCallbacks = (value: unknown, pointer: string) => {
    if (!isRecord(value)) return;
    for (const [name, callback] of Object.entries(value))
      scanPaths(callback, `${pointer}/${escapeJsonPointerToken(name)}`, true);
  };
  const scanPathItem = (value: unknown, pointer: string) => {
    if (!isRecord(value)) return;
    for (const method of HTTP_METHODS) {
      const operation = value[method];
      if (!isRecord(operation)) continue;
      const operationPath = `${pointer}/${method}`;
      operations.push({ pointer: operationPath, operation });
      scanCallbacks(operation.callbacks, `${operationPath}/callbacks`);
    }
  };
  const scanPaths = (value: unknown, pointer: string, skipExtensions = false) => {
    if (!isRecord(value)) return;
    for (const [path, pathItem] of Object.entries(value)) {
      // Paths and Callback Objects allow extension payloads. Named webhook
      // and component maps may instead contain real entries named x-*.
      if (skipExtensions && path.startsWith("x-")) continue;
      scanPathItem(pathItem, `${pointer}/${escapeJsonPointerToken(path)}`);
    }
  };
  scanPaths(document.paths, "/paths", true);
  scanPaths(document.webhooks, "/webhooks");
  if (isRecord(document.components)) {
    scanPaths(document.components.pathItems, "/components/pathItems");
    scanCallbacks(document.components.callbacks, "/components/callbacks");
  }
  return operations;
}

interface OperationSelection {
  source: OperationLocation;
  target: OperationLocation;
}

// Existence is insufficient: another API can occupy the same pointer or operationId.
// Compare each carried reference with its origin after making inheritance explicit.
export function validateSourceReferences(
  contract: CanvasContract,
  selections: OperationSelection[],
  merged: ApiDocument,
  errors: ConversionIssue[],
): void {
  const source = structuredClone(contract.document);
  inheritReusablePaths(source);
  const paths = isRecord(source.paths) ? source.paths : {};
  for (const pathItem of Object.values(paths)) inheritPathItem(source, pathItem);
  const carriedPaths: Record<string, unknown> = {};
  for (const { source: location } of selections) {
    const pathItem = paths[location.path];
    if (!isRecord(pathItem)) continue;
    const existingPath = carriedPaths[location.path];
    const carried = isRecord(existingPath)
      ? existingPath
      : Object.fromEntries(
          Object.entries(pathItem).filter(
            ([key]) => !HTTP_METHODS.some((method) => method === key),
          ),
        );
    carried[location.method] = pathItem[location.method];
    carriedPaths[location.path] = carried;
  }
  const sourceOperations = authoredOperations(source);
  const mergedOperations = authoredOperations(merged);
  const expectedOperationPointer = (sourcePointer: string): string | undefined => {
    if (!sourcePointer.startsWith("/paths/")) return sourcePointer;
    for (const selection of selections) {
      const prefix = operationPointer(selection.source);
      if (sourcePointer === prefix || sourcePointer.startsWith(`${prefix}/`))
        return operationPointer(selection.target) + sourcePointer.slice(prefix.length);
    }
    return undefined;
  };
  const sameOperation = (
    original: AuthoredOperation | undefined,
    current: AuthoredOperation | undefined,
  ): boolean =>
    original !== undefined &&
    current !== undefined &&
    expectedOperationPointer(original.pointer) === current.pointer;
  const pathReferenceKeepsOrigin = (reference: string): boolean => {
    let pointer: string;
    try {
      pointer = decodeURIComponent(reference.slice(1));
    } catch {
      return false;
    }
    if (pointer !== "/paths" && !pointer.startsWith("/paths/")) return true;
    const related = sourceOperations.filter(
      ({ pointer: operation }) =>
        operation === pointer ||
        operation.startsWith(`${pointer}/`) ||
        pointer.startsWith(`${operation}/`),
    );
    if (related.length > 0)
      return related.every(({ pointer }) => expectedOperationPointer(pointer) === pointer);
    // Path-level metadata belongs to a retained source path; it cannot be supplied
    // solely by another contract that happens to expose the same URL.
    const pathPrefix = pointer.split("/").slice(0, 3).join("/");
    return sourceOperations.some(
      ({ pointer }) =>
        pointer.startsWith(`${pathPrefix}/`) && expectedOperationPointer(pointer) === pointer,
    );
  };
  const seen = new Set<string>();
  const check = (label: string, original: unknown, current: unknown, sameOrigin = false) => {
    if (seen.has(label)) return;
    seen.add(label);
    if (
      original === undefined ||
      current === undefined ||
      (!sameOrigin &&
        canonical(withoutCanvasKeys(original)) !== canonical(withoutCanvasKeys(current)))
    )
      errors.push({
        message: `Ссылка ${label} из «${contract.name}» изменила значение или потеряла цель; исправьте исходный контракт или выбор операций`,
      });
  };
  const visitSource = (object: Record<string, unknown>) => {
    for (const property of ["$ref", "$dynamicRef", "operationRef"]) {
      const reference = object[property];
      if (typeof reference === "string" && reference.startsWith("#")) {
        const visited = seen.has(reference);
        const original = resolveLocalReference(source, reference);
        const current = resolveLocalReference(merged, reference);
        if (!pathReferenceKeepsOrigin(reference)) {
          check(reference, original, undefined);
          continue;
        }
        const originalOperation = sourceOperations.find(({ operation }) => operation === original);
        const currentOperation = mergedOperations.find(({ operation }) => operation === current);
        // Whole-operation links retain identity across explicit preview edits. Subschema
        // references still require the exact source value to avoid retargeting schemas.
        if (originalOperation !== undefined) {
          const sameOrigin = sameOperation(originalOperation, currentOperation);
          check(reference, original, sameOrigin ? current : undefined, sameOrigin);
        } else check(reference, original, current);
        // A byte-identical alias can resolve differently after merging. Follow its
        // source targets too; the checked-reference set also terminates cycles.
        if (!visited && original !== undefined) walkDocument(original, visitSource);
      }
    }
    if (isRecord(object.links)) {
      for (const link of Object.values(object.links)) {
        if (isRecord(link) && typeof link.operationId === "string") {
          const originals = sourceOperations.filter(
            ({ operation }) => operation.operationId === link.operationId,
          );
          const currents = mergedOperations.filter(
            ({ operation }) => operation.operationId === link.operationId,
          );
          const sameOrigin =
            originals.length === 1 &&
            currents.length === 1 &&
            sameOperation(originals[0], currents[0]);
          check(
            `operationId ${link.operationId}`,
            originals[0],
            sameOrigin ? currents[0] : undefined,
            sameOrigin,
          );
        }
      }
    }
  };
  walkDocument({ ...source, paths: carriedPaths }, visitSource);
}

export function parameterValue(
  document: ApiDocument,
  parameter: unknown,
): Record<string, unknown> | undefined {
  const visited = new Set<Record<string, unknown>>();
  while (isRecord(parameter)) {
    if (visited.has(parameter)) return undefined;
    visited.add(parameter);
    if (typeof parameter.$ref !== "string") return parameter;
    parameter = resolveLocalReference(document, parameter.$ref);
  }
  return undefined;
}

export function inheritOperation(
  source: ApiDocument,
  pathItem: Record<string, unknown>,
  operation: Record<string, unknown>,
): void {
  if (!("security" in operation) && source.security !== undefined)
    operation.security = structuredClone(source.security);
  if (!("servers" in operation)) {
    const servers = pathItem.servers ?? source.servers;
    if (servers !== undefined) operation.servers = structuredClone(servers);
  }
  const parameters: unknown[] = Array.isArray(pathItem.parameters)
    ? structuredClone(pathItem.parameters)
    : [];
  for (const parameter of Array.isArray(operation.parameters) ? operation.parameters : []) {
    const value = parameterValue(source, parameter);
    const index =
      value === undefined ||
      typeof value.name !== "string" ||
      value.name === "" ||
      typeof value.in !== "string" ||
      value.in === ""
        ? -1
        : parameters.findIndex((candidate) => {
            const previous = parameterValue(source, candidate);
            return (
              previous !== undefined && previous.name === value.name && previous.in === value.in
            );
          });
    if (index < 0) parameters.push(parameter);
    else parameters[index] = parameter;
  }
  if (parameters.length > 0) operation.parameters = parameters;
  if (isRecord(operation.callbacks)) {
    for (const callback of Object.values(operation.callbacks)) inheritCallback(source, callback);
  }
}

function inheritCallback(source: ApiDocument, callback: unknown): void {
  if (!isRecord(callback)) return;
  for (const [expression, pathItem] of Object.entries(callback)) {
    if (!expression.startsWith("x-")) inheritPathItem(source, pathItem);
  }
}

function inheritPathItem(source: ApiDocument, pathItem: unknown): void {
  if (!isRecord(pathItem)) return;
  for (const method of HTTP_METHODS) {
    const operation = pathItem[method];
    if (isRecord(operation)) inheritOperation(source, pathItem, operation);
  }
}

function inheritReusablePaths(document: ApiDocument): void {
  if (isRecord(document.webhooks))
    for (const pathItem of Object.values(document.webhooks)) inheritPathItem(document, pathItem);
  const components = isRecord(document.components) ? document.components : {};
  if (isRecord(components.pathItems))
    for (const pathItem of Object.values(components.pathItems)) inheritPathItem(document, pathItem);
  if (isRecord(components.callbacks)) {
    for (const callback of Object.values(components.callbacks)) inheritCallback(document, callback);
  }
}

// P7b (decisions.md mocker-p7-api-design D12): the hand-rolled schema tree
// of the «Контракт» tab — a JSON Schema node rendered as a line (name, type,
// the marks a reader scans for) and its children indented under it. A
// `$ref` node shows the referenced COMPONENT'S NAME collapsed and expands
// from `components` only on click, so a self-referencing schema
// (`children: {items: {$ref: self}}`) renders without a stack overflow by
// construction: nothing recurses through a reference the reader did not
// open. No dependency: the owner chose this over swagger-ui-react (a
// megabyte, a foreign style, no room for the diff badges), and it closes a
// corner of the schema-tree debt CARVE-OUTS.md prices.
//
// §14's word rule: the tree says «схема», «тип», «обязательное»,
// «элементы», «варианты» — never "patch", "recipe", "matcher".

import type { ReactElement } from "react";
import { useState } from "react";
import { Badge, Code, Group, Stack, Text, UnstyledButton } from "@mantine/core";
import { IconChevronDown, IconChevronRight } from "@tabler/icons-react";

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** resolveLocalRef follows one `#/…` pointer into the document — the only
 * kind the server stores (customep.ValidateRefs refuses anything else) —
 * and answers undefined for one it cannot walk. */
export function resolveLocalRef(doc: unknown, ref: string): unknown {
  if (!ref.startsWith("#/")) {
    return undefined;
  }
  let cur: unknown = doc;
  for (const rawToken of ref.slice(2).split("/")) {
    const token = rawToken.replace(/~1/g, "/").replace(/~0/g, "~");
    if (isObject(cur)) {
      cur = cur[token];
    } else if (Array.isArray(cur)) {
      cur = cur[Number(token)];
    } else {
      return undefined;
    }
  }
  return cur;
}

/** The last segment of a pointer: `#/components/schemas/User` → `User`. */
export function refName(ref: string): string {
  const parts = ref.split("/");
  return parts[parts.length - 1] ?? ref;
}

function typeLabel(schema: Record<string, unknown>): string {
  const t = schema["type"];
  let label: string;
  if (Array.isArray(t)) {
    label = t.map(String).join(" | ");
  } else if (typeof t === "string") {
    label = t;
  } else if (isObject(schema["properties"])) {
    label = "object";
  } else if (schema["items"] !== undefined) {
    label = "array";
  } else if (Array.isArray(schema["enum"])) {
    label = "enum";
  } else {
    label = "любой";
  }
  const format = schema["format"];
  if (typeof format === "string") {
    label += ` (${format})`;
  }
  return label;
}

export interface SchemaTreeProps {
  /** The node to render — a schema object, a `$ref` node, or a boolean. */
  schema: unknown;
  /** The whole document, for `$ref` resolution. */
  doc: unknown;
  /** The property name this node sits under; empty for a root. */
  name?: string;
  /** Whether the parent lists this property as required. */
  required?: boolean;
  /** Nesting depth, for indentation only. */
  depth?: number;
}

/** SchemaTree renders one schema node and, for an object or an array, its
 * children. A `$ref` renders as a collapsed button carrying the component
 * name; the click resolves it through `doc` and renders the target with the
 * same component one level deeper. */
export function SchemaTree({
  schema,
  doc,
  name = "",
  required = false,
  depth = 0,
}: SchemaTreeProps): ReactElement {
  const [open, setOpen] = useState(false);
  const indent = { marginLeft: depth === 0 ? 0 : 16 };

  if (typeof schema === "boolean") {
    return (
      <Text size="sm" style={indent} data-testid="schema-node">
        {name !== "" ? <Code>{name}</Code> : null} {schema ? "любое значение" : "ничего"}
      </Text>
    );
  }
  if (!isObject(schema)) {
    return (
      <Text size="sm" c="dimmed" style={indent} data-testid="schema-node">
        {name !== "" ? <Code>{name}</Code> : null} схема не задана
      </Text>
    );
  }

  const ref = schema["$ref"];
  if (typeof ref === "string") {
    const target = open ? resolveLocalRef(doc, ref) : undefined;
    return (
      <Stack gap={2} style={indent}>
        <UnstyledButton
          onClick={() => setOpen((v) => !v)}
          data-testid={`schema-ref-${refName(ref)}`}
          aria-expanded={open}
        >
          <Group gap={6} wrap="nowrap">
            {open ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
            {name !== "" ? <Code>{name}</Code> : null}
            <Badge size="xs" variant="light" color="blue">
              {refName(ref)}
            </Badge>
            {required ? (
              <Text size="xs" c="dimmed">
                обязательное
              </Text>
            ) : null}
          </Group>
        </UnstyledButton>
        {open ? (
          target === undefined ? (
            <Text size="xs" c="red" ml={20} data-testid="schema-ref-missing">
              компонент {refName(ref)} не найден в документе
            </Text>
          ) : (
            <SchemaTree schema={target} doc={doc} depth={depth + 1} />
          )
        ) : null}
      </Stack>
    );
  }

  const requiredSet = new Set(
    Array.isArray(schema["required"]) ? schema["required"].map(String) : [],
  );
  const properties = isObject(schema["properties"]) ? schema["properties"] : undefined;
  const items = schema["items"];
  const enumValues = Array.isArray(schema["enum"]) ? schema["enum"] : undefined;
  const branches = (["oneOf", "anyOf", "allOf"] as const)
    .map((k) => ({ k, list: schema[k] }))
    .filter((b): b is { k: "oneOf" | "anyOf" | "allOf"; list: unknown[] } => Array.isArray(b.list));
  const nullable = schema["nullable"] === true || schema["x-nullable"] === true;
  const description = schema["description"];

  return (
    <Stack gap={2} style={indent}>
      <Group gap={6} wrap="nowrap" data-testid="schema-node">
        {name !== "" ? <Code>{name}</Code> : null}
        <Text size="sm">{typeLabel(schema)}</Text>
        {nullable ? (
          <Text size="xs" c="dimmed">
            или null
          </Text>
        ) : null}
        {required ? (
          <Text size="xs" c="dimmed">
            обязательное
          </Text>
        ) : null}
        {enumValues !== undefined ? (
          <Text size="xs" c="dimmed">
            одно из: {enumValues.map((v) => JSON.stringify(v)).join(", ")}
          </Text>
        ) : null}
        {typeof description === "string" && description !== "" ? (
          <Text size="xs" c="dimmed" lineClamp={1}>
            — {description}
          </Text>
        ) : null}
      </Group>
      {properties !== undefined
        ? Object.keys(properties)
            .sort()
            .map((prop) => (
              <SchemaTree
                key={prop}
                schema={properties[prop]}
                doc={doc}
                name={prop}
                required={requiredSet.has(prop)}
                depth={depth + 1}
              />
            ))
        : null}
      {items !== undefined ? (
        <SchemaTree schema={items} doc={doc} name="элементы" depth={depth + 1} />
      ) : null}
      {branches.map((b) => (
        <Stack key={b.k} gap={2} style={{ marginLeft: 16 }}>
          <Text size="xs" c="dimmed">
            {b.k === "allOf" ? "все из" : "варианты"}
          </Text>
          {b.list.map((branch, i) => (
            <SchemaTree key={i} schema={branch} doc={doc} depth={depth + 1} />
          ))}
        </Stack>
      ))}
    </Stack>
  );
}

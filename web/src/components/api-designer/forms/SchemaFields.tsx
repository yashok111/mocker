import type { ReactElement } from "react";
import { Group, NativeSelect, NumberInput, Stack, TextInput } from "@mantine/core";
import { isRecord } from "../documentModel";
import { JsonValueEditor } from "./JsonValueEditor";

const TYPES = ["", "object", "array", "string", "integer", "number", "boolean"];

function updateField(
  schema: Record<string, unknown>,
  field: string,
  value: unknown,
): Record<string, unknown> {
  const next = { ...schema };
  if (value === "" || value === undefined) delete next[field];
  else next[field] = value;
  return next;
}

export function SchemaFields({
  schema,
  onChange,
  labelPrefix = "Схема",
  compact = false,
  pointer,
}: {
  schema: unknown;
  onChange: (schema: Record<string, unknown>) => void;
  labelPrefix?: string;
  compact?: boolean;
  pointer: string;
}): ReactElement {
  const value = isRecord(schema) ? schema : {};
  const type = typeof value.type === "string" ? value.type : "";

  return (
    <Stack gap="xs">
      <Group grow align="flex-start">
        <NativeSelect
          label={`${labelPrefix}: тип`}
          value={type}
          onChange={(event) => onChange(updateField(value, "type", event.currentTarget.value))}
        >
          {TYPES.map((option) => (
            <option key={option} value={option}>
              {option || "не задан"}
            </option>
          ))}
        </NativeSelect>
        <TextInput
          label={`${labelPrefix}: format`}
          value={typeof value.format === "string" ? value.format : ""}
          onChange={(event) => onChange(updateField(value, "format", event.currentTarget.value))}
        />
      </Group>
      <TextInput
        label={`${labelPrefix}: ссылка $ref`}
        placeholder="#/components/schemas/Order"
        value={typeof value.$ref === "string" ? value.$ref : ""}
        onChange={(event) => onChange(updateField(value, "$ref", event.currentTarget.value))}
      />
      {!compact && (
        <>
          <JsonValueEditor
            label={`${labelPrefix}: enum`}
            description={'Массив JSON, например [1, 2] или ["active", "closed"]'}
            value={value.enum ?? []}
            pointer={`${pointer}/enum`}
            minRows={2}
            validate={(entries) => (Array.isArray(entries) ? undefined : "Ожидается массив JSON")}
            onChange={(entries) => {
              if (Array.isArray(entries))
                onChange(updateField(value, "enum", entries.length === 0 ? undefined : entries));
            }}
          />
          <Group grow align="flex-start">
            <NumberInput
              label={`${labelPrefix}: минимум`}
              value={typeof value.minimum === "number" ? value.minimum : ""}
              onChange={(minimum) =>
                onChange(updateField(value, "minimum", minimum === "" ? undefined : minimum))
              }
            />
            <NumberInput
              label={`${labelPrefix}: максимум`}
              value={typeof value.maximum === "number" ? value.maximum : ""}
              onChange={(maximum) =>
                onChange(updateField(value, "maximum", maximum === "" ? undefined : maximum))
              }
            />
          </Group>
          <TextInput
            label={`${labelPrefix}: pattern`}
            value={typeof value.pattern === "string" ? value.pattern : ""}
            onChange={(event) => onChange(updateField(value, "pattern", event.currentTarget.value))}
          />
        </>
      )}
      {type === "array" && (
        <SchemaFields
          compact
          pointer={`${pointer}/items`}
          labelPrefix={`${labelPrefix}: элементы`}
          schema={value.items}
          onChange={(items) => onChange(updateField(value, "items", items))}
        />
      )}
    </Stack>
  );
}

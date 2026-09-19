import type { ReactElement } from "react";
import { Textarea } from "@mantine/core";
import { hasUnsafeJsonNumber } from "../jsonNumberPrecision";
import { useFormFieldDraft } from "./FormDraftContext";

function formatJson(value: unknown): string {
  return value === undefined ? "" : JSON.stringify(value, null, 2);
}

export function JsonValueEditor({
  value,
  onChange,
  label,
  description,
  minRows = 4,
  resetKey,
  pointer,
  validate,
}: {
  value: unknown;
  onChange: (value: unknown) => void;
  label: string;
  description?: string;
  minRows?: number;
  resetKey?: string;
  pointer: string;
  validate?: (value: unknown) => string | undefined;
}): ReactElement {
  const formatted = formatJson(value);
  const { store, draft } = useFormFieldDraft(pointer);

  return (
    <Textarea
      label={label}
      description={description}
      rows={minRows}
      value={draft?.source ?? formatted}
      error={draft?.error}
      data-field-key={resetKey}
      styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
      onChange={(event) => {
        const nextSource = event.currentTarget.value;
        if (nextSource.trim() === "") {
          store.set(pointer, {
            source: nextSource,
            propertySource: draft?.propertySource ?? formatted,
            error: "Введите JSON",
          });
          return;
        }
        try {
          const parsed: unknown = JSON.parse(nextSource);
          const error = hasUnsafeJsonNumber(nextSource)
            ? "Число нельзя сохранить в форме без потери точности. Используйте исходник документа."
            : validate?.(parsed);
          if (error) {
            store.set(pointer, {
              source: nextSource,
              propertySource: draft?.propertySource ?? formatted,
              error,
            });
            return;
          }
          onChange(parsed);
          store.remove(pointer);
        } catch {
          store.set(pointer, {
            source: nextSource,
            propertySource: draft?.propertySource ?? formatted,
            error: "Некорректный JSON",
          });
        }
      }}
    />
  );
}

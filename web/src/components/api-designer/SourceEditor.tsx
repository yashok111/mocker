import { lazy, Suspense, useEffect, useRef } from "react";
import type { ReactElement } from "react";
import { Center, Loader, Textarea } from "@mantine/core";
import { findJsonPointerRange } from "./monaco/jsonPointerRange";

const LazyMonacoEditor = lazy(() => import("./monaco/MonacoEditor"));
const LazyMonacoDiff = lazy(() => import("./monaco/MonacoDiff"));

export function SourceEditor({
  value,
  onChange,
  ariaLabel = "Исходник OpenAPI",
  focusPointer,
}: {
  value: string;
  onChange: (value: string) => void;
  ariaLabel?: string;
  focusPointer?: string;
}): ReactElement {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea || focusPointer === undefined) return;
    const range = findJsonPointerRange(textarea.value, focusPointer);
    if (!range) return;
    textarea.focus();
    textarea.setSelectionRange(range.startOffset, range.endOffset);
  }, [focusPointer]);
  if (typeof Worker === "undefined") {
    return (
      <Textarea
        ref={textareaRef}
        aria-label={ariaLabel}
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        minRows={22}
        styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
      />
    );
  }
  return (
    <Suspense fallback={<EditorLoader label="Загружаем редактор" />}>
      <LazyMonacoEditor
        value={value}
        onChange={onChange}
        ariaLabel={ariaLabel}
        focusPointer={focusPointer}
      />
    </Suspense>
  );
}

export function SourceDiff({
  original,
  modified,
  narrow,
  focusPointer,
}: {
  original: string;
  modified: string;
  narrow: boolean;
  focusPointer?: string;
}): ReactElement {
  if (typeof Worker === "undefined") {
    return (
      <div aria-label="Построчное сравнение OpenAPI">
        <Textarea
          label="Было"
          value={original}
          readOnly
          minRows={12}
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
        />
        <Textarea
          label="Стало"
          value={modified}
          readOnly
          minRows={12}
          mt="sm"
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
        />
      </div>
    );
  }
  return (
    <Suspense fallback={<EditorLoader label="Загружаем сравнение" />}>
      <LazyMonacoDiff
        key={focusPointer ?? "diff"}
        original={original}
        modified={modified}
        narrow={narrow}
        focusPointer={focusPointer}
      />
    </Suspense>
  );
}

function EditorLoader({ label }: { label: string }): ReactElement {
  return (
    <Center mih={420} aria-label={label}>
      <Loader size="sm" />
    </Center>
  );
}

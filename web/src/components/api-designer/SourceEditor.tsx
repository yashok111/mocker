import { lazy, Suspense, useEffect, useRef } from "react";
import type { ReactElement } from "react";
import { Center, Loader, Text, Textarea } from "@mantine/core";
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
  focusSide,
}: {
  original: string;
  modified: string;
  narrow: boolean;
  focusPointer?: string;
  focusSide?: "before" | "after";
}): ReactElement {
  const beforeRef = useRef<HTMLTextAreaElement>(null);
  const afterRef = useRef<HTMLTextAreaElement>(null);
  const missingPointer =
    focusSide !== undefined &&
    focusPointer !== undefined &&
    findJsonPointerRange(focusSide === "before" ? original : modified, focusPointer) === null;
  useEffect(() => {
    if (focusPointer === undefined) return;
    if (missingPointer) {
      beforeRef.current?.setSelectionRange(0, 0);
      afterRef.current?.setSelectionRange(0, 0);
      return;
    }
    const before = findJsonPointerRange(original, focusPointer);
    const after = findJsonPointerRange(modified, focusPointer);
    const useBefore = focusSide === "before" || (focusSide === undefined && after === null);
    const range = useBefore ? before : after;
    const textarea = useBefore ? beforeRef.current : afterRef.current;
    if (!range || !textarea) return;
    textarea.focus();
    textarea.setSelectionRange(range.startOffset, range.endOffset);
  }, [focusPointer, focusSide, original, modified, missingPointer]);
  if (typeof Worker === "undefined") {
    return (
      <div aria-label="Построчное сравнение OpenAPI">
        {missingPointer && (
          <Text component="output" size="sm" mb="sm">
            Элемент {focusPointer || "/"} отсутствует на стороне «
            {focusSide === "before" ? "Было" : "Стало"}».
          </Text>
        )}
        <Textarea
          ref={beforeRef}
          label="Было"
          value={original}
          readOnly
          minRows={12}
          styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }}
        />
        <Textarea
          ref={afterRef}
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
        key={`${focusSide ?? "auto"}:${focusPointer ?? "diff"}`}
        original={original}
        modified={modified}
        narrow={narrow}
        focusPointer={focusPointer}
        focusSide={focusSide}
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

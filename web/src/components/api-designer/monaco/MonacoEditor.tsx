import type { ReactElement } from "react";
import { useEffect, useRef } from "react";
import type { editor } from "monaco-editor";
import Editor from "@monaco-editor/react";
import { findJsonPointerRange } from "./jsonPointerRange";
import "./setup";

export default function MonacoEditor({
  value,
  onChange,
  ariaLabel,
  focusPointer,
}: {
  value: string;
  onChange: (value: string) => void;
  ariaLabel: string;
  focusPointer?: string;
}): ReactElement {
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null);
  useEffect(() => {
    revealPointer(editorRef.current, focusPointer);
  }, [focusPointer]);
  return (
    <Editor
      height="clamp(420px, calc(100dvh - 330px), 760px)"
      language="json"
      path="file:///draft/openapi.json"
      value={value}
      onMount={(instance) => {
        editorRef.current = instance;
        revealPointer(instance, focusPointer);
      }}
      onChange={(next) => onChange(next ?? "")}
      theme="vs"
      options={{
        ariaLabel,
        accessibilitySupport: "on",
        automaticLayout: true,
        minimap: { enabled: false },
        wordWrap: "on",
        tabSize: 2,
        insertSpaces: true,
        scrollBeyondLastLine: false,
        renderValidationDecorations: "on",
      }}
    />
  );
}

function revealPointer(instance: editor.IStandaloneCodeEditor | null, pointer?: string): void {
  const model = instance?.getModel();
  if (!instance || !model || pointer === undefined) return;
  const source = findJsonPointerRange(model.getValue(), pointer);
  if (!source) return;
  const start = model.getPositionAt(source.startOffset);
  const end = model.getPositionAt(source.endOffset);
  const range = {
    startLineNumber: start.lineNumber,
    startColumn: start.column,
    endLineNumber: end.lineNumber,
    endColumn: end.column,
  };
  instance.revealRangeInCenter(range);
  instance.setSelection(range);
  instance.focus();
}

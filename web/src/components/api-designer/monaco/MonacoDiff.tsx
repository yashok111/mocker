import { useEffect, useRef } from "react";
import type { ReactElement } from "react";
import type { editor } from "monaco-editor";
import { monaco } from "./setup";
import { findJsonPointerRange } from "./jsonPointerRange";

export default function MonacoDiff({
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
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<editor.IStandaloneDiffEditor | null>(null);
  const originalModelRef = useRef<editor.ITextModel | null>(null);
  const modifiedModelRef = useRef<editor.ITextModel | null>(null);
  const initialValues = useRef({ original, modified, narrow });

  useEffect(() => {
    const container = containerRef.current;
    if (container === null) return;

    const initial = initialValues.current;
    const originalModel = monaco.editor.createModel(initial.original, "json");
    const modifiedModel = monaco.editor.createModel(initial.modified, "json");
    const diffEditor = monaco.editor.createDiffEditor(container, {
      ariaLabel: "Построчное сравнение OpenAPI",
      accessibilitySupport: "auto",
      automaticLayout: true,
      renderSideBySide: !initial.narrow,
      renderSideBySideInlineBreakpoint: 700,
      useInlineViewWhenSpaceIsLimited: true,
      hideUnchangedRegions: {
        enabled: true,
        revealLineCount: 3,
        minimumLineCount: 4,
        contextLineCount: 3,
      },
      originalEditable: false,
      readOnly: true,
      minimap: { enabled: false },
      wordWrap: "on",
      scrollBeyondLastLine: false,
    });

    originalModelRef.current = originalModel;
    modifiedModelRef.current = modifiedModel;
    editorRef.current = diffEditor;
    diffEditor.setModel({ original: originalModel, modified: modifiedModel });

    return () => {
      editorRef.current = null;
      originalModelRef.current = null;
      modifiedModelRef.current = null;
      diffEditor.setModel(null);
      diffEditor.dispose();
      originalModel.dispose();
      modifiedModel.dispose();
    };
  }, []);

  useEffect(() => {
    const model = originalModelRef.current;
    if (model !== null && model.getValue() !== original) model.setValue(original);
  }, [original]);

  useEffect(() => {
    const model = modifiedModelRef.current;
    if (model !== null && model.getValue() !== modified) model.setValue(modified);
  }, [modified]);

  useEffect(() => {
    editorRef.current?.updateOptions({ renderSideBySide: !narrow });
  }, [narrow]);

  useEffect(() => {
    const instance = editorRef.current;
    if (instance === null || focusPointer === undefined) return;

    const modifiedRange = findJsonPointerRange(modified, focusPointer);
    const originalRange = findJsonPointerRange(original, focusPointer);
    const targetEditor = modifiedRange
      ? instance.getModifiedEditor()
      : instance.getOriginalEditor();
    const sourceRange = modifiedRange ?? originalRange;
    const model = targetEditor.getModel();
    if (sourceRange === null || model === null) return;

    const start = model.getPositionAt(sourceRange.startOffset);
    const end = model.getPositionAt(sourceRange.endOffset);
    const range = {
      startLineNumber: start.lineNumber,
      startColumn: start.column,
      endLineNumber: end.lineNumber,
      endColumn: end.column,
    };
    targetEditor.revealRangeInCenter(range);
    targetEditor.setSelection(range);
  }, [focusPointer, modified, original]);

  return (
    <div
      ref={containerRef}
      style={{
        height: "clamp(420px, calc(100dvh - 330px), 760px)",
        width: "100%",
      }}
    />
  );
}

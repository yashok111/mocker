import type { ReactElement } from "react";
import Editor from "@monaco-editor/react";
import "./setup";

export default function MonacoEditor({
  value,
  onChange,
  ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  ariaLabel: string;
}): ReactElement {
  return (
    <Editor
      height="clamp(420px, calc(100dvh - 330px), 760px)"
      language="json"
      path="file:///draft/openapi.json"
      value={value}
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

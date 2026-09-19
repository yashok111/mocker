import * as monaco from "monaco-editor";
import { loader } from "@monaco-editor/react";
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";
import JsonWorker from "monaco-editor/languages/features/json/json.worker.js?worker";

declare global {
  interface Window {
    MonacoEnvironment?: {
      getWorker(_moduleId: string, label: string): Worker;
    };
  }
}

window.MonacoEnvironment = {
  getWorker(_moduleId: string, label: string): Worker {
    return label === "json" ? new JsonWorker() : new EditorWorker();
  },
};

loader.config({ monaco });
monaco.json.jsonDefaults.setDiagnosticsOptions({
  validate: true,
  allowComments: false,
  trailingCommas: "error",
  schemaValidation: "warning",
});

export { monaco };

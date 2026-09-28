import type { ScenarioExportFormat } from "@/api/generated/schemas";

export const formatLabels: Record<ScenarioExportFormat | "svg" | "png" | "pdf" | "zip", string> = {
  plantuml: "PlantUML",
  mermaid: "Mermaid",
  "openapi-json": "OpenAPI JSON",
  "openapi-yaml": "OpenAPI YAML",
  "asyncapi-json": "AsyncAPI JSON",
  "asyncapi-yaml": "AsyncAPI YAML",
  postman: "Postman",
  curl: "cURL",
  markdown: "Markdown",
  html: "HTML",
  pdf: "PDF (печать)",
  svg: "SVG",
  png: "PNG",
  zip: "ZIP",
};

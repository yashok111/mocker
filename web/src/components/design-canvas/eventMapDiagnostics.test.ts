import { expect, it } from "vitest";
import type { DesignScenarioEventMapDiagnostic } from "@/api/generated/schemas";
import { diagnosticMarkers } from "./eventMapDiagnostics";

it("marks graph elements with their highest diagnostic severity", () => {
  const diagnostics: DesignScenarioEventMapDiagnostic[] = [
    {
      id: "info",
      code: "external",
      severity: "info",
      pointer: "/a",
      message: "",
      elementId: "node",
    },
    {
      id: "warning",
      code: "missing",
      severity: "warning",
      pointer: "/b",
      message: "",
      elementId: "node",
    },
    {
      id: "error",
      code: "broken",
      severity: "error",
      pointer: "/c",
      message: "",
      elementId: "edge",
    },
    { id: "unbound", code: "truncated", severity: "error", pointer: "/d", message: "" },
  ];
  const markers = diagnosticMarkers(diagnostics);
  expect(markers.get("node")).toBe("warning");
  expect(markers.get("edge")).toBe("error");
  expect(markers.size).toBe(2);
});

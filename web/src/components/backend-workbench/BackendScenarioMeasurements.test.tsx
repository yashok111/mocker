import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { MeasurementValues } from "./BackendScenarioMeasurements";
import type { BackendScenarioMeasurements } from "@/api/generated/schemas";
it("distinguishes unknown bytes from measured zero and exposes conditions", () => {
  const report = {
    input: { side: "before" },
    metrics: {
      sql_count: {
        unit: "count",
        value: "0",
        p95: null,
        sampleCount: 1,
        missingSamples: 0,
        limitations: [],
      },
      response_bytes: {
        unit: "bytes",
        value: null,
        p95: null,
        sampleCount: 0,
        missingSamples: 1,
        limitations: ["missing bytes"],
      },
    },
    conditions: [],
    sampledExecutions: 1,
    failedExecutions: 1,
    skippedExecutions: 0,
    unknownExecutions: 0,
  } as unknown as BackendScenarioMeasurements;
  render(
    <MantineProvider>
      <MeasurementValues report={report} />
    </MantineProvider>,
  );
  expect(screen.getByText("Неизвестно")).toBeInTheDocument();
  expect(screen.getByText("0")).toBeInTheDocument();
  expect(screen.getByText("missing bytes")).toBeInTheDocument();
  expect(screen.getByText(/Это не population error rate/)).toBeInTheDocument();
});
